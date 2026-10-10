package sprint

// Unit coverage for the dead-base marking and card base re-pointing helpers in
// steps_merge.go: DeadBaseWhy, deadBaseOpen, DeadBaseHeld, deadBaseStep (driven
// through MergeStep), ValidBase, CardBase, repointBase and crossRefusal. Every
// case is pure over a hand-built Snapshot or the package's world harness: no
// store, no clock, no subprocess, no git, no network (docs/SPEC-SPRINT.md
// section 7, a dead base).

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSprintStepsMergeCoverDeadBaseWhy pins the one sentence of a dead base:
// the lander's refusal and the judgment's words.
func TestSprintStepsMergeCoverDeadBaseWhy(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "card a names BASE old-topic, which is not on origin", DeadBaseWhy("a", "old-topic"))
}

// TestSprintStepsMergeCoverValidBase pins the branch names a lander could cut:
// the shape a card's BASE may name and the component rules git adds.
func TestSprintStepsMergeCoverValidBase(t *testing.T) {
	t.Parallel()
	for _, base := range []string{"dev", "release/v1.2", "a_b-c.d"} {
		assert.True(t, ValidBase(base), "ValidBase(%q) = false", base)
	}
	for _, base := range []string{"", "*", "-x", "a b", "a..b", "a//b", "a/", "x.lock", "x.", strings.Repeat("x", 201)} {
		assert.False(t, ValidBase(base), "ValidBase(%q) = true", base)
	}
}

// TestSprintStepsMergeCoverRepointBase pins repointBase: the brief with its
// first BASE line naming the new base, the pin it dropped, the indent and the
// line end it kept, and false when the brief names no base.
func TestSprintStepsMergeCoverRepointBase(t *testing.T) {
	t.Parallel()
	t.Run("drops the pin and names the new base", func(t *testing.T) {
		t.Parallel()
		brief, old, ok := repointBase("BASE: dev@abc123\n", "main")
		assert.True(t, ok)
		assert.Equal(t, "dev", old)
		assert.Equal(t, "BASE: main\n", brief)
	})
	t.Run("keeps the indent and the CRLF line end", func(t *testing.T) {
		t.Parallel()
		brief, old, ok := repointBase("  BASE: dev\r\n", "main")
		assert.True(t, ok)
		assert.Equal(t, "dev", old)
		assert.Equal(t, "  BASE: main\r\n", brief)
	})
	t.Run("changes only the first BASE line", func(t *testing.T) {
		t.Parallel()
		brief, old, ok := repointBase("BASE: dev\nBASE: other\n", "main")
		assert.True(t, ok)
		assert.Equal(t, "dev", old)
		assert.Equal(t, "BASE: main\nBASE: other\n", brief)
	})
	t.Run("names no base", func(t *testing.T) {
		t.Parallel()
		const in = "no base here\n"
		brief, old, ok := repointBase(in, "main")
		assert.False(t, ok)
		assert.Empty(t, old)
		assert.Equal(t, in, brief, "the brief is unchanged")
	})
}

// TestSprintStepsMergeCoverDeadBaseOpen pins deadBaseOpen: the open judgments of
// a dead base on the card, passing over another judgment type and another card.
func TestSprintStepsMergeCoverDeadBaseOpen(t *testing.T) {
	t.Parallel()
	s := &Snapshot{Open: []Open{
		{Key: OpenKey("j1", "s1-1"), Note: Note{ID: "j1", Kind: Judgment, Type: NDeadBase, Stream: "s1", Primaries: []string{"s1-1"}}},
		{Key: OpenKey("j2", "s1-1"), Note: Note{ID: "j2", Kind: Judgment, Type: NWorkFailed, Stream: "s1", Primaries: []string{"s1-1"}}},
		{Key: OpenKey("j3", "s1-2"), Note: Note{ID: "j3", Kind: Judgment, Type: NDeadBase, Stream: "s1", Primaries: []string{"s1-2"}}},
	}}
	got := deadBaseOpen(s, "s1-1")
	require.Len(t, got, 1, "only the dead-base judgment on this card: %+v", got)
	assert.Equal(t, "j1", got[0].Note.ID)
}

// TestSprintStepsMergeCoverDeadBaseHeld pins DeadBaseHeld: the land pass skips
// the card only when it is marked dead on the base it names now and that
// judgment is open.
func TestSprintStepsMergeCoverDeadBaseHeld(t *testing.T) {
	t.Parallel()
	marked := &Card{ID: "s1-1", Row: "s1", Col: Merging, Rev: 1, Fields: map[string]string{FieldDeadBase: "dev"}}
	open := []Open{{Key: OpenKey("j1", "s1-1"), Note: Note{ID: "j1", Kind: Judgment, Type: NDeadBase, Stream: "s1", Primaries: []string{"s1-1"}}}}
	withOpen := &Snapshot{Open: open}
	none := &Snapshot{}
	assert.False(t, DeadBaseHeld(none, nil, "dev"), "a nil card is not held")
	assert.False(t, DeadBaseHeld(withOpen, marked, ""), "an empty base is not held")
	assert.False(t, DeadBaseHeld(withOpen, marked, "main"), "a mark of another base is not held")
	assert.False(t, DeadBaseHeld(none, marked, "dev"), "a mark with no open judgment is not held")
	assert.True(t, DeadBaseHeld(withOpen, marked, "dev"), "the mark and its open judgment hold the card")
}

// TestSprintStepsMergeCoverDeadBaseStep pins deadBaseStep through MergeStep on an
// accepted card: no cards names the stream, a card not merging in the stream is
// refused, the main path marks the primary and raises one judgment, and a second
// step with the mark and the judgment open writes nothing.
func TestSprintStepsMergeCoverDeadBaseStep(t *testing.T) {
	t.Parallel()
	t.Run("no cards names the stream", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		p := MergeStep(w.s, MergeReq{Stream: "s1", DeadBase: "old-topic"})
		require.Len(t, p.Refused, 1)
		assert.Equal(t, "s1", p.Refused[0].Key)
		assert.Contains(t, p.Refused[0].Why, "a dead base names its cards")
		assert.Empty(t, p.Units)
	})
	t.Run("a card not merging in the stream is refused", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 2)
		accepted(w, "s1-1")
		p := MergeStep(w.s, MergeReq{Stream: "s1", DeadBase: "old-topic", Cards: []string{"s1-2"}})
		require.Len(t, p.Refused, 1)
		assert.Equal(t, "s1-2", p.Refused[0].Key)
		assert.Contains(t, p.Refused[0].Why, "not merging in stream s1")
		assert.Empty(t, p.Units)
	})
	t.Run("the main path marks the primary and raises one judgment", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		accepted(w, "s1-1")
		w.must(MergeStep(w.s, MergeReq{Stream: "s1", DeadBase: "old-topic", Cards: []string{"s1-1"}, Who: Coordinator}))
		assert.Equal(t, "old-topic", w.s.Work.Card("s1-1").F(FieldDeadBase))
		js := w.notesOf(NDeadBase)
		require.Len(t, js, 1)
		assert.Equal(t, "s1-1", js[0].Card)
		assert.Contains(t, js[0].What, "card s1-1 names BASE old-topic, which is not on origin")
		assert.Contains(t, js[0].What, "nova-sprint card base s1-1 <a branch on origin>")
	})
	t.Run("a second step with the mark and the judgment open writes nothing", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		accepted(w, "s1-1")
		req := MergeReq{Stream: "s1", DeadBase: "old-topic", Cards: []string{"s1-1"}, Who: Coordinator}
		w.must(MergeStep(w.s, req))
		p := MergeStep(w.s, req)
		assert.Empty(t, p.Units, "one fact, once")
		assert.Empty(t, p.Refused)
		assert.Len(t, w.notesOf(NDeadBase), 1)
	})
}

// TestSprintStepsMergeCoverCardBase pins CardBase: every refusal (a who that is
// not the coordinator, a card not on the table, a ready card, an invalid base, a
// base not on origin, a brief with no BASE line, a base it names already) and the
// main path that re-points the brief, records the set, clears the mark and
// answers the dead-base judgment.
func TestSprintStepsMergeCoverCardBase(t *testing.T) {
	t.Parallel()
	t.Run("a who that is not the coordinator is refused", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		accepted(w, "s1-1")
		p := CardBase(w.s, CardBaseReq{ID: "s1-1", Base: "main", OnOrigin: true, Who: "m1"})
		require.Len(t, p.Refused, 1)
		assert.Contains(t, p.Refused[0].Why, "card base is the coordinator's alone")
		assert.Empty(t, p.Units)
	})
	t.Run("a card not on the table is refused", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		p := CardBase(w.s, CardBaseReq{ID: "ghost", Base: "main", OnOrigin: true, Who: Coordinator})
		require.Len(t, p.Refused, 1)
		assert.Contains(t, p.Refused[0].Why, "ghost is no card on the table")
		assert.Empty(t, p.Units)
	})
	t.Run("a ready card names its column and the brief verb", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 2)
		p := CardBase(w.s, CardBaseReq{ID: "s1-2", Base: "main", OnOrigin: true, Who: Coordinator})
		require.Len(t, p.Refused, 1)
		assert.Contains(t, p.Refused[0].Why, "s1-2 is ready: card base re-points a merging card")
		assert.Contains(t, p.Refused[0].Why, "nova-sprint brief")
	})
	t.Run("an invalid base is refused", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		accepted(w, "s1-1")
		p := CardBase(w.s, CardBaseReq{ID: "s1-1", Base: "*", OnOrigin: true, Who: Coordinator})
		require.Len(t, p.Refused, 1)
		assert.Contains(t, p.Refused[0].Why, "'*' is not a branch name")
	})
	t.Run("a base not on origin is refused", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		accepted(w, "s1-1")
		p := CardBase(w.s, CardBaseReq{ID: "s1-1", Base: "main", OnOrigin: false, Who: Coordinator})
		require.Len(t, p.Refused, 1)
		assert.Contains(t, p.Refused[0].Why, "main is not a branch on origin")
	})
	t.Run("a brief with no BASE line is refused", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		accepted(w, "s1-1")
		p := CardBase(w.s, CardBaseReq{ID: "s1-1", Base: "main", OnOrigin: true, Who: Coordinator})
		require.Len(t, p.Refused, 1)
		assert.Contains(t, p.Refused[0].Why, "s1-1's brief names no BASE: line")
	})
	t.Run("a base it names already is refused", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		accepted(w, "s1-1")
		w.s.Work.Card("s1-1").Fields["brief"] = "REPO: x\nBASE: dev\n\nDo.\n"
		p := CardBase(w.s, CardBaseReq{ID: "s1-1", Base: "dev", OnOrigin: true, Who: Coordinator})
		require.Len(t, p.Refused, 1)
		assert.Contains(t, p.Refused[0].Why, "s1-1 names BASE dev already")
	})
	t.Run("the main path re-points the brief, records the set and answers the judgment", func(t *testing.T) {
		t.Parallel()
		w := setup(t, 1)
		accepted(w, "s1-1")
		w.s.Work.Card("s1-1").Fields["brief"] = "REPO: x\nBASE: dev\n\nDo.\n"
		w.must(MergeStep(w.s, MergeReq{Stream: "s1", DeadBase: "dev", Cards: []string{"s1-1"}, Who: Coordinator}))
		js := w.notesOf(NDeadBase)
		require.Len(t, js, 1)
		w.must(CardBase(w.s, CardBaseReq{ID: "s1-1", Base: "main", OnOrigin: true, Who: Coordinator}))
		c := w.s.Work.Card("s1-1")
		assert.Contains(t, c.F("brief"), "BASE: main")
		assert.NotContains(t, c.F("brief"), "dev")
		assert.Equal(t, "dev -> main "+t0.UTC().Format(time.RFC3339)+" by coordinator", c.F(FieldBaseSet))
		assert.False(t, c.Has(FieldDeadBase), "the dead-base mark is cleared")
		assert.Empty(t, w.openOn("s1-1"), "the judgment is closed")
		var decided *Note
		for i := range w.notes {
			if w.notes[i].Kind == Decided && w.notes[i].Answers == js[0].ID {
				decided = &w.notes[i]
			}
		}
		require.NotNil(t, decided, "the judgment is answered with a decided note")
		assert.Equal(t, NDeadBase, decided.Type)
		assert.Contains(t, decided.What, "card base: s1-1 BASE dev -> main")
	})
}

// TestSprintStepsMergeCoverCrossRefusal pins crossRefusal: the empty other, the
// card itself, an other off the table, one in the same stream, one landed, and ""
// for an other placed in another stream and not landed.
func TestSprintStepsMergeCoverCrossRefusal(t *testing.T) {
	t.Parallel()
	work := NewTable(Work)
	for _, c := range []*Card{
		{ID: "self", Row: "s1", Col: Merging, Rev: 1, Fields: map[string]string{}},
		{ID: "same", Row: "s1", Col: Merging, Rev: 1, Fields: map[string]string{}},
		{ID: "other", Row: "s2", Col: Merging, Rev: 1, Fields: map[string]string{}},
		{ID: "landed", Row: "s2", Col: Landed, Rev: 1, Fields: map[string]string{}},
	} {
		work.Put(c)
	}
	s := &Snapshot{Work: work}
	assert.Contains(t, crossRefusal(s, "s1", "self", ""), "names no other card")
	assert.Contains(t, crossRefusal(s, "s1", "self", "self"), "names the card itself")
	assert.Contains(t, crossRefusal(s, "s1", "self", "ghost"), "is not on the table")
	assert.Contains(t, crossRefusal(s, "s1", "self", "same"), "is in the same stream s1")
	assert.Contains(t, crossRefusal(s, "s1", "self", "landed"), "has landed already")
	assert.Empty(t, crossRefusal(s, "s1", "self", "other"), "an other of another stream is not refused")
}
