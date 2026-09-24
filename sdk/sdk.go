// Package sdk helps write ernest extensions in Go.
//
// An extension is a WASI command (GOOS=wasip1 GOARCH=wasm) that registers
// tools and hooks, then calls Serve:
//
//	func main() {
//		sdk.Tool("reverse", "Reverse text.", schema, reverse)
//		sdk.OnToolCall(guard)
//		sdk.Serve()
//	}
//
// Serve answers JSON-lines requests on /agent/rpc until the host closes it.
// The guest also sees /work (the workspace, copy-on-write), read-only
// /agent/history and /agent/config/model, which switches the model when
// written, and /agent/config/models, one available model per line.
package sdk

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	rpcPath = "/agent/rpc"

	opDescribe = "describe"
	opCall     = "call"
	opHook     = "hook"
	opCommand  = "command"

	eventToolCall   = "tool_call"
	eventToolResult = "tool_result"
)

// ToolFunc runs a tool with its raw JSON arguments.
type ToolFunc func(args json.RawMessage) (string, error)

// CommandFunc runs a slash command with the text after its name,
// e.g. "gpt-5" for "/model gpt-5".
type CommandFunc func(input string) (Result, error)

// Result is a command reply: Output to show, or Choices for the user to
// pick from. The pick re-runs the command with it as input. Selected
// preselects one choice.
type Result struct {
	Output   string
	Choices  []string
	Selected string
}

// Text is a Result that only shows s.
func Text(s string) Result { return Result{Output: s} }

// Call is a pending or finished tool call. Args is raw JSON.
type Call struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
}

// Verdict is a tool_call hook decision.
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

type tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	fn          ToolFunc
}

type command struct {
	Name        string `json:"name"`
	Prefix      string `json:"prefix,omitempty"`
	Description string `json:"description"`
	fn          CommandFunc
}

var (
	tools    []tool
	commands []command
	onCall   func(Call) Decision
	onResult func(Call, string) string
)

// Tool registers a tool. schema is a JSON schema for its arguments.
func Tool(name, description, schema string, fn ToolFunc) {
	tools = append(tools, tool{Name: name, Description: description, Parameters: json.RawMessage(schema), fn: fn})
}

// Command registers a slash command for the user, e.g. "/model".
func Command(name, description string, fn CommandFunc) {
	commands = append(commands, command{Name: name, Description: description, fn: fn})
}

// PrefixCommand registers a slash command that a prefix symbol also runs,
// e.g. "?" for "/ask": "?why" runs "/ask why".
func PrefixCommand(prefix, name, description string, fn CommandFunc) {
	commands = append(commands, command{Name: name, Prefix: prefix, Description: description, fn: fn})
}

// OnToolCall registers a hook run before every tool call.
func OnToolCall(fn func(Call) Decision) { onCall = fn }

// OnToolResult registers a hook that may rewrite every tool result.
func OnToolResult(fn func(c Call, out string) string) { onResult = fn }

type request struct {
	ID     int             `json:"id"`
	Op     string          `json:"op"`
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args"`
	Event  string          `json:"event"`
	Call   Call            `json:"call"`
	Output string          `json:"output"`
	Input  string          `json:"input"`
}

type reply struct {
	ID       int       `json:"id"`
	Error    string    `json:"error,omitempty"`
	Tools    []tool    `json:"tools,omitempty"`
	Hooks    []string  `json:"hooks,omitempty"`
	Commands []command `json:"commands,omitempty"`
	Choices  []string  `json:"choices,omitempty"`
	Selected string    `json:"selected,omitempty"`
	Output   *string   `json:"output,omitempty"`
	Block    bool      `json:"block,omitempty"`
	Reason   string    `json:"reason,omitempty"`
}

// Serve handles host requests until the host closes the pipe.
func Serve() error {
	f, err := os.OpenFile(rpcPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	rd := bufio.NewReader(f)
	enc := json.NewEncoder(f)
	for {
		line, err := rd.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}

		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		if err := enc.Encode(handle(req)); err != nil {
			return err
		}
	}
}

func handle(req request) reply {
	r := reply{ID: req.ID}

	switch req.Op {
	case opDescribe:
		r.Tools = tools
		r.Hooks = hooks()
		r.Commands = commands

	case opCall:
		out, err := call(req.Name, req.Args)
		if err != nil {
			r.Error = err.Error()
			return r
		}
		r.Output = &out

	case opHook:
		hook(req, &r)

	case opCommand:
		res, err := runCommand(req.Name, req.Input)
		if err != nil {
			r.Error = err.Error()
			return r
		}
		r.Choices, r.Selected = res.Choices, res.Selected
		if len(res.Choices) == 0 {
			r.Output = &res.Output
		}

	default:
		r.Error = "unknown op " + req.Op
	}
	return r
}

func hooks() []string {
	var hs []string
	if onCall != nil {
		hs = append(hs, eventToolCall)
	}
	if onResult != nil {
		hs = append(hs, eventToolResult)
	}
	return hs
}

func call(name string, args json.RawMessage) (string, error) {
	for _, t := range tools {
		if t.Name == name {
			return t.fn(args)
		}
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

func runCommand(name, input string) (Result, error) {
	for _, c := range commands {
		if c.Name == name {
			return c.fn(input)
		}
	}
	return Result{}, fmt.Errorf("unknown command %q", name)
}

func hook(req request, r *reply) {
	switch {
	case req.Event == eventToolCall && onCall != nil:
		d := onCall(req.Call)
		r.Block = d.Verdict == Block
		r.Reason = d.Reason

	case req.Event == eventToolResult && onResult != nil:
		out := onResult(req.Call, req.Output)
		r.Output = &out
	}
}
