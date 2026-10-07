package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/binstamp"
	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// replaceRig is a member of width 2 over a fake sprint that lists two ready work
// cards (c1, c2), one taken a take, and a fake runner whose children end when the
// test says so; the "binary" is a file the test replaces mid-loop.
type replaceRig struct {
	exe      string
	onQueue  func(call int) // called at each queue verb, i.e. once per tick, before it answers
	empty    bool           // the sprint lists no card
	queues   int
	takes    int
	finishes []string
	taken    map[string]bool
	started  []string
	childEnd bool
	deadline int // the packets' route deadline, seconds (0: none)
}

func (r *replaceRig) Run(args ...string) (int, []byte) {
	switch args[0] {
	case "queue":
		r.queues++
		if r.onQueue != nil {
			r.onQueue(r.queues)
		}
		var cards []map[string]any
		for _, id := range []string{"c1", "c2"} {
			col := "ready"
			if r.taken[id] {
				col = "working"
			}
			if !r.empty {
				cards = append(cards, map[string]any{"id": id, "col": col, "gen": 1, "packet": r.pkt(id)})
			}
		}
		b, _ := json.Marshal(map[string]any{"as": "m1", "epoch": 1, "width": 2, "cards": cards})
		return 0, b
	case "take":
		r.takes++
		var ps []member.Packet
		for _, id := range []string{"c1", "c2"} {
			if !r.taken[id] && len(ps) < 1 { // one a take: c1 first
				r.taken[id] = true
				ps = append(ps, r.pkt(id))
			}
		}
		b, _ := json.Marshal(map[string]any{"packets": ps})
		return 0, b
	case "finish":
		r.finishes = append(r.finishes, args[3])
		delete(r.taken, strings.SplitN(args[3], "@", 2)[0])
	}
	return 0, nil
}

func (r *replaceRig) pkt(id string) member.Packet {
	return member.Packet{Card: id, Kind: "work", As: "m1", Attempt: 1, Gen: 1, Epoch: 1, Branch: "work/" + id, Deadline: r.deadline}
}

func (r *replaceRig) Start(p member.Packet) (member.Child, error) {
	r.started = append(r.started, p.Card)
	return &rigChild{r: r}, nil
}

type rigChild struct{ r *replaceRig }

func (c *rigChild) Done() bool { return c.r.childEnd }
func (c *rigChild) Result() member.Result {
	return member.Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Report: "done"}
}

type noPush struct{}

func (noPush) Push(member.Packet, member.Result) member.Push { return member.Push{None: "test"} }

func newReplaceRig(t *testing.T) (*replaceRig, *member.Member, *bytes.Buffer, func() string) {
	t.Helper()
	r := &replaceRig{exe: filepath.Join(t.TempDir(), "nova-swarm"), taken: map[string]bool{}}
	require.NoError(t, os.WriteFile(r.exe, []byte("the build the member began with"), 0o755))
	var out bytes.Buffer
	return r, member.New(member.Config{As: "m1"}, r, r, noPush{}, &out), &out, func() string { return binstamp.Of(r.exe) }
}

func (r *replaceRig) install(t *testing.T) {
	t.Helper()
	require.NoError(t, os.WriteFile(r.exe, []byte("the build installed under it, longer"), 0o755))
}

// Tonight (2026-10-01) six members kept running the binaries they began with until
// restarted by hand. A member reads its binary's file before every tick: with no
// child running it stops at once (exit 3, its supervisor starts the new build); an
// unchanged binary ticks on.
func TestMemberStopsAtOnceWhenItsBinaryIsReplacedAndNothingRuns(t *testing.T) {
	t.Parallel()
	r, m, out, stamp := newReplaceRig(t)
	r.empty = true
	var errb bytes.Buffer

	n, replaced := memberLoop(m, loopRun{every: time.Millisecond, limit: 3, stamp: stamp}, out, &errb)
	assert.False(t, replaced, "an unchanged binary ticks on")
	assert.Equal(t, 3, n)
	assert.NotContains(t, out.String(), "MEMBER STOP")

	r.queues = 0
	r.onQueue = func(call int) {
		if call == 1 {
			r.install(t)
		}
	}
	n, replaced = memberLoop(m, loopRun{every: time.Millisecond, limit: 10, stamp: stamp}, out, &errb)
	assert.True(t, replaced)
	assert.Equal(t, 1, n, "no tick after the replacement")
	assert.Equal(t, 1, r.queues)
	assert.Contains(t, out.String(), "MEMBER STOP the binary this member runs was replaced; its supervisor starts the new one")
}

// With children running the member does not kill them: it takes nothing new, says
// so once, reports each child as it ends, and stops when the last one is reported.
func TestMemberDrainsItsChildrenThenStopsWhenItsBinaryIsReplaced(t *testing.T) {
	t.Parallel()
	r, m, out, stamp := newReplaceRig(t)
	var errb bytes.Buffer
	r.onQueue = func(call int) {
		switch call {
		case 1: // tick 1 takes c1 (room is left for c2); the build is installed under it
			r.install(t)
		case 4: // the child ends before tick 4
			r.childEnd = true
		}
	}
	// the loop is bounded by 20 ticks: a drain that never stopped would hit it
	n, replaced := memberLoop(m, loopRun{every: time.Millisecond, limit: 20, stamp: stamp}, out, &errb)
	assert.True(t, replaced)
	assert.Equal(t, []string{"c1"}, r.started, "c2 was ready and room was left, and it was not taken after the replacement")
	assert.Equal(t, 1, r.takes, "no take once the binary was replaced")
	assert.Equal(t, []string{"c1@1"}, r.finishes, "the running child was reported, not killed")
	assert.Equal(t, 1, strings.Count(out.String(), "MEMBER DRAIN"), "said once")
	assert.Equal(t, 1, strings.Count(out.String(), "MEMBER STOP"))
	assert.Less(t, strings.Index(out.String(), "MEMBER DRAIN"), strings.Index(out.String(), "MEMBER STOP"))
	assert.Equal(t, 4, n, "ticks 2 and 3 ran with the child still running, tick 4 reported it")
	assert.Equal(t, 0, m.Running())
}
