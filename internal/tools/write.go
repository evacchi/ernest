package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/evacchi/ernest/internal/llm"
)

const (
	dirPerm  = 0o755
	filePerm = 0o644
)

const writeSchema = `{
  "type": "object",
  "properties": {
    "path":    {"type": "string", "description": "File path"},
    "content": {"type": "string", "description": "Full file content"}
  },
  "required": ["path", "content"]
}`

// Write creates or overwrites a file, creating parent directories.
// Overwrites return a unified diff against the previous content.
type Write struct{}

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (Write) Spec() llm.ToolSpec {
	return spec("write", "Create or overwrite a file.", writeSchema)
}

func (Write) Run(_ context.Context, args json.RawMessage) (string, error) {
	in, err := decode[writeArgs](args)
	if err != nil {
		return "", err
	}

	prev, err := os.ReadFile(in.Path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	existed := err == nil

	if err := os.MkdirAll(filepath.Dir(in.Path), dirPerm); err != nil {
		return "", err
	}
	if err := os.WriteFile(in.Path, []byte(in.Content), filePerm); err != nil {
		return "", err
	}

	if !existed {
		return fmt.Sprintf("created %s (%d bytes)", in.Path, len(in.Content)), nil
	}
	return unified(in.Path, string(prev), in.Content), nil
}
