package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

const (
	slash   = "/"
	cmdHelp = "help"
)

// Command is a slash command typed in the input, e.g. "/model gpt-5".
type Command struct {
	Name        string
	Description string
	Run         func(ctx context.Context, input string) (string, error)
}

// parseSlash splits "/model gpt-5" into ("model", "gpt-5").
func parseSlash(text string) (name, input string, ok bool) {
	rest, ok := strings.CutPrefix(text, slash)
	if !ok || rest == "" {
		return "", "", false
	}
	name, input, _ = strings.Cut(rest, " ")
	return name, strings.TrimSpace(input), true
}

// help lists commands, e.g. "/model  Show or switch the model".
func (r *renderer) help(cmds map[string]Command) string {
	names := make([]string, 0, len(cmds))
	for n := range cmds {
		names = append(names, n)
	}
	sort.Strings(names)

	lines := []string{fmt.Sprintf("%-10s %s", slash+cmdHelp, r.pal.dim.Render("List commands"))}
	for _, n := range names {
		lines = append(lines, fmt.Sprintf("%-10s %s", slash+n, r.pal.dim.Render(cmds[n].Description)))
	}
	return strings.Join(lines, "\n")
}
