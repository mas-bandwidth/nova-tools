package friend

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// outboxJob writes an outbox job's REPORT.md as a session words its report.
func outboxJob(t *testing.T, dir, job, verdict string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "outbox", job), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "outbox", job, "REPORT.md"), []byte("Verdict: "+verdict+"\nHead: "+strings.Repeat("a", 40)+"\n"), 0o644))
}

// epochCard is a held work card at epoch e, dealt after the clear.
func epochCard(card, col string, e uint64) HeldCard {
	job := card + "~" + strconv.FormatUint(e, 10)
	return HeldCard{Card: card, Job: job, Col: col, Epoch: e,
		Brief: "STATUS: nova-sprint card " + card + ", epoch " + strconv.FormatUint(e, 10) + ", attempt 1; when done, write outbox/" + job + "/REPORT.md\n"}
}

// The finding of 2026-10-10: after a clear, a friend's working dir kept 323 inbox items and
// 535 outbox REPORTs of epoch 15, each outbox one refused on every pass ("not on her row").
// On the sprint's new epoch, one daemon pass moves every older-epoch inbox and outbox item to
// the retired folder named for its epoch and leaves the live folders empty (the card
// friends-snap-to-the-new-epoch; docs/SPEC-FRIEND.md, the inbox).
func TestANewEpochRetiresTheOlderItemsAndStartsEmpty(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	dir := r.d.Dir
	landed, returned, who := "landed.w1~15", "returned.w1~15", "reader.w1~15"
	inboxJob(t, dir, landed, "STATUS: nova-sprint card landed.w1, epoch 15\n")
	inboxJob(t, dir, returned, "STATUS: nova-sprint card returned.w1, epoch 15\n")
	inboxJob(t, dir, who, "WHO: friend landed.w1\n")
	outboxJob(t, dir, landed, "LAND")
	outboxJob(t, dir, returned, "HOLD")
	inboxJob(t, dir, "by-hand", "the owner's note: read this\n")
	// the sprint at its new epoch: one card dealt after the clear, on her row at epoch 16
	row := &twinRow{}
	row.epoch = 16
	fresh := epochCard("fresh.w1", "working", 16)
	row.set(fresh)
	r.d.Held = row.held

	r.run(t, 1)

	// the live folders start the new epoch empty
	assert.NoDirExists(t, filepath.Join(dir, "inbox", landed))
	assert.NoDirExists(t, filepath.Join(dir, "inbox", returned))
	assert.NoDirExists(t, filepath.Join(dir, "inbox", who))
	assert.NoDirExists(t, filepath.Join(dir, "outbox", landed))
	assert.NoDirExists(t, filepath.Join(dir, "outbox", returned))
	// and the items are under the retired folder named for their epoch, kept, never deleted
	assert.FileExists(t, filepath.Join(dir, "retired", "epoch-15", "inbox", landed, "BRIEF.md"))
	assert.FileExists(t, filepath.Join(dir, "retired", "epoch-15", "inbox", returned, "BRIEF.md"))
	assert.FileExists(t, filepath.Join(dir, "retired", "epoch-15", "inbox", who, "BRIEF.md"))
	assert.FileExists(t, filepath.Join(dir, "retired", "epoch-15", "outbox", landed, "REPORT.md"))
	assert.FileExists(t, filepath.Join(dir, "retired", "epoch-15", "outbox", returned, "REPORT.md"))
	assert.FileExists(t, filepath.Join(dir, "inbox", "by-hand", "BRIEF.md"), "a job whose name parses to no epoch, by another hand, is never moved")
	assert.FileExists(t, filepath.Join(dir, "inbox", fresh.Job, "BRIEF.md"), "the new epoch's card is written as ever")
	moved := 0
	for _, l := range r.records {
		if strings.Contains(l, " epoch: retired ") {
			moved++
		}
	}
	assert.Equal(t, 5, moved, "one line per move: %v", r.records)
}

// A job whose name parses to no epoch is never moved, one a lane runs is kept, one on her row
// at its own epoch is kept, and the same card's older deal is retired with the rest; a brief
// written since the ask began is decided on the next pass (the inbox's retire guards).
func TestSnapEpochKeepsAndRefuses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	held := epochCard("held.w1", "working", 16)
	inboxJob(t, dir, "held.w1~16", held.Brief) // her row's own job is kept
	inboxJob(t, dir, "held.w1~15", held.Brief) // the same card's older deal is of an older epoch
	running := epochCard("running.w1", "working", 16)
	inboxJob(t, dir, "running.w1~15", running.Brief)
	inboxJob(t, dir, "by-hand", "the owner's note: read this\n")
	var lines []string
	row := Row{Epoch: 16, Cards: []HeldCard{held}}
	keep := map[string]bool{"running.w1~15": true}
	asked := time.Now()
	n, err := SnapEpoch(dir, row, keep, asked, t0, func(l string) { lines = append(lines, l) })
	require.NoError(t, err)
	assert.Equal(t, 1, n, "the row's own job is kept, the older deal is not: %v", lines)
	assert.NoDirExists(t, filepath.Join(dir, "inbox", "held.w1~15"))
	assert.DirExists(t, filepath.Join(dir, "inbox", "held.w1~16"))
	assert.DirExists(t, filepath.Join(dir, "inbox", "running.w1~15"), "a job a lane runs is kept")
	assert.DirExists(t, filepath.Join(dir, "inbox", "by-hand"), "a job whose name parses to no epoch, by another hand, is never moved")
	// a job whose brief was written since the ask began is friend sync's for a card dealt
	// after the answer: the next pass decides it, as the inbox's retire does
	later := epochCard("later.w1", "working", 16)
	inboxJob(t, dir, "later.w1~15", later.Brief)
	fresh := time.Now()
	require.NoError(t, os.Chtimes(filepath.Join(dir, "inbox", "later.w1~15"), fresh.Add(time.Second), fresh.Add(time.Second)))
	n, err = SnapEpoch(dir, row, keep, fresh, t0, func(l string) { lines = append(lines, l) })
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.DirExists(t, filepath.Join(dir, "inbox", "later.w1~15"))
	// asked before its write: moved, whole, to the folder named for its epoch
	n, err = SnapEpoch(dir, row, keep, fresh.Add(2*time.Second), t0, func(l string) { lines = append(lines, l) })
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.NoDirExists(t, filepath.Join(dir, "inbox", "later.w1~15"))
	assert.FileExists(t, filepath.Join(dir, "retired", "epoch-15", "inbox", "later.w1~15", "BRIEF.md"))
}

// A name taken in the retired folder never overwrites what is there: the move takes the job's
// name with the time beside it, as the inbox's retire does.
func TestSnapEpochNeverOverwrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	first := epochCard("first.w1", "working", 16)
	inboxJob(t, dir, "first.w1~15", first.Brief)
	to := filepath.Join(dir, "retired", "epoch-15", "inbox", "first.w1~15")
	require.NoError(t, os.MkdirAll(to, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(to, "BRIEF.md"), []byte("there before\n"), 0o644))
	row := Row{Epoch: 16}
	var lines []string
	n, err := SnapEpoch(dir, row, nil, time.Now().Add(time.Second), t0, func(l string) { lines = append(lines, l) })
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.FileExists(t, filepath.Join(dir, "retired", "epoch-15", "inbox", "first.w1~15", "BRIEF.md"), "what is there is never written over")
	assert.FileExists(t, filepath.Join(dir, "retired", "epoch-15", "inbox", "first.w1~15."+strconv.FormatInt(t0.Unix(), 10), "BRIEF.md"), "the move takes the name with the time beside it")
}

// On the same signal the lanes open fresh sessions: an epoch the row names past the one the
// lane state was opened under forgets every lane session, so a full or stale session never
// carries across a clear, across a restart either (the card friends-snap-to-the-new-epoch).
func TestAnEpochAdvanceOpensTheLanesFresh(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := &lanesHarness{finish: map[string]bool{}, active: map[string]int{}}
		h.dir = cardDirFixture(t, nil, nil, nil)
		r, state := laneRig(t, h, 1)
		*state = LaneState{Sessions: map[int]string{1: "old"}, Epoch: 15}
		row := &twinRow{}
		row.epoch = 16
		row.set(epochCard("fresh.w1", "working", 16))
		r.d.Held = row.held

		r.run(t, 3)

		turns, _, seeds := h.got()
		assert.NotEmpty(t, seeds, "the lane's next session is opened fresh: the seed is given again")
		assert.Contains(t, turns, "ses_1: fresh.w1", "the card runs in the lane's fresh session")
		assert.Equal(t, uint64(16), state.Epoch, "the lane state names the epoch its sessions were opened under")
		assert.Equal(t, map[int]string{1: "ses_1"}, state.Sessions, "the older epoch's session is gone, the fresh one kept")
		kept := 0
		for _, l := range r.records {
			if strings.Contains(l, " epoch: the lanes open fresh sessions") {
				kept++
			}
		}
		assert.Equal(t, 1, kept, "said once, on the advance: %v", r.records)
	})
}
