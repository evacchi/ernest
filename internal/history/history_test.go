package history

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/evacchi/ernest/internal/llm"
)

func TestRoundTrip(t *testing.T) {
	s := NewStore(t.TempDir())
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "list files\nplease"},
		{
			Role:      llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{ID: "1", Name: "bash", Args: `{"command":"ls"}`}},
			Replay:    []json.RawMessage{json.RawMessage(`{"type":"reasoning"}`)},
		},
		{Role: llm.RoleTool, ToolCallID: "1", Content: "go.mod"},
	}
	for _, m := range msgs {
		if err := s.Append("a", m); err != nil {
			t.Fatal(err)
		}
	}

	got, err := s.Load("a")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(msgs) {
		t.Fatalf("loaded %d messages", len(got))
	}
	if string(got[1].Replay[0]) != `{"type":"reasoning"}` || got[1].ToolCalls[0].Name != "bash" {
		t.Errorf("assistant = %+v", got[1])
	}
	if got[2].ToolCallID != "1" {
		t.Errorf("tool = %+v", got[2])
	}
}

func TestList(t *testing.T) {
	s := NewStore(t.TempDir())
	s.Append("20260101-000000", llm.Message{Role: llm.RoleUser, Content: "old"})
	s.Append("20260202-000000", llm.Message{Role: llm.RoleUser, Content: strings.Repeat("x", maxTitle+5)})

	got, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "20260202-000000" || got[1].Title != "old" {
		t.Fatalf("list = %+v", got)
	}
	if got[0].Title != strings.Repeat("x", maxTitle)+ellipsis {
		t.Errorf("title = %q", got[0].Title)
	}
}

func TestListMissingDir(t *testing.T) {
	got, err := NewStore(t.TempDir() + "/nope").List()
	if err != nil || len(got) != 0 {
		t.Errorf("list = %+v, %v", got, err)
	}
}

func TestBadID(t *testing.T) {
	s := NewStore(t.TempDir())
	if _, err := s.Load("../x"); err != errBadID {
		t.Errorf("load err = %v", err)
	}
	if err := s.Append("", llm.Message{}); err != errBadID {
		t.Errorf("append err = %v", err)
	}
}
