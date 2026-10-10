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

func (w *twinRow) held(context.Context) (Row, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.asked++
	return Row{Cards: append([]HeldCard(nil), w.cards...), Reads: true, From: FromCards}, w.err
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
	inboxJob(t, dir, "by-hand", "the owner's note: read this\n")
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
	c, err := SyncInbox(dir, Row{Reads: true, Cards: []HeldCard{
		{Card: "escape.w1", Job: "../escape", Brief: "STATUS: nova-sprint card x\n"},
		{Card: "linked.w1", Job: "linked.w1~15", Brief: "STATUS: nova-sprint card linked.w1\n"},
		{Card: "empty.w1", Job: "empty.w1~15"},
	}}, map[string]bool{running.Job: true}, time.Now(), t0, func(l string) { lines = append(lines, l) })
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
	c, err := SyncInbox(dir, Row{Reads: true}, nil, asked, t0, func(string) {})
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dir, "inbox", fresh.Job, "BRIEF.md"), "written since the ask: the next pass decides it")
	assert.FileExists(t, filepath.Join(dir, RetiredDir, stale.Job, "BRIEF.md"))
	assert.Equal(t, InboxCounts{Inbox: 1}, c)
	c, err = SyncInbox(dir, Row{Reads: true}, nil, asked.Add(2*time.Second), t0, func(string) {})
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(dir, RetiredDir, fresh.Job, "BRIEF.md"), "and the next pass, asked after it, retires it")
	assert.Equal(t, InboxCounts{}, c)
}

// viewOf is the sprint server's worker view of a friend as nova-sprint view worker --json
// writes it (cmd/nova-sprint view.go workerView, schema 1): her work cards, each with the
// inbox path friend sync delivers its brief to, and no brief.
func viewOf(friend string, cards ...HeldCard) string {
	var b strings.Builder
	fmt.Fprintf(&b, `{"view":"worker","schema":1,"sum":"x","at":"2026-10-05T19:00:00Z","epoch":15,"as":%q,"kind":"friend","cursor":"c","cards":[`, friend)
	for i, c := range cards {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, `{"id":%q,"p":"p","st":%q,"brief":"~/%s-working/inbox/%s/BRIEF.md","att":1,"gen":1}`, c.Card, c.Col, friend, c.Job)
	}
	b.WriteString(`],"wait":[]}`)
	return b.String()
}

// While the server refuses friend cards, the worker view says what is on her row: friend
// cards is asked again once a ServedEvery, the view read once a ViewEvery and never between,
// and a server that did not answer is not a refusal (nothing falls back on it).
func TestHeldFallsBackToTheWorkerView(t *testing.T) {
	t.Parallel()
	at := t0
	asks, views := 0, 0
	var askErr error = &Refused{Why: "friend cards refused: the server runs the workers' verbs only"}
	answer := ""
	held := HeldVia("friend-a", func(_ context.Context, argv []string) (string, error) {
		asks++
		assert.Equal(t, []string{"friend", "cards", "friend-a", "--json"}, argv)
		return answer, askErr
	}, func(context.Context) (string, error) {
		views++
		return viewOf("friend-a", workCard("taken.w2", "working"), workCard("dealt.w3", "ready")), nil
	}, func() time.Time { return at })

	row, err := held(context.Background())
	require.NoError(t, err)
	assert.Equal(t, FromView, row.From)
	assert.Contains(t, row.Note, "does not serve friend cards (card "+ServedBy+" adds it)", "the refusal is said, naming the card that adds the verb")
	assert.False(t, row.Reads, "the view lists no reads, so none is retired on it")
	require.Len(t, row.Cards, 2)
	assert.Equal(t, HeldCard{Card: "taken.w2", Job: "taken.w2~15", Col: "working", Why: ViewWhy}, row.Cards[0])
	assert.Equal(t, "dealt.w3~15", row.Cards[1].Job)

	at = at.Add(time.Second)
	_, err = held(context.Background())
	assert.ErrorIs(t, err, ErrNotDue, "the view runs on the server's line: never every loop")
	at = at.Add(ViewEvery)
	row, err = held(context.Background())
	require.NoError(t, err)
	assert.Empty(t, row.Note, "said once a ServedEvery, as friend cards is asked, never once a view")
	assert.Equal(t, [2]int{1, 2}, [2]int{asks, views}, "friend cards is not asked again inside a ServedEvery")

	// the server comes to serve it: the next ask after ServedEvery is its answer, with briefs
	askErr, answer = nil, `{"friend":"friend-a","cards":[{"card":"taken.w2","job":"taken.w2~15","col":"working","brief":"STATUS: nova-sprint card taken.w2\n"}]}`
	at = t0.Add(ServedEvery)
	row, err = held(context.Background())
	require.NoError(t, err)
	assert.Equal(t, FromCards, row.From)
	assert.True(t, row.Reads)
	assert.Equal(t, "STATUS: nova-sprint card taken.w2\n", row.Cards[0].Brief)

	// a server that did not answer is the ask's error: no view, nothing reconciled
	askErr = errors.New("the sprint server at x did not answer")
	_, err = held(context.Background())
	assert.ErrorContains(t, err, "did not answer")
	assert.Equal(t, 2, views)
}

// ParseView reads only her own friend view, and only briefs at inbox/<job>/BRIEF.md.
func TestParseViewRefusesAnotherView(t *testing.T) {
	t.Parallel()
	_, err := ParseView("friend-b", viewOf("friend-a"))
	assert.ErrorContains(t, err, `not friend "friend-b"'s`)
	_, err = ParseView("friend-a", strings.Replace(viewOf("friend-a"), `"kind":"friend"`, `"kind":"member"`, 1))
	assert.ErrorContains(t, err, "(member)")
	_, err = ParseView("friend-a", strings.Replace(viewOf("friend-a", workCard("x.w1", "working")), "/BRIEF.md", "/NOTES.md", 1))
	assert.ErrorContains(t, err, "no inbox/<job>/BRIEF.md")
	cards, err := ParseView("friend-a", viewOf("friend-a"))
	require.NoError(t, err)
	assert.Empty(t, cards)
	// a job that climbs out of her inbox is no inbox/<job>/BRIEF.md
	_, err = ParseView("friend-a", viewOf("friend-a", HeldCard{Card: "x.w1", Job: "..", Col: "working"}))
	assert.ErrorContains(t, err, "no inbox/<job>/BRIEF.md")
}

// On the worker view's answer the daemon counts her inbox against her row, retires the job
// of a work card that left it, keeps her reads (the view names none), and says a held card
// with no BRIEF.md once while it stays missing, not once a loop.
func TestAViewRowRetiresWorkKeepsReadsAndSaysMissingOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	dir := r.d.Dir
	kept, gone, missing := workCard("kept.w1", "working"), workCard("gone.w1", "working"), workCard("missing.w2", "working")
	inboxJob(t, dir, kept.Job, kept.Brief)
	inboxJob(t, dir, gone.Job, gone.Brief)
	inboxJob(t, dir, "frontier.r1.friend-b", "WHO: friend friend-b\n\nread this\n")
	r.d.Held = func(context.Context) (Row, error) {
		cards, err := ParseView("friend-b", viewOf("friend-b", kept, missing))
		return Row{Cards: cards, From: FromView}, err
	}
	r.run(t, 3)
	assert.NoDirExists(t, filepath.Join(dir, "inbox", gone.Job))
	assert.FileExists(t, filepath.Join(dir, RetiredDir, gone.Job, "BRIEF.md"))
	assert.FileExists(t, filepath.Join(dir, "inbox", "frontier.r1.friend-b", "BRIEF.md"), "a read is never retired on an answer that lists no reads")
	assert.NoDirExists(t, filepath.Join(dir, "inbox", missing.Job), "no brief is made up")
	n := 0
	for _, l := range r.records {
		if strings.Contains(l, "inbox: missing inbox/missing.w2~15/BRIEF.md (card missing.w2, working on her row): "+ViewWhy) {
			n++
		}
	}
	assert.Equal(t, 1, n, "said once while it stands: %v", r.records)
	s := r.last()
	assert.Equal(t, [3]int{2, 1, 1}, [3]int{s.Held, s.InboxJobs, s.Missing}, "held=2 (kept, missing), inbox=1 (kept: a read is not counted against a row that lists none), missing=1")
	assert.Equal(t, FromView, s.HeldFrom)
}

// A daemon whose server refuses friend cards, with no worker view to fall back on, says so
// once a minute, naming the card that adds the verb, and writes and retires nothing.
func TestANotServedServerIsSaidOnceAMinute(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	dir := r.d.Dir
	c := workCard("kept.w1", "working")
	inboxJob(t, dir, c.Job, c.Brief)
	asks := 0
	r.d.Held = HeldVia("friend-b", func(context.Context, []string) (string, error) {
		asks++
		return "", &Refused{Why: "friend cards refused: the server runs the workers' verbs only"}
	}, nil, func() time.Time { r.mu.Lock(); defer r.mu.Unlock(); return r.now })
	r.run(t, 200)
	n := 0
	for _, l := range r.records {
		if strings.Contains(l, "inbox: the sprint server does not serve friend cards (card "+ServedBy+" adds it)") {
			n++
		}
	}
	r.mu.Lock()
	elapsed := r.now.Sub(t0)
	r.mu.Unlock()
	require.Greater(t, elapsed, 2*ServedEvery, "the run spans minutes")
	assert.Equal(t, asks, n, "said each time it is asked: %v", r.records)
	assert.GreaterOrEqual(t, n, 2)
	assert.LessOrEqual(t, n, 1+int(elapsed/ServedEvery), "once a minute, never once a loop")
	assert.FileExists(t, filepath.Join(dir, "inbox", c.Job, "BRIEF.md"))
	assert.NoDirExists(t, filepath.Join(dir, RetiredDir))
	assert.Contains(t, r.last().InboxError, ServedBy)
}
