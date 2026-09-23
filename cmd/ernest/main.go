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
)

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
	flag.Parse()

	key := os.Getenv(envAPIKey)
	if key == "" {
		return fmt.Errorf("%s not set", envAPIKey)
	}

	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	// One-shot: plain rendering, Ctrl-C cancels the prompt.
	if *oneShot != "" {
		a, host, err := assemble(*model, key, wd, ui.NewPrinter().Emit, os.Stderr)
		if err != nil {
			return err
		}
		defer host.Close(context.Background())

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return a.Prompt(ctx, *oneShot)
	}

	app := ui.NewApp(*model)
	a, host, err := assemble(*model, key, wd, app.Emit, app.Log())
	if err != nil {
		return err
	}
	defer host.Close(context.Background())
	return app.Run(a.Prompt)
}

// assemble wires provider, built-in tools and extensions into an agent.
// Extensions read history lazily, after the agent exists.
func assemble(model, key, wd string, emit func(agent.Event), log io.Writer) (*agent.Agent, *ext.Host, error) {
	var current atomic.Pointer[agent.Agent]
	session := ext.Session{
		Model: model,
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
		return nil, nil, err
	}

	ts := []agent.Tool{tools.Read{}, tools.Write{}, tools.Edit{}, tools.Bash{}}
	var hooks []agent.Hook
	for _, p := range plugins {
		for _, t := range p.Tools() {
			ts = append(ts, t)
		}
		hooks = append(hooks, p)
	}

	provider := openai.New(openai.Config{BaseURL: os.Getenv(envBaseURL), APIKey: key, Model: model})
	a := agent.New(provider, fmt.Sprintf(systemPrompt, wd), ts, hooks, emit)
	current.Store(a)
	return a, host, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
