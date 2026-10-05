package sprint_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A HOLD that proposes PATHS is the machine's to answer (docs/SPEC-SPRINT.md section 8, the
// rules table's row paths; the owner, 2026-10-05: "every step the coordinator did by hand
// today is a missing instruction"). That day five cards held three to five times each on the
// same PATHS blocker: the worker wrote PATHS-PROPOSED in her report every time, the failed
// rule dealt the same brief again until the attempt bound, and the coordinator widened PATHS
// by hand through drop and add --replaces. Now the first such HOLD is answered: with no
// proposed file another open card names on its PATHS: or SHARED: line, the card is replaced
// by its twin (<id>-t, its PATHS widened to the proposal, its dependents its own) and the
// answer is logged with the proposal; with one, the judgment stays the one judgment, its
// text the complete add --replaces command and a brief file the machine wrote under the
// card's job directory, and the card is dealt nothing more. On the twin store (store.Mem).

// pathsHead is the head the held attempt pushed: the twin carries on from it.
const pathsHead = "0123456789abcdef0123456789abcdef01234567"

func pathsBrief(id, paths string) string {
	return id + ": the work (s2) tier: flash\nREPO: mas-bandwidth/nova-tools\nBASE: sprint/s2\nPATHS: " + paths + "\n\nThe task.\n"
}

// pathsRig is the conflict rig with stream s2: p-a and p-b to hold, dep that needs p-a, and
// other, open, whose PATHS name internal/shared/s.go.
func newPathsRig(t *testing.T) (*conflictRig, string) {
	t.Helper()
	dir := t.TempDir()
	r := newConflictRig(t)
	r.must(store.Step{Verb: "set", Load: []string{sprint.Work}, Plan: func(*sprint.Snapshot) sprint.Plan {
		return sprint.Plan{Props: []sprint.PropWrite{{Table: sprint.Work, Name: sprint.PropTwinBriefs, Value: dir, WasAbsent: true}}}
	}})
	r.must(store.AddStep(sprint.AddReq{Stream: "s2", Cards: []sprint.CardAdd{
		{ID: "p-a", Brief: pathsBrief("p-a", "internal/a/a.go")},
		{ID: "p-b", Brief: pathsBrief("p-b", "internal/b/b.go")},
		{ID: "dep", Brief: pathsBrief("dep", "internal/d/d.go"), Needs: []string{"p-a"}},
		{ID: "other", Brief: pathsBrief("other", "internal/shared/s.go")},
	}}))
	return r, dir
}

// hold deals the primary's attempt, takes it and finishes it held with the report.
func (r *conflictRig) hold(id, report string) {
	r.t.Helper()
	if r.snap().Work.Card(id).Col == sprint.Ready {
		r.must(dealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{id}}}))
	}
	s := r.snap()
	wc := s.Fleet.Card(s.Work.Card(id).F("work"))
	require.NotNil(r.t, wc, id)
	if wc.Col != sprint.Working {
		r.must(store.TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
		wc = r.snap().Fleet.Card(wc.ID)
	}
	r.must(store.FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")},
		Failed: true, Head: pathsHead, Report: report}))
	require.Equal(r.t, sprint.Review, r.snap().Work.Card(id).Col, id)
}

// judgmentsOn is the open judgments whose subject is the card.
func judgmentsOn(s *sprint.Snapshot, id string) []sprint.Open {
	var out []sprint.Open
	for _, o := range s.Open {
		if o.Note.Kind == sprint.Judgment && o.Subject() == id {
			out = append(out, o)
		}
	}
	return out
}

func TestAHoldWithPathsProposedCutsTheWidenedTwinByRule(t *testing.T) {
	t.Parallel()
	t.Run("no proposed file is shared: the widened twin replaces it", func(t *testing.T) {
		t.Parallel()
		r, dir := newPathsRig(t)
		r.hold("p-a", "HOLD: the fix needs internal/a/b.go, outside PATHS\nPATHS-PROPOSED: internal/a/b.go")
		r.tick()

		s := r.snap()
		old := s.Work.Card("p-a")
		require.NotNil(t, old)
		assert.False(t, old.Placed(), "the held card is off the table")
		assert.Equal(t, "replaced by p-a-t", old.F("reason"))
		twin := s.Work.Card("p-a-t")
		require.NotNil(t, twin, "the twin keeps the id with a -t suffix")
		require.True(t, twin.Placed())
		assert.Equal(t, "p-a", twin.F(sprint.FieldReplaces))
		assert.Contains(t, twin.F("brief"), "PATHS: internal/a/a.go,internal/a/b.go", "PATHS widened to the proposal")
		assert.Contains(t, twin.F("brief"), "CARRY: p-a attempt 1 head="+pathsHead, "the twin starts from the held head")
		assert.Equal(t, "p-a-t", s.Work.Card("dep").F("needs"), "twins inherit: dep needs the twin")
		assert.Empty(t, judgmentsOn(s, "p-a"), "the held card's judgment is answered")
		assert.Empty(t, r.open(sprint.NBlocked), "no blocked judgment for the replaced card")

		var said []string
		for _, l := range r.logNotes() {
			if l.Kind == sprint.Decided && strings.Contains(l.What, sprint.RuleSaid(sprint.RulePaths, "")) {
				said = append(said, l.What)
			}
		}
		require.Len(t, said, 1, "one answer, logged")
		assert.Contains(t, said[0], "PATHS-PROPOSED: internal/a/b.go", "logged with the proposal")
		assert.Contains(t, said[0], "p-a-t")

		for range 3 {
			r.tick()
		}
		s = r.snap()
		assert.Nil(t, s.Work.Card("p-a-t-t"), "the twin is cut once")
		assert.Nil(t, s.Work.Card("p-a-t2"), "the twin is cut once")
		assert.NoDirExists(t, filepath.Join(dir, "p-a"), "a twin by rule needs no brief file")
	})

	t.Run("a proposed file is shared: one judgment with the complete twin command", func(t *testing.T) {
		t.Parallel()
		r, dir := newPathsRig(t)
		r.hold("p-b", "HOLD: the fix needs internal/shared/s.go\nPATHS-PROPOSED: internal/shared/s.go")
		for range 3 {
			r.tick()
		}

		s := r.snap()
		pb := s.Work.Card("p-b")
		require.True(t, pb.Placed())
		assert.Equal(t, sprint.Review, pb.Col, "held, never dealt again on the same brief")
		assert.Equal(t, 1, pb.Int("attempt"), "no second HOLD on the same proposal")
		assert.Nil(t, s.Work.Card("p-b-t"), "a shared file is a mind's: no twin by rule")

		js := judgmentsOn(s, "p-b")
		require.Len(t, js, 1, "one judgment")
		what := js[0].Note.What
		file := filepath.Join(dir, "p-b", "p-b-t.md")
		assert.Contains(t, what, "internal/shared/s.go")
		assert.Contains(t, what, "other", "names the card that shares the file")
		assert.Contains(t, what, "nova-sprint add --stream s2 --replaces p-b --before p-b --brief-file "+file, "the complete command")

		brief, err := os.ReadFile(file)
		require.NoError(t, err, "the machine wrote the twin's brief")
		assert.Contains(t, string(brief), "PATHS: internal/b/b.go,internal/shared/s.go")
		assert.Contains(t, string(brief), "CARRY: p-b attempt 1 head="+pathsHead)
		assert.Equal(t, 1, strings.Count(what, "nova-sprint add "), "the text says it once over the ticks")
	})

	t.Run("a proposal inside its PATHS already is a mind's, never redealt", func(t *testing.T) {
		t.Parallel()
		r, dir := newPathsRig(t)
		r.hold("p-a", "HOLD: blocked\nPATHS-PROPOSED: internal/a/a.go")
		for range 2 {
			r.tick()
		}
		s := r.snap()
		assert.Equal(t, 1, s.Work.Card("p-a").Int("attempt"), "not redealt on the same brief")
		assert.Nil(t, s.Work.Card("p-a-t"))
		assert.Len(t, judgmentsOn(s, "p-a"), 1)
		assert.NoDirExists(t, filepath.Join(dir, "p-a"), "no twin, no brief file")
	})
}

// logNotes is every note on the sprint's log.
func (r *conflictRig) logNotes() []sprint.Note {
	r.t.Helper()
	lines, err := r.st.Log(r.ctx)
	require.NoError(r.t, err)
	var out []sprint.Note
	for _, l := range lines {
		if l.Note != nil {
			out = append(out, *l.Note)
		}
	}
	return out
}
