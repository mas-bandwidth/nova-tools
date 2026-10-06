package friend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The daemon writes every card she holds (the finding of 2026-10-05: the friend daemons
// held cards on their rows, taken by the deal in batch mode or by the coordinator's friend
// take, and wrote no inbox/<job>/BRIEF.md for them, so no lane ran them). twinRow stands for
// the sprint server's answer to friend cards <friend>: the cards on her row however they got
// there, with no bus event for any of them.
type twinRow struct {
	mu    sync.Mutex
	cards []HeldCard
	err   error
	asked int
}

func (w *twinRow) held(context.Context) ([]HeldCard, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.asked++
	return append([]HeldCard(nil), w.cards...), w.err
}

func (w *twinRow) set(cards ...HeldCard) { w.mu.Lock(); w.cards = cards; w.mu.Unlock() }

// workCard is a held work card as friend sync renders its brief (its STATUS line first).
func workCard(card, col string) HeldCard {
	job := card + "~15"
	return HeldCard{Card: card, Job: job, Col: col, Brief: fmt.Sprintf("STATUS: nova-sprint card %s, epoch 15, attempt 1; push your work to the branch sprint/%s.g1.e15; when done, write outbox/%s/REPORT.md with Verdict: LAND|HOLD|FAIL and Head: <sha>\n\n%s: the brief\n", card, card, job, card)}
}

func inboxJob(t *testing.T, dir, job, brief string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox", job), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", job, "BRIEF.md"), []byte(brief), 0o644))
}

func briefOf(t *testing.T, dir, job string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "inbox", job, "BRIEF.md"))
	if err != nil {
		return ""
	}
	return string(raw)
}

func TestEveryHeldCardIsWrittenToTheInbox(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	dir := r.d.Dir
	row := &twinRow{}
	r.d.Held = row.held
	// her inbox already holds forty jobs on her row, a sprint job whose card was dropped, and a
	// job put there by another hand; her queue file names none of the new cards (it is never
	// pruned: 292 tasks on 2026-10-05)
	var cards []HeldCard
	for i := range 40 {
		c := workCard(fmt.Sprintf("old-%02d.w1", i), "working")
		inboxJob(t, dir, c.Job, c.Brief)
		cards = append(cards, c)
	}
	dropped := workCard("dropped.w1", "working")
	inboxJob(t, dir, dropped.Job, dropped.Brief)
	inboxJob(t, dir, "by-hand", "Glenn's note: read this\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`{"tasks":[{"id":"stale.w1","state":"queued"}]}`), 0o644))
	// one card taken onto her row by another actor (friend take: working), one dealt to her in
	// batch mode (ready behind it): no message on her stream says either
	taken, dealt := workCard("taken.w2", "working"), workCard("dealt.w3", "ready")
	row.set(append(cards, taken, dealt)...)

	r.run(t, 1)

	assert.Equal(t, taken.Brief, briefOf(t, dir, taken.Job), "the card taken onto her row is written after one loop")
	assert.Equal(t, dealt.Brief, briefOf(t, dir, dealt.Job), "the card dealt in batch mode is written after one loop")
	assert.NoDirExists(t, filepath.Join(dir, "inbox", dropped.Job), "a dropped card's job leaves her inbox")
	assert.FileExists(t, filepath.Join(dir, RetiredDir, dropped.Job, "BRIEF.md"), "and is retired, not deleted")
	assert.FileExists(t, filepath.Join(dir, "inbox", "by-hand", "BRIEF.md"), "a job no sprint brief heads is never moved")
	var wrote []string
	for _, l := range r.records {
		if strings.Contains(l, " inbox: wrote ") {
			wrote = append(wrote, l)
		}
	}
	require.Len(t, wrote, 2, "one line per write: %v", r.records)
	assert.Contains(t, wrote[0], "inbox/taken.w2~15/BRIEF.md (card taken.w2, working on her row)")
	assert.Contains(t, wrote[1], "inbox/dealt.w3~15/BRIEF.md (card dealt.w3, ready on her row)")
	s := r.last()
	assert.True(t, s.HeldKnown)
	assert.Equal(t, [3]int{42, 42, 0}, [3]int{s.Held, s.InboxJobs, s.Missing}, "held, inbox and missing as status prints them")
	// the turn runs on its own goroutine, which the loop's end does not wait for
	delivered := func() string { r.mu.Lock(); defer r.mu.Unlock(); return strings.Join(r.delivered, "\n") }
	require.Eventually(t, func() bool { return delivered() != "" }, 10*time.Second, time.Millisecond)
	assert.Contains(t, delivered(), "inbox/taken.w2~15/BRIEF.md", "the batch session is told of each brief written")
	assert.Contains(t, delivered(), "inbox/dealt.w3~15/BRIEF.md")

	// the taken card goes back off her row (returned): the next loop retires its job, and a
	// brief there is never written over
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", dealt.Job, "BRIEF.md"), []byte(dealt.Brief+"her notes\n"), 0o644))
	row.set(append(cards, dealt)...)
	r.run(t, 2)
	assert.NoDirExists(t, filepath.Join(dir, "inbox", taken.Job))
	assert.FileExists(t, filepath.Join(dir, RetiredDir, taken.Job, "BRIEF.md"))
	assert.Contains(t, briefOf(t, dir, dealt.Job), "her notes", "a brief already there is never replaced")
	assert.Equal(t, [3]int{41, 41, 0}, [3]int{r.last().Held, r.last().InboxJobs, r.last().Missing})
}

// An answer that does not come writes nothing and retires nothing, and is said once.
func TestAHeldAnswerThatFailsTouchesNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	dir := r.d.Dir
	row := &twinRow{err: errors.New("friend cards refused: the server runs the workers' verbs only")}
	r.d.Held = row.held
	c := workCard("kept.w1", "working")
	inboxJob(t, dir, c.Job, c.Brief)
	r.run(t, 3)
	assert.FileExists(t, filepath.Join(dir, "inbox", c.Job, "BRIEF.md"))
	assert.NoDirExists(t, filepath.Join(dir, RetiredDir))
	n := 0
	for _, l := range r.records {
		if strings.Contains(l, "inbox: the server did not say") {
			n++
		}
	}
	assert.Equal(t, 1, n, "said once until it changes: %v", r.records)
	assert.Equal(t, 3, row.asked, "asked every loop")
	assert.False(t, r.last().HeldKnown)
	assert.Contains(t, r.last().InboxError, "friend cards refused")
}

// SyncInbox refuses a job that is no inbox directory, and a symlinked one; a job a lane is
// running is kept though its card left her row.
func TestSyncInboxRefusesAndKeeps(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox"), 0o755))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "inbox", "linked.w1~15")))
	running := workCard("running.w1", "working")
	inboxJob(t, dir, running.Job, running.Brief)
	var lines []string
	c, err := SyncInbox(dir, []HeldCard{
		{Card: "escape.w1", Job: "../escape", Brief: "STATUS: nova-sprint card x\n"},
		{Card: "linked.w1", Job: "linked.w1~15", Brief: "STATUS: nova-sprint card linked.w1\n"},
		{Card: "empty.w1", Job: "empty.w1~15"},
	}, map[string]bool{running.Job: true}, time.Now(), t0, func(l string) { lines = append(lines, l) })
	require.NoError(t, err)
	assert.Equal(t, InboxCounts{Held: 3, Inbox: 1, Missing: 3}, c)
	assert.NoFileExists(t, filepath.Join(outside, "BRIEF.md"), "nothing is written through a symlink")
	assert.NoFileExists(t, filepath.Join(dir, "escape", "BRIEF.md"))
	assert.FileExists(t, filepath.Join(dir, "inbox", running.Job, "BRIEF.md"), "a running lane's job is kept")
	assert.Len(t, lines, 3, "one line per refusal: %v", lines)
}

// A free lane is handed a held card her queue file does not name, once its brief is written.
func TestALaneIsHandedAHeldCardTheQueueFileDoesNotName(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := &lanesHarness{finish: map[string]bool{"held.w1": true}, active: map[string]int{}}
		h.dir = cardDirFixture(t, [][2]string{{"stale.w1", "done"}}, nil, nil)
		r, _ := laneRig(t, h, 1)
		row := &twinRow{}
		row.set(workCard("held.w1", "working"))
		r.d.Held = row.held
		r.run(t, 3)
		turns, _, _ := h.got()
		assert.Equal(t, []string{"ses_1: held.w1"}, turns)
	})
}

// A brief written after the ask began is kept though the answer does not name it: friend
// sync wrote it for a card dealt after the server answered (the TLA+ model InboxReconcile's
// reversed witness retires it: docs/SPEC-FRIEND.md).
func TestABriefWrittenSinceTheAskIsNotRetired(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	asked := time.Now()
	fresh, stale := workCard("fresh.w1", "working"), workCard("stale.w1", "working")
	inboxJob(t, dir, fresh.Job, fresh.Brief)
	inboxJob(t, dir, stale.Job, stale.Brief)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "inbox", fresh.Job, "BRIEF.md"), asked.Add(time.Second), asked.Add(time.Second)))
	require.NoError(t, os.Chtimes(filepath.Join(dir, "inbox", stale.Job, "BRIEF.md"), asked.Add(-time.Second), asked.Add(-time.Second)))
	c, err := SyncInbox(dir, nil, nil, asked, t0, func(string) {})
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dir, "inbox", fresh.Job, "BRIEF.md"), "written since the ask: the next pass decides it")
	assert.FileExists(t, filepath.Join(dir, RetiredDir, stale.Job, "BRIEF.md"))
	assert.Equal(t, InboxCounts{Inbox: 1}, c)
	c, err = SyncInbox(dir, nil, nil, asked.Add(2*time.Second), t0, func(string) {})
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dir, RetiredDir, fresh.Job, "BRIEF.md"), "and the next pass, asked after it, retires it")
	assert.Equal(t, InboxCounts{}, c)
}
