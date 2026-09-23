package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, tool interface {
	Run(context.Context, json.RawMessage) (string, error)
}, args any) (string, error) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	return tool.Run(context.Background(), raw)
}

func TestEdit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("a b a c"), filePerm); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		old, errPart string
	}{
		{"zz", "not found"},
		{"a", "2 times"},
		{"b", ""},
	}
	for _, tt := range tests {
		_, err := run(t, Edit{}, editArgs{Path: path, Old: tt.old, New: "X"})
		if tt.errPart == "" && err != nil {
			t.Errorf("%q: %v", tt.old, err)
		}
		if tt.errPart != "" && (err == nil || !strings.Contains(err.Error(), tt.errPart)) {
			t.Errorf("%q: err = %v, want %q", tt.old, err, tt.errPart)
		}
	}

	data, _ := os.ReadFile(path)
	if string(data) != "a X a c" {
		t.Errorf("content = %q", data)
	}
}

func TestEditReturnsDiff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.go")
	if err := os.WriteFile(path, []byte("1\n2\n3\n4\n5\n6\n7\n8\n"), filePerm); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, Edit{}, editArgs{Path: path, Old: "6\n", New: "six\n"})
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"+++ b/" + path, "@@ -3,6 +3,6 @@", "-6\n+six\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("diff missing %q:\n%s", want, out)
		}
	}
}

func TestRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(path, []byte("1\n2\n3\n4"), filePerm); err != nil {
		t.Fatal(err)
	}

	out, err := run(t, Read{}, readArgs{Path: path, Offset: 2, Limit: 2})
	if err != nil || out != "2\n3" {
		t.Errorf("window = %q, %v", out, err)
	}

	out, err = run(t, Read{}, readArgs{Path: path, Offset: 99})
	if err != nil || out != "" {
		t.Errorf("past end = %q, %v", out, err)
	}
}

func TestBashFailureIsResult(t *testing.T) {
	out, err := run(t, Bash{}, bashArgs{Command: "echo hi; exit 3"})
	if err != nil || !strings.Contains(out, "hi") || !strings.Contains(out, "exit status 3") {
		t.Errorf("out = %q, err = %v", out, err)
	}
}
