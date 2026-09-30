// Command approve is a sample ernest extension deciding escalated bash
// calls, i.e. those asking to leave the sandbox. It asks the user:
//
//	Run unsandboxed: go get x?
//	  allow once · allow for session · deny
//
// "allow for session" approves that exact command until exit; dismissing
// the question denies.
//
//	GOOS=wasip1 GOARCH=wasm go build -o .ernest/extensions/approve.wasm ./examples/approve
//
//	/revoke   pick a session-approved command to remove
//
// The list lives in guest memory, out of the model's reach: its bash can
// write the workdir, so a file there could approve itself.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/evacchi/ernest/sdk"
)

const (
	toolBash    = "bash"
	noneAllowed = "no approved commands"
	question    = "Run unsandboxed: %s?"

	allowOnce    = "allow once"
	allowSession = "allow for session"
	deny         = "deny"
)

// approved is the session's allowlist, in approval order.
var approved []string

type bashArgs struct {
	Command  string `json:"command"`
	Escalate bool   `json:"escalate"`
}

func main() {
	sdk.Command("revoke", "Remove an approved command: /revoke [command]", revoke)
	sdk.OnToolCall(decide)

	if err := sdk.Serve(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// decide asks about escalated bash calls not approved for the session.
// Anything else is not ours to judge.
func decide(c sdk.Call) sdk.Decision {
	if c.Name != toolBash {
		return sdk.Decision{}
	}

	var in bashArgs
	if err := json.Unmarshal([]byte(c.Args), &in); err != nil || !in.Escalate {
		return sdk.Decision{}
	}

	grant := sdk.Decision{Verdict: sdk.Grant}
	if slices.Contains(approved, in.Command) {
		return grant
	}

	switch sdk.Ask(fmt.Sprintf(question, in.Command), allowOnce, allowSession, deny) {
	case allowSession:
		approved = append(approved, in.Command)
		return grant
	case allowOnce:
		return grant
	}
	return sdk.Decision{Verdict: sdk.Block, Reason: "denied by the user"}
}

// revoke without input offers the list; the pick re-runs it.
func revoke(input string) (sdk.Result, error) {
	cmd := strings.TrimSpace(input)
	if cmd == "" {
		if len(approved) == 0 {
			return sdk.Text(noneAllowed), nil
		}
		return sdk.Result{Choices: slices.Clone(approved)}, nil
	}

	i := slices.Index(approved, cmd)
	if i < 0 {
		return sdk.Result{}, fmt.Errorf("not approved: %s", cmd)
	}
	approved = slices.Delete(approved, i, i+1)
	return sdk.Text("revoked: " + cmd), nil
}
