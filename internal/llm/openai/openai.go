// Package openai implements llm.Provider over the OpenAI Chat Completions API
// with server-sent events streaming.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/evacchi/ernest/internal/llm"
)

// DefaultBaseURL is the public OpenAI endpoint.
const DefaultBaseURL = "https://api.openai.com/v1"

const (
	completionsPath = "/chat/completions"
	toolType        = "function"

	headerAuth        = "Authorization"
	headerContentType = "Content-Type"
	headerAccept      = "Accept"
	mimeJSON          = "application/json"
	mimeEventStream   = "text/event-stream"

	ssePrefix  = "data:"
	sseDone    = "[DONE]"
	maxSSELine = 4 << 20
)

// Config selects endpoint, credentials and model.
type Config struct {
	BaseURL string
	APIKey  string
	Model   string
}

// Client is an llm.Provider backed by Chat Completions.
type Client struct {
	cfg  Config
	http *http.Client

	mu    sync.RWMutex
	model string
}

// New returns a client. Empty BaseURL means DefaultBaseURL.
func New(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	return &Client{cfg: cfg, http: &http.Client{}, model: cfg.Model}
}

// Model returns the model used for the next request.
func (c *Client) Model() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.model
}

// SetModel switches the model; in-flight requests keep the old one.
func (c *Client) SetModel(m string) error {
	m = strings.TrimSpace(m)
	if m == "" {
		return errors.New("openai: empty model name")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.model = m
	return nil
}

// Stream sends req and assembles the streamed reply.
func (c *Client) Stream(ctx context.Context, req llm.Request, onDelta func(string)) (llm.Message, error) {
	body, err := json.Marshal(c.encode(req))
	if err != nil {
		return llm.Message{}, err
	}

	url := strings.TrimSuffix(c.cfg.BaseURL, "/") + completionsPath
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return llm.Message{}, err
	}
	hreq.Header.Set(headerAuth, "Bearer "+c.cfg.APIKey)
	hreq.Header.Set(headerContentType, mimeJSON)
	hreq.Header.Set(headerAccept, mimeEventStream)

	resp, err := c.http.Do(hreq)
	if err != nil {
		return llm.Message{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return llm.Message{}, fmt.Errorf("openai: %s: %s", resp.Status, bytes.TrimSpace(raw))
	}
	return readStream(resp.Body, onDelta)
}

// readStream consumes SSE lines until [DONE], e.g.
//
//	data: {"choices":[{"delta":{"content":"Hel"}}]}
//	data: [DONE]
func readStream(r io.Reader, onDelta func(string)) (llm.Message, error) {
	var acc accumulator
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, maxSSELine)

	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), ssePrefix)
		if !ok {
			continue
		}

		data = strings.TrimSpace(data)
		if data == sseDone {
			return acc.message(), nil
		}

		var chunk wireChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return llm.Message{}, fmt.Errorf("openai: bad chunk: %w", err)
		}
		if chunk.Error != nil {
			return llm.Message{}, fmt.Errorf("openai: %s", chunk.Error.Message)
		}
		acc.add(chunk, onDelta)
	}

	if err := sc.Err(); err != nil {
		return llm.Message{}, err
	}
	return llm.Message{}, errors.New("openai: stream ended without [DONE]")
}

// accumulator merges deltas. Tool calls arrive split by index:
// the first chunk carries id and name, later ones append arguments.
type accumulator struct {
	text  strings.Builder
	calls []llm.ToolCall
}

func (a *accumulator) add(chunk wireChunk, onDelta func(string)) {
	for _, ch := range chunk.Choices {
		if ch.Delta.Content != "" {
			a.text.WriteString(ch.Delta.Content)
			onDelta(ch.Delta.Content)
		}

		for _, tc := range ch.Delta.ToolCalls {
			for len(a.calls) <= tc.Index {
				a.calls = append(a.calls, llm.ToolCall{})
			}
			call := &a.calls[tc.Index]
			if tc.ID != "" {
				call.ID = tc.ID
			}
			if tc.Function.Name != "" {
				call.Name = tc.Function.Name
			}
			call.Args += tc.Function.Arguments
		}
	}
}

func (a *accumulator) message() llm.Message {
	return llm.Message{
		Role:      llm.RoleAssistant,
		Content:   a.text.String(),
		ToolCalls: a.calls,
	}
}

func (c *Client) encode(req llm.Request) wireRequest {
	out := wireRequest{Model: c.Model(), Stream: true}

	for _, m := range req.Messages {
		wm := wireMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
		for _, tc := range m.ToolCalls {
			wm.ToolCalls = append(wm.ToolCalls, wireToolCall{
				ID:       tc.ID,
				Type:     toolType,
				Function: wireFunction{Name: tc.Name, Arguments: tc.Args},
			})
		}
		out.Messages = append(out.Messages, wm)
	}

	for _, t := range req.Tools {
		out.Tools = append(out.Tools, wireTool{
			Type:     toolType,
			Function: wireToolDef{Name: t.Name, Description: t.Description, Parameters: t.Params},
		})
	}
	return out
}

type wireRequest struct {
	Model    string        `json:"model"`
	Messages []wireMessage `json:"messages"`
	Tools    []wireTool    `json:"tools,omitempty"`
	Stream   bool          `json:"stream"`
}

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireToolCall struct {
	Index    int          `json:"index,omitempty"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function wireFunction `json:"function"`
}

type wireFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

type wireTool struct {
	Type     string      `json:"type"`
	Function wireToolDef `json:"function"`
}

type wireToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type wireChunk struct {
	Choices []struct {
		Delta wireMessage `json:"delta"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}
