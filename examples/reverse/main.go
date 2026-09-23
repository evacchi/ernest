// Command reverse is a sample ernest extension: a "reverse" tool and a
// tool_call hook that blocks "rm -rf".
//
//	GOOS=wasip1 GOARCH=wasm go build -o .ernest/extensions/reverse.wasm ./examples/reverse
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/evacchi/ernest/sdk"
)

const schema = `{
  "type": "object",
  "properties": {"text": {"type": "string"}},
  "required": ["text"]
}`

const forbidden = "rm -rf"

func main() {
	sdk.Tool("reverse", "Reverse a string.", schema, reverse)
	sdk.OnToolCall(guard)

	if err := sdk.Serve(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func reverse(args json.RawMessage) (string, error) {
	var in struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}

	r := []rune(in.Text)
	slices.Reverse(r)
	return string(r), nil
}

func guard(c sdk.Call) sdk.Decision {
	if strings.Contains(c.Args, forbidden) {
		return sdk.Decision{Verdict: sdk.Block, Reason: forbidden + " is not allowed"}
	}
	return sdk.Decision{}
}
