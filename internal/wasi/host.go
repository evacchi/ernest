package wasi

import (
	"context"
	"encoding/binary"
	"io"
	"runtime"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/sys"
	wfs "tractor.dev/wanix/fs"
)

// HostModuleName is the module name imported by WASI guests.
const HostModuleName = "wasi_snapshot_preview1"

var le = binary.LittleEndian

const (
	i32 = api.ValueTypeI32
	i64 = api.ValueTypeI64
)

// Instantiate builds and instantiates the wasi_snapshot_preview1 host module
// backed by s in the given runtime. The returned Closer removes the module; the
// System itself is not closed.
func Instantiate(ctx context.Context, r wazero.Runtime, s *System) (api.Closer, error) {
	b := r.NewHostModuleBuilder(HostModuleName)
	s.export(b)
	return b.Instantiate(ctx)
}

// wasiFunc is a host function that returns a WASI errno.
type wasiFunc func(ctx context.Context, mod api.Module, stack []uint64) Errno

// export registers every preview 1 function on the builder.
func (s *System) export(b wazero.HostModuleBuilder) {
	fn := func(name string, params []api.ValueType, f wasiFunc) {
		b.NewFunctionBuilder().
			WithGoModuleFunction(api.GoModuleFunc(func(ctx context.Context, mod api.Module, stack []uint64) {
				stack[0] = uint64(f(ctx, mod, stack))
			}), params, []api.ValueType{i32}).
			Export(name)
	}

	fn("args_get", []api.ValueType{i32, i32}, s.argsGet)
	fn("args_sizes_get", []api.ValueType{i32, i32}, s.argsSizesGet)
	fn("environ_get", []api.ValueType{i32, i32}, s.environGet)
	fn("environ_sizes_get", []api.ValueType{i32, i32}, s.environSizesGet)
	fn("clock_res_get", []api.ValueType{i32, i32}, s.clockResGet)
	fn("clock_time_get", []api.ValueType{i32, i64, i32}, s.clockTimeGet)
	fn("fd_advise", []api.ValueType{i32, i64, i64, i32}, s.fdAdvise)
	fn("fd_allocate", []api.ValueType{i32, i64, i64}, s.fdAllocate)
	fn("fd_close", []api.ValueType{i32}, s.fdClose)
	fn("fd_datasync", []api.ValueType{i32}, s.fdSync)
	fn("fd_fdstat_get", []api.ValueType{i32, i32}, s.fdFdstatGet)
	fn("fd_fdstat_set_flags", []api.ValueType{i32, i32}, s.fdFdstatSetFlags)
	fn("fd_fdstat_set_rights", []api.ValueType{i32, i64, i64}, s.fdFdstatSetRights)
	fn("fd_filestat_get", []api.ValueType{i32, i32}, s.fdFilestatGet)
	fn("fd_filestat_set_size", []api.ValueType{i32, i64}, s.fdFilestatSetSize)
	fn("fd_filestat_set_times", []api.ValueType{i32, i64, i64, i32}, s.fdFilestatSetTimes)
	fn("fd_pread", []api.ValueType{i32, i32, i32, i64, i32}, s.fdPread)
	fn("fd_prestat_get", []api.ValueType{i32, i32}, s.fdPrestatGet)
	fn("fd_prestat_dir_name", []api.ValueType{i32, i32, i32}, s.fdPrestatDirName)
	fn("fd_pwrite", []api.ValueType{i32, i32, i32, i64, i32}, s.fdPwrite)
	fn("fd_read", []api.ValueType{i32, i32, i32, i32}, s.fdRead)
	fn("fd_readdir", []api.ValueType{i32, i32, i32, i64, i32}, s.fdReaddir)
	fn("fd_renumber", []api.ValueType{i32, i32}, s.fdRenumber)
	fn("fd_seek", []api.ValueType{i32, i64, i32, i32}, s.fdSeek)
	fn("fd_sync", []api.ValueType{i32}, s.fdSync)
	fn("fd_tell", []api.ValueType{i32, i32}, s.fdTell)
	fn("fd_write", []api.ValueType{i32, i32, i32, i32}, s.fdWrite)
	fn("path_create_directory", []api.ValueType{i32, i32, i32}, s.pathCreateDirectory)
	fn("path_filestat_get", []api.ValueType{i32, i32, i32, i32, i32}, s.pathFilestatGet)
	fn("path_filestat_set_times", []api.ValueType{i32, i32, i32, i32, i64, i64, i32}, s.pathFilestatSetTimes)
	fn("path_link", []api.ValueType{i32, i32, i32, i32, i32, i32, i32}, s.pathLink)
	fn("path_open", []api.ValueType{i32, i32, i32, i32, i32, i64, i64, i32, i32}, s.pathOpenABI)
	fn("path_readlink", []api.ValueType{i32, i32, i32, i32, i32, i32}, s.pathReadlink)
	fn("path_remove_directory", []api.ValueType{i32, i32, i32}, s.pathRemoveDirectory)
	fn("path_rename", []api.ValueType{i32, i32, i32, i32, i32, i32}, s.pathRename)
	fn("path_symlink", []api.ValueType{i32, i32, i32, i32, i32}, s.pathSymlink)
	fn("path_unlink_file", []api.ValueType{i32, i32, i32}, s.pathUnlinkFile)
	fn("poll_oneoff", []api.ValueType{i32, i32, i32, i32}, s.pollOneoff)
	fn("proc_raise", []api.ValueType{i32}, s.procRaise)
	fn("sched_yield", []api.ValueType{}, s.schedYield)
	fn("random_get", []api.ValueType{i32, i32}, s.randomGet)
	fn("sock_accept", []api.ValueType{i32, i32, i32}, notSupported)
	fn("sock_recv", []api.ValueType{i32, i32, i32, i32, i32, i32}, notSupported)
	fn("sock_send", []api.ValueType{i32, i32, i32, i32, i32}, notSupported)
	fn("sock_shutdown", []api.ValueType{i32, i32}, s.sockShutdown)

	// proc_exit has no result and needs to unwind the guest.
	b.NewFunctionBuilder().
		WithGoModuleFunction(api.GoModuleFunc(s.procExit),
			[]api.ValueType{i32}, []api.ValueType{}).
		Export("proc_exit")
}

func notSupported(context.Context, api.Module, []uint64) Errno { return ENOTSUP }

// sockShutdown validates the descriptor even though sockets are not backed by
// the VFS: an unknown fd is EBADF and a non-socket is ENOTSOCK, matching what
// callers expect. A valid socket would be unreachable here.
func (s *System) sockShutdown(_ context.Context, _ api.Module, stack []uint64) Errno {
	e, errno := s.lookup(FD(uint32(stack[0])), 0)
	if errno != ESUCCESS {
		return errno
	}
	if e.fileType != SocketStreamType && e.fileType != SocketDGramType {
		return ENOTSOCK
	}
	return ENOTSUP
}

// --- memory helpers -------------------------------------------------------

func readString(mem api.Memory, ptr, length uint32) (string, bool) {
	b, ok := mem.Read(ptr, length)
	if !ok {
		return "", false
	}
	return string(b), true
}

// readIOVecs decodes an array of (ptr, len) iovec structures into byte slices
// aliasing guest memory.
func readIOVecs(mem api.Memory, ptr, count uint32) ([][]byte, bool) {
	vecs := make([][]byte, 0, count)
	for i := range count {
		base := ptr + i*sizeOfIOVec
		buf, ok := mem.ReadUint32Le(base)
		if !ok {
			return nil, false
		}
		length, ok := mem.ReadUint32Le(base + 4)
		if !ok {
			return nil, false
		}
		var b []byte
		if length > 0 {
			b, ok = mem.Read(buf, length)
			if !ok {
				return nil, false
			}
		}
		vecs = append(vecs, b)
	}
	return vecs, true
}

func encodeFileStat(st FileStat) []byte {
	b := make([]byte, sizeOfFileStat)
	le.PutUint64(b[0:], st.Dev)
	le.PutUint64(b[8:], st.Ino)
	b[16] = byte(st.FileType)
	le.PutUint64(b[24:], st.Nlink)
	le.PutUint64(b[32:], st.Size)
	le.PutUint64(b[40:], st.Atime)
	le.PutUint64(b[48:], st.Mtime)
	le.PutUint64(b[56:], st.Ctime)
	return b
}

func encodeFDStat(st FDStat) []byte {
	b := make([]byte, sizeOfFDStat)
	b[0] = byte(st.FileType)
	le.PutUint16(b[2:], uint16(st.Flags))
	le.PutUint64(b[8:], uint64(st.RightsBase))
	le.PutUint64(b[16:], uint64(st.RightsInheriting))
	return b
}

// --- process information --------------------------------------------------

func (s *System) storeStrings(mem api.Memory, argvPtr, bufPtr uint32, vals []string) Errno {
	offset := bufPtr
	for i, v := range vals {
		if !mem.WriteUint32Le(argvPtr+uint32(i)*4, offset) {
			return EFAULT
		}
		if len(v) > 0 && !mem.Write(offset, []byte(v)) {
			return EFAULT
		}
		if !mem.WriteByte(offset+uint32(len(v)), 0) {
			return EFAULT
		}
		offset += uint32(len(v)) + 1
	}
	return ESUCCESS
}

func (s *System) argsGet(_ context.Context, mod api.Module, stack []uint64) Errno {
	return s.storeStrings(mod.Memory(), uint32(stack[0]), uint32(stack[1]), s.args)
}

func (s *System) argsSizesGet(_ context.Context, mod api.Module, stack []uint64) Errno {
	count, size := sizesGet(s.args)
	mem := mod.Memory()
	if !mem.WriteUint32Le(uint32(stack[0]), uint32(count)) || !mem.WriteUint32Le(uint32(stack[1]), uint32(size)) {
		return EFAULT
	}
	return ESUCCESS
}

func (s *System) environGet(_ context.Context, mod api.Module, stack []uint64) Errno {
	return s.storeStrings(mod.Memory(), uint32(stack[0]), uint32(stack[1]), s.environ)
}

func (s *System) environSizesGet(_ context.Context, mod api.Module, stack []uint64) Errno {
	count, size := sizesGet(s.environ)
	mem := mod.Memory()
	if !mem.WriteUint32Le(uint32(stack[0]), uint32(count)) || !mem.WriteUint32Le(uint32(stack[1]), uint32(size)) {
		return EFAULT
	}
	return ESUCCESS
}

// --- clocks and randomness ------------------------------------------------

func (s *System) clockResGet(_ context.Context, mod api.Module, stack []uint64) Errno {
	switch ClockID(uint32(stack[0])) {
	case ClockRealtime, ClockMonotonic, ClockProcessCPUTimeID, ClockThreadCPUTimeID:
		if !mod.Memory().WriteUint64Le(uint32(stack[1]), 1) {
			return EFAULT
		}
		return ESUCCESS
	default:
		return EINVAL
	}
}

func (s *System) clockTimeGet(_ context.Context, mod api.Module, stack []uint64) Errno {
	var v uint64
	switch ClockID(uint32(stack[0])) {
	case ClockRealtime:
		v = uint64(s.now().UnixNano())
	case ClockMonotonic, ClockProcessCPUTimeID, ClockThreadCPUTimeID:
		v = uint64(s.monotonic())
	default:
		return EINVAL
	}
	if !mod.Memory().WriteUint64Le(uint32(stack[2]), v) {
		return EFAULT
	}
	return ESUCCESS
}

func (s *System) randomGet(_ context.Context, mod api.Module, stack []uint64) Errno {
	buf, ok := mod.Memory().Read(uint32(stack[0]), uint32(stack[1]))
	if !ok {
		return EFAULT
	}
	if _, err := io.ReadFull(s.rand, buf); err != nil {
		return EIO
	}
	return ESUCCESS
}

// --- file descriptor operations -------------------------------------------

func (s *System) fdClose(_ context.Context, _ api.Module, stack []uint64) Errno {
	return s.closeFD(FD(uint32(stack[0])))
}

func (s *System) fdRenumber(_ context.Context, _ api.Module, stack []uint64) Errno {
	return s.renumber(FD(uint32(stack[0])), FD(uint32(stack[1])))
}

func (s *System) fdSync(_ context.Context, _ api.Module, stack []uint64) Errno {
	e, errno := s.lookup(FD(uint32(stack[0])), 0)
	if errno != ESUCCESS {
		return errno
	}
	if e.file != nil {
		if err := wfs.Sync(e.file); err != nil && !isUnsupported(err) {
			return MakeErrno(err)
		}
	}
	return ESUCCESS
}

func (s *System) fdAdvise(_ context.Context, _ api.Module, stack []uint64) Errno {
	_, errno := s.lookup(FD(uint32(stack[0])), FDAdviseRight)
	return errno
}

func (s *System) fdAllocate(_ context.Context, _ api.Module, stack []uint64) Errno {
	_, errno := s.lookup(FD(uint32(stack[0])), FDAllocateRight)
	if errno != ESUCCESS {
		return errno
	}
	// A VFS does not guarantee preallocation; report it as unsupported (which
	// callers such as the WASI test suite accept in lieu of growing the file).
	return ENOTSUP
}

func (s *System) fdFdstatGet(_ context.Context, mod api.Module, stack []uint64) Errno {
	e, errno := s.lookup(FD(uint32(stack[0])), 0)
	if errno != ESUCCESS {
		return errno
	}
	st := FDStat{
		FileType:         e.fileType,
		Flags:            e.fdflags,
		RightsBase:       e.rights,
		RightsInheriting: e.rightsInher,
	}
	if !mod.Memory().Write(uint32(stack[1]), encodeFDStat(st)) {
		return EFAULT
	}
	return ESUCCESS
}

func (s *System) fdFdstatSetFlags(_ context.Context, _ api.Module, stack []uint64) Errno {
	e, errno := s.lookup(FD(uint32(stack[0])), FDStatSetFlagsRight)
	if errno != ESUCCESS {
		return errno
	}
	e.fdflags = FDFlags(uint32(stack[1]))
	e.appendMode = e.fdflags.Has(Append)
	return ESUCCESS
}

func (s *System) fdFdstatSetRights(_ context.Context, _ api.Module, stack []uint64) Errno {
	e, errno := s.lookup(FD(uint32(stack[0])), 0)
	if errno != ESUCCESS {
		return errno
	}
	base := Rights(stack[1])
	inheriting := Rights(stack[2])
	// Rights may only be dropped, never added.
	if base&^e.rights != 0 || inheriting&^e.rightsInher != 0 {
		return ENOTCAPABLE
	}
	e.rights = base
	e.rightsInher = inheriting
	return ESUCCESS
}

func (s *System) fdFilestatGet(_ context.Context, mod api.Module, stack []uint64) Errno {
	e, errno := s.lookup(FD(uint32(stack[0])), FDFileStatGetRight)
	if errno != ESUCCESS {
		return errno
	}
	st, errno := s.fdFileStat(e)
	if errno != ESUCCESS {
		return errno
	}
	if !mod.Memory().Write(uint32(stack[1]), encodeFileStat(st)) {
		return EFAULT
	}
	return ESUCCESS
}

func (s *System) fdFilestatSetSize(_ context.Context, _ api.Module, stack []uint64) Errno {
	e, errno := s.lookup(FD(uint32(stack[0])), FDFileStatSetSizeRight)
	if errno != ESUCCESS {
		return errno
	}
	if e.fileType == DirectoryType {
		return EISDIR
	}
	if e.file == nil {
		return EINVAL
	}
	if err := wfs.Truncate(s.fsys, e.path, int64(stack[1])); err != nil {
		return MakeErrno(err)
	}
	return ESUCCESS
}

func (s *System) fdFilestatSetTimes(_ context.Context, _ api.Module, stack []uint64) Errno {
	e, errno := s.lookup(FD(uint32(stack[0])), FDFileStatSetTimesRight)
	if errno != ESUCCESS {
		return errno
	}
	atime, mtime, ok := s.resolveTimes(stack[1], stack[2], FSTFlags(uint32(stack[3])))
	if !ok {
		return EINVAL
	}
	if err := wfs.Chtimes(s.fsys, e.path, atime, mtime); err != nil {
		return MakeErrno(err)
	}
	return ESUCCESS
}

func (s *System) fdRead(_ context.Context, mod api.Module, stack []uint64) Errno {
	e, errno := s.lookup(FD(uint32(stack[0])), FDReadRight)
	if errno != ESUCCESS {
		return errno
	}
	mem := mod.Memory()
	iovecs, ok := readIOVecs(mem, uint32(stack[1]), uint32(stack[2]))
	if !ok {
		return EFAULT
	}
	n, errno := s.read(e, iovecs)
	if errno != ESUCCESS {
		return errno
	}
	if !mem.WriteUint32Le(uint32(stack[3]), uint32(n)) {
		return EFAULT
	}
	return ESUCCESS
}

func (s *System) fdWrite(_ context.Context, mod api.Module, stack []uint64) Errno {
	e, errno := s.lookup(FD(uint32(stack[0])), FDWriteRight)
	if errno != ESUCCESS {
		return errno
	}
	mem := mod.Memory()
	iovecs, ok := readIOVecs(mem, uint32(stack[1]), uint32(stack[2]))
	if !ok {
		return EFAULT
	}
	n, errno := s.write(e, iovecs)
	if errno != ESUCCESS {
		return errno
	}
	if !mem.WriteUint32Le(uint32(stack[3]), uint32(n)) {
		return EFAULT
	}
	return ESUCCESS
}

func (s *System) fdPread(_ context.Context, mod api.Module, stack []uint64) Errno {
	e, errno := s.lookup(FD(uint32(stack[0])), FDReadRight|FDSeekRight)
	if errno != ESUCCESS {
		return errno
	}
	mem := mod.Memory()
	iovecs, ok := readIOVecs(mem, uint32(stack[1]), uint32(stack[2]))
	if !ok {
		return EFAULT
	}
	n, errno := s.pread(e, iovecs, stack[3])
	if errno != ESUCCESS {
		return errno
	}
	if !mem.WriteUint32Le(uint32(stack[4]), uint32(n)) {
		return EFAULT
	}
	return ESUCCESS
}

func (s *System) fdPwrite(_ context.Context, mod api.Module, stack []uint64) Errno {
	e, errno := s.lookup(FD(uint32(stack[0])), FDWriteRight|FDSeekRight)
	if errno != ESUCCESS {
		return errno
	}
	mem := mod.Memory()
	iovecs, ok := readIOVecs(mem, uint32(stack[1]), uint32(stack[2]))
	if !ok {
		return EFAULT
	}
	n, errno := s.pwrite(e, iovecs, stack[3])
	if errno != ESUCCESS {
		return errno
	}
	if !mem.WriteUint32Le(uint32(stack[4]), uint32(n)) {
		return EFAULT
	}
	return ESUCCESS
}

func (s *System) fdSeek(_ context.Context, mod api.Module, stack []uint64) Errno {
	e, errno := s.lookup(FD(uint32(stack[0])), FDSeekRight)
	if errno != ESUCCESS {
		return errno
	}
	off, errno := s.seek(e, int64(stack[1]), Whence(uint32(stack[2])))
	if errno != ESUCCESS {
		return errno
	}
	if !mod.Memory().WriteUint64Le(uint32(stack[3]), off) {
		return EFAULT
	}
	return ESUCCESS
}

func (s *System) fdTell(_ context.Context, mod api.Module, stack []uint64) Errno {
	e, errno := s.lookup(FD(uint32(stack[0])), FDTellRight)
	if errno != ESUCCESS {
		return errno
	}
	off, errno := s.seek(e, 0, SeekCurrent)
	if errno != ESUCCESS {
		return errno
	}
	if !mod.Memory().WriteUint64Le(uint32(stack[1]), off) {
		return EFAULT
	}
	return ESUCCESS
}

func (s *System) fdReaddir(_ context.Context, mod api.Module, stack []uint64) Errno {
	e, errno := s.lookup(FD(uint32(stack[0])), 0)
	if errno != ESUCCESS {
		return errno
	}
	// Reading a non-directory as a directory reports EBADF, which is what
	// runtimes such as Go expect when a file was opened without O_DIRECTORY.
	if e.fileType != DirectoryType {
		return EBADF
	}
	if !e.rights.Has(FDReadDirRight) {
		return ENOTCAPABLE
	}
	bufPtr := uint32(stack[1])
	bufLen := uint32(stack[2])
	cookie := stack[3]

	entries, errno := s.readdir(e, cookie)
	if errno != ESUCCESS {
		return errno
	}

	out := make([]byte, 0, bufLen)
	hdr := make([]byte, sizeOfDirent)
	for _, d := range entries {
		name := []byte(d.Name)
		le.PutUint64(hdr[0:], d.Next)
		le.PutUint64(hdr[8:], d.Ino)
		le.PutUint32(hdr[16:], uint32(len(name)))
		hdr[20] = byte(d.Type)
		hdr[21], hdr[22], hdr[23] = 0, 0, 0

		out = append(out, hdr...)
		out = append(out, name...)
		if uint32(len(out)) >= bufLen {
			break
		}
	}
	if uint32(len(out)) > bufLen {
		out = out[:bufLen]
	}
	if !mod.Memory().Write(bufPtr, out) {
		return EFAULT
	}
	if !mod.Memory().WriteUint32Le(uint32(stack[4]), uint32(len(out))) {
		return EFAULT
	}
	return ESUCCESS
}

func (s *System) fdPrestatGet(_ context.Context, mod api.Module, stack []uint64) Errno {
	e, errno := s.lookup(FD(uint32(stack[0])), 0)
	if errno != ESUCCESS {
		return errno
	}
	if e.preopenName == "" {
		return EBADF
	}
	b := make([]byte, sizeOfPreStat)
	b[0] = byte(PreOpenDir)
	le.PutUint32(b[4:], uint32(len(e.preopenName)))
	if !mod.Memory().Write(uint32(stack[1]), b) {
		return EFAULT
	}
	return ESUCCESS
}

func (s *System) fdPrestatDirName(_ context.Context, mod api.Module, stack []uint64) Errno {
	e, errno := s.lookup(FD(uint32(stack[0])), 0)
	if errno != ESUCCESS {
		return errno
	}
	if e.preopenName == "" {
		return EBADF
	}
	name := []byte(e.preopenName)
	length := uint32(stack[2])
	if length < uint32(len(name)) {
		return ENAMETOOLONG
	}
	if !mod.Memory().Write(uint32(stack[1]), name) {
		return EFAULT
	}
	return ESUCCESS
}

// --- path operations ------------------------------------------------------

func (s *System) pathOpenABI(_ context.Context, mod api.Module, stack []uint64) Errno {
	dir, errno := s.lookup(FD(uint32(stack[0])), PathOpenRight)
	if errno != ESUCCESS {
		return errno
	}
	name, ok := readString(mod.Memory(), uint32(stack[2]), uint32(stack[3]))
	if !ok {
		return EFAULT
	}
	fd, errno := s.pathOpen(dir,
		LookupFlags(uint32(stack[1])),
		name,
		OpenFlags(uint32(stack[4])),
		Rights(stack[5]),
		Rights(stack[6]),
		FDFlags(uint32(stack[7])),
	)
	if errno != ESUCCESS {
		return errno
	}
	if !mod.Memory().WriteUint32Le(uint32(stack[8]), uint32(fd)) {
		return EFAULT
	}
	return ESUCCESS
}

func (s *System) pathCreateDirectory(_ context.Context, mod api.Module, stack []uint64) Errno {
	dir, errno := s.lookup(FD(uint32(stack[0])), PathCreateDirectoryRight)
	if errno != ESUCCESS {
		return errno
	}
	name, ok := readString(mod.Memory(), uint32(stack[1]), uint32(stack[2]))
	if !ok {
		return EFAULT
	}
	full, errno := s.resolve(dir, name)
	if errno != ESUCCESS {
		return errno
	}
	if err := wfs.Mkdir(s.fsys, full, 0o755); err != nil {
		return MakeErrno(err)
	}
	return ESUCCESS
}

func (s *System) pathRemoveDirectory(_ context.Context, mod api.Module, stack []uint64) Errno {
	dir, errno := s.lookup(FD(uint32(stack[0])), PathRemoveDirectoryRight)
	if errno != ESUCCESS {
		return errno
	}
	name, ok := readString(mod.Memory(), uint32(stack[1]), uint32(stack[2]))
	if !ok {
		return EFAULT
	}
	full, errno := s.resolve(dir, name)
	if errno != ESUCCESS {
		return errno
	}
	st, errno := s.statPath(full, false)
	if errno != ESUCCESS {
		return errno
	}
	if st.FileType != DirectoryType {
		return ENOTDIR
	}
	if err := wfs.Remove(s.fsys, full); err != nil {
		return MakeErrno(err)
	}
	return ESUCCESS
}

func (s *System) pathUnlinkFile(_ context.Context, mod api.Module, stack []uint64) Errno {
	dir, errno := s.lookup(FD(uint32(stack[0])), PathUnlinkFileRight)
	if errno != ESUCCESS {
		return errno
	}
	name, ok := readString(mod.Memory(), uint32(stack[1]), uint32(stack[2]))
	if !ok {
		return EFAULT
	}
	full, errno := s.resolve(dir, name)
	if errno != ESUCCESS {
		return errno
	}
	st, errno := s.statPath(full, false)
	if errno != ESUCCESS {
		return errno
	}
	if st.FileType == DirectoryType {
		return EISDIR
	}
	if err := wfs.Remove(s.fsys, full); err != nil {
		return MakeErrno(err)
	}
	return ESUCCESS
}

func (s *System) pathRename(_ context.Context, mod api.Module, stack []uint64) Errno {
	oldDir, errno := s.lookup(FD(uint32(stack[0])), PathRenameSourceRight)
	if errno != ESUCCESS {
		return errno
	}
	newDir, errno := s.lookup(FD(uint32(stack[3])), PathRenameTargetRight)
	if errno != ESUCCESS {
		return errno
	}
	mem := mod.Memory()
	oldName, ok := readString(mem, uint32(stack[1]), uint32(stack[2]))
	if !ok {
		return EFAULT
	}
	newName, ok := readString(mem, uint32(stack[4]), uint32(stack[5]))
	if !ok {
		return EFAULT
	}
	oldFull, errno := s.resolve(oldDir, oldName)
	if errno != ESUCCESS {
		return errno
	}
	newFull, errno := s.resolve(newDir, newName)
	if errno != ESUCCESS {
		return errno
	}
	if err := wfs.Rename(s.fsys, oldFull, newFull); err != nil {
		return MakeErrno(err)
	}
	return ESUCCESS
}

func (s *System) pathSymlink(_ context.Context, mod api.Module, stack []uint64) Errno {
	mem := mod.Memory()
	oldPath, ok := readString(mem, uint32(stack[0]), uint32(stack[1]))
	if !ok {
		return EFAULT
	}
	dir, errno := s.lookup(FD(uint32(stack[2])), PathSymlinkRight)
	if errno != ESUCCESS {
		return errno
	}
	newName, ok := readString(mem, uint32(stack[3]), uint32(stack[4]))
	if !ok {
		return EFAULT
	}
	newFull, errno := s.resolve(dir, newName)
	if errno != ESUCCESS {
		return errno
	}
	if err := wfs.Symlink(s.fsys, oldPath, newFull); err != nil {
		return MakeErrno(err)
	}
	return ESUCCESS
}

func (s *System) pathReadlink(_ context.Context, mod api.Module, stack []uint64) Errno {
	dir, errno := s.lookup(FD(uint32(stack[0])), PathReadLinkRight)
	if errno != ESUCCESS {
		return errno
	}
	name, ok := readString(mod.Memory(), uint32(stack[1]), uint32(stack[2]))
	if !ok {
		return EFAULT
	}
	full, errno := s.resolve(dir, name)
	if errno != ESUCCESS {
		return errno
	}
	target, err := wfs.Readlink(s.fsys, full)
	if err != nil {
		return MakeErrno(err)
	}
	bufPtr := uint32(stack[3])
	bufLen := uint32(stack[4])
	b := []byte(target)
	n := min(uint32(len(b)), bufLen)
	if !mod.Memory().Write(bufPtr, b[:n]) {
		return EFAULT
	}
	if !mod.Memory().WriteUint32Le(uint32(stack[5]), n) {
		return EFAULT
	}
	return ESUCCESS
}

func (s *System) pathFilestatGet(_ context.Context, mod api.Module, stack []uint64) Errno {
	dir, errno := s.lookup(FD(uint32(stack[0])), PathFileStatGetRight)
	if errno != ESUCCESS {
		return errno
	}
	name, ok := readString(mod.Memory(), uint32(stack[2]), uint32(stack[3]))
	if !ok {
		return EFAULT
	}
	full, errno := s.resolve(dir, name)
	if errno != ESUCCESS {
		return errno
	}
	follow := LookupFlags(uint32(stack[1])).Has(SymlinkFollow)
	st, errno := s.statPath(full, follow)
	if errno != ESUCCESS {
		return errno
	}
	if !mod.Memory().Write(uint32(stack[4]), encodeFileStat(st)) {
		return EFAULT
	}
	return ESUCCESS
}

func (s *System) pathFilestatSetTimes(_ context.Context, mod api.Module, stack []uint64) Errno {
	dir, errno := s.lookup(FD(uint32(stack[0])), PathFileStatSetTimesRight)
	if errno != ESUCCESS {
		return errno
	}
	name, ok := readString(mod.Memory(), uint32(stack[2]), uint32(stack[3]))
	if !ok {
		return EFAULT
	}
	full, errno := s.resolve(dir, name)
	if errno != ESUCCESS {
		return errno
	}
	atime, mtime, ok := s.resolveTimes(stack[4], stack[5], FSTFlags(uint32(stack[6])))
	if !ok {
		return EINVAL
	}
	if err := wfs.Chtimes(s.fsys, full, atime, mtime); err != nil {
		return MakeErrno(err)
	}
	return ESUCCESS
}

func (s *System) pathLink(context.Context, api.Module, []uint64) Errno {
	// Hard links are not part of the VFS abstraction.
	return ENOTSUP
}

// --- scheduling and process control ---------------------------------------

func (s *System) schedYield(context.Context, api.Module, []uint64) Errno {
	runtime.Gosched()
	return ESUCCESS
}

func (s *System) procRaise(_ context.Context, _ api.Module, stack []uint64) Errno {
	return ENOTSUP
}

func (s *System) procExit(ctx context.Context, mod api.Module, stack []uint64) {
	code := int(uint32(stack[0]))
	if s.exit != nil {
		s.exit(code)
	}
	_ = mod.CloseWithExitCode(ctx, uint32(code))
	panic(sys.NewExitError(uint32(code)))
}

// --- poll -----------------------------------------------------------------

func (s *System) pollOneoff(ctx context.Context, mod api.Module, stack []uint64) Errno {
	inPtr := uint32(stack[0])
	outPtr := uint32(stack[1])
	n := uint32(stack[2])
	neventsPtr := uint32(stack[3])
	if n == 0 {
		return EINVAL
	}
	mem := mod.Memory()

	type ev struct {
		userdata uint64
		errno    Errno
		typ      uint8
		nbytes   uint64
	}
	var ready []ev

	var (
		earliest   time.Duration
		haveClock  bool
		earliestEv ev
	)

	for i := range n {
		sub, ok := mem.Read(inPtr+i*sizeOfSubscription, sizeOfSubscription)
		if !ok {
			return EFAULT
		}
		userdata := le.Uint64(sub[0:])
		tag := sub[8]
		switch tag {
		case eventTypeClock:
			timeout := le.Uint64(sub[24:])
			flags := le.Uint16(sub[40:])
			var d time.Duration
			if flags&subscriptionClockAbstime != 0 {
				d = time.Duration(int64(timeout) - s.now().UnixNano())
			} else {
				d = time.Duration(timeout)
			}
			if d <= 0 {
				ready = append(ready, ev{userdata: userdata, typ: eventTypeClock})
			} else if !haveClock || d < earliest {
				haveClock = true
				earliest = d
				earliestEv = ev{userdata: userdata, typ: eventTypeClock}
			}
		case eventTypeFDRead, eventTypeFDWrite:
			fd := FD(le.Uint32(sub[16:]))
			e, errno := s.lookup(fd, 0)
			if errno != ESUCCESS {
				ready = append(ready, ev{userdata: userdata, typ: tag, errno: errno})
				continue
			}
			// Regular files and standard streams are always considered ready.
			var nbytes uint64 = 1
			if tag == eventTypeFDRead && e.file != nil {
				if st, se := s.fdFileStat(e); se == ESUCCESS {
					nbytes = st.Size
				}
			}
			ready = append(ready, ev{userdata: userdata, typ: tag, nbytes: nbytes})
		default:
			ready = append(ready, ev{userdata: userdata, typ: tag, errno: EINVAL})
		}
	}

	// If nothing is immediately ready, wait for the earliest clock deadline.
	if len(ready) == 0 && haveClock {
		timer := time.NewTimer(earliest)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ECANCELED
		}
		ready = append(ready, earliestEv)
	}

	count := 0
	for _, e := range ready {
		out := make([]byte, sizeOfEvent)
		le.PutUint64(out[0:], e.userdata)
		le.PutUint16(out[8:], uint16(e.errno))
		out[10] = e.typ
		le.PutUint64(out[16:], e.nbytes)
		if !mem.Write(outPtr+uint32(count)*sizeOfEvent, out) {
			return EFAULT
		}
		count++
	}
	if !mem.WriteUint32Le(neventsPtr, uint32(count)) {
		return EFAULT
	}
	return ESUCCESS
}

// --- shared helpers -------------------------------------------------------

// resolveTimes computes the access and modification times to apply, honoring
// the *_NOW flags. A zero time.Time means "leave unchanged".
func (s *System) resolveTimes(atim, mtim uint64, flags FSTFlags) (time.Time, time.Time, bool) {
	if flags.Has(SetAccessTime) && flags.Has(SetAccessTimeNow) {
		return time.Time{}, time.Time{}, false
	}
	if flags.Has(SetModifyTime) && flags.Has(SetModifyTimeNow) {
		return time.Time{}, time.Time{}, false
	}
	now := s.now()
	var atime, mtime time.Time
	switch {
	case flags.Has(SetAccessTimeNow):
		atime = now
	case flags.Has(SetAccessTime):
		atime = time.Unix(0, int64(atim))
	}
	switch {
	case flags.Has(SetModifyTimeNow):
		mtime = now
	case flags.Has(SetModifyTime):
		mtime = time.Unix(0, int64(mtim))
	}
	return atime, mtime, true
}

func isUnsupported(err error) bool {
	return MakeErrno(err) == ENOTSUP
}
