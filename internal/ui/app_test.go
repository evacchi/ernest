package ui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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

// A command's prefix runs it: "!ls -a" runs "sh" with "ls -a".
func TestCommandPrefix(t *testing.T) {
	var got string
	cmds := []Command{{Name: "sh", Prefix: "!", Run: func(_ context.Context, in string) (Result, error) {
		got = in
		return Result{}, nil
	}}}
	m := newModel(func() string { return "m" }, true, nil, cmds)

	m.input.SetValue("!ls -a")
	cmd := m.submit()
	if cmd == nil || m.running {
		t.Fatal("prefix did not start a command")
	}
	runAll(cmd)

	if got != "ls -a" {
		t.Errorf("input = %q", got)
	}
}

// runAll runs cmd and, recursively, the commands of a batch it returns.
func runAll(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		return
	}
	for _, c := range batch {
		runAll(c)
	}
}
