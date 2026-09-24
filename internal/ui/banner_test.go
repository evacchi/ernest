package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestBanner(t *testing.T) {
	home, _ := os.UserHomeDir()
	r := newRenderer(true)
	out := ansi.Strip(r.banner("gpt-6-luna", Info{
		Workdir:    filepath.Join(home, "src", "app"),
		Extensions: []string{"model", "reverse"},
		Skills:     []string{"pdf", "git"},
	}))

	for _, want := range []string{"▄▄▄▄▄▄▄", "▀▀▀▀▀▀▀▀▀▀▀", "ernest", "the importance of being harness", "gpt-6-luna", "~/src/app", "model, reverse · $pdf $git", "/help"} {
		if !strings.Contains(out, want) {
			t.Errorf("banner missing %q:\n%s", want, out)
		}
	}
}

// Long paths are shortened from the left so the box fits the terminal.
func TestBannerFitsWidth(t *testing.T) {
	r := newRenderer(true)
	r.setWidth(50)
	out := r.banner("gpt-6-luna", Info{Workdir: "/very/long/path/that/goes/on/and/on/forever/project"})

	for _, l := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(l); w > 50 {
			t.Errorf("width %d > 50: %q", w, ansi.Strip(l))
		}
	}
	if !strings.Contains(ansi.Strip(out), "…") || !strings.Contains(ansi.Strip(out), "project") {
		t.Errorf("path not shortened from the left:\n%s", ansi.Strip(out))
	}
}
