package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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

func TestParseSlash(t *testing.T) {
	tests := []struct {
		in, name, input string
		ok              bool
	}{
		{"/model", "model", "", true},
		{"/model  gpt-6-luna ", "model", "gpt-6-luna", true},
		{"/", "", "", false},
		{"hello /model", "", "", false},
	}
	for _, tt := range tests {
		name, input, ok := parseSlash(strings.TrimSpace(tt.in))
		if name != tt.name || input != tt.input || ok != tt.ok {
			t.Errorf("%q = (%q, %q, %v)", tt.in, name, input, ok)
		}
	}
}

func TestResolve(t *testing.T) {
	cmds := map[string]Command{"model": {}, "mode": {}, "reverse": {}}
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"model", "model", true},
		{"rev", "reverse", true},
		{"mod", "", false}, // ambiguous
		{"he", "help", true},
		{"x", "", false},
	}
	for _, tt := range tests {
		got, ok := resolve(cmds, tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("resolve(%q) = %q, %v", tt.in, got, ok)
		}
	}
}

func TestPicker(t *testing.T) {
	p := newPicker("model", []string{"gpt-5", "gpt-6-luna", "o3"}, "gpt-6-luna")
	if c, _ := p.choice(); c != "gpt-6-luna" {
		t.Fatalf("preselected = %q", c)
	}

	p.key(tea.KeyPressMsg{Code: tea.KeyDown})
	if c, _ := p.choice(); c != "o3" {
		t.Errorf("after down = %q", c)
	}

	// Typing filters and resets the cursor.
	p.key(tea.KeyPressMsg{Code: '5', Text: "5"})
	if c, _ := p.choice(); c != "gpt-5" || len(p.visible()) != 1 {
		t.Errorf("filtered = %q %v", c, p.visible())
	}
	if p.key(tea.KeyPressMsg{Code: tea.KeyEnter}) != pickDone {
		t.Error("enter did not pick")
	}
	if p.key(tea.KeyPressMsg{Code: tea.KeyEscape}) != pickCancel {
		t.Error("esc did not cancel")
	}
}

// The picker keeps one height while filtering: bubbletea's inline renderer
// leaves stale rows when a frame shrinks.
func TestPickerFixedHeight(t *testing.T) {
	r := newRenderer(true)
	items := make([]string, 30)
	for i := range items {
		items[i] = fmt.Sprintf("model-%02d", i)
	}
	p := newPicker("model", items, "")

	want := strings.Count(r.picker(p), "\n")
	for _, f := range []string{"1", "12", "zzz"} {
		p.filter = f
		if got := strings.Count(r.picker(p), "\n"); got != want {
			t.Errorf("filter %q: %d lines, want %d", f, got+1, want+1)
		}
	}
}
