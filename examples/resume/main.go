// Command resume is a sample ernest extension adding the /resume slash
// command. Like /model, it only reads and writes files under /agent;
// the host swaps the conversation.
//
//	GOOS=wasip1 GOARCH=wasm go build -o .ernest/extensions/resume.wasm ./examples/resume
//
//	/resume                    pick a saved session
//	/resume 20260924-103000    resume that session
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/evacchi/ernest/sdk"
)

const (
	sessions      = "/agent/sessions"
	configSession = "/agent/config/session"
)

var errNoSessions = errors.New("no saved sessions")

func main() {
	sdk.Command("resume", "Resume a saved session: /resume [id]", resume)

	if err := sdk.Serve(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// resume offers the saved sessions, or resumes the one picked. A pick is
// a whole line, "<id> <title>"; the id is its first field.
func resume(input string) (sdk.Result, error) {
	id, _, _ := strings.Cut(strings.TrimSpace(input), " ")
	if id != "" {
		if err := os.WriteFile(configSession, []byte(id+"\n"), 0o644); err != nil {
			return sdk.Result{}, err
		}
		return sdk.Text("resumed " + id), nil
	}

	b, err := os.ReadFile(sessions)
	if err != nil {
		return sdk.Result{}, err
	}

	var choices []string
	for line := range strings.Lines(string(b)) {
		if line = strings.TrimSpace(line); line != "" {
			choices = append(choices, line)
		}
	}
	if len(choices) == 0 {
		return sdk.Result{}, errNoSessions
	}
	return sdk.Result{Choices: choices}, nil
}
