// Package openai implements llm.Provider over OpenAI APIs with server-sent
// events streaming: Responses (NewResponses) and Chat Completions (New).
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

// base holds what both APIs share: endpoint, credentials, current model.
type base struct {
	cfg  Config
	http *http.Client

	mu    sync.RWMutex
	model string
}

func newBase(cfg Config) base {
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	return base{cfg: cfg, http: &http.Client{}, model: cfg.Model}
}

// Client is an llm.Provider backed by Chat Completions.
type Client struct {
	base
}

// New returns a Chat Completions client. Empty BaseURL means DefaultBaseURL.
func New(cfg Config) *Client {
	return &Client{base: newBase(cfg)}
}

// Model returns the model used for the next request.
func (c *base) Model() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.model
}

// SetModel switches the model; in-flight requests keep the old one.
func (c *base) SetModel(m string) error {
	m = strings.TrimSpace(m)
	if m == "" {
		return errors.New("openai: empty model name")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.model = m
	return nil
}

// post sends body as JSON to path and returns the event stream.
func (c *base) post(ctx context.Context, path string, body any) (io.ReadCloser, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	url := strings.TrimSuffix(c.cfg.BaseURL, "/") + path
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set(headerAuth, "Bearer "+c.cfg.APIKey)
	hreq.Header.Set(headerContentType, mimeJSON)
	hreq.Header.Set(headerAccept, mimeEventStream)

	resp, err := c.http.Do(hreq)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai: %s: %s", resp.Status, bytes.TrimSpace(msg))
	}
	return resp.Body, nil
}

// errStreamEnd reports a stream cut before its terminal event.
var errStreamEnd = errors.New("openai: stream ended early")

// readSSE calls fn with each "data:" payload until fn reports done or the
// stream sends [DONE] (Chat Completions' terminator).
func readSSE(r io.Reader, fn func(data []byte) (done bool, err error)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(nil, maxSSELine)

	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), ssePrefix)
		if !ok {
			continue
		}

		data = strings.TrimSpace(data)
		if data == sseDone {
			return nil
		}

		done, err := fn([]byte(data))
		if err != nil || done {
			return err
		}
	}

	if err := sc.Err(); err != nil {
		return err
	}
	return errStreamEnd
}

// Stream sends req and assembles the streamed reply.
func (c *Client) Stream(ctx context.Context, req llm.Request, onDelta func(string)) (llm.Message, error) {
	body, err := c.post(ctx, completionsPath, c.encode(req))
	if err != nil {
		return llm.Message{}, err
	}
	defer body.Close()
	return readStream(body, onDelta)
}

// readStream consumes chunks until [DONE], e.g.
//
//	data: {"choices":[{"delta":{"content":"Hel"}}]}
//	data: [DONE]
func readStream(r io.Reader, onDelta func(string)) (llm.Message, error) {
	var acc accumulator
	err := readSSE(r, func(data []byte) (bool, error) {
		var chunk wireChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return false, fmt.Errorf("openai: bad chunk: %w", err)
		}
		if chunk.Error != nil {
			return false, fmt.Errorf("openai: %s", chunk.Error.Message)
		}
		acc.add(chunk, onDelta)
		return false, nil
	})
	if err != nil {
		return llm.Message{}, err
	}
	return acc.message(), nil
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
