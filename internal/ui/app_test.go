package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A running command shows a spinner line until its result arrives.
func TestCommandSpinner(t *testing.T) {
	cmds := []Command{{Name: "model", Run: func(context.Context, string) (Result, error) {
		return Result{Choices: []string{"a", "b"}}, nil
	}}}
	m := newModel(func() string { return "m" }, true, nil, cmds)

	m.input.SetValue("/model")
	if m.submit() == nil {
		t.Fatal("no command started")
	}

	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "running /model") {
		t.Errorf("no spinner while loading:\n%s", view)
	}

	m.Update(cmdMsg{name: "model", res: Result{Choices: []string{"a", "b"}}})
	view = ansi.Strip(m.View().Content)
	if strings.Contains(view, "running /model") || m.pick == nil {
		t.Errorf("spinner left or no picker:\n%s", view)
	}
}
