//go:build !darwin

package tools

// NewSandbox fails closed: no backend, no sandbox.
func NewSandbox(writable ...string) (Sandbox, error) {
	return nil, ErrNoSandbox
}
