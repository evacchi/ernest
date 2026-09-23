package ui

import (
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	appName     = "ernest"
	tagline     = "the importance of being harness"
	noExtension = "no extensions"
	bannerHint  = "/help for commands"
	homePrefix  = "~"
	ellipsis    = "…"

	// Columns around the text: border and padding on both sides, plus the
	// gap after the hat.
	boxChrome = 2 + 2
	hatGap    = "  "
)

// Hat rows: crown, band (accent), brim.
var (
	hatCrown = []string{"  ▄▄▄▄▄▄▄  ", "  ███████  "}
	hatBand  = "  ███████  "
	hatBrim  = "▀▀▀▀▀▀▀▀▀▀▀"
)

// Info describes the session for the splash banner.
type Info struct {
	Workdir    string
	Extensions []string
}

// banner is the startup splash: a top hat beside the session details,
// in a rounded box.
//
//	╭──────────────────────────────────────────╮
//	│   ▄▄▄▄▄▄▄    ernest  the importance of being harness │
//	│   ███████    gpt-6-luna · ~/src/app      │
//	│   ███████    model, reverse              │
//	│ ▀▀▀▀▀▀▀▀▀▀▀  /help for commands          │
//	╰──────────────────────────────────────────╯
func (r *renderer) banner(model string, info Info) string {
	hat := strings.Join([]string{
		hatCrown[0],
		hatCrown[1],
		r.pal.user.Render(hatBand),
		hatBrim,
	}, "\n")

	exts := noExtension
	if len(info.Extensions) > 0 {
		exts = strings.Join(info.Extensions, ", ")
	}

	// Fit the text column; the path gives way from the left.
	room := max(r.width-lipgloss.Width(hat)-len(hatGap)-boxChrome, 1)
	where := model + " · "
	where += truncLeft(shortPath(info.Workdir), room-ansi.StringWidth(where))

	text := strings.Join([]string{
		ansi.Truncate(r.pal.tool.Render(appName)+"  "+r.pal.dim.Italic(true).Render(tagline), room, ellipsis),
		r.pal.dim.Render(ansi.Truncate(where, room, ellipsis)),
		r.pal.dim.Render(ansi.Truncate(exts, room, ellipsis)),
		r.pal.dim.Render(bannerHint),
	}, "\n")

	body := lipgloss.JoinHorizontal(lipgloss.Top, hat, hatGap, text)
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(r.pal.user.GetForeground()).
		Padding(0, 1)
	return box.Render(body)
}

// truncLeft keeps the tail of s within n columns, e.g. "…/src/app".
func truncLeft(s string, n int) string {
	if ansi.StringWidth(s) <= n {
		return s
	}
	if n <= 1 {
		return ellipsis
	}

	r := []rune(s)
	return ellipsis + string(r[len(r)-(n-1):])
}

// shortPath abbreviates the home directory as "~".
func shortPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(p, home) {
		return p
	}
	return homePrefix + strings.TrimPrefix(p, home)
}
