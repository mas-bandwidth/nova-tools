package sprint_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/testgit"
)

// stream set --base re-points a stream to a live base (docs/SPEC-SPRINT.md,
// stream-set-base-b.w3): a stream whose base is gone, merged or red had no verb to move
// its cards to a live one, so every card of the stream not yet dealt and every card queued
// to merge gets its brief's BASE line rewritten, a brief revision recorded, and the cards
// dealt and working keep their base and are listed. The verb refuses whole, writing
// nothing, when origin holds no such branch or when a card's PATHS are absent at the tip
// (the check add runs). On the twin store (store.Mem) with a temporary origin.
func TestStreamSetBaseRepointsQueuedCards(t *testing.T) {
	t.Parallel()
	clone := setBaseTwin(t)
	t.Run("repointsReadyAndQueuedAndListsDealt", func(t *testing.T) {
		t.Parallel()
		r := newConflictRig(t)
		addSetBaseCards(r, "sb")
		// sb-2 is queued to merge, sb-3 is dealt and working: the first two are targets,
		// the third keeps its base and is listed.
		r.toMerging("sb-2")
		takeSetBaseCard(r, "sb-3")
		require.Equal(t, sprint.Merging, r.snap().Work.Card("sb-2").Col, "sb-2 is queued to merge")
		require.Equal(t, sprint.Working, r.snap().Work.Card("sb-3").Col, "sb-3 is working")

		sha, gone, err := sprint.BaseBranchTip(context.Background(), clone, "origin", "sprint/live", testgit.Environ())
		require.NoError(t, err)
		require.False(t, gone, "origin holds sprint/live")
		missing := map[string][]string{}
		for _, id := range []string{"sb-1", "sb-2", "sb-3"} {
			if m := sprint.SetBaseCheck(r.snap().Work.Card(id).F("brief"), "sprint/live", clone, sha); len(m) > 0 {
				missing[id] = m
			}
		}
		require.Empty(t, missing, "every PATHS entry is at the live tip")

		res := r.must(streamSetBaseStep(sprint.StreamSetBaseReq{Streams: []string{"sb"}, Base: "sprint/live", Who: "coordinator", Missing: missing}))
		assert.NotEmpty(t, res.Moved, "the verb moved cards")
		s := r.snap()
		assert.Contains(t, s.Work.Card("sb-1").F("brief"), "BASE: sprint/live", "a ready card is re-pointed")
		assert.Contains(t, s.Work.Card("sb-2").F("brief"), "BASE: sprint/live", "a queued-to-merge card is re-pointed")
		assert.Contains(t, s.Work.Card("sb-3").F("brief"), "BASE: sprint/old", "a working card keeps its base")
		assert.Contains(t, strings.Join(res.Said, "\n"), "sb-3", "the dealt card is listed")
		assert.NotContains(t, strings.Join(res.Said, "\n"), "sb-1", "a re-pointed card is not listed as kept")
	})
	t.Run("refusesMissingBranchAndWritesNothing", func(t *testing.T) {
		t.Parallel()
		r := newConflictRig(t)
		addSetBaseCards(r, "sb")
		_, gone, err := sprint.BaseBranchTip(context.Background(), clone, "origin", "sprint/gone", testgit.Environ())
		require.NoError(t, err)
		require.True(t, gone, "origin holds no sprint/gone")
		res, err := r.st.Run(r.ctx, streamSetBaseStep(sprint.StreamSetBaseReq{Streams: []string{"sb"}, Base: "sprint/gone", Who: "coordinator", Gone: gone}))
		require.NoError(t, err)
		require.NotEmpty(t, res.Refused, "a branch origin does not hold is refused")
		assert.Empty(t, res.Moved, "nothing was written")
		assert.Contains(t, r.snap().Work.Card("sb-1").F("brief"), "BASE: sprint/old", "the card keeps its base")
	})
	t.Run("refusesPathsAbsentAtTheTipAndWritesNothing", func(t *testing.T) {
		t.Parallel()
		r := newConflictRig(t)
		addSetBaseCards(r, "sb")
		// sb-4 names a PATHS file that is not at the live tip
		r.must(store.AddStep(sprint.AddReq{Stream: "sb", Cards: []sprint.CardAdd{
			{ID: "sb-4", Brief: setBaseBrief("sprint/old", "internal/x/new.go")},
		}}))
		sha, gone, err := sprint.BaseBranchTip(context.Background(), clone, "origin", "sprint/live", testgit.Environ())
		require.NoError(t, err)
		require.False(t, gone)
		missing := map[string][]string{}
		for _, id := range []string{"sb-1", "sb-2", "sb-3", "sb-4"} {
			if m := sprint.SetBaseCheck(r.snap().Work.Card(id).F("brief"), "sprint/live", clone, sha); len(m) > 0 {
				missing[id] = m
			}
		}
		require.NotEmpty(t, missing, "sprint/live lacks an internal/x/new.go the card names")
		res, err := r.st.Run(r.ctx, streamSetBaseStep(sprint.StreamSetBaseReq{Streams: []string{"sb"}, Base: "sprint/live", Who: "coordinator", Missing: missing}))
		require.NoError(t, err)
		require.NotEmpty(t, res.Refused, "a card whose PATHS miss the tip is refused")
		assert.Empty(t, res.Moved, "nothing was written")
		assert.Contains(t, r.snap().Work.Card("sb-1").F("brief"), "BASE: sprint/old", "the card keeps its base")
	})
}

// streamSetBaseStep is the command's step for stream set --base: the plan over the evidence
// the command read, all or none.
func streamSetBaseStep(req sprint.StreamSetBaseReq) store.Step {
	return store.Step{Verb: "stream set", Named: true, Args: store.ArgsOf(req), Load: []string{sprint.Work, sprint.Merge},
		Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.StreamSetBase(s, req) }}
}

// addSetBaseCards adds sb-1 and sb-2 (re-pointable) and sb-3 (a card the test drives to
// working); every brief names sprint/old and one PATHS file that exists at the live tip.
func addSetBaseCards(r *conflictRig, stream string) {
	r.t.Helper()
	r.must(store.AddStep(sprint.AddReq{Stream: stream, Cards: []sprint.CardAdd{
		{ID: stream + "-1", Brief: setBaseBrief("sprint/old", "internal/x/*.go")},
		{ID: stream + "-2", Brief: setBaseBrief("sprint/old", "internal/x/*.go")},
		{ID: stream + "-3", Brief: setBaseBrief("sprint/old", "internal/x/*.go")},
	}}))
}

// takeSetBaseCard deals the ready card and takes it, so the primary is working (dealt).
func takeSetBaseCard(r *conflictRig, id string) {
	r.t.Helper()
	if r.snap().Work.Card(id).Col == sprint.Ready {
		r.must(dealStep(sprint.DealReq{Sel: sprint.Sel{IDs: []string{id}}}))
	}
	s := r.snap()
	wc := s.Fleet.Card(s.Work.Card(id).F("work"))
	require.NotNil(r.t, wc, id)
	r.must(store.TakeStep(sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}}))
}

// setBaseBrief is a card brief that names its base and PATHS (the check add runs reads
// these at the base tip).
func setBaseBrief(base, paths string) string {
	return "c: the work tier: flash\nREPO: mas-bandwidth/nova-tools\nBASE: " + base + "\nPATHS: " + paths + "\n\nThe task.\n"
}

// setBaseTwin is a temporary origin and a clone of it, holding a sprint/live branch whose
// tip has internal/x/x.go: the base the command reads and checks. It returns the clone.
func setBaseTwin(t *testing.T) (clone string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	work := filepath.Join(root, "work")
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = testgit.Environ("GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(work, "internal/x"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(work, "internal/x/x.go"), []byte("package x\n"), 0o644))
	run(work, "init", "-q", "-b", "sprint/old")
	run(work, "add", ".")
	run(work, "commit", "-q", "-m", "base")
	run(work, "branch", "sprint/live")
	run(root, "clone", "-q", "--bare", work, origin)
	clone = filepath.Join(root, "clone")
	run(root, "clone", "-q", origin, clone)
	return clone
}
