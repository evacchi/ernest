package wasi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/sys"
	wfs "tractor.dev/wanix/fs"
	"tractor.dev/wanix/fs/localfs"
	"tractor.dev/wanix/fs/memfs"

	wasi "github.com/evacchi/ernest/internal/wasi"
)

// TestWASITestsuite runs the official WebAssembly/wasi-testsuite (snapshot
// preview 1 subset) against this implementation. It is opt-in: set
// WASI_TESTSUITE_DIR to the root of a testsuite checkout that contains prebuilt
// binaries (the prod/testsuite-base branch), e.g.
//
//	WASI_TESTSUITE_DIR=/path/to/wasi-testsuite go test ./wasi -run TestWASITestsuite -v
//
// Full conformance is a non-goal; the harness reports a pass/total summary per
// suite and marks individual mismatches as failures for visibility.
func TestWASITestsuite(t *testing.T) {
	base := os.Getenv("WASI_TESTSUITE_DIR")
	if base == "" {
		t.Skip("set WASI_TESTSUITE_DIR to run the wasi-testsuite")
	}

	suites := []string{
		"tests/c/testsuite/wasm32-wasip1",
		"tests/rust/testsuite/wasm32-wasip1",
		"tests/assemblyscript/testsuite/wasm32-wasip1",
	}

	var grandPass, grandTotal int
	for _, suite := range suites {
		dir := filepath.Join(base, suite)
		wasms, _ := filepath.Glob(filepath.Join(dir, "*.wasm"))
		if len(wasms) == 0 {
			continue
		}
		sort.Strings(wasms)
		pass := 0
		t.Run(suite, func(t *testing.T) {
			for _, w := range wasms {
				if runSuiteCase(t, dir, w) {
					pass++
				}
			}
			t.Logf("%s: %d/%d passed", suite, pass, len(wasms))
		})
		grandPass += pass
		grandTotal += len(wasms)
	}
	t.Logf("TOTAL: %d/%d passed", grandPass, grandTotal)
}

type suiteManifest struct {
	Args     []string          `json:"args"`
	Env      map[string]string `json:"env"`
	Root     string            `json:"root"`
	ExitCode int               `json:"exit_code"`
	Stdout   *string           `json:"stdout"`
	Stderr   *string           `json:"stderr"`
	// Operation-based configs (wasip3) are not supported here.
	Operations json.RawMessage `json:"operations"`
}

// runSuiteCase runs one wasm test case and returns whether it matched its
// manifest expectations.
func runSuiteCase(t *testing.T, dir, wasmPath string) (ok bool) {
	name := strings.TrimSuffix(filepath.Base(wasmPath), ".wasm")
	var m suiteManifest
	if data, err := os.ReadFile(filepath.Join(dir, name+".json")); err == nil {
		if err := json.Unmarshal(data, &m); err != nil {
			t.Errorf("%s: bad manifest: %v", name, err)
			return false
		}
	}

	t.Run(name, func(t *testing.T) {
		if len(m.Operations) > 0 {
			t.Skip("operation-based manifest not supported")
		}
		wasm, err := os.ReadFile(wasmPath)
		if err != nil {
			t.Fatal(err)
		}

		// Build the guest file system from the fixture directory, if any, on a
		// disposable copy so tests may mutate it freely.
		var rootFS wfs.FS = memfs.New()
		if m.Root != "" {
			tmp := t.TempDir()
			if err := os.CopyFS(tmp, os.DirFS(filepath.Join(dir, m.Root))); err != nil {
				t.Fatalf("copy fixture: %v", err)
			}
			lfs, err := localfs.New(tmp)
			if err != nil {
				t.Fatalf("localfs: %v", err)
			}
			rootFS = lfs
		}

		env := make([]string, 0, len(m.Env))
		for k, v := range m.Env {
			env = append(env, k+"="+v)
		}
		sort.Strings(env)

		args := append([]string{filepath.Base(wasmPath)}, m.Args...)

		var stdout, stderr bytes.Buffer
		s := wasi.NewSystem(rootFS,
			wasi.WithArgs(args...),
			wasi.WithEnviron(env...),
			wasi.WithStdio(nil, &stdout, &stderr),
		)
		defer s.Close()
		if m.Root != "" {
			if _, err := s.Preopen("/", "."); err != nil {
				t.Fatal(err)
			}
		}

		exitCode, runErr := runWithTimeout(wasm, s, 15*time.Second)
		ok = true
		if runErr != nil {
			t.Errorf("run error: %v\nstderr: %s", runErr, stderr.String())
			ok = false
		}
		if exitCode != m.ExitCode {
			t.Errorf("exit code = %d, want %d\nstdout: %q\nstderr: %q",
				exitCode, m.ExitCode, stdout.String(), stderr.String())
			ok = false
		}
		if m.Stdout != nil && stdout.String() != *m.Stdout {
			t.Errorf("stdout = %q, want %q", stdout.String(), *m.Stdout)
			ok = false
		}
		if m.Stderr != nil && stderr.String() != *m.Stderr {
			t.Errorf("stderr = %q, want %q", stderr.String(), *m.Stderr)
			ok = false
		}
	})
	return ok
}

// runWithTimeout instantiates the module (running _start) with a wall-clock
// timeout, returning its exit code.
func runWithTimeout(wasm []byte, s *wasi.System, timeout time.Duration) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	r := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCloseOnContextDone(true))
	defer r.Close(ctx)

	if _, err := wasi.Instantiate(ctx, r, s); err != nil {
		return -1, err
	}
	_, err := r.Instantiate(ctx, wasm)
	if err == nil {
		return 0, nil
	}
	var exit *sys.ExitError
	if errors.As(err, &exit) {
		return int(exit.ExitCode()), nil
	}
	return -1, err
}
