//go:build wasip1

// Command wasi is a multi-subcommand WASI guest used by the ported end-to-end
// tests. Its portable subcommands (ls, stat, stdout, largestdout) are adapted
// from wazero's testdata/go/wasi.go (Apache-2.0); the socket/http/stdin cases,
// which do not apply to a pure VFS backend, are omitted.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func main() {
	switch os.Args[1] {
	case "ls":
		var repeat bool
		if len(os.Args) == 4 {
			repeat = os.Args[3] == "repeat"
		}
		// Go doesn't open with O_DIRECTORY, so reading a non-directory later
		// fails with EBADF rather than ENOTDIR.
		if err := mainLs(os.Args[2], repeat); errors.Is(err, syscall.EBADF) {
			fmt.Println("ENOTDIR")
		} else if err != nil {
			panic(err)
		}
	case "stat":
		if err := mainStat(); err != nil {
			panic(err)
		}
	case "stdout":
		mainStdout()
	case "largestdout":
		mainLargeStdout()
	}
}

func mainLs(path string, repeat bool) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()

	if err = printFileNames(d); err != nil {
		return err
	} else if repeat {
		if _, err = d.Seek(0, io.SeekStart); err != nil {
			return err
		}
		return printFileNames(d)
	}
	return nil
}

func printFileNames(d *os.File) error {
	names, err := d.Readdirnames(-1)
	if err != nil {
		return err
	}
	for _, n := range names {
		fmt.Println("./" + n)
	}
	return nil
}

func mainStat() error {
	isatty := func(name string, fd uintptr) error {
		f := os.NewFile(fd, "")
		st, err := f.Stat()
		if err != nil {
			return err
		}
		ttyMode := fs.ModeDevice | fs.ModeCharDevice
		fmt.Println(name, "isatty:", st.Mode()&ttyMode == ttyMode)
		return nil
	}
	for fd, name := range []string{"stdin", "stdout", "stderr", "/"} {
		if err := isatty(name, uintptr(fd)); err != nil {
			return err
		}
	}
	return nil
}

func mainStdout() {
	os.Stdout.WriteString("test")
}

func mainLargeStdout() {
	const ntest = 1024

	var decls, calls bytes.Buffer
	for i := 1; i <= ntest; i++ {
		s := strconv.Itoa(i)
		decls.WriteString(strings.Replace(decl, "$", s, -1))
		calls.WriteString(strings.Replace("call(test$)\n\t", "$", s, -1))
	}

	program = strings.Replace(program, "$DECLS", decls.String(), 1)
	program = strings.Replace(program, "$CALLS", calls.String(), 1)
	fmt.Print(program)
}

var program = `package main

var count int

func call(f func() bool) {
	if f() {
		count++
	}
}

$DECLS

func main() {
	$CALLS
	if count != 0 {
		println("failed", count, "case(s)")
	}
}
`

const decl = `
type T$ [$]uint8
func test$() bool {
	v := T${1}
	return v == [$]uint8{2} || v != [$]uint8{1}
}`
