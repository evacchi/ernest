//go:build wasip1

// Command filetest is a WASI guest compiled to GOOS=wasip1 by the end-to-end
// test. It exercises the file-system surface of the host through the standard
// library: arguments, environment, file writes/reads, mkdir and directory
// listing, all against the preopened root "/".
package main

import (
	"fmt"
	"os"
	"sort"
)

func main() {
	fmt.Println("arg1:", os.Args[1])
	fmt.Println("env FOO:", os.Getenv("FOO"))

	if err := os.WriteFile("/hello.txt", []byte("hello vfs"), 0o644); err != nil {
		panic(err)
	}
	b, err := os.ReadFile("/hello.txt")
	if err != nil {
		panic(err)
	}
	fmt.Println("read:", string(b))

	if err := os.Mkdir("/sub", 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile("/sub/a.txt", []byte("a"), 0o644); err != nil {
		panic(err)
	}

	entries, err := os.ReadDir("/")
	if err != nil {
		panic(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		suffix := ""
		if e.IsDir() {
			suffix = "/"
		}
		names = append(names, e.Name()+suffix)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Println("entry:", n)
	}
	fmt.Println("done")
}
