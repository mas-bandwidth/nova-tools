package testredis

import (
	"bytes"
	"sync"
)

// readyLine is what redis-server prints once it listens: "Ready to accept
// connections" before Redis 7, "Ready to accept connections tcp" since.
const readyLine = "Ready to accept connections"

// tailSize is how much of a server's output is kept for a failure message.
const tailSize = 16 << 10

// tail is a server's standard output and standard error: the last of it is
// kept, and ready is closed when the ready line has been printed. Only the
// server writes here, so the line is the server's own word that it holds its
// port.
type tail struct {
	mu    sync.Mutex
	buf   []byte
	seen  bool
	ready chan struct{}
}

func (w *tail) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	// Read before the cut, so a line split over two writes is whole here.
	if !w.seen && bytes.Contains(w.buf, []byte(readyLine)) {
		w.seen = true
		close(w.ready)
	}
	if over := len(w.buf) - tailSize; over > 0 {
		w.buf = append(w.buf[:0], w.buf[over:]...)
	}
	return len(p), nil
}

func (w *tail) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.buf)
}
