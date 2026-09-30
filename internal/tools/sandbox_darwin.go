package tools

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	sandboxExec = "/usr/bin/sandbox-exec"
	gitDir      = ".git"
)

// Later rules win in SBPL, so the allow below narrows the write deny.
// Paths arrive as -D parameters, never interpolated into the profile.
const profileHead = `(version 1)
(allow default)
(deny network*)
(deny file-write*)
(allow file-write* (literal "/dev/null") (literal "/dev/tty") (regex #"^/dev/ttys[0-9]+$")`

// seatbelt runs commands under macOS Seatbelt: whole FS readable, writes
// confined to roots, no network.
type seatbelt struct {
	roots []string // symlink-resolved: Seatbelt matches real paths
}

// NewSandbox returns a Seatbelt sandbox that can write only under writable.
func NewSandbox(writable ...string) (Sandbox, error) {
	if _, err := exec.LookPath(sandboxExec); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNoSandbox, err)
	}

	// e.g. /tmp → /private/tmp
	roots := make([]string, 0, len(writable))
	for _, w := range writable {
		real, err := filepath.EvalSymlinks(w)
		if err != nil {
			return nil, err
		}
		roots = append(roots, real)
	}
	return seatbelt{roots: roots}, nil
}

// Wrap rewrites cmd into: sandbox-exec -D W0=… -p profile <cmd> <args>.
func (s seatbelt) Wrap(cmd *exec.Cmd) error {
	var profile strings.Builder
	profile.WriteString(profileHead)

	args := []string{sandboxExec}
	for i, r := range s.roots {
		name := fmt.Sprintf("W%d", i)
		args = append(args, "-D", name+"="+r)
		fmt.Fprintf(&profile, ` (subpath (param %q))`, name)
	}
	profile.WriteString(")\n")

	// Deny after allow: .git stays read-only, so history cannot be rewritten.
	for i, r := range s.roots {
		name := fmt.Sprintf("G%d", i)
		args = append(args, "-D", name+"="+filepath.Join(r, gitDir))
		fmt.Fprintf(&profile, "(deny file-write* (subpath (param %q)))\n", name)
	}

	args = append(args, "-p", profile.String(), cmd.Path)
	cmd.Args = append(args, cmd.Args[1:]...)
	cmd.Path = sandboxExec
	return nil
}
