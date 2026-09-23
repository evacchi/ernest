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

// sseBody streams text in two chunks and one tool call with split arguments.
const sseBody = `data: {"choices":[{"delta":{"role":"assistant","content":"Hel"}}]}

data: {"choices":[{"delta":{"content":"lo"}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"pa"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"th\":\"x\"}"}}]}}]}

data: [DONE]

`

func TestStream(t *testing.T) {
	var got wireRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(headerAuth) != "Bearer k" {
			t.Errorf("auth header = %q", r.Header.Get(headerAuth))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		fmt.Fprint(w, sseBody)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, APIKey: "k", Model: "m"})
	req := llm.Request{
		Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
		Tools:    []llm.ToolSpec{{Name: "read", Params: json.RawMessage(`{}`)}},
	}

	var deltas []string
	msg, err := c.Stream(context.Background(), req, func(s string) { deltas = append(deltas, s) })
	if err != nil {
		t.Fatal(err)
	}

	if got.Model != "m" || !got.Stream || len(got.Tools) != 1 || got.Tools[0].Type != toolType {
		t.Errorf("request = %+v", got)
	}
	if strings.Join(deltas, "|") != "Hel|lo" {
		t.Errorf("deltas = %q", deltas)
	}
	if msg.Content != "Hello" {
		t.Errorf("content = %q", msg.Content)
	}

	want := llm.ToolCall{ID: "call_1", Name: "read", Args: `{"path":"x"}`}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0] != want {
		t.Errorf("tool calls = %+v", msg.ToolCalls)
	}
}

func TestStreamHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"bad key"}}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL})
	_, err := c.Stream(context.Background(), llm.Request{}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("err = %v", err)
	}
}

func TestModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != modelsPath {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[{"id":"gpt-6-luna"},{"id":"whisper-1"}]}`)
	}))
	defer srv.Close()

	ids, err := New(Config{BaseURL: srv.URL}).Models(context.Background())
	if err != nil || strings.Join(ids, ",") != "gpt-6-luna,whisper-1" {
		t.Fatalf("ids = %v, %v", ids, err)
	}
}
