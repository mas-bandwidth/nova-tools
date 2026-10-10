package sprint_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/testgit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rebaseBrief is a card's brief cut on a base branch, its BASE: line the one
// rebase reads.
func rebaseBrief(base string) string {
	return "c: a card tier: flash\nREPO: example.invalid/owner/name\nBASE: " + base + "\n\nDo the task.\n"
}

// rebaseStep is the store step the verb runs: it plans sprint.Rebase over the
// work table (cmd/nova-sprint/rebase.go, the rebase verb).
func rebaseStep(r sprint.RebaseReq) store.Step {
	return store.Step{Args: store.ArgsOf(r), Verb: "rebase", Load: []string{sprint.Work}, Mirrors: true,
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.Rebase(s, r) }}
}

// rebaseRepo is a twin repository with a branch old and a branch new that
// contains it: the base the cards are moved from and the one they move to.
func rebaseRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = testgit.Environ()
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "old")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file"), []byte("one\n"), 0o644))
	run("add", ".")
	run("commit", "-q", "-m", "old")
	run("branch", "new")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file"), []byte("two\n"), 0o644))
	run("checkout", "-q", "new")
	run("commit", "-q", "-am", "new")
	run("checkout", "-q", "old")
	return dir
}

// contains is the git merge-base --is-ancestor check of the verb: from is an
// ancestor of to.
func contains(t *testing.T, repo string) func(from, to string) error {
	return func(from, to string) error {
		t.Helper()
		cmd := exec.Command("git", "-C", repo, "merge-base", "--is-ancestor", from, to)
		cmd.Env = testgit.Environ()
		return cmd.Run()
	}
}

// header is a brief's first `KEY: value` line, "" when it carries none.
func header(brief, key string) string {
	for _, l := range strings.Split(brief, "\n") {
		k, v, ok := cardhdr.KeyValue(l)
		if ok && k == key {
			return v
		}
	}
	return ""
}

// rebaseIDs is the card each MOVED line names.
func rebaseIDs(moved []string) []string {
	var out []string
	for _, m := range moved {
		out = append(out, strings.Fields(m)[0])
	}
	return out
}

// TestRebaseMovesEveryUnlandedCardToTheNewBase: nova-sprint rebase
// --from <branch> --to <branch> moves every unlanded card whose BASE is --from
// to --to on a RUNNING machine: an undealt card's brief changes, a dealt card
// keeps its head, and each card is one line in the log.
func TestRebaseMovesEveryUnlandedCardToTheNewBase(t *testing.T) {
	t.Parallel()
	repo := rebaseRepo(t)
	r := newHoldRig(t, 0, 0)
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{
		{ID: "s1-u", Brief: rebaseBrief("old")},
		{ID: "s1-d", Brief: rebaseBrief("old")},
	}}))
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Held: true, Cards: []sprint.CardAdd{
		{ID: "s1-h", Brief: rebaseBrief("old")},
	}}))
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{
		{ID: "s1-other", Brief: rebaseBrief("elsewhere")},
	}}))
	r.tick()

	// take and finish the first card of the line, so it is dealt at a head;
	// s1-h stays held and undealt
	dealt := strings.TrimSuffix(r.takeOne("m1"), ".w1")
	require.Equal(t, "s1-u", dealt, "the first card of the line is the one dealt")
	s := r.snap()
	wc := s.Fleet.Card(s.Work.Card("s1-u").F("work"))
	r.must(store.FinishStep(sprint.FinishReq{As: "m1", Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}, Who: "m1"}))
	r.tick()
	before := r.snap().Work.Placed("s1-u").F("head")
	require.NotEmpty(t, before, "the dealt card is at a head")
	require.Equal(t, sprint.Waiting, r.snap().StateOf("s1-h"), "the held card is undealt")

	res, err := r.st.Run(r.ctx, rebaseStep(sprint.RebaseReq{From: "old", To: "new", Who: "coordinator", Contains: contains(t, repo)}))
	require.NoError(t, err)
	require.Empty(t, res.Refused)
	assert.ElementsMatch(t, []string{"s1-u", "s1-d", "s1-h"}, rebaseIDs(res.Moved), "every unlanded card on old moves, dealt cards included")
	r.tick() // the pump applies the queued work-table rebase

	s = r.snap()
	for _, id := range []string{"s1-u", "s1-d", "s1-h"} {
		c := s.Work.Placed(id)
		require.NotNil(t, c, id)
		assert.Equal(t, "new", header(c.F("brief"), "BASE"), "%s brief is cut on the new base", id)
	}
	assert.Equal(t, "elsewhere", header(s.Work.Placed("s1-other").F("brief"), "BASE"), "a card on another base does not move")
	assert.Equal(t, before, s.Work.Placed("s1-u").F("head"), "the dealt card keeps its head and lands on the new base")

	lines, err := r.st.Log(r.ctx)
	require.NoError(t, err)
	var rebased []string
	for _, l := range lines {
		if l.Note != nil && strings.Contains(l.Note.What, "BASE old -> new") {
			rebased = append(rebased, l.Note.What)
		}
	}
	assert.Len(t, rebased, 3, "one line per card in the log")
}

// TestRebaseRefusesABaseThatDoesNotContainTheOldOne: the git merge-base
// --is-ancestor check refuses the whole rebase, nothing changed, when --to does
// not hold --from.
func TestRebaseRefusesABaseThatDoesNotContainTheOldOne(t *testing.T) {
	t.Parallel()
	repo := rebaseRepo(t)
	r := newHoldRig(t, 0, 0)
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{{ID: "s1-1", Brief: rebaseBrief("old")}}}))
	res, err := r.st.Run(r.ctx, rebaseStep(sprint.RebaseReq{From: "new", To: "old", Who: "coordinator", Contains: contains(t, repo)}))
	require.NoError(t, err)
	require.Len(t, res.Refused, 1)
	assert.Contains(t, res.Refused[0].Why, "does not contain")
	assert.Empty(t, res.Moved, "nothing was changed")
}

// TestMissingBaseJudgmentNamesEveryCardAndTheRebaseLine: a land whose base
// branch is gone raises one judgment naming every unlanded card on that base,
// with the rebase line that fixes them.
func TestMissingBaseJudgmentNamesEveryCardAndTheRebaseLine(t *testing.T) {
	t.Parallel()
	j := sprint.MissingBaseJudgment("dev", []sprint.MissingBaseCard{{ID: "s1-1", Stream: "s1"}, {ID: "s2-1", Stream: "s2"}})
	assert.Equal(t, sprint.NMissingBase, j.Type)
	assert.Equal(t, sprint.Judgment, j.Kind)
	assert.ElementsMatch(t, []string{"s1-1", "s2-1"}, j.Primaries)
	assert.Contains(t, j.Decisions, "rebase")
	assert.Contains(t, j.What, "nova-sprint rebase --from dev --to")
	assert.Contains(t, j.What, "s1-1")
}

// TestMissingBaseJudgmentCarriesItsRaiseTime: the one judgment a land whose
// base branch is gone raises is stamped with the time the step committed it,
// so the coordinator's inbox ages it from the incident instead of showing it
// born at the zero time and permanently overdue (rebase.go, the missing-base
// judgment; docs/SPEC-SPRINT.md, the rebase verb).
func TestMissingBaseJudgmentCarriesItsRaiseTime(t *testing.T) {
	t.Parallel()
	r := newHoldRig(t, 0, 0)
	r.must(store.AddStep(sprint.AddReq{Stream: "s1", Cards: []sprint.CardAdd{
		{ID: "s1-1", Brief: rebaseBrief("dev")},
	}}))
	raised := r.st.Now()
	r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", MissingBase: "dev", Who: "lander"}))

	lines, err := r.st.Log(r.ctx)
	require.NoError(t, err)
	var got *sprint.Note
	for _, l := range lines {
		if l.Note != nil && l.Note.Type == sprint.NMissingBase {
			got = l.Note
		}
	}
	require.NotNil(t, got, "the land raises the missing-base judgment")
	assert.False(t, got.At.IsZero(), "the judgment is not born at the zero time")
	assert.Equal(t, raised, got.At, "the judgment carries the step's raise time")
}
