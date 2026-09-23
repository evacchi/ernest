package tools

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/evacchi/ernest/internal/llm"
)

const readSchema = `{
  "type": "object",
  "properties": {
    "path":   {"type": "string",  "description": "File path"},
    "offset": {"type": "integer", "description": "1-based first line"},
    "limit":  {"type": "integer", "description": "Max lines to return"}
  },
  "required": ["path"]
}`

// Read returns a text file, optionally a line window of it.
type Read struct{}

type readArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

func (Read) Spec() llm.ToolSpec {
	return spec("read", "Read a text file. Use offset/limit for large files.", readSchema)
}

func (Read) Run(_ context.Context, args json.RawMessage) (string, error) {
	in, err := decode[readArgs](args)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(in.Path)
	if err != nil {
		return "", err
	}

	// Window: lines [offset, offset+limit), clamped to the file.
	lines := strings.Split(string(data), "\n")
	start := min(max(in.Offset-1, 0), len(lines))
	end := len(lines)
	if in.Limit > 0 {
		end = min(start+in.Limit, end)
	}
	return truncate(strings.Join(lines[start:end], "\n")), nil
}
