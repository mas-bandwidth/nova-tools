package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bound is two identical findings (docs/SPEC-SPRINT.md, "The brief is wrong, not the
// worker"): a second attempt that comes back with the finding the first came back with,
// the same reader class at the same file and line however it is worded, stops the card
// and raises the brief defect at once, carrying both findings; the attempt cap stays for
// findings that differ; the bound is a setting, 2 by default.

// attemptFoundBrokenBy runs the primary's live attempt to its finish and the first read
// asked of it to broken with the finding: the reader who found it.
func attemptFoundBrokenBy(w *world, id, finding string) string {
	w.t.Helper()
	pr := w.s.Work.Card(id)
	wc := w.s.Fleet.Card(WorkCardID(id, pr.Int("attempt")))
	require.NotNil(w.t, wc, "attempt %s of %s has a work card", pr.F("attempt"), id)
	if wc.Col == Ready {
		w.must(Take(w.s, TakeReq{As: wc.Row, Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
	}
	w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID)}))
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{id}}}))
	for _, rc := range w.s.Readers.Of(id) {
		if rc.Col == Asked && rc.Int("attempt") == pr.Int("attempt") {
			w.must(Read(w.s, ReadReq{Usage: "input=1000 output=100", As: rc.Row, Verdict: "broken", Finding: finding, Sel: Sel{IDs: []string{rc.ID}}}))
			return rc.Row
		}
	}
	w.t.Fatalf("no read of %s asked at attempt %s", id, pr.F("attempt"))
	return ""
}

func TestTwoIdenticalFindingsStopACard(t *testing.T) {
	t.Parallel()
	rework := func(w *world) Plan { return Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{"s1-1"}}}) }
	briefWrong := func(w *world) []Note { return w.notesOf(NBriefWrong) }

	t.Run("the second identical finding stops the card", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
		first := attemptFoundBrokenBy(w, "s1-1", "internal/x.go:12: the guard is missing. Add it.")
		require.Empty(t, briefWrong(w), "one finding is no repeat")
		w.must(rework(w))
		require.Equal(t, 2, w.s.Work.Card("s1-1").Int("attempt"))
		assert.Equal(t, "attempt 1: "+first+" at internal/x.go:12", w.s.Work.Card("s1-1").F(FieldFindingKeys), "the rework keeps the attempt's key")

		// the same reader at the same file and line, worded otherwise: the card stops here
		second := attemptFoundBrokenBy(w, "s1-1", "Still broken at internal/x.go:12; the guard was never added")
		require.Equal(t, first, second, "the finder checks the fix")
		notes := briefWrong(w)
		require.Len(t, notes, 1, "the brief defect is raised at the second attempt, not the fourth")
		n := notes[0]
		assert.Equal(t, Decisions[NBriefWrong], n.Decisions, "brief and drop, never rework")
		assert.Contains(t, n.What, "s1-1 has failed the same way twice (attempts 1 to 2, the same finding: "+first+" at internal/x.go:12)")
		assert.Contains(t, n.What, "findings: attempt 1: internal/x.go:12: the guard is missing.; attempt 2: Still broken at internal/x.go:12; the guard was never added", "both findings carried")
		assert.Empty(t, w.notesOf(NReadBroken)[1:], "the repeat raises the brief defect in place of a broken read")

		p := rework(w)
		require.Len(t, p.Refused, 1, "the card is stopped: no third attempt")
		assert.Contains(t, p.Refused[0].Why, "attempt 1: internal/x.go:12: the guard is missing.; attempt 2: Still broken at internal/x.go:12")
		assert.Contains(t, p.Refused[0].Why, "run: nova-sprint brief s1-1 --brief-file <path>")
		assert.Empty(t, p.Units, "nothing written")
		assert.Equal(t, Review, w.s.Work.Card("s1-1").Col)

		// a replaced brief counts from its attempt: the same finding again is a first
		w.s.Work.Card("s1-1").Fields[FieldBriefAttempt] = "2"
		w.must(rework(w))
		require.Equal(t, 3, w.s.Work.Card("s1-1").Int("attempt"))
		attemptFoundBrokenBy(w, "s1-1", "internal/x.go:12: the guard is missing")
		require.Len(t, briefWrong(w), 1, "the attempt on a new brief is not a repeat of one on the old")
	})

	t.Run("findings that differ run to the attempt cap", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
		for a := 1; a < AttemptsDefault; a++ {
			attemptFoundBrokenBy(w, "s1-1", "internal/x.go:"+itoa(10+a)+": wrong")
			require.Empty(t, briefWrong(w), "attempt %d: another line is another finding", a)
			w.must(rework(w))
		}
		attemptFoundBrokenBy(w, "s1-1", "internal/x.go:99: wrong")
		notes := briefWrong(w)
		require.Len(t, notes, 1)
		assert.Contains(t, notes[0].What, "s1-1: brief defect after 4 attempts", "four stays only for findings that differ")
	})

	t.Run("another reader class at the same line is another finding", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "reader-a at internal/x.go:12", FindingKey("Reader-A", "see ./internal/x.go:12, then x.go:14"))
		assert.NotEqual(t, FindingKey("reader-a", "internal/x.go:12: a"), FindingKey("reader-b", "internal/x.go:12: a"))
		assert.NotEqual(t, FindingKey("reader-a", "internal/x.go:12: a"), FindingKey("reader-a", "internal/x.go:13: a"))
		assert.Equal(t, "work at a.go:3", FindingKey("", "tests red at a.go:3"), "failed work's report is the class work")
		assert.Equal(t, "files outside paths", FindingKey("reader-a", "Files outside its PATHS: internal/y.go"), "no file and line: the whole-text class")
		assert.Empty(t, FindingKey("reader-a", ""))
		c := &Card{ID: "x", Fields: map[string]string{"attempt": "2", "finding": "internal/x.go:12: a", FieldFindingAttempt: "1", FieldFindingReader: "reader-a"}}
		_, at := AtIdenticalFindings(c, "reader-b", "internal/x.go:12: a", 2)
		assert.False(t, at, "another reader")
		_, at = AtIdenticalFindings(c, "reader-a", "internal/x.go:12: b", 2)
		assert.True(t, at, "the same reader, file and line, a card admitted before the keys were kept")
	})

	t.Run("the bound is a setting", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		assert.Equal(t, IdenticalDefault, w.s.IdenticalBound("s1"), "2 by default")
		w.s.Work.SetProp(PropIdentical, "3")
		require.Equal(t, 3, w.s.IdenticalBound("s1"))
		w.must(Deal(w.s, DealReq{Sel: Sel{IDs: []string{"s1-1"}}}))
		for a := 1; a <= 2; a++ {
			attemptFoundBrokenBy(w, "s1-1", "internal/x.go:12: the guard is missing")
			require.Empty(t, briefWrong(w), "attempt %d: under the setting of 3", a)
			w.must(rework(w))
		}
		attemptFoundBrokenBy(w, "s1-1", "internal/x.go:12: the guard is missing")
		notes := briefWrong(w)
		require.Len(t, notes, 1, "the third identical finding stops it")
		assert.True(t, strings.Contains(notes[0].What, "has failed the same way 3 times (attempts 1 to 3"), notes[0].What)
		w.s.Work.SetProp(PropIdentical, "1")
		assert.Equal(t, IdenticalDefault, w.s.IdenticalBound("s1"), "one finding is never a repeat: under 2 is no setting")
	})
}
