package ext

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/sys"
	"tractor.dev/wanix/fs/pipe"

	"github.com/evacchi/ernest/internal/agent"
	"github.com/evacchi/ernest/internal/llm"
	"github.com/evacchi/ernest/internal/wasi"
)

const (
	describeTimeout = 10 * time.Second
	guestRoot       = "/"
	vfsRoot         = "."
)

var errExited = errors.New("exited")

// Plugin is one running extension. It serves as an agent.Hook; hooks the
// guest did not subscribe to are no-ops.
type Plugin struct {
	name string
	rt   wazero.Runtime
	sys  *wasi.System
	stop context.CancelFunc
	rpc  *pipe.PortFile

	replies chan reply
	done    chan struct{} // closed when the guest exits
	exitErr error         // valid after done is closed

	mu     sync.Mutex // one request in flight
	nextID int

	tools    []*Tool
	commands []*Command
	hooks    map[string]bool
}

// start boots the guest in its own goroutine and runs describe.
func start(ctx context.Context, cache wazero.CompilationCache, name string, wasm []byte,
	workdir string, s Session, log io.Writer) (*Plugin, error) {

	hostEnd, guestEnd := newPipe()
	root, err := namespace(workdir, s, guestEnd)
	if err != nil {
		return nil, err
	}

	// The guest outlives ctx; stop kills it (CloseOnContextDone).
	runCtx, stop := context.WithCancel(context.Background())
	cfg := wazero.NewRuntimeConfig().WithCompilationCache(cache).WithCloseOnContextDone(true)
	out := newPrefixWriter(log, name)

	p := &Plugin{
		name:    name,
		rt:      wazero.NewRuntimeWithConfig(runCtx, cfg),
		sys:     wasi.NewSystem(root, wasi.WithArgs(name), wasi.WithStdio(nil, out, out)),
		stop:    stop,
		rpc:     hostEnd,
		replies: make(chan reply),
		done:    make(chan struct{}),
		hooks:   map[string]bool{},
	}

	mod, err := p.prepare(ctx, wasm)
	if err != nil {
		close(p.done)
		return nil, errors.Join(err, p.close(ctx))
	}

	go p.run(runCtx, mod)
	go p.readLoop()

	dctx, cancel := context.WithTimeout(ctx, describeTimeout)
	defer cancel()
	if err := p.describe(dctx); err != nil {
		return nil, errors.Join(err, p.close(ctx))
	}
	return p, nil
}

// prepare wires WASI to the VFS and compiles the module.
func (p *Plugin) prepare(ctx context.Context, wasm []byte) (wazero.CompiledModule, error) {
	if _, err := p.sys.Preopen(guestRoot, vfsRoot); err != nil {
		return nil, err
	}
	if _, err := wasi.Instantiate(ctx, p.rt, p.sys); err != nil {
		return nil, err
	}
	return p.rt.CompileModule(ctx, wasm)
}

// run executes _start until the guest exits, then unblocks readers.
func (p *Plugin) run(ctx context.Context, mod wazero.CompiledModule) {
	_, err := p.rt.InstantiateModule(ctx, mod, wazero.NewModuleConfig().WithName(p.name))
	p.exitErr = exitError(err)
	close(p.done)
	p.rpc.Port.Close()
}

func exitError(err error) error {
	var exit *sys.ExitError
	if err == nil || (errors.As(err, &exit) && exit.ExitCode() == 0) {
		return errExited
	}
	return err
}

// readLoop turns reply lines into values until the pipe closes.
func (p *Plugin) readLoop() {
	defer close(p.replies)

	rd := bufio.NewReader(p.rpc)
	for {
		line, err := rd.ReadBytes('\n')
		if err != nil {
			return
		}

		var r reply
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}

		select {
		case p.replies <- r:
		case <-p.done:
			return
		}
	}
}

// do sends req and waits for the reply with the same id. Replies to
// requests abandoned on ctx cancellation are skipped.
func (p *Plugin) do(ctx context.Context, req request) (reply, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.nextID++
	req.ID = p.nextID
	line, err := json.Marshal(req)
	if err != nil {
		return reply{}, err
	}
	if _, err := p.rpc.Write(append(line, '\n')); err != nil {
		return reply{}, p.dead()
	}

	for {
		select {
		case r, ok := <-p.replies:
			if !ok {
				return reply{}, p.dead()
			}
			if r.ID != req.ID {
				continue
			}
			if r.Error != "" {
				return r, errors.New(r.Error)
			}
			return r, nil

		case <-ctx.Done():
			return reply{}, ctx.Err()
		}
	}
}

func (p *Plugin) dead() error {
	<-p.done
	return fmt.Errorf("extension %s: %w", p.name, p.exitErr)
}

func (p *Plugin) describe(ctx context.Context) error {
	r, err := p.do(ctx, request{Op: opDescribe})
	if err != nil {
		return fmt.Errorf("extension %s: describe: %w", p.name, err)
	}

	for _, t := range r.Tools {
		spec := llm.ToolSpec{Name: t.Name, Description: t.Description, Params: t.Parameters}
		p.tools = append(p.tools, &Tool{p: p, spec: spec})
	}
	for _, c := range r.Commands {
		p.commands = append(p.commands, &Command{p: p, name: c.Name, desc: c.Description})
	}
	for _, h := range r.Hooks {
		p.hooks[h] = true
	}
	return nil
}

// close stops the guest. Closing the pipe unblocks a guest parked in a
// read, which context cancellation alone cannot interrupt.
func (p *Plugin) close(ctx context.Context) error {
	p.stop()
	p.rpc.Port.Close()
	<-p.done
	return errors.Join(p.rt.Close(ctx), p.sys.Close())
}

// Name is the extension file name without extension.
func (p *Plugin) Name() string { return p.name }

// Tools returns the tools the guest declared.
func (p *Plugin) Tools() []*Tool { return p.tools }

// Commands returns the slash commands the guest declared.
func (p *Plugin) Commands() []*Command { return p.commands }

// OnToolCall asks the guest whether call may run.
func (p *Plugin) OnToolCall(ctx context.Context, call llm.ToolCall) (agent.Decision, error) {
	if !p.hooks[eventToolCall] {
		return agent.Decision{}, nil
	}

	r, err := p.do(ctx, request{Op: opHook, Event: eventToolCall, Call: toWire(call)})
	if err != nil {
		return agent.Decision{}, err
	}
	if r.Block {
		return agent.Decision{Verdict: agent.Block, Reason: r.Reason}, nil
	}
	return agent.Decision{}, nil
}

// OnToolResult lets the guest rewrite a tool result. No output in the reply
// keeps the original.
func (p *Plugin) OnToolResult(ctx context.Context, call llm.ToolCall, out string) (string, error) {
	if !p.hooks[eventToolResult] {
		return out, nil
	}

	r, err := p.do(ctx, request{Op: opHook, Event: eventToolResult, Call: toWire(call), Output: &out})
	if err != nil {
		return "", err
	}
	if r.Output == nil {
		return out, nil
	}
	return *r.Output, nil
}

func toWire(c llm.ToolCall) *wireCall {
	return &wireCall{ID: c.ID, Name: c.Name, Args: c.Args}
}

// Tool is a guest-provided tool.
type Tool struct {
	p    *Plugin
	spec llm.ToolSpec
}

func (t *Tool) Spec() llm.ToolSpec { return t.spec }

func (t *Tool) Run(ctx context.Context, args json.RawMessage) (string, error) {
	r, err := t.p.do(ctx, request{Op: opCall, Name: t.spec.Name, Args: args})
	if err != nil {
		return "", err
	}
	if r.Output == nil {
		return "", nil
	}
	return *r.Output, nil
}

// Command is a guest-provided slash command, e.g. "/model gpt-5".
type Command struct {
	p    *Plugin
	name string
	desc string
}

func (c *Command) Name() string        { return c.name }
func (c *Command) Description() string { return c.desc }

// Run executes the command with the text after its name.
func (c *Command) Run(ctx context.Context, input string) (string, error) {
	r, err := c.p.do(ctx, request{Op: opCommand, Name: c.name, Input: input})
	if err != nil {
		return "", err
	}
	if r.Output == nil {
		return "", nil
	}
	return *r.Output, nil
}
