package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// lcrSprint lists one work card c1 working at gen 1, epoch 1, with its packet; a finish is
// answered with finishCode; every verb is recorded.
type lcrSprint struct {
	mu         sync.Mutex
	finishCode int
	finishes   int
}

func (s *lcrSprint) Run(args ...string) (int, []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch args[0] {
	case "queue":
		p := lcrPacket()
		b, _ := json.Marshal(map[string]any{"as": "m1", "epoch": 1, "width": 1,
			"cards": []map[string]any{{"id": "c1", "col": "working", "gen": 1, "packet": p}}})
		return 0, b
	case "finish":
		s.finishes++
		if s.finishCode != 0 {
			return s.finishCode, []byte("refused: its primary p-c1 is not working on it")
		}
	}
	return 0, nil
}

func lcrPacket() member.Packet {
	return member.Packet{Card: "c1", Kind: "work", As: "m1", Attempt: 1, Gen: 1, Epoch: 1, Branch: "work/c1"}
}

type lcrChild struct {
	mu   sync.Mutex
	done bool
}

func (c *lcrChild) Done() bool { c.mu.Lock(); defer c.mu.Unlock(); return c.done }
func (c *lcrChild) end()       { c.mu.Lock(); defer c.mu.Unlock(); c.done = true }
func (c *lcrChild) Result() member.Result {
	return member.Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "0123456789abcdef0123456789abcdef01234567", Report: "done"}
}

// lcrDirRunner starts a launch as nativeRunner.Start leaves one running (its directory under
// the slots made, the launch marked live) and ends it through the real nativeRunner.Ended.
type lcrDirRunner struct {
	nr       *nativeRunner
	mu       sync.Mutex
	names    []string
	children []*lcrChild
}

func (d *lcrDirRunner) Start(p member.Packet) (member.Child, error) {
	name := launchName(p)
	if err := os.MkdirAll(filepath.Join(d.nr.slots, name, "jobs", p.Card), 0o755); err != nil {
		return nil, err
	}
	d.nr.started(name)
	c := &lcrChild{}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.names = append(d.names, name)
	d.children = append(d.children, c)
	return c, nil
}

func (d *lcrDirRunner) Ended(p member.Packet, failed bool) { d.nr.Ended(p, failed) }

type lcrShaPusher struct{}

func (lcrShaPusher) Push(member.Packet, member.Result) member.Push {
	return member.Push{Sha: "0123456789abcdef0123456789abcdef01234567"}
}

// DEFECT (2026-10-01, fixed): the cleaner removed the directory of a running launch that reused an
// ended launch's name. A launch's name is <card>.g<gen>.e<epoch>, so a card run again at the same
// claim reuses it: here an ok finish the sprint refuses (exit 1: "its primary is not working on
// it") is forgotten (member.go, Tick: `m.forget(id, code == 0)`), and the next pass recovers the
// still-working card at the same gen and epoch and starts it again under the same name. Today a
// refused report tags nothing (Ended, not accepted: the slot is kept for the sweep), and a retire
// by name, the cleaner's or the sweep's, never removes a launch that is live again (retire asks
// r.live under the removing lock the start claims the name under).
func TestReviewTheCleanerKeepsTheDirectoryOfANewLaunchOfTheSameName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	slots := filepath.Join(root, "slots")
	require.NoError(t, os.MkdirAll(slots, 0o755))
	nr := &nativeRunner{root: root, slots: slots, stderr: io.Discard, tagged: make(chan string, cleanQueue)} // the cleaner is behind: its queue is worked by the test
	rn := &lcrDirRunner{nr: nr}
	sp := &lcrSprint{finishCode: 1}
	m := member.New(member.Config{As: "m1", Width: 1}, sp, rn, lcrShaPusher{}, &bytes.Buffer{})

	_, err := m.Tick(time.Unix(0, 0)) // recovers c1: launch c1.g1.e1
	require.NoError(t, err)
	require.Len(t, rn.children, 1)
	rn.children[0].end()
	_, err = m.Tick(time.Unix(0, 0)) // pushed, finish refused (exit 1): forgotten, Ended not accepted: kept, nothing tagged
	require.NoError(t, err)
	require.Equal(t, 1, sp.finishes)
	require.Empty(t, nr.tagged, "a refused report tags nothing")
	require.DirExists(t, filepath.Join(slots, "c1.g1.e1"), "the slot is kept")
	_, err = m.Tick(time.Unix(0, 0)) // the card is still working: recovered under the same name
	require.NoError(t, err)
	require.Equal(t, []string{"c1.g1.e1", "c1.g1.e1"}, rn.names, "the same launch name, started again")
	require.False(t, rn.children[1].Done(), "the new launch is running")

	nr.retire("c1.g1.e1", time.Now()) // the cleaner or the sweep reaches the name
	assert.DirExists(t, filepath.Join(slots, "c1.g1.e1"), "the running launch's directory is never removed")
}

// lcrHeldRunner's Start waits for release, and closes returned when it gives the child.
type lcrHeldRunner struct {
	entered, release, returned chan struct{}
}

func (h *lcrHeldRunner) Start(member.Packet) (member.Child, error) {
	close(h.entered)
	<-h.release
	close(h.returned)
	return &lcrChild{}, nil
}

// DEFECT: a bounded member (--once, --ticks <n>) returns with long work in flight. With
// Background (cmdMember always sets it) a start, a result read and a push run in goroutines;
// memberLoop returns at `if limit > 0 && n >= limit` (member.go) without waiting for them, and
// cmdMember prints MEMBER OK and exits: a card the pass took (the sprint has it working) is
// never started, or is cut between cmd.Start and its pid file (so the next member cannot adopt
// it and stages the slot again under the running child); a push in flight is cut off.
func TestReviewABoundedMemberLoopDoesNotReturnWithAStartInFlight(t *testing.T) {
	t.Parallel()
	rn := &lcrHeldRunner{entered: make(chan struct{}), release: make(chan struct{}), returned: make(chan struct{})}
	sp := &lcrSprint{}
	var out, errb bytes.Buffer
	m := member.New(member.Config{As: "m1", Width: 1, Background: true}, sp, rn, lcrShaPusher{}, &out)
	// the loop waits for the start it began (WaitLong), so it runs beside the test, which
	// lets the start go once it is in flight
	type result struct {
		n        int
		replaced bool
	}
	done := make(chan result)
	go func() {
		n, replaced := memberLoop(m, time.Hour, 1, func() string { return "" }, &out, &errb)
		done <- result{n, replaced}
	}()
	<-rn.entered
	select {
	case <-done:
		t.Fatal("memberLoop --once returned while the start it began is still in flight; the process exits under it")
	default:
	}
	close(rn.release)
	got := <-done
	require.Equal(t, 1, got.n)
	require.False(t, got.replaced)
	select {
	case <-rn.returned:
	default:
		t.Error("memberLoop --once returned before the start it began had ended")
	}
}
