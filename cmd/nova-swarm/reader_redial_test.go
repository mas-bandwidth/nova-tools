package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
)

// readerRedialSprint is a fake sprint server for a reader: it refuses every
// verb (the server is down) for the first downLeft calls, then answers with one
// asked read. It records the verbs it answered.
type readerRedialSprint struct {
	mu       sync.Mutex
	downLeft int
	// dropBegins is how many read --begin asks the server drops while it holds
	// the queue's connection: a restart that ends a held wait (the second case).
	dropBegins int
	began      []string
	queues     int
}

func (s *readerRedialSprint) Run(args ...string) (int, []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.downLeft > 0 {
		s.downLeft--
		return 2, []byte("the sprint server at x did not answer: dial tcp: connect: connection refused")
	}
	switch args[0] {
	case "queue":
		s.queues++
		p := member.Packet{Card: "r1", Kind: "read", As: "reader-1", Attempt: 1, Gen: 1, Epoch: 3}
		b, _ := json.Marshal(map[string]any{"as": "reader-1", "epoch": 3, "width": 1,
			"cards": []map[string]any{{"id": "r1", "col": "asked", "gen": 1, "packet": p}}})
		return 0, b
	case "read":
		if slices.Contains(args, "--begin") {
			if s.dropBegins > 0 {
				s.dropBegins--
				return 2, []byte("the sprint server at x did not answer: read: connection reset by peer")
			}
			s.began = append(s.began, args[3:]...)
		}
		return 0, nil
	}
	return 0, nil
}

func (s *readerRedialSprint) begins() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.began)
}

type readerRedialRunner struct{ started []string }

func (r *readerRedialRunner) Start(p member.Packet) (member.Child, error) {
	r.started = append(r.started, p.Card)
	return &readerDeadChild{}, nil
}

type readerDeadChild struct{}

func (readerDeadChild) Done() bool            { return false }
func (readerDeadChild) Result() member.Result { return member.Result{} }

func TestReaderResumesAfterServerRefusal(t *testing.T) {
	t.Parallel()
	s := &readerRedialSprint{downLeft: 5}
	rn := &readerRedialRunner{}
	var out, errb bytes.Buffer
	m := member.New(member.Config{As: "reader-1", Reader: true, Width: 1}, s, rn, noPush{}, &out)
	at := func(time.Duration) <-chan time.Time {
		c := make(chan time.Time, 1)
		c <- time.Unix(0, 0)
		return c
	}
	n, _ := memberLoop(m, loopRun{every: time.Hour, limit: 40, after: at}, &out, &errb)
	t.Logf("ticks=%d queues=%d began=%v started=%v\nOUT:\n%s\nERR:\n%s", n, s.queues, s.begins(), rn.started, out.String(), errb.String())
	require.Contains(t, rn.started, "r1", "the reader never took the asked read: out=%s", out.String())
	assert.Contains(t, s.begins(), "r1@1", "read --begin was never sent: %v", s.begins())
}

// TestReaderRedialsThroughTheDialer drives a reader's Send through
// newSprintDialer, the path production takes (member.go, send == nil), so the
// re-dial is pinned where the card asks: the server refuses a few times, the
// dialer rebuilds its client and backs off, and the reader resumes beating and
// takes the asked read (tla/LoopRedial.tla, ServerRestart, Redial, BeatsAgain).
func TestReaderRedialsThroughTheDialer(t *testing.T) {
	t.Parallel()
	s := &readerRedialSprint{downLeft: 3}
	rn := &readerRedialRunner{}
	var out, errb bytes.Buffer
	d := newSprintDialer("server.example:6390")
	waits := 0
	d.wait = func(context.Context, time.Duration) bool { waits++; return true }
	d.dial = func(_ context.Context, _ string, _ *http.Client, verbs ...[]string) ([]sprintwire.Result, error) {
		code, printed := s.Run(verbs[0]...)
		if code == 2 {
			return nil, errors.New(string(printed))
		}
		return []sprintwire.Result{{Code: code, Stdout: string(printed)}}, nil
	}
	w := &sprintwire.Worker{Send: d.do}
	m := member.New(member.Config{As: "reader-1", Reader: true, Width: 1}, w, rn, noPush{}, &out)
	at := func(time.Duration) <-chan time.Time {
		c := make(chan time.Time, 1)
		c <- time.Unix(0, 0)
		return c
	}
	memberLoop(m, loopRun{every: time.Hour, limit: 40, after: at}, &out, &errb)
	require.Contains(t, rn.started, "r1", "the reader never took the asked read through the dialer: out=%s", out.String())
	assert.Contains(t, s.begins(), "r1@1", "read --begin was never sent through the dialer: %v", s.begins())
	assert.Positive(t, waits, "the dialer never backed off on the refused server")
}

// TestReaderResumesAfterADroppedHeldWait pins the second restart: the server
// answers the queue (the reader holds a connection) but the restart drops the
// held read --begin ask a few times. The reader re-dials from its own transport
// and keeps beating, and it begins the asked read once the server holds the
// connection again (tla/LoopRedial.tla, ServerRestart, Redial, BeatsAgain).
func TestReaderResumesAfterADroppedHeldWait(t *testing.T) {
	t.Parallel()
	s := &readerRedialSprint{dropBegins: 3}
	rn := &readerRedialRunner{}
	var out, errb bytes.Buffer
	m := member.New(member.Config{As: "reader-1", Reader: true, Width: 1}, s, rn, noPush{}, &out)
	at := func(time.Duration) <-chan time.Time {
		c := make(chan time.Time, 1)
		c <- time.Unix(0, 0)
		return c
	}
	n, _ := memberLoop(m, loopRun{every: time.Hour, limit: 16, after: at}, &out, &errb)
	t.Logf("ticks=%d queues=%d began=%v started=%v\nOUT:\n%s\nERR:\n%s", n, s.queues, s.begins(), rn.started, out.String(), errb.String())
	require.Contains(t, rn.started, "r1", "the reader never took the asked read after the dropped wait: out=%s", out.String())
	assert.Contains(t, s.begins(), "r1@1", "read --begin was never sent after the dropped wait: %v", s.begins())
}
