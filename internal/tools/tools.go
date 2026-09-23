// Package tools provides the built-in tools: read, write, edit, bash.
// They act on the host file system relative to the process working directory.
package tools

import (
	"encoding/json"
	"fmt"

	"github.com/aymanbagabas/go-udiff"

	"github.com/evacchi/ernest/internal/llm"
)

const (
	maxOutputBytes = 50 << 10
	truncatedNote  = "\n[truncated]"
	noChanges      = "no changes"
	oldPrefix      = "a/"
	newPrefix      = "b/"
)

// truncate caps tool output so one call cannot flood the context.
func truncate(s string) string {
	if len(s) <= maxOutputBytes {
		return s
	}
	return s[:maxOutputBytes] + truncatedNote
}

// decode parses tool arguments into T.
func decode[T any](args json.RawMessage) (T, error) {
	var v T
	if err := json.Unmarshal(args, &v); err != nil {
		return v, fmt.Errorf("bad arguments: %w", err)
	}
	return v, nil
}

func spec(name, desc, schema string) llm.ToolSpec {
	return llm.ToolSpec{Name: name, Description: desc, Params: json.RawMessage(schema)}
}

// unified returns a git-style diff of path, e.g.
//
//	--- a/main.go
//	+++ b/main.go
//	@@ -1,3 +1,3 @@
//	-old
//	+new
func unified(path, before, after string) string {
	if before == after {
		return noChanges
	}
	return truncate(udiff.Unified(oldPrefix+path, newPrefix+path, before, after))
}
