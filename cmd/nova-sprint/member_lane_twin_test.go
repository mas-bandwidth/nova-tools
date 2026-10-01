package main

import (
	"bytes"
	"path/filepath"
	"slices"
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

// lostFinish is the twin store that does not answer the finishes fail names,
// as a store 100 ms away whose fence a verb lost twelve times: exit 2, nothing
// changed.
type lostFinish struct {
	twinSprint
	fail func(card string) bool
}

func (s lostFinish) Run(args ...string) (int, []byte) {
	if len(args) > 3 && args[0] == "finish" && s.fail(args[3]) {
		return 2, []byte("nova-sprint finish: the sprint is busy: other operations kept the fence moving, 12 reads in 3.862s; nothing was changed; run the verb again")
	}
	return s.twinSprint.Run(args...)
}

// The fleet pass of 2026-10-01 16:32 ET: hetzner, 100 ms from the store, lost one
// finish to the fence, ended its pass there, and tried the same card again every
// pass while seven finished cards waited unreported behind it. A verb the store
// does not answer never ends the pass: the other ended children are reported in
// the same pass, the take still fills the lanes, the pass says which verb was
// not answered, and the next pass tries it again.
func TestAPassGoesOnPastAFinishTheStoreDidNotAnswer(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "sprint.twin")
	for _, line := range []string{
		"nova-sprint init --readers reader-a,reader-b --members m1:3",
		"nova-sprint add --stream s1 --count 9",
		"nova-sprint start",
		"nova-sprint tick",
		"nova-sprint tick",
	} {
		code, o, e := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s\n%s%s", line, o, e)
	}
	rn := &twinRunner{children: map[string]*twinChild{}}
	var log bytes.Buffer
	lost := ""
	var finished []string
	sprint := lostFinish{twinSprint: twinSprint{file: file, actor: "m1"}, fail: func(card string) bool {
		finished = append(finished, card)
		return strings.HasPrefix(card, lost+"@")
	}}
	m := member.New(member.Config{As: "m1"}, sprint, rn, &twinPusher{push: member.Push{Sha: "0123456789abcdef0123456789abcdef01234567"}}, &log)
	_, err := m.Tick(time.Unix(0, 0))
	require.NoError(t, err, log.String())
	require.Len(t, rn.packets, 3, "at its width of 3: %s", log.String())

	first := []string{rn.packets[0].Card, rn.packets[1].Card, rn.packets[2].Card}
	for _, id := range first {
		c := rn.children[id]
		c.mu.Lock()
		c.done, c.res = true, member.Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "0123456", Report: "did it"}
		c.mu.Unlock()
	}
	// the store does not answer the finish of the card the pass reports first
	sorted := append([]string(nil), first...)
	slices.Sort(sorted)
	lost = sorted[0]
	acted, err := m.Tick(time.Unix(0, 0))
	require.Error(t, err, "the pass says a verb was not answered: %s", log.String())
	assert.Contains(t, err.Error(), "the store did not answer 1 of this pass's verbs (finish "+lost+")")
	assert.Len(t, finished, 3, "every ended child got its finish in the one pass: %v\n%s", finished, log.String())
	assert.Contains(t, log.String(), "NOTE finish "+lost+": the store did not answer; the pass goes on")
	assert.Len(t, rn.packets, 6, "the three freed lanes were filled in the same pass: %s", log.String())
	assert.Equal(t, 3, m.Live(), "at the width, never over it")
	assert.Equal(t, 5, acted, "two finishes reported and three cards started")

	// the next pass tries the unanswered finish again, and the store takes it
	lost, finished = "none", nil
	_, err = m.Tick(time.Unix(0, 0))
	require.NoError(t, err, log.String())
	require.Len(t, finished, 1, "only the finish not answered is sent again: %v", finished)
	assert.True(t, strings.HasPrefix(finished[0], sorted[0]+"@"), "%v", finished)
	seen := map[string]bool{}
	for _, p := range rn.packets {
		assert.False(t, seen[p.Card], "%s started twice: %s", p.Card, log.String())
		seen[p.Card] = true
	}
}
