package sprintdash

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"
)

// The event stream (the owner, 2026-10-03 11:30 AM: "nova sprint website is not updating
// once per-second. something is chug."): a page that polls after each answer sees the
// answer's own latency on top of the second, so a client keeps /events open instead and is
// sent each new copy of the sprint as the refresh makes it, as server-sent events.

// KeepaliveDefault is the time between two keepalive comments on an idle stream.
const KeepaliveDefault = 15 * time.Second

// tickSlack is how early a tick of Run may read: a ticker's tick a hair under Every after
// the last read began reads, so the stream's cadence is the ticker's, never two of them.
const tickSlack = 10

// Run reads the sprint on each tick while an /events client is connected, so each new
// copy is pushed as it is read; it returns when ctx is done. tick is the clock's ticker
// (time.Ticker's channel, every Every); a test hands it a channel of its own.
func (s *Server) Run(ctx context.Context, tick <-chan time.Time) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick:
			s.mu.Lock()
			watched := s.streams > 0
			s.mu.Unlock()
			if watched {
				s.refresh(s.Every - s.Every/tickSlack)
			}
		}
	}
}

// events streams the copy as server-sent events until the client goes: event "sprint",
// data what view makes of each new good read (false: nothing to send for it), the first
// at once from the copy there is, then one per good read; a keepalive comment every
// Keepalive (KeepaliveDefault when zero). Every answer is no-store.
func (s *Server) events(w http.ResponseWriter, r *http.Request, view func(*sprintCopy) ([]byte, bool)) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store, max-age=0")
	h.Set("X-Accel-Buffering", "no") // a proxy in front passes each event on as it comes
	rc := http.NewResponseController(w)
	// ignored: a writer with no deadline to lift (a test's recorder) streams the same
	_ = rc.SetWriteDeadline(time.Time{})
	s.mu.Lock()
	s.streams++
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.streams--
		s.mu.Unlock()
	}()
	s.Refresh()
	every := s.Keepalive
	if every <= 0 {
		every = KeepaliveDefault
	}
	keep, stop := s.ticker(every)
	defer stop()
	w.WriteHeader(http.StatusOK)
	_ = rc.Flush() // ignored: the headers go now; a client gone ends the stream below
	var sent uint64
	for {
		s.mu.Lock()
		if s.changed == nil {
			s.changed = make(chan struct{})
		}
		c, gen, changed := s.copy, s.gen, s.changed
		s.mu.Unlock()
		if c != nil && gen != sent {
			sent = gen
			if body, ok := view(c); ok {
				// ignored: a write that fails is a client gone; the stream ends with it, and it reconnects
				if _, err := w.Write(event("sprint", body)); err != nil {
					return
				}
				_ = rc.Flush() // ignored: a client gone fails the next write
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		case <-keep:
			// ignored: as above, a client gone
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
				return
			}
			_ = rc.Flush() // ignored: as above
		}
	}
}

// event is one server-sent event: its name, and data a line each of body's lines.
func event(name string, body []byte) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "event: %s\n", name)
	for _, line := range bytes.Split(bytes.TrimSpace(body), []byte("\n")) {
		b.WriteString("data: ")
		b.Write(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.Bytes()
}

// ticker is a ticker of the stream's keepalive: the clock's, or a test's (keepaliveTick).
func (s *Server) ticker(every time.Duration) (<-chan time.Time, func()) {
	if s.keepaliveTick != nil {
		return s.keepaliveTick(every)
	}
	t := time.NewTicker(every)
	return t.C, t.Stop
}
