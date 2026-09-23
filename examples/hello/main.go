// Command hello is a sample ernest extension adding the /hello slash command.
//
//	GOOS=wasip1 GOARCH=wasm go build -o .ernest/extensions/hello.wasm ./examples/hello
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/evacchi/ernest/sdk"
)

func main() {
	sdk.Command("hello", "Say hi to someone: /hello [name]", hello)

	if err := sdk.Serve(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func hello(input string) (sdk.Result, error) {
	name := strings.TrimSpace(input)
	if name == "" {
		return sdk.Text("Hi!"), nil
	}
	return sdk.Text("Hi, " + name + "!"), nil
}
