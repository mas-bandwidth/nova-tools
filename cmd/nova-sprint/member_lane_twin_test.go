package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// slowFinish is the twin store with every finish held in flight, as a store
// 100 ms away that loses the fence to the tick holds it: at each finish it
// calls at, and the finish goes on only when at returns (the test holds it
// there while it looks: no clock).
type slowFinish struct {
	twinSprint
	at func()
}

func (s slowFinish) Run(args ...string) (int, []byte) {
	if len(args) > 0 && args[0] == "finish" {
		s.at()
	}
	return s.twinSprint.Run(args...)
}

// The owner, 2026-10-01: "space stuck on 15 working for some time, width is 16.
// why?" / "Do the minimal change that would keep working at maximum." A member
// at its width whose child exits fills that lane first in its next pass, before
// the finish that reports the child (held in flight here while the test looks):
// the third card is started before the finish is sent, so the member is back at
// its width without waiting for it; no more than the width is ever alive; no
// card is started twice.
func TestAFreedLaneIsFilledBeforeASlowFinish(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "sprint.twin")
	twin := func(line string) {
		t.Helper()
		code, o, e := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s\n%s%s", line, o, e)
	}
	for _, line := range []string{
		"nova-sprint init --readers reader-a,reader-b --members m1:2",
		"nova-sprint add --stream s1 --count 6",
		"nova-sprint start",
		"nova-sprint tick",
		"nova-sprint tick",
	} {
		twin(line)
	}
	rn := &twinRunner{children: map[string]*twinChild{}}
	var log bytes.Buffer
	var m *member.Member
	finishes := 0
	var startedAtFinish, liveAtFinish int
	sprint := slowFinish{twinSprint: twinSprint{file: file, actor: "m1"}, at: func() {
		finishes++
		startedAtFinish, liveAtFinish = len(rn.packets), m.Live()
	}}
	m = member.New(member.Config{As: "m1"}, sprint, rn, &twinPusher{push: member.Push{Sha: "0123456789abcdef0123456789abcdef01234567"}}, &log)
	tick := func() {
		t.Helper()
		_, err := m.Tick(time.Unix(0, 0))
		require.NoError(t, err, log.String())
	}
	tick()
	require.Len(t, rn.packets, 2, "at its width of 2: %s", log.String())

	c := rn.children[rn.packets[0].Card]
	c.mu.Lock()
	c.done, c.res = true, member.Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "0123456", Report: "did it"}
	c.mu.Unlock()
	tick()
	require.Equal(t, 1, finishes, "the ended child is reported in the pass: %s", log.String())
	assert.Equal(t, 3, startedAtFinish, "the freed lane was filled before the finish was sent: %s", log.String())
	assert.Equal(t, 2, liveAtFinish, "back at the width while the finish is in flight")
	assert.Equal(t, 2, m.Live(), "never more than the width alive")
	seen := map[string]bool{}
	for _, p := range rn.packets {
		assert.False(t, seen[p.Card], "%s started twice: %s", p.Card, log.String())
		seen[p.Card] = true
	}
	assert.Equal(t, 1, strings.Count(log.String(), "start "+rn.packets[2].Card+" "), "started once: %s", log.String())
}
