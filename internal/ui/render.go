// Package ui renders agent events in the terminal: markdown via glamour,
// code via chroma, tool calls as compact blocks.
//
//	❯ list the go files                 user
//	● bash  $ ls *.go                   tool call
//	  │ main.go                         result, first maxBodyLines lines
//	  │ … +12 lines
//	There are **two** files ...          assistant markdown
package ui

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"charm.land/glamour/v2"
	"charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"

	"github.com/evacchi/ernest/internal/agent"
	"github.com/evacchi/ernest/internal/llm"
)

const (
	defaultWidth = 80
	maxBodyLines = 12
	gutter       = "  │ "

	// Built-in tool names whose calls get a tailored rendering.
	toolRead  = "read"
	toolWrite = "write"
	toolEdit  = "edit"
	toolBash  = "bash"
)

// palette holds the lipgloss styles for one background.
type palette struct {
	user, bullet, tool, dim, err, warn, add, del lipgloss.Style
}

func newPalette(dark bool) palette {
	ld := lipgloss.LightDark(dark)
	fg := func(light, darkc string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(ld(lipgloss.Color(light), lipgloss.Color(darkc)))
	}

	return palette{
		user:   fg("#5A56E0", "#A5A1FF").Bold(true),
		bullet: fg("#2E8B57", "#5FD787"),
		tool:   lipgloss.NewStyle().Bold(true),
		dim:    fg("#8A8A8A", "#6C6C6C"),
		err:    fg("#C0392B", "#FF6B6B"),
		warn:   fg("#B7791F", "#F6C177"),
		add:    fg("#2E8B57", "#5FD787"),
		del:    fg("#C0392B", "#FF6B6B"),
	}
}

// renderer turns events into styled strings.
type renderer struct {
	dark  bool
	width int
	pal   palette
	hl    *highlighter
	md    *glamour.TermRenderer
}

func newRenderer(dark bool) *renderer {
	r := &renderer{dark: dark, pal: newPalette(dark), hl: newHighlighter(dark)}
	r.setWidth(defaultWidth)
	return r
}

// setWidth rebuilds the markdown renderer for a new terminal width.
func (r *renderer) setWidth(w int) {
	if w <= 0 || w == r.width {
		return
	}

	style := styles.LightStyle
	if r.dark {
		style = styles.DarkStyle
	}

	md, err := glamour.NewTermRenderer(glamour.WithStandardStyle(style), glamour.WithWordWrap(w))
	if err != nil {
		return
	}
	r.width, r.md = w, md
}

// markdown renders assistant text; on failure the raw text is shown.
func (r *renderer) markdown(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}

	// glamour counts a tab as one column; expand first so lines fit.
	out, err := r.md.Render(strings.ReplaceAll(s, "\t", strings.Repeat(" ", tabWidth)))
	if err != nil {
		return s
	}
	return strings.Trim(trimPad(out), "\n")
}

func (r *renderer) user(text string) string {
	return r.pal.user.Render(promptFirst) + text
}

func (r *renderer) errorLine(err error) string {
	return r.pal.err.Render("✗ " + err.Error())
}

// callArgs is the union of built-in tool arguments the UI understands.
type callArgs struct {
	Path    string `json:"path"`
	Offset  int    `json:"offset"`
	Command string `json:"command"`
	Content string `json:"content"`
	Old     string `json:"old"`
	New     string `json:"new"`
}

func parseArgs(c llm.ToolCall) callArgs {
	var a callArgs
	_ = json.Unmarshal([]byte(c.Args), &a)
	return a
}

// call renders the header line, e.g. "● bash  $ go test ./...".
func (r *renderer) call(c llm.ToolCall) string {
	return r.pal.bullet.Render("●") + " " + r.pal.tool.Render(c.Name) + "  " + r.summary(c)
}

func (r *renderer) summary(c llm.ToolCall) string {
	a := parseArgs(c)

	switch c.Name {
	case toolBash:
		return r.pal.dim.Render("$ ") + firstLine(a.Command)
	case toolRead:
		if a.Offset > 0 {
			return fmt.Sprintf("%s:%d", a.Path, a.Offset)
		}
		return a.Path
	case toolWrite, toolEdit:
		return a.Path
	}
	return r.pal.dim.Render(firstLine(c.Args))
}

// result renders the call header plus its interpreted output.
func (r *renderer) result(c llm.ToolCall, out string, o agent.Outcome) string {
	return r.call(c) + "\n" + r.body(c, out, o)
}

func (r *renderer) body(c llm.ToolCall, out string, o agent.Outcome) string {
	switch o {
	case agent.OutcomeError:
		return r.block(paintLines(r.pal.err, out))
	case agent.OutcomeBlocked:
		return r.block(paintLines(r.pal.warn, out))
	}

	a := parseArgs(c)
	switch c.Name {
	case toolRead:
		return r.block(r.hl.code(head(out), a.Path, ""), more(out))
	case toolWrite:
		if looksLikeDiff(out) {
			return r.block(r.diff(out))
		}
		return r.block(r.hl.code(head(a.Content), a.Path, ""), more(a.Content))
	case toolEdit, toolBash:
		if looksLikeDiff(out) {
			return r.block(r.diff(out))
		}
	}
	return r.block(paintLines(r.pal.dim, head(out)), more(out))
}

// paintLines styles each line on its own; rendering a multi-line string at
// once would pad every line to the widest one.
func paintLines(st lipgloss.Style, s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = st.Render(l)
	}
	return strings.Join(lines, "\n")
}

// trailingPad matches a line's tail of spaces and SGR codes.
var trailingPad = regexp.MustCompile(`(?:\x1b\[[0-9;]*m| )+$`)

// trimPad drops the right padding glamour adds to every line, keeping a
// reset if the padding carried styles.
func trimPad(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = trailingPad.ReplaceAllStringFunc(l, func(tail string) string {
			if strings.Contains(tail, "\x1b") {
				return sgrReset
			}
			return ""
		})
	}
	return strings.Join(lines, "\n")
}

// block prefixes every line with the gutter and appends "… +N lines".
func (r *renderer) block(text string, hidden ...int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	g := r.pal.dim.Render(gutter)
	for i, l := range lines {
		lines[i] = g + l
	}

	out := strings.Join(lines, "\n")
	if len(hidden) > 0 && hidden[0] > 0 {
		out += "\n" + g + r.moreNote(hidden[0])
	}
	return out
}

func (r *renderer) moreNote(n int) string {
	if n <= 0 {
		return ""
	}
	return r.pal.dim.Render(fmt.Sprintf("… +%d lines", n))
}

// head keeps the first maxBodyLines lines.
func head(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= maxBodyLines {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:maxBodyLines], "\n")
}

// more counts lines hidden by head.
func more(s string) int {
	return max(strings.Count(strings.TrimRight(s, "\n"), "\n")+1-maxBodyLines, 0)
}

// firstLine returns the first line of s, marking any cut with " …".
func firstLine(s string) string {
	l, rest, cut := strings.Cut(strings.TrimSpace(s), "\n")
	if cut && rest != "" {
		return l + " …"
	}
	return l
}
