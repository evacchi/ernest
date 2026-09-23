// Command model is a sample ernest extension adding the /model slash
// command. It knows nothing about providers: it only reads and writes
// /agent/config/model, and the host applies the change.
//
//	GOOS=wasip1 GOARCH=wasm go build -o .ernest/extensions/model.wasm ./examples/model
//
//	/model              show the current model
//	/model gpt-6-luna   switch model
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/evacchi/ernest/sdk"
)

const configModel = "/agent/config/model"

func main() {
	sdk.Command("model", "Show or switch the model: /model [name]", model)

	if err := sdk.Serve(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func model(input string) (string, error) {
	name := strings.TrimSpace(input)
	if name != "" {
		if err := os.WriteFile(configModel, []byte(name+"\n"), 0o644); err != nil {
			return "", err
		}
	}

	cur, err := os.ReadFile(configModel)
	if err != nil {
		return "", err
	}
	return "model: " + strings.TrimSpace(string(cur)), nil
}
