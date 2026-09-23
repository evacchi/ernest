// Command probe is a test extension exercising the guest VFS.
package main

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/evacchi/ernest/sdk"
)

func main() {
	sdk.Tool("model", "Read /agent/config/model.", `{}`, func(json.RawMessage) (string, error) {
		b, err := os.ReadFile("/agent/config/model")
		return strings.TrimSpace(string(b)), err
	})
	sdk.Tool("setmodel", "Write /agent/config/model.", `{}`, func(args json.RawMessage) (string, error) {
		return "", os.WriteFile("/agent/config/model", []byte("m2\n"), 0o644)
	})
	sdk.Tool("touch", "Write /work/probe.txt, read it back.", `{}`, func(json.RawMessage) (string, error) {
		if err := os.WriteFile("/work/probe.txt", []byte("hi"), 0o644); err != nil {
			return "", err
		}
		b, err := os.ReadFile("/work/probe.txt")
		return string(b), err
	})
	sdk.OnToolResult(func(_ sdk.Call, out string) string { return strings.ToUpper(out) })
	sdk.Serve()
}
