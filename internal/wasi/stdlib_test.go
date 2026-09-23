package wasi_test

import (
	"bytes"
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/tetratelabs/wazero"
	wfs "tractor.dev/wanix/fs"
	"tractor.dev/wanix/fs/memfs"

	wasi "github.com/evacchi/ernest/internal/wasi"
)

// These tests are ported from wazero's wasi_stdlib_test.go (Apache-2.0). Rather
// than mounting a host directory through wazero's own FS config, the guest file
// system here is a wanix VFS and the preopens are configured on our System — so
// the same guest behavior is validated against the VFS backend. The
// socket/http/stdin subcommands, which don't apply to a pure VFS, are omitted.

var (
	wasiGuestOnce  sync.Once
	wasiGuestBytes []byte
	wasiGuestErr   error
)

// wasiGuest compiles testdata/wasi to GOOS=wasip1 once per test binary.
func wasiGuest(t *testing.T) []byte {
	t.Helper()
	wasiGuestOnce.Do(func() {
		goTool, err := exec.LookPath("go")
		if err != nil {
			wasiGuestErr = err
			return
		}
		dir, err := os.MkdirTemp("", "wasiguest")
		if err != nil {
			wasiGuestErr = err
			return
		}
		out := filepath.Join(dir, "wasi.wasm")
		cmd := exec.Command(goTool, "build", "-o", out, "./testdata/wasi")
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		if o, err := cmd.CombinedOutput(); err != nil {
			wasiGuestErr = fmt.Errorf("%w\n%s", err, o)
			return
		}
		wasiGuestBytes, wasiGuestErr = os.ReadFile(out)
	})
	if wasiGuestErr != nil {
		t.Skipf("cannot build wasi guest: %v", wasiGuestErr)
	}
	return wasiGuestBytes
}

// runWasiGuest runs the compiled guest with the given args against root (mounted
// as the preopen "/"), returning everything it wrote to stdout+stderr.
func runWasiGuest(t *testing.T, root wfs.FS, args ...string) string {
	t.Helper()
	bin := wasiGuest(t)

	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	t.Cleanup(func() { r.Close(ctx) })

	var out bytes.Buffer
	s := wasi.NewSystem(root,
		wasi.WithArgs(args...),
		wasi.WithStdio(nil, &out, &out),
	)
	t.Cleanup(func() { s.Close() })
	if _, err := s.Preopen("/", "."); err != nil {
		t.Fatal(err)
	}

	if _, err := wasi.Instantiate(ctx, r, s); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Instantiate(ctx, bin); err != nil {
		t.Fatalf("guest %v failed: %v\noutput:\n%s", args, err, out.String())
	}
	return out.String()
}

func Test_fdReaddir_ls(t *testing.T) {
	root := memfs.New()
	// Mirrors wazero's fstest "dir": a file, a subdirectory, and another file.
	mustWrite(t, root, "-", "")
	mustMkdir(t, root, "a-")
	mustWrite(t, root, "ab-", "")

	t.Run("empty directory", func(t *testing.T) {
		requireLsOut(t, nil, runWasiGuest(t, root, "wasi", "ls", "./a-"))
	})

	t.Run("not a directory", func(t *testing.T) {
		out := runWasiGuest(t, root, "wasi", "ls", "-")
		require(t, "\n"+out, "\nENOTDIR\n")
	})

	t.Run("directory with entries", func(t *testing.T) {
		requireLsOut(t, []string{"./-", "./a-", "./ab-"}, runWasiGuest(t, root, "wasi", "ls", "."))
	})

	t.Run("directory with entries - read twice", func(t *testing.T) {
		requireLsOut(t, []string{
			"./-", "./a-", "./ab-",
			"./-", "./a-", "./ab-",
		}, runWasiGuest(t, root, "wasi", "ls", ".", "repeat"))
	})

	t.Run("directory with tons of entries", func(t *testing.T) {
		big := memfs.New()
		const n = 300
		for i := range n {
			mustWrite(t, big, strconv.Itoa(i), "")
		}
		out := runWasiGuest(t, big, "wasi", "ls", ".")
		lines := strings.Split(out, "\n")
		// n entries plus a trailing newline.
		require(t, len(lines), n+1)
	})
}

func requireLsOut(t *testing.T, expected []string, console string) {
	t.Helper()
	actual := strings.Split(console, "\n")
	sort.Strings(actual)
	actual = actual[1:] // drop the entry produced by the trailing newline
	sort.Strings(expected)
	if len(actual) == 0 {
		if expected != nil {
			t.Fatalf("expected %v, got empty", expected)
		}
		return
	}
	require(t, strings.Join(actual, "\n"), strings.Join(expected, "\n"))
}

func Test_fdReaddir_stat(t *testing.T) {
	// wazero's original expectation is all "false" because it reports
	// non-terminal stdio as a plain device. Here stdio is always a character
	// device (terminal detection is deferred), so the three streams report as
	// TTYs while the preopened directory does not.
	out := runWasiGuest(t, memfs.New(), "wasi", "stat")
	require(t, "\n"+out, `
stdin isatty: true
stdout isatty: true
stderr isatty: true
/ isatty: false
`)
}

func Test_Stdout(t *testing.T) {
	require(t, runWasiGuest(t, memfs.New(), "wasi", "stdout"), "test")
}

func Test_LargeStdout(t *testing.T) {
	out := runWasiGuest(t, memfs.New(), "wasi", "largestdout")
	// The guest writes a large generated Go program to stdout; it must arrive
	// intact, which we verify by parsing it.
	if _, err := parser.ParseFile(token.NewFileSet(), "out.go", out, 0); err != nil {
		t.Fatalf("large stdout is not valid Go (len=%d): %v", len(out), err)
	}
}

func mustWrite(t *testing.T, fsys wfs.FS, name, data string) {
	t.Helper()
	if err := wfs.WriteFile(fsys, name, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustMkdir(t *testing.T, fsys wfs.FS, name string) {
	t.Helper()
	if err := wfs.Mkdir(fsys, name, 0o755); err != nil {
		t.Fatal(err)
	}
}

func require[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}
