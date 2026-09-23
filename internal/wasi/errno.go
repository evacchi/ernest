package wasi

import (
	"errors"
	"io"
	"io/fs"
	"syscall"

	wfs "tractor.dev/wanix/fs"
)

// Errno is a WASI error code, matching the numeric values defined by the WASI
// snapshot preview 1 specification.
type Errno uint16

const (
	ESUCCESS Errno = iota
	E2BIG
	EACCES
	EADDRINUSE
	EADDRNOTAVAIL
	EAFNOSUPPORT
	EAGAIN
	EALREADY
	EBADF
	EBADMSG
	EBUSY
	ECANCELED
	ECHILD
	ECONNABORTED
	ECONNREFUSED
	ECONNRESET
	EDEADLK
	EDESTADDRREQ
	EDOM
	EDQUOT
	EEXIST
	EFAULT
	EFBIG
	EHOSTUNREACH
	EIDRM
	EILSEQ
	EINPROGRESS
	EINTR
	EINVAL
	EIO
	EISCONN
	EISDIR
	ELOOP
	EMFILE
	EMLINK
	EMSGSIZE
	EMULTIHOP
	ENAMETOOLONG
	ENETDOWN
	ENETRESET
	ENETUNREACH
	ENFILE
	ENOBUFS
	ENODEV
	ENOENT
	ENOEXEC
	ENOLCK
	ENOLINK
	ENOMEM
	ENOMSG
	ENOPROTOOPT
	ENOSPC
	ENOSYS
	ENOTCONN
	ENOTDIR
	ENOTEMPTY
	ENOTRECOVERABLE
	ENOTSOCK
	ENOTSUP
	ENOTTY
	ENXIO
	EOVERFLOW
	EOWNERDEAD
	EPERM
	EPIPE
	EPROTO
	EPROTONOSUPPORT
	EPROTOTYPE
	ERANGE
	EROFS
	ESPIPE
	ESRCH
	ESTALE
	ETIMEDOUT
	ETXTBSY
	EXDEV
	ENOTCAPABLE
)

// Error implements the error interface.
func (e Errno) Error() string {
	if name, ok := errorNames[e]; ok {
		return name
	}
	return "errno"
}

var errorNames = map[Errno]string{
	ESUCCESS: "ESUCCESS", E2BIG: "E2BIG", EACCES: "EACCES", EAGAIN: "EAGAIN",
	EBADF: "EBADF", EBADMSG: "EBADMSG", EEXIST: "EEXIST", EFAULT: "EFAULT",
	EINVAL: "EINVAL", EIO: "EIO", EISDIR: "EISDIR", ELOOP: "ELOOP",
	EMFILE: "EMFILE", ENAMETOOLONG: "ENAMETOOLONG", ENFILE: "ENFILE",
	ENOENT: "ENOENT", ENOMEM: "ENOMEM", ENOSPC: "ENOSPC", ENOSYS: "ENOSYS",
	ENOTDIR: "ENOTDIR", ENOTEMPTY: "ENOTEMPTY", ENOTSOCK: "ENOTSOCK",
	ENOTSUP: "ENOTSUP", ENOTCAPABLE: "ENOTCAPABLE", EOVERFLOW: "EOVERFLOW",
	EPERM: "EPERM", EPIPE: "EPIPE", ERANGE: "ERANGE", EROFS: "EROFS",
	ESPIPE: "ESPIPE", EXDEV: "EXDEV",
}

// MakeErrno converts a Go error into the closest matching WASI error code.
func MakeErrno(err error) Errno {
	if err == nil {
		return ESUCCESS
	}
	switch {
	case errors.Is(err, io.EOF):
		return ESUCCESS
	case errors.Is(err, fs.ErrNotExist):
		return ENOENT
	// "directory not empty" must be checked before fs.ErrExist: on some
	// platforms the underlying ENOTEMPTY also satisfies errors.Is(_, ErrExist).
	case errors.Is(err, wfs.ErrNotEmpty), errors.Is(err, syscall.ENOTEMPTY), contains(err, "not empty"):
		return ENOTEMPTY
	case errors.Is(err, fs.ErrExist):
		return EEXIST
	case errors.Is(err, fs.ErrPermission):
		return EACCES
	case errors.Is(err, fs.ErrClosed):
		return EBADF
	case errors.Is(err, fs.ErrInvalid):
		return EINVAL
	case errors.Is(err, wfs.ErrNotSupported):
		return ENOTSUP
	}

	var errno syscall.Errno
	if errors.As(err, &errno) {
		return fromSyscall(errno)
	}

	// Match common textual errors produced by fs helpers that do not wrap a
	// sentinel value (e.g. "not a directory" from path resolution).
	switch {
	case contains(err, "not a directory"):
		return ENOTDIR
	case contains(err, "is a directory"):
		return EISDIR
	case contains(err, "not empty"):
		return ENOTEMPTY
	case contains(err, "read-only"):
		return EROFS
	case contains(err, "invalid"):
		return EINVAL
	}
	return EIO
}

func contains(err error, substr string) bool {
	msg := err.Error()
	for i := 0; i+len(substr) <= len(msg); i++ {
		if msg[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func fromSyscall(errno syscall.Errno) Errno {
	switch errno {
	case syscall.E2BIG:
		return E2BIG
	case syscall.EACCES:
		return EACCES
	case syscall.EAGAIN:
		return EAGAIN
	case syscall.EBADF:
		return EBADF
	case syscall.EEXIST:
		return EEXIST
	case syscall.EFAULT:
		return EFAULT
	case syscall.EFBIG:
		return EFBIG
	case syscall.EINTR:
		return EINTR
	case syscall.EINVAL:
		return EINVAL
	case syscall.EIO:
		return EIO
	case syscall.EISDIR:
		return EISDIR
	case syscall.ELOOP:
		return ELOOP
	case syscall.EMFILE:
		return EMFILE
	case syscall.ENAMETOOLONG:
		return ENAMETOOLONG
	case syscall.ENFILE:
		return ENFILE
	case syscall.ENODEV:
		return ENODEV
	case syscall.ENOENT:
		return ENOENT
	case syscall.ENOMEM:
		return ENOMEM
	case syscall.ENOSPC:
		return ENOSPC
	case syscall.ENOSYS:
		return ENOSYS
	case syscall.ENOTDIR:
		return ENOTDIR
	case syscall.ENOTEMPTY:
		return ENOTEMPTY
	case syscall.EPERM:
		return EPERM
	case syscall.EPIPE:
		return EPIPE
	case syscall.ERANGE:
		return ERANGE
	case syscall.EROFS:
		return EROFS
	case syscall.ESPIPE:
		return ESPIPE
	case syscall.EXDEV:
		return EXDEV
	default:
		return EIO
	}
}
