package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/evacchi/ernest/internal/agent"
	"github.com/evacchi/ernest/internal/llm"
)

// Rendered markdown must fit the width once the terminal expands tabs,
// and carry no right padding.
func TestMarkdownFitsWidth(t *testing.T) {
	r := newRenderer(true)
	r.setWidth(40)
	out := r.markdown("text\n\n```go\nfunc f() {\n\treturn\n}\n```\n")

	for _, l := range strings.Split(out, "\n") {
		plain := ansi.Strip(l)
		if strings.Contains(plain, "\t") {
			t.Errorf("tab left in %q", plain)
		}
		if strings.HasSuffix(plain, " ") {
			t.Errorf("right padding in %q", plain)
		}
		if w := ansi.StringWidth(l); w > r.width {
			t.Errorf("width %d > %d: %q", w, r.width, plain)
		}
	}
}

func TestResultLinesUnpadded(t *testing.T) {
	r := newRenderer(true)
	call := llm.ToolCall{Name: toolBash, Args: `{"command":"x"}`}
	out := r.result(call, "ok\n\n[exit status 1]", agent.OutcomeOK)

	for _, l := range strings.Split(ansi.Strip(out), "\n") {
		if strings.HasSuffix(l, " ") && strings.TrimSpace(l) != "│" {
			t.Errorf("padded line %q", l)
		}
	}
}
