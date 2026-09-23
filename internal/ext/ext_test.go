package ext

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evacchi/ernest/internal/agent"
	"github.com/evacchi/ernest/internal/llm"
)

// build compiles a Go package to a wasip1 module in a temp dir.
func build(t *testing.T, pkg string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), filepath.Base(pkg)+wasmExt)
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, b)
	}
	return out
}

// fakeSession is an in-memory Session backing store.
type fakeSession struct{ model string }

func (f *fakeSession) session() Session {
	return Session{
		Model:    func() string { return f.model },
		SetModel: func(m string) error { f.model = m; return nil },
		History:  func() []llm.Message { return nil },
	}
}

func load(t *testing.T, pkg, workdir string) *Plugin {
	t.Helper()
	return loadWith(t, pkg, workdir, &fakeSession{model: "m1"})
}

func loadWith(t *testing.T, pkg, workdir string, fs *fakeSession) *Plugin {
	t.Helper()
	ctx := context.Background()
	h := NewHost(workdir, fs.session(), &bytes.Buffer{})
	t.Cleanup(func() { h.Close(ctx) })

	p, err := h.Load(ctx, build(t, pkg))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReverse(t *testing.T) {
	ctx := context.Background()
	p := load(t, "../../examples/reverse", t.TempDir())

	if len(p.Tools()) != 1 || p.Tools()[0].Spec().Name != "reverse" {
		t.Fatalf("tools = %+v", p.Tools())
	}

	out, err := p.Tools()[0].Run(ctx, []byte(`{"text":"abc"}`))
	if err != nil || out != "cba" {
		t.Errorf("reverse = %q, %v", out, err)
	}

	d, err := p.OnToolCall(ctx, llm.ToolCall{Name: "bash", Args: `{"command":"rm -rf /"}`})
	if err != nil || d.Verdict != agent.Block {
		t.Errorf("decision = %+v, %v", d, err)
	}

	// Not subscribed: passes through without a round trip.
	out, err = p.OnToolResult(ctx, llm.ToolCall{}, "x")
	if err != nil || out != "x" {
		t.Errorf("result hook = %q, %v", out, err)
	}
}

func TestProbeVFS(t *testing.T) {
	ctx := context.Background()
	work := t.TempDir()
	p := load(t, "./testdata/probe", work)

	tools := map[string]*Tool{}
	for _, tl := range p.Tools() {
		tools[tl.Spec().Name] = tl
	}

	out, err := tools["model"].Run(ctx, []byte(`{}`))
	if err != nil || out != "m1" {
		t.Errorf("model = %q, %v", out, err)
	}

	if _, err := tools["setmodel"].Run(ctx, []byte(`{}`)); err != nil {
		t.Errorf("setmodel: %v", err)
	}
	out, err = tools["model"].Run(ctx, []byte(`{}`))
	if err != nil || out != "m2" {
		t.Errorf("model after write = %q, %v", out, err)
	}

	out, err = tools["touch"].Run(ctx, []byte(`{}`))
	if err != nil || out != "hi" {
		t.Errorf("touch = %q, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(work, "probe.txt")); !os.IsNotExist(err) {
		t.Errorf("guest write reached disk: %v", err)
	}

	out, err = p.OnToolResult(ctx, llm.ToolCall{}, "abc")
	if err != nil || out != "ABC" {
		t.Errorf("result hook = %q, %v", out, err)
	}

	_, err = p.do(ctx, request{Op: opCall, Name: "nope"})
	if err == nil || !strings.Contains(err.Error(), "unknown tool") {
		t.Errorf("unknown tool err = %v", err)
	}
}

// The /model demo: a slash command implemented purely with file I/O.
func TestModelCommand(t *testing.T) {
	ctx := context.Background()
	fs := &fakeSession{model: "m1"}
	p := loadWith(t, "../../examples/model", t.TempDir(), fs)

	if len(p.Commands()) != 1 || p.Commands()[0].Name() != "model" {
		t.Fatalf("commands = %+v", p.Commands())
	}
	cmd := p.Commands()[0]

	out, err := cmd.Run(ctx, "")
	if err != nil || !strings.Contains(out, "m1") {
		t.Errorf("show = %q, %v", out, err)
	}

	out, err = cmd.Run(ctx, "gpt-6-luna")
	if err != nil || fs.model != "gpt-6-luna" || !strings.Contains(out, "gpt-6-luna") {
		t.Errorf("set = %q, %v, model %q", out, err, fs.model)
	}
}
