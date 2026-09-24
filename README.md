# ernest

*the importance of being harness*

Minimal coding agent in Go, in the spirit of [pi](https://github.com/badlogic/pi-mono).
OpenAI models, four built-in tools, wasm extensions.

```
 cmd/ernest ──► ui (bubbletea / printer)
      │
      └──────► agent ──► llm.Provider ◄── openai (Responses | Chat, SSE)
                  │
                  ├──► Tool ◄── tools (read/write/edit/bash)
                  │         ◄──┐
                  └──► Hook ◄──┴── ext ──► internal/wasi ──► wanix VFS
                                    │            ▲
                                    └─ wazero ───┴── *.wasm (built with sdk/)
```

## Run

```
export OPENAI_API_KEY=...
go run ./cmd/ernest                 # REPL
go run ./cmd/ernest -p "list files" # one-shot
go run ./cmd/ernest resume          # REPL, running /resume first
```

`ernest cmd args` starts the REPL with `/cmd args`, for any command.

Sessions are saved to `.ernest/sessions/<id>.jsonl`, one message per
line, as they happen.

Interactive mode is a [bubbletea](https://charm.land) UI: markdown via
glamour, code and diffs highlighted with chroma. `edit`/`write` return
unified diffs, rendered with line numbers; so is `git diff` output from
`bash`. Keys: enter send, ctrl+j newline, esc interrupt, ctrl+d quit.
Typing `/` colors the command and suggests matches (tab completes).
`!cmd` (or `/sh cmd`) runs a shell command on the host; command and
output are added to the history, so the model sees them.

The system prompt includes `~/.ernest/AGENTS.md`, then every `AGENTS.md`
from `/` down to the working directory. It also lists
[skills](https://agentskills.io), `<name>/SKILL.md` under
`.ernest/skills` or `.agents/skills`, in the project or in `~`; project
skills win name clashes. Only name, description and path go in the
prompt; the model reads a `SKILL.md` when a task calls for it.

`-model` or `ERNEST_MODEL` picks the model (default `gpt-6-luna`).
`-api` picks the OpenAI API: `responses` (default; reasoning models with
tools, reasoning replayed across turns) or `chat` (Chat Completions, for
compatible endpoints set via `OPENAI_BASE_URL`).

## Extensions

Any `*.wasm` in `.ernest/extensions/` is loaded at startup. `/reload`
restarts them all from that directory (not while a prompt is running).
Compiled modules are cached on disk (`os.UserCacheDir()/ernest/wazero`),
so restarts skip compiling unchanged extensions.

```
GOOS=wasip1 GOARCH=wasm go build -o .ernest/extensions/reverse.wasm ./examples/reverse
GOOS=wasip1 GOARCH=wasm go build -o .ernest/extensions/model.wasm ./examples/model
GOOS=wasip1 GOARCH=wasm go build -o .ernest/extensions/hello.wasm ./examples/hello
GOOS=wasip1 GOARCH=wasm go build -o .ernest/extensions/resume.wasm ./examples/resume
```

- `examples/reverse`: a tool plus a hook blocking `rm -rf`.
- `examples/model`: the `/model` slash command. It only reads and writes
  files under `/agent/config`; the host switches the provider. Without a
  name it returns the chat models from `/agent/config/models` as choices,
  shown as a picker.
- `examples/hello`: the `/hello [name]` slash command, which replies with a
  greeting.
- `examples/resume`: the `/resume [id]` slash command. Without an id it
  offers `/agent/sessions` as choices; the pick is written to
  `/agent/config/session`, and the host loads and reprints it.

An extension is a long-lived WASI command. It talks to the host only through
files; there are no custom imports or exports. The guest file system is a
[wanix](https://github.com/tractordev/wanix) VFS served by `internal/wasi`
(from [wazero-wasi-wanix](https://github.com/evacchi/wazero-wasi-wanix)):

```
/work                workspace, copy-on-write: guest writes never reach disk
/agent/rpc           duplex pipe to the host
/agent/history       conversation so far, JSON
/agent/config/model  current model; writing it switches the model
/agent/config/models available models, one per line (GET /v1/models)
/agent/sessions      saved sessions, newest first: "<id> <first prompt>"
/agent/config/session current session id; writing an id resumes it
```

### Protocol

JSON lines on `/agent/rpc`. The host sends one request at a time; the guest
answers with the same `id`.

| op | request | reply |
|---|---|---|
| `describe` | — | `{tools:[{name,description,parameters}], hooks:[...]}` |
| `call` | `{name,args}` | `{output}` or `{error}` |
| `hook` | `{event:"tool_call",call}` | `{block?,reason?}` |
| `hook` | `{event:"tool_result",call,output}` | `{output?}` (absent: unchanged) |
| `command` | `{name,input}` | `{output}`, `{choices,selected?}` or `{error}` |

`describe` may also list `commands:[{name,prefix?,description}]`; they show
up as slash commands in the UI. A `prefix` symbol is a shorthand: `?why`
runs `/ask why`. A reply with `choices` opens a picker; the pick
re-runs the command with it as `input`.

Go extensions can use `sdk/`; see `examples/reverse`.
