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
	tools    map[string]Tool
	specs    []llm.ToolSpec
	hooks    []Hook
	emit     func(Event)

	mu      sync.Mutex
	history []llm.Message
}

// New returns an agent. emit may be nil. On duplicate tool names the first
// wins, so extensions cannot shadow built-ins.
func New(p llm.Provider, system string, tools []Tool, hooks []Hook, emit func(Event)) *Agent {
	if emit == nil {
		emit = func(Event) {}
	}

	a := &Agent{
		provider: p,
		tools:    make(map[string]Tool, len(tools)),
		hooks:    hooks,
		emit:     emit,
	}
	for _, t := range tools {
		spec := t.Spec()
		if _, dup := a.tools[spec.Name]; dup {
			continue
		}
		a.tools[spec.Name] = t
		a.specs = append(a.specs, spec)
	}

	if system != "" {
		a.history = []llm.Message{{Role: llm.RoleSystem, Content: system}}
	}
	return a
}

// History returns a copy of the conversation.
func (a *Agent) History() []llm.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.history)
}

// Prompt adds a user message and loops until the model stops calling tools.
func (a *Agent) Prompt(ctx context.Context, text string) error {
	a.append(llm.Message{Role: llm.RoleUser, Content: text})

	for range maxTurns {
		req := llm.Request{Messages: a.History(), Tools: a.specs}
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
			a.append(a.call(ctx, call))
		}

		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return errTooManyTurns
}

func (a *Agent) delta(s string) {
	a.emit(Event{Kind: EventDelta, Text: s})
}

func (a *Agent) append(m llm.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.history = append(a.history, m)
}

// call runs one tool call through the hooks and returns the tool message.
func (a *Agent) call(ctx context.Context, call llm.ToolCall) llm.Message {
	a.emit(Event{Kind: EventToolCall, Call: call})

	out, outcome := a.exec(ctx, call)
	if out == "" {
		out = noOutput
	}

	a.emit(Event{Kind: EventToolResult, Call: call, Text: out, Outcome: outcome})
	return llm.Message{Role: llm.RoleTool, ToolCallID: call.ID, Content: out}
}

// exec applies OnToolCall hooks, runs the tool, then OnToolResult hooks.
// Hook failures fail closed: the model sees the error, not the output.
func (a *Agent) exec(ctx context.Context, call llm.ToolCall) (string, Outcome) {
	if err := ctx.Err(); err != nil {
		return failed(err)
	}

	for _, h := range a.hooks {
		d, err := h.OnToolCall(ctx, call)
		if err != nil {
			return failed(fmt.Errorf("hook: %w", err))
		}
		if d.Verdict == Block {
			return "blocked: " + d.Reason, OutcomeBlocked
		}
	}

	out, err := a.run(ctx, call)
	if err != nil {
		return failed(err)
	}

	for _, h := range a.hooks {
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

func (a *Agent) run(ctx context.Context, call llm.ToolCall) (string, error) {
	tool, ok := a.tools[call.Name]
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
