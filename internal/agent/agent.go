// Package agent runs the model/tool loop.
//
//	user ─► Stream ─► assistant msg ─► no tool calls? done
//	                     │
//	                     └─► per call: OnToolCall ─► blocked? result = reason
//	                                    └─► Run ─► OnToolResult ─► tool msg ─► Stream ...
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/evacchi/ernest/internal/llm"
)

const (
	maxTurns    = 50
	noOutput    = "(no output)"
	errorPrefix = "error: "
)

var errTooManyTurns = fmt.Errorf("agent: exceeded %d turns", maxTurns)

// Tool is something the model can call.
type Tool interface {
	Spec() llm.ToolSpec
	Run(ctx context.Context, args json.RawMessage) (string, error)
}

// Verdict is a hook decision on a pending tool call.
type Verdict int

const (
	Allow Verdict = iota
	Block
)

// Decision is a Verdict plus the reason shown to the model when blocking.
type Decision struct {
	Verdict Verdict
	Reason  string
}

// Hook observes and may alter tool execution.
type Hook interface {
	OnToolCall(ctx context.Context, call llm.ToolCall) (Decision, error)
	OnToolResult(ctx context.Context, call llm.ToolCall, out string) (string, error)
}

// EventKind tells what an Event carries.
type EventKind int

const (
	EventDelta      EventKind = iota // Text: streamed assistant text chunk
	EventMessage                     // assistant message complete
	EventToolCall                    // Call: about to run
	EventToolResult                  // Call + Text + Outcome: result fed back to the model
)

// Outcome classifies a tool result.
type Outcome int

const (
	OutcomeOK Outcome = iota
	OutcomeError
	OutcomeBlocked
)

// Event reports loop progress to the UI.
type Event struct {
	Kind    EventKind
	Text    string
	Call    llm.ToolCall
	Outcome Outcome
}

// Agent holds one conversation.
type Agent struct {
	provider llm.Provider
	emit     func(Event)

	mu      sync.Mutex
	history []llm.Message
	ts      *toolset
	record  func(llm.Message) // sees every appended message; may be nil
}

// toolset is the tools and hooks one model turn works with.
type toolset struct {
	tools map[string]Tool
	specs []llm.ToolSpec
	hooks []Hook
}

// newToolset indexes tools by name. On duplicates the first wins, so
// extensions cannot shadow built-ins.
func newToolset(tools []Tool, hooks []Hook) *toolset {
	ts := &toolset{tools: make(map[string]Tool, len(tools)), hooks: hooks}
	for _, t := range tools {
		spec := t.Spec()
		if _, dup := ts.tools[spec.Name]; dup {
			continue
		}
		ts.tools[spec.Name] = t
		ts.specs = append(ts.specs, spec)
	}
	return ts
}

// New returns an agent. emit may be nil.
func New(p llm.Provider, system string, tools []Tool, hooks []Hook, emit func(Event)) *Agent {
	if emit == nil {
		emit = func(Event) {}
	}

	a := &Agent{provider: p, emit: emit, ts: newToolset(tools, hooks)}
	if system != "" {
		a.history = []llm.Message{{Role: llm.RoleSystem, Content: system}}
	}
	return a
}

// SetTools replaces tools and hooks, e.g. after reloading extensions.
// A running prompt picks them up on its next model turn.
func (a *Agent) SetTools(tools []Tool, hooks []Hook) {
	ts := newToolset(tools, hooks)

	a.mu.Lock()
	defer a.mu.Unlock()
	a.ts = ts
}

func (a *Agent) toolset() *toolset {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ts
}

// History returns a copy of the conversation.
func (a *Agent) History() []llm.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.history)
}

// Record calls fn with every message added from now on, e.g. to save
// the session. fn runs in order, under the history lock.
func (a *Agent) Record(fn func(llm.Message)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.record = fn
}

// Restore replaces the conversation with msgs, keeping the system prompt.
// Restored messages are not recorded: they are already saved.
func (a *Agent) Restore(msgs []llm.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.history = slices.DeleteFunc(a.history, func(m llm.Message) bool { return m.Role != llm.RoleSystem })
	for _, m := range msgs {
		if m.Role == llm.RoleSystem {
			continue
		}
		a.history = append(a.history, m)
	}
}

// Prompt adds a user message and loops until the model stops calling tools.
func (a *Agent) Prompt(ctx context.Context, text string) error {
	a.append(llm.Message{Role: llm.RoleUser, Content: text})

	for range maxTurns {
		ts := a.toolset()
		req := llm.Request{Messages: a.History(), Tools: ts.specs}
		msg, err := a.provider.Stream(ctx, req, a.delta)
		if err != nil {
			return err
		}
		a.append(msg)
		a.emit(Event{Kind: EventMessage, Text: msg.Content})

		if len(msg.ToolCalls) == 0 {
			return nil
		}

		// Every call gets an answer, even after cancellation, so the
		// history stays valid for the next prompt.
		for _, call := range msg.ToolCalls {
			a.append(a.call(ctx, ts, call))
		}

		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return errTooManyTurns
}

// Note adds a user message the model sees on the next prompt, without
// running a turn, e.g. the output of a user's "!ls".
func (a *Agent) Note(text string) {
	a.append(llm.Message{Role: llm.RoleUser, Content: text})
}

func (a *Agent) delta(s string) {
	a.emit(Event{Kind: EventDelta, Text: s})
}

func (a *Agent) append(m llm.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.history = append(a.history, m)
	if a.record != nil {
		a.record(m)
	}
}

// call runs one tool call through the hooks and returns the tool message.
func (a *Agent) call(ctx context.Context, ts *toolset, call llm.ToolCall) llm.Message {
	a.emit(Event{Kind: EventToolCall, Call: call})

	out, outcome := ts.exec(ctx, call)
	if out == "" {
		out = noOutput
	}

	a.emit(Event{Kind: EventToolResult, Call: call, Text: out, Outcome: outcome})
	return llm.Message{Role: llm.RoleTool, ToolCallID: call.ID, Content: out}
}

// exec applies OnToolCall hooks, runs the tool, then OnToolResult hooks.
// Hook failures fail closed: the model sees the error, not the output.
func (ts *toolset) exec(ctx context.Context, call llm.ToolCall) (string, Outcome) {
	if err := ctx.Err(); err != nil {
		return failed(err)
	}

	for _, h := range ts.hooks {
		d, err := h.OnToolCall(ctx, call)
		if err != nil {
			return failed(fmt.Errorf("hook: %w", err))
		}
		if d.Verdict == Block {
			return "blocked: " + d.Reason, OutcomeBlocked
		}
	}

	out, err := ts.run(ctx, call)
	if err != nil {
		return failed(err)
	}

	for _, h := range ts.hooks {
		out, err = h.OnToolResult(ctx, call, out)
		if err != nil {
			return failed(fmt.Errorf("hook: %w", err))
		}
	}
	return out, OutcomeOK
}

func failed(err error) (string, Outcome) {
	return errorPrefix + err.Error(), OutcomeError
}

func (ts *toolset) run(ctx context.Context, call llm.ToolCall) (string, error) {
	tool, ok := ts.tools[call.Name]
	if !ok {
		return "", fmt.Errorf("unknown tool %q", call.Name)
	}

	args := json.RawMessage(call.Args)
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	if !json.Valid(args) {
		return "", errors.New("arguments are not valid JSON")
	}
	return tool.Run(ctx, args)
}
