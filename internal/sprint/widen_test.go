package sprint_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A card held only for files outside its PATHS that are adjacent to its change is twinned
// wider by the machine (docs/SPEC-SPRINT.md section 8, answered by rule, the row widen; the
// owner, 2026-10-05: "now all the things you are doing manually now in LLM space, make sure
// there are cards for this sprint to have them automated by the machine"). That day the
// coordinator twinned ten such cards by hand, each file read from the HOLD, appended to PATHS
// and SHARED, and the twin pointed at the finished head. On the twin store (store.Mem), the
// worker's HOLD in its own words.

// widenHead is the head the held attempt pushed.
const widenHead = "0123456789abcdef0123456789abcdef01234567"

// widenBrief is a card whose PATHS name one file of internal/x.
const widenBrief = "c: the change (w) tier: flash\nREPO: mas-bandwidth/nova-tools\nPATHS: internal/x/a.go\nSHARED: internal/x/a.go\n\nThe task.\n"

// heldOut adds the card w-1, deals it, and finishes its first attempt held with the report,
// at widenHead.
func (r *conflictRig) heldOut(report string) {
	r.t.Helper()
	r.must(store.AddStep(sprint.AddReq{Stream: "w", IDs: []string{"w-1"}, Brief: widenBrief}))
	r.tick()
	if r.snap().Work.Card("w-1").Col == sprint.Ready {
		r.must(store.DealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{"w-1"}}}))
	}
	s := r.snap()
	wc := s.Fleet.Card(s.Work.Card("w-1").F("work"))
	require.NotNil(r.t, wc)
	r.must(store.TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
	wc = r.snap().Fleet.Card(wc.ID)
	r.must(store.FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")},
		Failed: true, Head: widenHead, Report: report}))
	require.Equal(r.t, sprint.Review, r.snap().Work.Card("w-1").Col)
}

// notesTo is the happened notes addressed to the coordinator whose text holds what.
func (r *conflictRig) notesTo(what string) []sprint.Note {
	r.t.Helper()
	all, _, err := r.m.NotesSince(r.ctx, "", 100000)
	require.NoError(r.t, err)
	var out []sprint.Note
	for _, n := range all {
		if n.Kind == sprint.Happened && n.To == "coordinator" && strings.Contains(n.What, what) {
			out = append(out, n)
		}
	}
	return out
}

// openOn is the open judgments of the type on the card.
func (r *conflictRig) openOn(typ, id string) []sprint.Open {
	r.t.Helper()
	var out []sprint.Open
	for _, o := range r.open(typ) {
		if o.Subject() == id {
			out = append(out, o)
		}
	}
	return out
}

func TestACardHeldOnlyForAdjacentPathsIsTwinnedWider(t *testing.T) {
	t.Parallel()
	t.Run("a test file and a ledger: twinned wider, one note", func(t *testing.T) {
		t.Parallel()
		r := newConflictRig(t)
		r.heldOut("HOLD: the change is done and green, but it needs files outside PATHS: " +
			"internal/x/a_test.go (the package's test asserts the new field) and " +
			"internal/ci/testdata/deleted-tests.txt (the ledger the change grows); the branch is sprint/w-1.w1.g1.e1")
		r.tick()

		s := r.snap()
		old := s.Work.Card("w-1")
		require.NotNil(t, old)
		assert.False(t, old.Placed(), "the held card is off the table")
		assert.Equal(t, "dropped", old.F("outcome"))
		assert.Contains(t, old.F("reason"), "replaced by w-1b")
		twin := s.Work.Placed("w-1b")
		require.NotNil(t, twin, "the twin is on the table")
		assert.Equal(t, "w-1", twin.F(sprint.FieldReplaces))
		brief := twin.F("brief")
		assert.Contains(t, brief, "PATHS: internal/x/a.go,internal/x/a_test.go,internal/ci/testdata/deleted-tests.txt\n", "PATHS widened to exactly the files")
		assert.Contains(t, brief, "SHARED: internal/x/a.go,internal/x/a_test.go,internal/ci/testdata/deleted-tests.txt\n", "SHARED widened too")
		assert.NotContains(t, brief, "sprint/w-1.w1.g1.e1", "a branch is not a file")
		carry, ok := member.CarryOf(brief)
		require.True(t, ok, "the twin's brief carries the head: %s", brief)
		assert.Equal(t, member.Carry{Card: "w-1", Attempt: 1, Head: widenHead}, carry, "the twin starts from the finished head")
		assert.Contains(t, twin.F("widened"), "internal/x/a_test.go,internal/ci/testdata/deleted-tests.txt from w-1 attempt 1 head="+widenHead)

		assert.Empty(t, r.openOn(sprint.NWorkFailed, "w-1"), "the HOLD is answered by rule")
		assert.NotEmpty(t, r.answeredBy("widen"))
		notes := r.notesTo("w-1 twinned as w-1b")
		require.Len(t, notes, 1, "the coordinator gets one note")
		assert.Contains(t, notes[0].What, "internal/x/a_test.go")
		assert.Contains(t, notes[0].What, "internal/ci/testdata/deleted-tests.txt")

		r.tick()
		assert.Len(t, r.notesTo("w-1 twinned as w-1b"), 1, "once")
		assert.Nil(t, r.snap().Work.Card("w-1c"), "twinned once")
		r.clean("twinned wider")
	})
	t.Run("an unrelated package: stays a judgment", func(t *testing.T) {
		t.Parallel()
		r := newConflictRig(t)
		r.m.SetRulesOff(sprint.RuleFailed) // the failed rule would rework a machine's card: the judgment is left to show
		r.heldOut("HOLD: the change needs files outside PATHS: internal/x/a_test.go (its test) and " +
			"internal/other/b.go (another package's code calls the old name)")
		r.tick()
		r.tick()

		s := r.snap()
		pr := s.Work.Placed("w-1")
		require.NotNil(t, pr, "the held card stays on the table")
		assert.Equal(t, sprint.Review, pr.Col)
		assert.Equal(t, 1, pr.Int("attempt"))
		assert.Nil(t, s.Work.Card("w-1b"), "no twin")
		assert.Len(t, r.openOn(sprint.NWorkFailed, "w-1"), 1, "a non-adjacent file is a mind's")
		assert.Empty(t, r.answeredBy("widen"))
		assert.Empty(t, r.notesTo("twinned as"))
		r.clean("left")
	})
}
