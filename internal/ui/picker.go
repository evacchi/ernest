package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

const (
	maxPickerRows = 10
	pickerHelp    = "↑↓ select · type to filter · enter confirm · esc cancel"
	keyUp         = "up"
	keyDown       = "down"
	keyBackspace  = "backspace"
)

// pickAction is what a key did to the picker.
type pickAction int

const (
	pickNone pickAction = iota
	pickDone
	pickCancel
)

// picker lets the user choose one of a command's choices; the pick
// re-runs the command with it, e.g. "/model" → pick → "/model gpt-5".
type picker struct {
	command string
	items   []string
	filter  string
	cursor  int
}

func newPicker(command string, items []string, selected string) *picker {
	p := &picker{command: command, items: items}
	for i, it := range items {
		if it == selected {
			p.cursor = i
		}
	}
	return p
}

// visible returns the items matching the filter, case-insensitively.
func (p *picker) visible() []string {
	if p.filter == "" {
		return p.items
	}

	var out []string
	f := strings.ToLower(p.filter)
	for _, it := range p.items {
		if strings.Contains(strings.ToLower(it), f) {
			out = append(out, it)
		}
	}
	return out
}

func (p *picker) choice() (string, bool) {
	v := p.visible()
	if len(v) == 0 {
		return "", false
	}
	return v[p.cursor], true
}

// key applies one key press: arrows move, text filters, enter picks.
func (p *picker) key(k tea.KeyPressMsg) pickAction {
	switch k.String() {
	case keyUp:
		p.move(-1)
	case keyDown:
		p.move(1)
	case keyEnter:
		if _, ok := p.choice(); ok {
			return pickDone
		}
	case keyEsc, keyCtrlC:
		return pickCancel
	case keyBackspace:
		if p.filter != "" {
			p.filter = p.filter[:len(p.filter)-1]
			p.cursor = 0
		}
	default:
		if k.Text != "" {
			p.filter += k.Text
			p.cursor = 0
		}
	}
	return pickNone
}

func (p *picker) move(d int) {
	n := len(p.visible())
	if n == 0 {
		return
	}
	p.cursor = (p.cursor + d + n) % n
}

// picker renders a scrolling window of items around the cursor:
//
//	/model  filter: lu
//	❯ gpt-6-luna
//	  gpt-6-luna-mini
//	  2/2
//
// The height depends only on the unfiltered list: filtering pads with
// blank rows, because bubbletea's inline renderer leaves stale rows when
// a frame shrinks.
func (r *renderer) picker(p *picker) string {
	head := r.pal.user.Render("/" + p.command)
	if p.filter != "" {
		head += r.pal.dim.Render("  filter: ") + p.filter
	}

	rows := min(len(p.items), maxPickerRows)
	items := p.visible()
	start := min(max(p.cursor-maxPickerRows/2, 0), max(len(items)-maxPickerRows, 0))
	end := min(start+maxPickerRows, len(items))

	lines := []string{head}
	for i := start; i < end; i++ {
		if i == p.cursor {
			lines = append(lines, r.pal.user.Render(promptFirst+items[i]))
			continue
		}
		lines = append(lines, promptNext+items[i])
	}
	if len(items) == 0 {
		lines = append(lines, r.pal.dim.Render(promptNext+"no matches"))
	}
	for len(lines) < rows+1 {
		lines = append(lines, "")
	}

	footer := ""
	if len(items) > 0 {
		footer = r.pal.dim.Render(fmt.Sprintf("%s%d/%d", promptNext, p.cursor+1, len(items)))
	}
	return strings.Join(append(lines, footer), "\n")
}
