package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The readers' state (docs/SPEC-SPRINT.md section 6; tla/DirtyTick.tla, the
// readers update): a reader is up, away or down; the ask never asks one that
// is not up; a read asked of one that goes away is asked again; fewer than two
// up is one judgment per tick-end; reader remove takes a row that holds no
// read off the table.

// readerState is the state where shows for a reader.
func (ta *testApp) readerState(reader string) string {
	ta.t.Helper()
	var v struct {
		Tables map[string]map[string]map[string]string
	}
	ta.json("where", &v)
	return v.Tables[sprint.Readers][reader]["status"]
}

// readers is the readers' row keys, as where shows them.
func (ta *testApp) readerRows() []string {
	ta.t.Helper()
	var v struct {
		Tables map[string]map[string]map[string]string
	}
	ta.json("where", &v)
	var out []string
	for r := range v.Tables[sprint.Readers] {
		out = append(out, r)
	}
	return out
}

// inReview is n primaries of stream s1 finished and in review, the machine
// STOPPED (the ask is the coordinator's by hand).
func (ta *testApp) inReview(n int) {
	ta.t.Helper()
	ta.ok(fmt.Sprintf("add --stream s1 --count %d", n))
	ta.deal(n)
	ta.ok(fmt.Sprintf("take --as m1 --limit %d", n))
	var words []string
	for i := 1; i <= n; i++ {
		words = append(words, fmt.Sprintf("s1-%d.w1@1", i))
	}
	ta.ok("finish --as m1 " + strings.Join(words, " "))
}

// askedOf is the read cards a reader holds asked, by id.
func (ta *testApp) askedOf(reader string) []string {
	ta.t.Helper()
	var q struct{ Cards []queueCard }
	ta.json("queue --as "+reader, &q)
	var ids []string
	for _, c := range q.Cards {
		ids = append(ids, c.ID)
	}
	return ids
}

// A reader's own queue is its beat: with no beat it is down, a beat makes it
// up, and past the beat bound it is away.
func TestAReaderIsUpWhileItBeatsAndAwayWhenItLapses(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.quiet = map[string]bool{"reader-c": true}
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	assert.Equal(t, "up", ta.readerState("reader-a"))
	assert.Equal(t, "down", ta.readerState("reader-c"), "a row no reader process has beaten for is down")
	ta.ok("queue --as reader-c")
	assert.Equal(t, "up", ta.readerState("reader-c"), "queue --as is the reader's beat")
	ta.a.sleep(sprint.ReaderBeatBound + time.Second)
	assert.Equal(t, "away", ta.readerState("reader-c"), "no beat past the bound is away")
	assert.Equal(t, "up", ta.readerState("reader-a"), "a reader that keeps asking for its queue stays up")
	ta.ok("queue --as reader-c")
	assert.Equal(t, "up", ta.readerState("reader-c"))
}

// reader away holds a reader away whatever it beats; reader up releases it.
func TestReaderAwayAndUp(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	out := ta.ok("reader away reader-c")
	assert.Contains(t, out, "READER-AWAY OK readers=reader-c")
	ta.ok("queue --as reader-c") // it still beats
	assert.Equal(t, "away", ta.readerState("reader-c"))
	assert.Equal(t, "up", ta.readerState("reader-a"))
	out = ta.ok("reader up reader-c")
	assert.Contains(t, out, "READER-UP OK readers=reader-c")
	assert.Equal(t, "up", ta.readerState("reader-c"))
}

// A reader with no row is refused, exit 1, and nothing is written; no name is
// refused at the arguments.
func TestReaderAwayRefusesAnUnknownReader(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	code, out, errs := ta.do("reader away reader-a reader-z")
	assert.Equal(t, 1, code, "%s%s", out, errs)
	assert.Contains(t, errs, "no reader reader-z")
	assert.Equal(t, "up", ta.readerState("reader-a"), "reader-a was named with an unknown reader: nothing was written")
	code, _, errs = ta.do("reader up")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "wants at least one reader")
	code, _, errs = ta.do("reader up bad.name")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "a reader name wants letters, digits, _ and -: bad.name")
}

// The ask never asks a reader that is away: the pair is the readers up.
func TestTheAskNeverAsksAnAwayReader(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	ta.ok("reader away reader-a")
	ta.inReview(2)
	ta.ok("ask")
	assert.Empty(t, ta.askedOf("reader-a"), "reader-a is away: no read is asked of it")
	assert.Len(t, ta.askedOf("reader-b"), 2)
	assert.Len(t, ta.askedOf("reader-c"), 2)
}

// With fewer than two readers up the tick asks none, raises one judgment for
// every primary waiting, and asks them when readers are up again.
func TestFewerThanTwoReadersUpIsOneJudgmentPerTick(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	ta.ok("reader away reader-a reader-b")
	ta.inReview(3)
	ta.ok("start")
	ta.ok("tick")
	ta.ok("tick")
	for _, r := range []string{"reader-a", "reader-b", "reader-c"} {
		assert.Empty(t, ta.askedOf(r), "%s was asked with one reader up", r)
	}
	var judgments []sprint.Group
	for _, g := range ta.inboxGroups() {
		if g.Kind == sprint.Judgment && g.Type == sprint.NFewReaders {
			judgments = append(judgments, g)
		}
	}
	require.Len(t, judgments, 1, "one judgment for the sprint, whatever the primaries waiting: %+v", ta.inboxGroups())
	assert.Contains(t, judgments[0].What, "fewer than two readers up: reader-a away, reader-b away, reader-c up")
	ta.ok("reader up reader-a")
	ta.ok("tick")
	assert.Len(t, ta.askedOf("reader-a"), 3)
	assert.Len(t, ta.askedOf("reader-c"), 3)
	for _, g := range ta.inboxGroups() {
		assert.False(t, g.Kind == sprint.Judgment && g.Type == sprint.NFewReaders, "the judgment closes when two are up")
	}
}

// A read asked of a reader that goes away is asked of another reader by the
// next tick: no ask --another, and the primary stays at its attempt.
func TestAReadAskedOfAReaderThatGoesAwayIsAskedAgain(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	ta.ok("reader away reader-c")
	ta.inReview(1)
	ta.ok("ask")
	require.Len(t, ta.askedOf("reader-a"), 1)
	require.Len(t, ta.askedOf("reader-b"), 1)
	ta.ok("reader up reader-c")
	ta.ok("reader away reader-b")
	ta.ok("start")
	ta.ok("tick")
	assert.Empty(t, ta.askedOf("reader-b"), "the read returned from the reader that went away")
	assert.Equal(t, []string{"s1-1.r1.reader-a"}, ta.askedOf("reader-a"))
	assert.Equal(t, []string{"s1-1.r1.reader-c"}, ta.askedOf("reader-c"), "asked of the next reader up at the same attempt")
	out := ta.ok("card s1-1")
	assert.NotContains(t, out, "attempt 2")
	for _, g := range ta.inboxGroups() {
		assert.False(t, g.Kind == sprint.Judgment, "no judgment is owed: %+v", g)
	}
}

// reader remove takes the rows off the readers table, in one write.
func TestReaderRemove(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	ta.ok("reader away reader-c")
	out := ta.ok("reader remove reader-c")
	assert.Contains(t, out, "READER-REMOVE OK readers=reader-c")
	assert.ElementsMatch(t, []string{"reader-a", "reader-b"}, ta.readerRows())
	out = ta.ok("reader remove reader-a reader-b")
	assert.Contains(t, out, "READER-REMOVE OK readers=reader-a,reader-b")
	assert.Empty(t, ta.readerRows())
	ta.ok("reader add reader-c")
	assert.Equal(t, "up", ta.readerState("reader-c"), "a reader added again starts clean: its hold went with its row")
}

// A reader that holds a read, asked or reading, is refused, naming the reader
// and the read; the table is unchanged.
func TestReaderRemoveRefusesAReaderWithReads(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	ta.inReview(1)
	ta.ok("ask")
	for _, r := range []string{"reader-a", "reader-b"} {
		if len(ta.askedOf(r)) == 1 {
			code, out, errs := ta.do("reader remove " + r + " reader-c")
			assert.Equal(t, 1, code, "%s%s", out, errs)
			assert.Contains(t, errs, r+" holds s1-1.r1."+r+" (asked)")
			assert.NotContains(t, out, "READER-REMOVE OK")
		}
	}
	assert.ElementsMatch(t, []string{"reader-a", "reader-b", "reader-c"}, ta.readerRows(), "the table is unchanged: reader-c, named with the others, was not removed")
	ta.ok("read --as reader-a --begin --limit 5")
	code, _, errs := ta.do("reader remove reader-a")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "reader-a holds s1-1.r1.reader-a (reading)")
}

// A reader that read holds its read card ok: its row is not removed either, so
// the read stands.
func TestReaderRemoveRefusesAReaderWhoseReadStands(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b,reader-c --members m1")
	ta.inReview(1)
	ta.ok("ask")
	ta.ok("read --as reader-a --ok --limit 5")
	code, _, errs := ta.do("reader remove reader-a")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "reader-a holds s1-1.r1.reader-a (ok)")
}

func TestReaderRemoveRefusesAnUnknownReader(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	code, out, errs := ta.do("reader remove reader-a reader-z")
	assert.Equal(t, 1, code, "%s%s", out, errs)
	assert.Contains(t, errs, "no reader reader-z")
	assert.ElementsMatch(t, []string{"reader-a", "reader-b"}, ta.readerRows(), "nothing was written")
}

func TestReaderRemoveWantsANameAndAValidName(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1")
	code, _, errs := ta.do("reader remove")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "wants at least one reader")
	code, _, errs = ta.do("reader remove 'bad name'")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "a reader name wants letters, digits, _ and -: bad name")
	assert.Len(t, ta.readerRows(), 2)
}
