package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// The stage wall scales with the machine: a member's --stage-wall (default 120s, the loop
// row's argv in nova-config) is every launch's native --stage-timeout (a 36-thread bench,
// 2026-10-02: a 2.3 GHz bench under load staged in 73-108 s median against a 120 s wall).
func TestAMembersStageWallIsEveryLaunchsStageTimeout(t *testing.T) {
	t.Parallel()
	r := argsRunner(t, "override/model", "999", 9*time.Second)
	args, _ := launched(t, r, member.Packet{Card: "c1", Kind: "work", Attempt: 1, Gen: 1, Branch: "work/c1"})
	assert.Equal(t, "2m0s", args["--stage-timeout"], "the default wall is native's own 120s, said")
	r = argsRunner(t, "override/model", "999", 9*time.Second)
	r.stageWall = 5 * time.Minute
	args, _ = launched(t, r, member.Packet{Card: "c2", Kind: "work", Attempt: 1, Gen: 1, Branch: "work/c2"})
	assert.Equal(t, "5m0s", args["--stage-timeout"])
}

// --stage-wall is a positive duration or whole seconds; anything else is refused before
// the member makes anything.
func TestMemberRefusesAStageWallThatIsNoBound(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"0", "-5s", "soon"} {
		root := t.TempDir()
		var out, errb bytes.Buffer
		code := run(append(memberFull(root), "--stage-wall", v), strings.NewReader(""), &out, &errb, time.Now())
		assert.Equal(t, 2, code, "--stage-wall %s: stderr %q", v, errb.String())
		assert.Contains(t, errb.String(), "--stage-wall", v)
		_, err := os.Stat(filepath.Join(root, "slots"))
		assert.True(t, os.IsNotExist(err), "--stage-wall %s: a directory was made before the refusal", v)
	}
}

// A checkout that borrows the bench mirror's objects needs them inside the wall: the
// mirror's object directory is a read, never a write and never an exec.
func TestTheWallReadsTheObjectsACheckoutBorrows(t *testing.T) {
	t.Parallel()
	bin := nativeHarness(t)
	_, slot := aSlot(t)
	jobDir := filepath.Join(slot, "jobs", "a-label")
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	objects := filepath.Join(t.TempDir(), "mirror.git", "objects")
	argv := nativeSandboxArgv([]string{bin}, nativeRunConfig{slotDir: slot, borrowed: objects}, filepath.Join(slot, "data"), jobDir, filepath.Join(slot, "tmp", "a-label"))
	assert.True(t, hasFlagPair(argv, "--read-noexec", objects), "the wall reads the borrowed objects:\n%s", strings.Join(argv, " "))
	assert.False(t, hasFlagPair(argv, "--write", objects), "the borrowed objects are never a write")
	argv = nativeSandboxArgv([]string{bin}, nativeRunConfig{slotDir: slot}, filepath.Join(slot, "data"), jobDir, filepath.Join(slot, "tmp", "a-label"))
	assert.False(t, hasFlagPair(argv, "--read-noexec", objects), "a checkout that borrows nothing is handed nothing")
}

// A read's JOB.md is gated on the packages its diff touches, read off the staged checkout
// (git diff --name-only <start>..HEAD), and names the machine's shared build cache.
func TestAReadsGateIsReadOffItsDiff(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	seed, origin := filepath.Join(root, "seed"), filepath.Join(root, "origin.git")
	runGit(t, "", "init", "-q", "-b", "main", "--", seed)
	for rel, body := range map[string]string{
		"go.mod":                     "module example.com/m\n\ngo 1.26\n",
		"internal/sprint/x.go":       "package sprint\n",
		"internal/swarm/s.go":        "package swarm\n",
		"internal/swarm/doc_test.go": "package swarm\n\nvar doc = \"../../docs/SPEC-FIXTURE.md\"\n",
		"internal/ci/ci_test.go":     "package ci\n\nfunc TestEverything(t *testing.T) {}\n",
		"docs/SPEC-FIXTURE.md":       "# swarm\n",
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(seed, rel)), 0o755))
		write(t, filepath.Join(seed, rel), body)
	}
	gitAs(t, seed, "add", ".")
	gitAs(t, seed, "commit", "-q", "-m", "base")
	runGit(t, "", "clone", "-q", "--bare", "--", seed, origin)
	w := filepath.Join(root, "w")
	runGit(t, "", "clone", "-q", "--", origin, w)
	write(t, filepath.Join(w, "internal", "sprint", "x.go"), "package sprint\n\n// changed\n")
	write(t, filepath.Join(w, "docs", "SPEC-FIXTURE.md"), "# swarm, changed\n")
	gitAs(t, w, "commit", "-q", "-am", "work")
	head := gitAs(t, w, "rev-parse", "HEAD")
	runGit(t, w, "push", "-q", "origin", "HEAD:refs/heads/sprint/w")

	slot := filepath.Join(root, "slot")
	job := filepath.Join(slot, "jobs", "w.r1")
	require.NoError(t, os.MkdirAll(job, 0o755))
	fr := &cardcontract.Frame{Kind: "read", Card: "w.r1", Attempt: 1, Model: "fake/model-x", Repo: origin,
		BaseRef: "main", ReviewBase: "main", StageSha: head, Branch: "sprint/w"}
	st, err := swarm.StageCard(swarm.StageOptions{TargetDir: filepath.Join(job, swarm.JobRepo), JobDir: job,
		BenchHome: filepath.Join(root, "no-bench"), Base: &swarm.CardBase{Repo: origin, Sha: head, Ref: "main", Named: origin}, Branch: fr.Branch})
	require.NoError(t, err)
	require.NoError(t, installFrame(nativeRunConfig{slotDir: slot, root: root, model: fr.Model, frame: fr}, job, st.BaseSha))
	text, err := os.ReadFile(filepath.Join(job, cardcontract.JobName))
	require.NoError(t, err)
	assert.Contains(t, string(text), "    nice -n 19 go test -count=1 -timeout 600s ./internal/sprint ./internal/swarm\n")
	assert.NotContains(t, string(text), "./internal/ci\n", "internal/ci is never run whole, and no test of it reads the doc")
	assert.Contains(t, string(text), "GOCACHE is "+filepath.Join(root, "cache", "go-build"))
}
