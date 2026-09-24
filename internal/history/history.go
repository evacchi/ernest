// Package history saves conversations so they can be resumed.
//
// Each session is one JSON-lines file, one message per line, appended as
// the conversation grows:
//
//	.ernest/sessions/20260924-103000.jsonl
//	  {"role":"user","content":"list the go files"}
//	  {"role":"assistant","tool_calls":[...],"replay":[...]}
package history

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/evacchi/ernest/internal/llm"
)

const (
	fileExt    = ".jsonl"
	idLayout   = "20060102-150405"
	maxTitle   = 60
	ellipsis   = "…"
	dirPerm    = 0o755
	filePerm   = 0o644
	appendMode = os.O_APPEND | os.O_CREATE | os.O_WRONLY
)

var errBadID = errors.New("history: bad session id")

// Info summarizes a saved session.
type Info struct {
	ID    string
	Title string // first line of the first user message
}

// Store keeps sessions as files in one directory.
type Store struct {
	dir string
}

// NewStore returns a store rooted at dir, created on first write.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// NewID returns an id for a session started now.
func NewID() string {
	return time.Now().Format(idLayout)
}

// record is a message as saved: unlike the visible history, it keeps the
// provider's replay items (e.g. encrypted reasoning).
type record struct {
	llm.Message
	Replay []json.RawMessage `json:"replay,omitempty"`
}

// Append adds m to session id. The file appears with the first message,
// so sessions without prompts leave nothing behind.
func (s *Store) Append(id string, m llm.Message) error {
	line, err := json.Marshal(record{Message: m, Replay: m.Replay})
	if err != nil {
		return err
	}

	path, err := s.path(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, dirPerm); err != nil {
		return err
	}
	f, err := os.OpenFile(path, appendMode, filePerm)
	if err != nil {
		return err
	}

	_, err = f.Write(append(line, '\n'))
	return errors.Join(err, f.Close())
}

// Load returns the messages of session id.
func (s *Store) Load(id string) ([]llm.Message, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []llm.Message
	err = scan(f, func(m llm.Message) bool {
		out = append(out, m)
		return true
	})
	return out, err
}

// List returns the saved sessions, newest first. Ids sort by time.
func (s *Store) List() ([]Info, error) {
	paths, err := filepath.Glob(filepath.Join(s.dir, "*"+fileExt))
	if err != nil {
		return nil, err
	}
	slices.Sort(paths)
	slices.Reverse(paths)

	out := make([]Info, 0, len(paths))
	for _, p := range paths {
		title, err := readTitle(p)
		if err != nil {
			return nil, err
		}
		id := strings.TrimSuffix(filepath.Base(p), fileExt)
		out = append(out, Info{ID: id, Title: title})
	}
	return out, nil
}

// readTitle reads a session file up to its first user message.
func readTitle(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	title := ""
	err = scan(f, func(m llm.Message) bool {
		if m.Role != llm.RoleUser {
			return true
		}
		title = shorten(m.Content)
		return false
	})
	return title, err
}

// path maps id to its file. Ids are plain names: "../x" would escape dir.
func (s *Store) path(id string) (string, error) {
	if id == "" || filepath.Base(id) != id {
		return "", errBadID
	}
	return filepath.Join(s.dir, id+fileExt), nil
}

// scan decodes one message per line until fn returns false. Lines can be
// long (tool output), so it reads whole lines rather than tokens.
func scan(r io.Reader, fn func(llm.Message) bool) error {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			var rec record
			if jerr := json.Unmarshal(line, &rec); jerr != nil {
				return jerr
			}
			rec.Message.Replay = rec.Replay
			if !fn(rec.Message) {
				return nil
			}
		}

		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// shorten keeps the first line, up to maxTitle runes.
func shorten(s string) string {
	first, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	r := []rune(first)
	if len(r) <= maxTitle {
		return first
	}
	return string(r[:maxTitle]) + ellipsis
}
