package main

import (
	"bytes"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
)

// readerRedialSprint is a fake sprint server for a reader: it refuses every
// verb (the server is down) for the first downLeft calls, then answers with one
// asked read. It records the verbs it answered.
type readerRedialSprint struct {
	mu       sync.Mutex
	downLeft int
	began    []string
	queues   int
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
