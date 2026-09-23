package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/evacchi/ernest/internal/llm"
)

// respBody streams a reasoning item, text in two deltas and one function
// call whose arguments arrive in pieces.
const respBody = `event: response.created
data: {"type":"response.created","response":{"id":"r1"}}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[],"encrypted_content":"ENC"}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":1,"delta":"Hel"}

event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":1,"delta":"lo"}

event: response.function_call_arguments.delta
data: {"type":"response.function_call_arguments.delta","output_index":2,"delta":"{\"pa"}

event: response.output_item.done
data: {"type":"response.output_item.done","output_index":2,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"read","arguments":"{\"path\":\"x\"}"}}

event: response.completed
data: {"type":"response.completed","response":{"id":"r1","status":"completed"}}

`

func TestResponsesStream(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != responsesPath {
			t.Errorf("path = %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		fmt.Fprint(w, respBody)
	}))
	defer srv.Close()

	c := NewResponses(Config{BaseURL: srv.URL, APIKey: "k", Model: "m"})
	req := llm.Request{
		Messages: []llm.Message{{Role: llm.RoleSystem, Content: "sys"}, {Role: llm.RoleUser, Content: "hi"}},
		Tools:    []llm.ToolSpec{{Name: "read", Description: "d", Params: json.RawMessage(`{"type":"object"}`)}},
	}

	var deltas []string
	msg, err := c.Stream(context.Background(), req, func(s string) { deltas = append(deltas, s) })
	if err != nil {
		t.Fatal(err)
	}

	if got["model"] != "m" || got["stream"] != true || got["store"] != false {
		t.Errorf("request = %v", got)
	}
	tools := got["tools"].([]any)
	if tool := tools[0].(map[string]any); tool["type"] != "function" || tool["name"] != "read" {
		t.Errorf("tool = %v", tool)
	}

	if strings.Join(deltas, "|") != "Hel|lo" || msg.Content != "Hello" {
		t.Errorf("deltas = %q, content = %q", deltas, msg.Content)
	}

	want := llm.ToolCall{ID: "call_1", Name: "read", Args: `{"path":"x"}`}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0] != want {
		t.Errorf("tool calls = %+v", msg.ToolCalls)
	}
	if len(msg.Replay) != 1 || !strings.Contains(string(msg.Replay[0]), "ENC") {
		t.Errorf("replay = %s", msg.Replay)
	}
}

// Follow-up turns replay reasoning, then the call, then its output.
func TestResponsesEncodeHistory(t *testing.T) {
	c := NewResponses(Config{Model: "m"})
	reasoning := json.RawMessage(`{"type":"reasoning","id":"rs_1","encrypted_content":"ENC"}`)

	body := c.encode(llm.Request{Messages: []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, Content: "ok", Replay: []json.RawMessage{reasoning},
			ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "read", Args: `{}`}}},
		{Role: llm.RoleTool, ToolCallID: "call_1", Content: "data"},
	}})

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}

	var decoded struct {
		Input []map[string]any `json:"input"`
	}
	json.Unmarshal(raw, &decoded)

	kinds := []string{}
	for _, it := range decoded.Input {
		k, _ := it["type"].(string)
		if k == "" || k == "message" {
			k = it["role"].(string)
		}
		kinds = append(kinds, k)
	}

	want := "system,user,reasoning,assistant,function_call,function_call_output"
	if strings.Join(kinds, ",") != want {
		t.Errorf("input kinds = %v, want %s\n%s", kinds, want, raw)
	}
}

func TestResponsesFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"boom\"}}}\n\n")
	}))
	defer srv.Close()

	c := NewResponses(Config{BaseURL: srv.URL})
	_, err := c.Stream(context.Background(), llm.Request{}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
}
