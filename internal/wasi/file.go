package wasi

import (
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	wfs "tractor.dev/wanix/fs"
)

// fdEntry is a single entry in the file descriptor table. It represents one of:
//   - a standard stream (character device) backed by reader/writer;
//   - an open regular file backed by an fs.File;
//   - a directory, identified only by its path within the backing VFS.
type fdEntry struct {
	fileType    FileType
	fdflags     FDFlags
	rights      Rights
	rightsInher Rights

	// path is the entry's location within the backing VFS. It is set for files
	// and directories ("." denotes the VFS root).
	path string

	// preopenName is the guest-visible name of a preopened directory, or "".
	preopenName string

	// file is the open handle for regular files (nil for directories and
	// standard streams).
	file fs.File

	// reader / writer back the standard streams.
	reader io.Reader
	writer io.Writer

	appendMode bool

	// dirCache holds the materialized directory listing for fd_readdir; it is
	// (re)built whenever iteration restarts at cookie 0.
	dirCache []dirEntryInfo
}

// FileStat holds the fields of a WASI filestat structure.
type FileStat struct {
	Dev      uint64
	Ino      uint64
	FileType FileType
	Nlink    uint64
	Size     uint64
	Atime    uint64 // nanoseconds since the Unix epoch
	Mtime    uint64
	Ctime    uint64
}

// FDStat holds the fields of a WASI fdstat structure.
type FDStat struct {
	FileType         FileType
	Flags            FDFlags
	RightsBase       Rights
	RightsInheriting Rights
}

// dirEntryInfo is a single directory entry as returned to fd_readdir.
type dirEntryInfo struct {
	Next uint64
	Ino  uint64
	Type FileType
	Name string
}

// inodeOf derives a stable, non-zero inode number from a path.
func inodeOf(p string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(p))
	v := h.Sum64()
	if v == 0 {
		v = 1
	}
	return v
}

// resolve joins a path supplied by the guest to a directory entry, rejecting
// attempts to escape the directory. It returns the location within the backing
// VFS.
func (s *System) resolve(dir *fdEntry, name string) (string, Errno) {
	if dir.fileType != DirectoryType {
		return "", ENOTDIR
	}
	clean := path.Clean(name)
	if strings.HasPrefix(clean, "/") || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", ENOTCAPABLE
	}
	if clean == "." {
		clean = ""
	}
	full := clean
	if dir.path != "" && dir.path != "." {
		full = path.Join(dir.path, clean)
	}
	if full == "" {
		full = "."
	}
	if !wfs.ValidPath(full) {
		return "", EINVAL
	}
	return full, ESUCCESS
}

// statPath stats a location in the VFS, following symlinks unless nofollow.
func (s *System) statPath(full string, follow bool) (FileStat, Errno) {
	var (
		info fs.FileInfo
		err  error
	)
	if follow {
		info, err = wfs.Stat(s.fsys, full)
	} else {
		info, err = wfs.Lstat(s.fsys, full)
	}
	if err != nil {
		return FileStat{}, MakeErrno(err)
	}
	return fileStatOf(full, info), ESUCCESS
}

// fileStatOf builds a FileStat from an fs.FileInfo.
func fileStatOf(full string, info fs.FileInfo) FileStat {
	mtime := uint64(info.ModTime().UnixNano())
	return FileStat{
		Dev:      1,
		Ino:      inodeOf(full),
		FileType: fileTypeOf(info.Mode()),
		Nlink:    1,
		Size:     uint64(info.Size()),
		Atime:    mtime,
		Mtime:    mtime,
		Ctime:    mtime,
	}
}

// openFlags converts WASI open flags and rights into os package flags.
func openFlags(open OpenFlags, rights Rights) int {
	var flags int
	readable := rights.Has(FDReadRight)
	writable := rights.Has(FDWriteRight)
	switch {
	case readable && writable:
		flags = os.O_RDWR
	case writable:
		flags = os.O_WRONLY
	default:
		flags = os.O_RDONLY
	}
	if open.Has(OpenCreate) {
		flags |= os.O_CREATE
	}
	if open.Has(OpenExclusive) {
		flags |= os.O_EXCL
	}
	if open.Has(OpenTruncate) {
		flags |= os.O_TRUNC
	}
	// Note: O_APPEND is deliberately NOT set on the underlying file. Append
	// behavior is implemented in write() by seeking to the end first, so that
	// fd_fdstat_set_flags can turn append mode on and off at runtime.
	return flags
}

// PathOpen opens name relative to the directory descriptor dir.
func (s *System) pathOpen(dir *fdEntry, lookup LookupFlags, name string, open OpenFlags, base, inheriting Rights, fdflags FDFlags) (FD, Errno) {
	full, errno := s.resolve(dir, name)
	if errno != ESUCCESS {
		return -1, errno
	}

	// Restrict requested rights to those the directory may pass on.
	base &= dir.rightsInher
	inheriting &= dir.rightsInher

	// Truncating requires the right to set a file's size on the directory.
	if open.Has(OpenTruncate) && !dir.rights.Has(PathFileStatSetSizeRight) {
		return -1, ENOTCAPABLE
	}

	// Determine whether the target is a directory. A missing target is only an
	// error now if we are not creating it.
	st, statErrno := s.statPath(full, lookup.Has(SymlinkFollow))
	isDir := statErrno == ESUCCESS && st.FileType == DirectoryType

	if open.Has(OpenDirectory) || (isDir && !open.Has(OpenCreate)) {
		if statErrno != ESUCCESS {
			return -1, statErrno
		}
		if st.FileType != DirectoryType {
			return -1, ENOTDIR
		}
		e := &fdEntry{
			fileType:    DirectoryType,
			fdflags:     fdflags,
			rights:      base & DirectoryRights,
			rightsInher: inheriting,
			path:        full,
		}
		return s.register(e), ESUCCESS
	}

	if open.Has(OpenExclusive) && statErrno == ESUCCESS {
		return -1, EEXIST
	}

	flags := openFlags(open, base)
	f, err := wfs.OpenFile(s.fsys, full, flags, 0o644)
	if err != nil {
		return -1, MakeErrno(err)
	}
	e := &fdEntry{
		fileType:    RegularFileType,
		fdflags:     fdflags,
		rights:      base,
		rightsInher: inheriting,
		path:        full,
		file:        f,
		appendMode:  fdflags.Has(Append),
	}
	return s.register(e), ESUCCESS
}

// fdFileStat returns the attributes of an open descriptor.
func (s *System) fdFileStat(e *fdEntry) (FileStat, Errno) {
	switch {
	case e.reader != nil || e.writer != nil:
		// Standard streams report their detected device type with zeroed
		// metadata (a terminal is a character device; a pipe or buffer is not).
		return FileStat{FileType: e.fileType, Nlink: 1}, ESUCCESS
	case e.file != nil:
		info, err := e.file.Stat()
		if err != nil {
			return FileStat{}, MakeErrno(err)
		}
		return fileStatOf(e.path, info), ESUCCESS
	default:
		return s.statPath(e.path, true)
	}
}

// read fills the iovecs from the descriptor's current offset.
func (s *System) read(e *fdEntry, iovecs [][]byte) (int, Errno) {
	r := e.reader
	if r == nil {
		if e.file == nil {
			return 0, EBADF
		}
		r = e.file
	}
	total := 0
	for _, iov := range iovecs {
		if len(iov) == 0 {
			continue
		}
		n, err := r.Read(iov)
		total += n
		if err == io.EOF {
			break
		}
		if err != nil {
			if total > 0 {
				break
			}
			return 0, MakeErrno(err)
		}
		if n < len(iov) {
			break
		}
	}
	return total, ESUCCESS
}

// write consumes the iovecs, writing to the descriptor's current offset.
func (s *System) write(e *fdEntry, iovecs [][]byte) (int, Errno) {
	if e.writer == nil && e.file == nil {
		return 0, EBADF
	}
	if e.appendMode && e.file != nil {
		if _, err := wfs.Seek(e.file, 0, io.SeekEnd); err != nil {
			return 0, MakeErrno(err)
		}
	}
	total := 0
	for _, iov := range iovecs {
		if len(iov) == 0 {
			continue
		}
		var (
			n   int
			err error
		)
		if e.writer != nil {
			n, err = e.writer.Write(iov)
		} else {
			n, err = wfs.Write(e.file, iov)
		}
		total += n
		if err != nil {
			return total, MakeErrno(err)
		}
	}
	return total, ESUCCESS
}

func (s *System) pread(e *fdEntry, iovecs [][]byte, offset uint64) (int, Errno) {
	if e.file == nil {
		return 0, ESPIPE
	}
	total := 0
	off := int64(offset)
	for _, iov := range iovecs {
		if len(iov) == 0 {
			continue
		}
		n, err := wfs.ReadAt(e.file, iov, off)
		total += n
		off += int64(n)
		if err == io.EOF {
			break
		}
		if err != nil {
			if total > 0 {
				break
			}
			return 0, MakeErrno(err)
		}
		if n < len(iov) {
			break
		}
	}
	return total, ESUCCESS
}

func (s *System) pwrite(e *fdEntry, iovecs [][]byte, offset uint64) (int, Errno) {
	if e.file == nil {
		return 0, ESPIPE
	}
	total := 0
	off := int64(offset)
	for _, iov := range iovecs {
		if len(iov) == 0 {
			continue
		}
		n, err := wfs.WriteAt(e.file, iov, off)
		total += n
		off += int64(n)
		if err != nil {
			return total, MakeErrno(err)
		}
	}
	return total, ESUCCESS
}

func (s *System) seek(e *fdEntry, delta int64, whence Whence) (uint64, Errno) {
	// Directories support only rewinding (and reporting the current position):
	// a zero delta seeks to the start and resets the fd_readdir cursor, which is
	// how runtimes such as Go re-read a directory. Any real seek is rejected
	// with EISDIR, as WASI expects for directories.
	if e.fileType == DirectoryType {
		if delta == 0 {
			if whence == SeekStart {
				e.dirCache = nil
			}
			return 0, ESUCCESS
		}
		return 0, EISDIR
	}
	if e.file == nil {
		return 0, ESPIPE
	}
	off, err := wfs.Seek(e.file, delta, int(whence))
	if err != nil {
		return 0, MakeErrno(err)
	}
	return uint64(off), ESUCCESS
}

// readdir returns directory entries starting at cookie. The listing (including
// "." and "..") is cached on the entry and rebuilt whenever cookie is 0.
func (s *System) readdir(e *fdEntry, cookie uint64) ([]dirEntryInfo, Errno) {
	if e.fileType != DirectoryType {
		return nil, ENOTDIR
	}
	if cookie == 0 || e.dirCache == nil {
		entries, err := wfs.ReadDir(s.fsys, e.path)
		if err != nil {
			return nil, MakeErrno(err)
		}
		cache := make([]dirEntryInfo, 0, len(entries)+2)
		cache = append(cache,
			dirEntryInfo{Next: 1, Ino: inodeOf(e.path), Type: DirectoryType, Name: "."},
			dirEntryInfo{Next: 2, Ino: inodeOf(path.Dir(e.path)), Type: DirectoryType, Name: ".."},
		)
		for i, de := range entries {
			child := de.Name()
			full := child
			if e.path != "" && e.path != "." {
				full = path.Join(e.path, child)
			}
			cache = append(cache, dirEntryInfo{
				Next: uint64(i) + 3,
				Ino:  inodeOf(full),
				Type: fileTypeOf(de.Type()),
				Name: child,
			})
		}
		e.dirCache = cache
	}
	if cookie >= uint64(len(e.dirCache)) {
		return nil, ESUCCESS
	}
	return e.dirCache[cookie:], ESUCCESS
}

// now returns the current wall-clock time.
func (s *System) now() time.Time { return s.realtime() }
