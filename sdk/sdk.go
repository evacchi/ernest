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
// The guest also sees /work (the workspace, copy-on-write) and read-only
// /agent/model and /agent/history.
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

	eventToolCall   = "tool_call"
	eventToolResult = "tool_result"
)

// ToolFunc runs a tool with its raw JSON arguments.
type ToolFunc func(args json.RawMessage) (string, error)

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

var (
	tools    []tool
	onCall   func(Call) Decision
	onResult func(Call, string) string
)

// Tool registers a tool. schema is a JSON schema for its arguments.
func Tool(name, description, schema string, fn ToolFunc) {
	tools = append(tools, tool{Name: name, Description: description, Parameters: json.RawMessage(schema), fn: fn})
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
}

type reply struct {
	ID     int      `json:"id"`
	Error  string   `json:"error,omitempty"`
	Tools  []tool   `json:"tools,omitempty"`
	Hooks  []string `json:"hooks,omitempty"`
	Output *string  `json:"output,omitempty"`
	Block  bool     `json:"block,omitempty"`
	Reason string   `json:"reason,omitempty"`
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

	case opCall:
		out, err := call(req.Name, req.Args)
		if err != nil {
			r.Error = err.Error()
			return r
		}
		r.Output = &out

	case opHook:
		hook(req, &r)

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
