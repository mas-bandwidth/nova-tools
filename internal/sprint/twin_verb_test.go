package sprint

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// twin <card> (docs/SPEC-SPRINT.md section 2, "twin"; tla/SprintRules.tla, Replace): the twin
// takes the card's number raised, its brief widened and corrected, its edges and its place;
// the card is dropped "twinned as <id>" and every open judgment on it is answered.

const twinTestBrief = "REPO: example/tools\nPATHS: internal/a/a.go\nTEST: ./internal/a TestA\n\nTHE TASK. Fix the thing.\ntier: pro"

// twinWorld is cards3 in s1 at its bound, with dep1 and dep2 in s2 needing it.
func twinWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, "reader-a", "reader-b")
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"first", "cards3"}, Brief: twinTestBrief, Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"dep1"}, Needs: []string{"cards3"}, Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"dep2"}, Needs: []string{"first", "cards3"}, Who: "coordinator"}))
	w.note(judgment(NBriefWrong, "s1", w.s.Now, 0, "cards3"))
	w.clean("twin world")
	return w
}

func TestTwinReplacesACardAndItsDependentsFollow(t *testing.T) {
	t.Parallel()
	w := twinWorld(t)
	before := w.s.Work.Placed("cards3")
	require.NotNil(t, before)
	require.Len(t, w.openOn("cards3"), 1, "the card is at its bound")
	head := strings.Repeat("ab", 20)
	w.must(Twin(w.s, TwinReq{ID: "cards3", Paths: []string{"internal/b/**", "internal/a/a.go"}, Tier: "heavy",
		Instruction: "Widen the fix to b.", Carry: &TwinCarry{Attempt: 2, Branch: "sprint/cards3.w2", Head: head, Line: "CARRY: cards3 attempt 2 head=" + head},
		Who: "coordinator"}))
	w.clean("twin")

	tw := w.s.Work.Placed("cards4")
	require.NotNil(t, tw, "the twin's id is the card's with its trailing number raised")
	assert.Equal(t, "s1", tw.Row)
	assert.Equal(t, Ready, tw.Col, "the twin starts where a fresh card starts")
	assert.Equal(t, "0", tw.F("attempt"), "from its first attempt")
	assert.Less(t, tw.Score, before.Score, "in the card's place, in front of it")
	assert.Equal(t, "heavy", tw.F(FieldTier))
	assert.Equal(t, "cards3", tw.F(FieldReplaces))
	brief := tw.F("brief")
	assert.Contains(t, brief, "\nPATHS: internal/a/a.go,internal/b/**\n", "PATHS widened, each glob once")
	assert.Contains(t, brief, "THE TASK. This card is the twin of cards3: its attempt 2 pushed branch sprint/cards3.w2 at head "+head+
		", and this attempt starts from that head. Widen the fix to b. Fix the thing.", "THE TASK prefixed by the correction")
	assert.True(t, strings.HasPrefix(brief, "REPO: example/tools\nCARRY: cards3 attempt 2 head="+head+"\n"), "the carry in the header: %q", brief)

	old := w.s.Work.Card("cards3")
	assert.False(t, old.Placed(), "the card is off the table")
	assert.Equal(t, "twinned as cards4", old.F("reason"))
	assert.Equal(t, "cards4", w.s.Work.Card("dep1").F("needs"), "a dependent follows the twin")
	assert.Equal(t, "first,cards4", w.s.Work.Card("dep2").F("needs"), "in the same place, its other need kept")
	assert.Empty(t, w.notesOf(NBlocked), "no blocked judgment")
	assert.Empty(t, w.openOn("cards3"), "the judgment on the card is closed")
	answered := false
	for _, n := range w.notes {
		answered = answered || (n.Kind == Decided && n.Type == NBriefWrong && n.What == "twinned as cards4")
	}
	assert.True(t, answered, "the judgment is answered by the twin")

	// twinned again: the number raised again, the brief left as it was with nothing to add
	w.must(Twin(w.s, TwinReq{ID: "cards4", Who: "coordinator"}))
	assert.Equal(t, brief, w.s.Work.Placed("cards5").F("brief"))
	assert.Equal(t, "heavy", w.s.Work.Placed("cards5").F(FieldTier), "the pin is kept")
	assert.Equal(t, "cards5", w.s.Work.Card("dep1").F("needs"))
}

func TestTwinOfAMergingCardIsReturnedFirst(t *testing.T) {
	t.Parallel()
	w := setup(t, 1)
	w.must(Add(w.s, AddReq{Stream: "s2", IDs: []string{"dep"}, Needs: []string{"s1-1"}, Who: "coordinator"}))
	accepted(w, "s1-1")
	require.Equal(t, Merging, w.state("s1-1"))
	p := w.must(Twin(w.s, TwinReq{ID: "s1-1", Who: "coordinator"}))
	assert.Contains(t, p.Said, TwinReturnedFirst)
	assert.Equal(t, Review, w.state("s1-1"), "returned first")
	assert.Nil(t, w.s.Work.Placed("s1-2"), "the twin is the next step")
	w.must(Twin(w.s, TwinReq{ID: "s1-1", Who: "coordinator"}))
	w.clean("twin of a returned card")
	assert.Equal(t, Ready, w.state("s1-2"), "the twin starts fresh, never in review on the old head")
	assert.Equal(t, "review", w.s.Work.Card("s1-1").F("dropped_from"))
	assert.Nil(t, w.s.Merge.Placed("s1-1"), "its merge place went with it")
	assert.Equal(t, "s1-2", w.s.Work.Card("dep").F("needs"))
	assert.Empty(t, w.openOn("s1-1"), "the returned judgment is answered")
}

func TestTwinIsRefusedWholeWhenItCannotHold(t *testing.T) {
	t.Parallel()
	w := twinWorld(t)
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"stop"}, Sentinel: true, Who: "coordinator"}))
	w.must(Add(w.s, AddReq{Stream: "s3", IDs: []string{"done", "gone"}, Who: "coordinator"}))
	w.place(w.s.Work, "done", "s3", Landed)
	w.must(Drop(w.s, DropReq{Sel: Sel{IDs: []string{"gone"}}, Reason: "r", Who: "coordinator"}))
	for _, tc := range []struct {
		name string
		req  TwinReq
		why  string
	}{
		{"landed", TwinReq{ID: "done"}, "landed: landed is final"},
		{"dropped", TwinReq{ID: "gone"}, "no primary gone on the work table"},
		{"a sentinel", TwinReq{ID: "stop"}, "sentinel"},
		{"no tier", TwinReq{ID: "cards3", Tier: "huge"}, "--tier wants"},
		{"a glob out", TwinReq{ID: "cards3", Paths: []string{"../x"}}, "climbs out"},
		{"a cycle", TwinReq{ID: "cards3", Needs: []string{"dep1"}}, "cycle: "},
		{"not the coordinator", TwinReq{ID: "cards3", Who: "intruder"}, "coordinator"},
	} {
		if tc.req.Who == "" {
			tc.req.Who = "coordinator"
		}
		p := Twin(w.s, tc.req)
		require.NotEmpty(t, p.Refused, tc.name)
		assert.Contains(t, p.Refused[0].Why, tc.why, tc.name)
		assert.Empty(t, p.Units, "%s: nothing is written", tc.name)
	}
	p := Twin(w.s, TwinReq{ID: "cards3", Needs: []string{"dep1"}, Who: "coordinator"})
	assert.Contains(t, p.Refused[0].Why, "dep1 needs cards4", "the cycle names its edge")
	assert.True(t, w.s.Work.Card("cards3").Placed(), "the card is where it was")
	assert.Equal(t, "cards3", w.s.Work.Card("dep1").F("needs"))
}

func TestTwinNumberIDsRaiseTheTrailingNumber(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"cards3": "cards4", "fr-go-runners-r6": "fr-go-runners-r7", "plain": "plain2", "x9": "x10"} {
		assert.Equal(t, want, TwinNumberIDs(in)[0], in)
	}
}
