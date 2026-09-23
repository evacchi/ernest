package wasi

import (
	"io"
	"io/fs"
	"testing"

	wfs "tractor.dev/wanix/fs"
	"tractor.dev/wanix/fs/memfs"
)

// newTestSystem returns a System over an empty memfs with the root preopened at
// fd 3, plus the root directory entry.
func newTestSystem(t *testing.T) (*System, *fdEntry) {
	t.Helper()
	s := NewSystem(memfs.New())
	if _, err := s.Preopen("/", "."); err != nil {
		t.Fatal(err)
	}
	dir, errno := s.lookup(3, 0)
	if errno != ESUCCESS {
		t.Fatalf("lookup preopen: %v", errno)
	}
	return s, dir
}

func TestResolveSandbox(t *testing.T) {
	s, dir := newTestSystem(t)

	cases := []struct {
		name  string
		want  string
		errno Errno
	}{
		{"foo.txt", "foo.txt", ESUCCESS},
		{"a/b/c", "a/b/c", ESUCCESS},
		{".", ".", ESUCCESS},
		{"./x", "x", ESUCCESS},
		{"a/../b", "b", ESUCCESS},
		{"..", "", ENOTCAPABLE},
		{"../escape", "", ENOTCAPABLE},
		{"/abs", "", ENOTCAPABLE},
	}
	for _, tc := range cases {
		got, errno := s.resolve(dir, tc.name)
		if errno != tc.errno {
			t.Errorf("resolve(%q) errno = %v, want %v", tc.name, errno, tc.errno)
			continue
		}
		if errno == ESUCCESS && got != tc.want {
			t.Errorf("resolve(%q) = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestPathOpenReadWrite(t *testing.T) {
	s, dir := newTestSystem(t)

	fd, errno := s.pathOpen(dir, 0, "greeting.txt", OpenCreate, FileRights, FileRights, 0)
	if errno != ESUCCESS {
		t.Fatalf("pathOpen create: %v", errno)
	}
	e, errno := s.lookup(fd, 0)
	if errno != ESUCCESS {
		t.Fatal(errno)
	}
	n, errno := s.write(e, [][]byte{[]byte("hello "), []byte("world")})
	if errno != ESUCCESS || n != 11 {
		t.Fatalf("write = %d, %v; want 11, ESUCCESS", n, errno)
	}
	if errno := s.closeFD(fd); errno != ESUCCESS {
		t.Fatalf("close: %v", errno)
	}

	// Reopen read-only and read the contents back.
	fd, errno = s.pathOpen(dir, 0, "greeting.txt", 0, FDReadRight, FDReadRight, 0)
	if errno != ESUCCESS {
		t.Fatalf("pathOpen read: %v", errno)
	}
	e, _ = s.lookup(fd, 0)
	buf := make([]byte, 32)
	n, errno = s.read(e, [][]byte{buf})
	if errno != ESUCCESS {
		t.Fatalf("read: %v", errno)
	}
	if got := string(buf[:n]); got != "hello world" {
		t.Fatalf("read = %q, want %q", got, "hello world")
	}

	// The write must be observable directly through the backing VFS.
	if b, err := wfs.ReadFile(s.fsys, "greeting.txt"); err != nil || string(b) != "hello world" {
		t.Fatalf("VFS content = %q, %v", b, err)
	}
}

func TestReaddir(t *testing.T) {
	s, dir := newTestSystem(t)
	if err := wfs.WriteFile(s.fsys, "a.txt", []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := wfs.Mkdir(s.fsys, "d", 0o755); err != nil {
		t.Fatal(err)
	}

	entries, errno := s.readdir(dir, 0)
	if errno != ESUCCESS {
		t.Fatalf("readdir: %v", errno)
	}
	got := map[string]FileType{}
	for _, e := range entries {
		got[e.Name] = e.Type
	}
	if got["."] != DirectoryType || got[".."] != DirectoryType {
		t.Errorf("readdir missing dot entries: %v", got)
	}
	if got["a.txt"] != RegularFileType {
		t.Errorf("a.txt type = %v, want RegularFileType", got["a.txt"])
	}
	if got["d"] != DirectoryType {
		t.Errorf("d type = %v, want DirectoryType", got["d"])
	}
}

func TestSeekAndPread(t *testing.T) {
	s, dir := newTestSystem(t)
	if err := wfs.WriteFile(s.fsys, "data", []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	fd, errno := s.pathOpen(dir, 0, "data", 0, FDReadRight|FDSeekRight, FDReadRight|FDSeekRight, 0)
	if errno != ESUCCESS {
		t.Fatal(errno)
	}
	e, _ := s.lookup(fd, 0)

	off, errno := s.seek(e, 5, SeekStart)
	if errno != ESUCCESS || off != 5 {
		t.Fatalf("seek = %d, %v; want 5", off, errno)
	}
	buf := make([]byte, 3)
	n, errno := s.read(e, [][]byte{buf})
	if errno != ESUCCESS || string(buf[:n]) != "567" {
		t.Fatalf("read after seek = %q, %v; want 567", buf[:n], errno)
	}

	// pread does not disturb the file offset.
	pbuf := make([]byte, 2)
	n, errno = s.pread(e, [][]byte{pbuf}, 0)
	if errno != ESUCCESS || string(pbuf[:n]) != "01" {
		t.Fatalf("pread = %q, %v; want 01", pbuf[:n], errno)
	}
}

func TestMakeErrno(t *testing.T) {
	cases := []struct {
		err  error
		want Errno
	}{
		{nil, ESUCCESS},
		{io.EOF, ESUCCESS},
		{fs.ErrNotExist, ENOENT},
		{fs.ErrExist, EEXIST},
		{fs.ErrPermission, EACCES},
		{fs.ErrInvalid, EINVAL},
		{wfs.ErrNotEmpty, ENOTEMPTY},
	}
	for _, tc := range cases {
		if got := MakeErrno(tc.err); got != tc.want {
			t.Errorf("MakeErrno(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}
