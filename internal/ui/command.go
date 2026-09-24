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
// A Prefix, if set, is a shorthand: "!ls" runs "/sh ls".
type Command struct {
	Name        string
	Prefix      string
	Description string
	Run         func(ctx context.Context, input string) (Result, error)
}

// Result is a command reply: Output to show, or Choices to pick from.
// The pick re-runs the command with it as input.
type Result struct {
	Output   string
	Choices  []string
	Selected string
}

// resolve finds a command by exact name, or by a unique prefix
// ("/mo" → "model").
func resolve(cmds map[string]Command, name string) (string, bool) {
	if _, ok := cmds[name]; ok || name == cmdHelp {
		return name, true
	}

	match := ""
	for _, n := range append(sortedNames(cmds), cmdHelp) {
		if !strings.HasPrefix(n, name) {
			continue
		}
		if match != "" {
			return "", false
		}
		match = n
	}
	return match, match != ""
}

// suggest lists commands whose name starts with prefix, "help" included.
func suggest(cmds map[string]Command, prefix string) []string {
	var out []string
	for _, n := range append(sortedNames(cmds), cmdHelp) {
		if strings.HasPrefix(n, prefix) {
			out = append(out, n)
		}
	}
	return out
}

func sortedNames(cmds map[string]Command) []string {
	names := make([]string, 0, len(cmds))
	for n := range cmds {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// typedCommand returns the "/name" token being typed on the first input
// line, if any.
func typedCommand(value string) (token string, ok bool) {
	first, _, _ := strings.Cut(value, "\n")
	if !strings.HasPrefix(first, slash) {
		return "", false
	}
	token, _, _ = strings.Cut(first, " ")
	return token, true
}

// suggestions renders matching commands under the input while the
// user types "/…", the first one highlighted (tab completes it).
func (r *renderer) suggestions(cmds map[string]Command, names []string) string {
	lines := make([]string, 0, len(names))
	for i, n := range names {
		desc := "List commands"
		if c, ok := cmds[n]; ok {
			desc = c.Description
		}

		name := fmt.Sprintf("%-10s", slash+n)
		if i == 0 {
			name = r.pal.user.Render(name)
		}
		lines = append(lines, statusIndent+name+" "+r.pal.dim.Render(desc))
	}
	return strings.Join(lines, "\n")
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

// parsePrefix splits "!ls -a" into ("sh", "ls -a") when a command
// declares prefix "!". "/" is reserved for slash commands.
func parsePrefix(cmds map[string]Command, text string) (name, input string, ok bool) {
	for _, n := range sortedNames(cmds) {
		p := cmds[n].Prefix
		if p == "" || p == slash {
			continue
		}
		if rest, ok := strings.CutPrefix(text, p); ok {
			return n, strings.TrimSpace(rest), true
		}
	}
	return "", "", false
}

// help lists commands, e.g. "/model  Show or switch the model".
func (r *renderer) help(cmds map[string]Command) string {
	names := sortedNames(cmds)
	lines := []string{fmt.Sprintf("%-10s %s", slash+cmdHelp, r.pal.dim.Render("List commands"))}
	for _, n := range names {
		desc := cmds[n].Description
		if p := cmds[n].Prefix; p != "" {
			desc += " (" + p + ")"
		}
		lines = append(lines, fmt.Sprintf("%-10s %s", slash+n, r.pal.dim.Render(desc)))
	}
	return strings.Join(lines, "\n")
}
