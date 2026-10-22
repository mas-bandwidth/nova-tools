package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStepReadsGrantsTheToolchainFromTheBenchHomeWhenProcessHomeIsTheSlot is the script
// card's go vet exit 126: the step process's HOME is the slot, and the wall must still
// grant the bench sdk the shim execs. The home is injected (the verb passes
// benchPasswdHome), so the test never sets HOME and runs in parallel.
func TestStepReadsGrantsTheToolchainFromTheBenchHomeWhenProcessHomeIsTheSlot(t *testing.T) {
	t.Parallel()
	slot := t.TempDir() // the process HOME of a script step; stepReads takes the bench home as an argument and never reads it
	home := t.TempDir()
	sdk := filepath.Join(home, "sdk")
	require.NoError(t, os.MkdirAll(sdk, 0o755))
	real, err := filepath.EvalSymlinks(sdk)
	require.NoError(t, err)
	reads := stepReads(t.TempDir(), filepath.Join(t.TempDir(), "bin"), home, "")
	assert.True(t, hasFlagPair(reads, "--read", real), "the step wall grants the bench sdk: %s", strings.Join(reads, " "))
	assert.False(t, hasFlagPair(reads, "--read", filepath.Join(slot, "sdk")), "the slot home is not a toolchain root: %s", strings.Join(reads, " "))
}

// fakeGoroot lays out a Go distribution's shape at dir: bin/go and pkg/tool, nothing run.
func fakeGoroot(t *testing.T, dir string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "bin"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "pkg", "tool"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bin", "go"), []byte("#!/bin/sh\n"), 0o755))
	real, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	return real
}

// TestStepReadsGrantsTheGoToolchainTheStepRunsInsideTheModuleCache is nightly-slow run
// 39812741141's `go vet` exit 2: the go command switched toolchains (GOTOOLCHAIN), so the
// go a step runs is golang.org/toolchain@... under the module cache, a root the wall reads
// and never executes. The wall ran that go (its command's own directory) but not vet in its
// pkg/tool. The wall grants the step's go its whole toolchain, and only that toolchain:
// the rest of the module cache stays read-only and never executable.
func TestStepReadsGrantsTheGoToolchainTheStepRunsInsideTheModuleCache(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	mod := filepath.Join(home, "go", "pkg", "mod")
	goroot := fakeGoroot(t, filepath.Join(mod, "golang.org", "toolchain@v0.0.1-go1.26.6.linux-amd64"))
	realMod, err := filepath.EvalSymlinks(mod)
	require.NoError(t, err)
	reads := stepReads(t.TempDir(), filepath.Join(t.TempDir(), "bin"), home, goroot)
	assert.True(t, hasFlagPair(reads, "--read", goroot), "the step's go toolchain is granted with execute: %s", strings.Join(reads, " "))
	assert.True(t, hasFlagPair(reads, "--read-noexec", realMod), "the module cache stays never executable: %s", strings.Join(reads, " "))
	assert.False(t, hasFlagPair(reads, "--read", realMod), "the module cache is not granted execute: %s", strings.Join(reads, " "))
}

// TestStepReadsNamesNoGorootAlreadyUnderAToolchainRootOrNotAGoroot: a toolchain under the
// bench sdk is already granted, and a directory without a Go distribution's shape (bin/go
// and pkg/tool), a home or a directory holding one, is never granted execute on the strength
// of a variable.
func TestStepReadsNamesNoGorootAlreadyUnderAToolchainRootOrNotAGoroot(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	sdkGo := fakeGoroot(t, filepath.Join(home, "sdk", "go1.26.10"))
	reads := stepReads(t.TempDir(), filepath.Join(t.TempDir(), "bin"), home, sdkGo)
	assert.False(t, hasFlagPair(reads, "--read", sdkGo), "a toolchain under the sdk is not named twice: %s", strings.Join(reads, " "))
	notGo := t.TempDir()
	reads = stepReads(t.TempDir(), filepath.Join(t.TempDir(), "bin"), home, notGo)
	assert.False(t, hasFlagPair(reads, "--read", notGo), "a directory that is not a GOROOT is not granted: %s", strings.Join(reads, " "))
	homeGo := fakeGoroot(t, home)
	reads = stepReads(t.TempDir(), filepath.Join(t.TempDir(), "bin"), home, homeGo)
	assert.False(t, hasFlagPair(reads, "--read", homeGo), "a home directory is never a root, even shaped as a GOROOT: %s", strings.Join(reads, " "))
	reads = stepReads(t.TempDir(), filepath.Join(t.TempDir(), "bin"), home, filepath.Dir(homeGo))
	assert.False(t, hasFlagPair(reads, "--read", filepath.Dir(homeGo)), "a directory holding the home is never a root: %s", strings.Join(reads, " "))
	reads = stepReads(t.TempDir(), filepath.Join(t.TempDir(), "bin"), home, "relative/goroot")
	assert.False(t, hasFlagPair(reads, "--read", "relative/goroot"), "a relative GOROOT is not granted: %s", strings.Join(reads, " "))
}

// TestStepGorootIsTheGorootVariableElseTheGoOnPath: the toolchain a step's go runs is
// GOROOT when the caller sets it (the go command sets it when it switches toolchains), else
// the distribution the go on PATH sits in; none when there is no go.
func TestStepGorootIsTheGorootVariableElseTheGoOnPath(t *testing.T) {
	t.Parallel()
	root := fakeGoroot(t, filepath.Join(t.TempDir(), "go"))
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	found := func(string) (string, error) { return filepath.Join(root, "bin", "go"), nil }
	missing := func(string) (string, error) { return "", os.ErrNotExist }
	assert.Equal(t, "/the/variable", stepGoroot(env(map[string]string{"GOROOT": "/the/variable"}), found))
	assert.Equal(t, root, stepGoroot(env(nil), found))
	assert.Equal(t, "", stepGoroot(env(nil), missing))
}

// treeSteps is a tree under STEP 2 of lintGoodCard: two model steps (docs/SPEC-SPRINT.md, a
// card is a tree of steps).
const treeSteps = "STEP 2. Read docs/SPEC-SWARM.md first.\n" +
	"STEP 2.1. Rename Foo to Bar.\n  PATHS: pkg/swarm/a.go\n  COMMIT: swarm: rename Foo to Bar\n  VERDICT: ok when go vet passes\n" +
	"STEP 2.2. Write the doc line.\n  PATHS: docs/SPEC-SWARM.md\n  COMMIT: docs: say Bar\n  VERDICT: ok when the doc says Bar"

// regexStep is a regex script step over a.txt whose POST is the hash of want.
func regexStep(num, re, want string) string {
	h := sha256.Sum256([]byte(want))
	return "STEP " + num + ". Rename.\n  PATHS: a.txt\n  COMMIT: step " + num + "\n  VERDICT: the hash holds\n  SCRIPT: regex\n  ```\n  " + re + "\n  ```\n  POST: sha256 a.txt " + hex.EncodeToString(h[:]) + "\n"
}

// stepRepo is a checkout of one commit holding a.txt.
func stepRepo(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	require.NoError(t, os.MkdirAll(repo, 0o755))
	runGit(t, repo, "init", "-q", "-b", "work")
	runGit(t, repo, "config", "user.name", "step test")
	runGit(t, repo, "config", "user.email", "step@example.com")
	require.NoError(t, os.WriteFile(filepath.Join(repo, "a.txt"), []byte("Foo and Foo\n"), 0o644))
	runGit(t, repo, "add", "a.txt")
	runGit(t, repo, "commit", "-q", "-m", "base")
	return repo
}

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
	mixed := strings.Replace(tree, "  VERDICT: ok when go vet passes\n", "  VERDICT: ok when go vet passes\n  SCRIPT: regex\n  ```\n  s/Foo/Bar/\n  ```\n  POST: exit0 go vet ./pkg/swarm/\n", 1)
	exit, stdout, _ = runSwarm(t, "lint", "--card", writeLintCard(t, "mixed.card", mixed))
	require.Equal(t, 1, exit, stdout)
	assert.Contains(t, stdout, "LINT DRIFT card=mixed.card script-step:", "a script step beside a model step is refused")
}

func TestTheStepVerbRunsAScriptCardInItsWallAndPrintsTheRemainder(t *testing.T) {
	t.Parallel()
	repo := stepRepo(t)
	card := "RESULT: s1-9 sha=0123456789ab\nBASE: dev\nPATHS: a.txt\n\nSTEP 1. Enter with cd repo.\n" + regexStep("2", "s|Foo|Bar|", "Bar and Bar\n") + "STEP 3. End as JOB.md says; write RESULT.md.\n"
	cardFile := writeLintCard(t, "s1-9.md", card)
	result := filepath.Join(t.TempDir(), "RESULT.md")
	// the wall here is a recorder: it logs its argv and runs the command after `--`, so what
	// the step hands its wall is asserted without a kernel wall in a unit test
	log := filepath.Join(t.TempDir(), "wall.log")
	wall := filepath.Join(t.TempDir(), "wall")
	require.NoError(t, os.WriteFile(wall, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> '"+log+"'\nwhile [ \"$1\" != -- ]; do shift; done\nshift\nexec \"$@\"\n"), 0o755))

	cwd, err := os.Getwd()
	require.NoError(t, err)
	rel, err := filepath.Rel(cwd, wall)
	require.NoError(t, err)
	// a relative wall path: every command runs from the checkout, so the step makes it absolute
	exit, stdout, stderr := runSwarm(t, "step", "--card", cardFile, "--dir", repo, "--work", t.TempDir(), "--result", result, "--sandbox", rel)
	require.Equal(t, 0, exit, "stdout: %s\nstderr: %s", stdout, stderr)
	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	assert.Equal(t, "STEP OK step 2: ok "+head+" post holds\n", stdout)
	assert.Equal(t, "step 2\n", runGit(t, repo, "log", "-1", "--format=%s"), "the step's commit line is its commit")
	b, err := os.ReadFile(result)
	require.NoError(t, err)
	assert.Contains(t, string(b), "head: "+head+"\nbranch: work\nverdict: ok\n")
	assert.Contains(t, string(b), "## Body\n\nstep 2: ok "+head+" post holds\n")
	walled, err := os.ReadFile(log)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(walled)), "\n")
	require.NotEmpty(t, lines, "git ran in the step's wall")
	for _, l := range lines {
		assert.Contains(t, l, " --net-deny ", "every walled command denies the network: %s", l)
		assert.Contains(t, l, "--write "+repo+" ", "the checkout is a write: %s", l)
		assert.Equal(t, 2, strings.Count(l, "--write "), "the private temp and the checkout are the only writes: %s", l)
	}

	land := strings.Repeat("c", 40)
	exit, stdout, _ = runSwarm(t, "step", "--card", cardFile, "--remainder", "s1-9", "--from", "2", "--land", land)
	require.Equal(t, 0, exit)
	assert.True(t, strings.HasPrefix(stdout, "RESULT: s1-9 sha="+land[:12]+"\nFrom: STEP 2\nNeeds: s1-9\nBASE: dev@"+land+"\nPATHS: a.txt\n"), stdout)
}

func TestTheStepVerbStopsAtAFailedStep(t *testing.T) {
	t.Parallel()
	repo := stepRepo(t)
	card := "RESULT: s1-8 sha=0123456789ab\nPATHS: a.txt\n\nSTEP 1. Enter with cd repo.\n" +
		regexStep("2", "s|Foo|Bar|", "not what the regex makes\n") + regexStep("3", "s|and|or|", "Bar or Bar\n") + "STEP 4. End as JOB.md says.\n"
	result := filepath.Join(t.TempDir(), "RESULT.md")
	exit, stdout, _ := runSwarm(t, "step", "--card", writeLintCard(t, "s1-8.md", card), "--dir", repo, "--work", t.TempDir(), "--result", result, "--no-wall")
	require.Equal(t, 1, exit, stdout)
	assert.Contains(t, stdout, "STEP NOTE no wall (--no-wall): the card's programs run unconfined")
	assert.Contains(t, stdout, "STEP FAILED step 2: broken - post: sha256 a.txt is ")
	assert.NotContains(t, stdout, "step 3", "the walk stops at the failed step")
	assert.Equal(t, "base\n", runGit(t, repo, "log", "-1", "--format=%s"), "nothing committed")
	b, err := os.ReadFile(filepath.Join(repo, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "Bar and Bar\n", string(b), "step 3's regex never ran")
	r, err := os.ReadFile(result)
	require.NoError(t, err)
	assert.Contains(t, string(r), "verdict: not-done\n")
}

func TestTheStepVerbRefusesAProgramOutsideAWallItWasNotToldToDrop(t *testing.T) {
	t.Parallel()
	card := writeLintCard(t, "c.md", "RESULT: c sha=0123456789ab\nPATHS: a.txt\n\nSTEP 1. Enter.\n"+regexStep("2", "s|a|b|", "b")+"STEP 3. End.\n")
	exit, _, stderr := runSwarm(t, "step", "--card", card, "--dir", t.TempDir(), "--no-wall", "--sandbox", "/x/nova-sandbox")
	require.Equal(t, 2, exit)
	assert.Contains(t, stderr, "--no-wall and --sandbox name two different walls")
	model := writeLintCard(t, "m.md", "RESULT: m sha=0123456789ab\n\nSTEP 1. Enter.\nSTEP 2. x\n  PATHS: a.txt\n  COMMIT: c\n  VERDICT: v\n")
	exit, _, stderr = runSwarm(t, "step", "--card", model, "--dir", t.TempDir(), "--no-wall")
	require.Equal(t, 2, exit)
	assert.Contains(t, stderr, "a card whose every work step is a script step")
}

// A script card's executor takes the child's place outside the child's wall, and is niced as
// a walled child is: decided from the run's wall before native clears it for the executor.
func TestAScriptCardIsNicedOnDarwin(t *testing.T) {
	t.Parallel()
	step := []string{"/bin/nova-swarm", "step", "--card", "c.md"}
	cwd, err := os.Getwd()
	require.NoError(t, err)
	for _, c := range []struct {
		name, goos, wall string
		niced            bool
		tail             []string
	}{
		{"darwin, walled", "darwin", "/w/nova-sandbox", true, []string{"--sandbox", "/w/nova-sandbox"}},
		{"darwin, a relative wall made absolute", "darwin", "w/nova-sandbox", true, []string{"--sandbox", filepath.Join(cwd, "w", "nova-sandbox")}},
		{"darwin, --no-wall", "darwin", "", false, []string{"--no-wall"}},
		{"linux, walled", "linux", "/w/nova-sandbox", false, []string{"--sandbox", "/w/nova-sandbox"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			path, argv, niced, err := nativeStepLaunch(c.goos, step, c.wall)
			require.NoError(t, err)
			assert.Equal(t, "/bin/nova-swarm", path)
			assert.Equal(t, append([]string{"step", "--card", "c.md"}, c.tail...), argv)
			assert.Equal(t, c.niced, niced)
		})
	}
	assert.Equal(t, []string{"/bin/nova-swarm", "step", "--card", "c.md"}, step, "the argv handed in is not written through")
}

// --dry-run prints each step the walk would run and the wall, and writes nothing.
func TestTheStepVerbsDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	repo := stepRepo(t)
	card := writeLintCard(t, "d.md", "RESULT: d sha=0123456789ab\nPATHS: a.txt\n\nSTEP 1. Enter.\n"+regexStep("2", "s|Foo|Bar|", "Bar and Bar\n")+"STEP 3. End.\n")
	exit, stdout, stderr := runSwarm(t, "step", "--card", card, "--dir", repo, "--no-wall", "--dry-run")
	require.Equal(t, 0, exit, stderr)
	assert.Equal(t, "STEP PLAN step=2 lang=regex paths=a.txt posts=1 wall=none\n", stdout)
	assert.Equal(t, "base\n", runGit(t, repo, "log", "-1", "--format=%s"), "nothing committed")
	b, err := os.ReadFile(filepath.Join(repo, "a.txt"))
	require.NoError(t, err)
	assert.Equal(t, "Foo and Foo\n", string(b), "nothing run")
}
