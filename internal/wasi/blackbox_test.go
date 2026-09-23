package wasi_test

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/sys"
	"tractor.dev/wanix/fs/memfs"

	wasi "github.com/evacchi/ernest/internal/wasi"
)

// The .wasm fixtures under testdata are the small, hand-written WASI command
// modules from wazero's own test suite (Apache-2.0). They exercise a slice of
// the ABI as black boxes, independently of the backend, which makes them a good
// smoke test for this VFS-backed implementation.
//
//	exit_on_start.wasm          calls proc_exit(2)
//	print_args.wasm             writes the null-terminated argv buffer to stdout
//	print_prestat_dirname.wasm  writes the name of preopen fd 3 to stdout

//go:embed testdata/exit_on_start.wasm
var exitOnStartWasm []byte

//go:embed testdata/print_args.wasm
var printArgsWasm []byte

//go:embed testdata/print_prestat_dirname.wasm
var printPrestatDirnameWasm []byte

// runGuest instantiates wasm as a WASI command against a fresh System and
// returns whatever it wrote to stdout together with the instantiation error
// (which carries the exit code for commands that call proc_exit).
func runGuest(t *testing.T, wasm []byte, configure func(*wasi.System)) (string, error) {
	t.Helper()
	ctx := context.Background()

	r := wazero.NewRuntime(ctx)
	t.Cleanup(func() { r.Close(ctx) })

	var stdout bytes.Buffer
	sys := wasi.NewSystem(memfs.New(),
		wasi.WithArgs("guest.wasm"),
		wasi.WithStdio(nil, &stdout, nil),
	)
	if configure != nil {
		configure(sys)
	}
	t.Cleanup(func() { sys.Close() })

	if _, err := wasi.Instantiate(ctx, r, sys); err != nil {
		t.Fatalf("instantiate host module: %v", err)
	}
	_, err := r.Instantiate(ctx, wasm)
	return stdout.String(), err
}

func TestExitOnStart(t *testing.T) {
	_, err := runGuest(t, exitOnStartWasm, nil)
	var exit *sys.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("expected *sys.ExitError, got %v", err)
	}
	if exit.ExitCode() != 2 {
		t.Fatalf("exit code = %d, want 2", exit.ExitCode())
	}
}

func TestPrintArgs(t *testing.T) {
	stdout, err := runGuestArgs(t, printArgsWasm, "prog", "foo", "bar")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "prog\x00foo\x00bar\x00"; stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func runGuestArgs(t *testing.T, wasm []byte, args ...string) (string, error) {
	t.Helper()
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	t.Cleanup(func() { r.Close(ctx) })

	var stdout bytes.Buffer
	sys := wasi.NewSystem(memfs.New(),
		wasi.WithArgs(args...),
		wasi.WithStdio(nil, &stdout, nil),
	)
	t.Cleanup(func() { sys.Close() })
	if _, err := wasi.Instantiate(ctx, r, sys); err != nil {
		t.Fatalf("instantiate host module: %v", err)
	}
	_, err := r.Instantiate(ctx, wasm)
	return stdout.String(), err
}

func TestPrintPrestatDirname(t *testing.T) {
	stdout, err := runGuest(t, printPrestatDirnameWasm, func(s *wasi.System) {
		if _, err := s.Preopen("/", "."); err != nil {
			t.Fatal(err)
		}
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if stdout != "/" {
		t.Fatalf("stdout = %q, want %q", stdout, "/")
	}
}
