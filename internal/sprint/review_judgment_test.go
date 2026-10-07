package sprint

import (
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// finished drives a ready primary of s1 to review, its work ok or failed.
func finished(w *world, id string, failed bool) {
	w.t.Helper()
	w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{id}}}))
	c := w.s.Fleet.Card(w.s.Work.Card(id).F("work"))
	w.must(Take(w.s, TakeReq{As: c.Row, Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID)}))
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{c.ID}}, Gens: gensOf(w.s, c.ID), Failed: failed, Report: "r"}))
}

// readOK has every outstanding read card of the primary say ok, and, while the
// primary wants another read (ReadsWanted: its reads are asked together, so
// only after a read taken back), asks it and has that one say ok too.
func readOK(w *world, id string) {
	w.t.Helper()
	for range 4 {
		for _, rc := range readCardsAt(w.s, w.s.Work.Card(id), readAttempt(w.s.Work.Card(id))) {
			w.must(Read(w.s, ReadReq{Usage: "input=1000 output=100", As: rc.Row, Verdict: "ok", Sel: Sel{IDs: []string{rc.ID}}}))
		}
		pr := w.s.Work.Card(id)
		if pr == nil || pr.Col != Review || readCardsWanted(w.s, pr, nil) == 0 {
			return
		}
		if p := w.askReads(); len(p.Units) == 0 {
			return // no reader free: the test says what it wants
		}
	}
}

// askedRead is the primary's one read card asked and not read at its attempt.
func askedRead(w *world, id string) *Card {
	w.t.Helper()
	out := readCardsAt(w.s, w.s.Work.Card(id), readAttempt(w.s.Work.Card(id)))
	require.Len(w.t, out, 1, "%s: one read outstanding", id)
	return out[0]
}

// openTypes is the types of the judgments open on the subject, sorted.
func openTypes(w *world, id string) string {
	var out []string
	for _, o := range w.openOn(id) {
		out = append(out, o.Note.Type)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

func openIDs(w *world, id string) []string {
	var ids []string
	for _, o := range w.openOn(id) {
		ids = append(ids, o.Note.ID)
	}
	return ids
}

// ackRefused is an ack of every judgment open on the primary that must be
// refused: ack answers none of the judgments that offer accept, and the
// refusal names accept's command.
func ackRefused(w *world, id string) {
	w.t.Helper()
	p := w.do(Ack(w.s, AckReq{Notes: openIDs(w, id), Reason: "seen"}))
	require.Len(w.t, p.Refused, 1, "an ack that silences %s: %+v", id, p)
	require.Empty(w.t, p.Units, "an ack that silences %s: %+v", id, p)
	require.Contains(w.t, p.Refused[0].Why, "ack does not answer", "an ack that silences %s: %+v", id, p)
	require.Contains(w.t, p.Refused[0].Why, "nova-sprint accept --group", "an ack that silences %s: %+v", id, p)
}

// Finish: failed work is its own judgment and nothing more; ok work that
// arrives in review is asked by the machine, and is no judgment.
func TestReviewJudgmentFinish(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	finished(w, "s1-1", false)
	got := openTypes(w, "s1-1")
	require.Empty(t, got, "ok work, never asked yet: %q", got)
	finished(w, "s1-2", true)
	got = openTypes(w, "s1-2")
	require.Equal(t, NWorkFailed, got, "failed work: %q", got)
	w.clean("finished")
}

// tickTakes says the tick's accept part takes the primary (TickAccept), on the
// world as it stands: the plan is not applied.
func tickTakes(w *world, id string) bool {
	w.t.Helper()
	p, _ := TickAccept(w.s, TickReq{})
	return slices.ContainsFunc(p.Units, func(u Unit) bool { return u.Key == id })
}

// Read: the second different ok is no judgment, the tick accepts it (since
// 2026-10-06, no hand step: it was ready to accept on a STOPPED machine); a
// third ok writes none either; reads done without two oks are reads exhausted.
func TestReviewJudgmentRead(t *testing.T) {
	t.Parallel()
	w := setup(t, 2)
	finished(w, "s1-1", false)
	w.askReads()
	readOK(w, "s1-1")
	got := openTypes(w, "s1-1")
	require.Empty(t, got, "two oks: %q", got)
	require.True(t, tickTakes(w, "s1-1"), "two oks: the tick accepts it")
	w.askReads()
	readOK(w, "s1-1")
	n := len(w.notesOf(NReadyToAccept))
	require.Zero(t, n, "an ok wrote ready to accept: %d", n)
	finished(w, "s1-2", false)
	w.askReads()
	rcs := readCardsAt(w.s, w.s.Work.Card("s1-2"), 1)
	require.Len(t, rcs, 2)
	w.must(Read(w.s, ReadReq{Usage: "input=1000 output=100", As: rcs[0].Row, Verdict: "ok", Sel: Sel{IDs: []string{rcs[0].ID}}}))
	w.must(Read(w.s, ReadReq{Usage: "input=1000 output=100", As: rcs[1].Row, Verdict: "broken", Finding: "f:1", Sel: Sel{IDs: []string{rcs[1].ID}}}))
	p := w.do(Ack(w.s, AckReq{Notes: openIDs(w, "s1-2"), Reason: "seen"}))
	require.Len(t, p.Refused, 1, "ack of a broken read: %+v", p)
	got = openTypes(w, "s1-2")
	require.Equal(t, NReadBroken, got, "one ok, one broken, the ack refused: %q", got)
	w.clean("read")
}

// Return: a primary sent back to review with its reads standing has the one
// judgment returned to review, which offers accept; a further ok read writes
// nothing more, and ack of it is refused.
func TestReviewJudgmentReturnAndAck(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	accepted(w, "s1-1")
	w.must(Return(w.s, ReturnReq{Sel: Sel{IDs: []string{"s1-1"}}, Reason: "hold it"}))
	open := w.openOn("s1-1")
	require.Len(t, open, 1, "returned: %+v", open)
	require.Equal(t, NReturned, open[0].Note.Type, "returned: %+v", open)
	require.Contains(t, open[0].Note.Decisions, "accept", "returned: %+v", open)
	w.askReads()
	readOK(w, "s1-1")
	got := openTypes(w, "s1-1")
	require.Equal(t, NReturned, got, "a third ok after return: %q", got)
	// ack answers neither returned to review nor ready to accept.
	ackRefused(w, "s1-1")
	got = openTypes(w, "s1-1")
	require.Equal(t, NReturned, got, "returned after the refused ack: %q", got)
	w.clean("returned")
}

// Ask: one more reader for an acceptable primary writes no judgment; the tick
// accepts it all the same.
func TestReviewJudgmentAsk(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	finished(w, "s1-1", false)
	w.askReads()
	readOK(w, "s1-1")
	require.Empty(t, openTypes(w, "s1-1"), "two oks: the tick's")
	w.askReads()
	got := openTypes(w, "s1-1")
	require.Empty(t, got, "asked of another: %q", got)
	require.True(t, tickTakes(w, "s1-1"), "asked of another: the tick accepts it")
	w.clean("asked")
}

// A refused rework leaves the primary in review as it found it: an acceptable
// primary is the tick's to accept, no judgment.
func TestReviewJudgmentRefusedRework(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	finished(w, "s1-1", false)
	w.askReads()
	readOK(w, "s1-1")
	p := w.do(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	require.Len(t, p.Refused, 1, "rework with no fix: %+v", p)
	require.Equal(t, Review, w.state("s1-1"), "rework with no fix: %+v", p)
	got := openTypes(w, "s1-1")
	require.Empty(t, got, "refused rework: %q", got)
	require.True(t, tickTakes(w, "s1-1"), "refused rework: the tick accepts it")
	w.clean("refused")
}

// CI: green on the current head closes red; a primary never asked is then
// stranded in review.
func TestReviewJudgmentCI(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	finished(w, "s1-1", false)
	w.must(RecordCI(w.s, CIReq{Sel: Sel{IDs: []string{"s1-1"}}, Red: true, Run: "r1"}))
	w.must(RecordCI(w.s, CIReq{Sel: Sel{IDs: []string{"s1-1"}}, Run: "r2"}))
	open := w.openOn("s1-1")
	require.Len(t, open, 1, "green after red: %+v", open)
	require.Equal(t, NStranded, open[0].Note.Type, "green after red: %+v", open)
	require.Contains(t, open[0].Note.What, "never asked", "green after red: %+v", open)
	w.clean("ci")
}

// TestReworkOfNoNamedCardTakesReviewAndTheBoundedReadyCards pins the pool a
// rework with no card named draws from (Rework, steps_review.go): the primaries
// in review, and the ready ones whose work card is withdrawn at its redeal
// bound, and no other ready primary.
func TestReworkOfNoNamedCardTakesReviewAndTheBoundedReadyCards(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		redeals int
		want    []string
	}{
		{"one primary in review, one at its redeal bound", MaxRedeals, []string{"s1-2", "s1-3"}},
		{"one primary in review, one a redeal short of its bound", MaxRedeals - 1, []string{"s1-3"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := stoppedForConflict(t)
			inOrder(returnCards("s1-2", "s1-3"), reworkCards("s1-2"), atRedealBound("s1-2", c.redeals))(w)
			p := Rework(w.s, ReworkReq{Fix: "a fix"})
			var got []string
			for _, u := range p.Units {
				got = append(got, u.Key)
			}
			slices.Sort(got)
			assert.Equal(t, c.want, got, "%s: reworked %v, refused %v, want %v", c.name, got, p.Refused, c.want)
			assert.Empty(t, p.Refused, "%s: reworked %v, refused %v, want %v", c.name, got, p.Refused, c.want)
		})
	}
}

func stoppedForConflict(t *testing.T) *world {
	w := setup(t, 3)
	accepted(w, "s1-1", "s1-2", "s1-3")
	w.must(MergeStep(w.s, MergeReq{Stream: "s1", Batch: 2, Conflict: "s1-2"}))
	return w
}

func returnCards(ids ...string) func(w *world) {
	return func(w *world) {
		w.t.Helper()
		w.must(Return(w.s, ReturnReq{Sel: Sel{IDs: ids}, Reason: "suspect"}))
	}
}

func reworkCards(ids ...string) func(w *world) {
	return func(w *world) {
		w.t.Helper()
		w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: ids}, Fix: "a fix"}))
	}
}

// atRedealBound takes every member down, so that the card in working is
// withdrawn and back in ready, and sets the redeals its work card has had,
// with the mark of a take that ended.
func atRedealBound(id string, redeals int) func(w *world) {
	return func(w *world) {
		w.t.Helper()
		w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1"}))
		w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m2"}))
		wc := w.s.Fleet.Card(WorkCardID(id, w.s.Work.Card(id).Int("attempt")))
		wc.Fields["redeals"] = itoa(redeals)
		wc.Fields[FieldTakeEnded] = stamp(w.s.Now)
	}
}

func inOrder(steps ...func(w *world)) func(w *world) {
	return func(w *world) {
		for _, step := range steps {
			step(w)
		}
	}
}
