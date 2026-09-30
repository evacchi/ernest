package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"time"

	"github.com/evacchi/ernest/internal/agent"
	"github.com/evacchi/ernest/internal/llm"
)

const (
	shell          = "bash"
	defaultTimeout = 2 * time.Minute
	pipeGrace      = time.Second

	bashDesc = "Run a bash command in the working directory. It runs sandboxed: " +
		"no network (DNS fails too), writes only under the working and temp dirs, .git read-only. " +
		"If a command needs more, set escalate: true; the user is asked to approve."
	sandboxHint = "\n[sandboxed: no network, limited writes; if that caused the failure, retry with escalate: true]"
)

var errNotGranted = errors.New("escalation not approved; run it sandboxed or ask the user")

const bashSchema = `{
  "type": "object",
  "properties": {
    "command": {"type": "string",  "description": "Shell command"},
    "timeout": {"type": "integer", "description": "Seconds, default 120"},
    "escalate": {"type": "boolean", "description": "Run outside the sandbox, e.g. for network access. Needs user approval"}
  },
  "required": ["command"]
}`

// Bash runs a shell command and returns combined stdout/stderr.
// A nil Sandbox runs it unconfined. Escalated commands skip the
// sandbox, and run only if hooks granted the call.
type Bash struct {
	Sandbox Sandbox
}

type bashArgs struct {
	Command  string `json:"command"`
	Timeout  int    `json:"timeout"`
	Escalate bool   `json:"escalate"`
}

func (Bash) Spec() llm.ToolSpec {
	return spec("bash", bashDesc, bashSchema)
}

func (b Bash) Run(ctx context.Context, args json.RawMessage) (string, error) {
	in, err := decode[bashArgs](args)
	if err != nil {
		return "", err
	}
	if in.Escalate && !agent.Granted(ctx) {
		return "", errNotGranted
	}

	timeout := defaultTimeout
	if in.Timeout > 0 {
		timeout = time.Duration(in.Timeout) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// WaitDelay stops Wait from hanging on children that keep pipes open.
	cmd := exec.CommandContext(ctx, shell, "-c", in.Command)
	cmd.WaitDelay = pipeGrace

	// Fail closed: a sandbox that cannot wrap the command must not be skipped.
	sandboxed := b.Sandbox != nil && !in.Escalate
	if sandboxed {
		if err := b.Sandbox.Wrap(cmd); err != nil {
			return "", err
		}
	}
	out, err := cmd.CombinedOutput()

	// A failing command is a result, not a tool error: the model needs the output.
	text := string(out)
	if err != nil {
		text += "\n[" + err.Error() + "]"
	}

	// The model cannot tell a sandbox denial from a real failure.
	// Appended after truncating, so long output cannot hide it.
	text = truncate(text)
	if err != nil && sandboxed {
		text += sandboxHint
	}
	return text, nil
}
