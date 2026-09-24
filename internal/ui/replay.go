package ui

import (
	"github.com/evacchi/ernest/internal/agent"
	"github.com/evacchi/ernest/internal/llm"
)

// transcript renders a resumed conversation as the blocks it printed
// when live: user prompts, tool results, answers. Tool results find
// their call by id; the system prompt is skipped.
func (r *renderer) transcript(msgs []llm.Message) []string {
	calls := map[string]llm.ToolCall{}
	var out []string

	for _, m := range msgs {
		switch m.Role {
		case llm.RoleUser:
			out = append(out, r.user(m.Content))

		case llm.RoleAssistant:
			for _, c := range m.ToolCalls {
				calls[c.ID] = c
			}
			if m.Content != "" {
				out = append(out, r.markdown(m.Content))
			}

		case llm.RoleTool:
			out = append(out, r.result(calls[m.ToolCallID], m.Content, agent.OutcomeOK))
		}
	}
	return out
}
