// Command ernest is a minimal coding agent.
//
//	ernest              interactive UI
//	ernest -p "prompt"  one prompt, then exit
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync/atomic"

	"github.com/evacchi/ernest/internal/agent"
	"github.com/evacchi/ernest/internal/ext"
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

	apiResponses = "responses"
	apiChat      = "chat"
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
		a, host, _, err := assemble(p, wd, ui.NewPrinter().Emit, os.Stderr)
		if err != nil {
			return err
		}
		defer host.Close(context.Background())

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return a.Prompt(ctx, *oneShot)
	}

	app := ui.NewApp(p.Model)
	a, host, cmds, err := assemble(p, wd, app.Emit, app.Log())
	if err != nil {
		return err
	}
	defer host.Close(context.Background())
	return app.Run(a.Prompt, cmds)
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

// assemble wires provider, built-in tools and extensions into an agent,
// and turns extension commands into UI slash commands. Extensions read
// history lazily, after the agent exists.
func assemble(p provider, wd string, emit func(agent.Event), log io.Writer) (*agent.Agent, *ext.Host, []ui.Command, error) {
	var current atomic.Pointer[agent.Agent]
	session := ext.Session{
		Model:    p.Model,
		SetModel: p.SetModel,
		Models:   p.Models,
		History: func() []llm.Message {
			if a := current.Load(); a != nil {
				return a.History()
			}
			return nil
		},
	}

	host := ext.NewHost(wd, session, log)
	plugins, err := host.LoadDir(context.Background(), extDir)
	if err != nil {
		host.Close(context.Background())
		return nil, nil, nil, err
	}

	ts := []agent.Tool{tools.Read{}, tools.Write{}, tools.Edit{}, tools.Bash{}}
	var hooks []agent.Hook
	var cmds []ui.Command
	for _, pl := range plugins {
		for _, t := range pl.Tools() {
			ts = append(ts, t)
		}
		for _, c := range pl.Commands() {
			cmds = append(cmds, uiCommand(c))
		}
		hooks = append(hooks, pl)
	}

	a := agent.New(p, fmt.Sprintf(systemPrompt, wd), ts, hooks, emit)
	current.Store(a)
	return a, host, cmds, nil
}

// uiCommand adapts an extension command to the UI.
func uiCommand(c *ext.Command) ui.Command {
	return ui.Command{
		Name:        c.Name(),
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
