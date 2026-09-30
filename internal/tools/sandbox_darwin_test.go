package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sandboxed(t *testing.T, work string) Bash {
	t.Helper()
	sb, err := NewSandbox(work)
	if err != nil {
		t.Fatal(err)
	}
	return Bash{Sandbox: sb}
}

func TestSandboxWrites(t *testing.T) {
	work, outside := t.TempDir(), t.TempDir()
	git := filepath.Join(work, gitDir)
	if err := os.Mkdir(git, 0o755); err != nil {
		t.Fatal(err)
	}
	b := sandboxed(t, work)

	tests := []struct {
		name, dir string
		allowed   bool
	}{
		{"inside", work, true},
		{"outside", outside, false},
		{"git", git, false},
	}
	for _, tt := range tests {
		path := filepath.Join(tt.dir, "f")
		_, err := run(t, b, bashArgs{Command: "echo x > " + path})
		if err != nil {
			t.Fatal(err)
		}

		_, statErr := os.Stat(path)
		if (statErr == nil) != tt.allowed {
			t.Errorf("%s: written = %v, want %v", tt.name, statErr == nil, tt.allowed)
		}
	}
}

func TestSandboxReadsAndExec(t *testing.T) {
	out, err := run(t, sandboxed(t, t.TempDir()), bashArgs{Command: "ls / && echo ok"})
	if err != nil || !strings.Contains(out, "ok") {
		t.Errorf("out = %q, err = %v", out, err)
	}
}

func TestSandboxNoNetwork(t *testing.T) {
	out, err := run(t, sandboxed(t, t.TempDir()), bashArgs{Command: "curl -sS -m 5 http://1.1.1.1 && echo reached"})
	if err != nil || strings.Contains(out, "reached") {
		t.Errorf("out = %q, err = %v", out, err)
	}
}
