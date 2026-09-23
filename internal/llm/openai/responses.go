package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/evacchi/ernest/internal/llm"
)

// Responses API wire names.
const (
	responsesPath = "/responses"

	itemMessage    = "message"
	itemCall       = "function_call"
	itemCallOutput = "function_call_output"
	itemReasoning  = "reasoning"
	partOutputText = "output_text"

	evTextDelta  = "response.output_text.delta"
	evItemDone   = "response.output_item.done"
	evCompleted  = "response.completed"
	evFailed     = "response.failed"
	evIncomplete = "response.incomplete"
	evError      = "error"

	// Stateless: reasoning comes back encrypted and is replayed by us.
	includeReasoning = "reasoning.encrypted_content"
)

// Responses is an llm.Provider backed by /v1/responses, OpenAI's API for
// reasoning models with tools. Requests are stateless (store=false);
// reasoning items are kept on the assistant message and replayed.
type Responses struct {
	base
}

// NewResponses returns a Responses client. Empty BaseURL means DefaultBaseURL.
func NewResponses(cfg Config) *Responses {
	return &Responses{base: newBase(cfg)}
}

// Stream sends req and assembles the streamed reply.
func (c *Responses) Stream(ctx context.Context, req llm.Request, onDelta func(string)) (llm.Message, error) {
	body, err := c.post(ctx, responsesPath, c.encode(req))
	if err != nil {
		return llm.Message{}, err
	}
	defer body.Close()
	return readResponse(body, onDelta)
}

// readResponse folds stream events into one assistant message:
//
//	response.output_text.delta   text chunk
//	response.output_item.done    finished function call or reasoning item
//	response.completed           end
func readResponse(r io.Reader, onDelta func(string)) (llm.Message, error) {
	msg := llm.Message{Role: llm.RoleAssistant}

	err := readSSE(r, func(data []byte) (bool, error) {
		var ev respEvent
		if err := json.Unmarshal(data, &ev); err != nil {
			return false, fmt.Errorf("openai: bad event: %w", err)
		}

		switch ev.Type {
		case evTextDelta:
			msg.Content += ev.Delta
			onDelta(ev.Delta)
		case evItemDone:
			return false, addItem(&msg, ev.Item)
		case evCompleted:
			return true, nil
		case evFailed, evIncomplete, evError:
			return false, ev.err()
		}
		return false, nil
	})
	if err != nil {
		return llm.Message{}, err
	}
	return msg, nil
}

// addItem keeps function calls and reasoning; message items are already
// covered by the text deltas.
func addItem(msg *llm.Message, raw json.RawMessage) error {
	var it respItem
	if err := json.Unmarshal(raw, &it); err != nil {
		return fmt.Errorf("openai: bad item: %w", err)
	}

	switch it.Type {
	case itemCall:
		msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{ID: it.CallID, Name: it.Name, Args: it.Arguments})
	case itemReasoning:
		msg.Replay = append(msg.Replay, raw)
	}
	return nil
}

func (c *Responses) encode(req llm.Request) respRequest {
	out := respRequest{
		Model:   c.Model(),
		Stream:  true,
		Include: []string{includeReasoning},
	}

	for _, m := range req.Messages {
		out.Input = append(out.Input, inputItems(m)...)
	}
	for _, t := range req.Tools {
		out.Tools = append(out.Tools, respTool{Type: toolType, Name: t.Name, Description: t.Description, Parameters: t.Params})
	}
	return out
}

// inputItems maps one message to Responses input items. An assistant turn
// becomes: replayed reasoning, its text, then its function calls.
func inputItems(m llm.Message) []any {
	switch m.Role {
	case llm.RoleTool:
		return []any{respCallOutput{Type: itemCallOutput, CallID: m.ToolCallID, Output: m.Content}}

	case llm.RoleAssistant:
		var items []any
		for _, r := range m.Replay {
			items = append(items, r)
		}
		if m.Content != "" {
			items = append(items, respAssistant{
				Type:    itemMessage,
				Role:    string(llm.RoleAssistant),
				Content: []respText{{Type: partOutputText, Text: m.Content}},
			})
		}
		for _, tc := range m.ToolCalls {
			items = append(items, respCall{Type: itemCall, CallID: tc.ID, Name: tc.Name, Arguments: tc.Args})
		}
		return items
	}

	return []any{respInput{Role: string(m.Role), Content: m.Content}}
}

type respRequest struct {
	Model   string     `json:"model"`
	Input   []any      `json:"input"`
	Tools   []respTool `json:"tools,omitempty"`
	Stream  bool       `json:"stream"`
	Store   bool       `json:"store"`
	Include []string   `json:"include,omitempty"`
}

type respTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type respInput struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type respAssistant struct {
	Type    string     `json:"type"`
	Role    string     `json:"role"`
	Content []respText `json:"content"`
}

type respText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type respCall struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type respCallOutput struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

type respItem struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type respEvent struct {
	Type     string          `json:"type"`
	Delta    string          `json:"delta"`
	Item     json.RawMessage `json:"item"`
	Message  string          `json:"message"`
	Response *struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	} `json:"response"`
}

// err describes a failed, incomplete or error event.
func (ev respEvent) err() error {
	switch {
	case ev.Response != nil && ev.Response.Error != nil:
		return fmt.Errorf("openai: %s", ev.Response.Error.Message)
	case ev.Response != nil && ev.Response.IncompleteDetails != nil:
		return fmt.Errorf("openai: incomplete: %s", ev.Response.IncompleteDetails.Reason)
	case ev.Message != "":
		return fmt.Errorf("openai: %s", ev.Message)
	}
	return fmt.Errorf("openai: %s", ev.Type)
}
