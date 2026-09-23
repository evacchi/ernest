package ui

import (
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"

	"github.com/evacchi/ernest/internal/agent"
)

// Printer renders events without interaction (one-shot mode): answers to
// stdout, tool activity to stderr. Colors degrade to plain text when the
// output is not a terminal.
type Printer struct {
	r        *renderer
	out, err io.Writer
	stream   strings.Builder
}

// NewPrinter returns a printer sized to the terminal, if any.
func NewPrinter() *Printer {
	tty := term.IsTerminal(os.Stdout.Fd())
	dark := !tty || lipgloss.HasDarkBackground(os.Stdin, os.Stdout)

	p := &Printer{
		r:   newRenderer(dark),
		out: colorprofile.NewWriter(os.Stdout, os.Environ()),
		err: colorprofile.NewWriter(os.Stderr, os.Environ()),
	}
	if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil {
		p.r.setWidth(w)
	}
	return p
}

// Emit renders one agent event.
func (p *Printer) Emit(e agent.Event) {
	switch e.Kind {
	case agent.EventDelta:
		p.stream.WriteString(e.Text)

	case agent.EventMessage:
		if p.stream.Len() == 0 {
			return
		}
		io.WriteString(p.out, p.r.markdown(p.stream.String())+"\n")
		p.stream.Reset()

	case agent.EventToolResult:
		io.WriteString(p.err, p.r.result(e.Call, e.Text, e.Outcome)+"\n\n")
	}
}
