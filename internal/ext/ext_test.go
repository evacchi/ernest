package ext

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

var errNoAsk = errors.New("no ask")

// fakeSession is an in-memory Session backing store.
type fakeSession struct {
	model, id string
	ask       func(question string, choices []string) string
}

func (f *fakeSession) session() Session {
	return Session{
		Model:    func() string { return f.model },
		SetModel: func(m string) error { f.model = m; return nil },
		Models: func(context.Context) ([]string, error) {
			return []string{"gpt-6-luna", "gpt-5", "whisper-1", "gpt-4o-2024-08-06", "o3", "gpt-4o-realtime-preview"}, nil
		},
		History: func() []llm.Message { return nil },
		ID:      func() string { return f.id },
		Resume:  func(id string) error { f.id = id; return nil },
		Ask: func(_ context.Context, q string, choices []string) (string, error) {
			if f.ask == nil {
				return "", errNoAsk
			}
			return f.ask(q, choices), nil
		},
		Sessions: func() ([]string, error) {
			return []string{"20260924-103000 fix the bug", "20260923-090000 hello"}, nil
		},
	}
}

func load(t *testing.T, pkg, workdir string) *Plugin {
	t.Helper()
	return loadWith(t, pkg, workdir, &fakeSession{model: "m1"})
}

func loadWith(t *testing.T, pkg, workdir string, fs *fakeSession) *Plugin {
	t.Helper()
	_, ps := loadAll(t, workdir, fs, pkg)
	return ps[0]
}

func loadAll(t *testing.T, workdir string, fs *fakeSession, pkgs ...string) (*Host, []*Plugin) {
	t.Helper()
	ctx := context.Background()
	h := NewHost(workdir, fs.session(), &bytes.Buffer{})
	t.Cleanup(func() { h.Close(ctx) })

	var ps []*Plugin
	for _, pkg := range pkgs {
		p, err := h.Load(ctx, build(t, pkg))
		if err != nil {
			t.Fatal(err)
		}
		ps = append(ps, p)
	}
	return h, ps
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

	rm := llm.ToolCall{ID: "1", Name: "bash", Args: `{"command":"rm -rf /"}`}
	ls := llm.ToolCall{ID: "2", Name: "bash", Args: `{"command":"ls"}`}
	ds, err := p.OnToolCalls(ctx, []llm.ToolCall{rm, ls})
	if err != nil || ds[0].Verdict != agent.Block || ds[1].Verdict != agent.Allow {
		t.Errorf("decisions = %+v, %v", ds, err)
	}

	// Not subscribed: passes through without a round trip.
	out, err = p.OnToolResult(ctx, ls, "x")
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

	// No argument: filtered chat models, current one kept and preselected.
	res, err := cmd.Run(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(res.Choices, ","); got != "gpt-5,gpt-6-luna,m1,o3" || res.Selected != "m1" {
		t.Errorf("choices = %s, selected %q", got, res.Selected)
	}

	res, err = cmd.Run(ctx, "gpt-6-luna")
	if err != nil || fs.model != "gpt-6-luna" || !strings.Contains(res.Output, "gpt-6-luna") {
		t.Errorf("set = %+v, %v, model %q", res, err, fs.model)
	}
}

// The /resume demo: lists /agent/sessions, resumes via /agent/config/session.
func TestResumeCommand(t *testing.T) {
	ctx := context.Background()
	fs := &fakeSession{model: "m1"}
	p := loadWith(t, "../../examples/resume", t.TempDir(), fs)
	cmd := p.Commands()[0]

	res, err := cmd.Run(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Choices) != 2 || res.Choices[0] != "20260924-103000 fix the bug" {
		t.Fatalf("choices = %q", res.Choices)
	}

	// A pick is the whole line; the id is its first field.
	res, err = cmd.Run(ctx, res.Choices[0])
	if err != nil || fs.id != "20260924-103000" || res.Output != "resumed 20260924-103000" {
		t.Errorf("resume = %+v, %v, id %q", res, err, fs.id)
	}
}

// Reload replaces running extensions; the old instances stop.
func TestReload(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	wasm := build(t, "../../examples/reverse")
	data, err := os.ReadFile(wasm)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "reverse.wasm"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	fs := &fakeSession{model: "m1"}
	h := NewHost(t.TempDir(), fs.session(), &bytes.Buffer{})
	defer h.Close(ctx)

	old, err := h.LoadDir(ctx, dir)
	if err != nil || len(old) != 1 {
		t.Fatalf("load = %v, %v", old, err)
	}

	fresh, err := h.Reload(ctx, dir)
	if err != nil || len(fresh) != 1 {
		t.Fatalf("reload = %v, %v", fresh, err)
	}

	if out, err := fresh[0].Tools()[0].Run(ctx, []byte(`{"text":"ab"}`)); err != nil || out != "ba" {
		t.Errorf("new instance = %q, %v", out, err)
	}
	if _, err := old[0].Tools()[0].Run(ctx, []byte(`{"text":"ab"}`)); err == nil {
		t.Error("old instance still answers")
	}
}

// Compiled modules persist in the user cache dir across hosts.
func TestDiskCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))

	load(t, "../../examples/reverse", t.TempDir())

	dir, err := cacheDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dir, home) {
		t.Fatalf("cache dir %s outside %s", dir, home)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Errorf("cache dir empty: %v, %v", entries, err)
	}
}

// approve asks once per escalated command; "allow for session" skips
// later asks, "deny" blocks, plain calls pass without asking.
func TestApprove(t *testing.T) {
	ctx := context.Background()
	answers := []string{"allow for session", "deny"}
	var asked []string
	fs := &fakeSession{model: "m1", ask: func(q string, choices []string) string {
		asked = append(asked, q)
		a := answers[0]
		answers = answers[1:]
		return a
	}}
	p := loadWith(t, "../../examples/approve", t.TempDir(), fs)

	batch := []llm.ToolCall{
		{ID: "1", Name: "bash", Args: `{"command":"go get x","escalate":true}`},
		{ID: "2", Name: "bash", Args: `{"command":"go get x","escalate":true}`},
		{ID: "3", Name: "bash", Args: `{"command":"go get y","escalate":true}`},
		{ID: "4", Name: "bash", Args: `{"command":"go get y"}`},
	}
	ds, err := p.OnToolCalls(ctx, batch)
	if err != nil {
		t.Fatal(err)
	}

	want := []agent.Verdict{agent.Grant, agent.Grant, agent.Block, agent.Allow}
	for i, d := range ds {
		if d.Verdict != want[i] {
			t.Errorf("call %s: verdict = %v, want %v", batch[i].ID, d.Verdict, want[i])
		}
	}
	if len(asked) != 2 || !strings.Contains(asked[0], "go get x") {
		t.Errorf("asked = %q", asked)
	}

	res, err := command(p, "revoke").Run(ctx, "")
	if err != nil || !slices.Equal(res.Choices, []string{"go get x"}) {
		t.Errorf("revoke choices = %+v, %v", res, err)
	}
}

func command(p *Plugin, name string) *Command {
	for _, c := range p.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}
