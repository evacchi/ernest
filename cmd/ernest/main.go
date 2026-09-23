// Command ernest is a minimal coding agent.
//
//	ernest              interactive REPL
//	ernest -p "prompt"  one prompt, then exit
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"

	"github.com/evacchi/ernest/internal/agent"
	"github.com/evacchi/ernest/internal/ext"
	"github.com/evacchi/ernest/internal/llm"
	"github.com/evacchi/ernest/internal/llm/openai"
	"github.com/evacchi/ernest/internal/tools"
)

const (
	envAPIKey  = "OPENAI_API_KEY"
	envBaseURL = "OPENAI_BASE_URL"
	envModel   = "ERNEST_MODEL"

	defaultModel = "gpt-5"
	extDir       = ".ernest/extensions"
	promptMark   = "> "
	previewLen   = 200
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

	a, host, err := assemble(*model, key, wd)
	if err != nil {
		return err
	}
	defer host.Close(context.Background())

	if *oneShot != "" {
		return prompt(a, *oneShot)
	}
	return repl(a)
}

// assemble wires provider, built-in tools and extensions into an agent.
// Extensions read history lazily, after the agent exists.
func assemble(model, key, wd string) (*agent.Agent, *ext.Host, error) {
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

	host := ext.NewHost(wd, session, os.Stderr)
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
	a := agent.New(provider, fmt.Sprintf(systemPrompt, wd), ts, hooks, printEvent)
	current.Store(a)
	return a, host, nil
}

// repl reads one prompt per line until EOF.
func repl(a *agent.Agent) error {
	sc := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print(promptMark)
		if !sc.Scan() {
			return sc.Err()
		}

		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		// A failed prompt is reported; the session goes on.
		if err := prompt(a, line); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
	}
}

// prompt runs one prompt; Ctrl-C cancels it without leaving the REPL.
func prompt(a *agent.Agent, text string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	err := a.Prompt(ctx, text)
	if errors.Is(err, context.Canceled) {
		return errors.New("interrupted")
	}
	return err
}

// printEvent streams text to stdout and tool activity to stderr.
func printEvent(e agent.Event) {
	switch e.Kind {
	case agent.EventDelta:
		fmt.Print(e.Text)
	case agent.EventMessage:
		if e.Text != "" {
			fmt.Println()
		}
	case agent.EventToolCall:
		fmt.Fprintf(os.Stderr, "→ %s %s\n", e.Call.Name, preview(e.Call.Args))
	case agent.EventToolResult:
		fmt.Fprintf(os.Stderr, "  %s\n", preview(e.Text))
	}
}

// preview flattens s to one line of at most previewLen bytes.
func preview(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= previewLen {
		return s
	}
	return s[:previewLen] + "…"
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
