package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// startFriend is the friend's start receipt for the oldest n cards ready on her row, as
// friend sync reads her jobs begun (store.FriendStartStep): each goes to working, its
// deadline from now (docs/SPEC-SPRINT.md section 1, a friend's card is working once she
// starts it).
func (ta *testApp) startFriend(friend string, n int) {
	ta.t.Helper()
	st, err := ta.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(ta.t, err)
	ready, err := st.ReadCells(context.Background(), sprint.Fleet, sprint.FriendRow(friend), sprint.Ready)
	require.NoError(ta.t, err)
	sprint.SortCards(ready)
	r := sprint.FriendStartReq{Friend: friend, Gens: map[string]int{}}
	for _, c := range ready[:min(n, len(ready))] {
		r.IDs, r.Gens[c.ID] = append(r.IDs, c.ID), max(c.Int("gen"), 1)
	}
	if len(r.IDs) == 0 {
		return
	}
	res, err := st.Run(context.Background(), store.FriendStartStep(r))
	require.NoError(ta.t, err)
	require.Empty(ta.t, res.Refused)
}

// beginJob is the friend beginning job <job> in her working directory under root, as her
// daemon stages it and she then writes in it: its JOB.md an hour old, and a file of hers now.
func beginJob(t *testing.T, root, friend, job string) {
	t.Helper()
	dir := filepath.Join(root, friend+"-working", "jobs", job)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	jobFile := filepath.Join(dir, "JOB.md")
	require.NoError(t, os.WriteFile(jobFile, []byte("# JOB\n"), 0o644))
	old := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(jobFile, old, old))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("begun\n"), 0o644))
}

// Friend sync is a friend's start receipt (docs/SPEC-SPRINT.md section 1, a friend's card is
// working once she starts it): her card is dealt ready, stays ready while its job is only
// staged, goes to working when sync reads a write of hers in its job after its staging, and a
// report on a card still ready is read as her start and collected in the same sync.
func TestFriendSyncStartsACardWhoseJobSheBegan(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	var c cardView
	ta.json("card s1-1", &c)
	require.Len(t, c.Work, 1)
	require.Equal(t, sprint.Ready, c.Work[0].Col, "dealt ready")
	ta.ok("friend sync --root " + root)

	// staged and not written in: not begun
	dir := filepath.Join(root, "amy-working", "jobs", "s1-1.w1")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "JOB.md"), []byte("# JOB\n"), 0o644))
	out := ta.ok("friend sync --root " + root)
	assert.NotContains(t, out, "FRIEND-CARD STARTED")
	ta.json("card s1-1", &c)
	assert.Equal(t, sprint.Ready, c.Work[0].Col)

	beginJob(t, root, "amy", "s1-1.w1")
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD STARTED friend=amy card=s1-1.w1 job=s1-1.w1: a write under jobs/s1-1.w1 after its staging")
	c = cardView{}
	ta.json("card s1-1", &c)
	assert.Equal(t, sprint.Working, c.Work[0].Col, "working once she starts it")
	assert.Equal(t, 1, whereFriends(ta)["amy"].Working, "the friends table counts started cards")
	ta.clean()
}

func TestFriendSyncCollectsAReportOnACardStillReady(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	ta.ok("friend sync --root " + root)
	outboxReport(t, root, "amy", "s1-1.w1", "Verdict: LAND\nHead: "+landHead+"\n\nDone.\n")
	out := ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD STARTED friend=amy card=s1-1.w1 job=s1-1.w1: her report is in outbox/s1-1.w1")
	assert.Contains(t, out, "FRIEND-CARD FINISHED friend=amy card=s1-1.w1 result=ok")
	ta.clean()
}
