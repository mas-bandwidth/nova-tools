//go:build slow

// These tests are behind the `slow` build tag: the PR test jobs do not build them and
// .github/workflows/nightly-slow.yml does (#516, the two-minute rule -- a package's
// tests answer in a minute). Nothing here is skipped or weakened; it runs nightly, whole.

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcontract"
	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/gitrun"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
	"github.com/mas-bandwidth/nova-tools/pkg/testbin"
	"github.com/mas-bandwidth/nova-tools/pkg/typedrec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNativeRunKillsAtDeadline: a child that sleeps past the wall is killed by it, and the
// run records the deadline's own exit. The deadline is an event, not a wall clock: it fires
// through this run's own seam once the harness has written its argv, so the test proves the
// kill and not the machine's load.
func TestNativeRunKillsAtDeadline(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)

	// The deadline fires when the harness has started, which its argv records, and this run
	// alone receives it: the seam is a field on the configuration, so the test runs in
	// parallel with the others instead of swapping the package seam under them.
	fire := make(chan time.Time)
	quit := make(chan struct{})
	t.Cleanup(func() { close(quit) })
	go func() {
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			if _, err := os.Stat(filepath.Join(slot, "jobs", "lbl", "argv")); err == nil {
				close(fire)
				return
			}
			select {
			case <-quit:
				return
			case <-tick.C:
			}
		}
	}()

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "lbl",
		card: []byte("FAKE-SLEEP 60\n"), slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
		deadlineFn: func(time.Duration) (<-chan time.Time, func() bool) {
			return fire, func() bool { return true }
		},
	}, &errOut)
	require.Equal(t, 0, code, "a deadline kill is not a refusal, got exit %d:\n%s", code, errOut.String())
	require.Equal(t, -1, res.rc, "the deadline killed the child, and the run records rc=-1")
}

// SLOW: 25.2 s on bench-tier at dev 64b9bec48, over the five-second line.
// TestNativeSamplerNeverOverlapsAndNeverDelaysTheDeadline: rule 13d, "no sample starts while
// one is unanswered", and "a usage reader that never returns does not move the deadline, and
// the run still ends inside the bound the deadline's own test holds (issue #779)".
//
// THE READER THAT NEVER RETURNS is a `sqlite3` on PATH that sleeps past every bound. The
// sampler gives one read 5 seconds and abandons it; the card's deadline is 3 seconds and is
// the thing under test, so a sampler that could hold the ending open would show here as a
// run that outlived its own deadline.
func TestNativeSamplerNeverOverlapsAndNeverDelaysTheDeadline(t *testing.T) {
	windowsIsNotABench(t)
	needsSQLite(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	// A database must EXIST for the reader to be run at all: an absent one is an absence
	// and never a read.
	db := filepath.Join(slot, "data", "opencode", "opencode.db")
	require.NoError(t, os.MkdirAll(filepath.Dir(db), 0o755))
	cmd := exec.Command(swarm.SQLiteBinary, db)
	cmd.Stdin = strings.NewReader("CREATE TABLE message (id INTEGER PRIMARY KEY, data TEXT NOT NULL, time_created INTEGER NOT NULL);\n")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "building the fixture store:\n%s", out)
	// A `sqlite3` on PATH that never answers, ahead of the real one.
	slow := t.TempDir()
	stall := filepath.Join(slow, swarm.SQLiteBinary)
	require.NoError(t, testbin.WriteExecutable(stall, []byte("#!/bin/sh\nsleep 600\n"), 0o755))
	t.Setenv("PATH", slow+string(os.PathListSeparator)+os.Getenv("PATH"))

	card := filepath.Join(root, "card.md")
	require.NoError(t, os.WriteFile(card, []byte("a card\nFAKE-IGNORE-TERM\nFAKE-SLEEP 60\n"), 0o644))
	args := append(budgetNativeArgs(t, bin, card, slot, root, "50000"), "--usage-interval", "1s")
	for i := range args {
		if args[i] == "--deadline" {
			args[i+1] = "3s"
		}
	}
	// THE EVENT, NOT THE CLOCK. The reader sleeps ten minutes and the card's deadline is
	// three seconds. What is asserted is that the run RETURNS and that its DEADLINE is what
	// ended the card: `rc=-1` with NO `stopped=` field, which is the deadline's own shape and
	// not a sampler's. A sampler that could hold the ending open would not reach either
	// assertion at all -- the go test timeout is this repo's bound on a hang, and it is a
	// better one than a number written here, which is why the repo refuses the number.
	var stdout, stderr bytes.Buffer
	run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	line := nativeOKLine(t, stdout.String())
	got := fieldOf(line, "rc")
	require.Equal(t, "-1", got, "the DEADLINE ended this card, so the line prints rc=-1; got %q:\n%s", got, line)
	got = fieldOf(line, "stopped")
	require.Empty(t, got, "a reader that never answers is not three FAILED reads while the card still had time; the deadline ended it and the line carries no stopped= field, got %q:\n%s", got, line)
}

// SLOW: 11.1 s on bench-tier at dev 64b9bec48, over the five-second line.
// TestNativeFailedReadsEndNothingByThemselves: a reader that refuses every read, and one that
// refuses twice then answers, both end nothing; the child ends by itself.
//
// A READ THAT FAILS NEVER ENDS A CARD BY ITSELF (cmd/nova-swarm/nativesample.go,
// UnverifiableAfter): three failed reads in a row used to, and on 2026-10-03 one member ended
// 32 cards that way in three machine-wide bursts while its sqlite3 launches stalled. The
// budget is enforced against the last answer plus an extrapolation while reads fail, and an
// outage ends the card only past UnverifiableAfter with that figure at the ceiling
// (TestAnOutageEndsTheCardOnlyPastTheBoundWithTheExtrapolatedSpendAtTheCeiling).
func TestNativeFailedReadsEndNothingByThemselves(t *testing.T) {
	windowsIsNotABench(t)
	needsSQLite(t)
	bin := nativeHarness(t)
	for _, tc := range []struct {
		name     string
		failures int // how many refusals the reader gives before it answers
		wantRC   int
		wantStop string
		wantEnd  string
	}{
		{"every_read_refused_ends_nothing", 1000, 0, "", swarm.EndDone},
		{"twice_then_an_answer_ends_nothing", 2, 0, "", swarm.EndDone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, slot := aSlot(t)
			// A DATABASE MUST EXIST for a read to be attempted at all: an absent one is an
			// absence, and rule 13d keeps the two apart.
			db := filepath.Join(slot, "data", "opencode", "opencode.db")
			require.NoError(t, os.MkdirAll(filepath.Dir(db), 0o755))
			require.NoError(t, os.WriteFile(db, []byte("a database\n"), 0o644))
			// A reader that refuses its first `failures` calls and answers after that,
			// counting in a file of its own so the count survives across processes.
			dir := t.TempDir()
			counter := filepath.Join(dir, "calls")
			script := "#!/bin/sh\n" +
				"n=$(cat " + counter + " 2>/dev/null || echo 0)\n" +
				"n=$((n+1)); echo $n > " + counter + "\n" +
				"if [ \"$n\" -le " + strconv.Itoa(tc.failures) + " ]; then echo 'Error: file is not a database' >&2; exit 1; fi\n" +
				"exit 0\n"
			require.NoError(t, testbin.WriteExecutable(filepath.Join(dir, swarm.SQLiteBinary), []byte(script), 0o755))
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

			card := filepath.Join(root, "card.md")
			require.NoError(t, os.WriteFile(card, []byte("a card\nFAKE-SLEEP 8\nFAKE-FINDINGS 1\n"), 0o644))
			args := append(budgetNativeArgs(t, bin, card, slot, root, "100000"), "--usage-interval", "1s")
			for i := range args {
				if args[i] == "--deadline" {
					args[i+1] = "60s"
				}
			}
			var stdout, stderr strings.Builder
			rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
			require.Equal(t, tc.wantRC, rc, "this card exits %d, got %d\nstdout:\n%s\nstderr:\n%s", tc.wantRC, rc, stdout.String(), stderr.String())
			got := fieldOf(nativeOKLine(t, stdout.String()), "stopped")
			assert.Equal(t, tc.wantStop, got, "stopped= is %q, want %q:\n%s", got, tc.wantStop, stdout.String())
			head, rows := usageRows(t, filepath.Join(slot, "jobs", "lbl"))
			got = cell(t, head, rows[len(rows)-1], "end")
			assert.Equal(t, tc.wantEnd, got, "the last row carries end=%s, got %q", tc.wantEnd, got)
		})
	}
}

// #632: `template` must print the six typed card templates nova-pulse `cut` reads from a
// templates directory (read, fix, text, replay, drift, tone) plus models.tsv, so a templates
// dir can be built from the tool instead of copied out of cmd/nova-pulse/testdata.
func TestTemplatePrintsThePulseCardTemplates(t *testing.T) {
	t.Parallel()
	b := newBench(t)
	for _, name := range []string{"read", "fix", "text", "replay", "drift", "tone", "models.tsv"} {
		exit, stdout, stderr := b.swarm("template", "--name", name)
		require.Equal(t, 0, exit, "`template --name %s` exited %d; nova-pulse cut needs the six typed templates and models.tsv:\n%s%s", name, exit, stdout, stderr)
		require.NotEqual(t, "", strings.TrimSpace(stdout), "`template --name %s` printed nothing", name)
	}
	// read is a text-only card and must carry the no-build line and the RESULT contract.
	exit, stdout, stderr := b.swarm("template", "--name", "read")
	require.Equal(t, 0, exit, "`template --name read` exited %d:\n%s%s", exit, stdout, stderr)
	mustContain(t, "the read card", stdout, "RESULT <label> sha=<sha12>")
	mustContain(t, "the read card", stdout, "Do not run go build, go test or any toolchain")
}

func TestNativeDrainDeliversPoolIdentityToChild(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a native execution")
	}

	root, slot := aSlot(t)
	write(t, filepath.Join(root, "identity.tsv"),
		"owner\tname\temail\npool-owner\tNative Drain Worker\tdrain-worker@example.com\n")

	require.NoError(t, buildShared())
	bin := builtHarness
	cardPath := filepath.Join(root, "card.md")
	write(t, cardPath, "native drain card\nFAKE-GIT-COMMIT\nFAKE-FINDINGS 0\n")

	// Hostile bench git config with a ghost user in the parent environment.
	benchHome := t.TempDir()
	benchConfig := filepath.Join(benchHome, ".gitconfig")
	write(t, benchConfig, "[user]\n\tname = Hostile Ghost\n\temail = ghost@example.com\n")

	origHome := os.Getenv("HOME")
	origGitConfig := os.Getenv("GIT_CONFIG_GLOBAL")
	defer func() {
		os.Setenv("HOME", origHome)
		if origGitConfig != "" {
			os.Setenv("GIT_CONFIG_GLOBAL", origGitConfig)
		} else {
			os.Unsetenv("GIT_CONFIG_GLOBAL")
		}
	}()
	os.Setenv("HOME", benchHome)
	os.Setenv("GIT_CONFIG_GLOBAL", benchConfig)

	label := "drain-task-1"
	var stdout, stderr bytes.Buffer
	rc := run([]string{
		"native",
		"--tokens", "unmetered",
		"--slots-store", nativeStore(t),
		"--owner", "fake-1",
		"--harness", bin,
		"--model", "fake/fake-model",
		"--label", label,
		"--card", cardPath,
		"--slot", slot,
		"--root", root,
		"--deadline", "30s",
		"--no-wall",
	}, strings.NewReader(""), &stdout, &stderr, time.Now())

	require.Equal(t, 0, rc, "native run exit = %d, want 0;\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())

	jobDir := filepath.Join(slot, "jobs", label)
	commitIdentityPath := filepath.Join(jobDir, "commit-identity")
	raw, err := os.ReadFile(commitIdentityPath)
	if err != nil {
		errRaw, _ := os.ReadFile(filepath.Join(jobDir, "commit-identity-err"))
		require.Fail(t, fmt.Sprintf("failed to read commit-identity: %v; harness git err: %s", err, string(errRaw)))
	}

	got := strings.TrimSpace(string(raw))
	want := "Native Drain Worker <drain-worker@example.com> Native Drain Worker <drain-worker@example.com>"
	assert.Equal(t, want, got, "native harness commit identity = %q, want %q", got, want)
	assert.NotContains(t, got, "Hostile Ghost", "hostile bench gitconfig leaked into native harness commit: %q", got)
	assert.NotContains(t, got, "ghost@example.com", "hostile bench gitconfig leaked into native harness commit: %q", got)
}

func TestNativeRefusesMissingPoolIdentityBeforeHarness(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a native execution")
	}

	root, slot := aSlot(t)
	// Remove identity.tsv so the pool root has no identity file.
	err := os.Remove(filepath.Join(root, "identity.tsv"))
	if !os.IsNotExist(err) {
		require.NoError(t, err)
	}

	require.NoError(t, buildShared())
	bin := builtHarness
	cardPath := filepath.Join(root, "card.md")
	write(t, cardPath, "native missing identity card\nFAKE-GIT-COMMIT\nFAKE-FINDINGS 0\n")

	// Hostile bench git config and identity in the parent environment.
	benchHome := t.TempDir()
	benchConfig := filepath.Join(benchHome, ".gitconfig")
	write(t, benchConfig, "[user]\n\tname = Hostile Ghost\n\temail = ghost@example.com\n")

	origHome := os.Getenv("HOME")
	origGitConfig := os.Getenv("GIT_CONFIG_GLOBAL")
	origAuthorName := os.Getenv("GIT_AUTHOR_NAME")
	origAuthorEmail := os.Getenv("GIT_AUTHOR_EMAIL")
	origCommitterName := os.Getenv("GIT_COMMITTER_NAME")
	origCommitterEmail := os.Getenv("GIT_COMMITTER_EMAIL")
	defer func() {
		os.Setenv("HOME", origHome)
		restoreEnv("GIT_CONFIG_GLOBAL", origGitConfig)
		restoreEnv("GIT_AUTHOR_NAME", origAuthorName)
		restoreEnv("GIT_AUTHOR_EMAIL", origAuthorEmail)
		restoreEnv("GIT_COMMITTER_NAME", origCommitterName)
		restoreEnv("GIT_COMMITTER_EMAIL", origCommitterEmail)
	}()
	os.Setenv("HOME", benchHome)
	os.Setenv("GIT_CONFIG_GLOBAL", benchConfig)
	os.Setenv("GIT_AUTHOR_NAME", "Hostile Ghost")
	os.Setenv("GIT_AUTHOR_EMAIL", "ghost@example.com")
	os.Setenv("GIT_COMMITTER_NAME", "Hostile Ghost")
	os.Setenv("GIT_COMMITTER_EMAIL", "ghost@example.com")

	label := "missing-id-task-1"
	var stdout, stderr bytes.Buffer
	rc := run([]string{
		"native",
		"--tokens", "unmetered",
		"--slots-store", nativeStore(t),
		"--owner", "fake-1",
		"--harness", bin,
		"--model", "fake/fake-model",
		"--label", label,
		"--card", cardPath,
		"--slot", slot,
		"--root", root,
		"--deadline", "30s",
		"--no-wall",
	}, strings.NewReader(""), &stdout, &stderr, time.Now())

	require.Equal(t, 2, rc, "native run exit = %d, want 2 (refusal);\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	require.Contains(t, stderr.String(), "NATIVE REFUSED", "stderr does not contain NATIVE REFUSED:\n%s", stderr.String())
	require.Contains(t, stderr.String(), "refusing to launch under nobody's name", "stderr does not contain expected refusal message:\n%s", stderr.String())
	// #3193: the refusal names identity.tsv and the one remedy, the fleet converge.
	line := stderr.String()
	require.Contains(t, line, "identity.tsv", "the refusal does not name identity.tsv and the remedy `make -C fleet converge`:\n%s", line)
	require.Contains(t, line, "make -C fleet converge", "the refusal does not name identity.tsv and the remedy `make -C fleet converge`:\n%s", line)

	// Verify harness was never started: neither native.log nor harness-output.log was created.
	nativeLog := filepath.Join(slot, "native.log")
	_, err = os.Stat(nativeLog)
	assert.True(t, os.IsNotExist(err), "native.log exists at %s, want harness never started", nativeLog)
	jobDir := filepath.Join(slot, "jobs", label)
	harnessOut := filepath.Join(jobDir, "harness-output.log")
	_, err = os.Stat(harnessOut)
	assert.True(t, os.IsNotExist(err), "harness-output.log exists at %s, want harness never started", harnessOut)
	commitIdentity := filepath.Join(jobDir, "commit-identity")
	_, err = os.Stat(commitIdentity)
	assert.True(t, os.IsNotExist(err), "commit-identity exists at %s, harness should not have run", commitIdentity)
}

func TestNativeRefusesMalformedPoolIdentityBeforeHarness(t *testing.T) {
	if testing.Short() {
		t.Skip("this one runs a native execution")
	}

	root, slot := aSlot(t)
	// Overwrite identity.tsv with malformed contents (header only, no rows).
	write(t, filepath.Join(root, "identity.tsv"), "owner\tname\temail\n")

	require.NoError(t, buildShared())
	bin := builtHarness
	cardPath := filepath.Join(root, "card.md")
	write(t, cardPath, "native malformed identity card\nFAKE-GIT-COMMIT\nFAKE-FINDINGS 0\n")

	// Hostile bench git config and identity in the parent environment.
	benchHome := t.TempDir()
	benchConfig := filepath.Join(benchHome, ".gitconfig")
	write(t, benchConfig, "[user]\n\tname = Hostile Ghost\n\temail = ghost@example.com\n")

	origHome := os.Getenv("HOME")
	origGitConfig := os.Getenv("GIT_CONFIG_GLOBAL")
	origAuthorName := os.Getenv("GIT_AUTHOR_NAME")
	origAuthorEmail := os.Getenv("GIT_AUTHOR_EMAIL")
	origCommitterName := os.Getenv("GIT_COMMITTER_NAME")
	origCommitterEmail := os.Getenv("GIT_COMMITTER_EMAIL")
	defer func() {
		os.Setenv("HOME", origHome)
		restoreEnv("GIT_CONFIG_GLOBAL", origGitConfig)
		restoreEnv("GIT_AUTHOR_NAME", origAuthorName)
		restoreEnv("GIT_AUTHOR_EMAIL", origAuthorEmail)
		restoreEnv("GIT_COMMITTER_NAME", origCommitterName)
		restoreEnv("GIT_COMMITTER_EMAIL", origCommitterEmail)
	}()
	os.Setenv("HOME", benchHome)
	os.Setenv("GIT_CONFIG_GLOBAL", benchConfig)
	os.Setenv("GIT_AUTHOR_NAME", "Hostile Ghost")
	os.Setenv("GIT_AUTHOR_EMAIL", "ghost@example.com")
	os.Setenv("GIT_COMMITTER_NAME", "Hostile Ghost")
	os.Setenv("GIT_COMMITTER_EMAIL", "ghost@example.com")

	label := "malformed-id-task-1"
	var stdout, stderr bytes.Buffer
	rc := run([]string{
		"native",
		"--tokens", "unmetered",
		"--slots-store", nativeStore(t),
		"--owner", "fake-1",
		"--harness", bin,
		"--model", "fake/fake-model",
		"--label", label,
		"--card", cardPath,
		"--slot", slot,
		"--root", root,
		"--deadline", "30s",
		"--no-wall",
	}, strings.NewReader(""), &stdout, &stderr, time.Now())

	require.Equal(t, 2, rc, "native run exit = %d, want 2 (refusal);\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
	require.Contains(t, stderr.String(), "NATIVE REFUSED", "stderr does not contain NATIVE REFUSED:\n%s", stderr.String())
	require.Contains(t, stderr.String(), "refusing to launch under nobody's name", "stderr does not contain expected refusal message:\n%s", stderr.String())
	// #3193: the refusal names identity.tsv and the one remedy, the fleet converge.
	line := stderr.String()
	require.Contains(t, line, "identity.tsv", "the refusal does not name identity.tsv and the remedy `make -C fleet converge`:\n%s", line)
	require.Contains(t, line, "make -C fleet converge", "the refusal does not name identity.tsv and the remedy `make -C fleet converge`:\n%s", line)

	// Verify harness was never started: neither native.log nor harness-output.log was created.
	nativeLog := filepath.Join(slot, "native.log")
	_, err := os.Stat(nativeLog)
	assert.True(t, os.IsNotExist(err), "native.log exists at %s, want harness never started", nativeLog)
	jobDir := filepath.Join(slot, "jobs", label)
	harnessOut := filepath.Join(jobDir, "harness-output.log")
	_, err = os.Stat(harnessOut)
	assert.True(t, os.IsNotExist(err), "harness-output.log exists at %s, want harness never started", harnessOut)
	commitIdentity := filepath.Join(jobDir, "commit-identity")
	_, err = os.Stat(commitIdentity)
	assert.True(t, os.IsNotExist(err), "commit-identity exists at %s, harness should not have run", commitIdentity)
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

// TestNativeConfigNamesTheWorkerReadRootsOnAWalledRun is the issue, at the fence. RED
// WITHOUT THE FIX: the walled run's permission block held the job's own directories and
// nothing else, so every read of the staged root was auto-rejected.
func TestNativeConfigNamesTheWorkerReadRootsOnAWalledRun(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	stage := t.TempDir()

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "lbl",
		card:     []byte("FAKE-NORESULT\n"),
		slotDir:  slot,
		root:     root,
		deadline: 30 * time.Second,
		sandbox:  nativeSandbox(t),
		worker:   aReadRootWorker(stage),
	}, &errOut)
	require.Equal(t, 0, code, "the run exits 0, got %d:\n%s", code, errOut.String())
	external := externalDirectoryRules(t, slot)
	// The path itself, and both wildcard spellings, because the harness asks about a
	// directory under either -- FenceReadPatterns is the one place that decides the shapes.
	for _, want := range []string{stage, stage + "/*", stage + "/**"} {
		assert.Equal(t, "allow", external[want], "read_roots named %s, so the fence allows %s on a WALLED run; it holds %v", stage, want, external)
	}
}

// TestNativeWallReadsTheWorkerReadRoots is the same declaration at the OTHER fence. A root
// the fence allows and the wall does not is the same dead card with the denial one layer
// down, so both are told or neither is. RED WITHOUT THE FIX: the wall's argv named the slot,
// the harness's own directory, /opt/homebrew and the toolchain roots, and no root of the
// description's.
func TestNativeWallReadsTheWorkerReadRoots(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	_, slot := aSlot(t)
	stage := t.TempDir()
	cfg := nativeRunConfig{slotDir: slot, benchHome: t.TempDir(), benchOS: "linux", worker: aReadRootWorker(stage)}
	argv := nativeSandboxArgv([]string{bin}, cfg, slot+"/data", slot+"/jobs/lbl", slot+"/tmp/lbl")

	found := false
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "--read" && argv[i+1] == stage {
			found = true
		}
		// AND NEVER A WRITE. A root the desk names is a reference, not a workspace.
		assert.False(t, (argv[i] == "--write" || argv[i] == "--read-noexec") && argv[i+1] == stage, "a read root reaches the wall as --read and on no other flag; %s is on %s:\n%s", stage, argv[i], strings.Join(argv, " "))
	}
	assert.True(t, found, "read_roots named %s, so the wall is handed --read %s; the argv reads:\n%s", stage, stage, strings.Join(argv, " "))
}

// TestNativeConfigStillWithholdsTheCardsReadPathsOnAWalledRun is the line this fix does NOT
// cross, pinned so a later widening is a red test rather than a security review nobody asks
// for. A card is the MODEL'S text; a description is a PERSON'S. A fence rule a card can
// widen for itself is no fence, so the card's `READ:` paths stay what they were -- a
// --no-wall affair, where there is no OS wall to open them and the fence is all there is
// (issue #644, TestNativeConfigNamesTheCardsReadPaths).
func TestNativeConfigStillWithholdsTheCardsReadPathsOnAWalledRun(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "lbl",
		card:     []byte("card line 1\nREAD: /sys/kernel/security/lsm\nFAKE-NORESULT\n"),
		slotDir:  slot,
		root:     root,
		deadline: 30 * time.Second,
		sandbox:  nativeSandbox(t),
	}, &errOut)
	require.Equal(t, 0, code, "the run exits 0, got %d:\n%s", code, errOut.String())
	external := externalDirectoryRules(t, slot)
	for _, never := range []string{"/sys/kernel/security/lsm", "/sys/kernel/security/*"} {
		_, named := external[never]
		assert.False(t, named, "a WALLED run takes no path from the card's own text: %s is named; it holds %v", never, external)
	}
}

// TestNativeConfigNamesTheJobDirectory: every native run writes a harness config, whether or
// not --config named one, and its permission block makes the job's own directories internal
// to the fence, and nothing above them. RED WITHOUT THE CHANGE: before it, a run with no
// --config wrote no config at all and a run with one wrote the provider's bytes with no
// permission block, so what the fence called external was left entirely to the harness.
func TestNativeConfigNamesTheJobDirectory(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "lbl",
		card:     []byte("FAKE-NORESULT\n"),
		slotDir:  slot,
		root:     root,
		deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "the run exits 0, got %d:\n%s", code, errOut.String())
	assert.NotEqual(t, "", res.configSHA, "the NATIVE OK line names the sha8 of the config the child saw, got %q", res.configSHA)
	assert.NotEqual(t, "-", res.configSHA, "the NATIVE OK line names the sha8 of the config the child saw, got %q", res.configSHA)

	external := externalDirectoryRules(t, slot)
	job := res.job
	for _, want := range []string{job + "/*", job + "/**"} {
		assert.Equal(t, "allow", external[want], "the permission block allows %s (the job's own directory); it holds %v", want, external)
	}
	// AND NOTHING ABOVE THE JOB. The harness resolves a card's `../scratch` after its own
	// `cd repo`, so the job's `jobs` parent buys nothing -- and naming it would hand one
	// card every sibling job in the slot on a bench with no wall.
	jobs := filepath.Dir(job)
	for _, never := range []string{jobs + "/*", jobs + "/**"} {
		_, named := external[never]
		assert.False(t, named, "%s is above the job and is never named; it holds %v", never, external)
	}
	assert.Equal(t, "deny", external["*"], "everything else is denied without prompting (a deny is a tool error the model routes around; an ask auto-rejects and ends the run); it holds %v", external)
}

// TestNativeConfigDeniesExternalPaths: the generated config the child reads DENIES a path
// outside the job, and denies webfetch, so no permission is left to prompt. An `ask` in a
// non-interactive `run` is auto-rejected and the model stops -- the run ends and the card's
// commits are stranded. RED WITHOUT THE CHANGE: the block said `ask` (issue #918).
func TestNativeConfigDeniesExternalPaths(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "lbl",
		card:     []byte("FAKE-NORESULT\n"),
		slotDir:  slot,
		root:     root,
		deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "the run exits 0, got %d:\n%s", code, errOut.String())
	raw, err := os.ReadFile(filepath.Join(slot, "data", ".config", "opencode", "opencode.json"))
	require.NoError(t, err, "every native run writes the harness config the job's fence reads")
	var cfg struct {
		Permission struct {
			External map[string]string `json:"external_directory"`
			Webfetch string            `json:"webfetch"`
		} `json:"permission"`
	}
	err = json.Unmarshal(raw, &cfg)
	require.NoError(t, err, "the config the child saw is not readable JSON:\n%s", raw)
	assert.Equal(t, "deny", cfg.Permission.External["*"], "a path outside the job is denied, never asked about; it holds %v", cfg.Permission.External)
	assert.Equal(t, "deny", cfg.Permission.Webfetch, "webfetch is denied too, so no permission is left to prompt; it holds %q", cfg.Permission.Webfetch)
}

// TestNativeConfigNamesTheCardsReadPaths: on a bench with NO OS WALL, a card that names a
// read-only system path on its `READ:` line runs with that path open to the fence -- there
// is no wall to open it, and the second case of #644 was `cat /sys/kernel/security/lsm`
// rejected twice on Space.
func TestNativeConfigNamesTheCardsReadPaths(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "lbl",
		card:     []byte("card line 1\nREAD: /sys/kernel/security/lsm\nFAKE-NORESULT\n"),
		slotDir:  slot,
		root:     root,
		deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "the run exits 0, got %d:\n%s", code, errOut.String())
	external := externalDirectoryRules(t, slot)
	// The harness asks about the PARENT of a file it is told to read, with a `/*` on it.
	for _, want := range []string{"/sys/kernel/security/lsm", "/sys/kernel/security/*"} {
		assert.Equal(t, "allow", external[want], "the card named READ: /sys/kernel/security/lsm, so %s is allowed; it holds %v", want, external)
	}
}

// TestNativeReportsAFenceRejection: a harness that prints its own rejection line and exits 0
// is reported `fence=rejected path=<p>` on the NATIVE OK line. RED WITHOUT THE CLASSIFIER:
// before it the line said `harness=ok rc=0` and nothing else, and the card was scored
// `no-result` -- a coordinator went and read the model for a fence the machinery built.
func TestNativeReportsAFenceRejection(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	require.NoError(t, os.WriteFile(cardPath, []byte("FAKE-FENCE-REJECT /workspace/swarm-root/1/jobs/*\n"), 0o644))
	args := []string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
		"--label", "lbl", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"}
	var stdout, stderr bytes.Buffer
	rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	require.Equal(t, 0, rc, "the run exits 0, got %d:\n%s", rc, stderr.String())
	want := " fence=rejected path=/workspace/swarm-root/1/jobs/*"
	require.Contains(t, stdout.String(), want, "the NATIVE OK line carries%s:\n%s", want, stdout.String())
}

// TestDeniedPathWithASpaceStillRefuses is P1 as a CLI regression, at the full path. A
// whitespace-bearing absolute path is not evidence of prose.
func TestDeniedPathWithASpaceStillRefuses(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	for _, refused := range []string{
		"/opt/sdk tool/bin/go",
		"/opt/sdk (old)/bin/go",
		"/opt/my sdk/go 1.26/bin/go",
	} {
		t.Run(refused, func(t *testing.T) {
			root, slot := aSlot(t)
			cardPath := filepath.Join(root, "card.md")
			require.NoError(t, os.WriteFile(cardPath, []byte("FAKE-EXEC-REFUSED "+refused+"\n"), 0o644))
			args := []string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
				"--label", "go-card", "--card", cardPath, "--slot", slot, "--root", root,
				"--deadline", "30s", "--no-wall"}
			var stdout, stderr bytes.Buffer
			rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
			require.NotContains(t, stdout.String(), "NATIVE OK", "a denied path holding a space is still a denial; the run reported OK (rc=%d):\n%s", rc, stdout.String())
			require.NotEqual(t, 0, rc, "a denied path holding a space is still a denial; the run reported OK (rc=%d):\n%s", rc, stdout.String())
			// AND IT IS NAMED WHOLE. A path truncated at its first space is a path the
			// coordinator cannot act on, and read_roots would take the wrong directory.
			assert.Contains(t, stderr.String(), refused, "the refusal names the complete path %q; it reads:\n%s", refused, stderr.String())
		})
	}
}

// TestRefusalClaimsNoCauseItCannotProve is P2. A shell's `Permission denied` on a path is
// evidence that SOMETHING was denied and nothing more: a witness is a bash
// redirection to an unwritable output that printed exactly this shape and then RECOVERED,
// exit 0, having attempted no program at all. The refusal may not call that path a program,
// may not assert that a gate never ran or that nothing was compiled, and may not prescribe
// read_roots as the cure for a failed write.
func TestRefusalClaimsNoCauseItCannotProve(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	require.NoError(t, os.WriteFile(cardPath, []byte("FAKE-DENY-AND-RECOVER /opt/out/report.txt\n"), 0o644))
	args := []string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
		"--label", "redirect-card", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"}
	var stdout, stderr bytes.Buffer
	rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	// The disposition is still refused -- a denial nobody read is not an OK -- but the WORDS
	// must be honest about what is known.
	require.NotEqual(t, 0, rc, "a denial in the capture is never OK, whatever it was: rc=%d\n%s", rc, stdout.String())
	require.NotContains(t, stdout.String(), "NATIVE OK", "a denial in the capture is never OK, whatever it was: rc=%d\n%s", rc, stdout.String())
	line := stderr.String()
	for _, forbidden := range []string{
		"never executed", "nothing compiled", "never compiled",
		"the program", "the gate never",
	} {
		assert.NotContains(t, line, forbidden, "the refusal asserts %q, which this line cannot establish -- a redirection to an unwritable path prints the same words and the card recovers:\n%s", forbidden, line)
	}
	// It must say what it does not know, in so many words.
	assert.Contains(t, line, "unverified", "the refusal labels the denied operation unverified; it reads:\n%s", line)
	// It must carry the line itself, which is the only thing a person can act on.
	assert.Contains(t, line, "Permission denied", "the refusal quotes the capture's own line verbatim; it reads:\n%s", line)
	// AND IT MUST NOT BLAME A WALL THAT WAS NEVER THERE. This run was --no-wall.
	for _, forbidden := range []string{"the wall refused", "read_roots"} {
		assert.NotContains(t, line, forbidden, "an unwalled run attributes nothing to a wall or its read set (%q):\n%s", forbidden, line)
	}
}

// TestWalledRefusalOffersTheReadRootsAsOnePossibility: on a WALLED run the read set is a
// real candidate and the refusal may name it -- as one possible cause among the others, never
// as the diagnosis. The roots are still worked out for the coordinator rather than left to a
// guess.
func TestWalledRefusalOffersTheReadRootsAsOnePossibility(t *testing.T) {
	t.Parallel()
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	require.NoError(t, os.WriteFile(cardPath, []byte("FAKE-EXEC-REFUSED /opt/sdk tool/bin/go\n"), 0o644))
	args := []string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
		"--label", "walled-card", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--sandbox", nativeSandbox(t)}
	var stdout, stderr bytes.Buffer
	rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	require.NotEqual(t, 0, rc, "the walled run is refused, got rc=0:\n%s", stdout.String())
	line := stderr.String()
	assert.Contains(t, line, "read_roots", "a walled run's refusal offers the read set as one candidate; it reads:\n%s", line)
	assert.Contains(t, line, "/opt/sdk tool/bin", "the candidate root is the complete directory of the denied path; it reads:\n%s", line)
	assert.Contains(t, line, "unverified", "a walled run's refusal is still an unverified cause; it reads:\n%s", line)
}

// TestADenialTheCardRewroteStillRefuses is the hold on #1478 at 29047871, the #1892
// class: the shell-denial verdict was read from `<job>/harness-output.log` by path after the
// child exited, and that name is in the card's own --write directory and is its cwd. A card
// that printed the denial and then replaced the file (or removed it, so the read failed) got
// NATIVE OK. The verdict is now taken from the bytes the parent received; what the card does
// to the file afterwards cannot reach it.
func TestADenialTheCardRewroteStillRefuses(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	const refused = "/opt/sdk/go1.26.5/bin/go"
	for name, card := range map[string]string{
		"replaced after the denial": "FAKE-EXEC-REFUSED " + refused + "\nFAKE-REWRITE-CAPTURE\n",
		"removed so the read fails": "FAKE-DROP-CAPTURE\nFAKE-EXEC-REFUSED " + refused + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			root, slot := aSlot(t)
			cardPath := filepath.Join(root, "card.md")
			require.NoError(t, os.WriteFile(cardPath, []byte(card), 0o644))
			args := []string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
				"--label", "rewrite-card", "--card", cardPath, "--slot", slot, "--root", root,
				"--deadline", "30s", "--no-wall"}
			var stdout, stderr bytes.Buffer
			rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
			require.NotContains(t, stdout.String(), "NATIVE OK", "a denial the card hid from its own capture file still returned OK (rc=%d):\n%s%s", rc, stdout.String(), stderr.String())
			require.NotEqual(t, 0, rc, "a denial the card hid from its own capture file still returned OK (rc=%d):\n%s%s", rc, stdout.String(), stderr.String())
			line := stderr.String()
			assert.Contains(t, line, "NATIVE REFUSED", "the refusal names the denied path and the step from the parent's copy; it reads:\n%s", line)
			assert.Contains(t, line, refused, "the refusal names the denied path and the step from the parent's copy; it reads:\n%s", line)
			assert.Contains(t, line, "step=3", "the refusal names the denied path and the step from the parent's copy; it reads:\n%s", line)
		})
	}
}

// AN UNREAD DENIAL CANNOT RETURN OK (issue #1465, as the hold on #1478 reshaped it).
//
// The run this closes: a `native` Go card was handed GOMODCACHE, GOCACHE and
// GOTOOLCHAIN=local, wrote its test, and could not compile it --
//
//	/usr/bin/bash: line 1: /home/worker/go/bin/go: Permission denied
//
// -- because the toolchain those three names are FOR is under no root the wall admits. The
// card said so in its own RESULT.md, and the tool said
//
//	NATIVE OK label=go-writer-bound-count ... rc=0 wall=743s sandbox=landlock harness=ok
//
// usd 0.0993, and a commit nobody had compiled read as green. The card was honest; every
// machine-readable field lied. A coordinator reading dispositions and not prose ships it.
//
// The class is made impossible here rather than papered over: a run whose capture holds a
// denial the card's own shell reported is a HARD REFUSAL. There is no NATIVE OK line at all
// and the exit is non-zero.
//
// WHAT THE REFUSAL MAY SAY is a separate question, and the P2 witness settled it: the shell
// names a path and a refusal and NOT an operation, so the line labels the operation
// unverified and asks for the measurement instead of inventing a cause. This case is WALLED,
// because the run it closes was (sandbox=landlock), and a walled run may offer the read set
// as one candidate.
func TestNativeGateThatCouldNotRunIsNeverOK(t *testing.T) {
	t.Parallel()
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	const refused = "/opt/sdk/go1.26.5/bin/go"
	cardPath := filepath.Join(root, "card.md")
	// The card publishes a RESULT.md the way the real one did, so this is the SILENT shape
	// and not a wall death: the harness spoke, the result exists, the child exited 0.
	require.NoError(t, os.WriteFile(cardPath, []byte("FAKE-EXEC-REFUSED "+refused+"\n"), 0o644))
	args := []string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
		"--label", "go-card", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--sandbox", nativeSandbox(t)}
	var stdout, stderr bytes.Buffer
	rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())

	assert.NotContains(t, stdout.String(), "NATIVE OK", "a denial nobody read and the run still reported OK:\n%s", stdout.String())
	assert.NotEqual(t, 0, rc, "a run whose gate could not execute exits non-zero, got %d:\n%s%s", rc, stdout.String(), stderr.String())
	line := stderr.String()
	require.Contains(t, line, "NATIVE REFUSED", "the run owes one refusal line naming the class:\n%s", line)
	// What is MEASURED is named, and the one thing that is not measured is named as not
	// measured. Nothing here claims the path was a program or that a gate did not run.
	for _, want := range []string{refused, "step=3", "operation=unverified", "Permission denied", "read_roots"} {
		assert.Contains(t, line, want, "the refusal names %q; it reads:\n%s", want, line)
	}
	for _, forbidden := range []string{"never executed", "nothing compiled", "the gate never"} {
		assert.NotContains(t, line, forbidden, "the refusal asserts %q, which the shell's line cannot establish:\n%s", forbidden, line)
	}
	// THE WORK IS NOT THROWN AWAY. The child ran and was paid for: the job directory, its
	// result and its usage row are all still named, so a coordinator can harvest what the
	// card did manage before the gate died.
	assert.Contains(t, line, filepath.Join(slot, "jobs", "go-card"), "the refusal names the job directory so the spend is harvestable; it reads:\n%s", line)
	_, err := os.Stat(filepath.Join(slot, "jobs", "go-card", "RESULT.md"))
	assert.NoError(t, err, "the card's own result is left where it was published")
}

// TestNativeOrdinaryRunIsStillOK: the guard above fires on the class and on nothing else. A
// card that merely prints the words -- another program's refusal it routed around, its own
// prose -- is a finished run and still says OK.
func TestNativeOrdinaryRunIsStillOK(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	require.NoError(t, os.WriteFile(cardPath, []byte("FAKE-REFUSE 2\n"), 0o644))
	args := []string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
		"--label", "read-card", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"}
	var stdout, stderr bytes.Buffer
	rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	require.Equal(t, 0, rc, "a card that was refused a READ and carried on is a finished run, got %d:\n%s%s", rc, stdout.String(), stderr.String())
	require.Contains(t, stdout.String(), "NATIVE OK ", "the NATIVE OK line is printed for a run whose gate did execute:\n%s", stdout.String())
}

// THE READ ROOTS THE DESK NAMED REACH BOTH FENCES (issue #1463).
//
// A worker description's `read_roots` is the desk's own declaration of what a job may READ:
// a bench-local mirror, a corpus, a toolchain. `worker check` accepted it, and then neither
// fence was told. The OS wall's read set was built without it, and the HARNESS's own
// permission block -- which took the card's `READ:` paths on a `--no-wall` run only -- was
// built without it too, so every read of the staged path was auto-rejected and a card came
// back with the whole row owed after 148 seconds.
//
// Both fences now hear it, on a walled run as much as an unwalled one. The wall is still the
// real boundary (SPEC-SANDBOX rule 1); the harness's fence is a second, weaker one, and a
// second fence that denies what the first one grants can only cost cards.
func TestNativeWalledRunOpensTheWorkersReadRoots(t *testing.T) {
	t.Parallel()
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	stage, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	worker := workerWithReadRoots(t, stage)
	cardPath := filepath.Join(root, "card.md")
	require.NoError(t, os.WriteFile(cardPath, []byte("a card line 1\nline 2\n"), 0o644))
	args := []string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--worker", worker,
		"--label", "staged", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--sandbox", nativeSandbox(t)}
	var stdout, stderr bytes.Buffer
	rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	require.Equal(t, 0, rc, "the walled run exits 0, got %d:\n%s%s", rc, stdout.String(), stderr.String())
	// THE OS WALL. The read root is a --read, beside the slot directory and the harness's own.
	argv := sandboxArgv(t, filepath.Join(slot, "jobs", "staged"))
	assert.Contains(t, argv, "--read "+stage, "the wall argv does not read the description's read_roots entry %s:\n%s", stage, argv)
	// THE HARNESS'S OWN FENCE. The same root, in the permission block the child reads, on a
	// WALLED run -- which is the whole of #1463.
	external := externalDirectoryRules(t, slot)
	for _, want := range []string{stage, stage + "/*"} {
		assert.Equal(t, "allow", external[want], "the harness fence does not admit the read root %s on a walled run; it holds %v", want, external)
	}
}

// A push origin rejected on its own side is the remote's failure, not the
// commit's: it is sent again after a wait, and the commit lands. Origin rejecting
// it every time is the refusal, with git's line, after the last wait; a push
// refused for the commit itself is never sent again.
func TestAPushTheRemoteRejectedIsSentAgain(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	head := b.commit(t, "the work\n")
	var waits []time.Duration
	g := b.pusher()
	twice := &rejectGit{reject: 2}
	g.git, g.sleep = twice.run, func(d time.Duration) { waits = append(waits, d) }
	require.Equal(t, member.Push{Sha: head}, g.Push(b.p, member.Result{Head: head}))
	assert.Equal(t, 3, twice.pushes, "rejected twice, landed on the third")
	assert.Equal(t, pushWaits[:2], waits)
	assert.Equal(t, head, b.originHas(t, "sprint/c1"))

	b2 := newPushBench(t)
	head2 := b2.commit(t, "the work\n")
	waits = nil
	g2 := b2.pusher()
	always := &rejectGit{reject: 100}
	g2.git, g2.sleep = always.run, func(d time.Duration) { waits = append(waits, d) }
	got := g2.Push(b2.p, member.Result{Head: head2})
	assert.Contains(t, got.Refused, "[remote rejected]")
	assert.Equal(t, len(pushWaits)+1, always.pushes)
	assert.Equal(t, pushWaits, waits)
	assert.Empty(t, b2.originHas(t, "sprint/c1"))

	b3 := newPushBench(t)
	head3 := b3.commit(t, "the work\n")
	waits = nil
	g3 := b3.pusher()
	rec := &recordGitRun{refusePush: "!\t0123:refs/heads/sprint/c1\t[rejected] (non-fast-forward)"}
	g3.git, g3.sleep = rec.run, func(d time.Duration) { waits = append(waits, d) }
	got = g3.Push(b3.p, member.Result{Head: head3})
	assert.Contains(t, got.Refused, "[rejected]")
	assert.Empty(t, waits, "a push refused for the commit is not sent again")
}

// A fetch from the checkout that fails is the member's moment, never the card's: it is made
// again after a wait, with nothing of the failed try left in this launch's namespace, and the
// commit lands; a fetch that fails every time is the refusal, saying the push repository and
// the tries. The launch's refs are gone before the push to origin, so another launch's fetch
// never meets them for the seconds a push and a pull request take. On the 5000-card load test
// (2026-10-01) one card failed "push refused: fetch from the checkout: fatal: bad object
// refs/member/<another launch>/HEAD", a moment of the shared push repository.
func TestAFetchFromTheCheckoutThatFailsIsMadeAgain(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	head := b.commit(t, "the work\n")
	var waits []time.Duration
	g := b.pusher()
	once := &badFetchGit{fail: 1}
	g.git, g.sleep = once.run, func(d time.Duration) { waits = append(waits, d) }
	require.Equal(t, member.Push{Sha: head}, g.Push(b.p, member.Result{Head: head}))
	assert.Equal(t, 2, once.fetches, "failed once, fetched on the second")
	assert.Equal(t, pushWaits[:1], waits)
	require.Len(t, once.refsAfterFailure, 1)
	assert.Contains(t, once.refsAfterFailure[0], "refs/member/"+launchName(b.p)+"/HEAD", "the failed try really created the launch namespace")
	assert.Empty(t, once.refsAtFetch, "each try starts with this launch's namespace empty")
	assert.Empty(t, once.refsAtPush, "the launch's refs are dropped before the push to origin")
	assert.Equal(t, head, b.originHas(t, "sprint/c1"))

	b2 := newPushBench(t)
	head2 := b2.commit(t, "the work\n")
	waits = nil
	g2 := b2.pusher()
	always := &badFetchGit{fail: 100}
	g2.git, g2.sleep = always.run, func(d time.Duration) { waits = append(waits, d) }
	got := g2.Push(b2.p, member.Result{Head: head2})
	assert.Empty(t, got.Sha)
	assert.Equal(t, fmt.Sprintf("fetch from the checkout into the member's push repository, %d tries: %s", len(pushWaits)+1, badFetchLine), got.Refused)
	assert.Equal(t, len(pushWaits)+1, always.fetches)
	assert.Equal(t, pushWaits, waits)
	require.Len(t, always.refsAfterFailure, len(pushWaits)+1)
	for _, refs := range always.refsAfterFailure {
		assert.Contains(t, refs, "refs/member/"+launchName(b2.p)+"/HEAD", "every failed try created refs before returning its error")
	}
	assert.Empty(t, always.refsAtFetch, "each retry starts after the failed try was cleaned up")
	assert.Empty(t, runGit(t, filepath.Join(b2.root, "push.git"), "for-each-ref", "--format=%(refname)", "refs/member/"), "the final refusal also cleans up its partial fetch")
	assert.Empty(t, b2.originHas(t, "sprint/c1"))
}

// THE SHAPE ITSELF: the harness says its last word, it is a question, and the process
// exits 0 having published nothing. RED WITHOUT THE FIX: the job holds no RESULT.md at
// all and the question is in a transcript nobody reads.
func TestNativeWritesAnAskedResultForACardThatEndedWithAQuestion(t *testing.T) {
	t.Parallel()

	question := "Would you like me to proceed with moving RESULT.md into the nested repo and finalize the commit?"
	job, stdout, stderr := nativeAsked(t, "asked", "FAKE-SAY "+question+"\nFAKE-NORESULT\n")

	raw, err := os.ReadFile(filepath.Join(job, "RESULT.md"))
	require.NoError(t, err, "a card that ended by asking left no report\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	line1 := strings.SplitN(string(raw), "\n", 2)[0]
	require.True(t, strings.HasPrefix(line1, "RESULT: ASKED "), "line 1 must carry the verdict word, got %q", line1)
	require.Contains(t, line1, question, "line 1 must carry the question itself, got %q", line1)
	require.Contains(t, string(raw), "written-by: nova-swarm native", "the report does not say the machinery wrote it:\n%s", raw)
	require.Contains(t, stderr, "NATIVE NOTE: the card ended its last turn with a question", "the run did not say it had written the report:\n%s", stderr)
	// AND THE VERDICT DOES NOT MOVE. A report the machinery wrote is not the card's own,
	// and counting it would print `NATIVE OK` for a card that did nothing but ask.
	require.NotContains(t, stdout, "NATIVE OK", "a card that only asked a question was called OK:\n%s", stdout)
	require.Contains(t, stdout, "why=no-result", "the verdict line must still name the card as incomplete:\n%s", stdout)
}

// A CARD THAT PUBLISHED IS DONE, whatever its prose said. The fake harness publishes by
// default, so this is the ordinary ending: the report is the card's own, the verdict is
// OK, and nothing is overwritten.
func TestNativeLeavesAFinishedCardsReportAlone(t *testing.T) {
	t.Parallel()

	job, stdout, stderr := nativeAsked(t, "done", "FAKE-SAY Want me to open the PR as well?\nFAKE-FINDINGS 0\n")

	raw, err := os.ReadFile(filepath.Join(job, "RESULT.md"))
	require.NoError(t, err, "the card's own report is missing\nstderr:\n%s", stderr)
	require.NotContains(t, string(raw), "RESULT: ASKED", "a finished card's report was overwritten with an asked verdict:\n%s", raw)
	require.Contains(t, stdout, "NATIVE OK", "a card that published its own report is OK:\n%s", stdout)
}

// A CRASH IS A CRASH. The harness asks, then exits non-zero: that is `rc=<n>`, an end this
// tool already names, and a model that fell over is not waiting for an answer. The exit
// code is read before the capture is, which is why this card gets no report even though it
// asked; the question-is-the-last-line half of the same shape is a row of the table in
// pkg/swarm/nativeasked_test.go, where it costs no provider retry to arrange.
func TestNativeDoesNotCallACrashAnAskedCard(t *testing.T) {
	t.Parallel()

	job, stdout, _ := nativeAsked(t, "crash", "FAKE-SAY Should I retry the build?\nFAKE-429\n")

	raw, err := os.ReadFile(filepath.Join(job, "RESULT.md"))
	require.Error(t, err, "a crashed card was given an asked report:\n%s", raw)
	require.Contains(t, stdout, "NATIVE INCOMPLETE", "a crash is still incomplete:\n%s", stdout)
	require.NotContains(t, stdout, "RESULT: ASKED", "a crash was reported as a question:\n%s", stdout)
}

// TestNativeRunHandsTheChildTheBenchGo holds the delivery leg of PR 5093 that
// TestTheChildEnvResolvesTheBenchGo leaves open (Zhi's cold read: that test hands
// nativeChildEnv its goBin by hand, so removing the production call
// swarm.BenchGoBin(benchHome(cfg), os.Getenv("PATH")) left it green). This one runs
// nativeRun itself against a fake bench home whose sdk Go is on no PATH entry, and reads
// the PATH the child was really handed from the run's native-argv.log: the sdk's bin and its GOROOT/bin
// are on it. RED WITHOUT THE CALL: the child's PATH is the member's own and names no sdk.
func TestNativeRunHandsTheChildTheBenchGo(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the bench layout is a link into the sdk tree")
	}
	bin := nativeHarness(t)
	home := t.TempDir()
	sdkBin := filepath.Join(home, "sdk", "go1.26.6", "bin")
	require.NoError(t, os.MkdirAll(sdkBin, 0o755))
	for _, tool := range []string{"go", "gofmt"} {
		require.NoError(t, testbin.WriteExecutable(filepath.Join(sdkBin, tool), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(home, "sdk", "bin"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(sdkBin, "go"), filepath.Join(home, "sdk", "bin", "go")))
	want, err := filepath.EvalSymlinks(sdkBin)
	require.NoError(t, err)
	require.NotContains(t, filepath.SplitList(os.Getenv("PATH")), want, "the test's own PATH must not name the fake sdk")

	root, slot := aSlot(t)
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "benchgo-lbl", benchHome: home,
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "the run exits 0, got %d:\n%s", code, errOut.String())
	raw, err := os.ReadFile(filepath.Join(slot, "native-argv.log"))
	require.NoError(t, err, "the run recorded no native-argv.log")
	path := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(line, "env: PATH="); ok {
			path = v
		}
	}
	require.NotEmpty(t, path, "the child was handed no PATH:\n%s", raw)
	assert.Contains(t, filepath.SplitList(path), want, "the child's PATH %q does not carry the bench's sdk Go %s", path, want)
	assert.Contains(t, filepath.SplitList(path), filepath.Join(home, "sdk", "bin"), "the child's PATH %q does not carry the bench's ~/sdk/bin", path)
}

// TestNativeRefusesABudgetNothingCanObserve is the heart of slice 2. Each case names what
// the caller asked for and what the bench could offer, and every one of them is
// `NATIVE REFUSED` at exit 2 with nothing made.
func TestNativeRefusesABudgetNothingCanObserve(t *testing.T) {
	bin := nativeHarness(t)
	t.Setenv("CAP_BUDGET_ENV", "a fake key")
	for _, tc := range []struct {
		name      string
		tokens    string
		usage     string // "" means no --worker at all, which is `opencode` by rule 13d
		maxTurns  int
		maxCache  int
		noSQLite  bool
		wantRefus bool
		names     string // a word the refusal must carry
	}{
		// A NUMBER BESIDE A SOURCE THAT REPORTS NOTHING.
		{name: "numeric_beside_usage_none", tokens: "100000", usage: swarm.UsageNone, wantRefus: true, names: "usage"},
		// A NUMBER ON A BENCH WITH NO READER. There is no --worker here, so the source is
		// `opencode` by rule 13d -- and `opencode` is read with sqlite3.
		{name: "numeric_without_sqlite3", tokens: "100000", noSQLite: true, wantRefus: true, names: swarm.SQLiteBinary},
		// THE CARD'S OWN BUDGET IS READ FROM THE SAME SOURCE, so it meets the same refusal
		// WHATEVER --tokens says: decision 19's careless caller, who typed `unmetered` and
		// a max_turns and would otherwise have had a cap nothing enforced.
		{name: "max_turns_unmetered_beside_usage_none", tokens: "unmetered", usage: swarm.UsageNone, maxTurns: 40, wantRefus: true, names: "max_turns"},
		{name: "max_cache_read_unmetered_beside_usage_none", tokens: "unmetered", usage: swarm.UsageNone, maxCache: 900000, wantRefus: true, names: "max_cache_read"},
		{name: "max_turns_unmetered_without_sqlite3", tokens: "unmetered", usage: swarm.UsageOpenCode, maxTurns: 40, noSQLite: true, wantRefus: true, names: "max_turns"},
		// AND `unmetered` WITH NO SUCH DESCRIPTION RUNS UNDER BOTH, "as it does today".
		{name: "unmetered_beside_usage_none_runs", tokens: "unmetered", usage: swarm.UsageNone},
		{name: "unmetered_without_sqlite3_runs", tokens: "unmetered", usage: swarm.UsageOpenCode, noSQLite: true},
		{name: "unmetered_no_worker_without_sqlite3_runs", tokens: "unmetered", noSQLite: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, slot := aSlot(t)
			card := budgetCard(t, root)
			args := budgetNativeArgs(t, bin, card, slot, root, tc.tokens)
			if tc.usage != "" || tc.maxTurns > 0 || tc.maxCache > 0 {
				usage := tc.usage
				if usage == "" {
					usage = swarm.UsageOpenCode
				}
				args = append(args, "--worker", budgetWorker(t, usage, tc.maxTurns, tc.maxCache))
			}
			// The PATH is emptied LAST, after every fixture that needed a real one has
			// been built, so the only thing this takes away is the usage reader.
			if tc.noSQLite {
				noSQLiteOnPath(t)
			}
			var stdout, stderr bytes.Buffer
			rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
			if !tc.wantRefus {
				require.Equal(t, 0, rc, "this launch runs, got exit %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
				return
			}
			require.Equal(t, 2, rc, "this launch is NATIVE REFUSED at exit 2, got %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
			assert.Contains(t, stderr.String(), "NATIVE REFUSED", "the refusal is a NATIVE REFUSED line:\n%s", stderr.String())
			assert.True(t, tc.names == "" || strings.Contains(stderr.String(), tc.names), "the refusal names %q:\n%s", tc.names, stderr.String())
			madeNothing(t, slot)
		})
	}
}

// TestNativeUsageIntervalFloorAndCeiling: rule 13d, "On `native` an interval under one
// second, or one not shorter than `--deadline`, is refused at exit 2: the first could end
// an honest card on three quick reads, and under the second no sample would ever run."
func TestNativeUsageIntervalFloorAndCeiling(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	for _, tc := range []struct {
		name     string
		interval string
		deadline string
		want     int
	}{
		{"under_a_second", "900ms", "30s", 2},
		{"exactly_the_deadline", "30s", "30s", 2},
		{"longer_than_the_deadline", "45s", "30s", 2},
		{"a_second_exactly_is_the_floor", "1s", "30s", 0},
		{"shorter_than_the_deadline_runs", "2s", "30s", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, slot := aSlot(t)
			card := budgetCard(t, root)
			args := budgetNativeArgs(t, bin, card, slot, root, "unmetered")
			// The deadline the case names replaces the helper's own.
			for i := range args {
				if args[i] == "--deadline" {
					args[i+1] = tc.deadline
				}
			}
			args = append(args, "--usage-interval", tc.interval)
			var stdout, stderr bytes.Buffer
			rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
			require.Equal(t, tc.want, rc, "--usage-interval %s beside --deadline %s is exit %d, got %d\nstdout:\n%s\nstderr:\n%s",
				tc.interval, tc.deadline, tc.want, rc, stdout.String(), stderr.String())
			if tc.want == 2 {
				assert.Contains(t, stderr.String(), "--usage-interval", "the refusal names --usage-interval:\n%s", stderr.String())
				madeNothing(t, slot)
			}
		})
	}
}

// TestNativeRefusesWithoutTheBudgetWord: rule 13d, "The word is required on every launch."
// `native` takes `--tokens <n>` or `--tokens unmetered`; without it the verb is exit 2
// naming the flag, and `0` is refused, exactly as on `add`.
//
// AND IT MAKES NO DIRECTORY. The refusal is a flag check and it stands above everything
// `native` does to the disk -- the job directory at native.go's MkdirAll, the lease it
// takes there, the data home, the temp directory. A refusal that had already made a
// directory would leave the bench's hygiene pass a job that never ran.
func TestNativeRefusesWithoutTheBudgetWord(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	for _, tc := range []struct {
		name   string
		tokens string
		words  []string
	}{
		{"absent", "", []string{"--tokens"}},
		{"zero", "0", []string{"--tokens"}},
		// FULL STRING, not a numeric prefix (HOLD on PR #2131): parseInt's
		// Sscanf("%d") accepted "50oops" as 50, made the job tree, and printed
		// budget=-/50. The word is a positive integer in full or the exact word
		// unmetered.
		{"malformed", "50oops", []string{"--tokens", "50oops"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, slot := aSlot(t)
			card := budgetCard(t, root)
			var stdout, stderr bytes.Buffer
			rc := run(budgetNativeArgs(t, bin, card, slot, root, tc.tokens),
				strings.NewReader(""), &stdout, &stderr, time.Now())
			require.Equal(t, 2, rc, "a native launch with tokens=%q is exit 2, got %d\nstdout:\n%s\nstderr:\n%s",
				tc.tokens, rc, stdout.String(), stderr.String())
			for _, w := range tc.words {
				assert.Contains(t, stderr.String(), w, "the refusal names %q:\n%s", w, stderr.String())
			}
			// NO DIRECTORY WAS MADE. The job directory, the data home and the temp
			// directory are all the run's own, and none of them exists after a refusal
			// this early.
			for _, made := range []string{
				filepath.Join(slot, "jobs"),
				filepath.Join(slot, "data"),
				filepath.Join(slot, "tmp"),
			} {
				_, err := os.Stat(made)
				assert.Error(t, err, "the refusal made %s; rule 13d refuses before any directory is made", made)
			}
		})
	}
}

// TestNativeUnmeteredPrintsTheWordOnTheLine: rule 13d, "`NATIVE OK` always carries
// `budget=`, which is `budget=unmetered`, or rule 13's three spellings against the
// number." A card launched `--tokens unmetered` runs to its deadline and says so.
func TestNativeUnmeteredPrintsTheWordOnTheLine(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	card := budgetCard(t, root)
	var stdout, stderr bytes.Buffer
	rc := run(budgetNativeArgs(t, bin, card, slot, root, "unmetered"),
		strings.NewReader(""), &stdout, &stderr, time.Now())
	require.Equal(t, 0, rc, "an unmetered native launch exits 0, got %d:\n%s", rc, stderr.String())
	require.Contains(t, stdout.String(), " budget=unmetered ", "NATIVE OK carries budget=unmetered:\n%s", stdout.String())
}

// TestNativeNumericBudgetPrintsAgainstTheNumber: the same line under a number. Until a
// sample has observed anything the spelling is rule 13's own dash -- "`budget=-/<n>` for a
// job whose usage was never observed" -- and it is never silence and never `unmetered`.
func TestNativeNumericBudgetPrintsAgainstTheNumber(t *testing.T) {
	t.Parallel()

	// A NUMERIC BUDGET WANTS A READER (rule 13d, and this repo's slice 2): on a bench with
	// no `sqlite3` the same launch is a NATIVE REFUSED, which is the rule working and not
	// this assertion failing. The skip names the missing program rather than pretending.
	if !swarm.SQLiteOnPath() {
		t.Skipf("%s is not on PATH, and a numeric budget is refused without it (rule 13d)", swarm.SQLiteBinary)
	}
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	card := budgetCard(t, root)
	var stdout, stderr bytes.Buffer
	rc := run(budgetNativeArgs(t, bin, card, slot, root, "50000"),
		strings.NewReader(""), &stdout, &stderr, time.Now())
	require.Equal(t, 0, rc, "a numeric native launch exits 0, got %d:\n%s", rc, stderr.String())
	require.Contains(t, stdout.String(), "/50000 ", "NATIVE OK carries budget=<spent|n+|->/50000:\n%s", stdout.String())
	require.NotContains(t, stdout.String(), " budget=unmetered", "a numeric budget never prints unmetered:\n%s", stdout.String())
}

// TestNativeBudgetSitsWhereTheGrammarPutsIt: the output grammar (SPEC-SWARM.md:1946) puts
// `budget=` immediately after `harness=<ok|silent>` and ahead of every optional tail, so a
// reader parses one fixed line. The position is the contract, not merely the presence.
func TestNativeBudgetSitsWhereTheGrammarPutsIt(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	card := budgetCard(t, root)
	var stdout, stderr bytes.Buffer
	rc := run(budgetNativeArgs(t, bin, card, slot, root, "unmetered"),
		strings.NewReader(""), &stdout, &stderr, time.Now())
	require.Equal(t, 0, rc, "the launch exits 0, got %d:\n%s", rc, stderr.String())
	line := nativeOKLine(t, stdout.String())
	fields := strings.Fields(line)
	for i, f := range fields {
		if !strings.HasPrefix(f, "harness=") {
			continue
		}
		require.Less(t, i+1, len(fields), "budget= follows harness= on the NATIVE OK line (grammar, SPEC-SWARM.md:1946):\n%s", line)
		require.True(t, strings.HasPrefix(fields[i+1], "budget="), "budget= follows harness= on the NATIVE OK line (grammar, SPEC-SWARM.md:1946):\n%s", line)
		return
	}
	t.Fatalf("the NATIVE OK line carries no harness= field:\n%s", line)
}

// TestNativeDeclaresTheRouteModelInTheJobConfig: the config the harness starts with names
// the launch's model under its provider, so a harness whose catalog does not know the
// model (a fresh data home whose catalog fetch lost the race to the built-in snapshot)
// still finds it. The provider here is a three-part model id, as an openrouter route is.
func TestNativeDeclaresTheRouteModelInTheJobConfig(t *testing.T) {
	t.Parallel()
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/x-ai/grok-4.7", label: "catalog",
		card: []byte("FAKE-PWD\n"), slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "%s", errOut.String())
	raw, err := os.ReadFile(filepath.Join(slot, "data", ".config", "opencode", "opencode.json"))
	require.NoError(t, err, "the job's harness config")
	var cfg struct {
		Provider map[string]struct {
			Models map[string]json.RawMessage `json:"models"`
		} `json:"provider"`
	}
	require.NoError(t, json.Unmarshal(raw, &cfg), "%s", raw)
	_, declared := cfg.Provider["fake"].Models["x-ai/grok-4.7"]
	assert.True(t, declared, "the route's model is not declared under its provider:\n%s", raw)
}

// ISSUE #1923. `native` checks that the SLOT is under the root and then joins the
// card's LABEL into <slot>/jobs/<label> and <slot>/tmp/<label> with nothing asked of
// it. Those two directories are MkdirAll'd, the first is leased (a publish and an
// os.Remove of `.lease` inside it), and both are handed to the wall as --write. A
// label of `../../../OUTSIDE` therefore names a directory outside the swarm root to
// make, to delete inside, and to give the card write access to.
//
// The bound this test crosses: a label is a NAME, and every path this run derives
// from it stays strictly below the slot, which stays below the root. The honest
// label in the same table is here so a fix that refuses every label fails too.
func TestNativeRefusesALabelThatWalksOutOfTheSwarmRoot(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	for _, label := range []string{
		"../../../OUTSIDE",
		"..",
		"a/b",
		"-rf",
	} {
		t.Run(label, func(t *testing.T) {
			base := t.TempDir()
			root := filepath.Join(base, "swarm-root")
			slot := filepath.Join(root, "1")
			require.NoError(t, os.MkdirAll(slot, 0o755))
			outside := filepath.Join(base, "OUTSIDE")
			require.NoError(t, os.MkdirAll(outside, 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(outside, "keep"), []byte("not the card's\n"), 0o644))

			var errOut bytes.Buffer
			_, code := nativeRun(nativeRunConfig{
				binary: bin, model: "fake/fake-model", label: label,
				card:    []byte("a card\n"),
				slotDir: slot, root: root, deadline: time.Minute, noWall: true,
			}, &errOut)

			assert.Equal(t, 2, code, "a label that is a path exits 2, got %d:\n%s", code, errOut.String())
			assert.Contains(t, errOut.String(), "NATIVE REFUSED", "the refusal is one REFUSED line, got:\n%s", errOut.String())
			assert.Contains(t, errOut.String(), "label", "the refusal does not name the label:\n%s", errOut.String())
			// Nothing was made, leased or removed outside the root.
			_, err := os.Lstat(filepath.Join(outside, ".lease"))
			assert.Error(t, err, "a lease was published outside the swarm root at %s", outside)
			_, err = os.Lstat(filepath.Join(outside, "keep"))
			assert.NoError(t, err, "the bytes outside the swarm root did not survive")
			entries, err := os.ReadDir(base)
			require.NoError(t, err)
			for _, e := range entries {
				assert.Contains(t, []string{"swarm-root", "OUTSIDE"}, e.Name(), "the run made %s beside the swarm root", filepath.Join(base, e.Name()))
			}
		})
	}
}

// The same admission with an honest label still makes the job directory, so the
// refusal above is about the label being a path and not about labels.
func TestNativeStillAcceptsAnOrdinaryLabel(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	base := t.TempDir()
	root := filepath.Join(base, "swarm-root")
	slot := filepath.Join(root, "1")
	require.NoError(t, os.MkdirAll(slot, 0o755))
	write(t, filepath.Join(root, "identity.tsv"),
		"owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "card-1.a_b",
		card:    []byte("a card\n"),
		slotDir: slot, root: root, deadline: time.Minute, noWall: true,
	}, &errOut)
	require.False(t, code == 2 && strings.Contains(errOut.String(), "not a job name"), "an ordinary label was refused as a path:\n%s", errOut.String())
	_, err := os.Stat(filepath.Join(slot, "jobs", "card-1.a_b"))
	require.NoError(t, err, "the honest job directory was not made")
}

// TestNativeLaunchGoesThroughTheOneLauncher is the call site #2646 left open (the
// hold): a native launch builds its harness argv with swarm.LaunchArgvFor, the providers
// table's one launcher, and the argv the child is handed is exactly the one it returned.
// A provider the table names launches with its own row; one it does not launches with the
// table's declared default row, never a literal argv in the caller. RED WITHOUT THE WIRING:
// native built `run --model <m> --title <l> -- <card>` inline and never asked the table.
func TestNativeLaunchGoesThroughTheOneLauncher(t *testing.T) {
	bin := nativeHarness(t)
	for _, tc := range []struct{ model, row string }{
		{"fake/fake-model", swarm.DefaultLaunchRow},
		{"opencode/deepseek-v4-flash", "opencode"},
	} {
		t.Run(tc.row, func(t *testing.T) {
			root, slot := aSlot(t)
			type call struct {
				provider string
				req      swarm.LaunchRequest
				argv     []string
			}
			var calls []call
			orig := launchArgvFor
			t.Cleanup(func() { launchArgvFor = orig })
			launchArgvFor = func(provider, goos string, req swarm.LaunchRequest) ([]string, error) {
				argv, err := orig(provider, goos, req)
				calls = append(calls, call{provider, req, argv})
				return argv, err
			}

			card := "a card\n"
			var errOut bytes.Buffer
			_, code := nativeRun(nativeRunConfig{
				binary: bin, model: tc.model, label: "launcher-lbl",
				card: []byte(card), slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
			}, &errOut)
			require.Equal(t, 0, code, "the run exits 0, got %d:\n%s", code, errOut.String())
			require.Len(t, calls, 1, "the launch did not go through swarm.LaunchArgvFor: %d calls, want 1", len(calls))
			c := calls[0]
			assert.Equal(t, tc.row, c.provider, "the launch asked the table for row %q, want %q", c.provider, tc.row)
			assert.Equal(t, bin, c.req.Harness, "the launch request is %+v, want harness %s, model %s, title launcher-lbl and the card as prompt", c.req, bin, tc.model)
			assert.Equal(t, tc.model, c.req.Model, "the launch request is %+v, want harness %s, model %s, title launcher-lbl and the card as prompt", c.req, bin, tc.model)
			assert.Equal(t, "launcher-lbl", c.req.Title, "the launch request is %+v, want harness %s, model %s, title launcher-lbl and the card as prompt", c.req, bin, tc.model)
			assert.Equal(t, card, c.req.Prompt, "the launch request is %+v, want harness %s, model %s, title launcher-lbl and the card as prompt", c.req, bin, tc.model)
			raw, err := os.ReadFile(filepath.Join(slot, "native-argv.log"))
			require.NoError(t, err, "the run recorded no native-argv.log: %v", err)
			want := "argv: " + oneline.Escape(strings.Join(c.argv, " "))
			assert.Contains(t, string(raw), want+"\n", "the child was not handed the one launcher's argv; want line %q in:\n%s", want, raw)
		})
	}
}

// TestNativeRunPhasesAreTheOldOrder verifies that nativeRun decomposes into
// the named phase functions (prepare, wall, start, watch, collect, report)
// and executes them in that exact linear order.
func TestNativeRunPhasesAreTheOldOrder(t *testing.T) {
	t.Parallel()

	wantPhases := []string{"prepare", "wall", "start", "watch", "collect", "report"}

	bin := nativeHarness(t)
	_, slot := aSlot(t)
	root := filepath.Dir(slot)

	var recorded []string
	cfg := nativeRunConfig{
		binary:   bin,
		model:    "fake/fake-model",
		label:    "phase-order-test",
		card:     []byte("test card\n"),
		slotDir:  slot,
		root:     root,
		deadline: 30 * time.Second,
		noWall:   true,
		onPhase: func(phase string) {
			recorded = append(recorded, phase)
		},
	}
	var errOut bytes.Buffer
	res, code := nativeRun(cfg, &errOut)
	require.Equal(t, 0, code, "nativeRun failed: %s", errOut.String())
	require.Equal(t, 0, res.rc, "child did not succeed")
	assert.Equal(t, wantPhases, recorded, "phases did not execute in the expected order")
}

// The run of the report: stream errors in the harness's log, a clean exit, no result. The
// reason is the first error line this run wrote, never an older run's; the verdict line is
// still INCOMPLETE.
func TestAProviderErrorInTheHarnessLogIsAProviderFailure(t *testing.T) {
	t.Parallel()
	out, errb := providerRun(t, "pf1", "FAKE-STREAM-ERROR\nFAKE-NORESULT\n", olderRunsError)
	assert.Contains(t, errb, "NATIVE PROVIDER-FAIL label=pf1 ")
	assert.Contains(t, errb, ` reason=provider: class=provider-5xx status=- msg=Streaming response failed: [internal_error] Stream error: h2 protocol error`)
	assert.NotContains(t, errb, "an older run's", "the log is read from where this run began")
	assert.NotContains(t, errb, "timestamp=", "the line is the message, not its timestamp")
	assert.Contains(t, out, "NATIVE INCOMPLETE ")
	assert.NotContains(t, out, "NATIVE OK", "the verdict line is unchanged: the run delivered nothing")
	assert.Equal(t, 1, strings.Count(errb, "NATIVE PROVIDER-FAIL"), "one line, the first error")
}

// A non-retryable provider refusal at launch is a provider failure (nova-tools#5199), in
// the shape of slot ci-03.w246.g4 of 2026-10-03: rc=1 within two seconds, the data-home log
// empty, the error only in the printed output, the 402 in the session.
func TestANonRetryableProviderRefusalAtLaunchIsAProviderFailure(t *testing.T) {
	t.Parallel()
	out, errb := providerRun(t, "pf7", "FAKE-CREDIT-REFUSAL\n", nil)
	assert.Equal(t, 1, strings.Count(errb, "NATIVE PROVIDER-FAIL label=pf7 "), errb)
	status := "-" // the printed output names no status
	if swarm.SQLiteOnPath() {
		status = "402" // the session's record keeps the provider's
	}
	assert.Contains(t, errb, " reason=provider: class=out-of-credit status="+status+" msg=Insufficient credits. Add more using https://openrouter.test/settings/credits\n")
	assert.Contains(t, out, "NATIVE INCOMPLETE ")
	assert.Equal(t, member.EndProvider, nativeEnd([]byte(errb)), "the member finishes the take as the provider's, never no result")
}

// Only the provider's refusal for credit in the session makes a run a provider failure by
// itself: a session error of another class (MessageOutputLengthError), on a run that exited
// 1 with nothing printed and nothing in the log, is not the provider's.
func TestASessionErrorThatIsNotARefusalForCreditIsNotAProviderFailureByItself(t *testing.T) {
	t.Parallel()
	needsSQLite(t)
	out, errb := providerRun(t, "pf8", "FAKE-SESSION-ERROR-EXIT\n", nil)
	assert.NotContains(t, errb, "PROVIDER-FAIL", "the session's error keeps its class: not the provider's")
	assert.Contains(t, out, "NATIVE INCOMPLETE ")
}

// The second trigger: a clean exit after a tool result, with no error line anywhere.
func TestARunThatEndsOnAToolResultIsAProviderFailure(t *testing.T) {
	t.Parallel()
	needsSQLite(t)
	_, errb := providerRun(t, "pf2", "FAKE-ENDS-ON-TOOL\nFAKE-NORESULT\n", nil)
	assert.Contains(t, errb, "NATIVE PROVIDER-FAIL label=pf2 ")
	assert.Contains(t, errb, " reason=provider: class=other status=- msg=ended without a final message")
}

// The session's own record of the failed message is the cause when it has one: the
// provider's status and words, the class they say, and no key-shaped value.
func TestTheSessionsRecordOfTheFailedMessageIsTheCause(t *testing.T) {
	t.Parallel()
	needsSQLite(t)
	_, errb := providerRun(t, "pf6", "FAKE-SESSION-ERROR\nFAKE-NORESULT\n", nil)
	assert.Contains(t, errb, "NATIVE PROVIDER-FAIL label=pf6 ")
	assert.Contains(t, errb, " reason=provider: class=out-of-credit status=402 msg=Insufficient credits. key [redacted]\n")
	assert.NotContains(t, errb, "abcdefghij", "a key-shaped value never reaches the line")
}

// A run that ends with a final assistant message and no result is the card's, as today.
func TestARunThatEndsWithAFinalMessageAndNoResultStaysNoResult(t *testing.T) {
	t.Parallel()
	needsSQLite(t)
	out, errb := providerRun(t, "pf3", "FAKE-ENDS-ON-FINAL\nFAKE-NORESULT\n", nil)
	assert.NotContains(t, errb, "PROVIDER-FAIL")
	assert.Contains(t, out, "NATIVE INCOMPLETE ")
	assert.NotContains(t, out, "NATIVE OK")
}

// A run with no error and no result, and no record of a session, stays no-result.
func TestARunWithNoErrorsAndNoResultStaysNoResult(t *testing.T) {
	t.Parallel()
	out, errb := providerRun(t, "pf4", "FAKE-NORESULT\n", olderRunsError)
	assert.NotContains(t, errb, "PROVIDER-FAIL")
	assert.Contains(t, out, "NATIVE INCOMPLETE ")
	assert.NotContains(t, out, "NATIVE OK")
}

// A provider error in the log beside a published result is not a failure: the card
// finished (and the transcript that stops on a tool is no failure either).
func TestAProviderErrorBesideAResultIsNotAFailure(t *testing.T) {
	t.Parallel()
	needsSQLite(t)
	out, errb := providerRun(t, "pf5", "FAKE-STREAM-ERROR\nFAKE-ENDS-ON-TOOL\nFAKE-RESULT ok\n", nil)
	assert.NotContains(t, errb, "PROVIDER-FAIL")
	assert.Contains(t, out, "NATIVE OK ")
}

// TestControlCardResultsSurviveSweep is issue #2632. A control card's RESULT.md,
// usage.tsv and report are published under
// <results-root>/<label>/<runID>/<attempt>/, which is not the job directory.
// --sweep-now leaves that directory intact and the job directory gone. nova-pulse
// status reads the spend from the results root, not from the job that was deleted.
func TestControlCardResultsSurviveSweep(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	t.Run("sweep-now", func(t *testing.T) {
		controlCardSurvivesSweep(t, true)
	})
}

// TestTwoInvocationsPreserveResults is the preservation control for a repeated
// label. Attempt numbers restart at 1 every invocation, so the run directory
// has to be the identity. Both sweeps run, and each run's report, RESULT.md
// and usage.tsv stay intact and unmixed.
func TestTwoInvocationsPreserveResults(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "control"
	resultsRoot := filepath.Join(t.TempDir(), "results")
	store := nativeStore(t)
	for _, card := range []struct{ say, findings string }{
		{"run-one-report", "1"},
		{"run-two-report", "2"},
	} {
		cardPath := filepath.Join(root, "card-"+card.findings+".md")
		body := fmt.Sprintf("FAKE-SAY %s\nFAKE-FINDINGS %s\n", card.say, card.findings)
		require.NoError(t, os.WriteFile(cardPath, []byte(body), 0o644))
		var stdout, stderr bytes.Buffer
		rc := run([]string{"native", "--tokens", "unmetered", "--slots-store", store, "--owner", "fake-1",
			"--harness", bin, "--model", "fake/fake-model", "--label", label,
			"--card", cardPath, "--slot", slot, "--root", root,
			"--deadline", "30s", "--idle", "0", "--no-wall",
			"--results-root", resultsRoot, "--sweep-now"},
			strings.NewReader(""), &stdout, &stderr, time.Now())
		require.Equal(t, 0, rc, "invocation findings=%s exits 0, got %d\n%s\n%s", card.findings, rc, stdout.String(), stderr.String())
	}
	job := filepath.Join(slot, "jobs", label)
	_, err := os.Stat(job)
	require.True(t, os.IsNotExist(err), "the job directory must be gone after both sweeps, stat=%v", err)
	runs := runDirs(t, resultsRoot, label)
	require.Len(t, runs, 2, "two invocations need two run directories, got %v", runs)
	require.NotEqual(t, runs[0], runs[1], "the run ids collided: %v", runs)
	seenSay := map[string]bool{}
	seenFindings := map[string]bool{}
	for _, id := range runs {
		attempt := filepath.Join(resultsRoot, label, id, "1")
		report, err := os.ReadFile(filepath.Join(attempt, "report"))
		require.NoError(t, err, "run %s report", id)
		result, err := os.ReadFile(filepath.Join(attempt, "RESULT.md"))
		require.NoError(t, err, "run %s RESULT.md", id)
		n := usageDataRows(t, filepath.Join(attempt, "usage.tsv"))
		require.Equal(t, 1, n, "run %s usage has %d data rows, want 1 (a second invocation must not append here)", id, n)
		switch {
		case strings.Contains(string(report), "run-one-report"):
			seenSay["one"] = true
			require.NotContains(t, string(report), "run-two-report", "run %s report mixes both invocations:\n%s", id, report)
		case strings.Contains(string(report), "run-two-report"):
			seenSay["two"] = true
			require.NotContains(t, string(report), "run-one-report", "run %s report mixes both invocations:\n%s", id, report)
		default:
			t.Fatalf("run %s report is neither invocation:\n%s", id, report)
		}
		switch {
		case strings.Contains(string(result), "findings: 1\n"):
			seenFindings["1"] = true
			require.NotContains(t, string(result), "findings: 2\n", "run %s RESULT mixes both invocations:\n%s", id, result)
		case strings.Contains(string(result), "findings: 2\n"):
			seenFindings["2"] = true
			require.NotContains(t, string(result), "findings: 1\n", "run %s RESULT mixes both invocations:\n%s", id, result)
		default:
			t.Fatalf("run %s RESULT lost its findings line:\n%s", id, result)
		}
	}
	require.True(t, seenSay["one"], "both reports and both results must survive, says=%v findings=%v", seenSay, seenFindings)
	require.True(t, seenSay["two"], "both reports and both results must survive, says=%v findings=%v", seenSay, seenFindings)
	require.True(t, seenFindings["1"], "both reports and both results must survive, says=%v findings=%v", seenSay, seenFindings)
	require.True(t, seenFindings["2"], "both reports and both results must survive, says=%v findings=%v", seenSay, seenFindings)
}

// TestPublicationFailureKeepsTheJob is the capture-failure control. The harness
// unlinks harness-output.log, so the required report cannot be published.
// --sweep-now must leave the job directory in place.
func TestPublicationFailureKeepsTheJob(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "control"
	resultsRoot := filepath.Join(t.TempDir(), "results")
	cardPath := filepath.Join(root, "card.md")
	require.NoError(t, os.WriteFile(cardPath, []byte("FAKE-DROP-CAPTURE\nFAKE-SAY kept-in-the-job\n"), 0o644))
	var stdout, stderr bytes.Buffer
	rc := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--idle", "0", "--no-wall",
		"--results-root", resultsRoot, "--sweep-now"},
		strings.NewReader(""), &stdout, &stderr, time.Now())
	require.Equal(t, 0, rc, "the card still exits 0, got %d\n%s\n%s", rc, stdout.String(), stderr.String())
	require.Contains(t, stderr.String(), "the report could not be published", "a missing capture must be named:\n%s", stderr.String())
	require.Contains(t, stderr.String(), "left", "--sweep-now must say it left the job in place:\n%s", stderr.String())
	require.Contains(t, stderr.String(), "in place", "--sweep-now must say it left the job in place:\n%s", stderr.String())
	job := filepath.Join(slot, "jobs", label)
	_, err := os.Stat(job)
	require.NoError(t, err, "the job directory must be kept when the report cannot be published")
	_, err = os.Stat(filepath.Join(job, "RESULT.md"))
	require.NoError(t, err, "the job still holds RESULT.md")
	_, err = os.Stat(filepath.Join(job, "harness-output.log"))
	require.True(t, os.IsNotExist(err), "the capture was dropped, stat=%v", err)
	_ = filepath.WalkDir(resultsRoot, func(path string, d os.DirEntry, err error) error {
		assert.False(t, err == nil && d.Name() == "report", "a failed publish still wrote %s", path)
		return nil
	})
}

// ISSUE #1585, as two `native` runs in one physical job directory.
//
// The bench store gives two concurrent runs different SEATS, and #1562's identity repair
// makes those seats correct. Their `<slot>/jobs/<label>` was still ONE PATH, and so were
// the data home, the temp directory and the logs under it. The first run to exit removed
// `<job>/.lease`, and the second -- alive, still working -- lost its reaper protection,
// because its heartbeat was a bare Chtimes that cannot restore a file that is gone.
//
// SPEC-SWARM settles which repair is right before either is written. Under **Slots** a
// worker has "its own data home", "its own job directory", and "a slot is held by exactly
// one worker"; under **the races, taken out**, two workers on one data home is the
// 2026-09-10 `database is locked` failure, closed on purpose. Two live runs in one job
// directory is therefore not a thing to make safe: it is a thing to REFUSE. The lease is
// also pid-fenced (pkg/swarm, TestAReleaseNeverRemovesAnotherRunsLease), so that a
// release can never remove a lease it did not take even when a hand, an older binary or a
// bench script has freed the path.
//
// THE BARRIER IS THE NOTE FILE, not a duration: the first run's harness blocks on
// `FAKE-AWAIT-NOTE` until this test writes `<job>/note`, so the overlap window is exactly
// as long as the assertions take and no run here waits on a chosen number of seconds.
func TestASecondNativeRunInOneJobDirectoryIsRefusedAndTheFirstIsUntouched(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	const label = "shared-label"
	jobDir := filepath.Join(slot, "jobs", label)
	lease := filepath.Join(jobDir, swarm.JobLeaseName)

	// RUN A: it takes the job directory and then blocks in its harness on the note.
	type outcome struct {
		code int
		err  string
	}
	first := make(chan outcome, 1)
	go func() {
		var errOut bytes.Buffer
		_, code := nativeRun(nativeRunConfig{
			binary: bin, model: "fake/fake-model", label: label,
			card:    []byte("FAKE-AWAIT-NOTE 120\n"),
			slotDir: slot, root: root, deadline: 2 * time.Minute, noWall: true,
		}, &errOut)
		first <- outcome{code: code, err: errOut.String()}
	}()

	// A holds the directory the moment its lease is on disk. That file IS the readiness
	// signal: it is written before the child starts and it is what the reaper reads.
	held := awaitLease(t, lease)
	require.Contains(t, held, "label="+label+"\n", "A's lease does not name its card:\n%s", held)

	// RUN B: the same slot, the same label, the same physical directory.
	var errB bytes.Buffer
	_, codeB := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card:    []byte("a second card\n"),
		slotDir: slot, root: root, deadline: 2 * time.Minute, noWall: true,
	}, &errB)

	assert.Equal(t, 2, codeB, "a second run in a live job directory exits 2, got %d:\n%s", codeB, errB.String())
	assert.Contains(t, errB.String(), "NATIVE REFUSED", "the refusal is one REFUSED line, got:\n%s", errB.String())
	assert.Contains(t, errB.String(), "held by a live run", "the refusal does not say why:\n%s", errB.String())
	assert.Contains(t, errB.String(), "pid=", "the refusal does not name the holder:\n%s", errB.String())
	lines := strings.Count(strings.TrimSpace(errB.String()), "\n") + 1
	assert.Equal(t, 1, lines, "exactly one REFUSED line, got %d:\n%s", lines, errB.String())

	// AND THE FIRST RUN IS EXACTLY AS IT WAS. The lease is still A's, byte for byte: the
	// refused launch neither truncated it, rewrote it, nor removed it.
	now, err := os.ReadFile(lease)
	require.NoError(t, err, "the refused second run removed the live run's lease (#1585)")
	assert.Equal(t, held, string(now), "the refused second run rewrote the live run's lease:\nbefore:\n%s\nafter:\n%s", held, now)

	// Release the barrier and let A finish on its own terms.
	require.NoError(t, os.WriteFile(filepath.Join(jobDir, "note"), []byte("go on\n"), 0o644))
	got := <-first
	require.Equal(t, 0, got.code, "the first run did not finish cleanly after the second was refused: exit %d\n%s", got.code, got.err)
	// A finished, so ITS release removes ITS lease: a finished job leaves nothing behind
	// that pretends to be alive.
	_, err = os.Lstat(lease)
	assert.True(t, os.IsNotExist(err), "the finished run left its lease behind: %v", err)
}

// SECOND P1 AT THE VERB (#1585, HOLD on 6146897a). With `.lease` a path the
// launcher cannot establish ownership at -- an owned directory, here -- the take used to
// answer "no lease, carry on", and BOTH of two runs were told they held the job directory.
// A run that cannot prove it owns its job directory now does not start, and it says so in
// one line with a remedy, BEFORE it has written anything into the shared place: no data
// home, no temp directory, no harness output, no argv for the wall.
func TestANativeRunThatCannotEstablishOwnershipRefusesBeforeItWritesAnything(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	const label = "unownable"
	jobDir := filepath.Join(slot, "jobs", label)
	require.NoError(t, os.MkdirAll(filepath.Join(jobDir, swarm.JobLeaseName), 0o755))

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card:    []byte("a card\n"),
		slotDir: slot, root: root, deadline: 2 * time.Minute, noWall: true,
	}, &errOut)

	require.Equal(t, 2, code, "a run that cannot take its job lease exits 2, got %d:\n%s", code, errOut.String())
	assert.Contains(t, errOut.String(), "NATIVE REFUSED", "the refusal is one REFUSED line, got:\n%s", errOut.String())
	assert.Contains(t, errOut.String(), "cannot prove it owns its job directory", "the refusal does not say what it could not establish:\n%s", errOut.String())
	assert.Contains(t, errOut.String(), "run it again", "the refusal carries no remedy:\n%s", errOut.String())
	got := strings.Count(strings.TrimSpace(errOut.String()), "\n") + 1
	assert.Equal(t, 1, got, "exactly one REFUSED line, got %d:\n%s", got, errOut.String())

	// NOTHING WAS WRITTEN. The job directory holds what the test put there and not one
	// thing more, and the slot has no data home or temp directory for this label.
	entries, err := os.ReadDir(jobDir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.Equal(t, swarm.JobLeaseName, e.Name(), "the refused run wrote %q into a job directory it does not own", e.Name())
	}
	_, err = os.Stat(filepath.Join(slot, "tmp", label))
	assert.True(t, os.IsNotExist(err), "the refused run made its temp directory anyway: %v", err)
}

// TestNativeRunsWithNoSlotsStore: no --slots-store, no --owner, and a home with no
// nova-bench/slots directory at all. The run is not refused, the card finishes NATIVE OK,
// and no slot store is made on the way past.
func TestNativeRunsWithNoSlotsStore(t *testing.T) {
	bin := nativeHarness(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	write(t, cardPath, "a card\n")

	var stdout, stderr bytes.Buffer
	rc := run([]string{"native", "--tokens", "unmetered", "--harness", bin, "--model", "fake/fake-model",
		"--label", "lbl", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"},
		strings.NewReader(""), &stdout, &stderr, time.Now())
	require.Equal(t, 0, rc, "a native launch with no slot store runs, got exit %d:\n%s%s", rc, stdout.String(), stderr.String())
	require.NotContains(t, stderr.String(), "SLOTS REFUSED", "native refused on a slot ledger it no longer keeps:\n%s", stderr.String())
	require.NotContains(t, stderr.String(), "no_slots_store", "native refused on a slot ledger it no longer keeps:\n%s", stderr.String())
	require.True(t, strings.HasPrefix(stdout.String(), "NATIVE OK "), "the card finishes NATIVE OK:\n%s%s", stdout.String(), stderr.String())
	_, err := os.Stat(filepath.Join(home, "nova-bench", "slots"))
	require.True(t, os.IsNotExist(err), "native made a slot store under the home: %v", err)
}

// TestNativeIgnoresTheRetiredSlotsStore: the batman shape, a store whose one owner holds
// its whole share. A caller that still passes --slots-store and --owner is not refused on
// an unknown flag and is not refused on the full share: the card runs, and the store holds
// exactly the leases it held before.
func TestNativeIgnoresTheRetiredSlotsStore(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	write(t, cardPath, "RESULT: schema-card sha=aaaaaaaaaaaa\nKIND: schema\na schema card\n")
	store := slotShares(t, "capacity\t1\nreserve\t0\nswarm-batman\t1\n")
	// The holder is ALIVE (this test's own pid), so the seat is held and never reaped.
	require.NoError(t, swarm.MakeSlotLease(store, "held-1", "swarm-batman", os.Getpid(), "held", time.Now().UTC().Add(time.Hour)))
	before := slotLeaseCount(t, store)

	var stdout, stderr bytes.Buffer
	rc := run([]string{"native", "--tokens", "unmetered", "--harness", bin, "--model", "fake/fake-model",
		"--label", "schema-card", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall",
		"--slots-store", store, "--owner", "swarm-batman"},
		strings.NewReader(""), &stdout, &stderr, time.Now())
	require.Equal(t, 0, rc, "a dealt card is not refused by the retired file ledger, got exit %d:\n%s%s", rc, stdout.String(), stderr.String())
	require.NotContains(t, stderr.String(), "SLOTS REFUSED", "native refused on the file ledger:\n%s", stderr.String())
	after := slotLeaseCount(t, store)
	require.Equal(t, before, after, "native wrote to the retired store: %d leases before, %d after", before, after)
}

// TestNativeArgvReadsHarnessDir: the wall's argv reads the harness binary's own directory
// and /opt/homebrew (when it exists), so git and the harness's libraries resolve inside the
// wall — the reads the shell launcher made, which the native path of run 7 must make too.
func TestNativeArgvReadsHarnessDir(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	_, slot := aSlot(t)
	jobDir := filepath.Join(slot, "jobs", "a-label")
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	argv := nativeSandboxArgv([]string{bin}, nativeRunConfig{slotDir: slot}, filepath.Join(slot, "data"), jobDir, filepath.Join(slot, "tmp", "a-label"))
	harnessDir := filepath.Dir(bin)
	assert.True(t, hasFlagPair(argv, "--read", harnessDir), "the wall argv does not read the harness directory %s:\n%s", harnessDir, strings.Join(argv, " "))
	if fi, err := os.Stat("/opt/homebrew"); err == nil && fi.IsDir() {
		assert.True(t, hasFlagPair(argv, "--read", "/opt/homebrew"), "the wall argv does not read /opt/homebrew, which exists:\n%s", strings.Join(argv, " "))
	} else {
		assert.False(t, hasFlagPair(argv, "--read", "/opt/homebrew"), "the wall argv reads /opt/homebrew, which is absent:\n%s", strings.Join(argv, " "))
	}
}

// TestNativeArgvReadsTheBenchToolchainRoots is the edge the schema dogfood loop found on
// 2026-09-18, and it is the whole bug in one assertion: the provisioning standard puts Go
// and sbcl under `~/sdk` with `~/go/bin` on PATH and the module cache at `~/go/pkg/mod`,
// the wall named none of them, and `nova-swarm native` pins GOTOOLCHAIN=local -- so every
// Go card on the bench got `Permission denied` on the bench's own go and then
// `go.mod requires go >= 1.26 (running go 1.22.2)` from the only one the wall left it.
// The roots are read-only and come from ONE list (swarm.ToolchainRoots).
func TestNativeArgvReadsTheBenchToolchainRoots(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	_, slot := aSlot(t)
	jobDir := filepath.Join(slot, "jobs", "a-label")
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	// A home of the test's own, with the standard's shape under it, so the assertion is
	// about the argv and not about the machine the test happens to run on.
	// The LINUX list, named rather than taken from the machine, so the assertion is the
	// same on a Mac runner and on a linux one: those are the roots that live under a home.
	// The home RESOLVED, because a root reaches the argv resolved through its symlinks (the
	// wall checks the resolved target) and on a Mac a temp dir is under /var, itself a link
	// to /private/var. Resolving here keeps the assertion about the argv.
	home, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	for _, name := range swarm.ToolchainRootNames("linux") {
		require.NoError(t, os.MkdirAll(filepath.Join(home, filepath.FromSlash(name)), 0o755))
	}
	// Paths that are NOT the toolchain, made before the argv so an argv that named the
	// home or globbed it would carry them.
	var others []string
	for _, name := range []string{".config/nova-secrets", ".ssh"} {
		other := filepath.Join(home, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(other, 0o700))
		others = append(others, other)
	}
	cfg := nativeRunConfig{slotDir: slot, benchHome: home, benchOS: "linux"}
	argv := nativeSandboxArgv([]string{bin}, cfg, filepath.Join(slot, "data"), jobDir, filepath.Join(slot, "tmp", "a-label"))
	// ONE LIST, TWO KINDS. An exec root goes on --read, which carries EXECUTE on both wall
	// bodies; a read-only root goes on --read-noexec, which takes the execute away. The
	// kind is the list's, and each root must be on ITS OWN flag and on no other -- a
	// read-only root that slipped onto --read is exactly the widening the security
	// read of #1364 refused.
	for _, root := range swarm.ToolchainRootList("linux") {
		path := filepath.Join(home, filepath.FromSlash(root.Name))
		want, wrong := "--read-noexec", "--read"
		if root.Exec {
			want, wrong = "--read", "--read-noexec"
		}
		assert.True(t, hasFlagPair(argv, want, path), "the wall argv does not carry the toolchain root %s as %s:\n%s", path, want, strings.Join(argv, " "))
		assert.False(t, hasFlagPair(argv, wrong, path), "the toolchain root %s is on %s, which is the other kind:\n%s", path, wrong, strings.Join(argv, " "))
		assert.False(t, hasFlagPair(argv, "--write", path), "the toolchain root %s is a WRITE; it is read-only:\n%s", path, strings.Join(argv, " "))
	}
	// THE MODULE CACHE BY NAME, because it is the root this argv form was added for: READ
	// WITHOUT EXECUTE, never read+execute. Every `go mod download` on the bench lands
	// there and the bench user can write to it, so a card able to execute out of it could
	// run whatever a dependency shipped.
	modCache := filepath.Join(home, filepath.FromSlash("go/pkg/mod"))
	assert.True(t, hasFlagPair(argv, "--read-noexec", modCache), "the module cache is not granted read-without-execute:\n%s", strings.Join(argv, " "))
	assert.False(t, hasFlagPair(argv, "--read", modCache), "the module cache is on --read, which CARRIES EXECUTE:\n%s", strings.Join(argv, " "))
	// NOTHING ELSE UNDER HOME. The wall gained the toolchain and not the home: the key
	// store and an ssh directory beside it stay outside every named path, on either flag.
	for _, other := range append(others, home) {
		for _, flag := range []string{"--read", "--read-noexec", "--write"} {
			assert.False(t, hasFlagPair(argv, flag, other), "the wall argv names %s on %s, and it is not a toolchain root:\n%s", other, flag, strings.Join(argv, " "))
		}
	}
	// ~/go/bin is granted BY NEITHER KIND (the security read of #1364): every
	// `go install` on the bench lands there and the bench user can write to it. On a
	// provisioned bench ~/go/bin/go is a symlink into the sdk tree and the kernel checks
	// the resolved target, so a card's PATH still finds the granted toolchain.
	goBin := filepath.Join(home, filepath.FromSlash("go/bin"))
	require.NoError(t, os.MkdirAll(goBin, 0o755))
	argv = nativeSandboxArgv([]string{bin}, cfg, filepath.Join(slot, "data"), jobDir, filepath.Join(slot, "tmp", "a-label"))
	for _, flag := range []string{"--read", "--read-noexec", "--write"} {
		assert.False(t, hasFlagPair(argv, flag, goBin), "the wall argv grants ~/go/bin on %s:\n%s", flag, strings.Join(argv, " "))
	}
}

func TestNativeArgvSkipsAToolchainRootThatIsNotThere(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	_, slot := aSlot(t)
	jobDir := filepath.Join(slot, "jobs", "a-label")
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	home := t.TempDir() // empty: not one root exists under it
	argv := nativeSandboxArgv([]string{bin}, nativeRunConfig{slotDir: slot, benchHome: home, benchOS: "linux"}, filepath.Join(slot, "data"), jobDir, filepath.Join(slot, "tmp", "a-label"))
	for i, a := range argv {
		if a != "--read" && a != "--read-noexec" {
			continue
		}
		if i+1 < len(argv) {
			assert.False(t, strings.HasPrefix(argv[i+1], home), "the wall argv names %s under a home with no toolchain:\n%s", argv[i+1], strings.Join(argv, " "))
		}
	}
}

// TestNativeRunRecordsCardAndBinaryHashes: a run that finishes records the child's exit
// code, the wall it took, and the sha256 of the card text and of the binary itself.
func TestNativeRunRecordsCardAndBinaryHashes(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	card := []byte("the card text, byte for byte\nwith a second line\n")

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "a-label",
		card: card, slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "a finished run exits 0, got %d:\n%s", code, errOut.String())
	require.Equal(t, 0, res.rc, "the child exits 0, recorded %d", res.rc)
	require.Positive(t, res.wallSeconds, "the wall is positive, recorded %v", res.wallSeconds)
	wantCardSum := sha256.Sum256(card)
	wantCard := hex.EncodeToString(wantCardSum[:])
	assert.Equal(t, wantCard, res.cardSHA256, "card sha256 is %s, want %s", res.cardSHA256, wantCard)
	wantBinary, err := fileSHA256(bin)
	require.NoError(t, err)
	assert.Equal(t, wantBinary, res.binarySHA256, "binary sha256 is %s, want %s", res.binarySHA256, wantBinary)
}

// TestNativeCarriesProviderConfig: a `--config` opencode.json is carried beside the auth
// file into the job's own data home, mode 0600, and the fake harness sees it at the path it
// resolves from its own XDG data home. The provider's own bytes reach the child unchanged.
//
// ISSUE #644 CHANGED THE OTHER HALF OF THIS: the file is written WHETHER OR NOT --config
// named one, because the job's fence rules live in it, and the sha8 on the NATIVE OK line is
// the sha8 of the bytes the child saw -- the only config a later reader can check the run
// against. Before it, a run with no --config wrote no config and ran on the harness's default
// fence, which auto-rejected the card's own `../scratch`.
func TestNativeCarriesProviderConfig(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	const config = `{"provider":{"fake":{"options":{"baseURL":"http://localhost:11434/v1"}}}}` + "\n"

	t.Run("without_config", func(t *testing.T) {
		root, slot := aSlot(t)
		auth := filepath.Join(t.TempDir(), "auth.json")
		require.NoError(t, os.WriteFile(auth, []byte(`{"fake":"the-fake-secret"}`), 0o600))
		var errOut bytes.Buffer
		_, code := nativeRun(nativeRunConfig{
			binary: bin, model: "fake/fake-model", label: "lbl",
			card: []byte("FAKE-RECORD-CONFIG\n"), slotDir: slot, root: root, authFile: auth,
			deadline: 30 * time.Second, noWall: true,
		}, &errOut)
		require.Equal(t, 0, code, "the run exits 0, got %d:\n%s", code, errOut.String())
		// The config exists even with no --config: it is where the job's fence rules are.
		assertConfigRecord(t, slot, "0600", `"external_directory"`)
	})

	t.Run("with_config", func(t *testing.T) {
		root, slot := aSlot(t)
		auth := filepath.Join(t.TempDir(), "auth.json")
		require.NoError(t, os.WriteFile(auth, []byte(`{"fake":"the-fake-secret"}`), 0o600))
		cfgPath := filepath.Join(t.TempDir(), "opencode.json")
		require.NoError(t, os.WriteFile(cfgPath, []byte(config), 0o644))
		var errOut bytes.Buffer
		res, code := nativeRun(nativeRunConfig{
			binary: bin, model: "fake/fake-model", label: "lbl",
			card: []byte("FAKE-RECORD-CONFIG\n"), slotDir: slot, root: root, authFile: auth,
			configFile: cfgPath, deadline: 30 * time.Second, noWall: true,
		}, &errOut)
		require.Equal(t, 0, code, "the run exits 0, got %d:\n%s", code, errOut.String())
		assertConfigRecord(t, slot, "0600", `"baseURL": "http://127.0.0.1:`)
		copied := filepath.Join(slot, "data", ".config", "opencode", "opencode.json")
		st, err := os.Stat(copied)
		require.NoError(t, err, "the config copy was not written")
		assert.Equal(t, os.FileMode(0o600), st.Mode().Perm(), "the config copy is mode %04o, want 0600", st.Mode().Perm())
		body, err := os.ReadFile(copied)
		require.NoError(t, err)
		wantSum := sha256.Sum256(body)
		wantSHA := hex.EncodeToString(wantSum[:])[:8]
		assert.Equal(t, wantSHA, res.configSHA, "the run records the sha8 of the bytes the child saw: %q, want %q", res.configSHA, wantSHA)
		assert.Contains(t, string(body), `"baseURL": "http://127.0.0.1:`, "the child dials the read-deadline proxy, not the configured upstream:\n%s", body)
		assert.NotContains(t, string(body), "localhost:11434", "the child still dials the upstream directly:\n%s", body)
		assert.Contains(t, string(body), `"external_directory"`, "the job's fence rules are in the config the child reads:\n%s", body)
	})
}

// TestNativeRefusesConfigProviderWithoutKey: a --config whose entry for THE MODEL'S OWN
// provider has no key in --auth is refused before anything runs, in one line, naming the
// provider and never the key. That provider is the one the harness is about to call, so its
// missing key is a run that dies rc=1 in under a second.
func TestNativeRefusesConfigProviderWithoutKey(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	auth := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(auth, []byte(`{"fake":"the-fake-secret"}`), 0o600))
	cfgPath := filepath.Join(t.TempDir(), "opencode.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`{"provider":{"fake":{},"zeta":{}}}`), 0o644))

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "zeta/zeta-model", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root, authFile: auth,
		configFile: cfgPath, deadline: 30 * time.Second,
	}, &errOut)
	require.Equal(t, 2, code, "a config whose entry for the model's provider has no key exits 2, got %d:\n%s", code, errOut.String())
	require.Contains(t, errOut.String(), "NATIVE REFUSED", "the refusal is one REFUSED line:\n%s", errOut.String())
	require.Contains(t, errOut.String(), "zeta", "the refusal names the provider, never the key:\n%s", errOut.String())
	require.NotContains(t, errOut.String(), "the-fake-secret", "the refusal never prints a key:\n%s", errOut.String())
	got := strings.Count(strings.TrimSpace(errOut.String()), "\n") + 1
	require.Equal(t, 1, got, "exactly one REFUSED line, got %d:\n%s", got, errOut.String())
}

// TestNativeConfigChecksOnlyTheModelsProvider: a --config may name every provider a person
// keeps in ~/.config/opencode, and only the one the --model names is checked for a key. The
// config here names two -- a keyless ollama the model uses, and an inception that has no
// entry in --auth and that this run never calls -- and the run is admitted. Checking all of
// them turned every adoption pass on this bench into `names provider inception, whose key is
// absent` for a card that wanted a local model (issue #523 follow-up).
func TestNativeConfigChecksOnlyTheModelsProvider(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	auth := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(auth, []byte(`{"fake":"the-fake-secret"}`), 0o600))
	const config = `{"provider":{"ollama":{"options":{"baseURL":"http://localhost:11434/v1"}},"inception":{}}}` + "\n"
	cfgPath := filepath.Join(t.TempDir(), "opencode.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte(config), 0o644))

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "ollama/north-mini-code-32k", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root, authFile: auth,
		configFile: cfgPath, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "a provider the model does not use is not checked, got exit %d:\n%s", code, errOut.String())
	require.NotContains(t, errOut.String(), "NATIVE REFUSED", "no refusal for a provider this run never calls:\n%s", errOut.String())
	require.NotContains(t, errOut.String(), "inception", "the unused provider is not named at all:\n%s", errOut.String())
}

// TestNativeConfigKeylessProviderAdmitted: a --config that names a provider whose entry is
// absent from --auth is admitted, not refused, when that provider's options carry a baseURL
// and no apiKey field -- ollama on localhost needs no key, so there is no key to be absent.
// The config is still carried, mode 0600, and the run records the sha8 of what the child saw.
func TestNativeConfigKeylessProviderAdmitted(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	auth := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(auth, []byte(`{"fake":"the-fake-secret"}`), 0o600))
	const config = `{"provider":{"ollama":{"options":{"baseURL":"http://localhost:11434/v1"}}}}` + "\n"
	cfgPath := filepath.Join(t.TempDir(), "opencode.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte(config), 0o644))

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "ollama/north-mini-code-32k", label: "lbl",
		card: []byte("FAKE-RECORD-CONFIG\n"), slotDir: slot, root: root, authFile: auth,
		configFile: cfgPath, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "a keyless provider (baseURL, no apiKey) is admitted, got exit %d:\n%s", code, errOut.String())
	require.NotContains(t, errOut.String(), "NATIVE REFUSED", "the keyless provider is not refused, got:\n%s", errOut.String())
	written, err := os.ReadFile(filepath.Join(slot, "data", ".config", "opencode", "opencode.json"))
	require.NoError(t, err)
	wantSum := sha256.Sum256(written)
	wantSHA := hex.EncodeToString(wantSum[:])[:8]
	assert.Equal(t, wantSHA, res.configSHA, "the run records config sha8 %q, want %q", res.configSHA, wantSHA)
	assertConfigRecord(t, slot, "0600", `"baseURL": "http://127.0.0.1:`)
	assert.NotContains(t, string(written), "localhost:11434", "the child still dials the upstream directly:\n%s", written)
}

// TestNativeOKNamesTheCarriedConfig: the NATIVE OK line itself names the config the CHILD
// sees -- config=<sha8> -- which is the one token of issue #465's fix no other test pins on
// the printed line: the carry test pins the struct's sha8 and the copied bytes, and the
// OK-line tests pin sandbox= and harness=, but the token a caller reads to know a configured
// provider was carried before the child ever ran is asserted by nothing.
//
// WHAT THE SHA8 IS, AND WHY IT IS NOT THE NAMED FILE'S OWN BYTES. writeJobConfig hashes the
// bytes it WRITES to <dataHome>/.config/opencode/opencode.json, AFTER this job's own fence
// block is merged into them (issue #644, #704) -- "the sha8 OF THE BYTES THE CHILD SEES,
// which is the only config any later reader can check the run against". So the sha8 is of
// the merged body and never of the caller's file, and there is no config=- case at all: the
// fence block is written WHETHER OR NOT --config named a file, so a run without --config
// still carries a config and still names its sha8. This test originally pinned the caller's
// own bytes and a dash; both were the pre-#704 contract, and the two assertions below are
// the contract the code now promises.
func TestNativeOKNamesTheCarriedConfig(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	const config = `{"provider":{"fake":{"options":{"baseURL":"http://localhost:11434/v1"}}}}` + "\n"

	// carriedSHA is the sha8 of the bytes that landed where the harness reads them, read back
	// off the disk rather than recomputed from the inputs, so the assertion cannot agree with
	// the code by repeating its arithmetic.
	carriedSHA := func(t *testing.T, slot string) string {
		t.Helper()
		written, err := os.ReadFile(filepath.Join(slot, "data", ".config", "opencode", "opencode.json"))
		require.NoError(t, err, "the run carries a config where the harness reads it")
		sum := sha256.Sum256(written)
		return hex.EncodeToString(sum[:])[:8]
	}

	t.Run("with_config", func(t *testing.T) {
		root, slot := aSlot(t)
		auth := filepath.Join(t.TempDir(), "auth.json")
		require.NoError(t, os.WriteFile(auth, []byte(`{"fake":"the-fake-secret"}`), 0o600))
		cfgPath := filepath.Join(t.TempDir(), "opencode.json")
		require.NoError(t, os.WriteFile(cfgPath, []byte(config), 0o644))
		cardPath := filepath.Join(root, "card.md")
		require.NoError(t, os.WriteFile(cardPath, []byte("a card\n"), 0o644))
		var stdout, stderr bytes.Buffer
		rc := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
			"--label", "lbl", "--card", cardPath, "--slot", slot, "--root", root,
			"--auth", auth, "--config", cfgPath, "--deadline", "30s", "--no-wall"},
			strings.NewReader(""), &stdout, &stderr, time.Now())
		require.Equal(t, 0, rc, "the --config run exits 0, got %d:\n%s", rc, stderr.String())
		// The named provider is in the carried bytes -- config= names a config that really
		// carried --config's provider, not merely some config.
		written, err := os.ReadFile(filepath.Join(slot, "data", ".config", "opencode", "opencode.json"))
		require.NoError(t, err)
		assert.Contains(t, string(written), `"baseURL"`, "the carried config keeps --config's provider:\n%s", written)
		assert.Contains(t, string(written), "fake", "the carried config keeps --config's provider:\n%s", written)
		wantSHA := carriedSHA(t, slot)
		require.Contains(t, stdout.String(), " config="+wantSHA+" ", "NATIVE OK names the sha8 %s of the config the child sees:\n%s", wantSHA, stdout.String())
	})

	t.Run("without_config", func(t *testing.T) {
		root, slot := aSlot(t)
		auth := filepath.Join(t.TempDir(), "auth.json")
		require.NoError(t, os.WriteFile(auth, []byte(`{"fake":"the-fake-secret"}`), 0o600))
		cardPath := filepath.Join(root, "card.md")
		require.NoError(t, os.WriteFile(cardPath, []byte("a card\n"), 0o644))
		var stdout, stderr bytes.Buffer
		rc := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
			"--label", "lbl", "--card", cardPath, "--slot", slot, "--root", root,
			"--auth", auth, "--deadline", "30s", "--no-wall"},
			strings.NewReader(""), &stdout, &stderr, time.Now())
		require.Equal(t, 0, rc, "the run without --config exits 0, got %d:\n%s", rc, stderr.String())
		// No --config, but the fence block is still written, so the line still names a sha8
		// and NEVER a dash: a reader can check the fence the child ran under.
		wantSHA := carriedSHA(t, slot)
		require.Contains(t, stdout.String(), " config="+wantSHA+" ", "NATIVE OK names the sha8 %s of the fence config carried without --config:\n%s", wantSHA, stdout.String())
		require.NotContains(t, stdout.String(), " config=- ", "config= is never a dash: the fence block is carried whether or not --config named a file:\n%s", stdout.String())
	})
}

// TestFriendSequenceLocalModelCard runs one known-answer card on a fake local provider: the
// harness is the fake, the provider is a keyless ollama (baseURL, no apiKey, no auth entry),
// and the card FAKE-PWD answers with the job directory. The run is walled, admitted without a
// refusal, and the wall's own name and the card's known answer both land where a reader looks.
func TestFriendSequenceLocalModelCard(t *testing.T) {
	windowsIsNotABench(t)
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)
	root, slot := aSlot(t)
	auth := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(auth, []byte(`{"fake":"the-fake-secret"}`), 0o600))
	const config = `{"provider":{"ollama":{"options":{"baseURL":"http://localhost:11434/v1"}}}}` + "\n"
	cfgPath := filepath.Join(t.TempDir(), "opencode.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte(config), 0o644))
	label := "local-model-card"

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "ollama/north-mini-code-32k", label: label,
		card: []byte("FAKE-PWD\n"), slotDir: slot, root: root, authFile: auth,
		configFile: cfgPath, deadline: 30 * time.Second, sandbox: sandbox,
	}, &errOut)
	require.Equal(t, 0, code, "the keyless local provider runs walled, got exit %d:\n%s", code, errOut.String())
	require.NotContains(t, errOut.String(), "NATIVE REFUSED", "the local provider card is admitted, not refused:\n%s", errOut.String())
	assert.Equal(t, "fake-wall", res.wall, "the run names the wall it ran inside, got %q", res.wall)
	jobDir := filepath.Join(slot, "jobs", label)
	raw, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	require.NoError(t, err, "the card's known answer was not written")
	got := strings.TrimPrefix(strings.TrimSpace(string(raw)), "pwd=")
	assert.True(t, sameDir(got, jobDir), "the card's known answer is %q, want the job directory %q", got, jobDir)
}

// TestNativeAllowsProviderLoopback: a keyless provider (baseURL, no apiKey) whose baseURL
// names a loopback host:port is carried into the wall as --net-allow <host:port>, so the
// harness can reach the local model. The wall's nopromise grant (allow network-outbound
// (remote ip)) does NOT cover 127.0.0.1, so a local-model card died silently without this
// named grant (issue #591).
func TestNativeAllowsProviderLoopback(t *testing.T) {
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)
	root, slot := aSlot(t)
	const config = `{"provider":{"ollama":{"options":{"baseURL":"http://127.0.0.1:11434/v1"}}}}` + "\n"
	cfgPath := filepath.Join(t.TempDir(), "opencode.json")
	require.NoError(t, os.WriteFile(cfgPath, []byte(config), 0o644))
	label := "a-label"

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "ollama/north-mini-code-32k", label: label,
		card: []byte("a card\n"), slotDir: slot, root: root,
		configFile: cfgPath, deadline: 30 * time.Second, sandbox: sandbox,
	}, &errOut)
	require.Equal(t, 0, code, "the keyless loopback provider runs walled, got exit %d:\n%s", code, errOut.String())
	argv := sandboxArgv(t, filepath.Join(slot, "jobs", label))
	assert.Contains(t, argv, "--net-allow 127.0.0.1:11434", "the wall argv does not carry the loopback allow rule:\n%s", argv)
}

// TestNativeRunRefusalsNameTheirReason drives the remaining three refusals -- a model with
// no provider prefix, an auth file looser than 0600, and a slot outside its root -- so each
// prints its one REFUSED line.
func TestNativeRunRefusalsNameTheirReason(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	looseAuth := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(looseAuth, []byte(`{"fake":"secret"}`), 0o644))
	outside := filepath.Join(t.TempDir(), "outside-slot")

	cases := []struct {
		name string
		cfg  nativeRunConfig
		word string
	}{
		{
			"model_no_prefix",
			nativeRunConfig{binary: bin, model: "no-prefix", card: []byte("x\n"), slotDir: slot, root: root, deadline: 30 * time.Second},
			"no provider prefix",
		},
		{
			"auth_not_0600",
			nativeRunConfig{binary: bin, model: "fake/fake-model", card: []byte("x\n"), slotDir: slot, root: root, authFile: looseAuth, deadline: 30 * time.Second},
			"would not be 0600",
		},
		{
			"slot_outside_root",
			nativeRunConfig{binary: bin, model: "fake/fake-model", card: []byte("x\n"), slotDir: outside, root: root, deadline: 30 * time.Second},
			"outside the configured root",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "auth_not_0600" && runtime.GOOS == "windows" {
				t.Skip("windows cannot make a file 0600 in the POSIX sense: NTFS reports 0666 for every readable file, so writing the auth file 0644 cannot produce the looser-than-0600 condition the refusal names")
			}
			var errOut bytes.Buffer
			_, code := nativeRun(tc.cfg, &errOut)
			require.Equal(t, 2, code, "the refusal exits 2, got %d:\n%s", code, errOut.String())
			require.Contains(t, errOut.String(), tc.word, "the refusal names its reason (%s):\n%s", tc.word, errOut.String())
		})
	}
}

func TestCmdNativeCLI(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	require.NoError(t, os.WriteFile(cardPath, []byte("test card line 1\nline 2\n"), 0o644))

	// Missing flags -> exit 2 with refusal
	var stdout, stderr bytes.Buffer
	rc := run([]string{"native"}, strings.NewReader(""), &stdout, &stderr, time.Now())
	require.Equal(t, 2, rc, "missing flags must exit 2, got %d", rc)
	require.Contains(t, stderr.String(), "--harness is required", "expected --harness is required, got:\n%s", stderr.String())

	// Success run -> exit 0 with NATIVE OK
	stdout.Reset()
	stderr.Reset()
	args := []string{
		"native",
		"--tokens", "unmetered",
		"--slots-store", nativeStore(t),
		"--owner", "fake-1",
		"--harness", bin,
		"--model", "fake/fake-model",
		"--label", "test-label",
		"--card", cardPath,
		"--slot", slot,
		"--root", root,
		"--deadline", "30s",
		"--no-wall",
	}
	rc = run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	require.Equal(t, 0, rc, "native run must exit 0, got %d:\nstdout: %s\nstderr: %s", rc, stdout.String(), stderr.String())
	require.Contains(t, stdout.String(), "NATIVE OK", "stdout must contain NATIVE OK, got:\n%s", stdout.String())
	require.Contains(t, stdout.String(), "label=test-label", "stdout must contain label=test-label, got:\n%s", stdout.String())
}

// TestNativeRunChildDirIsJobDir: the child runs in its job directory <slot>/jobs/<label>,
// told its place by its cwd, not the caller's. The fake harness writes its own working
// directory into RESULT.md, and the test asserts it is exactly the job directory.
func TestNativeRunChildDirIsJobDir(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "a-label"

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("FAKE-PWD\n"), slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "the run exits 0, got %d:\n%s", code, errOut.String())
	jobDir := filepath.Join(slot, "jobs", label)
	raw, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	require.NoError(t, err, "the child did not write pwd into RESULT.md under the job directory")
	got := strings.TrimPrefix(strings.TrimSpace(string(raw)), "pwd=")
	want, err := filepath.EvalSymlinks(jobDir)
	require.NoError(t, err)
	assert.Equal(t, want, got, "the child's cwd is %q, want the job directory %q", got, want)
}

// TestNativeChildCwdIsJobDirFromForeignCwd: the walled child also runs in the job
// directory, and it does so even when the caller's own cwd is somewhere else entirely.
// The test chdirs away from the slot, the root, and the job directory, then runs the
// walled path and asserts the fake harness's pwd is exactly the job directory.
func TestNativeChildCwdIsJobDirFromForeignCwd(t *testing.T) {
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)
	root, slot := aSlot(t)
	label := "foreign-cwd-label"

	foreign := t.TempDir()
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(foreign), "chdir to a foreign directory")
	defer func() { _ = os.Chdir(orig) }()

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("FAKE-PWD\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
		sandbox: sandbox,
	}, &errOut)
	require.Equal(t, 0, code, "the walled run exits 0, got %d:\n%s", code, errOut.String())
	jobDir := filepath.Join(slot, "jobs", label)
	raw, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	require.NoError(t, err, "the child did not write pwd into RESULT.md under the job directory")
	got := strings.TrimPrefix(strings.TrimSpace(string(raw)), "pwd=")
	assert.True(t, sameDir(got, jobDir), "from cwd %s the child's cwd is %q, want the job directory %q", foreign, got, jobDir)
}

// TestNativeWalledJobPathWithSpacesCompletes is the regression for issue #572: a job whose
// path holds a space -- the configured root sits under `worker 2` -- is not a pre-launch
// refusal. The wall is the fake sandbox, which encodes its `cwd=` through oneline.Field
// exactly as the real producer does, so the escape round-trip is exercised rather than a
// hard-coded unescaped receipt. A job that completed must be validated against the decoded
// path and reach the usage recorder, never wear the face of a launch that never happened.
func TestNativeWalledJobPathWithSpacesCompletes(t *testing.T) {
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)

	root := filepath.Join(t.TempDir(), "worker 2")
	slot := filepath.Join(root, "slot-1")
	require.NoError(t, os.MkdirAll(slot, 0o755))
	write(t, filepath.Join(root, "identity.tsv"),
		"owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	label := "space-cwd"

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("FAKE-PWD\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
		sandbox: sandbox,
	}, &errOut)
	require.Equal(t, 0, code, "a completed job in a path with a space exits 0, got %d:\n%s", code, errOut.String())
	require.NotContains(t, errOut.String(), "NATIVE REFUSED", "a completed job is not a pre-launch refusal:\n%s", errOut.String())
	assert.Equal(t, "fake-wall", res.wall, "the run keeps the wall's own name, got %q", res.wall)
	jobDir := filepath.Join(slot, "jobs", label)
	// The child ran in the job directory, proven by the card's own known answer.
	raw, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	require.NoError(t, err, "the card's known answer was not written")
	got := strings.TrimPrefix(strings.TrimSpace(string(raw)), "pwd=")
	assert.True(t, sameDir(got, jobDir), "the child's cwd is %q, want the job directory %q", got, jobDir)
	// A completed job retains its usage receipt.
	_, err = os.Stat(filepath.Join(jobDir, "usage.tsv"))
	assert.NoError(t, err, "the completed job wrote no usage.tsv under %s", jobDir)
}

// TestNativeOKNamesTheWall: NATIVE OK names the wall it ran inside, copied from the wall's
// own SANDBOX OK line, and says none when no wall was named -- so a run without a wall is
// visible in the one line a caller reads.
func TestNativeOKNamesTheWall(t *testing.T) {
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)

	t.Run("no_wall", func(t *testing.T) {
		root, slot := aSlot(t)
		cardPath := filepath.Join(root, "card.md")
		require.NoError(t, os.WriteFile(cardPath, []byte("a card\n"), 0o644))
		var stdout, stderr bytes.Buffer
		rc := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
			"--label", "lbl", "--card", cardPath, "--slot", slot, "--root", root,
			"--deadline", "30s", "--no-wall"}, strings.NewReader(""), &stdout, &stderr, time.Now())
		require.Equal(t, 0, rc, "exit 0, got %d:\n%s", rc, stderr.String())
		require.Contains(t, stdout.String(), "NATIVE OK ", "NATIVE OK names the wall none-by-flag when --no-wall runs:\n%s", stdout.String())
		require.Contains(t, stdout.String(), " sandbox=none-by-flag ", "NATIVE OK names the wall none-by-flag when --no-wall runs:\n%s", stdout.String())
	})

	t.Run("walled", func(t *testing.T) {
		t.Setenv("NOVA_FAKE_SANDBOX", "pass")
		root, slot := aSlot(t)
		cardPath := filepath.Join(root, "card.md")
		require.NoError(t, os.WriteFile(cardPath, []byte("a card\n"), 0o644))
		var stdout, stderr bytes.Buffer
		rc := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
			"--label", "lbl", "--card", cardPath, "--slot", slot, "--root", root,
			"--deadline", "30s", "--sandbox", sandbox}, strings.NewReader(""), &stdout, &stderr, time.Now())
		require.Equal(t, 0, rc, "exit 0, got %d:\n%s", rc, stderr.String())
		require.Contains(t, stdout.String(), " sandbox=fake-wall ", "NATIVE OK copies the wall's own name (fake-wall):\n%s", stdout.String())
	})
}

// TestNativeRunPassesRepoAllowRule: when the wall can express a hash host rule, the native
// run's argv carries each repo the card named as a --repo allow rule, and the child still
// runs to completion.
func TestNativeRunPassesRepoAllowRule(t *testing.T) {
	t.Setenv("NOVA_FAKE_SANDBOX", "hosts")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)
	root, slot := aSlot(t)

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "a-label",
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
		sandbox: sandbox, repos: []string{"mas-bandwidth/nova-tools"},
	}, &errOut)
	require.Equal(t, 0, code, "a walled run with a repo rule exits 0, got %d:\n%s", code, errOut.String())
	argv := sandboxArgv(t, filepath.Join(slot, "jobs", "a-label"))
	assert.Contains(t, argv, "--repo mas-bandwidth/nova-tools", "the wall argv does not carry the repo allow rule:\n%s", argv)
}

// TestNativeRunDeniesBusInsideWall: recipients are never turned into an allow rule. The
// native run built the wall, and the wall's argv grants no bus -- no nova-bus command, no
// --recipient flag, and no bus checkout in the write set -- so a bus send from inside the
// wall is denied by construction.
func TestNativeRunDeniesBusInsideWall(t *testing.T) {
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)
	root, slot := aSlot(t)

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "a-label",
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
		sandbox: sandbox, recipients: []string{"peer-a", "peer-b"},
	}, &errOut)
	require.Equal(t, 0, code, "a walled run exits 0, got %d:\n%s", code, errOut.String())
	argv := sandboxArgv(t, filepath.Join(slot, "jobs", "a-label"))
	for _, denied := range []string{"nova-bus", "--recipient"} {
		assert.NotContains(t, argv, denied, "the wall argv grants a bus lane the wall denies (%q):\n%s", denied, argv)
	}
}

// TestNativeRefusesWhenWallCannotExpressRule: a card that names repos but no wall, or a
// wall that cannot express a HOST rule, is a refusal -- never an unwalled run. The one line
// names the label and the reason.
func TestNativeRefusesWhenWallCannotExpressRule(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)

	// No wall at all (--no-wall): the card named repos there is no wall to allow.
	t.Run("no_wall", func(t *testing.T) {
		var errOut bytes.Buffer
		_, code := nativeRun(nativeRunConfig{
			binary: bin, model: "fake/fake-model", label: "lbl",
			card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
			repos: []string{"mas-bandwidth/nova-tools"}, noWall: true,
		}, &errOut)
		require.Equal(t, 2, code, "the refusal exits 2, got %d:\n%s", code, errOut.String())
		assertRepoRefusal(t, errOut.String())
	})

	// A wall that cannot express a host rule (hosts=none).
	t.Run("wall_without_host_rules", func(t *testing.T) {
		t.Setenv("NOVA_FAKE_SANDBOX", "pass")
		sandbox := nativeSandbox(t)
		var errOut bytes.Buffer
		_, code := nativeRun(nativeRunConfig{
			binary: bin, model: "fake/fake-model", label: "lbl",
			card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
			sandbox: sandbox, repos: []string{"mas-bandwidth/nova-tools"},
		}, &errOut)
		require.Equal(t, 2, code, "the refusal exits 2, got %d:\n%s", code, errOut.String())
		assertRepoRefusal(t, errOut.String())
	})
}

// TestNativeRefusesWithoutWallUnlessFlagged: the wall is never implied away (SPEC-SANDBOX
// rule 1). A machine with no wall binary -- none named with --sandbox and none on PATH -- is
// a refusal naming what was looked for, unless the caller typed --no-wall, in which case the
// run goes unwalled and says so by its own name.
func TestNativeRefusesWithoutWallUnlessFlagged(t *testing.T) {
	bin := nativeHarness(t)
	t.Setenv("PATH", t.TempDir()) // no nova-sandbox on PATH anywhere

	t.Run("no_wall_no_flag", func(t *testing.T) {
		root, slot := aSlot(t)
		var errOut bytes.Buffer
		_, code := nativeRun(nativeRunConfig{
			binary: bin, model: "fake/fake-model", label: "lbl",
			card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
		}, &errOut)
		require.Equal(t, 2, code, "a run with no wall and no --no-wall exits 2, got %d:\n%s", code, errOut.String())
		require.Contains(t, errOut.String(), "NATIVE REFUSED", "the refusal is one REFUSED line, got:\n%s", errOut.String())
		require.Contains(t, errOut.String(), "lbl no wall:", "the refusal names the label and the missing wall, got:\n%s", errOut.String())
		require.Contains(t, errOut.String(), "nova-sandbox", "the refusal names what was looked for, got:\n%s", errOut.String())
		got := strings.Count(strings.TrimSpace(errOut.String()), "\n") + 1
		require.Equal(t, 1, got, "exactly one REFUSED line, got %d:\n%s", got, errOut.String())
	})

	t.Run("no_wall_with_flag", func(t *testing.T) {
		root, slot := aSlot(t)
		var errOut bytes.Buffer
		res, code := nativeRun(nativeRunConfig{
			binary: bin, model: "fake/fake-model", label: "lbl",
			card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
			noWall: true,
		}, &errOut)
		require.Equal(t, 0, code, "--no-wall owns the run and exits 0, got %d:\n%s", code, errOut.String())
		assert.Equal(t, "none-by-flag", res.wall, "--no-wall names the run none-by-flag, got %q", res.wall)
	})
}

// TestNativeRunsWalledWithoutHostRulesWhenNoRepos: a wall that cannot express a host rule
// (its check does not say hosts=enforceable) is still a wall. A card naming no repos runs
// inside it without --repo rules -- never unwalled, and no refusal -- while the same wall
// and a named repo is the refusal asserted elsewhere.
func TestNativeRunsWalledWithoutHostRulesWhenNoRepos(t *testing.T) {
	bin := nativeHarness(t)
	nativeSandboxOnPath(t)
	label := "a-label"
	root, slot := aSlot(t)

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
	}, &errOut)
	require.Equal(t, 0, code, "a wall without host rules still walls a card naming no repos, got %d:\n%s", code, errOut.String())
	assert.Equal(t, "fake-wall", res.wall, "the run names the wall it resolved on PATH, got %q", res.wall)
	argv := sandboxArgv(t, filepath.Join(slot, "jobs", label))
	assert.NotContains(t, argv, "--repo", "no repo was named, so no --repo rule is built:\n%s", argv)
}

// TestNativeEnvIsCleanAndInsideTheWall: the walled child is handed a clean environment, not
// the caller's. HOME and XDG_DATA_HOME appear exactly once and point at the data home, TMPDIR
// is the slot's own tmp/<label> (outside the git-inited job directory), and XDG_CONFIG_HOME /
// XDG_CACHE_HOME do not survive to point outside the wall. A planted foreign
// HOME/XDG_CONFIG_HOME/XDG_CACHE_HOME/TMPDIR and a provider key are set first, and the run
// happens from a foreign cwd, proving the child's own environment and directory are the
// run's, not the caller's.
func TestNativeEnvIsCleanAndInsideTheWall(t *testing.T) {
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)
	root, slot := aSlot(t)
	label := "clean-env"

	// Plant a foreign environment the run must shed: HOME and the two XDG homes outside the
	// wall, a TMPDIR the wall would deny, and a provider key whose value the log must redact.
	foreign := t.TempDir()
	t.Setenv("HOME", filepath.Join(foreign, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(foreign, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(foreign, "cache"))
	t.Setenv("TMPDIR", filepath.Join(foreign, "tmp"))
	t.Setenv("FAKE_KEY", "planted-secret-value")

	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(foreign), "chdir to a foreign directory")
	defer func() { _ = os.Chdir(orig) }()

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("FAKE-PWD\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
		sandbox: sandbox,
	}, &errOut)
	require.Equal(t, 0, code, "the walled run exits 0, got %d:\n%s", code, errOut.String())

	// The child still runs in its job directory even from a foreign cwd.
	jobDir := filepath.Join(slot, "jobs", label)
	raw, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	require.NoError(t, err, "the child did not write pwd into RESULT.md")
	if got := strings.TrimPrefix(strings.TrimSpace(string(raw)), "pwd="); got != jobDir {
		want, evalErr := filepath.EvalSymlinks(jobDir)
		assert.True(t, evalErr != nil || got == want, "from cwd %s the child's cwd is %q, want the job directory %q", foreign, got, want)
	}

	// The environment the run recorded is what the wall was handed.
	rawLog, err := os.ReadFile(filepath.Join(slot, "native-argv.log"))
	require.NoError(t, err, "the run recorded no native-argv.log")
	env := nativeLoggedEnv(t, string(rawLog))
	// The slot admission recorded is symlink-resolved (issue #578), so the expectation is too.
	dataHome := filepath.Join(resolvedPath(t, slot), "data")

	if got := env["HOME"]; assert.Len(t, got, 1, "the child has %d HOME entries, want 1: %v", len(got), got) {
		assert.Equal(t, dataHome, got[0], "HOME is %q, want the data home %q", got[0], dataHome)
	}
	if got := env["XDG_DATA_HOME"]; assert.Len(t, got, 1, "the child has %d XDG_DATA_HOME entries, want 1: %v", len(got), got) {
		assert.Equal(t, dataHome, got[0], "XDG_DATA_HOME is %q, want the data home %q", got[0], dataHome)
	}
	assert.Nil(t, env["XDG_CONFIG_HOME"], "XDG_CONFIG_HOME survived and points outside the wall: %v", env["XDG_CONFIG_HOME"])
	assert.Nil(t, env["XDG_CACHE_HOME"], "XDG_CACHE_HOME survived and points outside the wall: %v", env["XDG_CACHE_HOME"])
	tmp := env["TMPDIR"]
	require.Len(t, tmp, 1, "the child has %d TMPDIR entries, want 1: %v", len(tmp), tmp)
	// The run symlink-resolves the slot it was handed (#586), so the two spellings of one
	// directory are compared as directories, not as strings.
	want := filepath.Join(slot, "tmp", label)
	assert.True(t, sameDir(tmp[0], want), "TMPDIR is %q, want the slot's own tmp dir %q", tmp[0], want)
	got := env["FAKE_KEY"]
	assert.Equal(t, []string{"<redacted>"}, got, "the secret's value is not redacted in the log: %v", got)
}

// TestNativeSharedGoCaches: the Go module and build caches are bench-shared under
// <root>/cache, not one copy per card under the data home (card 8963). The child records
// GOMODCACHE, GOCACHE and GOTOOLCHAIN and stats the two directories it was handed, and the
// wall's argv carries the shared cache in its write set. The directories must exist with
// mode 0755 BEFORE the child runs, which the child's own stat is what proves.
func TestNativeSharedGoCaches(t *testing.T) {
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)
	root, slot := aSlot(t)
	label := "shared-caches"

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("FAKE-RECORD-CACHES\n"), slotDir: slot, root: root,
		deadline: 30 * time.Second, sandbox: sandbox,
	}, &errOut)
	require.Equal(t, 0, code, "the run exits 0, got %d:\n%s", code, errOut.String())
	jobDir := filepath.Join(slot, "jobs", label)
	record, err := os.ReadFile(filepath.Join(jobDir, "cache-record"))
	require.NoError(t, err, "the harness recorded no cache-record")
	got := string(record)
	cacheDir := filepath.Join(resolvedPath(t, root), "cache")
	wantMod := filepath.Join(cacheDir, "go-mod")
	wantBuild := filepath.Join(cacheDir, "go-build")

	assert.Contains(t, got, "GOMODCACHE="+wantMod+"\n", "GOMODCACHE is not the shared module cache %s:\n%s", wantMod, got)
	assert.Contains(t, got, "GOCACHE="+wantBuild+"\n", "GOCACHE is not the shared build cache %s:\n%s", wantBuild, got)
	assert.Contains(t, got, "GOTOOLCHAIN=local\n", "GOTOOLCHAIN is not local:\n%s", got)
	assert.Contains(t, got, "ASDF_OUTPUT_TRANSLATIONS=", "ASDF_OUTPUT_TRANSLATIONS does not point at the job's private Lisp overlay:\n%s", got)
	assert.Contains(t, got, filepath.Join(jobDir, ".cache", "common-lisp"), "ASDF_OUTPUT_TRANSLATIONS does not point at the job's private Lisp overlay:\n%s", got)
	// Each directory existed before the child ran: the record is written by the child, so
	// its own stat is the proof the parent made them first. Windows has no POSIX mode bits,
	// so there the record proves existence and this test proves a file can be created;
	// elsewhere the mode 0755 is the assertion.
	for _, dir := range []struct{ name, path string }{
		{"GOMODCACHE", wantMod},
		{"GOCACHE", wantBuild},
	} {
		line := ""
		for _, l := range strings.Split(got, "\n") {
			if strings.HasPrefix(l, "stat "+dir.name+": ") {
				line = l
			}
		}
		if runtime.GOOS == "windows" {
			if !assert.Contains(t, line, "dir=true", "%s was not an existing directory before the child ran:\n%s", dir.name, got) {
				continue
			}
			probe := filepath.Join(dir.path, "writable-probe")
			if !assert.NoError(t, os.WriteFile(probe, []byte("probe\n"), 0o644), "%s is not writable for the child's caches", dir.path) {
				continue
			}
			_ = os.Remove(probe)
			continue
		}
		assert.Contains(t, line, "mode=0755 dir=true", "%s was not an existing 0755 directory before the child ran:\n%s", dir.name, got)
	}
	// The shared cache is in the wall's write set, so every card of the bench may extract a
	// module inside the wall.
	argv := strings.Fields(sandboxArgv(t, jobDir))
	assert.True(t, hasFlagPair(argv, "--write", cacheDir), "the wall argv does not write the shared cache %s:\n%s", cacheDir, strings.Join(argv, " "))
	// and the go the child runs by name builds -trimpath, so that cache serves this
	// checkout (nova-tools#5174, cost rule 5; TestTheGoShimBuildsTrimpath)
	if runtime.GOOS != "windows" {
		shim, err := os.ReadFile(filepath.Join(slot, "shim", "go"))
		require.NoError(t, err, "the shared caches write the go shim")
		assert.Contains(t, string(shim), "-trimpath")
	}
}

// TestNativeNoSharedCachesRestoresHomeCaches: --no-shared-caches restores today's behaviour
// exactly -- GOMODCACHE, GOCACHE and GOTOOLCHAIN are not set (Go derives the caches from
// HOME as before), <root>/cache is not made, and the wall's write set does not name it.
func TestNativeNoSharedCachesRestoresHomeCaches(t *testing.T) {
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)
	root, slot := aSlot(t)
	label := "no-shared-caches"

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("FAKE-RECORD-CACHES\n"), slotDir: slot, root: root,
		deadline: 30 * time.Second, sandbox: sandbox, noSharedCaches: true,
	}, &errOut)
	require.Equal(t, 0, code, "the run exits 0, got %d:\n%s", code, errOut.String())
	jobDir := filepath.Join(slot, "jobs", label)
	record, err := os.ReadFile(filepath.Join(jobDir, "cache-record"))
	require.NoError(t, err, "the harness recorded no cache-record")
	got := string(record)
	for _, name := range []string{"GOMODCACHE", "GOCACHE", "GOTOOLCHAIN", "ASDF_OUTPUT_TRANSLATIONS"} {
		assert.Contains(t, got, name+"=\n", "--no-shared-caches set %s; the caches must stay under HOME:\n%s", name, got)
	}
	_, err = os.Stat(filepath.Join(root, "cache"))
	assert.True(t, os.IsNotExist(err), "--no-shared-caches made <root>/cache, want none: err=%v", err)
	argv := strings.Fields(sandboxArgv(t, jobDir))
	assert.False(t, hasFlagPair(argv, "--write", filepath.Join(resolvedPath(t, root), "cache")), "--no-shared-caches wrote <root>/cache into the wall argv:\n%s", strings.Join(argv, " "))
	_, err = os.Stat(filepath.Join(slot, "shim", "go"))
	assert.True(t, os.IsNotExist(err), "--no-shared-caches wrote the go shim, want none: err=%v", err)
}

// TestNativeChildCwdIsJobDirUnwalled: the child runs in its job directory on BOTH paths --
// walled and unwalled -- even when the caller's own cwd is somewhere else entirely. The
// unwalled half is the one the sixth run proved: with no wall the child must still be in the
// job directory, not the invoker's.
func TestNativeChildCwdIsJobDirUnwalled(t *testing.T) {
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)

	for _, tc := range []struct {
		name    string
		sandbox string
		noWall  bool
	}{
		{"unwalled", "", true},
		{"walled", sandbox, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.sandbox != "" {
				t.Setenv("NOVA_FAKE_SANDBOX", "pass")
			}
			root, slot := aSlot(t)
			label := "foreign-cwd-" + tc.name

			foreign := t.TempDir()
			orig, err := os.Getwd()
			require.NoError(t, err)
			require.NoError(t, os.Chdir(foreign), "chdir to a foreign directory")
			defer func() { _ = os.Chdir(orig) }()

			var errOut bytes.Buffer
			_, code := nativeRun(nativeRunConfig{
				binary: bin, model: "fake/fake-model", label: label,
				card: []byte("FAKE-PWD\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
				sandbox: tc.sandbox, noWall: tc.noWall,
			}, &errOut)
			require.Equal(t, 0, code, "the %s run exits 0, got %d:\n%s", tc.name, code, errOut.String())
			jobDir := filepath.Join(slot, "jobs", label)
			raw, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
			require.NoError(t, err, "the child did not write pwd into RESULT.md under the job directory")
			got := strings.TrimPrefix(strings.TrimSpace(string(raw)), "pwd=")
			assert.True(t, sameDir(got, jobDir), "from cwd %s the %s child's cwd is %q, want the job directory %q", foreign, tc.name, got, jobDir)
		})
	}
}

// TestNativeRunWritesUsageInJobDirectory: the native run writes usage.tsv beside RESULT.md
// in <slot>/jobs/<label>/usage.tsv (and slotDir fallback), so the batch gather reads it.
func TestNativeRunWritesUsageInJobDirectory(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "usage-loc"

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
		noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "native run exits 0, got %d:\n%s", code, errOut.String())
	jobUsage := filepath.Join(slot, "jobs", label, "usage.tsv")
	_, err := os.Stat(jobUsage)
	require.NoError(t, err, "usage.tsv not found beside RESULT.md in %s", jobUsage)
	slotUsage := filepath.Join(slot, "usage.tsv")
	_, err = os.Stat(slotUsage)
	require.NoError(t, err, "usage.tsv not found in slot directory %s", slotUsage)
}

// TestNativeRelativeSlotIsAbsolutized: the native run absolutizes --slot and --root at
// admission -- absolute and symlink-resolved -- so the wall's argv reads the slot and writes
// the job by that one path; the wall's refusal of `--read ./root/1` and `--write root/1/...`
// is what this absolutization exists to prevent. The run starts from a foreign working
// directory with the slot and root spelled relatively, and the test asserts the wall argv
// carries the resolved absolute slot.
func TestNativeRelativeSlotIsAbsolutized(t *testing.T) {
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)
	foreign := t.TempDir()
	root := filepath.Join(foreign, "root")
	slot := filepath.Join(root, "slot-1")
	require.NoError(t, os.MkdirAll(slot, 0o755))
	write(t, filepath.Join(root, "identity.tsv"),
		"owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(foreign), "chdir to a foreign directory")
	defer func() { _ = os.Chdir(orig) }()
	relRoot, err := filepath.Rel(foreign, root)
	require.NoError(t, err)
	relSlot, err := filepath.Rel(foreign, slot)
	require.NoError(t, err)
	label := "rel-slot"
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("a card\n"), slotDir: relSlot, root: relRoot, deadline: 30 * time.Second,
		sandbox: sandbox,
	}, &errOut)
	require.Equal(t, 0, code, "the run exits 0, got %d:\n%s", code, errOut.String())
	argv := sandboxArgv(t, filepath.Join(slot, "jobs", label))
	want, err := filepath.EvalSymlinks(slot)
	require.NoError(t, err, "resolving the slot %q", slot)
	assert.True(t, hasFlagPairResolved(strings.Fields(argv), "--read", want), "the wall argv does not read the slot by absolute path %s:\n%s", slot, argv)
}

// TestNativeTmpDirIsOutsideAnyRepo: the native run hands the child a TMPDIR that is the slot's
// own tmp/<label>, not the job directory. Native admission git-inits the job directory into a
// repo, and a card's temp dir inside a repo is exactly what makes nova-wake's
// TestAwakeRefusesNonBus fail for a reason the card did not cause (#460). The test git-inits
// the job directory the way admission does, then runs `git rev-parse --show-toplevel` from
// inside the exported TMPDIR and asserts it does not resolve into the job's repo.
func TestNativeTmpDirIsOutsideAnyRepo(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "tmp-outside-repo"

	// git-init the job directory the way native admission does, before the run starts.
	jobDir := filepath.Join(slot, "jobs", label)
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	if out, err := exec.Command("git", "init", "-q", jobDir).CombinedOutput(); err != nil {
		t.Skipf("git init unavailable: %v: %s", err, out)
	}

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
		noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "native run exits 0, got %d:\n%s", code, errOut.String())

	// The exported TMPDIR is the slot's own tmp/<label>, not under the job directory. The run
	// symlink-resolves the slot it was handed (#586), so the two spellings of one directory
	// are compared as directories, not as strings.
	want := filepath.Join(slot, "tmp", label)
	assert.True(t, sameDir(res.tmp, want), "TMPDIR is %q, want %q", res.tmp, want)
	st, err := os.Stat(res.tmp)
	require.NoError(t, err, "the exported TMPDIR %q is not a made directory: %v", res.tmp, err)
	require.True(t, st.IsDir(), "the exported TMPDIR %q is not a made directory: %v", res.tmp, err)

	// A git rev-parse from inside the exported TMPDIR must not resolve into the job's repo.
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = res.tmp
	out, err := cmd.CombinedOutput()
	toplevel := strings.TrimSpace(string(out))
	assert.False(t, err == nil && sameDir(toplevel, jobDir), "git rev-parse --show-toplevel from TMPDIR %q resolved into the job's repo %q", res.tmp, toplevel)
}

// TestNativeNoWallWritesHarnessLog: the UNWALLED run captures the harness's output to
// <job>/harness-output.log, exactly as the walled run does. Before this, `native --no-wall`
// pinned the child's stdout and stderr to <slot>/native.log alone and wrote nothing under
// the job, so every unwalled Space card that produced no RESULT left NO evidence of what the
// harness said -- the whole no-result class of 2026-09-16 was undiagnosable -- and
// `harness=silent` (#604) could not tell a silent harness from a lost log (issue #608).
//
// The capture is NOT `harness.log`: that file is the harness's own, and `harness=silent`
// reads it to ask whether the harness itself wrote anything. The test asserts both -- the
// capture holds the lines, and `harness.log` is left alone by this process.
//
// The fake harness says one line on each stream; both modes hold both lines in the file.
func TestNativeNoWallWritesHarnessLog(t *testing.T) {
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)

	for _, tc := range []struct {
		name    string
		sandbox string
		noWall  bool
	}{
		{"unwalled", "", true},
		{"walled", sandbox, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.sandbox != "" {
				t.Setenv("NOVA_FAKE_SANDBOX", "pass")
			}
			root, slot := aSlot(t)
			label := "harness-log-" + tc.name
			touched := filepath.Join(t.TempDir(), "touched")
			card := []byte("FAKE-SAY the-harness-said-this\nFAKE-TOUCH " + touched + "\n")

			var errOut bytes.Buffer
			_, code := nativeRun(nativeRunConfig{
				binary: bin, model: "fake/fake-model", label: label,
				card: card, slotDir: slot, root: root, deadline: 30 * time.Second,
				sandbox: tc.sandbox, noWall: tc.noWall,
			}, &errOut)
			require.Equal(t, 0, code, "the %s run exits 0, got %d:\n%s", tc.name, code, errOut.String())

			logPath := filepath.Join(slot, "jobs", label, "harness-output.log")
			raw, err := os.ReadFile(logPath)
			require.NoError(t, err, "the %s run wrote no harness output log at %s", tc.name, logPath)
			// The harness's own file is not this process's to write: a capture landing there
			// would answer `harness=silent` for a harness that said nothing at all (#604).
			_, err = os.Stat(filepath.Join(slot, "jobs", label, "harness.log"))
			assert.Error(t, err, "the %s run wrote the harness's own harness.log; the capture belongs in harness-output.log", tc.name)
			for _, want := range []string{"the-harness-said-this", "touch " + touched} {
				assert.Contains(t, string(raw), want, "the %s harness output log %s does not carry %q:\n%s", tc.name, logPath, want, raw)
			}
			// One capture path: whatever the harness log holds, the run log holds too, so a
			// reader of either sees the same run.
			runLog, err := os.ReadFile(filepath.Join(slot, "native.log"))
			require.NoError(t, err, "the %s run wrote no native.log", tc.name)
			for _, want := range []string{"the-harness-said-this", "touch " + touched} {
				assert.Contains(t, string(runLog), want, "the %s native.log does not carry %q:\n%s", tc.name, want, runLog)
			}
		})
	}
}

// TestNativeSilentHarnessIsNotOK pins THE ONE DEFINITION of the token (issues #591, #594,
// #608 folded): `harness=silent` exactly when the run's own capture
// (<job>/harness-output.log, the file the test above proves is written walled or not) holds
// nothing the CHILD said AND no RESULT.md is found anywhere the gather looks; `harness=ok`
// otherwise, and the token is always present.
//
// The four cases are the four ways that can go, and each is a fault someone had:
//   - silent: the run of issue #591 -- a local model whose tool calls the harness never
//     parsed, so no tool ran, nothing was written, the child exited 0 and the line said OK.
//   - spoke_no_result: a harness that SAID something and published nothing is `ok`, because
//     there is evidence to read; the batch scores that card `no-result`, which is the model's
//     own doing and a different remedy.
//   - wall_lines_only: the wall's own `SANDBOX ` lines are in the capture too, and counting
//     them would make a WALLED run -- the shape of #591 itself -- impossible to call silent.
//   - result_under_repo: the result is looked for where the gather looks (#594), and the
//     capture here is EMPTY on purpose, so this case fails the moment that lookup narrows
//     back to the job root: it is the only path where the lookup alone decides.
func TestNativeSilentHarnessIsNotOK(t *testing.T) {
	bin := nativeHarness(t)
	const label = "silent-label"
	// The fake says this on its own stderr for FAKE-SAY: "fake harness: " + the word + "\n",
	// which is 37 bytes -- the harness speaking, and nothing published.
	const saidBytes = 37
	for _, tc := range []struct {
		name, card, want string
		// resultInRepo plants a RESULT.md under <job>/repo before the run, the way a card
		// whose STEP 1 cloned into repo/ publishes, while the run itself says nothing.
		resultInRepo bool
		// walled runs the case inside the fake wall instead of --no-wall, so the capture
		// holds the wall's own SANDBOX lines and nothing else.
		walled bool
		// wantCapture is how many bytes of the child's own words the capture must hold.
		wantCapture int
	}{
		{name: "silent", card: "FAKE-NORESULT\n", want: " harness=silent"},
		{name: "wrote_a_result", card: "a card line 1\nline 2\n", want: " harness=ok"},
		{name: "spoke_no_result", card: "FAKE-SAY twenty-two-characters!\nFAKE-NORESULT\n", want: " harness=ok", wantCapture: saidBytes},
		{name: "wall_lines_only", card: "FAKE-NORESULT\n", want: " harness=silent", walled: true},
		{name: "result_under_repo", card: "FAKE-NORESULT\n", want: " harness=ok", resultInRepo: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.walled {
				t.Setenv("NOVA_FAKE_SANDBOX", "pass")
			}
			root, slot := aSlot(t)
			jobDir := filepath.Join(slot, "jobs", label)
			if tc.resultInRepo {
				repo := filepath.Join(jobDir, "repo")
				require.NoError(t, os.MkdirAll(repo, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(repo, "RESULT.md"), []byte("a card line 1\nall green from the clone\n"), 0o644))
			}
			cardPath := filepath.Join(root, "card.md")
			require.NoError(t, os.WriteFile(cardPath, []byte(tc.card), 0o644))
			args := []string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
				"--label", label, "--card", cardPath, "--slot", slot, "--root", root,
				"--deadline", "30s"}
			if tc.walled {
				args = append(args, "--sandbox", nativeSandbox(t))
			} else {
				args = append(args, "--no-wall")
			}
			var stdout, stderr bytes.Buffer
			rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
			require.Equal(t, 0, rc, "the run exits 0, got %d:\n%s", rc, stderr.String())
			require.Contains(t, stdout.String(), tc.want, "the NATIVE OK line carries%s:\n%s", tc.want, stdout.String())
			// The capture is the file the token is asked of, so the case that says the
			// harness spoke proves the bytes are in it.
			if tc.wantCapture > 0 {
				raw, err := os.ReadFile(filepath.Join(jobDir, "harness-output.log"))
				require.NoError(t, err, "the capture the token reads is missing")
				assert.Len(t, raw, tc.wantCapture, "the capture holds %d bytes of the child's words, want %d:\n%s", len(raw), tc.wantCapture, raw)
			}
			// A silent run's capture holds no word of the child's: either nothing at all, or
			// the wall's own header lines.
			if tc.want == " harness=silent" {
				raw, err := os.ReadFile(filepath.Join(jobDir, "harness-output.log"))
				require.NoError(t, err, "the capture is written even for a silent run")
				for _, line := range strings.Split(string(raw), "\n") {
					assert.True(t, strings.TrimSpace(line) == "" || strings.HasPrefix(line, "SANDBOX "), "a silent run's capture carries a line the child wrote: %q", line)
				}
			}
		})
	}
}

// TestNativeCaptureRefusesSymlink: the capture is the first file this process opens inside
// the JOB, which is the card's own writable directory. A symlink planted there -- by an
// earlier run of the same card, or by the card itself -- would carry the child's output to
// wherever it points, written by a process that has no wall around it (security#30's class).
// The open carries O_NOFOLLOW, so the run refuses by name and the target is untouched.
func TestNativeCaptureRefusesSymlink(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "planted-capture"
	jobDir := filepath.Join(slot, "jobs", label)
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	victim := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(victim, []byte("the bytes outside the wall\n"), 0o644))
	if err := os.Symlink(victim, filepath.Join(jobDir, "harness-output.log")); err != nil {
		t.Skipf("this platform will not plant a symlink: %v", err)
	}

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("FAKE-SAY planted\n"), slotDir: slot, root: root,
		deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 2, code, "a planted symlink at the capture path is a refusal (exit 2), got %d:\n%s", code, errOut.String())
	assert.Contains(t, errOut.String(), "NATIVE REFUSED", "the refusal does not name itself:\n%s", errOut.String())
	raw, err := os.ReadFile(victim)
	require.NoError(t, err)
	assert.Equal(t, "the bytes outside the wall\n", string(raw), "the run wrote through the planted symlink; the file outside now holds:\n%s", raw)
}

// ISSUE #881 (a): a key is authorized for one model only, and the worker description pins
// that one. `nova-swarm native --worker <file>` makes the description the source of the
// model; a --model whose model half differs is refused, naming BOTH models on one line, at
// exit 2, before any directory is made and before any child starts. Without --worker,
// native keeps --model as today.
func TestNativeRefusesAModelThatDiffersFromTheWorkerDescription(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	require.NoError(t, os.WriteFile(cardPath, []byte("a card\n"), 0o644))
	desc := nativeWorkerDescription(t, "fake-model", "key_file")

	var stdout, stderr bytes.Buffer
	rc := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/other-model",
		"--worker", desc, "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"}, strings.NewReader(""), &stdout, &stderr, time.Now())
	require.Equal(t, 2, rc, "a --model that differs from the description's is refused exit 2, got %d:\n%s%s", rc, stdout.String(), stderr.String())
	// THE ONE LINE NAMES BOTH MODELS: the description's fake-model and the --model typed.
	line := strings.TrimSpace(stderr.String())
	mustContain(t, "the refusal", line, "fake/other-model")
	mustContain(t, "the refusal", line, "fake-model")
	n := strings.Count(line, "\n") + 1
	require.Equal(t, 1, n, "the refusal names both models on ONE line, got %d:\n%s", n, stderr.String())
	assert.NotContains(t, stdout.String(), "NATIVE OK", "the refusal comes before any child runs:\n%s", stdout.String())
	_, err := os.Stat(filepath.Join(slot, "jobs"))
	assert.Error(t, err, "the refusal comes before any job directory is made:\n%s", stderr.String())
}

// ISSUE #881 (b): a description naming "secret": "<NAME>" takes the key from the
// ENVIRONMENT -- `nova-secrets exec` set it around the run -- so native passes NAME through
// to the harness's environment and writes NO auth file under the job. --auth remains only
// the legacy shape's. The fake harness proves the key is present by length, never by value.
func TestNativeSecretWorkerWritesNoAuthFileAndTheHarnessSeesName(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	require.NoError(t, os.WriteFile(cardPath, []byte("a card\nFAKE-FINDINGS 0\n"), 0o644))
	desc := nativeWorkerDescription(t, "fake-model", "secret")
	t.Setenv("FAKE_KEY", fakeKey)

	var stdout, stderr bytes.Buffer
	rc := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
		"--worker", desc, "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"}, strings.NewReader(""), &stdout, &stderr, time.Now())
	require.Equal(t, 0, rc, "a secret worker runs, exit %d:\n%s%s", rc, stdout.String(), stderr.String())
	jobDir := filepath.Join(slot, "jobs", "card")
	// THE HARNESS SAW THE NAME: the key reached it by environment, proven by length.
	capture, err := os.ReadFile(filepath.Join(jobDir, "harness-output.log"))
	require.NoError(t, err, "the run captured no harness output under the job")
	mustContain(t, "the harness capture", string(capture), "the key is present, length")
	// NO AUTH FILE IS WRITTEN UNDER THE JOB: neither the carried copy nor the harness's own.
	for _, p := range []string{
		filepath.Join(slot, "data", "auth.json"),
		filepath.Join(slot, "data", "opencode", "auth.json"),
	} {
		_, err := os.Stat(p)
		assert.Error(t, err, "a secret worker writes no auth file, but %s exists", p)
	}
	// The value is in no file under the slot, and in no line this tool printed.
	found := grepTree(t, slot, fakeKey)
	assert.Empty(t, found, "the secret is at rest in a file under the slot: %s", found)
	assert.NotContains(t, stdout.String()+stderr.String(), fakeKey, "the secret reached an event line")
}

// ISSUE #881 (b), the legacy half: `--auth` with a `--worker` description whose key is a
// key_file still copies the provider secret to the data home -- and says so in ONE NOTE
// line, because a description that named "secret": "<NAME>" would keep the key in the
// environment instead. The copy is the child's for the length of the run and no longer:
// when the card ends, no auth.json exists on the bench.
func TestNativeAuthWithAWorkerNamesItsLegacyCopy(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	auth := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(auth, []byte(`{"fake":"the-fake-secret"}`), 0o600))
	cardPath := filepath.Join(root, "card.md")
	require.NoError(t, os.WriteFile(cardPath, []byte("a card\nFAKE-FINDINGS 0\n"), 0o644))
	desc := nativeWorkerDescription(t, "fake-model", "key_file")

	var stdout, stderr bytes.Buffer
	rc := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
		"--worker", desc, "--auth", auth, "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"}, strings.NewReader(""), &stdout, &stderr, time.Now())
	require.Equal(t, 0, rc, "the legacy shape runs, exit %d:\n%s%s", rc, stdout.String(), stderr.String())
	mustContain(t, "the legacy note", stderr.String(), "NATIVE NOTE: --auth")
	for _, p := range []string{
		filepath.Join(slot, "data", "auth.json"),
		filepath.Join(slot, "data", "opencode", "auth.json"),
	} {
		_, err := os.Stat(p)
		assert.Error(t, err, "the legacy copy dies with the card, but %s exists after the run", p)
	}
}

// The legacy --auth copy is the child's for the length of the run and no longer: the
// harness reads it while the card runs -- the capture carries the child's own read of it,
// by length and never by value -- and when the run ends no auth.json exists on the bench
// (the bench standard's plaintext-key rule, docs/SPEC-SECRETS.md's dogfooding ten).
func TestNativeAuthCopyIsGoneAfterTheRun(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	auth := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(auth, []byte(`{"fake":"the-fake-secret"}`), 0o600))
	carried := filepath.Join(slot, "data", "auth.json")
	cardPath := filepath.Join(root, "card.md")
	card := "a card\nFAKE-CAT " + carried + "\nFAKE-FINDINGS 0\n"
	require.NoError(t, os.WriteFile(cardPath, []byte(card), 0o644))

	var stdout, stderr bytes.Buffer
	rc := run([]string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
		"--auth", auth, "--card", cardPath, "--slot", slot, "--root", root,
		"--tokens", "unmetered", "--deadline", "30s", "--no-wall"}, strings.NewReader(""), &stdout, &stderr, time.Now())
	require.Equal(t, 0, rc, "the legacy shape runs, exit %d:\n%s%s", rc, stdout.String(), stderr.String())
	// THE CHILD READ THE COPY WHILE IT RAN: its own cat of the carried file is in the
	// capture.
	capture, err := os.ReadFile(filepath.Join(slot, "jobs", "card", "harness-output.log"))
	require.NoError(t, err, "the run captured no harness output under the job")
	mustContain(t, "the harness capture", string(capture), "cat "+carried+": ok len=")
	// AND NO AUTH.JSON EXISTS AFTER THE CARD: neither the carried copy nor the spelling
	// beside it.
	for _, p := range []string{
		carried,
		filepath.Join(slot, "data", "opencode", "auth.json"),
	} {
		_, err := os.Stat(p)
		assert.Error(t, err, "no auth.json exists on the bench after a card, but %s exists", p)
	}
}

// ISSUE #881: secret implies env_var, and the model gate compares provider/model as one
// name -- a description's model without a slash takes the description's provider as its
// prefix. A secret-only description (no env_var) with provider opencode and model
// deepseek-v4-flash runs under --model opencode/deepseek-v4-flash; --model opencode/other
// is refused naming both; --model other/deepseek-v4-flash is refused too, because the
// provider half matters.
func TestNativeWorkerModelGateComparesQualifiedName(t *testing.T) {
	bin := nativeHarness(t)
	writeSecretOnly := func(t *testing.T) string {
		t.Helper()
		home := t.TempDir()
		desc := map[string]any{
			"name": "opencode-1", "provider": "opencode", "model": "deepseek-v4-flash",
			"secret": "CARD881_SECRET", "usage": "opencode",
			"harness": "fake-harness", "worker_dir": home, "deadline": "30s",
			"harness_args": []string{"run", "--model", "{model}", "--", "{prompt}"},
		}
		raw, err := json.MarshalIndent(desc, "", "  ")
		require.NoError(t, err)
		path := filepath.Join(t.TempDir(), "worker.json")
		require.NoError(t, os.WriteFile(path, raw, 0o644))
		return path
	}
	t.Setenv("CARD881_SECRET", fakeKey)

	t.Run("qualified_match_is_accepted", func(t *testing.T) {
		root, slot := aSlot(t)
		cardPath := filepath.Join(root, "card.md")
		require.NoError(t, os.WriteFile(cardPath, []byte("a card\nFAKE-FINDINGS 0\n"), 0o644))
		desc := writeSecretOnly(t)
		var stdout, stderr bytes.Buffer
		rc := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "opencode/deepseek-v4-flash",
			"--worker", desc, "--card", cardPath, "--slot", slot, "--root", root,
			"--deadline", "30s", "--no-wall"}, strings.NewReader(""), &stdout, &stderr, time.Now())
		require.Equal(t, 0, rc, "provider opencode model deepseek-v4-flash under --model opencode/deepseek-v4-flash is accepted, got exit %d:\n%s%s", rc, stdout.String(), stderr.String())
	})

	t.Run("model_mismatch_is_refused_naming_both", func(t *testing.T) {
		root, slot := aSlot(t)
		cardPath := filepath.Join(root, "card.md")
		require.NoError(t, os.WriteFile(cardPath, []byte("a card\n"), 0o644))
		desc := writeSecretOnly(t)
		var stdout, stderr bytes.Buffer
		rc := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "opencode/other",
			"--worker", desc, "--card", cardPath, "--slot", slot, "--root", root,
			"--deadline", "30s", "--no-wall"}, strings.NewReader(""), &stdout, &stderr, time.Now())
		require.Equal(t, 2, rc, "--model opencode/other against model deepseek-v4-flash is refused exit 2, got %d:\n%s%s", rc, stdout.String(), stderr.String())
		line := strings.TrimSpace(stderr.String())
		mustContain(t, "the refusal", line, "opencode/other")
		mustContain(t, "the refusal", line, "deepseek-v4-flash")
	})

	t.Run("provider_mismatch_is_refused", func(t *testing.T) {
		root, slot := aSlot(t)
		cardPath := filepath.Join(root, "card.md")
		require.NoError(t, os.WriteFile(cardPath, []byte("a card\n"), 0o644))
		// A description the CURRENT loader already accepts (env_var present beside
		// secret), so this subtest isolates the gate: the model half matches, only
		// the provider half differs, and the gate must still refuse.
		home := t.TempDir()
		desc := map[string]any{
			"name": "opencode-1", "provider": "opencode", "model": "deepseek-v4-flash",
			"env_var": "CARD881_ENV", "secret": "CARD881_SECRET", "usage": "opencode",
			"harness": "fake-harness", "worker_dir": home, "deadline": "30s",
			"harness_args": []string{"run", "--model", "{model}", "--", "{prompt}"},
		}
		t.Setenv("CARD881_SECRET", fakeKey)
		raw, err := json.MarshalIndent(desc, "", "  ")
		require.NoError(t, err)
		descPath := filepath.Join(t.TempDir(), "worker.json")
		require.NoError(t, os.WriteFile(descPath, raw, 0o644))
		var stdout, stderr bytes.Buffer
		rc := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "other/deepseek-v4-flash",
			"--worker", descPath, "--card", cardPath, "--slot", slot, "--root", root,
			"--deadline", "30s", "--no-wall"}, strings.NewReader(""), &stdout, &stderr, time.Now())
		require.Equal(t, 2, rc, "--model other/deepseek-v4-flash against provider opencode is refused exit 2, got %d:\n%s%s", rc, stdout.String(), stderr.String())
	})
}

// TestNativeWalledJobPathWithSpace: a walled native run whose root, slot and job directory
// all sit under a path holding a space must finish like any other -- the NATIVE OK line is
// printed and RESULT.md is written under the job directory. The wall names the cwd it
// applied on its SANDBOX OK line, and the only field a reader may trust is the machine
// receipt of the raw path bytes: the readable cwd=<dir> field is oneline.Field, which
// renders the space as \x20, and comparing that escaped spelling to the real job directory
// refused a run whose child had already finished (#624).
func TestNativeWalledJobPathWithSpace(t *testing.T) {
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	bin := nativeHarness(t)
	sandbox := nativeSandbox(t)
	root := filepath.Join(t.TempDir(), "My Bench")
	slot := filepath.Join(root, "slot-1")
	require.NoError(t, os.MkdirAll(slot, 0o755))
	write(t, filepath.Join(root, "identity.tsv"),
		"owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	cardPath := filepath.Join(root, "card.md")
	require.NoError(t, os.WriteFile(cardPath, []byte("FAKE-PWD\n"), 0o644))

	var stdout, stderr bytes.Buffer
	rc := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin, "--model", "fake/fake-model",
		"--label", "space-label", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--sandbox", sandbox}, strings.NewReader(""), &stdout, &stderr, time.Now())
	require.Equal(t, 0, rc, "a walled run under a root with a space exits 0, got %d:\nstdout: %s\nstderr: %s", rc, stdout.String(), stderr.String())
	require.Contains(t, stdout.String(), "NATIVE OK ", "the NATIVE OK line is printed for a root with a space:\n%s", stdout.String())
	_, err := os.Stat(filepath.Join(slot, "jobs", "space-label", "RESULT.md"))
	require.NoError(t, err, "RESULT.md is written under a job path with a space")
}

// TestNativeHoldsAJobLease: the launcher takes <job>/.lease BEFORE the child starts and
// releases it when the run ends (issue #1499). The child itself is the witness -- it reads
// the lease from inside the job and reports its length -- because the file's whole purpose
// is to exist WHILE the card runs: that is what the bench's hygiene pass reads instead of
// guessing from how long the capture has been quiet. A card in one long model call is
// silent and alive, and the reaper that could not tell the difference deleted two certify
// trees, and a running card's HOME and TMPDIR, on 2026-09-19.
func TestNativeHoldsAJobLease(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "lease-card"
	jobDir := filepath.Join(slot, "jobs", label)
	card := []byte("FAKE-CAT " + filepath.Join(jobDir, swarm.JobLeaseName) + "\n")

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: card, slotDir: slot, root: root, deadline: 30 * time.Second,
		noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "the run exits 0, got %d:\n%s", code, errOut.String())

	raw, err := os.ReadFile(filepath.Join(jobDir, "harness-output.log"))
	require.NoError(t, err, "the run wrote no harness output log")
	want := "cat " + filepath.Join(jobDir, swarm.JobLeaseName) + ": ok len="
	require.Contains(t, string(raw), want, "the child could not read a lease at %s while it ran; the capture says:\n%s",
		filepath.Join(jobDir, swarm.JobLeaseName), raw)
	assert.NotContains(t, string(raw), want+"0\n", "the lease was empty while the child ran; it must name the launcher's pid:\n%s", raw)
	_, err = os.Lstat(filepath.Join(jobDir, swarm.JobLeaseName))
	assert.True(t, os.IsNotExist(err), "the lease outlived the run (%v); a finished job must leave nothing that claims to be alive", err)
}

// TestNativeRunTakesThePoolIdentityFromTheLoopsArgv: a loop's nova-config argv names
// the pool identity (--identity <owner>,<name>,<email>), so a pool with no
// identity.tsv launches under it and a pool with one launches under the argv's;
// with neither the launch is refused naming both ways (swarm.ParseIdentity).
func TestNativeRunTakesThePoolIdentityFromTheLoopsArgv(t *testing.T) {
	t.Parallel()
	bin := nativeHarness(t)
	argv, err := swarm.ParseIdentity("loop-owner,Loop Worker,loop@example.com")
	require.NoError(t, err)
	for _, tc := range []struct {
		name     string
		tsv      bool
		identity *swarm.StagingIdentity
		want     string // GIT_AUTHOR_NAME, or "" for a refusal
	}{
		{"argv, no file", false, &argv, "Loop Worker"},
		{"argv over the file", true, &argv, "Loop Worker"},
		{"the file alone", true, nil, "Pool Worker"},
		{"neither", false, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root, slot := aSlot(t)
			if !tc.tsv {
				require.NoError(t, os.Remove(filepath.Join(root, "identity.tsv")))
			}
			var errOut bytes.Buffer
			_, code := nativeRun(nativeRunConfig{binary: bin, model: "fake/fake-model", label: "id", card: []byte("a card\n"),
				slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true, identity: tc.identity}, &errOut)
			if tc.want == "" {
				assert.Equal(t, 2, code)
				assert.Contains(t, errOut.String(), "--identity <owner>,<name>,<email>")
				return
			}
			require.Equal(t, 0, code, errOut.String())
			raw, err := os.ReadFile(filepath.Join(slot, "native-argv.log"))
			require.NoError(t, err)
			assert.Equal(t, []string{tc.want}, nativeLoggedEnv(t, string(raw))["GIT_AUTHOR_NAME"])
		})
	}
}

// THE C18 SHAPE: the provider answers 5xx at request start, the harness exits 1, and the
// job directory holds no RESULT.md. RED WITHOUT THE FIX: `NATIVE OK ... rc=1`.
func TestNativeRefusesToSayOKForAProviderFailureThatProducedNothing(t *testing.T) {
	t.Parallel()

	out := nativeVerdict(t, "c18", "FAKE-5XX\n")

	require.NotContains(t, out, "NATIVE OK", "a run with rc!=0 and no RESULT.md said OK -- a fill loop counts that as delivered:\n%s", out)
	require.Contains(t, out, "NATIVE INCOMPLETE ", "the verdict line must still be printed, and say what it is:\n%s", out)
	require.Contains(t, out, "why=no-result", "the verdict must name why it is incomplete (no RESULT.md):\n%s", out)
	// Every other field a reader parses is exactly where it was.
	for _, want := range []string{"label=c18", "job=", "rc=1", "wall=", "sandbox=", "card_sha256=", "harness="} {
		require.Contains(t, out, want, "the incomplete line dropped %q, which every reader of this line parses:\n%s", want, out)
	}
}

// A harness that exits 0, says nothing and writes nothing is the same class: the exit code
// was clean and the card produced no result, and nothing downstream should read that as a
// delivered card. Here the first condition to fail is the harness's own silence, which
// #591 already made a token on this line and which the verdict now acts on.
func TestNativeRefusesToSayOKWhenTheHarnessSaidNothing(t *testing.T) {
	t.Parallel()

	out := nativeVerdict(t, "noresult", "FAKE-NORESULT\n")

	require.NotContains(t, out, "NATIVE OK", "a clean exit that said nothing and wrote no RESULT.md said OK:\n%s", out)
	require.Contains(t, out, "why=harness-silent", "the verdict must name the silence:\n%s", out)
	require.Contains(t, out, " rc=0 ", "the exit code is still reported as it was:\n%s", out)
}

// AND THE OTHER DIRECTION, so the fix is not "never say OK": a card that ran, answered and
// wrote its RESULT.md still gets the word, with no why= tail at all.
func TestNativeStillSaysOKForARunThatProducedItsResult(t *testing.T) {
	t.Parallel()

	out := nativeVerdict(t, "green", "FAKE-RESULT ok\n")

	require.Contains(t, out, "NATIVE OK ", "a run that produced its RESULT.md must still be OK:\n%s", out)
	require.NotContains(t, out, "why=", "an OK verdict carries no why= tail:\n%s", out)
}

// THE SUPERMAN SHAPE (nova-tools #2058). Darwin, harness v1.18.20, 24 cards at once:
// 23 NATIVE OK, 1 launch exited 255 twice. Both job dirs held a complete RESULT.md;
// harness-output.log showed `error: Error starting FSEvents stream` right after
// `> build · <model>`, then the card's commands and "Wrote file successfully".
// The launcher printed the CAPACITY line and then nothing: no attempt=, no
// NATIVE OK/INCOMPLETE, exit 255. rr-run.sh retried in place and ran the card
// twice. Local ssh(1) exits 255 for any error, which is not proof the remote
// command never started: the outcome is potentially UNKNOWN, and a retry waits
// on reconciliation. A native that finishes a card and then exits 255 is a
// finished card a launcher can misread as a transport failure and retry.
//
// RED WITHOUT THE FIX: process exit 255 (the child's code passed through), or
// any verdict other than exactly one NATIVE INCOMPLETE with rc=255 why=rc.
func TestNativeHarnessExit255PrintsAVerdictAndDoesNotExit255(t *testing.T) {
	t.Parallel()

	stdout, stderr, code := nativeVerdictRun(t, "fsevents", "FAKE-FSEVENTS\n")
	combined := stdout + stderr
	require.NotContains(t, combined, "NATIVE OK", "a harness that exited 255 after writing RESULT.md must not say OK:\nstdout:\n%s\nstderr:\n%s\nexit %d", stdout, stderr, code)
	require.NotContains(t, combined, "NATIVE REFUSED", "a harness that exited 255 after writing RESULT.md must not say REFUSED:\nstdout:\n%s\nstderr:\n%s\nexit %d", stdout, stderr, code)
	var incomplete []string
	for _, line := range strings.Split(combined, "\n") {
		if strings.HasPrefix(line, "NATIVE INCOMPLETE ") {
			incomplete = append(incomplete, line)
		}
	}
	require.Len(t, incomplete, 1, "want exactly one NATIVE INCOMPLETE line, got %d:\nstdout:\n%s\nstderr:\n%s\nexit %d", len(incomplete), stdout, stderr, code)
	line := incomplete[0]
	hasRC, hasWhy := false, false
	for _, f := range strings.Fields(line) {
		if f == "rc=255" {
			hasRC = true
		}
		if f == "why=rc" {
			hasWhy = true
		}
	}
	require.True(t, hasRC, "the INCOMPLETE line must carry rc=255 and why=rc:\n%s", line)
	require.True(t, hasWhy, "the INCOMPLETE line must carry rc=255 and why=rc:\n%s", line)
	require.NotEqual(t, 255, code, "native exited 255; local ssh(1) 255 is any error and is potentially UNKNOWN, so a launcher may retry a finished card. The child's 255 belongs on the line as rc=255:\n%s\nstderr:\n%s", stdout, stderr)
	require.Equal(t, 1, code, "the verb ran and said NO, want exit 1, got %d:\n%s\nstderr:\n%s", code, stdout, stderr)
}

// THE NEGATIVE, so the 255 clamp is not "never say OK/INCOMPLETE": a card that
// produced its RESULT.md is still NATIVE OK at exit 0, and a silent harness is
// still NATIVE INCOMPLETE, and neither process exits 255.
func TestNativeOrdinaryCardsStillPrintOKAndIncomplete(t *testing.T) {
	t.Parallel()

	stdout, stderr, code := nativeVerdictRun(t, "green255", "FAKE-RESULT ok\n")
	require.Contains(t, stdout, "NATIVE OK ", "a run that produced its RESULT.md must still be OK:\n%s\nstderr:\n%s", stdout, stderr)
	require.NotContains(t, stdout, "NATIVE INCOMPLETE ", "an OK card must not also be INCOMPLETE:\n%s", stdout)
	require.NotEqual(t, 255, code, "a normal OK card must not exit 255:\n%s", stdout)
	require.Equal(t, 0, code, "a run that produced its RESULT.md still exits 0, got %d:\n%s\nstderr:\n%s", code, stdout, stderr)

	stdout, stderr, code = nativeVerdictRun(t, "quiet255", "FAKE-NORESULT\n")
	require.Contains(t, stdout, "NATIVE INCOMPLETE ", "a silent harness must still be INCOMPLETE:\n%s\nstderr:\n%s", stdout, stderr)
	require.NotContains(t, stdout, "NATIVE OK ", "a silent harness must not be OK:\n%s", stdout)
	require.NotEqual(t, 255, code, "an incomplete card must not exit 255:\n%s", stdout)
}

// A whole native run of a decide read past its bounce bar stages the checkout, decides, and
// ends with no child: the run is OK with the decision's broken read in RESULT.md, published.
func TestNativeRunEndsADecidedReadWithNoChild(t *testing.T) {
	t.Parallel()
	bin := nativeHarness(t)
	root, origin, head := readOrigin(t)
	slot := filepath.Join(root, "slot-1")
	require.NoError(t, os.MkdirAll(slot, 0o755))
	write(t, filepath.Join(root, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	fake := &fakeDecide{defect: 0.6}
	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{binary: bin, model: "fake/fake-model", label: "w.r1", card: readCard, slotDir: slot, root: root,
		deadline: 30 * time.Second, noWall: true, benchHome: filepath.Join(root, "no-bench"), resultsRoot: filepath.Join(root, "results", "w.r1"),
		frame: readFrame(cardcontract.Frame{DecideBounce: "0.5", DecideReview: "0.3"}, origin, head), decider: &decider{backend: fake, now: time.Now}}, &errOut)
	require.Equal(t, 0, code, errOut.String())
	assert.Equal(t, 1, fake.asks)
	assert.Equal(t, []string{"OK", ""}, func() []string { v, w := nativeVerdictWhy(res); return []string{v, w} }(), "the NATIVE line is OK")
	_, err := os.Stat(filepath.Join(res.job, "harness-output.log"))
	assert.True(t, os.IsNotExist(err), "no child ran")
	require.NotEmpty(t, res.resultsDir)
	raw, err := os.ReadFile(filepath.Join(res.resultsDir, "RESULT.md"))
	require.NoError(t, err)
	assert.Equal(t, "broken", typedrec.ParseCardResult(raw).Verdict)
}

// A decide read that cannot be made falls back to the strings read, never to a verdict:
// whole native runs over a backend that answers 402, one whose context ran out and one
// whose answer is outside the schema each run the child, publish no ok read of their own
// and record nothing, and say why on one NOTE line. A read in the band between the bars
// runs the child too, its decision recorded.
func TestNativeRunFallsBackToTheStringsReadWhenNoDecisionIsMade(t *testing.T) {
	t.Parallel()
	bin := nativeHarness(t)
	jev := func(send decide.Send) decide.Backend { return decide.Jev{Model: decide.JevModel, Send: send} }
	for _, tc := range []struct {
		name     string
		backend  decide.Backend
		note     string
		recorded int
	}{
		{"402", jev(func(context.Context, []byte) ([]byte, error) {
			return nil, errors.New(`the backend answered HTTP 402: "payment required"`)
		}), "no decide read: the backend answered HTTP 402", 0},
		{"timeout", jev(func(context.Context, []byte) ([]byte, error) { return nil, context.DeadlineExceeded }), "no decide read: context deadline exceeded", 0},
		{"malformed", jev(func(context.Context, []byte) ([]byte, error) { return []byte(`{"answers":{}}`), nil }), "no decide read: the backend's answers do not fit the schema", 0},
		{"strings band", &fakeDecide{defect: 0.4}, "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root, origin, head := readOrigin(t)
			slot := filepath.Join(root, "slot-1")
			require.NoError(t, os.MkdirAll(slot, 0o755))
			write(t, filepath.Join(root, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
			var errOut bytes.Buffer
			res, code := nativeRun(nativeRunConfig{binary: bin, model: "fake/fake-model", label: "w.r1", card: readCard, slotDir: slot, root: root,
				deadline: 30 * time.Second, noWall: true, benchHome: filepath.Join(root, "no-bench"), resultsRoot: filepath.Join(root, "results", "w.r1"),
				frame: readFrame(cardcontract.Frame{DecideBounce: "0.5", DecideReview: "0.3"}, origin, head), decider: &decider{backend: tc.backend, now: time.Now}}, &errOut)
			require.Equal(t, 0, code, errOut.String())
			_, err := os.Stat(filepath.Join(res.job, "harness-output.log"))
			assert.NoError(t, err, "the strings read's child ran")
			if raw, err := os.ReadFile(swarm.ResultPath(res.job)); err == nil {
				assert.NotEqual(t, "ok", typedrec.ParseCardResult(raw).Verdict, "no ok read is published for an undecided read")
			}
			ds, err := decide.Load(decideRecord(root))
			require.NoError(t, err)
			assert.Len(t, ds, tc.recorded)
			if tc.note != "" {
				assert.Contains(t, errOut.String(), "NATIVE NOTE: w.r1 "+tc.note)
				assert.Contains(t, errOut.String(), "; the strings read runs")
			}
		})
	}
}

// native's result lookup must not follow a symlink planted at RESULT.md and call it published.
func TestNativeDoesNotTreatAPlantedSymlinkAsAPublishedResult(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "planted-result"
	jobDir := filepath.Join(slot, "jobs", label)
	outside := plantNativeResultSymlink(t, jobDir, "RESULT plant sha=aaa\nDONE\nBRANCH worker/exfil-233\n")

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("FAKE-NORESULT\n"), slotDir: slot, root: root,
		deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "native run exits 0, got %d:\n%s", code, errOut.String())
	require.NotEqual(t, "ok", res.harness, "native treated a planted symlink at RESULT.md as a published result; harness=%s", res.harness)
	raw, err := os.ReadFile(outside)
	require.NoError(t, err)
	require.Contains(t, string(raw), "worker/exfil-233", "the file outside the job was rewritten through the link: %q", string(raw))
}

// TestNativeRunWritesTimeline: a native run timestamps the harness's own per-turn and
// per-tool report lines into <job>/timeline.tsv -- one row per model turn and per tool
// call, in the order the harness reported them, with the six columns the profile verb
// reads. A tool row carries no tokens (the harness gave none there), a turn row does.
func TestNativeRunWritesTimeline(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "timeline"

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card: []byte("FAKE-TIMELINE\n"), slotDir: slot, root: root,
		deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "native run exits 0, got %d:\n%s", code, errOut.String())

	path := filepath.Join(slot, "jobs", label, swarm.TimelineFileName)
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "the native run wrote no timeline at %s", path)
	head := strings.SplitN(string(raw), "\n", 2)[0]
	require.Equal(t, strings.Join(swarm.TimelineColumns, "\t"), head, "timeline header = %q, want %q", head, strings.Join(swarm.TimelineColumns, "\t"))

	rows, err := swarm.ReadTimeline(path)
	require.NoError(t, err, "read the timeline back")
	require.Len(t, rows, 6, "%d timeline rows, want 6 (one turn, five tool calls):\n%s", len(rows), raw)
	assert.Equal(t, "model", rows[0].Tool, "turn row = %+v, want tool=model in=1200 out=340", rows[0])
	assert.Equal(t, "1200", rows[0].InputTokens, "turn row = %+v, want tool=model in=1200 out=340", rows[0])
	assert.Equal(t, "340", rows[0].OutputTokens, "turn row = %+v, want tool=model in=1200 out=340", rows[0])
	// The tool calls, in report order, each with no tokens of its own.
	wants := []string{"git clone", "cat ", "go test", "go test", "result.md"}
	for i, want := range wants {
		r := rows[i+1]
		assert.Contains(t, strings.ToLower(r.Tool), want, "tool row %d = %q, want it to name %q", i, r.Tool, want)
		assert.Equal(t, "", r.InputTokens, "tool row %d carries tokens %q/%q, want both empty (the harness gave none)", i, r.InputTokens, r.OutputTokens)
		assert.Equal(t, "", r.OutputTokens, "tool row %d carries tokens %q/%q, want both empty (the harness gave none)", i, r.InputTokens, r.OutputTokens)
	}
	// Every span is a real, non-negative duration.
	for i, r := range rows {
		assert.False(t, r.End.Before(r.Start), "row %d ends before it starts: %s < %s", i, r.End, r.Start)
	}
}

// TestNativeRetriesAProvider5xxLaunch: the native path retries a launch that dies inside the
// grace on a provider server error, keeps the same job, harvests the second attempt's result
// and writes one usage row per attempt for the one job.
func TestNativeRetriesAProvider5xxLaunch(t *testing.T) {
	t.Parallel()

	// The retry wait is pinned to zero for the whole package by TestMain.
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "retry-5xx"

	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card:    []byte("a card that dies at request start\nFAKE-LAUNCHES\nFAKE-5XX-FIRST\n"),
		slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "native run exits 0, got %d:\n%s", code, errOut.String())
	require.Equal(t, 0, res.rc, "the second attempt succeeds and the run records rc=0, got %d:\n%s", res.rc, errOut.String())
	// The harvested result is the second attempt's: the first died before publishing and
	// the second published, so RESULT.md exists in the job directory.
	jobDir := filepath.Join(slot, "jobs", label)
	_, err := os.Stat(filepath.Join(jobDir, "RESULT.md"))
	assert.NoError(t, err, "the second attempt's result was not harvested")
	// One usage row per launch, attempt=1 and attempt=2, for the one job.
	raw, err := os.ReadFile(filepath.Join(jobDir, "usage.tsv"))
	require.NoError(t, err)
	rows := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	require.Len(t, rows, 3, "a retried card wants a header and two rows, got %d:\n%s", len(rows), raw)
	head := strings.Split(rows[0], "\t")
	attemptAt := -1
	usdAt := -1
	for i, name := range head {
		switch name {
		case "attempt":
			attemptAt = i
		case "usd":
			usdAt = i
		}
	}
	require.GreaterOrEqual(t, attemptAt, 0, "the header does not carry attempt and usd:\n%s", rows[0])
	require.GreaterOrEqual(t, usdAt, 0, "the header does not carry attempt and usd:\n%s", rows[0])
	got := strings.Split(rows[1], "\t")[attemptAt]
	assert.Equal(t, "1", got, "the first launch row carries attempt=%s, want 1", got)
	got = strings.Split(rows[2], "\t")[attemptAt]
	assert.Equal(t, "2", got, "the second launch row carries attempt=%s, want 2", got)
	got = strings.Split(rows[1], "\t")[usdAt]
	assert.Equal(t, "-", got, "an unreported usd stays a dash, got %q", got)
}

// A lost response is one launch, and the usage row stays unknown. The fake
// harness prints the timeout and exits. It does not prove the installed
// OpenCode consumer honors headerTimeout; it proves this loop does not turn
// that tail into done or failed, and does not start a second launch.
func TestNativeLostResponseStaysUnknownAndLaunchesOnce(t *testing.T) {
	t.Parallel()

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "lost-response"
	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: label,
		card:    []byte("FAKE-LAUNCHES\nFAKE-LOST-RESPONSE\n"),
		slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "native run exits 0, got %d:\n%s", code, errOut.String())
	require.True(t, res.lost, "lost=%v end=%s, want a retained unknown", res.lost, res.end)
	require.Equal(t, "unknown", res.end, "lost=%v end=%s, want a retained unknown", res.lost, res.end)
	jobDir := filepath.Join(slot, "jobs", label)
	launches, err := os.ReadFile(filepath.Join(jobDir, "launches"))
	require.NoError(t, err)
	n := strings.Count(string(launches), "launch")
	require.Equal(t, 1, n, "a lost response launched %d times, want 1:\n%s", n, launches)
	mark, err := os.ReadFile(filepath.Join(jobDir, "provider-acceptance"))
	require.NoError(t, err)
	require.Equal(t, oneline.Escape("unknown\n"), string(mark), "provider-acceptance = %q, want the escaped marker", mark)
	_, err = os.Stat(filepath.Join(jobDir, "RESULT.md"))
	require.Error(t, err, "a lost response must not publish a result")
}

func TestUnrecordedUnknownIsStillAHarvestHold(t *testing.T) {
	windowsIsNotABench(t)
	prev := persistUnknownFn
	persistUnknownFn = func(string) error { return errors.New("disk full") }
	t.Cleanup(func() { persistUnknownFn = prev })

	bin := nativeHarness(t)
	root, slot := aSlot(t)
	label := "unrecorded"
	cardPath := filepath.Join(root, label+".md")
	require.NoError(t, os.WriteFile(cardPath, []byte("FAKE-LOST-RESPONSE\n"), 0o644))
	var stdout, stderr bytes.Buffer
	code := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1",
		"--harness", bin, "--model", "fake/fake-model", "--label", label,
		"--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "30s", "--no-wall"},
		strings.NewReader(""), &stdout, &stderr, time.Now())
	require.NotEqual(t, 0, code, "a failed record exited 0:\n%s", stdout.String())
	require.Contains(t, stdout.String(), "why=unknown-acceptance", "the refusal returned before the unknown verdict:\n%s\n%s", stdout.String(), stderr.String())

	// The nova-pulse harvest hold that followed here left with internal/pulse (deleted 2026-09-25, #3969);
	// the native refusal above is the property that remains.
}

func TestNativeLostResponseLineSaysUnknownAcceptance(t *testing.T) {
	t.Parallel()

	out := nativeVerdict(t, "lost", "FAKE-LOST-RESPONSE\n")
	require.NotContains(t, out, "NATIVE OK", "a lost response said OK:\n%s", out)
	require.Contains(t, out, "why=unknown-acceptance", "the verdict did not keep unknown-acceptance:\n%s", out)
	require.NotContains(t, out, "why=no-result", "a lost response was filed as an ordinary incomplete:\n%s", out)
	require.NotContains(t, out, "why=rc", "a lost response was filed as an ordinary incomplete:\n%s", out)
}

// A transient wrapper does not make a current credit/key refusal retryable
// (SPEC-SPRINT section 5: the provider rests until its funds or key return).
func TestNativeDoesNotRetryARefusalInsideAServerError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, card, class string
	}{
		{"credit", "FAKE-SAY Unexpected server error: ref=err_credit\nFAKE-CREDIT-REFUSAL\n", "out-of-credit"},
		{"auth", "FAKE-SAY level=ERROR message=server_error statusCode=401 error=Unauthorized Unexpected server error\nFAKE-NORESULT\n", "auth"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bin := nativeHarness(t)
			root, slot := aSlot(t)
			var errOut bytes.Buffer
			_, code := nativeRun(nativeRunConfig{
				binary: bin, model: "fake/fake-model", label: tc.name,
				card:    []byte("FAKE-LAUNCHES\n" + tc.card),
				slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
			}, &errOut)
			require.Equal(t, 0, code, errOut.String())
			launches, err := os.ReadFile(filepath.Join(slot, "jobs", tc.name, "launches"))
			require.NoError(t, err)
			assert.Equal(t, 1, strings.Count(string(launches), "launch"), errOut.String())
			assert.Contains(t, errOut.String(), "reason=provider: class="+tc.class)
		})
	}
}

// An earlier job's credit refusal is not this launch's refusal. A later
// transient failure still earns its retry (SPEC-SPRINT section 5).
func TestNativeRetriesA5xxAfterAnOlderCreditRefusal(t *testing.T) {
	t.Parallel()
	needsSQLite(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "older-credit",
		card:    []byte("FAKE-CREDIT-REFUSAL\n"),
		slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, errOut.String())
	require.Contains(t, errOut.String(), "class=out-of-credit")
	// Make the old session deterministically older than the next launch.
	output, err := exec.Command(swarm.SQLiteBinary, filepath.Join(slot, "data", "opencode", "opencode.db"), "UPDATE message SET time_created=0").CombinedOutput()
	require.NoError(t, err, string(output))
	errOut.Reset()
	res, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "later-5xx",
		card:    []byte("FAKE-LAUNCHES\nFAKE-5XX-FIRST\n"),
		slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, errOut.String())
	assert.Equal(t, 0, res.rc, errOut.String())
	launches, err := os.ReadFile(filepath.Join(slot, "jobs", "later-5xx", "launches"))
	require.NoError(t, err)
	assert.Equal(t, 2, strings.Count(string(launches), "launch"), errOut.String())
}

// TestAFailedStartIsRetriedInPlace: a harness whose first three starts die at start is
// started again in the same job after 1, 1, 1 s, each retry one NATIVE RETRY line; the
// fourth start runs and publishes, and the run is OK with one result.
func TestAFailedStartIsRetriedInPlace(t *testing.T) {
	t.Parallel()
	res, log, waits, job := startRun(t, "start-retry", "a card\nFAKE-LAUNCHES\nFAKE-START-FAIL 3\n")
	assert.Equal(t, []time.Duration{time.Second, time.Second, time.Second}, waits, "the owner's schedule, from its start")
	assert.Equal(t, 3, strings.Count(log, "NATIVE RETRY "), "%s", log)
	assert.Contains(t, log, "NATIVE RETRY start=2 wait=1s cause=unknown-model")
	assert.Contains(t, log, "NATIVE RETRY start=4 wait=1s cause=unknown-model")
	assert.Equal(t, 0, res.rc, "the fourth start ran: %s", log)
	assert.NotContains(t, log, "PROVIDER-5XX", "a start that got past is no hand-back")
	launches, err := os.ReadFile(filepath.Join(job, "launches"))
	require.NoError(t, err)
	assert.Equal(t, 4, strings.Count(string(launches), "\n"), "four starts, one job: %s", launches)
	assert.Zero(t, res.starts)
}

// TestEveryStartFailedHandsBackNamingThem: a harness that never gets past its start is
// started ten times (the first and nine retries, waiting 31 s in all), then handed back as
// a provider failure whose cause is the harness catalog's refusal and names the starts.
func TestEveryStartFailedHandsBackNamingThem(t *testing.T) {
	t.Parallel()
	res, log, waits, job := startRun(t, "start-spent", "a card\nFAKE-LAUNCHES\nFAKE-START-FAIL 99\n")
	assert.Equal(t, harnessStartWaits, waits, "every wait of the schedule, once")
	assert.Equal(t, 9, strings.Count(log, "NATIVE RETRY "))
	assert.Equal(t, 10, res.starts)
	launches, err := os.ReadFile(filepath.Join(job, "launches"))
	require.NoError(t, err)
	assert.Equal(t, 10, strings.Count(string(launches), "\n"))
	assert.Contains(t, log, "NATIVE PROVIDER-5XX ")
	assert.Contains(t, log, "reason=provider: class=unknown-model status=- msg=model not found in the harness catalog: openrouter/x-ai/grok-4.7 (harness starts tried: 10)")
}

// TestALaunchReadsTheMachinesOneCatalog: with a catalog in place under the root, the
// launch is handed a copy of it in its own data home and the harness's own fetch is off,
// so the model is found with the same catalog whether or not the network answers; with
// none, the launch says so once and the harness fetches its own.
func TestALaunchReadsTheMachinesOneCatalog(t *testing.T) {
	t.Parallel()
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	catalog := `{"fake":{"id":"fake","models":{"fake-model":{"id":"fake-model","limit":{"context":200000,"output":32000}}}}}`
	require.NoError(t, os.MkdirAll(filepath.Join(root, "catalog"), 0o755))
	require.NoError(t, os.WriteFile(catalogFile(root), []byte(catalog), 0o644))
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "seeded", card: []byte("FAKE-CATALOG\n"),
		slotDir: slot, root: root, deadline: 30 * time.Second, noWall: true,
	}, &errOut)
	require.Equal(t, 0, code, "%s", errOut.String())
	raw, err := os.ReadFile(filepath.Join(slot, "jobs", "seeded", "RESULT.md"))
	require.NoError(t, err)
	got := string(raw)
	assert.Contains(t, got, "/slot-1/data/.cache/opencode/models.json\n", "a copy in the launch's own data home")
	assert.Contains(t, got, "fetch_disabled=1", "the harness's own fetch is off")
	assert.Contains(t, got, "print_logs=1", "the harness prints its ERROR lines")
	assert.Contains(t, got, fmt.Sprintf("sha256=%x", sha256.Sum256([]byte(catalog))), "the launch reads the machine's catalog byte for byte")
	assert.NotContains(t, errOut.String(), "NATIVE NOTE catalog")

	_, bare := aSlot(t)
	var none bytes.Buffer
	_, code = nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "bare", card: []byte("FAKE-CATALOG\n"),
		slotDir: bare, root: filepath.Dir(bare), deadline: 30 * time.Second, noWall: true,
	}, &none)
	require.Equal(t, 0, code, "%s", none.String())
	assert.Equal(t, 1, strings.Count(none.String(), "NATIVE NOTE catalog: no catalog at "), "%s", none.String())
}

// TestStatusGrammar pins the one status grammar across the verbs (docs/STANDARD.md
// section 2, "The status word leads every line", and its exit table: 0 done, 1 the verb
// ran and said no, 2 usage or a store that did not answer). Every row drives a verb
// through the tool's run function -- member through cmdMember, whose send parameter is
// the package's fake seam -- with the fixtures the package's own tests use, to its OK,
// REFUSED, and non-zero outcomes (DRIFT for lint and worker, RESULT REFUSED for verify,
// CARD REFUSED for native's public-class gate, and STAGE FAIL for native staging where
// the rename stays held), and asserts the line prefix, the first word after the verb
// token, and the exit code together, so none moves without the others. Four OK rows
// carry no status line: help prints the banner, template prints a verbatim document,
// version prints the one buildinfo line and profile prints its PROFILE and PROFILE
// SUMMARY event lines, and none is a status line.
func TestStatusGrammar(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		drive    func(t *testing.T) (exit int, stdout, stderr string)
		wantExit int
		wantLine string
		token    string
		wantWord string
	}{
		{"help_ok", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "help")
		}, 0, "", "", ""},
		{"template_ok", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "template", "--name", "card")
		}, 0, "", "", ""},
		{"template_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "template", "--nope")
		}, 2, "nova-swarm template REFUSED:", "nova-swarm template", "REFUSED"},
		{"lint_ok", func(t *testing.T) (int, string, string) {
			card, err := swarm.Template("card")
			require.NoError(t, err)
			return runSwarm(t, "lint", "--card", writeLintCard(t, "card.md", card), "--child-rules")
		}, 0, "LINT OK card=card.md", "LINT", "OK"},
		{"lint_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "lint", "--nope")
		}, 2, "nova-swarm lint REFUSED:", "nova-swarm lint", "REFUSED"},
		{"lint_drift", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "lint", "--card", writeLintCard(t, "empty.md", ""))
		}, 1, "LINT DRIFT card=empty.md", "LINT", "DRIFT"},
		{"member_ok", func(t *testing.T) (int, string, string) {
			args := append(memberWithoutOnce(t.TempDir()), "--ticks", "2", "--every", "1ms")
			var out, errb bytes.Buffer
			code := cmdMember(args[1:], &out, &errb, noServer)
			return code, out.String(), errb.String()
		}, 0, "MEMBER OK as=m1 ticks=2 running=0", "MEMBER", "OK"},
		{"member_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "member", "--nope")
		}, 2, "nova-swarm member REFUSED:", "nova-swarm member", "REFUSED"},
		{"native_ok", func(t *testing.T) (int, string, string) {
			windowsIsNotABench(t)
			bin := nativeHarness(t)
			root, slot := aSlot(t)
			cardPath := filepath.Join(root, "card.md")
			require.NoError(t, os.WriteFile(cardPath, []byte("a card\n"), 0o644))
			stdoutMu.Lock()
			defer stdoutMu.Unlock()
			var out, errb bytes.Buffer
			code := run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1",
				"--harness", bin, "--model", "fake/fake-model", "--label", "lbl", "--card", cardPath,
				"--slot", slot, "--root", root, "--deadline", "30s", "--no-wall"},
				strings.NewReader(""), &out, &errb, time.Now())
			return code, out.String(), errb.String()
		}, 0, "NATIVE OK label=lbl", "NATIVE", "OK"},
		{"native_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "native", "--nope")
		}, 2, "nova-swarm native REFUSED:", "nova-swarm native", "REFUSED"},
		{"native_public_class_refused", func(t *testing.T) (int, string, string) {
			root, slot := aSlot(t)
			cardPath := filepath.Join(root, "card.md")
			require.NoError(t, os.WriteFile(cardPath, []byte("a card\n"), 0o644))
			return runSwarm(t, "native", "--tokens", "unmetered", "--harness", nativeHarness(t),
				"--card", cardPath, "--slot", slot, "--root", root, "--deadline", "30s", "--no-wall",
				"--worker", workerCheckFixture(t, func(d map[string]any) { d["class"] = "public" }))
		}, 1, "CARD REFUSED reason=private-source repo=- class=public worker=check-1", "CARD", "REFUSED"},
		{"native_stage_fail", func(t *testing.T) (int, string, string) {
			windowsIsNotABench(t)
			bin := nativeHarness(t)
			root, slot := aSlot(t)
			cardPath := filepath.Join(root, "card.md")
			cardText := "RESULT: card-stage-fail sha=123456789012\nREPO: https://example.com/mas-bandwidth/missing-mirror.git\nBASE: dev\n"
			require.NoError(t, os.WriteFile(cardPath, []byte(cardText), 0o644))
			stdoutMu.Lock()
			defer stdoutMu.Unlock()
			var out, errb bytes.Buffer
			var code int
			line := captureStageOutput(t, func() {
				code = run([]string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1",
					"--harness", bin, "--model", "fake/fake-model", "--label", "card-stage-fail", "--card", cardPath,
					"--slot", slot, "--root", root, "--deadline", "30s", "--no-wall"},
					strings.NewReader(""), &out, &errb, time.Now())
			})
			return code, line + "\n" + out.String(), errb.String()
		}, 2, "STAGE FAIL bench=", "STAGE", "FAIL"},
		{"worker_ok", func(t *testing.T) (int, string, string) {
			return runWorkerCheck(workerCheckFixture(t, nil))
		}, 0, "WORKER OK check-1", "WORKER", "OK"},
		{"worker_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "worker", "check", filepath.Join(t.TempDir(), "absent.json"))
		}, 2, "nova-swarm worker check REFUSED:", "nova-swarm worker check", "REFUSED"},
		{"worker_drift", func(t *testing.T) (int, string, string) {
			bad := filepath.Join(t.TempDir(), "w.json")
			require.NoError(t, os.WriteFile(bad, []byte("{}\n"), 0o600))
			return runWorkerCheck(bad)
		}, 1, "WORKER DRIFT name:", "WORKER", "DRIFT"},
		{"verify_ok", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "verify", "--result", aResult(t, "RESULT: lbl sha=abc123\nverdict: ok\n"),
				"--contract", "RESULT: lbl sha=abc123", "--label", "lbl")
		}, 0, "RESULT OK", "RESULT", "OK"},
		{"verify_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "verify", "--nope")
		}, 2, "nova-swarm verify REFUSED:", "nova-swarm verify", "REFUSED"},
		{"verify_result_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "verify", "--result", aResult(t, "SOMETHING ELSE\nverdict: ok\n"),
				"--contract", "RESULT: lbl sha=abc123", "--label", "lbl")
		}, 1, "RESULT REFUSED", "RESULT", "REFUSED"},
		{"doctor_ok", func(t *testing.T) (int, string, string) {
			bin := filepath.Join(t.TempDir(), "nova-swarm")
			require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\necho nova-swarm v0.0.0-test sha=00000000\n"), 0o755))
			return runSwarm(t, "doctor", "--path", bin, "--local", bin)
		}, 0, "DOCTOR OK", "DOCTOR", "OK"},
		{"doctor_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "doctor", "--nope")
		}, 2, "nova-swarm doctor REFUSED:", "nova-swarm doctor", "REFUSED"},
		{"profile_ok", func(t *testing.T) (int, string, string) {
			root := t.TempDir()
			timelineFile(t, filepath.Join(root, "job-a"),
				"t_start\tt_end\ttool\twall_ms\tinput_tokens\toutput_tokens\n"+
					"2026-09-17T00:00:00Z\t2026-09-17T00:00:10Z\tmodel\t10000\t100\t20\n")
			return runSwarm(t, "profile", "--jobs", filepath.Join(root, "job-a"))
		}, 0, "", "", ""},
		{"profile_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "profile", "--nope")
		}, 2, "nova-swarm profile REFUSED:", "nova-swarm profile", "REFUSED"},
		{"slots_ok", func(t *testing.T) (int, string, string) {
			store := slotShares(t, "capacity\t1\nreserve\t0\nalice\t1\n")
			return runSwarm(t, "slots", "list", "--store", store)
		}, 0, "SLOTS OK store=", "SLOTS", "OK"},
		{"slots_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "slots", "list", "--nope")
		}, 2, "nova-swarm slots list REFUSED:", "nova-swarm slots list", "REFUSED"},
		{"version_ok", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "version")
		}, 0, "", "", ""},
		{"version_refused", func(t *testing.T) (int, string, string) {
			return runSwarm(t, "version", "extra")
		}, 2, "nova-swarm version REFUSED:", "nova-swarm version", "REFUSED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			exit, stdout, stderr := tc.drive(t)
			assert.Equal(t, tc.wantExit, exit, "stdout:\n%s\nstderr:\n%s", stdout, stderr)
			if tc.wantLine != "" {
				foundPrefix := false
				for _, out := range []string{stdout, stderr} {
					for _, line := range strings.Split(out, "\n") {
						if strings.HasPrefix(line, tc.wantLine) {
							foundPrefix = true
							break
						}
					}
					if foundPrefix {
						break
					}
				}
				require.True(t, foundPrefix, "no line opens with %q\nstdout:\n%s\nstderr:\n%s", tc.wantLine, stdout, stderr)
			}
			if tc.token == "" {
				for _, out := range []string{stdout, stderr} {
					for _, line := range strings.Split(out, "\n") {
						for _, tok := range verbTokens {
							if rest, ok := strings.CutPrefix(line, tok); ok && strings.HasPrefix(rest, " ") {
								rest = strings.TrimLeft(rest, " ")
								word, _, _ := strings.Cut(rest, " ")
								word = strings.TrimRight(word, ":")
								assert.False(t, statusWords[word], "line has status word %q after verb token %q: %q", word, tok, line)
							}
						}
					}
				}
				return
			}
			out := stdout + "\n" + stderr
			word, found := statusAfter(out, tc.token)
			require.True(t, found, "output lacks token %q on any line:\nstdout:\n%s\nstderr:\n%s", tc.token, stdout, stderr)
			assert.Equal(t, tc.wantWord, word, "first word after %q = %q, want %q (exit %d)\nstdout:\n%s\nstderr:\n%s", tc.token, word, tc.wantWord, exit, stdout, stderr)
		})
	}
}

// A read's JOB.md is gated on the packages its diff touches, read off the staged checkout
// (git diff --name-only <start>..HEAD), and names the machine's shared build cache.
func TestAReadsGateIsReadOffItsDiff(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	seed, origin := filepath.Join(root, "seed"), filepath.Join(root, "origin.git")
	runGit(t, "", "init", "-q", "-b", "main", "--", seed)
	for rel, body := range map[string]string{
		"go.mod":                 "module example.com/m\n\ngo 1.26\n",
		"internal/sprint/x.go":   "package sprint\n",
		"pkg/swarm/s.go":         "package swarm\n",
		"pkg/swarm/doc_test.go":  "package swarm\n\nvar doc = \"../../docs/SPEC-FIXTURE.md\"\n",
		"internal/ci/ci_test.go": "package ci\n\nfunc TestEverything(t *testing.T) {}\n",
		"docs/SPEC-FIXTURE.md":   "# swarm\n",
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
	_, ferr := installFrame(nativeRunConfig{slotDir: slot, root: root, model: fr.Model, frame: fr}, job, st.BaseSha, nil)
	require.NoError(t, ferr)
	text, err := os.ReadFile(filepath.Join(job, cardcontract.JobName))
	require.NoError(t, err)
	assert.Contains(t, string(text), "    nice -n 19 go test -count=1 -timeout 600s ./internal/sprint ./pkg/swarm\n")
	assert.NotContains(t, string(text), "./internal/ci\n", "internal/ci is never run whole, and no test of it reads the doc")
	assert.Contains(t, string(text), "GOCACHE is "+filepath.Join(root, "cache", "go-build"))
}

// An operational ancestry-check failure cannot prove that the checkout has only
// one line of work (docs/SPEC-CARD-CONTRACT.md section 4).
func TestReviewAnAncestryErrorCannotChooseBetweenCheckoutTips(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	other := b.secondLine(t)
	head := b.commit(t, "the work\n")
	g := b.pusher()
	g.git = func(ctx context.Context, o gitrun.Options, args ...string) (gitrun.Result, error) {
		if len(args) == 5 && args[0] == "merge-base" && args[1] == "--is-ancestor" && args[4] == other {
			return gitrun.Result{Stderr: []byte("fatal: injected object read failure\n")}, assert.AnError
		}
		return gitrun.Run(ctx, o, args...)
	}

	got := g.Push(b.p, member.Result{Head: wrongTail(head)})

	assert.Empty(t, got.Sha, "a failed ancestry check does not establish a unique tip")
	assert.NotEmpty(t, got.Refused, "the member refuses when uniqueness is unproved")
	assert.Empty(t, b.originHas(t, "sprint/c1"), "neither candidate is published")
}

// A fallback note reports a completed push, so a refused push cannot print it
// (docs/SPEC-CARD-CONTRACT.md section 4).
func TestReviewARefusedFallbackPushDoesNotClaimItWasPushed(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	head := b.commit(t, "the work\n")
	g := b.pusher()
	g.git = (&recordGitRun{refusePush: "fatal: injected credential failure"}).run
	var notes strings.Builder
	g.notes = &notes

	got := g.Push(b.p, member.Result{Head: wrongTail(head)})

	assert.Empty(t, got.Sha)
	assert.Contains(t, got.Refused, "injected credential failure")
	assert.Empty(t, b.originHas(t, "sprint/c1"))
	assert.NotContains(t, notes.String(), "was pushed", "failed pushes must not be reported as successful")
}

// A rework staged at the tip of its base branch finishes from the commit staging made there
// (pkg/swarm restageAtTip: the tip with the attempt before's work carried on top): a child
// that continues from it is pushed, and a head on the attempt before's old base is refused for
// not descending from the staged commit. nongo-11 attempt 262 was refused the other way round:
// staged at the old head, it restarted from the tip as its fix asked (nova-tools#5215). The
// member reads where the rework was staged from native's log, for the finish's report.
func TestAReworkStagedAtTheTipFinishesFromItAndNotFromTheOldHead(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	gitAs(t, b.checkout, "switch", "-q", "-c", "attempt1", b.base)
	prev := b.commit(t, "attempt 1\n") // the head attempt 1 pushed, on the old base
	gitAs(t, b.checkout, "switch", "-q", "-C", "rowan/c1", b.base)
	write(t, filepath.Join(b.checkout, "g"), "landed since\n")
	gitAs(t, b.checkout, "add", "g")
	gitAs(t, b.checkout, "commit", "-q", "-m", "landed since")    // the base branch's tip
	gitAs(t, b.checkout, "cherry-pick", "--end-of-options", prev) // attempt 1's work carried onto it
	carried := gitAs(t, b.checkout, "rev-parse", "HEAD")
	b.staged(t, carried)

	gitAs(t, b.checkout, "switch", "-q", "attempt1")
	stale := b.commit(t, "fixed on the old base\n")
	gitAs(t, b.checkout, "switch", "-q", "rowan/c1")
	got := b.pusher().Push(b.p, member.Result{Head: stale})
	assert.Empty(t, got.Sha)
	assert.Contains(t, got.Refused, "does not descend from the staged commit "+carried)

	head := b.commit(t, "fixed on the tip\n")
	assert.Equal(t, member.Push{Sha: head}, b.pusher().Push(b.p, member.Result{Head: head}), "a head from the staged commit is pushed")
	assert.Equal(t, head, b.originHas(t, b.p.Branch))

	carry := cardcontract.Carry{Base: "main", Tip: gitAs(t, b.checkout, "rev-parse", carried+"^"), Prev: prev, From: 1, Staged: carried, State: cardcontract.CarryOK}
	done := make(chan struct{})
	close(done)
	logPath := filepath.Join(b.root, "c1.native.log")
	write(t, logPath, "STAGE OK bench=b repo=r base=x secs=1 clone=0.1 fetch=0.1 checkout=0.1\n"+carry.Line()+"\nFRAME OK secs=0.1\n")
	c := &nativeChild{card: "c1", logPath: logPath, results: filepath.Join(b.root, "results"), job: filepath.Join(b.root, "job"), done: done}
	assert.Equal(t, carry.Words(), c.Result().Carry, "the finish's report is told where the rework was staged")
}

// A carried rework's staged commit is the attempt before's head: a commit origin holds and
// the checkout started from. The member's push repository holds it itself once the checkout
// is fetched, and borrows nothing from the bench mirror, which is refreshed under it. On
// 2026-10-03 (tokens-version-go attempt 3, rerate2-use-redis attempt 2) the push repository
// read the staged commit through the mirror's objects, the mirror lost it between the fetch
// and the count (the attempt before's branch gone with a refresh), and a correct carry was
// refused "does not descend from the staged commit: fatal: Not a valid commit name <staged>".
// Here the mirror is origin itself (a card naming a repository on disk), and it loses the
// staged commit after the fetch and before the count.
func TestACarriedReworksStagedCommitIsThePushRepositorysOwn(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	prev := b.commit(t, "attempt 1\n")
	gitAs(t, b.checkout, "push", "-q", "origin", prev+":refs/heads/sprint/c1.a1") // attempt 1's push
	b.staged(t, prev)                                                             // attempt 2 starts from it
	head := b.commit(t, "attempt 2\n")
	g := b.pusher()
	real, lost := g.git, false
	g.git = func(ctx context.Context, o gitrun.Options, args ...string) (gitrun.Result, error) {
		if args[0] == "merge-base" && !lost {
			lost = true
			runGit(t, b.origin, "branch", "-q", "-D", "sprint/c1.a1")
			runGit(t, b.origin, "gc", "-q", "--prune=now")
		}
		return real(ctx, o, args...)
	}
	assert.Equal(t, member.Push{Sha: head}, g.Push(b.p, member.Result{Head: head}))
	require.True(t, lost, "the mirror lost the staged commit before the count")
	assert.Equal(t, head, b.originHas(t, b.p.Branch))
	assert.NoFileExists(t, filepath.Join(b.root, "push.git", "objects", "info", "alternates"), "the push repository borrows no objects")
}

// The member pushes the child's commit to origin's branch the sprint named,
// from its own repository: the checkout's pre-push hook, credential helper and
// ssh command (the child's, written inside the wall) never run, and the
// launch's refs are gone from the push repository after.
func TestThePushPutsTheChildsCommitOnOriginsBranch(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	head := b.commit(t, "the work\n")
	marks := filepath.Join(b.root, "marks")
	hook := filepath.Join(b.checkout, ".git", "hooks", "pre-push")
	require.NoError(t, os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marks+"-hook\n"), 0o755))
	runGit(t, b.checkout, "config", "credential.helper", "!touch "+marks+"-credential")
	runGit(t, b.checkout, "config", "core.sshCommand", "touch "+marks+"-ssh")
	runGit(t, b.checkout, "config", "core.fsmonitor", "touch "+marks+"-fsmonitor")
	runGit(t, b.checkout, "config", "remote.origin.pushurl", filepath.Join(b.root, "elsewhere.git"))

	got := b.pusher().Push(b.p, member.Result{Ran: true, OK: true, Head: head[:12]})

	assert.Equal(t, member.Push{Sha: head}, got)
	assert.Equal(t, head, b.originHas(t, "sprint/c1"), "origin's branch holds the child's commit")
	for _, m := range []string{"-hook", "-credential", "-ssh", "-fsmonitor"} {
		assert.NoFileExists(t, marks+m, "the checkout's own configuration ran with the member's credential")
	}
	assert.NoDirExists(t, filepath.Join(b.root, "elsewhere.git"), "the checkout's pushurl is never read")
	assert.Empty(t, runGit(t, filepath.Join(b.root, "push.git"), "for-each-ref", "refs/member/"), "the launch's refs are dropped after the push")
}

// A second push of the same card is the same commit: origin is up to date and
// the push says so as a push, never a refusal.
func TestAPushRepeatedIsTheSamePush(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	head := b.commit(t, "the work\n")
	g := b.pusher()
	require.Equal(t, member.Push{Sha: head}, g.Push(b.p, member.Result{Head: head}))
	assert.Equal(t, member.Push{Sha: head}, g.Push(b.p, member.Result{Head: head}))
}

// A child that committed nothing (its head is origin's) is not pushed, and
// origin has no branch for it.
func TestAChildThatCommittedNothingIsNotPushed(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	got := b.pusher().Push(b.p, member.Result{Ran: true, OK: true, Head: b.base})
	assert.Empty(t, got.Sha)
	assert.Empty(t, got.Refused)
	assert.Contains(t, got.None, "the child committed nothing")
	assert.Empty(t, b.originHas(t, "sprint/c1"))
}

// A branch origin holds at another commit refuses the push with git's own
// line, and is left as it was: the member never forces.
func TestAPushOriginRefusesIsRefusedWithGitsLineAndNeverForced(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	other := filepath.Join(b.root, "other")
	runGit(t, "", "clone", "-q", "--", b.origin, other)
	require.NoError(t, os.WriteFile(filepath.Join(other, "f"), []byte("someone else\n"), 0o644))
	gitAs(t, other, "commit", "-q", "-am", "someone else")
	theirs := gitAs(t, other, "rev-parse", "HEAD")
	runGit(t, other, "push", "-q", "origin", "HEAD:refs/heads/sprint/c1")
	head := b.commit(t, "the work\n")

	got := b.pusher().Push(b.p, member.Result{Head: head})

	assert.Empty(t, got.Sha)
	assert.Contains(t, got.Refused, "[rejected]")
	assert.NotContains(t, got.Refused, "\t", "git's line is one line, no tab")
	assert.Equal(t, theirs, b.originHas(t, "sprint/c1"), "the branch is never forced")
}

// The push's own argv: from the member's push repository (never the
// checkout), --no-verify, `--` before the URL and the refspec, the full sha
// onto the sprint's branch, and no force in any form.
func TestThePushArgvIsUnforcedAndBehindTheSeparator(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	head := b.commit(t, "the work\n")
	rec := &recordGitRun{}
	g := b.pusher()
	g.git = rec.run
	require.Equal(t, member.Push{Sha: head}, g.Push(b.p, member.Result{Head: head}))
	var push []string
	for i, a := range rec.argv {
		assert.NotEqual(t, b.checkout, rec.calls[i].C, "git %v ran in the checkout", a)
		if len(a) > 0 && a[0] == "push" {
			push = a
			assert.Equal(t, filepath.Join(b.root, "push.git"), rec.calls[i].C)
			assert.Contains(t, rec.calls[i].Env, "GIT_TERMINAL_PROMPT=0", "a push with no credential is refused, never left at a prompt")
		}
	}
	require.NotNil(t, push, "a push was run")
	assert.Equal(t, []string{"push", "-q", "--porcelain", "--no-verify", "--", b.origin, head + ":refs/heads/sprint/c1"}, push)
	for _, a := range push {
		assert.NotContains(t, []string{"-f", "--force", "--force-with-lease", "--mirror", "--delete"}, a)
		assert.False(t, strings.HasPrefix(a, "+"), "a forced refspec: %s", a)
	}
}

// A git that refuses the push (a machine with no credential for origin) is a
// refusal carrying git's own fatal line.
func TestAGitThatRefusesThePushIsRefused(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	head := b.commit(t, "the work\n")
	line := "fatal: could not read Username for the origin: terminal prompts disabled"
	g := b.pusher()
	g.git = (&recordGitRun{refusePush: line}).run
	assert.Equal(t, member.Push{Refused: line}, g.Push(b.p, member.Result{Head: head}))
	assert.Empty(t, b.originHas(t, "sprint/c1"))
}

// What is not pushed, each said: a head that is not a sha, a branch that is
// not a branch name (a refspec in disguise), a card that names no repository,
// a checkout that is not there, a head the checkout's branches do not hold (with two lines
// of work on them, so the checkout has no one head of its own to push in its place).
func TestWhatIsNotPushedIsSaid(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	b.secondLine(t)
	head := b.commit(t, "the work\n")
	g := b.pusher()
	cases := []struct {
		name    string
		p       func(member.Packet) member.Packet
		head    string
		refused string
		none    string
	}{
		{"a head that is not a sha", nil, "--upload-pack=x", `the result's head "--upload-pack=x" is not a sha`, ""},
		{"a branch with a refspec colon", func(p member.Packet) member.Packet { p.Branch = "x:refs/heads/main"; return p }, head, "is not a branch name", ""},
		{"a branch that starts with a dash", func(p member.Packet) member.Packet { p.Branch = "-f"; return p }, head, "is not a branch name", ""},
		{"a branch with ..", func(p member.Packet) member.Packet { p.Branch = "sprint/../main"; return p }, head, "is not a branch name", ""},
		{"a card that names no repository", func(p member.Packet) member.Packet { p.Brief = "RESULT: c1\n\nDo it."; return p }, head, "", "the card names no repository"},
		{"no checkout", func(p member.Packet) member.Packet { p.Gen = 9; return p }, head, "no checkout at ", ""},
		{"a head the branches do not hold", nil, "deadbeefdeadbeef", "is not a commit on the checkout's branches", ""},
	}
	for _, tc := range cases {
		p := b.p
		if tc.p != nil {
			p = tc.p(p)
		}
		got := g.Push(p, member.Result{Head: tc.head})
		assert.Empty(t, got.Sha, tc.name)
		if tc.refused != "" {
			assert.Contains(t, got.Refused, tc.refused, tc.name)
		}
		if tc.none != "" {
			assert.Equal(t, tc.none, got.None, tc.name)
		}
	}
	assert.Empty(t, b.originHas(t, "sprint/c1"), "nothing of the refused was pushed")
	assert.Equal(t, b.base, b.originHas(t, "main"), "origin's main is as it was")
}

// A rework staged at the attempt before's pushed head, whose checkout's
// origin refs do not hold that head (a stale bench mirror), and whose child
// committed nothing, is not pushed: the child's commits are counted from the
// staged commit, never from the checkout's refs (docs/SPEC-CARD-CONTRACT.md
// section 4; red before the count moved off the refs).
func TestAReworkOnAStaleMirrorThatCommittedNothingIsNotPushed(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	h1 := b.commit(t, "attempt one\n") // on no origin ref the checkout knows: a stale mirror
	b.staged(t, h1)
	got := b.pusher().Push(b.p, member.Result{Ran: true, OK: true, Head: h1})
	assert.Empty(t, got.Sha, "attempt one's head is not attempt two's commit")
	assert.Contains(t, got.None, "the child committed nothing")
	assert.Empty(t, b.originHas(t, "sprint/c1"))
}

// A child that removed the checkout's remote and committed nothing is not
// pushed either: its refs are not the member's evidence.
func TestAChildThatRemovedTheRemoteAndCommittedNothingIsNotPushed(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	runGit(t, b.checkout, "remote", "remove", "origin")
	got := b.pusher().Push(b.p, member.Result{Ran: true, OK: true, Head: b.base})
	assert.Empty(t, got.Sha)
	assert.Contains(t, got.None, "the child committed nothing")
	assert.Empty(t, b.originHas(t, "sprint/c1"))
}

// A commit another launch left in the member's push repository is not this
// launch's: the head must be on this checkout's branches or HEAD, and descend
// from the commit this launch was staged at (docs/SPEC-CARD-CONTRACT.md
// section 4; red when the count alone decided).
func TestACommitAnotherLaunchLeftInThePushRepositoryIsRefused(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	g := b.pusher()
	foreign := b.commit(t, "card one's change\n")
	require.Equal(t, member.Push{Sha: foreign}, g.Push(b.p, member.Result{Head: foreign}), "card one's push leaves its objects in push.git")

	otherSeed, otherOrigin := filepath.Join(b.root, "other-seed"), filepath.Join(b.root, "other.git")
	runGit(t, "", "init", "-q", "-b", "main", "--", otherSeed)
	require.NoError(t, os.WriteFile(filepath.Join(otherSeed, "f"), []byte("other base\n"), 0o644))
	gitAs(t, otherSeed, "add", "f")
	gitAs(t, otherSeed, "commit", "-q", "-m", "other base")
	runGit(t, "", "clone", "-q", "--bare", "--", otherSeed, otherOrigin)
	p2 := member.Packet{Card: "c2", Kind: "work", As: "m1", Primary: "p2", Stream: "s2", Attempt: 1, Gen: 1, Epoch: 7,
		Brief: "RESULT: c2\nbase-repo: " + otherOrigin + "\nBASE: main\n\nOther work.", Branch: "sprint/c2"}
	checkout := filepath.Join(b.slots, launchName(p2), "jobs", "c2", swarm.JobRepo)
	runGit(t, "", "clone", "-q", "--", otherOrigin, checkout)
	write(t, filepath.Join(b.slots, launchName(p2), "staged"), gitAs(t, otherOrigin, "rev-parse", "main")+"\n")

	got := g.Push(p2, member.Result{Head: foreign})
	assert.Empty(t, got.Sha)
	assert.Contains(t, got.Refused, "is not on this checkout's branches or HEAD")
	assert.Empty(t, strings.TrimSpace(runGit(t, otherOrigin, "for-each-ref", "refs/heads/sprint/c2")))
}

// A head on this checkout that does not descend from the staged commit (the
// child reset to another history) is refused, never pushed.
func TestAHeadThatDoesNotDescendFromTheStagedCommitIsRefused(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	head := b.commit(t, "the work\n")
	gitAs(t, b.checkout, "checkout", "-q", "--orphan", "elsewhere")
	gitAs(t, b.checkout, "commit", "-q", "-m", "another history")
	orphan := gitAs(t, b.checkout, "rev-parse", "HEAD")
	b.staged(t, head)
	got := b.pusher().Push(b.p, member.Result{Head: orphan})
	assert.Empty(t, got.Sha)
	assert.Contains(t, got.Refused, "does not descend from the staged commit")
}

// A result whose head names no commit of the checkout (its tail invented), from a child that
// made exactly one line of work, pushes the checkout's own head and says so: five of the first
// twelve failures of the load test were this, the commit on the checkout's branch all along.
func TestAWrongTailHeadPushesTheCheckoutsOwnCommit(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	head := b.commit(t, "the work\n")
	claimed := wrongTail(head)
	g := b.pusher()
	var notes strings.Builder
	g.notes = &notes

	got := g.Push(b.p, member.Result{Ran: true, OK: true, Head: claimed})

	assert.Equal(t, member.Push{Sha: head}, got)
	assert.Equal(t, head, b.originHas(t, "sprint/c1"), "origin's branch holds the checkout's commit")
	assert.Equal(t, "NOTE push c1 head: the result named "+claimed+", which is no commit of the checkout; the checkout's own head "+head+" was pushed\n", notes.String())
}

// A result head the checkout does not hold, with two lines of work on its branches, is refused
// as before: the member does not choose between them. With no line of work it is refused too.
func TestAnAbsentHeadWithTwoCandidateBranchesIsStillRefused(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	b.secondLine(t)
	head := b.commit(t, "the work\n")
	g := b.pusher()
	var notes strings.Builder
	g.notes = &notes
	got := g.Push(b.p, member.Result{Head: wrongTail(head)})
	assert.Equal(t, member.Push{Refused: "the result's head " + wrongTail(head) + " is not a commit on the checkout's branches"}, got)
	assert.Empty(t, b.originHas(t, "sprint/c1"))
	assert.Empty(t, notes.String())

	none := newPushBench(t)
	got = none.pusher().Push(none.p, member.Result{Head: wrongTail(none.base)})
	assert.Equal(t, member.Push{Refused: "the result's head " + wrongTail(none.base) + " is not a commit on the checkout's branches"}, got, "no line of work: nothing to push in its place")
	assert.Empty(t, none.originHas(t, "sprint/c1"))
}

// A result head the checkout holds is pushed exactly as before, and nothing is noted, even
// when the checkout holds a second line of work beside it.
func TestARightHeadIsPushedAsBefore(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	b.secondLine(t)
	head := b.commit(t, "the work\n")
	g := b.pusher()
	var notes strings.Builder
	g.notes = &notes
	assert.Equal(t, member.Push{Sha: head}, g.Push(b.p, member.Result{Head: head}))
	assert.Equal(t, head, b.originHas(t, "sprint/c1"))
	assert.Empty(t, notes.String())
}

// A read whose frame names no bars (a pro card's, or a flash card's second read) is a
// strings read: no decision is asked and nothing is recorded. A decide read with no key in
// the environment, or bars it cannot read, says so once and the strings read runs.
func TestAReadWithNoBarsAsksNoDecision(t *testing.T) {
	t.Parallel()
	cfg, job, start, head := stagedRead(t, cardcontract.Frame{Tier: "pro"})
	fake := &fakeDecide{defect: 0.9}
	cfg.decider = &decider{backend: fake, now: time.Now}
	var out, errs bytes.Buffer
	route, op := nativeDecide(cfg, job, start, head, &out, &errs)
	assert.Equal(t, []string{"", ""}, []string{route, op})
	assert.Zero(t, fake.asks)
	assert.Empty(t, out.String()+errs.String())
	_, err := os.Stat(decideRecord(cfg.root))
	assert.True(t, os.IsNotExist(err))

	cfg.frame.DecideBounce, cfg.frame.DecideReview = "0.3", "0.5"
	route, _ = nativeDecide(cfg, job, start, head, &out, &errs)
	assert.Empty(t, route)
	assert.Contains(t, errs.String(), "NATIVE NOTE: w.r1 no decide read: decide_review 0.5 is above decide_bounce 0.3; the strings read is the band between them; the strings read runs")
	assert.Zero(t, fake.asks)
}

// An explicit full SHA that the checkout cannot resolve cannot establish a
// review start; refusal leaves no frame claiming the read is ready
// (docs/SPEC-CARD-CONTRACT.md, the frame and JOB.md).
func TestReviewAMissingImmutableBaseRefusesTheReadBeforeWritingItsFrame(t *testing.T) {
	t.Parallel()
	f := newReadFixture(t, []string{"landed-a.txt"}, []string{"landed-b.txt"}, false)
	slot := filepath.Join(t.TempDir(), "slot")
	job := f.stage(t, slot)
	const missing = "0000000000000000000000000000000000000000"

	_, err := installFrame(nativeRunConfig{slotDir: slot, model: "fake/fake-model", frame: f.frame(missing)}, job, f.head, nil)

	assert.ErrorIs(t, err, errReadStart, "an unresolved immutable base is a staging refusal")
	assert.NoFileExists(t, filepath.Join(job, cardcontract.JobName), "no reader is told to diff against a nonexistent commit")
	assert.NoFileExists(t, filepath.Join(slot, cardcontract.StagedName), "no staged frame record precedes an unknown start")
}

// A genuine no-common-ancestor result remains the explicit unknown-start
// policy, rather than an operational failure (docs/SPEC-CARD-CONTRACT.md).
func TestReviewUnrelatedHistoriesKeepTheUnknownStartPolicy(t *testing.T) {
	t.Parallel()
	f := newReadFixture(t, nil, nil, false)
	slot := filepath.Join(t.TempDir(), "slot")
	job := f.stage(t, slot)
	repo := filepath.Join(job, swarm.JobRepo)
	tree := gitAs(t, repo, "rev-parse", f.start+"^{tree}")
	// No parent: the immutable base is a valid commit of an unrelated history.
	base := gitAs(t, repo, "commit-tree", tree, "-m", "unrelated base")

	_, err := installFrame(nativeRunConfig{slotDir: slot, model: "fake/fake-model", frame: f.frame(base)}, job, f.head, nil)

	require.NoError(t, err)
	text, err := os.ReadFile(filepath.Join(job, cardcontract.JobName))
	require.NoError(t, err)
	assert.NotContains(t, string(text), "The work's change is exactly", "an unknown start cannot be stated as an exact diff")
	assert.FileExists(t, filepath.Join(slot, cardcontract.StagedName))
}

// A read staged after its base branch moved (cards landed on it while the work was read) is
// told the commit the work started from and the command that shows exactly the work's
// change, in every profile; a diff against the moved base shows the landed files too, which
// a reader on the 1000-card load test (2026-10-01) took for deletions and sent a correct
// work card back for (docs/SPEC-CARD-CONTRACT.md, JOB.md).
func TestAReadIsToldTheWorksChangeWhenTheBaseMoved(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	seed, origin := filepath.Join(root, "seed"), filepath.Join(root, "origin.git")
	runGit(t, "", "init", "-q", "-b", "main", "--", seed)
	write(t, filepath.Join(seed, "f"), "base\n")
	gitAs(t, seed, "add", "f")
	gitAs(t, seed, "commit", "-q", "-m", "base")
	runGit(t, "", "clone", "-q", "--bare", "--", seed, origin)
	start := gitAs(t, origin, "rev-parse", "main")

	// the work: one file, on its sprint branch
	w := filepath.Join(root, "w")
	runGit(t, "", "clone", "-q", "--", origin, w)
	require.NoError(t, os.MkdirAll(filepath.Join(w, "quacks"), 0o755))
	write(t, filepath.Join(w, "quacks", "w.txt"), "quack\n")
	gitAs(t, w, "add", "quacks/w.txt")
	gitAs(t, w, "commit", "-q", "-m", "quack: w")
	head := gitAs(t, w, "rev-parse", "HEAD")
	runGit(t, w, "push", "-q", "origin", "HEAD:refs/heads/sprint/w")

	// the base moves: two other cards land on main after the work began
	l := filepath.Join(root, "l")
	runGit(t, "", "clone", "-q", "--", origin, l)
	for _, name := range []string{"landed-a.txt", "landed-b.txt"} {
		write(t, filepath.Join(l, name), name+"\n")
		gitAs(t, l, "add", name)
		gitAs(t, l, "commit", "-q", "-m", name)
	}
	runGit(t, l, "push", "-q", "origin", "main")

	for _, model := range []string{"anthropic/claude-x", "openai/gpt-x", "fake/model-x"} {
		t.Run(cardcontract.FamilyOf(model), func(t *testing.T) {
			t.Parallel()
			slot := filepath.Join(root, "slot-"+cardcontract.FamilyOf(model))
			job := filepath.Join(slot, "jobs", "w.r1")
			require.NoError(t, os.MkdirAll(job, 0o755))
			fr := &cardcontract.Frame{Kind: "read", Card: "w.r1", Attempt: 1, Model: model, Repo: origin,
				BaseRef: "main", ReviewBase: "main", StageSha: head, Branch: "sprint/w"}
			st, err := swarm.StageCard(swarm.StageOptions{TargetDir: filepath.Join(job, swarm.JobRepo), JobDir: job,
				BenchHome: filepath.Join(root, "no-bench"), Base: &swarm.CardBase{Repo: origin, Sha: head, Ref: "main", Named: origin}, Branch: fr.Branch})
			require.NoError(t, err)
			require.Equal(t, head, st.BaseSha)
			checkout := filepath.Join(job, swarm.JobRepo)
			require.ElementsMatch(t, []string{"landed-a.txt", "landed-b.txt", "quacks/w.txt"},
				strings.Fields(runGit(t, checkout, "diff", "--name-only", "origin/main")), "the base moved: a diff against it is more than the work")

			_, ferr := installFrame(nativeRunConfig{slotDir: slot, model: model, frame: fr}, job, st.BaseSha, nil)
			require.NoError(t, ferr)
			jobText, err := os.ReadFile(filepath.Join(job, cardcontract.JobName))
			require.NoError(t, err)
			m := readStartRE.FindStringSubmatch(string(jobText))
			require.NotNil(t, m, "JOB.md names the command that shows the work's change:\n%s", jobText)
			assert.Equal(t, start, m[1], "the start is the commit the work began from")
			assert.Contains(t, string(jobText), "    git diff "+start+"..HEAD\n")
			assert.Contains(t, string(jobText), "main may have moved since the work began")
			assert.Contains(t, string(jobText), "a diff against the tip of main or origin/main shows every change landed since as a deletion")
			assert.Contains(t, string(jobText), "Those deletions are never the work's and never a finding: judge the work by the diff above alone.")
			assert.Equal(t, []string{"quacks/w.txt"}, strings.Fields(runGit(t, checkout, "diff", "--name-only", m[1]+"..HEAD")),
				"the command JOB.md names shows exactly the work's change")
		})
	}
}

// A base that never moves needs no fetch: a tag, or a full sha, reviews the work against
// itself with origin unreachable, and the start is still the commit the work began from.
func TestAReadAgainstATagOrAShaNeedsNoFetch(t *testing.T) {
	t.Parallel()
	f := newReadFixture(t, []string{"landed-a.txt"}, []string{"landed-b.txt"}, false)
	for name, base := range map[string]string{"tag": "v1", "sha": f.start} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			slot := filepath.Join(t.TempDir(), "slot")
			job := f.stage(t, slot)
			runGit(t, filepath.Join(job, swarm.JobRepo), "remote", "set-url", "origin", filepath.Join(t.TempDir(), "no-such-origin.git"))
			_, ferr := installFrame(nativeRunConfig{slotDir: slot, model: "fake/fake-model", frame: f.frame(base)}, job, f.head, nil)
			require.NoError(t, ferr)
			f.assertExactlyTheWork(t, job)
		})
	}
}

// A reader needs no --width: it runs the width of its machine's fleet row
// (reader-<m> is the reader on m), read with its queue every tick as a member
// reads its own (the owner, 2026-10-02: "why not just have as many readers as
// workers per-machine"). Nothing here names width as missing; the run fails
// only at the server no test has, and the start line says width=row.
func TestAReaderNeedsNoWidth(t *testing.T) {
	t.Parallel()
	var args []string
	full := memberFull(t.TempDir())
	for i := 0; i < len(full); i++ {
		if full[i] == "--width" {
			i++ // and its value
			continue
		}
		args = append(args, full[i])
	}
	args = append(args, "--reader", "--as", "reader-m1")
	var out, errb bytes.Buffer
	run(args, strings.NewReader(""), &out, &errb, time.Now())
	assert.NotContains(t, errb.String(), "--width", "a reader without --width is refused:\n%s", errb.String())
	assert.Contains(t, out.String(), "MEMBER reader as=reader-m1 width=row ", "the start line:\n%s", out.String())
}
