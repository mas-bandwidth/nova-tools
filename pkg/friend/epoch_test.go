package friend

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A clear is a new sprint epoch: the inbox and outbox names carry the epoch, so one
// reconcile at a new epoch moves every older-epoch item to retired/epoch-<its epoch>,
// whole and kept, and the live folders are the new epoch's. The same signal starts a
// fresh session for the next presence check and lane (docs/FRIENDS.md, the inbox/outbox
// standard; the seat ledger v1.2.4, friend snap).

const snapHead = "0123456789abcdef0123456789abcdef01234567"

func TestANewEpochLeavesTheLiveFoldersEmptyAndRetiresTheOld(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	dir := r.d.Dir
	row := &twinRow{}
	r.d.Held = row.held
	// epoch 15's inbox and outbox, and a note another hand put in her inbox
	inboxJob(t, dir, "old.w1~15", "STATUS: nova-sprint card old.w1, epoch 15, attempt 1\n\nold: the brief\n")
	outboxReport(t, dir, "old.w1~15", "Verdict: LAND\nHead: "+snapHead+"\n\nold: done\n")
	inboxJob(t, dir, "by-hand", "the owner's note: read this\n")
	// the sprint is at epoch 16
	now := workCard("now.w1", "working")
	now.Epoch, now.Job = 16, "now.w1~16"
	now.Brief = "STATUS: nova-sprint card now.w1, epoch 16, attempt 1\n\nnow: the brief\n"
	row.set(now)

	r.run(t, 1)

	assert.NoDirExists(t, filepath.Join(dir, "inbox", "old.w1~15"), "the old epoch's inbox job is not live")
	assert.NoDirExists(t, filepath.Join(dir, "outbox", "old.w1~15"), "the old epoch's outbox job is not live")
	assert.FileExists(t, filepath.Join(dir, RetiredEpochDir(15), "inbox", "old.w1~15", "BRIEF.md"), "it is kept, whole, under the folder named for its epoch")
	assert.FileExists(t, filepath.Join(dir, RetiredEpochDir(15), "outbox", "old.w1~15", "REPORT.md"), "the outbox item too")
	assert.FileExists(t, filepath.Join(dir, "inbox", "by-hand", "BRIEF.md"), "a job that names no epoch is never moved")
	assert.FileExists(t, filepath.Join(dir, "inbox", "now.w1~16", "BRIEF.md"), "the new epoch's job is written")
	assert.Contains(t, strings.Join(r.records, "\n"), fmt.Sprintf("epoch: retired inbox/old.w1~15 to %s/inbox/old.w1~15: epoch 15 is retired, epoch 16 is live", RetiredEpochDir(15)), "the move is said on the record")
}

// SnapToEpoch moves only an older epoch's jobs; a hand-placed name, the current epoch's
// job, and a later epoch's job stay live.
func TestSnapToEpochMovesOnlyTheOlderEpoch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, job := range []string{"old~15", "old~14.g2", "now~16", "ahead~17", "by-hand"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", job), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", job, "BRIEF.md"), []byte("x\n"), 0o644))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "outbox", job), 0o755))
	}

	var said []string
	moved, err := SnapToEpoch(dir, 16, nil, t0, func(l string) { said = append(said, l) })

	require.NoError(t, err)
	assert.Equal(t, 4, moved, "old~15 and old~14.g2 twice over")
	assert.NoDirExists(t, filepath.Join(dir, "inbox", "old~15"))
	assert.NoDirExists(t, filepath.Join(dir, "outbox", "old~15"))
	assert.NoDirExists(t, filepath.Join(dir, "inbox", "old~14.g2"))
	assert.DirExists(t, filepath.Join(dir, "inbox", "now~16"))
	assert.DirExists(t, filepath.Join(dir, "outbox", "now~16"))
	assert.DirExists(t, filepath.Join(dir, "inbox", "ahead~17"))
	assert.DirExists(t, filepath.Join(dir, "inbox", "by-hand"))
	assert.DirExists(t, filepath.Join(dir, RetiredEpochDir(15), "inbox", "old~15"))
	assert.DirExists(t, filepath.Join(dir, RetiredEpochDir(14), "outbox", "old~14.g2"))
	assert.Len(t, said, 4)
}

// A clear starts a fresh session: her lane state holds an epoch-15 session, and the first
// laneStep at epoch 16 forgets it, so the lane opens a new one.
func TestANewEpochStartsAFreshLaneSession(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := cardDirFixture(t, [][2]string{{"c0", "working"}}, []string{"c0"}, nil)
		h := &lanesHarness{dir: dir, finish: map[string]bool{"c0": true}, reject: map[string]string{}, active: map[string]int{}}
		r, state := laneRig(t, h, 1)
		row := &twinRow{}
		row.set(HeldCard{Card: "c0", Job: "c0~16", Col: "working", Epoch: 16, Brief: "STATUS: nova-sprint card c0, epoch 16, attempt 1\n\nc0: the brief\n"})
		r.d.Held = row.held
		*state = LaneState{Epoch: 15, Sessions: map[int]string{1: "ses_old"}}

		r.run(t, 10)

		assert.Equal(t, 16, state.Epoch, "the lanes record the epoch they last opened at")
		if assert.NotEmpty(t, state.Sessions, "the lane opens a session at the new epoch") {
			assert.NotEqual(t, "ses_old", state.Sessions[1], "the epoch-15 session is never carried across the clear")
		}
	})
}
