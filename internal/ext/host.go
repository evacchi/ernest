// Package ext runs wasm extensions on a VFS-backed WASI.
//
// Each extension is a long-lived WASI command. It talks to the host only
// through files: host requests arrive on /agent/rpc, replies go back on it.
//
//	agent ─► Tool/Hook ─► Plugin ──write──► pipe ──► /agent/rpc ─► guest loop
//	                        ▲                                         │
//	                        └──────read──── pipe ◄──── /agent/rpc ◄───┘
package ext

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tetratelabs/wazero"

	"github.com/evacchi/ernest/internal/llm"
)

const (
	wasmExt  = ".wasm"
	cacheApp = "ernest"
	cacheSub = "wazero"
)

// Session is the agent state exposed under /agent. SetModel is invoked
// when a guest writes /agent/config/model; Models backs the read-only
// /agent/config/models. Likewise ID, Resume and Sessions back
// /agent/config/session and /agent/sessions.
type Session struct {
	Model    func() string
	SetModel func(string) error
	Models   func(context.Context) ([]string, error)
	History  func() []llm.Message

	ID       func() string
	Resume   func(string) error
	Sessions func() ([]string, error)
}

// Host loads extensions and owns their lifetimes.
type Host struct {
	workdir string
	session Session
	log     io.Writer
	cache   wazero.CompilationCache
	plugins []*Plugin
}

// NewHost returns a host that mounts workdir as /work and logs guest
// stdout/stderr to log.
func NewHost(workdir string, s Session, log io.Writer) *Host {
	return &Host{
		workdir: workdir,
		session: s,
		log:     log,
		cache:   newCache(log),
	}
}

// newCache keeps compiled modules on disk, so restarts skip compiling
// unchanged extensions. Entries are keyed by module and wazero version,
// so all projects share one dir. Falls back to memory if the dir fails.
func newCache(log io.Writer) wazero.CompilationCache {
	dir, err := cacheDir()
	if err == nil {
		var c wazero.CompilationCache
		if c, err = wazero.NewCompilationCacheWithDir(dir); err == nil {
			return c
		}
	}

	fmt.Fprintln(log, "wazero cache in memory:", err)
	return wazero.NewCompilationCache()
}

// cacheDir is e.g. ~/Library/Caches/ernest/wazero on macOS.
func cacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, cacheApp, cacheSub), nil
}

// LoadDir loads every *.wasm in dir. A missing dir yields no plugins.
// A broken extension does not stop the others: their errors are joined.
func (h *Host) LoadDir(ctx context.Context, dir string) ([]*Plugin, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*"+wasmExt))
	if err != nil {
		return nil, err
	}

	var (
		out  []*Plugin
		errs []error
	)
	for _, path := range paths {
		p, err := h.Load(ctx, path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out = append(out, p)
	}
	return out, errors.Join(errs...)
}

// Reload stops every running extension and loads dir again. Unchanged
// modules come from the compilation cache.
func (h *Host) Reload(ctx context.Context, dir string) ([]*Plugin, error) {
	if err := h.stopAll(ctx); err != nil {
		return nil, err
	}
	return h.LoadDir(ctx, dir)
}

// Load starts the extension at path and asks it to describe itself.
func (h *Host) Load(ctx context.Context, path string) (*Plugin, error) {
	wasm, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	name := strings.TrimSuffix(filepath.Base(path), wasmExt)
	p, err := start(ctx, h.cache, name, wasm, h.workdir, h.session, h.log)
	if err != nil {
		return nil, err
	}

	h.plugins = append(h.plugins, p)
	return p, nil
}

// Close stops all extensions.
func (h *Host) Close(ctx context.Context) error {
	return errors.Join(h.stopAll(ctx), h.cache.Close(ctx))
}

func (h *Host) stopAll(ctx context.Context) error {
	var errs []error
	for _, p := range h.plugins {
		errs = append(errs, p.close(ctx))
	}
	h.plugins = nil
	return errors.Join(errs...)
}
