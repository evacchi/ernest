package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/evacchi/ernest/internal/llm"
)

// scripted replies with a fixed sequence of messages.
type scripted struct {
	replies []llm.Message
	reqs    []llm.Request
}

func (s *scripted) Stream(_ context.Context, req llm.Request, onDelta func(string)) (llm.Message, error) {
	s.reqs = append(s.reqs, req)
	msg := s.replies[0]
	s.replies = s.replies[1:]
	onDelta(msg.Content)
	return msg, nil
}

type echo struct{}

func (echo) Spec() llm.ToolSpec { return llm.ToolSpec{Name: "echo"} }

func (echo) Run(_ context.Context, args json.RawMessage) (string, error) {
	return string(args), nil
}

// guard blocks calls whose args contain "rm" and uppercases results.
type guard struct{}

func (guard) OnToolCalls(_ context.Context, calls []llm.ToolCall) ([]Decision, error) {
	ds := make([]Decision, len(calls))
	for i, c := range calls {
		if strings.Contains(c.Args, "rm") {
			ds[i] = Decision{Verdict: Block, Reason: "no rm"}
		}
	}
	return ds, nil
}

func (guard) OnToolResult(_ context.Context, _ llm.ToolCall, out string) (string, error) {
	return strings.ToUpper(out), nil
}

// granter grants every call whose args contain "sudo".
type granter struct{}

func (granter) OnToolCalls(_ context.Context, calls []llm.ToolCall) ([]Decision, error) {
	ds := make([]Decision, len(calls))
	for i, c := range calls {
		if strings.Contains(c.Args, "sudo") {
			ds[i] = Decision{Verdict: Grant}
		}
	}
	return ds, nil
}

func (granter) OnToolResult(_ context.Context, _ llm.ToolCall, out string) (string, error) {
	return out, nil
}

// whoami reports whether its call was granted.
type whoami struct{}

func (whoami) Spec() llm.ToolSpec { return llm.ToolSpec{Name: "whoami"} }

func (whoami) Run(ctx context.Context, _ json.RawMessage) (string, error) {
	if Granted(ctx) {
		return "root", nil
	}
	return "user", nil
}

func toolTurn(calls ...llm.ToolCall) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: calls}
}

func TestPrompt(t *testing.T) {
	p := &scripted{replies: []llm.Message{
		toolTurn(
			llm.ToolCall{ID: "1", Name: "echo", Args: `{"s":"hi"}`},
			llm.ToolCall{ID: "2", Name: "echo", Args: `{"s":"rm"}`},
			llm.ToolCall{ID: "3", Name: "nope"},
		),
		{Role: llm.RoleAssistant, Content: "done"},
	}}

	var text strings.Builder
	emit := func(e Event) {
		if e.Kind == EventDelta {
			text.WriteString(e.Text)
		}
	}

	a := New(p, "sys", []Tool{echo{}}, []Hook{guard{}}, emit)
	if err := a.Prompt(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	if text.String() != "done" {
		t.Errorf("streamed = %q", text.String())
	}

	// system, user, assistant(calls), 3 tool results, assistant(done)
	h := a.History()
	if len(h) != 7 {
		t.Fatalf("history len = %d: %+v", len(h), h)
	}

	want := map[string]string{
		"1": `{"S":"HI"}`,
		"2": "blocked: no rm",
		"3": `error: unknown tool "nope"`,
	}
	for _, m := range h[3:6] {
		if m.Role != llm.RoleTool || m.Content != want[m.ToolCallID] {
			t.Errorf("tool msg %s = %q, want %q", m.ToolCallID, m.Content, want[m.ToolCallID])
		}
	}

	if len(p.reqs[1].Messages) != 6 {
		t.Errorf("second request sent %d messages", len(p.reqs[1].Messages))
	}
}

// Grant reaches the tool; Block from any hook wins over it.
func TestGrant(t *testing.T) {
	p := &scripted{replies: []llm.Message{
		toolTurn(
			llm.ToolCall{ID: "1", Name: "whoami", Args: `{}`},
			llm.ToolCall{ID: "2", Name: "whoami", Args: `{"s":"sudo"}`},
			llm.ToolCall{ID: "3", Name: "whoami", Args: `{"s":"sudo rm"}`},
		),
		{Role: llm.RoleAssistant, Content: "done"},
	}}

	a := New(p, "", []Tool{whoami{}}, []Hook{granter{}, guard{}}, nil)
	if err := a.Prompt(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{"1": "USER", "2": "ROOT", "3": "blocked: no rm"}
	for _, m := range a.History()[2:5] {
		if m.Content != want[m.ToolCallID] {
			t.Errorf("tool msg %s = %q, want %q", m.ToolCallID, m.Content, want[m.ToolCallID])
		}
	}
}

func TestSetTools(t *testing.T) {
	p := &scripted{replies: []llm.Message{
		toolTurn(llm.ToolCall{ID: "1", Name: "echo", Args: `{}`}),
		{Role: llm.RoleAssistant, Content: "done"},
	}}

	a := New(p, "", nil, nil, nil)
	a.SetTools([]Tool{echo{}}, nil)
	if err := a.Prompt(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}

	if len(p.reqs[0].Tools) != 1 {
		t.Errorf("request tools = %v", p.reqs[0].Tools)
	}
	if got := a.History()[2].Content; got != "{}" {
		t.Errorf("tool result = %q", got)
	}
}

// Note adds a user message without a model turn.
func TestNote(t *testing.T) {
	p := &scripted{}
	a := New(p, "", nil, nil, nil)
	a.Note("$ ls")

	h := a.History()
	if len(h) != 1 || h[0].Role != llm.RoleUser || h[0].Content != "$ ls" {
		t.Errorf("history = %+v", h)
	}
	if len(p.reqs) != 0 {
		t.Errorf("model called")
	}
}

func TestRecordRestore(t *testing.T) {
	p := &scripted{replies: []llm.Message{{Role: llm.RoleAssistant, Content: "hi"}}}
	a := New(p, "sys", nil, nil, nil)

	var saved []llm.Message
	a.Record(func(m llm.Message) { saved = append(saved, m) })
	if err := a.Prompt(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}

	// The system prompt is not recorded: it is rebuilt on start.
	if len(saved) != 2 || saved[0].Content != "hello" || saved[1].Content != "hi" {
		t.Fatalf("saved = %+v", saved)
	}

	b := New(p, "sys2", nil, nil, nil)
	b.Note("stale")
	b.Restore(saved)

	h := b.History()
	if len(h) != 3 || h[0].Content != "sys2" || h[1].Content != "hello" || h[2].Content != "hi" {
		t.Errorf("history = %+v", h)
	}
}

// Rewrite changes user text sent to the model, not the history.
func TestRewrite(t *testing.T) {
	p := &scripted{replies: []llm.Message{{Role: llm.RoleAssistant, Content: "ok"}}}
	a := New(p, "sys", nil, nil, nil)
	a.Rewrite(func(s string) (string, error) { return "<skill>\n" + s, nil })

	if err := a.Prompt(context.Background(), "$pdf go"); err != nil {
		t.Fatal(err)
	}

	sent := p.reqs[0].Messages
	if sent[0].Content != "sys" || sent[1].Content != "<skill>\n$pdf go" {
		t.Errorf("sent = %+v", sent)
	}
	if h := a.History(); h[1].Content != "$pdf go" {
		t.Errorf("history = %+v", h)
	}
}
