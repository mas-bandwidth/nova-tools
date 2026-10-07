package sprint_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A card held only for files outside its PATHS is the tick's to twin, by rule
// (docs/SPEC-SPRINT.md section 8, the rules table's row widen). On 2026-10-05 the coordinator
// twinned ten such cards by hand, each file read from the HOLD, appended to PATHS and SHARED,
// the twin pointed at the finished head. Now the tick does it when every file is adjacent to
// the change, with one note to the coordinator naming the files; a HOLD naming an unrelated
// package stays a judgment. On the twin store (store.Mem), every rule on.

// widenBrief is a card whose PATHS and SHARED name one file of internal/x.
const widenBrief = "c: the change (w) tier: flash\nREPO: mas-bandwidth/nova-tools\nPATHS: internal/x/a.go\nSHARED: internal/x/a.go\n\nThe task.\n"

// widenCard adds the card w-1 with widenBrief.
func (r *conflictRig) widenCard() {
	r.t.Helper()
	r.must(store.AddStep(sprint.AddReq{Stream: "w", IDs: []string{"w-1"}, Brief: widenBrief}))
}

// held drives the primary's dealt (or ready) attempt through its work, finished failed at
// readHead with the report.
func (r *conflictRig) held(id, report string) {
	r.t.Helper()
	wc := r.taken(id)
	r.must(store.FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}, Failed: true, Head: readHead, Report: report}))
}

// taken is the primary's attempt dealt (when ready) and taken.
func (r *conflictRig) taken(id string) *sprint.Card {
	r.t.Helper()
	if r.snap().Work.Card(id).Col == sprint.Ready {
		r.must(dealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{id}}}))
	}
	s := r.snap()
	wc := s.Fleet.Card(s.Work.Card(id).F("work"))
	require.NotNil(r.t, wc, id)
	r.must(store.TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
	return r.snap().Fleet.Card(wc.ID)
}

// refusedE12 drives the primary through work at readHead, its reads ok and the accept, and the
// lander's E12 refusal naming the file.
func (r *conflictRig) refusedE12(id, stream, file string) {
	r.t.Helper()
	wc := r.taken(id)
	r.must(store.FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}, Head: readHead}))
	for range 4 {
		s := r.snap()
		if pr := s.Work.Card(id); pr.Col != sprint.Review || sprint.ReadsWanted(s, pr) == 0 {
			break
		}
		r.must(store.AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{id}}}))
		for _, rc := range r.snap().Readers.Of(id) {
			if rc.Col == sprint.Asked || rc.Col == sprint.Reading {
				r.must(store.ReadStep(sprint.ReadReq{Usage: "input=1000 output=100", As: rc.Row, Verdict: "ok", Finding: "f:1", Sel: sprint.Sel{IDs: []string{rc.ID}}}))
			}
		}
	}
	r.must(store.AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{id}}}))
	r.must(store.MergeStep(sprint.MergeReq{Stream: stream, Batch: 1, Conflict: id,
		Note: "the head " + readHead + " of " + id + " fails the lander's checks: it changes files outside its PATHS (E12): " + file}))
}

// widenNotes is the coordinator's PATHS-widened notes.
func (r *conflictRig) widenNotes() []sprint.Note {
	r.t.Helper()
	var out []sprint.Note
	for _, n := range r.coordinatorNotes("") {
		if n.Type == sprint.NPathsWidened {
			out = append(out, n)
		}
	}
	return out
}

func TestACardHeldOnlyForAdjacentPathsIsWidenedInPlace(t *testing.T) {
	t.Parallel()
	t.Run("a HOLD naming a doc: widened in place, one note", func(t *testing.T) {
		t.Parallel()
		r := newConflictRig(t)
		r.widenCard()
		r.held("w-1", "Verdict: HOLD\nHead: "+readHead+"\nThe change is done and green; it needs files outside PATHS: docs/NOTE.md.\n")
		require.Len(t, r.openOnCard(sprint.NWorkFailed, "w-1"), 1, "the HOLD is a judgment")
		r.tick()

		s := r.snap()
		assert.Nil(t, s.Work.Card("w-1b"), "no twin")
		pr := s.Work.Placed("w-1")
		require.NotNil(t, pr, "the same card, on the table")
		assert.NotEqual(t, sprint.Review, pr.Col, "its next attempt is opened")
		assert.Equal(t, "1", pr.F(sprint.FieldBriefAttempt), "its brief's bound counts again from attempt 1: no attempt charged")
		brief := pr.F("brief")
		assert.Contains(t, brief, "\nPATHS: internal/x/a.go,docs/NOTE.md\n", "PATHS widened by exactly the file")
		assert.Contains(t, brief, "\nSHARED: internal/x/a.go,docs/NOTE.md\n", "SHARED widened too")
		assert.Contains(t, brief, "CARRY: w-1 attempt 1 head="+readHead, "its next attempt starts from the finished head")
		assert.True(t, strings.HasPrefix(pr.F("fix"), "start from head "+readHead), "the fix names the head: %q", pr.F("fix"))
		assert.True(t, strings.HasPrefix(pr.F("note"), "answered by rule "+sprint.RuleWiden+": "), "a note on the card names the rule: %q", pr.F("note"))
		assert.Empty(t, r.openOnCard(sprint.NWorkFailed, "w-1"), "the judgment is answered")
		require.Len(t, r.answeredBy(sprint.RuleWiden), 1)
		notes := r.widenNotes()
		require.Len(t, notes, 1, "the coordinator gets one note")
		assert.Contains(t, notes[0].What, "docs/NOTE.md ("+sprint.AdjDocs+")")
		assert.Equal(t, []string{"w-1"}, notes[0].Primaries)

		r.tick()
		assert.Len(t, r.widenNotes(), 1, "widened once, noted once")
		r.clean("widened in place by rule")
	})
	t.Run("a HOLD naming only a test and a ledger is already inside PATHS", func(t *testing.T) {
		t.Parallel()
		r := newConflictRig(t)
		r.widenCard()
		r.held("w-1", "Verdict: HOLD\nHead: "+readHead+"\nThe change is done and green; it needs files outside PATHS: internal/x/a_test.go and internal/ci/testdata/testify/internal_x.txt.\n")
		r.tick()

		s := r.snap()
		assert.Nil(t, s.Work.Card("w-1b"), "not twinned")
		assert.Equal(t, 2, s.Work.Card("w-1").Int("attempt"), "the failed rule reworks it: those files are not outside PATHS")
		assert.NotContains(t, s.Work.Card("w-1").F("brief"), "a_test.go")
		assert.Empty(t, r.widenNotes())
		assert.Empty(t, r.answeredBy(sprint.RuleWiden))
		r.clean("a test and a ledger are not a paths hold")
	})
	t.Run("a HOLD naming an unrelated package: stays a judgment naming it", func(t *testing.T) {
		t.Parallel()
		r := newConflictRig(t)
		r.widenCard()
		r.held("w-1", "Verdict: HOLD\nHead: "+readHead+"\nThe change needs files outside PATHS: cmd/nova-other/main.go\n")
		r.tick()
		r.tick()

		s := r.snap()
		pr := s.Work.Card("w-1")
		assert.Equal(t, 1, pr.Int("attempt"), "no rule reworks it")
		assert.Equal(t, sprint.Review, pr.Col)
		assert.Nil(t, s.Work.Card("w-1b"), "not twinned")
		open := r.openOnCard(sprint.NWorkFailed, "w-1")
		require.Len(t, open, 1, "it stays a judgment")
		assert.True(t, strings.HasPrefix(open[0].Note.What, sprint.WidenBlocked+"cmd/nova-other/main.go (its HOLD): "), "its text names what blocks it: %q", open[0].Note.What)
		assert.Empty(t, r.widenNotes())
		assert.Empty(t, r.answeredBy(sprint.RuleWiden))
	})
	t.Run("an E12 refusal naming a doc: widened in place", func(t *testing.T) {
		t.Parallel()
		r := newConflictRig(t)
		r.widenCard()
		r.refusedE12("w-1", "w", "docs/NOTE.md")
		r.tick()

		s := r.snap()
		assert.Nil(t, s.Work.Card("w-1b"), "no twin")
		pr := s.Work.Placed("w-1")
		require.NotNil(t, pr, "the same card, on the table")
		assert.Contains(t, pr.F("brief"), "\nPATHS: internal/x/a.go,docs/NOTE.md\n")
		assert.Contains(t, pr.F("brief"), "CARRY: w-1 attempt 1 head="+readHead)
		assert.Equal(t, "1", pr.F(sprint.FieldBriefAttempt), "no attempt charged")
		assert.Empty(t, r.openOnCard(sprint.NReturned, "w-1"))
		require.Len(t, r.widenNotes(), 1)
		r.clean("an E12 refusal widened in place")
	})
	t.Run("an E12 refusal naming only a test is redone inside its PATHS", func(t *testing.T) {
		t.Parallel()
		r := newConflictRig(t)
		r.widenCard()
		r.refusedE12("w-1", "w", "internal/x/a_test.go")
		r.tick()

		s := r.snap()
		assert.Nil(t, s.Work.Card("w-1b"), "not twinned")
		assert.Equal(t, 2, s.Work.Card("w-1").Int("attempt"), "a test is not outside PATHS, so the conflict rule redoes it")
		assert.NotContains(t, s.Work.Card("w-1").F("brief"), "a_test.go")
		assert.Empty(t, r.widenNotes())
		r.clean("an E12 refusal of a test is not a widen")
	})
	t.Run("an E12 refusal naming an unrelated package: redone inside its PATHS", func(t *testing.T) {
		t.Parallel()
		r := newConflictRig(t)
		r.widenCard()
		r.refusedE12("w-1", "w", "cmd/nova-other/main.go")
		r.tick()

		s := r.snap()
		assert.Nil(t, s.Work.Card("w-1b"), "not twinned")
		assert.Equal(t, 2, s.Work.Card("w-1").Int("attempt"), "the conflict rule redoes it")
		assert.Empty(t, r.widenNotes())
	})
	t.Run("a HOLD proposing a shared file: the paths rule leaves it, and so does widen", func(t *testing.T) {
		t.Parallel()
		r := newConflictRig(t)
		r.widenCard()
		r.must(store.AddStep(sprint.AddReq{Stream: "v", IDs: []string{"v-1"}, Brief: "c: other (v) tier: flash\nREPO: mas-bandwidth/nova-tools\nPATHS: internal/v/b.go\nSHARED: internal/x/a_test.go\n\nThe task.\n"}))
		r.held("w-1", "Verdict: HOLD\nHead: "+readHead+"\nThe change needs internal/x/a_test.go outside PATHS.\nPATHS-PROPOSED: internal/x/a_test.go\n")
		r.tick()
		r.tick()
		assert.Nil(t, r.snap().Work.Card("w-1b"), "not twinned by widen")
		assert.Empty(t, r.answeredBy(sprint.RuleWiden))
		assert.Empty(t, r.widenNotes())
		open := r.openOnCard(sprint.NWorkFailed, "w-1")
		require.Len(t, open, 1, "the paths rule's one judgment")
		assert.True(t, strings.HasPrefix(open[0].Note.What, "paths proposed, shared"), "%q", open[0].Note.What)
	})
	t.Run("turned off: the failed rule reworks it", func(t *testing.T) {
		t.Parallel()
		r := newConflictRig(t)
		r.m.SetRulesOff(sprint.RuleWiden)
		r.widenCard()
		r.held("w-1", "Verdict: HOLD\nHead: "+readHead+"\nneeds internal/x/a_test.go outside PATHS\n")
		r.tick()
		assert.Nil(t, r.snap().Work.Card("w-1b"))
		assert.Equal(t, 2, r.snap().Work.Card("w-1").Int("attempt"))
	})
}

// What makes a file outside PATHS adjacent to the change.
func TestAFileOutsidePathsIsAdjacentByRule(t *testing.T) {
	t.Parallel()
	paths := []string{"internal/x/a.go", "internal/y/"}
	for _, tc := range []struct {
		name, file, text string
		hold             bool
		want             string
	}{
		{"the package's test", "internal/x/a_test.go", "", false, sprint.AdjTests},
		{"a directory's test", "internal/y/b_test.go", "", false, sprint.AdjTests},
		{"the package's testdata", "internal/x/testdata/in.txt", "", false, sprint.AdjTestdata},
		{"a ledger", "internal/ci/testdata/deleted-tests.txt", "", false, sprint.AdjLedger},
		{"a doc", "docs/SPEC-SPRINT.md", "", false, sprint.AdjDocs},
		{"a map", "internal/x/AGENTS.md", "", false, sprint.AdjDocs},
		{"another package's test", "internal/z/c_test.go", "", false, ""},
		{"another package", "internal/z/c.go", "", false, ""},
		{"named in a HOLD with its reason", "internal/z/c.go", "HOLD: internal/z/c.go:12 must carry the new field too", true, sprint.AdjNamed},
		{"named in a HOLD in a list", "internal/z/c.go", "HOLD: needs internal/z/c.go, internal/z/d.go", true, ""},
		{"named with a reason, not in a HOLD", "internal/z/c.go", "internal/z/c.go:12 must carry the new field too", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, sprint.WidenAdjacent(paths, tc.file, tc.text, tc.hold))
		})
	}
	assert.Equal(t, "c: x\nSHARED: a/b.go,c/d.go\n", sprint.SharedWidened("c: x\nSHARED: a/b.go\n", []string{"c/d.go", "a/b.go"}))
	assert.Equal(t, "c: x\n", sprint.SharedWidened("c: x\n", []string{"c/d.go"}), "no SHARED line: unchanged")
}
