package wasi

import (
	"crypto/rand"
	"io"
	"sync"
	"time"

	wfs "tractor.dev/wanix/fs"
)

// System is a WASI snapshot preview 1 implementation whose entire file system
// is served by a tractor.dev/wanix/fs.FS (a VFS). It also carries the process
// environment: command-line arguments, environment variables, standard streams,
// clocks and a source of randomness.
//
// A System is wired to a wazero runtime with Instantiate. It is safe for use by
// a single guest instance; create one System per instance.
type System struct {
	fsys wfs.FS

	args    []string
	environ []string

	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	rand io.Reader

	// realtime returns wall-clock time; monotonic returns a monotonically
	// increasing count of nanoseconds. Both are pluggable for testing.
	realtime  func() time.Time
	monotonic func() int64

	// exit, if set, is invoked by proc_exit before the guest is torn down.
	exit func(int)

	mu       sync.Mutex
	table    map[FD]*fdEntry
	nextFD   FD
	preopens []FD // in the order they should be discovered (fd 3, 4, ...)
}

// Option configures a System.
type Option func(*System)

// WithArgs sets the command-line arguments (argv[0] is the program name).
func WithArgs(args ...string) Option {
	return func(s *System) { s.args = args }
}

// WithEnviron sets the environment variables, each in "KEY=VALUE" form.
func WithEnviron(environ ...string) Option {
	return func(s *System) { s.environ = environ }
}

// WithStdio sets the standard input, output and error streams. A nil stream is
// replaced by a no-op (EOF on read, discard on write).
func WithStdio(stdin io.Reader, stdout, stderr io.Writer) Option {
	return func(s *System) {
		s.stdin, s.stdout, s.stderr = stdin, stdout, stderr
	}
}

// WithRand sets the source of randomness used by random_get.
func WithRand(r io.Reader) Option {
	return func(s *System) { s.rand = r }
}

// WithClocks overrides the realtime and monotonic clocks. Either may be nil to
// keep the default.
func WithClocks(realtime func() time.Time, monotonic func() int64) Option {
	return func(s *System) {
		if realtime != nil {
			s.realtime = realtime
		}
		if monotonic != nil {
			s.monotonic = monotonic
		}
	}
}

// WithExit registers a callback invoked by proc_exit. It is primarily useful
// for tests; production code should rely on the exit code surfaced by wazero.
func WithExit(fn func(int)) Option {
	return func(s *System) { s.exit = fn }
}

// NewSystem creates a System backed by fsys. Standard streams default to a
// discarding sink, randomness to crypto/rand, and the clocks to the wall clock.
//
// Callers typically follow up with one or more calls to Preopen before
// instantiating the guest.
func NewSystem(fsys wfs.FS, opts ...Option) *System {
	epoch := time.Now()
	s := &System{
		fsys:      fsys,
		stdin:     eofReader{},
		stdout:    io.Discard,
		stderr:    io.Discard,
		rand:      rand.Reader,
		realtime:  time.Now,
		monotonic: func() int64 { return int64(time.Since(epoch)) },
		table:     make(map[FD]*fdEntry),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.installStdio()
	return s
}

// installStdio registers file descriptors 0, 1 and 2 as character devices.
//
// Terminal detection (reporting a real TTY differently from a pipe or file) is
// intentionally deferred; standard streams are always character devices for now.
func (s *System) installStdio() {
	// A nil stream (e.g. from WithStdio(nil, …)) becomes a no-op so that its
	// descriptor is still a valid, stat-able character device.
	if s.stdin == nil {
		s.stdin = eofReader{}
	}
	if s.stdout == nil {
		s.stdout = io.Discard
	}
	if s.stderr == nil {
		s.stderr = io.Discard
	}
	s.table[0] = &fdEntry{fileType: CharacterDeviceType, rights: TTYRights, reader: s.stdin}
	s.table[1] = &fdEntry{fileType: CharacterDeviceType, rights: TTYRights, writer: s.stdout}
	s.table[2] = &fdEntry{fileType: CharacterDeviceType, rights: TTYRights, writer: s.stderr}
	if s.nextFD < 3 {
		s.nextFD = 3
	}
}

// Preopen exposes the directory at fsPath (a path within the backing VFS, "."
// for the root) to the guest under the name guestName (for example "/" or "."),
// returning the file descriptor assigned to it.
//
// Guests discover preopens by scanning descriptors from 3 upward with
// fd_prestat_get, so Preopen should be called before instantiation and the
// returned descriptor is informational.
func (s *System) Preopen(guestName, fsPath string) (FD, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if fsPath == "" {
		fsPath = "."
	}
	fd := s.nextFD
	s.nextFD++
	s.table[fd] = &fdEntry{
		fileType:    DirectoryType,
		rights:      DirectoryRights,
		rightsInher: DirectoryRights | FileRights,
		path:        fsPath,
		preopenName: guestName,
	}
	s.preopens = append(s.preopens, fd)
	return fd, nil
}

// FS returns the backing virtual file system.
func (s *System) FS() wfs.FS { return s.fsys }

// Close releases every open descriptor.
func (s *System) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for fd, e := range s.table {
		if e.file != nil {
			e.file.Close()
		}
		delete(s.table, fd)
	}
	return nil
}

// lookup returns the entry for fd, requiring that it holds every right in
// required.
func (s *System) lookup(fd FD, required Rights) (*fdEntry, Errno) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.table[fd]
	if e == nil {
		return nil, EBADF
	}
	if required != 0 && !e.rights.Has(required) {
		return nil, ENOTCAPABLE
	}
	return e, ESUCCESS
}

// register inserts e under a fresh descriptor number and returns it.
func (s *System) register(e *fdEntry) FD {
	s.mu.Lock()
	defer s.mu.Unlock()
	fd := s.nextFD
	s.nextFD++
	s.table[fd] = e
	return fd
}

// closeFD removes and closes the descriptor fd.
func (s *System) closeFD(fd FD) Errno {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.table[fd]
	if e == nil {
		return EBADF
	}
	delete(s.table, fd)
	if e.file != nil {
		if err := e.file.Close(); err != nil {
			return MakeErrno(err)
		}
	}
	return ESUCCESS
}

// renumber atomically moves the descriptor from onto to, closing any prior to.
func (s *System) renumber(from, to FD) Errno {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.table[from]
	if e == nil {
		return EBADF
	}
	if from == to {
		return ESUCCESS
	}
	// The destination must be an existing descriptor: fd_renumber replaces it,
	// it does not create arbitrary descriptor numbers.
	old := s.table[to]
	if old == nil {
		return EBADF
	}
	if old.file != nil {
		old.file.Close()
	}
	s.table[to] = e
	delete(s.table, from)
	return ESUCCESS
}

// eofReader is an io.Reader that is always at EOF.
type eofReader struct{}

func (eofReader) Read([]byte) (int, error) { return 0, io.EOF }

// SizesGet reports the count and total byte size (including NUL terminators) of
// a list of strings, matching the shape expected by args_sizes_get and
// environ_sizes_get.
func sizesGet(values []string) (count, size int) {
	for _, v := range values {
		size += len(v) + 1
	}
	return len(values), size
}
