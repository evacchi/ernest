// Command model is a sample ernest extension adding the /model slash
// command. It knows nothing about providers: it only reads and writes
// files under /agent/config, and the host applies the change.
//
//	GOOS=wasip1 GOARCH=wasm go build -o .ernest/extensions/model.wasm ./examples/model
//
//	/model              pick from the available chat models
//	/model gpt-6-luna   switch model
package main

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/evacchi/ernest/sdk"
)

const (
	configModel  = "/agent/config/model"
	configModels = "/agent/config/models"
)

var (
	// chatModel keeps text models: gpt-*, o1, o3-mini, ...
	chatModel = regexp.MustCompile(`^(gpt-|o\d|chatgpt-)`)

	// nonChat drops audio, image and other special-purpose variants.
	nonChat = regexp.MustCompile(`audio|realtime|tts|transcribe|image|search|embedding|moderation|instruct`)

	// snapshot drops dated pins like gpt-4o-2024-08-06.
	snapshot = regexp.MustCompile(`-\d{4}-\d{2}-\d{2}$`)
)

func main() {
	sdk.Command("model", "Pick or switch the model: /model [name]", model)

	if err := sdk.Serve(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func model(input string) (sdk.Result, error) {
	cur, err := read(configModel)
	if err != nil {
		return sdk.Result{}, err
	}

	// No name: offer the list, current model preselected.
	name := strings.TrimSpace(input)
	if name == "" {
		choices, err := chatModels(cur)
		if err != nil {
			return sdk.Result{}, err
		}
		return sdk.Result{Choices: choices, Selected: cur}, nil
	}

	if err := os.WriteFile(configModel, []byte(name+"\n"), 0o644); err != nil {
		return sdk.Result{}, err
	}
	return sdk.Text("model: " + name), nil
}

// chatModels filters the host's model list, always keeping cur.
func chatModels(cur string) ([]string, error) {
	all, err := read(configModels)
	if err != nil {
		return nil, err
	}

	out := []string{cur}
	for _, m := range strings.Fields(all) {
		if m == cur || !chatModel.MatchString(m) || nonChat.MatchString(m) || snapshot.MatchString(m) {
			continue
		}
		out = append(out, m)
	}

	slices.Sort(out)
	return slices.Compact(out), nil
}

func read(path string) (string, error) {
	b, err := os.ReadFile(path)
	return strings.TrimSpace(string(b)), err
}
