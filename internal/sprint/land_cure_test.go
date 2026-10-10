package sprint_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The base cure on the twin store and a twin repository (docs/SPEC-SPRINT.md section 8, the
// base-gate rule; 2026-10-05 7:30 PM: the fix card for a red base sat merging, refused
// because the base was red, and the coordinator fast-forwarded the base by hand). The gate
// is red while the tree's gate.txt says red.

// cureGate is the twin's tree gate: red while gate.txt says so; the trees it saw.
func cureGate(seen *[]string) func(context.Context, string) string {
	return func(_ context.Context, dir string) string {
		b, err := os.ReadFile(filepath.Join(dir, "gate.txt"))
		if err != nil {
			return err.Error()
		}
		*seen = append(*seen, strings.TrimSpace(string(b)))
		if strings.TrimSpace(string(b)) == "red" {
			return "go test ./internal/x/: exit status 1: --- FAIL: TestX"
		}
		return ""
	}
}

// branchHead is a developer's commit of file on a new branch cut from the base, pushed:
// the card's head.
func (r *syncRepo) branchHead(branch, file, content string) string {
	r.t.Helper()
	r.syncGit(r.dev, "fetch", "-q", "origin")
	r.syncGit(r.dev, "checkout", "-q", "-B", branch, "origin/"+syncBase)
	require.NoError(r.t, os.WriteFile(filepath.Join(r.dev, file), []byte(content), 0o644))
	r.syncGit(r.dev, "add", file)
	r.syncGit(r.dev, "commit", "-q", "-m", branch)
	r.syncGit(r.dev, "push", "-q", "origin", "HEAD:refs/heads/"+branch)
	return r.tip(branch)
}

func TestALanderLandsTheHeadThatCuresARedBase(t *testing.T) {
	t.Parallel()
	repo := newSyncRepo(t)
	// the base turns red; two cards queue onto it: s1-1 a casualty (its work is elsewhere,
	// the tree stays red), s1-2 the fix
	repo.commit(syncBase, "gate.txt", "red\n", "the base turns red")
	casualty := repo.branchHead("card/s1-1", "other.go", "package o\n")
	fix := repo.branchHead("card/s1-2", "gate.txt", "green\n")
	baseTip := repo.tip(syncBase)
	repo.syncGit(repo.land, "fetch", "-q", "origin")
	repo.syncGit(repo.land, "checkout", "-q", "--detach", "origin/"+syncBase)

	var seen []string
	gate := cureGate(&seen)
	require.NotEmpty(t, gate(t.Context(), repo.land), "the base is red at its tip")
	heads := []sprint.CureHead{{ID: "s1-1", Head: casualty}, {ID: "s1-2", Head: fix}}
	req := sprint.BaseCureReq{RepoDir: repo.land, Base: baseTip, Heads: heads, Env: repo.env, Gate: gate}

	// the lander checks each candidate's tree, not only the base's: the casualty is red
	// merged, the fix is green, and the fix is the cure
	cure, err := sprint.FindBaseCure(t.Context(), req)
	require.NoError(t, err)
	require.True(t, cure.Found())
	assert.Equal(t, "s1-2", cure.ID)
	assert.Equal(t, fix, cure.Head)
	assert.Equal(t, []string{"red", "red", "green"}, seen, "the base, then each candidate merged onto it, through the same gate")
	require.Len(t, cure.Tried, 1)
	assert.Equal(t, "s1-1", cure.Tried[0].ID)
	assert.Contains(t, cure.Tried[0].Why, "fails its gate too")
	assert.Equal(t, cure.Tip, repo.syncGit(repo.land, "rev-parse", "HEAD"), "the clone is left at the cure's merge")

	// it lands: the merge pushed onto the base, the base green again
	repo.syncGit(repo.land, "push", "-q", "origin", "HEAD:refs/heads/"+syncBase)
	assert.True(t, repo.has(syncBase, fix), "the base holds the fix")
	assert.Equal(t, "green", strings.TrimSpace(repo.syncGit(repo.origin, "show", syncBase+":gate.txt")))

	// on the store: the stream was refused once on the red base, then the cure lands by
	// name ahead of the casualty, its landing note naming the fix, and the stream goes on
	r := newConflictRig(t)
	for _, id := range []string{"s1-1", "s1-2"} {
		r.toMerging(id)
	}
	why := "go test ./internal/x/: exit status 1: --- FAIL: TestX"
	r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", Base: syncBase, BaseRefused: why}))
	require.Equal(t, "1", r.snap().StreamCtl("s1").F(sprint.FieldBaseGateRefused))
	note := cure.Note(baseTip, why)
	r.must(store.MergeStep(sprint.MergeReq{Stream: "s1", Cards: []string{cure.ID}, Resolved: map[string]string{cure.ID: note}}))
	s := r.snap()
	assert.Equal(t, sprint.Landed, s.Work.Card("s1-2").Col, "the cure landed")
	assert.Equal(t, sprint.Merging, s.Work.Card("s1-1").Col, "the casualty waits for the next batch, on the green base")
	assert.Contains(t, s.Merge.Card("s1-2").F("note"), "landed first as the base fix")
	assert.Contains(t, s.Merge.Card("s1-2").F("note"), baseTip[:12])
	assert.NotEqual(t, sprint.StreamStopped, s.StreamCtl("s1").F("state"), "the stream resumes")
	assert.Empty(t, s.StreamCtl("s1").F(sprint.FieldBaseGateRefused), "a pass that merges clears the base-gate count")

	// a candidate tried on this base is not gated again
	seen = nil
	repo.syncGit(repo.land, "reset", "-q", "--hard", baseTip)
	req.Tried = func(h sprint.CureHead) bool { return h.ID == "s1-1" }
	cure, err = sprint.FindBaseCure(t.Context(), req)
	require.NoError(t, err)
	assert.Equal(t, "s1-2", cure.ID)
	assert.Equal(t, []string{"green"}, seen)
}

func TestARedBaseWithNoCuringHeadIsLeftAsItWas(t *testing.T) {
	t.Parallel()
	repo := newSyncRepo(t)
	repo.commit(syncBase, "gate.txt", "red\n", "the base turns red")
	casualty := repo.branchHead("card/s1-1", "other.go", "package o\n")
	// a head that does not merge onto the base is no cure either
	conflicted := repo.branchHead("card/s1-3", "README.md", "# theirs\n")
	repo.commit(syncBase, "README.md", "# ours\n", "the base edits the readme")
	baseTip := repo.tip(syncBase)
	repo.syncGit(repo.land, "fetch", "-q", "origin")
	repo.syncGit(repo.land, "checkout", "-q", "--detach", "origin/"+syncBase)

	var seen []string
	cure, err := sprint.FindBaseCure(t.Context(), sprint.BaseCureReq{RepoDir: repo.land, Base: baseTip, Env: repo.env, Gate: cureGate(&seen),
		Heads: []sprint.CureHead{{ID: "s1-1", Head: casualty}, {ID: "s1-3", Head: conflicted}}})
	require.NoError(t, err)
	assert.False(t, cure.Found(), "no queued head cures the base: the stream stops on it as before")
	require.Len(t, cure.Tried, 2)
	assert.Contains(t, cure.Tried[1].Why, "does not merge")
	assert.Equal(t, []string{"red"}, seen, "only the head that merged was gated")
	assert.Equal(t, baseTip, repo.syncGit(repo.land, "rev-parse", "HEAD"), "the clone is the base again")
	assert.Empty(t, repo.syncGit(repo.land, "status", "--porcelain"), "and clean")

	_, err = sprint.FindBaseCure(t.Context(), sprint.BaseCureReq{RepoDir: repo.land, Base: baseTip})
	require.ErrorContains(t, err, "no tree gate")
}
