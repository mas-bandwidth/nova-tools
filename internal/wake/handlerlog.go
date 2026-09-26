package wake

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// A handler can print private task content. Keep its output in a private file,
// expose only the path, and drain output beyond the cap so logging never stops
// the handler. One file belongs to one dispatch, never to a polling tick.
const HandlerLogLimit = 1 << 20
const handlerLogTruncated = "\n[nova-wake: output truncated at 1 MiB]\n"

// HandlerLog drains a handler output stream into a bounded private file.
type HandlerLog struct {
	mu        sync.Mutex
	file      *os.File
	writer    io.Writer
	bytes     int
	truncated bool
	err       error
}

// OpenHandlerLog creates a fresh file beside the state and records note identity.
func OpenHandlerLog(state string, ids []string) (*HandlerLog, error) {
	dir := state + ".logs"
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	path, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(path, "dispatch-*.log")
	if err != nil {
		return nil, err
	}
	l := &HandlerLog{file: f, writer: f}
	header, err := json.Marshal(ids)
	if err == nil {
		_, err = fmt.Fprintf(l, "nova-wake handler note_ids=%s\n", header)
	}
	if err == nil {
		err = l.err
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return l, nil
}

func (l *HandlerLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(p)
	if l.err != nil || l.truncated {
		return n, nil
	}
	remaining := HandlerLogLimit - len(handlerLogTruncated) - l.bytes
	data := p
	if len(data) > remaining {
		data = data[:remaining]
	}
	if len(data) > 0 {
		wrote, err := l.writer.Write(data)
		l.bytes += wrote
		if err == nil && wrote != len(data) {
			err = io.ErrShortWrite
		}
		l.err = err
	}
	if len(p) > remaining && l.err == nil {
		l.truncated = true
		wrote, err := io.WriteString(l.writer, handlerLogTruncated)
		l.bytes += wrote
		if err == nil && wrote != len(handlerLogTruncated) {
			err = io.ErrShortWrite
		}
		l.err = err
	}
	return n, nil
}

// Close reports recording failures without changing the handler execution result.
func (l *HandlerLog) Close() error {
	err := l.file.Close()
	if l.err != nil {
		return l.err
	}
	return err
}

// Path names the private diagnostic file retained for this handler attempt.
func (l *HandlerLog) Path() string { return l.file.Name() }
