package ext

import (
	"bytes"
	"io"
	"sync"
)

// prefixWriter prefixes each complete line, e.g. "[reverse] hello\n".
type prefixWriter struct {
	mu     sync.Mutex
	out    io.Writer
	prefix []byte
	buf    []byte
}

func newPrefixWriter(out io.Writer, name string) *prefixWriter {
	return &prefixWriter{out: out, prefix: []byte("[" + name + "] ")}
}

func (w *prefixWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}

		line := append(append([]byte{}, w.prefix...), w.buf[:i+1]...)
		w.buf = w.buf[i+1:]
		if _, err := w.out.Write(line); err != nil {
			return len(p), err
		}
	}
}
