package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync/atomic"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"

	"github.com/evacchi/ernest/internal/agent"
	"github.com/evacchi/ernest/internal/llm"
)

const (
	inputMaxHeight = 8
	placeholder    = "Ask ernest…"
	promptFirst    = "❯ "
	promptNext     = "  "
	promptWidth    = 2
	rule           = "─"
	statusIndent   = "  "
	thinking       = "thinking…"
	helpLine       = "enter send · ctrl+j newline · esc interrupt · /help · ctrl+d quit"

	keyEnter  = "enter"
	keyEsc    = "esc"
	keyCtrlC  = "ctrl+c"
	keyCtrlD  = "ctrl+d"
	keyTab    = "tab"
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
//	            ────────────
//	            ❯ input, grows to inputMaxHeight lines
//	            ────────────
//	              model · help
type App struct {
	model func() string
	prog  atomic.Pointer[tea.Program]
}

// NewApp returns an app whose footer shows model(), read on every frame so
// a switch (e.g. /model) shows up at once.
func NewApp(model func() string) *App {
	return &App{model: model}
}

// Emit forwards an agent event to the running UI.
func (a *App) Emit(e agent.Event) {
	if p := a.prog.Load(); p != nil {
		p.Send(eventMsg(e))
	}
}

// SetCommands replaces the slash commands, e.g. after /reload.
func (a *App) SetCommands(cmds []Command) {
	if p := a.prog.Load(); p != nil {
		p.Send(commandsMsg(cmds))
	}
}

// Replay prints a resumed conversation into the scrollback.
func (a *App) Replay(msgs []llm.Message) {
	if p := a.prog.Load(); p != nil {
		p.Send(replayMsg(msgs))
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

// Run shows the splash banner, then the UI until the user quits.
// A non-empty start is submitted first, as if typed, e.g. "/resume".
func (a *App) Run(prompt PromptFunc, cmds []Command, info Info, start string) error {
	dark := lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	m := newModel(a.model, dark, prompt, cmds)
	m.start = start

	// The banner prints before bubbletea reports the size; ask directly.
	if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil {
		m.r.setWidth(w)
	}
	m.splash = m.r.banner(a.model(), info)

	p := tea.NewProgram(m)
	a.prog.Store(p)
	defer a.prog.Store(nil)

	_, err := p.Run()
	m.interrupt()
	return err
}

type (
	eventMsg agent.Event
	logMsg   string
	doneMsg  struct{ err error }
	cmdMsg   struct {
		name string
		res  Result
		err  error
	}
	flushedMsg  struct{}
	commandsMsg []Command
	replayMsg   []llm.Message
)

type model struct {
	r      *renderer
	input  textarea.Model
	spin   spinner.Model
	prompt PromptFunc
	name   func() string
	cmds   map[string]Command

	running bool
	cancel  context.CancelFunc
	pending *llm.ToolCall
	pick    *picker // non-nil while choosing a command argument
	busy    string  // command in flight, e.g. "model"
	splash  string  // printed once on start
	start   string  // submitted once on start

	stream strings.Builder // in-flight assistant markdown
	live   string          // rendered stream
	dirty  bool

	// outbox keeps scrollback prints ordered: one Println batch at a time.
	outbox   []string
	flushing bool
}

func newModel(name func() string, dark bool, prompt PromptFunc, cmds []Command) *model {
	r := newRenderer(dark)

	in := textarea.New()
	in.SetStyles(inputStyles(r.pal, dark))
	in.ShowLineNumbers = false
	in.SetPromptFunc(promptWidth, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return promptFirst
		}
		return promptNext
	})
	in.Placeholder = placeholder
	// Real terminal cursor: keeps the input text free of cursor escapes,
	// so View can color the typed "/command".
	in.SetVirtualCursor(false)
	in.DynamicHeight = true
	in.MaxHeight = inputMaxHeight
	in.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("ctrl+j", "shift+enter", "alt+enter"))

	return &model{
		r:      r,
		input:  in,
		spin:   spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		prompt: prompt,
		name:   name,
		cmds:   byName(cmds),
	}
}

func byName(cmds []Command) map[string]Command {
	m := make(map[string]Command, len(cmds))
	for _, c := range cmds {
		m[c.Name] = c
	}
	return m
}

// inputStyles drops the default cursor-line background: the input sits
// between two rules instead, on the terminal's own background.
func inputStyles(pal palette, dark bool) textarea.Styles {
	s := textarea.DefaultStyles(dark)
	for _, st := range []*textarea.StyleState{&s.Focused, &s.Blurred} {
		st.CursorLine = lipgloss.NewStyle()
		st.Text = lipgloss.NewStyle()
		st.Prompt = pal.user
		st.Placeholder = pal.dim
	}
	return s
}

func (m *model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.input.Focus(), m.print(m.splash)}
	if m.start != "" {
		m.input.SetValue(m.start)
		cmds = append(cmds, m.submit())
	}
	return tea.Batch(cmds...)
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

	case cmdMsg:
		m.busy = ""
		switch {
		case msg.err != nil:
			return m, m.print(m.r.errorLine(msg.err))
		case len(msg.res.Choices) > 0:
			m.pick = newPicker(msg.name, msg.res.Choices, msg.res.Selected)
			return m, nil
		}
		return m, m.print(msg.res.Output)

	case commandsMsg:
		m.cmds = byName(msg)
		return m, nil

	case replayMsg:
		var cmds []tea.Cmd
		for _, block := range m.r.transcript(msg) {
			cmds = append(cmds, m.print(block))
		}
		return m, tea.Batch(cmds...)

	case logMsg:
		return m, m.print(m.r.pal.dim.Render(string(msg)))

	case flushedMsg:
		m.flushing = false
		return m, m.flush()

	case spinner.TickMsg:
		if !m.running && m.busy == "" {
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
	if m.pick != nil {
		return m.onPickKey(k), true
	}

	switch k.String() {
	case keyTab:
		return nil, m.complete()

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

// onPickKey drives the picker; a pick re-runs its command with it.
func (m *model) onPickKey(k tea.KeyPressMsg) tea.Cmd {
	switch m.pick.key(k) {
	case pickCancel:
		m.pick = nil
	case pickDone:
		choice, _ := m.pick.choice()
		name := m.pick.command
		m.pick = nil
		return tea.Batch(m.print(m.r.user(slash+name+" "+choice)), m.command(name, choice))
	}
	return nil
}

// complete replaces a partial "/mo" with the first matching command.
func (m *model) complete() bool {
	token, ok := typedCommand(m.input.Value())
	if !ok || strings.Contains(m.input.Value(), " ") {
		return false
	}

	names := suggest(m.cmds, strings.TrimPrefix(token, slash))
	if len(names) == 0 {
		return true
	}
	m.input.SetValue(slash + names[0] + " ")
	return true
}

// submit starts the agent on the input text in a background command.
func (m *model) submit() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	if text == "" || m.running {
		return nil
	}
	m.input.Reset()

	if name, input, ok := parsePrefix(m.cmds, text); ok {
		return tea.Batch(m.print(m.r.user(text)), m.command(name, input))
	}
	if name, input, ok := parseSlash(text); ok {
		return tea.Batch(m.print(m.r.user(text)), m.command(name, input))
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.running, m.cancel = true, cancel
	prompt := m.prompt
	run := func() tea.Msg { return doneMsg{prompt(ctx, text)} }

	return tea.Batch(m.print(m.r.user(text)), run, m.spin.Tick)
}

// command runs a slash command in the background; /help is built in.
// Unique prefixes resolve: "/mo" runs "/model".
func (m *model) command(name, input string) tea.Cmd {
	resolved, ok := resolve(m.cmds, name)
	if !ok {
		return m.print(m.r.errorLine(fmt.Errorf("unknown command /%s (try /help)", name)))
	}
	if resolved == cmdHelp {
		return m.print(m.r.help(m.cmds))
	}

	c := m.cmds[resolved]
	m.busy = resolved
	run := func() tea.Msg {
		res, err := c.Run(context.Background(), input)
		return cmdMsg{name: resolved, res: res, err: err}
	}
	return tea.Batch(run, m.spin.Tick)
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
	case m.busy != "":
		b.WriteString(m.spin.View() + " " + m.r.pal.dim.Render("running "+slash+m.busy+"…") + "\n\n")
	}

	bar := m.r.pal.dim.Render(strings.Repeat(rule, m.r.width))
	if m.pick != nil {
		b.WriteString(bar + "\n" + m.r.picker(m.pick) + "\n" + bar + "\n")
		b.WriteString(statusIndent + m.r.pal.dim.Render(pickerHelp))
		return tea.NewView(b.String())
	}

	b.WriteString(bar + "\n")
	top := strings.Count(b.String(), "\n")
	b.WriteString(m.colorCommand(m.input.View()) + "\n" + bar + "\n")
	b.WriteString(m.status())

	v := tea.NewView(b.String())
	if c := m.input.Cursor(); c != nil {
		c.Y += top
		v.Cursor = c
	}
	return v
}

// colorCommand paints a typed "/command" token: accent when it names or
// uniquely prefixes a command, error color otherwise. A command prefix
// such as "!" is painted in accent.
func (m *model) colorCommand(view string) string {
	if name, _, ok := parsePrefix(m.cmds, m.input.Value()); ok {
		p := m.cmds[name].Prefix
		return strings.Replace(view, p, m.r.pal.user.Render(p), 1)
	}

	token, ok := typedCommand(m.input.Value())
	if !ok || token == slash {
		return view
	}

	style := m.r.pal.err
	if _, known := resolve(m.cmds, strings.TrimPrefix(token, slash)); known {
		style = m.r.pal.user
	}
	return strings.Replace(view, token, style.Render(token), 1)
}

// status shows command suggestions while typing "/name", else model and help.
func (m *model) status() string {
	value := m.input.Value()
	if token, ok := typedCommand(value); ok && !strings.ContainsAny(value, " \n") {
		if names := suggest(m.cmds, strings.TrimPrefix(token, slash)); len(names) > 0 {
			return m.r.suggestions(m.cmds, names)
		}
	}
	return statusIndent + m.r.pal.dim.Render(m.name()+" · "+helpLine)
}
