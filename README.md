# ernest

Minimal coding agent in Go, in the spirit of [pi](https://github.com/badlogic/pi-mono).
OpenAI models, four built-in tools, wasm extensions.

```
 cmd/ernest ──► agent ──► llm.Provider ◄── openai (SSE)
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
```

`-model` or `ERNEST_MODEL` picks the model (default `gpt-5`).
`OPENAI_BASE_URL` points at any Chat Completions compatible endpoint.

## Extensions

Any `*.wasm` in `.ernest/extensions/` is loaded at startup.

```
GOOS=wasip1 GOARCH=wasm go build -o .ernest/extensions/reverse.wasm ./examples/reverse
```

An extension is a long-lived WASI command. It talks to the host only through
files; there are no custom imports or exports. The guest file system is a
[wanix](https://github.com/tractordev/wanix) VFS served by `internal/wasi`
(from [wazero-wasi-wanix](https://github.com/evacchi/wazero-wasi-wanix)):

```
/work           workspace, copy-on-write: guest writes never reach disk
/agent/rpc      duplex pipe to the host
/agent/model    current model name
/agent/history  conversation so far, JSON
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

Go extensions can use `sdk/`; see `examples/reverse`.
