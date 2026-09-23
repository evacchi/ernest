// Package llm defines provider-neutral chat types.
package llm

import (
	"context"
	"encoding/json"
)

// Role is the author of a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one entry of a conversation.
// Assistant messages may carry ToolCalls; tool messages answer one via ToolCallID.
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ToolCall is a model request to run a tool. Args is raw JSON.
type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Args string `json:"args"`
}

// ToolSpec describes a tool to the model. Params is a JSON schema.
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Params      json.RawMessage `json:"parameters"`
}

// Request is one completion request.
type Request struct {
	Messages []Message
	Tools    []ToolSpec
}

// Provider streams a completion. onDelta receives text chunks as they arrive;
// the returned message is the assembled result.
type Provider interface {
	Stream(ctx context.Context, req Request, onDelta func(string)) (Message, error)
}
