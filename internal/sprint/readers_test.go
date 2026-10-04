package sprint

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The readers' state (docs/SPEC-SPRINT.md section 6; tla/DirtyTick.tla, the
// readers update): the ask asks readers up only, takes back a read asked of
// one that is not up, and with fewer than two up writes one judgment.

// toReview takes primaries to review, unasked.
func toReview(w *world, ids ...string) {
	w.t.Helper()
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: ids}}))
	for _, id := range ids {
		c := w.s.Fleet.Card(w.s.Work.Card(id).F("work"))
		w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
		w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	}
}

func readerNames(cs []*Card) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.F("reader"))
	}
	return out
}

func TestReaderStateFollowsTheHoldAndTheBeat(t *testing.T) {
	t.Parallel()
	now := t0
	beat := func(ago time.Duration) Beat { return Beat{At: now.Add(-ago)} }
	assert.Equal(t, ReaderUp, ReaderState(false, beat(ReaderBeatBound), now), "a beat at the bound is up")
	assert.Equal(t, ReaderAway, ReaderState(false, beat(ReaderBeatBound+time.Second), now), "a beat past the bound is away")
	assert.Equal(t, ReaderDown, ReaderState(false, Beat{}, now), "no beat ever is down")
	assert.Equal(t, ReaderAway, ReaderState(true, beat(0), now), "the hold is away whatever it beats")
	assert.Equal(t, ReaderAway, ReaderState(true, Beat{}, now))
}

func TestTheAskAsksReadersUpOnly(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	toReview(w, "s1-1", "s1-2")
	w.s.ReaderStates = map[string]string{"reader-a": ReaderAway, "reader-b": ReaderUp, "reader-c": ReaderUp}
	w.must(Ask(w.s, AskReq{}))
	for _, id := range []string{"s1-1", "s1-2"} {
		assert.ElementsMatch(t, []string{"reader-b", "reader-c"}, readerNames(readsAt(w.s, w.s.Work.Card(id), 1)), id)
	}
	// a reader down is no more asked than one away: with one up, none can be
	w = setup(t, 1)
	toReview(w, "s1-1")
	w.s.ReaderStates = map[string]string{"reader-a": ReaderDown, "reader-b": ReaderAway, "reader-c": ReaderUp}
	p := Ask(w.s, AskReq{})
	require.Len(t, p.Refused, 1)
	assert.Contains(t, p.Refused[0].Why, "a reader away or down is not asked")
	assert.Empty(t, p.Units)
}

func TestAReadAskedOfAReaderThatIsNotUpIsTakenBackAndAskedAgain(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.s.Readers.SetRows(append(w.s.Readers.Rows(), "reader-d"))
	toReview(w, "s1-1")
	w.must(Ask(w.s, AskReq{}))
	pr := w.s.Work.Card("s1-1")
	asked := readerNames(readsAt(w.s, pr, 1))
	require.Len(t, asked, 2)
	gone := asked[0]
	w.s.ReaderStates = map[string]string{"reader-a": ReaderUp, "reader-b": ReaderUp, "reader-c": ReaderUp, "reader-d": ReaderUp, gone: ReaderAway}
	// the tick asks it again: no ask --another, the attempt stays, one unit
	plan, due := TickAsk(w.s, TickReq{})
	require.Len(t, plan.Units, 1, "%+v", plan)
	assert.Zero(t, due)
	w.must(plan)
	pr = w.s.Work.Card("s1-1")
	now := readerNames(readsAt(w.s, pr, 1))
	assert.Len(t, now, 2)
	assert.NotContains(t, now, gone, "the read returned from the reader that is not up")
	assert.Equal(t, 1, pr.Int("attempt"), "no turn is spent: the attempt stays")
	old := w.s.Readers.Card(ReadCardID("s1-1", 1, gone))
	require.NotNil(t, old)
	assert.False(t, old.Placed())
	assert.Equal(t, "away", old.F("retired_by"))
	// a read begun stays with its reader
	w.s.ReaderStates[now[0]] = ReaderAway
	plan, _ = TickAsk(w.s, TickReq{})
	assert.NotEmpty(t, plan.Units, "asked of a reader away: taken back")
	w.must(Read(w.s, ReadReq{As: now[0], Begin: true, Sel: Sel{IDs: []string{ReadCardID("s1-1", 1, now[0])}}}))
	plan, _ = TickAsk(w.s, TickReq{})
	assert.Empty(t, plan.Units, "a read begun is not taken back")
}

func TestFewerThanTwoReadersUpIsOneJudgmentAndNoAsk(t *testing.T) {
	t.Parallel()
	w := setup(t, 3)
	toReview(w, "s1-1", "s1-2", "s1-3")
	w.s.ReaderStates = map[string]string{"reader-a": ReaderUp, "reader-b": ReaderAway, "reader-c": ReaderDown}
	plan, _ := TickAsk(w.s, TickReq{})
	assert.Empty(t, plan.Units, "an absent reader is never asked")
	require.Len(t, plan.Notes, 1, "one judgment for three primaries: %+v", plan.Notes)
	n := plan.Notes[0]
	assert.Equal(t, Judgment, n.Kind)
	assert.Equal(t, NFewReaders, n.Type)
	assert.True(t, n.StreamLevel)
	assert.True(t, strings.HasPrefix(n.What, "fewer than two readers up: "), n.What)
	assert.Contains(t, n.What, "reader-b away")
	assert.Contains(t, n.What, "reader-c down")
	assert.Contains(t, n.Decisions, "reader up")
	// written once: the next tick, with it open, writes none
	w.must(plan)
	plan, _ = TickAsk(w.s, TickReq{})
	assert.Empty(t, plan.Notes)
	// the no-stall rule holds the primaries by it
	assert.Empty(t, Unheld(HeldState{Snap: w.s, Running: true}, w.s.Now), "the judgment holds every primary in review")
	// two up: it closes and they are asked
	w.s.ReaderStates["reader-b"] = ReaderUp
	plan, _ = TickAsk(w.s, TickReq{})
	assert.Len(t, plan.Units, 3)
	assert.NotEmpty(t, plan.Closes, "the judgment closes")
}

// A snapshot with no reader states holds every reader up: the ask is as it was.
func TestNoReaderStatesHoldsEveryReaderUp(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	toReview(w, "s1-1")
	require.Nil(t, w.s.ReaderStates)
	plan, _ := TickAsk(w.s, TickReq{})
	assert.Len(t, plan.Units, 1)
	assert.Empty(t, plan.Notes)
}
