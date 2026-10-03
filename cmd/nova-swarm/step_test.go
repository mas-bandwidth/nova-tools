package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// treeSteps is a tree under STEP 2 of lintGoodCard: a regex script step and a model step
// (docs/SPEC-SPRINT.md, a card is a tree of steps).
const treeSteps = "STEP 2. Read docs/SPEC-SWARM.md first.\n" +
	"STEP 2.1. Rename Foo to Bar.\n  PATHS: internal/swarm/a.go\n  COMMIT: swarm: rename Foo to Bar\n  VERDICT: ok when go vet passes\n  SCRIPT: regex\n  ```regex\n  s/Foo/Bar/\n  ```\n  POST: exit0 go vet ./internal/swarm/\n" +
	"STEP 2.2. Write the doc line.\n  PATHS: docs/SPEC-SWARM.md\n  COMMIT: docs: say Bar\n  VERDICT: ok when the doc says Bar"

func TestATreeCardLintsCleanAndItsDefectsDrift(t *testing.T) {
	t.Parallel()
	tree := strings.Replace(lintGoodCard(), "STEP 2. Read docs/SPEC-SWARM.md first.", treeSteps, 1)
	exit, stdout, _ := runSwarm(t, "lint", "--card", writeLintCard(t, "tree.card", tree))
	require.Equal(t, 0, exit, "a tree card whose steps are whole lints clean: %s", stdout)
	gap := strings.Replace(tree, "STEP 2.2.", "STEP 2.3.", 1)
	exit, stdout, _ = runSwarm(t, "lint", "--card", writeLintCard(t, "gap.card", gap))
	require.Equal(t, 1, exit, stdout)
	assert.Contains(t, stdout, "LINT DRIFT card=gap.card steps-nested:")
	assert.NotContains(t, stdout, "steps-numbered", "the top-level numbering is unchanged by the children")
	bash := strings.Replace(tree, "SCRIPT: regex", "SCRIPT: bash", 1)
	exit, stdout, _ = runSwarm(t, "lint", "--card", writeLintCard(t, "bash.card", bash))
	require.Equal(t, 1, exit, stdout)
	assert.Contains(t, stdout, "LINT DRIFT card=bash.card script-step:")
}

func TestTheStepVerbRunsAScriptCardAndPrintsTheRemainder(t *testing.T) {
	t.Parallel()
	repo := filepath.Join(t.TempDir(), "repo")
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	require.NoError(t, os.MkdirAll(repo, 0o755))
	git("init", "-q", "-b", "work")
	git("config", "user.name", "step test")
	git("config", "user.email", "step@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.txt"), []byte("Foo and Foo\n"), 0o644))
	git("add", "a.txt")
	git("commit", "-q", "-m", "base")
	want := sha256.Sum256([]byte("Bar and Bar\n"))
	card := "RESULT: s1-9 sha=0123456789ab\nPATHS: a.txt\n\nSTEP 1. Enter with cd repo.\n" +
		"STEP 2. Rename.\n  PATHS: a.txt\n  COMMIT: rename Foo to Bar\n  VERDICT: the hash holds\n  SCRIPT: regex\n  ```\n  s|Foo|Bar|\n  ```\n  POST: sha256 a.txt " + hex.EncodeToString(want[:]) + "\n" +
		"STEP 3. End as JOB.md says; write RESULT.md.\n"
	cardFile := writeLintCard(t, "s1-9.md", card)
	result := filepath.Join(t.TempDir(), "RESULT.md")

	exit, stdout, stderr := runSwarm(t, "step", "--card", cardFile, "--dir", repo, "--work", t.TempDir(), "--result", result, "--steps", "all")
	require.Equal(t, 0, exit, "stdout: %s\nstderr: %s", stdout, stderr)
	head := git("rev-parse", "HEAD")
	assert.Equal(t, "STEP OK step 2: ok "+head+" post holds\n", stdout)
	assert.Equal(t, "rename Foo to Bar", git("log", "-1", "--format=%s"), "the step's commit line is its commit")
	b, err := os.ReadFile(result)
	require.NoError(t, err)
	assert.Contains(t, string(b), "head: "+head+"\nbranch: work\nverdict: ok\n")
	assert.Contains(t, string(b), "## Body\n\nstep 2: ok "+head+" post holds\n")

	exit, stdout, _ = runSwarm(t, "step", "--card", cardFile, "--remainder", "s1-9", "--from", "2")
	require.Equal(t, 0, exit)
	assert.True(t, strings.HasPrefix(stdout, "RESULT: s1-9 sha=0123456789ab\nFrom: STEP 2\nNeeds: s1-9\nPATHS: a.txt\n"), stdout)
}
