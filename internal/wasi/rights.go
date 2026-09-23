package wasi

// Rights are file descriptor rights: the set of operations that may be
// performed on a descriptor, and (for directories) inherited by descriptors
// opened through it.
type Rights uint64

const (
	FDDataSyncRight Rights = 1 << iota
	FDReadRight
	FDSeekRight
	FDStatSetFlagsRight
	FDSyncRight
	FDTellRight
	FDWriteRight
	FDAdviseRight
	FDAllocateRight
	PathCreateDirectoryRight
	PathCreateFileRight
	PathLinkSourceRight
	PathLinkTargetRight
	PathOpenRight
	FDReadDirRight
	PathReadLinkRight
	PathRenameSourceRight
	PathRenameTargetRight
	PathFileStatGetRight
	PathFileStatSetSizeRight
	PathFileStatSetTimesRight
	FDFileStatGetRight
	FDFileStatSetSizeRight
	FDFileStatSetTimesRight
	PathSymlinkRight
	PathRemoveDirectoryRight
	PathUnlinkFileRight
	PollFDReadWriteRight
	SockShutdownRight
	SockAcceptRight

	// AllRights is the set of every defined right.
	AllRights Rights = (1 << 30) - 1

	syncRights     = FDSyncRight | FDDataSyncRight
	seekRights     = FDSeekRight | FDTellRight
	fileStatRights = FDFileStatGetRight | FDFileStatSetSizeRight | FDFileStatSetTimesRight
	// dirStatRights omits FDFileStatSetSizeRight: directories cannot be
	// ftruncate'd, so they must not advertise that right.
	dirStatRights = FDFileStatGetRight | FDFileStatSetTimesRight
	pathRights    = PathCreateDirectoryRight | PathCreateFileRight |
		PathLinkSourceRight | PathLinkTargetRight | PathOpenRight |
		PathReadLinkRight | PathRenameSourceRight | PathRenameTargetRight |
		PathFileStatGetRight | PathFileStatSetSizeRight |
		PathFileStatSetTimesRight | PathSymlinkRight |
		PathRemoveDirectoryRight | PathUnlinkFileRight

	// FileRights are the rights granted to regular files.
	FileRights = syncRights | seekRights | fileStatRights |
		FDReadRight | FDWriteRight | FDStatSetFlagsRight |
		FDAdviseRight | FDAllocateRight | PollFDReadWriteRight

	// DirectoryRights are the rights granted to directories. Seek rights are
	// included so that directories can be rewound (fd_seek to offset 0), which
	// runtimes rely on to re-read a directory listing.
	DirectoryRights = pathRights | syncRights | dirStatRights | seekRights |
		FDStatSetFlagsRight | FDReadDirRight

	// TTYRights are the rights granted to a terminal-like character device
	// (stdio); the same as file rights minus the ability to seek.
	TTYRights = FileRights &^ seekRights
)

// Has reports whether all bits of f are set in flags.
func (flags Rights) Has(f Rights) bool { return flags&f == f }

// HasAny reports whether any bit of f is set in flags.
func (flags Rights) HasAny(f Rights) bool { return flags&f != 0 }
