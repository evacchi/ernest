package wasi_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tetratelabs/wazero"
	wfs "tractor.dev/wanix/fs"
	"tractor.dev/wanix/fs/memfs"

	wasi "github.com/evacchi/ernest/internal/wasi"
)

// TestEndToEndGoGuest compiles a real Go program to GOOS=wasip1 and runs it
// against this implementation with a wanix memfs as the whole guest file
// system. It exercises args, env, file writes/reads, mkdir and readdir end to
// end through the actual Go WASI runtime.
func TestEndToEndGoGuest(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not available")
	}

	wasmPath := filepath.Join(t.TempDir(), "filetest.wasm")
	build := exec.Command(goTool, "build", "-o", wasmPath, "./testdata/filetest")
	build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compiling guest failed: %v\n%s", err, out)
	}
	wasm, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	defer r.Close(ctx)

	root := memfs.New()
	var stdout, stderr bytes.Buffer
	sys := wasi.NewSystem(root,
		wasi.WithArgs("filetest.wasm", "greetings"),
		wasi.WithEnviron("FOO=bar"),
		wasi.WithStdio(nil, &stdout, &stderr),
	)
	defer sys.Close()
	if _, err := sys.Preopen("/", "."); err != nil {
		t.Fatal(err)
	}

	if _, err := wasi.Instantiate(ctx, r, sys); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Instantiate(ctx, wasm); err != nil {
		t.Fatalf("guest failed: %v\nstderr:\n%s", err, stderr.String())
	}

	got := stdout.String()
	for _, want := range []string{
		"arg1: greetings",
		"env FOO: bar",
		"read: hello vfs",
		"entry: hello.txt",
		"entry: sub/",
		"done",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout missing %q\nfull output:\n%s", want, got)
		}
	}

	// The guest's writes must be visible in the backing VFS.
	if b, err := wfs.ReadFile(root, "hello.txt"); err != nil || string(b) != "hello vfs" {
		t.Errorf("hello.txt in VFS = %q, %v; want %q", b, err, "hello vfs")
	}
	if b, err := wfs.ReadFile(root, "sub/a.txt"); err != nil || string(b) != "a" {
		t.Errorf("sub/a.txt in VFS = %q, %v; want %q", b, err, "a")
	}
}
