// Command ernest is a minimal coding agent.
//
//	ernest              interactive UI
//	ernest -p "prompt"  one prompt, then exit
//	ernest cmd [args]   interactive UI, running /cmd args first
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/evacchi/ernest/internal/agent"
	"github.com/evacchi/ernest/internal/ext"
	"github.com/evacchi/ernest/internal/history"
	"github.com/evacchi/ernest/internal/llm"
	"github.com/evacchi/ernest/internal/llm/openai"
	"github.com/evacchi/ernest/internal/tools"
	"github.com/evacchi/ernest/internal/ui"
)

const (
	envAPIKey  = "OPENAI_API_KEY"
	envBaseURL = "OPENAI_BASE_URL"
	envModel   = "ERNEST_MODEL"

	defaultModel = "gpt-6-luna"
	extDir       = ".ernest/extensions"
	sessionDir   = ".ernest/sessions"

	apiResponses = "responses"
	apiChat      = "chat"

	cmdReload    = "reload"
	noExtensions = "no extensions"

	cmdShell    = "sh"
	shellPrefix = "!"
	slash       = "/"
)

var (
	errBusy        = errors.New("a prompt is running")
	errArgsOneShot = errors.New("commands need the interactive UI; drop -p")
)

// provider is an llm.Provider whose model can change at runtime.
type provider interface {
	llm.Provider
	Model() string
	SetModel(string) error
	Models(context.Context) ([]string, error)
}

const systemPrompt = `You are ernest, a minimal coding agent.
Use the tools to read, write and edit files and to run commands.
Be concise. Working directory: %s`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	model := flag.String("model", envOr(envModel, defaultModel), "model name")
	oneShot := flag.String("p", "", "run one prompt and exit")
	api := flag.String("api", apiResponses, "OpenAI API: responses or chat")
	flag.Parse()

	// "ernest resume" starts the UI with "/resume".
	start := ""
	if flag.NArg() > 0 {
		start = slash + strings.Join(flag.Args(), " ")
	}

	key := os.Getenv(envAPIKey)
	if key == "" {
		return fmt.Errorf("%s not set", envAPIKey)
	}

	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	p, err := newProvider(*api, openai.Config{BaseURL: os.Getenv(envBaseURL), APIKey: key, Model: *model})
	if err != nil {
		return err
	}

	// One-shot: plain rendering, Ctrl-C cancels the prompt.
	if *oneShot != "" {
		if start != "" {
			return errArgsOneShot
		}
		s, err := assemble(p, wd, ui.NewPrinter().Emit, os.Stderr)
		if err != nil {
			return err
		}
		defer s.host.Close(context.Background())

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return s.agent.Prompt(ctx, *oneShot)
	}

	app := ui.NewApp(p.Model)
	s, err := assemble(p, wd, app.Emit, app.Log())
	if err != nil {
		return err
	}
	defer s.host.Close(context.Background())

	s.onReload = app.SetCommands
	s.onResume = app.Replay
	return app.Run(s.prompt, s.uiCommands(), ui.Info{Workdir: wd, Extensions: s.extensions}, start)
}

func newProvider(api string, cfg openai.Config) (provider, error) {
	switch api {
	case apiResponses:
		return openai.NewResponses(cfg), nil
	case apiChat:
		return openai.New(cfg), nil
	}
	return nil, fmt.Errorf("unknown -api %q: want %s or %s", api, apiResponses, apiChat)
}

// session is an assembled agent with its extensions, saved to store
// under id as it goes.
type session struct {
	agent      *agent.Agent
	host       *ext.Host
	commands   []ui.Command
	extensions []string

	store *history.Store
	id    atomic.Pointer[string]

	busy     atomic.Bool         // a prompt is running
	onReload func([]ui.Command)  // receives the new command list
	onResume func([]llm.Message) // receives a resumed conversation
}

// assemble wires provider, built-in tools and extensions into an agent,
// and turns extension commands into UI slash commands. Extensions read
// history lazily, after the agent exists.
func assemble(p provider, wd string, emit func(agent.Event), log io.Writer) (*session, error) {
	s := &session{store: history.NewStore(sessionDir)}
	id := history.NewID()
	s.id.Store(&id)

	var current atomic.Pointer[agent.Agent]
	state := ext.Session{
		Model:    p.Model,
		SetModel: p.SetModel,
		Models:   p.Models,
		History: func() []llm.Message {
			if a := current.Load(); a != nil {
				return a.History()
			}
			return nil
		},
		ID:       func() string { return *s.id.Load() },
		Resume:   s.resume,
		Sessions: s.sessions,
	}

	host := ext.NewHost(wd, state, log)
	plugins, err := host.LoadDir(context.Background(), extDir)
	if err != nil {
		host.Close(context.Background())
		return nil, err
	}

	s.host = host
	ts, hooks := s.use(plugins)
	s.agent = agent.New(p, fmt.Sprintf(systemPrompt, wd), ts, hooks, emit)
	s.agent.Record(func(m llm.Message) {
		if err := s.store.Append(*s.id.Load(), m); err != nil {
			fmt.Fprintln(log, "history:", err)
		}
	})
	current.Store(s.agent)
	return s, nil
}

// resume swaps the conversation for saved session id, which later
// messages extend. Refused mid-prompt, like reload.
func (s *session) resume(id string) error {
	if s.busy.Load() {
		return errBusy
	}

	msgs, err := s.store.Load(id)
	if err != nil {
		return err
	}
	s.agent.Restore(msgs)
	s.id.Store(&id)

	if s.onResume != nil {
		s.onResume(msgs)
	}
	return nil
}

// sessions lists saved sessions for /agent/sessions: "<id> <title>".
func (s *session) sessions() ([]string, error) {
	infos, err := s.store.List()
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(infos))
	for _, in := range infos {
		out = append(out, in.ID+" "+in.Title)
	}
	return out, nil
}

// use records plugins' commands and names and returns the agent's tools
// (built-ins first, so extensions cannot shadow them) and hooks.
func (s *session) use(plugins []*ext.Plugin) ([]agent.Tool, []agent.Hook) {
	ts := []agent.Tool{tools.Read{}, tools.Write{}, tools.Edit{}, tools.Bash{}}
	var hooks []agent.Hook
	s.commands, s.extensions = nil, nil

	for _, pl := range plugins {
		s.extensions = append(s.extensions, pl.Name())
		for _, t := range pl.Tools() {
			ts = append(ts, t)
		}
		for _, c := range pl.Commands() {
			s.commands = append(s.commands, uiCommand(c))
		}
		hooks = append(hooks, pl)
	}
	return ts, hooks
}

// prompt runs the agent, marking the session busy so /reload waits.
func (s *session) prompt(ctx context.Context, text string) error {
	s.busy.Store(true)
	defer s.busy.Store(false)
	return s.agent.Prompt(ctx, text)
}

// uiCommands is the extension commands plus the built-ins /reload and /sh.
// Built-ins come last so they win name clashes.
func (s *session) uiCommands() []ui.Command {
	reload := ui.Command{
		Name:        cmdReload,
		Description: "Reload extensions from " + extDir,
		Run:         s.reload,
	}
	sh := ui.Command{
		Name:        cmdShell,
		Prefix:      shellPrefix,
		Description: "Run a shell command; the model sees it",
		Run:         s.shell,
	}
	return append(slices.Clone(s.commands), reload, sh)
}

// shell runs a user's shell command on the host, like the bash tool,
// and notes command and output in the history for the model:
//
//	!ls  →  "User ran a shell command:\n$ ls\ngo.mod ..."
func (s *session) shell(ctx context.Context, input string) (ui.Result, error) {
	if input == "" {
		return ui.Result{}, errors.New("usage: " + shellPrefix + "command")
	}

	args, err := json.Marshal(map[string]string{"command": input})
	if err != nil {
		return ui.Result{}, err
	}
	out, err := tools.Bash{}.Run(ctx, args)
	if err != nil {
		return ui.Result{}, err
	}

	s.agent.Note("User ran a shell command:\n$ " + input + "\n" + out)
	return ui.Result{Output: out}, nil
}

// reload restarts every extension from extDir and swaps the agent's tools,
// hooks and the UI commands. Refused mid-prompt: in-flight tool calls
// would hit stopped extensions.
func (s *session) reload(ctx context.Context, _ string) (ui.Result, error) {
	if s.busy.Load() {
		return ui.Result{}, errors.New("a prompt is running; reload when it finishes")
	}

	plugins, err := s.host.Reload(ctx, extDir)
	ts, hooks := s.use(plugins)
	s.agent.SetTools(ts, hooks)
	if s.onReload != nil {
		s.onReload(s.uiCommands())
	}

	loaded := noExtensions
	if len(s.extensions) > 0 {
		loaded = strings.Join(s.extensions, ", ")
	}
	if err != nil {
		return ui.Result{}, fmt.Errorf("reloaded %s; failed: %w", loaded, err)
	}
	return ui.Result{Output: "reloaded " + loaded}, nil
}

// uiCommand adapts an extension command to the UI.
func uiCommand(c *ext.Command) ui.Command {
	return ui.Command{
		Name:        c.Name(),
		Prefix:      c.Prefix(),
		Description: c.Description(),
		Run: func(ctx context.Context, input string) (ui.Result, error) {
			r, err := c.Run(ctx, input)
			return ui.Result{Output: r.Output, Choices: r.Choices, Selected: r.Selected}, err
		},
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
