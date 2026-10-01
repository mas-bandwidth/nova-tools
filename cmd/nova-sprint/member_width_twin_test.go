package main

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// The member's width is its fleet row's (the owner, 2026-10-01: the row and the
// flag disagreed, and a second card sat in ready): a member started with no
// --width runs as many as its row names, read with its queue every tick, and a row
// lowered mid-run takes nothing new until the running fall under it.
func TestAMemberRunsTheWidthOfItsFleetRow(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "sprint.twin")
	twin := func(line string) {
		t.Helper()
		code, o, e := twinProcess(t, file, line)
		require.Equal(t, 0, code, "%s\n%s%s", line, o, e)
	}
	for _, line := range []string{
		"nova-sprint init --readers reader-a,reader-b --members m1:2",
		"nova-sprint add --stream s1 --count 3",
		"nova-sprint start",
		"nova-sprint tick",
		"nova-sprint tick",
	} {
		twin(line)
	}
	rn := &twinRunner{children: map[string]*twinChild{}}
	var log bytes.Buffer
	m := member.New(member.Config{As: "m1"}, twinSprint{file: file, actor: "m1"}, rn, &twinPusher{push: member.Push{Sha: "0123456789abcdef0123456789abcdef01234567"}}, &log)
	tick := func() {
		t.Helper()
		_, err := m.Tick(time.Unix(0, 0))
		require.NoError(t, err, log.String())
	}
	tick()
	require.Contains(t, log.String(), "width 0 -> 2 (the fleet row)", "the row's width is read with the queue")
	require.Len(t, rn.packets, 2, "two children at width 2: %s", log.String())

	twin("nova-sprint fleet up m1 --width 1")
	twin("nova-sprint tick")
	end := func(card string) {
		c := rn.children[card]
		c.mu.Lock()
		c.done, c.res = true, member.Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "0123456", Report: "did it"}
		c.mu.Unlock()
	}
	end(rn.packets[0].Card)
	tick() // the ended child is pushed
	tick() // and reported; one still runs at width 1: nothing new is taken
	assert.Equal(t, 1, m.Running())
	assert.Len(t, rn.packets, 2, "no new take while the running fill the lowered width: %s", log.String())
	assert.Contains(t, log.String(), "width 2 -> 1 (the fleet row)")

	end(rn.packets[1].Card)
	twin("nova-sprint tick")
	tick()
	tick()
	twin("nova-sprint tick")
	tick()
	assert.Len(t, rn.packets, 3, "a place under the width takes the third card: %s", log.String())
}
