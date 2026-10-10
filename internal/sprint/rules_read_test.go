package sprint_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A reader's finding is the tick's to answer, by rule (docs/SPEC-SPRINT.md section 8, the
// rules table's row read-broken; tla/SprintRules.tla, Part "reads"). The night of 2026-10-05
// the coordinator answered "a reader found it broken" about forty times, every time with
// `rework <card> --answers <id>`: the finding became the fix. Now the tick does it: the
// finding is the fix of a rework on the same tier; a finding naming a file outside PATHS
// twins the card with PATHS widened; a card at its brief's bound stays the coordinator's.
// On the twin store (store.Mem), every rule on.

// readHead is the head each attempt pushes.
const readHead = "89abcdef0123456789abcdef0123456789abcdef"

// readBrief is a card whose PATHS name one file of internal/x.
const readBrief = "c: the change (r) tier: flash\nREPO: mas-bandwidth/nova-tools\nPATHS: internal/x/a.go\n\nThe task.\n"

// brokenRead drives the primary's dealt (or ready) attempt through its work, pushed at
// readHead, and one read the reader finds broken with the finding.
func (r *conflictRig) brokenRead(id, finding string) {
	r.t.Helper()
	if r.snap().Work.Card(id).Col == sprint.Ready {
		r.must(dealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{id}}}))
	}
	s := r.snap()
	wc := s.Fleet.Card(s.Work.Card(id).F("work"))
	require.NotNil(r.t, wc, id)
	r.must(store.TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
	wc = r.snap().Fleet.Card(wc.ID)
	r.must(store.FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}, Head: readHead}))
	asked := func() *sprint.Card {
		s := r.snap()
		pr := s.Work.Card(id)
		for _, rc := range s.Readers.Of(id) {
			if rc.Int("attempt") == pr.Int("attempt") && (rc.Col == sprint.Asked || rc.Col == sprint.Reading) {
				return rc
			}
		}
		return nil
	}
	rc := asked()
	if rc == nil {
		r.must(store.AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: []string{id}}}))
		rc = asked()
	}
	require.NotNil(r.t, rc, "a read of %s is asked", id)
	r.must(store.ReadStep(sprint.ReadReq{Usage: "input=1000 output=100", As: rc.Row, Verdict: "broken", Finding: finding, Sel: sprint.Sel{IDs: []string{rc.ID}}}))
}

// readCard adds the card r-1 with readBrief.
func (r *conflictRig) readCard() {
	r.t.Helper()
	r.must(store.AddStep(sprint.AddReq{Stream: "r", IDs: []string{"r-1"}, Brief: readBrief}))
}

// The fault inventory of 2026-10-10: 56 of 105 cards in review carried a rule_answer older
// than their finished_at. A rule's answer and note belong to the attempt it started: that
// attempt's finish consumes them.
func TestARuleAnswerIsConsumedByTheAttemptItStarted(t *testing.T) {
	t.Parallel()
	r := newConflictRig(t)
	r.readCard()
	r.brokenRead("r-1", "internal/x/a.go:12 drops the error from Close; return it")
	r.tick()
	pr := r.snap().Work.Card("r-1")
	require.Equal(t, 2, pr.Int("attempt"), "reworked by rule in the tick")
	require.NotEmpty(t, pr.F(sprint.FieldRuleAnswer), "the answer is on the card while its attempt runs")
	require.NotEmpty(t, pr.F("note"))

	wc := r.snap().Fleet.Card(sprint.WorkCardID("r-1", 2))
	require.NotNil(t, wc)
	r.must(store.TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
	wc = r.snap().Fleet.Card(wc.ID)
	r.must(store.FinishStep(sprint.FinishReq{Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}, Head: readHead}))
	pr = r.snap().Work.Card("r-1")
	require.Equal(t, sprint.Review, pr.Col)
	assert.Empty(t, pr.F(sprint.FieldRuleAnswer), "its attempt finished: the answer is consumed")
	assert.Empty(t, pr.F("note"), "and its note")
}

func TestABrokenReadIsReworkedByRuleWithItsFinding(t *testing.T) {
	t.Parallel()
	t.Run("a finding inside PATHS: reworked with it as the fix, on the same tier", func(t *testing.T) {
		t.Parallel()
		r := newConflictRig(t)
		r.readCard()
		finding := "internal/x/a.go:12 drops the error from Close; return it"
		r.brokenRead("r-1", finding)
		require.Len(t, r.openOnCard(sprint.NReadBroken, "r-1"), 1, "the reader's finding is a judgment")
		r.tick()

		s := r.snap()
		pr := s.Work.Card("r-1")
		require.Equal(t, 2, pr.Int("attempt"), "reworked by the next tick")
		assert.Equal(t, finding, pr.F("fix"), "the finding is the fix")
		assert.Empty(t, pr.F(sprint.FieldTier), "on the same tier")
		assert.Empty(t, r.openOnCard(sprint.NReadBroken, "r-1"), "the judgment is answered")
		answered := r.answeredBy(sprint.RuleReadBroken)
		require.Len(t, answered, 1)
		assert.Contains(t, answered[0].What, sprint.ActRework)
		assert.True(t, strings.HasPrefix(pr.F("note"), "answered by rule "+sprint.RuleReadBroken+": "), "a note on the card names the rule: %q", pr.F("note"))
		assert.Contains(t, pr.F(sprint.FieldRuleAnswer), sprint.RuleReadBroken+": ")
		wc := s.Fleet.Card(sprint.WorkCardID("r-1", 2))
		require.NotNil(t, wc, "the next attempt is dealt")
		assert.Equal(t, finding, wc.F("fix"), "its child is told the finding")
		assert.Equal(t, "flash", strings.SplitN(wc.F(sprint.FieldRoute), "-", 2)[0], "dealt on the same tier")
		assert.Empty(t, r.coordinatorNotes("r-1"), "the coordinator is told nothing")
		r.clean("reworked by rule")
	})
	t.Run("a finding naming a file outside PATHS: twinned with PATHS widened", func(t *testing.T) {
		t.Parallel()
		r := newConflictRig(t)
		r.readCard()
		finding := "internal/y/b.go:40 still calls the old name, outside PATHS; rename the call too"
		r.brokenRead("r-1", finding)
		r.tick()

		s := r.snap()
		old := s.Work.Card("r-1")
		require.NotNil(t, old)
		assert.False(t, old.Placed(), "the card is replaced")
		assert.Contains(t, old.F("reason"), "replaced by r-1b")
		twin := s.Work.Placed("r-1b")
		require.NotNil(t, twin, "the twin is on the table")
		assert.Equal(t, "r-1", twin.F(sprint.FieldReplaces))
		brief := twin.F("brief")
		assert.Contains(t, brief, "\nPATHS: internal/x/a.go,internal/y/b.go\n", "PATHS widened by exactly the file")
		assert.Contains(t, brief, "CARRY: r-1 attempt 1 head="+readHead, "the twin starts from the broken attempt's head")
		assert.Equal(t, finding, twin.F("fix"), "the finding is the twin's fix")
		assert.True(t, strings.HasPrefix(twin.F("note"), "answered by rule "+sprint.RuleReadBroken+": "), "a note on the twin names the rule: %q", twin.F("note"))
		assert.Empty(t, r.openOnCard(sprint.NReadBroken, "r-1"), "the judgment is answered")
		require.Len(t, r.answeredBy(sprint.RuleReadBroken), 1)
		assert.Empty(t, r.coordinatorNotes("r-1"), "the coordinator is told nothing")

		r.tick()
		assert.Nil(t, r.snap().Work.Card("r-1c"), "twinned once")
		r.clean("twinned by rule")
	})
	t.Run("at the brief's bound: the coordinator's", func(t *testing.T) {
		t.Parallel()
		r := newConflictRig(t)
		r.readCard()
		finding := "internal/x/a.go:12 drops the error from Close; return it"
		r.brokenRead("r-1", finding)
		r.tick()
		require.Equal(t, 2, r.snap().Work.Card("r-1").Int("attempt"))
		r.brokenRead("r-1", finding)
		r.tick()
		r.tick()

		pr := r.snap().Work.Card("r-1")
		assert.Equal(t, 2, pr.Int("attempt"), "the same finding twice is not reworked")
		assert.Equal(t, sprint.Review, pr.Col)
		assert.Len(t, r.openOnCard(sprint.NBriefWrong, "r-1"), 1, "the bound stays a judgment")
		assert.Len(t, r.answeredBy(sprint.RuleReadBroken), 1, "the rule answered the first finding alone")
	})
	t.Run("turned off: stays a judgment", func(t *testing.T) {
		t.Parallel()
		r := newConflictRig(t)
		r.m.SetRulesOff(sprint.RuleReadBroken)
		r.readCard()
		r.brokenRead("r-1", "internal/x/a.go:12 drops the error from Close; return it")
		r.tick()
		assert.Equal(t, 1, r.snap().Work.Card("r-1").Int("attempt"))
		assert.Len(t, r.openOnCard(sprint.NReadBroken, "r-1"), 1)
	})
}

// The files a finding names outside a brief's PATHS: a test, a testdata file, a TLA+
// ledger, the docs catalog and an AGENTS.md map are inside every PATHS
// (cardgen.AlwaysInPaths), so a finding naming only those twins nothing.
func TestAFindingNamesFilesOutsidePaths(t *testing.T) {
	t.Parallel()
	brief := "c: x (s)\nREPO: o/r\nPATHS: internal/x/a.go,internal/z/*.go,docs/\n\nThe task.\n"
	for _, tc := range []struct {
		finding string
		want    []string
	}{
		{"internal/x/a.go:12 drops the error", nil},
		{"internal/z/q.go:3 and docs/SPEC-X.md say otherwise", nil},
		{"internal/y/b.go:40 calls the old name; `internal/y/b_test.go` too", []string{"internal/y/b.go"}},
		{"internal/y/b_test.go:9, internal/y/testdata/deep/g.txt, tla/RUNS.tsv, tla/CASES.tsv, internal/docs/catalog.go and internal/y/AGENTS.md are stale", nil},
		{"branch sprint/c.w1.g3.e15 at /abs/x/y.go and ../up/z.go, see https://example.invalid/a/b.go", nil},
		{"the TestA step fails", nil},
	} {
		assert.Equal(t, tc.want, sprint.FilesOutsidePaths(brief, tc.finding), tc.finding)
	}
	assert.Nil(t, sprint.FilesOutsidePaths("c: x (s)\n\nno PATHS line\n", "internal/y/b.go:1 is wrong"), "no PATHS: nothing is outside")
	assert.Equal(t, "c: x (s)\nCARRY: s-1 attempt 2 head=h\nREPO: o/r\nPATHS: internal/x/a.go,internal/y/b.go\n\nThe task.\n",
		sprint.PathsWidened("c: x (s)\nREPO: o/r\nPATHS: internal/x/a.go\n\nThe task.\n", []string{"internal/y/b.go"}, "CARRY: s-1 attempt 2 head=h"))
}

// coordinatorNotes is the happened notes to the coordinator whose text holds what.
func (r *conflictRig) coordinatorNotes(what string) []sprint.Note {
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

// openOnCard is the open judgments of the type on the card.
func (r *conflictRig) openOnCard(typ, id string) []sprint.Open {
	r.t.Helper()
	var out []sprint.Open
	for _, o := range r.open(typ) {
		if o.Subject() == id && o.Note.Kind == sprint.Judgment {
			out = append(out, o)
		}
	}
	return out
}

func TestRequiredValidationFilesNeverWidenAReadersScope(t *testing.T) {
	t.Parallel()
	finding := "internal/y/y_test.go, internal/y/testdata/case.json, testdata/deep/witness.tla, tla/RUNS.tsv, tla/CASES.tsv, internal/docs/catalog.go and internal/y/AGENTS.md are required; internal/y/y.go remains source."
	assert.Equal(t, []string{"internal/y/y.go"}, sprint.FilesOutsidePaths("PATHS: internal/x/x.go\n", finding))
}
