package ui

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"sync/atomic"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/evacchi/ernest/internal/agent"
	"github.com/evacchi/ernest/internal/llm"
)

const (
	inputMaxHeight = 8
	placeholder    = "Ask ernest…"
	thinking       = "thinking…"
	helpLine       = "enter send · ctrl+j newline · esc interrupt · ctrl+d quit"

	keyEnter  = "enter"
	keyEsc    = "esc"
	keyCtrlC  = "ctrl+c"
	keyCtrlD  = "ctrl+d"
	errPrefix = "ernest: "
)

// PromptFunc runs one user prompt to completion.
type PromptFunc func(ctx context.Context, text string) error

// App is the interactive terminal UI. Completed blocks are printed into
// the terminal scrollback; only the in-flight answer, the running tool and
// the input stay in the live area.
//
//	scrollback  › user prompt / ● tool results / answers
//	──────────
//	live        streaming answer (markdown) · spinner + running tool
//	            › input
//	            model · help
type App struct {
	model string
	prog  atomic.Pointer[tea.Program]
}

// NewApp returns an app labelled with the model name.
func NewApp(model string) *App {
	return &App{model: model}
}

// Emit forwards an agent event to the running UI.
func (a *App) Emit(e agent.Event) {
	if p := a.prog.Load(); p != nil {
		p.Send(eventMsg(e))
	}
}

// Log returns a writer for complete lines (e.g. extension output). Lines are
// printed above the UI while it runs, to stderr otherwise.
func (a *App) Log() io.Writer {
	return logWriter{a}
}

type logWriter struct{ a *App }

func (w logWriter) Write(b []byte) (int, error) {
	p := w.a.prog.Load()
	if p == nil {
		return os.Stderr.Write(b)
	}
	p.Send(logMsg(strings.TrimRight(string(b), "\n")))
	return len(b), nil
}

// Run shows the UI until the user quits.
func (a *App) Run(prompt PromptFunc) error {
	dark := lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	m := newModel(a.model, dark, prompt)

	p := tea.NewProgram(m)
	a.prog.Store(p)
	defer a.prog.Store(nil)

	_, err := p.Run()
	m.interrupt()
	return err
}

type (
	eventMsg   agent.Event
	logMsg     string
	doneMsg    struct{ err error }
	flushedMsg struct{}
)

type model struct {
	r      *renderer
	input  textarea.Model
	spin   spinner.Model
	prompt PromptFunc
	name   string

	running bool
	cancel  context.CancelFunc
	pending *llm.ToolCall

	stream strings.Builder // in-flight assistant markdown
	live   string          // rendered stream
	dirty  bool

	// outbox keeps scrollback prints ordered: one Println batch at a time.
	outbox   []string
	flushing bool
}

func newModel(name string, dark bool, prompt PromptFunc) *model {
	in := textarea.New()
	in.SetStyles(textarea.DefaultStyles(dark))
	in.ShowLineNumbers = false
	in.Prompt = "› "
	in.Placeholder = placeholder
	in.DynamicHeight = true
	in.MaxHeight = inputMaxHeight
	in.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("ctrl+j", "shift+enter", "alt+enter"))

	return &model{
		r:      newRenderer(dark),
		input:  in,
		spin:   spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		prompt: prompt,
		name:   name,
	}
}

func (m *model) Init() tea.Cmd {
	return m.input.Focus()
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.r.setWidth(msg.Width)
		m.input.SetWidth(msg.Width)
		m.dirty = true
		return m, nil

	case tea.KeyPressMsg:
		if cmd, handled := m.onKey(msg); handled {
			return m, cmd
		}

	case eventMsg:
		return m, m.onEvent(agent.Event(msg))

	case doneMsg:
		return m, m.onDone(msg.err)

	case logMsg:
		return m, m.print(m.r.pal.dim.Render(string(msg)))

	case flushedMsg:
		m.flushing = false
		return m, m.flush()

	case spinner.TickMsg:
		if !m.running {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// onKey handles app-level keys; the rest go to the input.
func (m *model) onKey(k tea.KeyPressMsg) (tea.Cmd, bool) {
	switch k.String() {
	case keyEnter:
		return m.submit(), true

	case keyEsc:
		m.interrupt()
		return nil, true

	case keyCtrlC:
		// Interrupt first, then clear input, then quit.
		switch {
		case m.running:
			m.interrupt()
		case m.input.Value() != "":
			m.input.Reset()
		default:
			return tea.Quit, true
		}
		return nil, true

	case keyCtrlD:
		if m.running || m.input.Value() != "" {
			return nil, false
		}
		return tea.Quit, true
	}
	return nil, false
}

// submit starts the agent on the input text in a background command.
func (m *model) submit() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	if text == "" || m.running {
		return nil
	}
	m.input.Reset()

	ctx, cancel := context.WithCancel(context.Background())
	m.running, m.cancel = true, cancel
	prompt := m.prompt
	run := func() tea.Msg { return doneMsg{prompt(ctx, text)} }

	return tea.Batch(m.print(m.r.user(text)), run, m.spin.Tick)
}

func (m *model) interrupt() {
	if m.cancel != nil {
		m.cancel()
	}
}

func (m *model) onEvent(e agent.Event) tea.Cmd {
	switch e.Kind {
	case agent.EventDelta:
		m.stream.WriteString(e.Text)
		m.dirty = true

	case agent.EventMessage:
		return m.commitStream()

	case agent.EventToolCall:
		m.pending = &e.Call

	case agent.EventToolResult:
		m.pending = nil
		return m.print(m.r.result(e.Call, e.Text, e.Outcome))
	}
	return nil
}

func (m *model) onDone(err error) tea.Cmd {
	m.running, m.cancel, m.pending = false, nil, nil
	cmd := m.commitStream()

	switch {
	case err == nil:
		return cmd
	case errors.Is(err, context.Canceled):
		return tea.Batch(cmd, m.print(m.r.pal.warn.Render("interrupted")))
	}
	return tea.Batch(cmd, m.print(m.r.errorLine(err)))
}

// commitStream moves the finished answer from the live area to scrollback.
func (m *model) commitStream() tea.Cmd {
	if m.stream.Len() == 0 {
		return nil
	}

	text := m.r.markdown(m.stream.String())
	m.stream.Reset()
	m.live, m.dirty = "", false
	return m.print(text)
}

// print queues s for scrollback.
func (m *model) print(s string) tea.Cmd {
	if s == "" {
		return nil
	}
	m.outbox = append(m.outbox, s)
	return m.flush()
}

// flush prints queued blocks in order. Println commands run concurrently,
// so a new batch starts only after the previous one reports back.
func (m *model) flush() tea.Cmd {
	if m.flushing || len(m.outbox) == 0 {
		return nil
	}

	cmds := make([]tea.Cmd, 0, len(m.outbox)+1)
	for _, s := range m.outbox {
		cmds = append(cmds, tea.Println(s+"\n"))
	}
	cmds = append(cmds, func() tea.Msg { return flushedMsg{} })

	m.outbox, m.flushing = nil, true
	return tea.Sequence(cmds...)
}

func (m *model) View() tea.View {
	var b strings.Builder

	if m.stream.Len() > 0 {
		if m.dirty {
			m.live, m.dirty = m.r.markdown(m.stream.String()), false
		}
		b.WriteString(m.live + "\n\n")
	}

	switch {
	case m.pending != nil:
		b.WriteString(m.spin.View() + " " + m.r.call(*m.pending) + "\n\n")
	case m.running && m.stream.Len() == 0:
		b.WriteString(m.spin.View() + " " + m.r.pal.dim.Render(thinking) + "\n\n")
	}

	b.WriteString(m.input.View() + "\n")
	b.WriteString(m.r.pal.dim.Render(m.name + " · " + helpLine))
	return tea.NewView(b.String())
}
