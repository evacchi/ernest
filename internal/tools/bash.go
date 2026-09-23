package tools

import (
	"context"
	"encoding/json"
	"os/exec"
	"time"

	"github.com/evacchi/ernest/internal/llm"
)

const (
	shell          = "bash"
	defaultTimeout = 2 * time.Minute
	pipeGrace      = time.Second
)

const bashSchema = `{
  "type": "object",
  "properties": {
    "command": {"type": "string",  "description": "Shell command"},
    "timeout": {"type": "integer", "description": "Seconds, default 120"}
  },
  "required": ["command"]
}`

// Bash runs a shell command and returns combined stdout/stderr.
type Bash struct{}

type bashArgs struct {
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

func (Bash) Spec() llm.ToolSpec {
	return spec("bash", "Run a bash command in the working directory.", bashSchema)
}

func (Bash) Run(ctx context.Context, args json.RawMessage) (string, error) {
	in, err := decode[bashArgs](args)
	if err != nil {
		return "", err
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
	out, err := cmd.CombinedOutput()

	// A failing command is a result, not a tool error: the model needs the output.
	text := string(out)
	if err != nil {
		text += "\n[" + err.Error() + "]"
	}
	return truncate(text), nil
}
