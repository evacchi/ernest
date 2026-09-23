package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/evacchi/ernest/internal/llm"
)

const editSchema = `{
  "type": "object",
  "properties": {
    "path": {"type": "string", "description": "File path"},
    "old":  {"type": "string", "description": "Exact text to replace; must occur once"},
    "new":  {"type": "string", "description": "Replacement text"}
  },
  "required": ["path", "old", "new"]
}`

// Edit replaces one unique occurrence of old with new and returns the
// change as a unified diff, so the model can verify it.
type Edit struct{}

type editArgs struct {
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

func (Edit) Spec() llm.ToolSpec {
	return spec("edit", "Replace an exact, unique snippet in a file.", editSchema)
}

func (Edit) Run(_ context.Context, args json.RawMessage) (string, error) {
	in, err := decode[editArgs](args)
	if err != nil {
		return "", err
	}
	if in.Old == "" {
		return "", errors.New("old must not be empty")
	}

	info, err := os.Stat(in.Path)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(in.Path)
	if err != nil {
		return "", err
	}

	// Uniqueness keeps the edit unambiguous.
	text := string(data)
	switch n := strings.Count(text, in.Old); n {
	case 0:
		return "", errors.New("old text not found")
	case 1:
	default:
		return "", fmt.Errorf("old text matches %d times; add context to make it unique", n)
	}

	edited := strings.Replace(text, in.Old, in.New, 1)
	if err := os.WriteFile(in.Path, []byte(edited), info.Mode().Perm()); err != nil {
		return "", err
	}
	return unified(in.Path, text, edited), nil
}
