package tools

import (
	"errors"
	"os/exec"
)

// ErrNoSandbox means this platform has no sandbox backend.
var ErrNoSandbox = errors.New("sandbox not supported on this platform")

// Sandbox confines a command before it starts.
type Sandbox interface {
	Wrap(cmd *exec.Cmd) error
}
