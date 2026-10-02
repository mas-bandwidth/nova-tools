package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
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
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	t.Setenv("NOVA_FAKE_SANDBOX", "pass")
	t.Setenv("FAKE_KEY", fakeKey)
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

// workerWithReadRoots writes a worker description whose read_roots names one directory: the
// shape a coordinator hands a card a staging mirror, a corpus or a toolchain with.
func workerWithReadRoots(t *testing.T, roots ...string) string {
	t.Helper()
	home := t.TempDir()
	desc := map[string]any{
		"name": "fake-1", "provider": "fake", "model": "fake-model",
		"env_var": "FAKE_KEY", "secret": "FAKE_KEY", "usage": "opencode",
		"harness": "fake-harness", "worker_dir": home, "deadline": "30s",
		"harness_args": []string{"run", "--model", "{model}", "--", "{prompt}"},
		"read_roots":   roots,
	}
	raw, err := json.MarshalIndent(desc, "", "  ")
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "worker.json")
	require.NoError(t, os.WriteFile(path, raw, 0o644))
	return path
}
