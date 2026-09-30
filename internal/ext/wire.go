package ext

import "encoding/json"

// JSON-lines RPC over /agent/rpc. The host writes one request per line and
// the guest answers with one reply per line, echoing the id:
//
//	→ {"id":1,"op":"describe"}
//	← {"id":1,"tools":[{"name":"reverse",...}],"hooks":["tool_call"]}
//	→ {"id":2,"op":"call","name":"reverse","args":{"text":"abc"}}
//	← {"id":2,"output":"cba"}
//	→ {"id":3,"op":"hook","event":"tool_call","calls":[{"id":"c1","name":"bash","args":"..."}]}
//	← {"id":3,"decisions":[{"verdict":"block","reason":"no rm -rf"}]}
//	→ {"id":4,"op":"command","name":"model","input":"gpt-5"}
//	← {"id":4,"output":"model: gpt-5"}
//	→ {"id":5,"op":"command","name":"model"}
//	← {"id":5,"choices":["gpt-5","gpt-6-luna"],"selected":"gpt-6-luna"}
//
// Mid-request, the guest may ask the user; the host answers under the
// same id and keeps waiting for the reply:
//
//	← {"id":3,"ask":{"question":"Run unsandboxed?","choices":["yes","no"]}}
//	→ {"id":3,"op":"answer","input":"yes"}

const (
	opDescribe = "describe"
	opCall     = "call"
	opHook     = "hook"
	opCommand  = "command"
	opAnswer   = "answer"

	verdictBlock = "block"
	verdictGrant = "grant"

	eventToolCall   = "tool_call"
	eventToolResult = "tool_result"
)

type request struct {
	ID     int             `json:"id"`
	Op     string          `json:"op"`
	Name   string          `json:"name,omitempty"`
	Args   json.RawMessage `json:"args,omitempty"`
	Event  string          `json:"event,omitempty"`
	Call   *wireCall       `json:"call,omitempty"`
	Calls  []wireCall      `json:"calls,omitempty"`
	Output *string         `json:"output,omitempty"`
	Input  string          `json:"input,omitempty"`
}

type reply struct {
	ID        int            `json:"id"`
	Error     string         `json:"error,omitempty"`
	Tools     []wireTool     `json:"tools,omitempty"`
	Hooks     []string       `json:"hooks,omitempty"`
	Commands  []wireCommand  `json:"commands,omitempty"`
	Choices   []string       `json:"choices,omitempty"`
	Selected  string         `json:"selected,omitempty"`
	Output    *string        `json:"output,omitempty"`
	Decisions []wireDecision `json:"decisions,omitempty"`
	Ask       *wireAsk       `json:"ask,omitempty"`
}

// wireDecision is one call's verdict: "block", "grant" or empty (allow).
type wireDecision struct {
	Verdict string `json:"verdict,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type wireAsk struct {
	Question string   `json:"question"`
	Choices  []string `json:"choices"`
}

type wireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type wireCommand struct {
	Name        string `json:"name"`
	Prefix      string `json:"prefix,omitempty"`
	Description string `json:"description"`
}

type wireCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
}
