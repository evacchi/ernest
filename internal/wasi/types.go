// Package wasi implements the WebAssembly System Interface (WASI) snapshot
// preview 1 for the wazero runtime, backed entirely by a virtual file system
// (a tractor.dev/wanix/fs.FS).
//
// It is designed in the spirit of github.com/dispatchrun/wasi-go: a clean
// separation between the ABI wiring (see host.go) and the implementation of the
// system calls (see system.go). Unlike wasi-go, the backend is not the host
// operating system but a pure VFS, which makes it portable, sandboxed and easy
// to embed.
package wasi

import "io/fs"

// FD is a file descriptor handle.
type FD = int32

// FileType is the type of a file descriptor or file (WASI filetype).
type FileType uint8

const (
	UnknownType FileType = iota
	BlockDeviceType
	CharacterDeviceType
	DirectoryType
	RegularFileType
	SocketDGramType
	SocketStreamType
	SymbolicLinkType
)

// fileTypeOf maps an io/fs.FileMode to a WASI filetype.
func fileTypeOf(mode fs.FileMode) FileType {
	switch {
	case mode&fs.ModeDir != 0:
		return DirectoryType
	case mode&fs.ModeSymlink != 0:
		return SymbolicLinkType
	case mode&fs.ModeCharDevice != 0:
		return CharacterDeviceType
	case mode&fs.ModeDevice != 0:
		return BlockDeviceType
	case mode&fs.ModeSocket != 0:
		return SocketStreamType
	case mode&fs.ModeNamedPipe != 0:
		return CharacterDeviceType
	case mode.IsRegular():
		return RegularFileType
	default:
		return UnknownType
	}
}

// Whence is the reference point for FDSeek.
type Whence uint8

const (
	SeekStart Whence = iota
	SeekCurrent
	SeekEnd
)

// FDFlags are file descriptor flags (fdflags).
type FDFlags uint16

const (
	Append FDFlags = 1 << iota
	DSync
	NonBlock
	RSync
	Sync
)

// Has reports whether all bits of f are set in flags.
func (flags FDFlags) Has(f FDFlags) bool { return flags&f == f }

// OpenFlags are the flags passed to path_open (oflags).
type OpenFlags uint16

const (
	OpenCreate OpenFlags = 1 << iota
	OpenDirectory
	OpenExclusive
	OpenTruncate
)

// Has reports whether all bits of f are set in flags.
func (flags OpenFlags) Has(f OpenFlags) bool { return flags&f == f }

// LookupFlags determine how paths are resolved (lookupflags).
type LookupFlags uint32

const (
	// SymlinkFollow expands symbolic links encountered during resolution.
	SymlinkFollow LookupFlags = 1 << iota
)

// Has reports whether all bits of f are set in flags.
func (flags LookupFlags) Has(f LookupFlags) bool { return flags&f == f }

// FSTFlags indicate which timestamps to set in *_filestat_set_times (fstflags).
type FSTFlags uint16

const (
	SetAccessTime FSTFlags = 1 << iota
	SetAccessTimeNow
	SetModifyTime
	SetModifyTimeNow
)

// Has reports whether all bits of f are set in flags.
func (flags FSTFlags) Has(f FSTFlags) bool { return flags&f == f }

// Advice is the file access pattern advisory for fd_advise.
type Advice uint8

// ClockID identifies a clock for clock_time_get / clock_res_get.
type ClockID uint32

const (
	ClockRealtime ClockID = iota
	ClockMonotonic
	ClockProcessCPUTimeID
	ClockThreadCPUTimeID
)

// PreOpenType is the tag of a prestat.
type PreOpenType uint8

const (
	PreOpenDir PreOpenType = iota
)

// ABI structure sizes, in bytes, as serialized to guest linear memory.
const (
	sizeOfFileStat     = 64
	sizeOfFDStat       = 24
	sizeOfPreStat      = 8
	sizeOfDirent       = 24
	sizeOfIOVec        = 8
	sizeOfSubscription = 48
	sizeOfEvent        = 32
)

// eventtype values used by poll_oneoff subscriptions/events.
const (
	eventTypeClock   uint8 = 0
	eventTypeFDRead  uint8 = 1
	eventTypeFDWrite uint8 = 2
)

// subclockflags
const subscriptionClockAbstime uint16 = 1 << 0
