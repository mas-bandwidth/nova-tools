package main

// THE REFUSALS `nova-swarm native` MAKES BEFORE IT SPENDS ANYTHING (docs/SPEC-SWARM.md,
// `native`; docs/SPEC-CARD-CONTRACT.md). Each test drives the verb's own entry, nativeRun
// (native.go:1590), with a nativeRunConfig built as native_test.go builds one -- an aSlot
// and a temp file standing in for the harness binary -- and asserts on what a caller sees:
// the exit code, the one NATIVE REFUSED line, the returned result and the job directory.
// No test calls prepare or report directly and none builds a nativeRunState by hand. No
// test here uses t.Parallel: some STAGE and NOTE lines go to os.Stdout directly.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// executableStandIn is a harness binary path that exists and is executable, so a refusal
// past prepare's binary step is reached.
func executableStandIn(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fake-harness")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	return p
}

// nativePrepareRefusal runs nativeRun and returns the result, exit code and errOut.
func nativePrepareRefusal(t *testing.T, cfg nativeRunConfig) (nativeRunResult, int, string) {
	t.Helper()
	var errOut bytes.Buffer
	res, code := nativeRun(cfg, &errOut)
	return res, code, errOut.String()
}

// refusedLineCount counts the NATIVE REFUSED lines in errOut.
func refusedLineCount(errOut string) int {
	return strings.Count(errOut, "NATIVE REFUSED")
}

// prepare says the model is provider/model with one slash and both sides nonempty
// (native.go:485): a model with no prefix is refused before anything is made.
func TestPrepareRefusesAModelWithNoProviderPrefix(t *testing.T) {
	windowsIsNotABench(t)
	root, slot := aSlot(t)
	_, code, errOut := nativePrepareRefusal(t, nativeRunConfig{
		binary: executableStandIn(t), model: "fake-model", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
	})
	assert.Equal(t, 2, code, "a model with no provider prefix exits 2, got %d:\n%s", code, errOut)
	assert.Contains(t, errOut, "has no provider prefix", "the refusal does not name its reason:\n%s", errOut)
	assert.Equal(t, 1, refusedLineCount(errOut), "exactly one REFUSED line, got:\n%s", errOut)
}

// A key is authorized for one model only and the worker description pins it, so a
// description that names another model is refused naming BOTH models (native.go:486-498).
func TestPrepareRefusesAWorkerPinningAnotherModel(t *testing.T) {
	windowsIsNotABench(t)
	root, slot := aSlot(t)
	_, code, errOut := nativePrepareRefusal(t, nativeRunConfig{
		binary: executableStandIn(t), model: "fake/fake-model", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
		worker: &swarm.Worker{Name: "w", Provider: "other", Model: "other-model"},
	})
	assert.Equal(t, 2, code, "a worker pinning another model exits 2, got %d:\n%s", code, errOut)
	assert.Contains(t, errOut, "fake/fake-model", "the refusal does not name --model:\n%s", errOut)
	assert.Contains(t, errOut, "other-model", "the refusal does not name the description's model:\n%s", errOut)
	assert.Equal(t, 1, refusedLineCount(errOut), "exactly one REFUSED line, got:\n%s", errOut)
}

// A description naming "secret": "<NAME>" takes the key from the environment, and an
// absent variable is refused before anything runs; the value never printed and no job
// directory made (native.go:502-509).
func TestPrepareRefusesAWorkerSecretNamingAnUnsetVariable(t *testing.T) {
	windowsIsNotABench(t)
	root, slot := aSlot(t)
	const name = "NOVA_SWARM_PREPARE_UNSET_SECRET"
	t.Setenv(name, "")
	jobDir := filepath.Join(slot, "jobs", "lbl")
	_, code, errOut := nativePrepareRefusal(t, nativeRunConfig{
		binary: executableStandIn(t), model: "fake/fake-model", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
		worker: &swarm.Worker{Name: "w", Provider: "fake", Model: "fake-model", Secret: name},
	})
	assert.Equal(t, 2, code, "an unset secret exits 2, got %d:\n%s", code, errOut)
	assert.Contains(t, errOut, name, "the refusal does not name the variable:\n%s", errOut)
	_, err := os.Stat(jobDir)
	assert.True(t, os.IsNotExist(err), "the refusal made a job directory %s for a card it never ran", jobDir)
}

// A slot outside the configured root is a write this run has no business making
// (native.go:531-535).
func TestPrepareRefusesASlotOutsideTheConfiguredRoot(t *testing.T) {
	windowsIsNotABench(t)
	_, slot := aSlot(t)
	otherRoot := t.TempDir()
	_, code, errOut := nativePrepareRefusal(t, nativeRunConfig{
		binary: executableStandIn(t), model: "fake/fake-model", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: otherRoot, deadline: 30 * time.Second,
	})
	assert.Equal(t, 2, code, "a slot outside the root exits 2, got %d:\n%s", code, errOut)
	assert.Contains(t, errOut, "is outside the configured root", "the refusal does not name its reason:\n%s", errOut)
	assert.Equal(t, 1, refusedLineCount(errOut), "exactly one REFUSED line, got:\n%s", errOut)
}

// A label is joined into the job directory, the temp directory and the wall's write set,
// so a label that walks is refused as a name, not a path, and nothing is made
// (native.go:548-554).
func TestPrepareRefusesALabelThatWalks(t *testing.T) {
	windowsIsNotABench(t)
	root, slot := aSlot(t)
	_, code, errOut := nativePrepareRefusal(t, nativeRunConfig{
		binary: executableStandIn(t), model: "fake/fake-model", label: "../x",
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
	})
	assert.Equal(t, 2, code, "a walking label exits 2, got %d:\n%s", code, errOut)
	assert.Contains(t, errOut, "is not a job name", "the refusal does not name its reason:\n%s", errOut)
	_, err := os.Stat(filepath.Join(slot, "jobs", "..", "x"))
	assert.True(t, os.IsNotExist(err), "a walking label made a directory outside the job directory")
}

// A results root inside the job directory would make the published copy the file a sweep
// deletes, so it is refused before anything is made (native.go:562-566).
func TestPrepareRefusesAResultsRootInsideTheJobDirectory(t *testing.T) {
	windowsIsNotABench(t)
	root, slot := aSlot(t)
	label := "lbl"
	_, code, errOut := nativePrepareRefusal(t, nativeRunConfig{
		binary: executableStandIn(t), model: "fake/fake-model", label: label,
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
		resultsRoot: filepath.Join(slot, "jobs", label, "results"),
	})
	assert.Equal(t, 2, code, "a results root inside the job exits 2, got %d:\n%s", code, errOut)
	assert.Contains(t, errOut, "is inside the job directory", "the refusal does not name its reason:\n%s", errOut)
}

// Two runs in one job directory share one data home and the first to end removes the
// other's lease, so a held job directory is refused (native.go:598-604).
func TestPrepareRefusesAJobDirectoryHeldByALiveRun(t *testing.T) {
	windowsIsNotABench(t)
	root, slot := aSlot(t)
	label := "lbl"
	jobDir := filepath.Join(slot, "jobs", label)
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	release, err := swarm.StartJobLease(jobDir, "holder")
	require.NoError(t, err, "the holder could not take the job lease")
	defer release()
	_, code, errOut := nativePrepareRefusal(t, nativeRunConfig{
		binary: executableStandIn(t), model: "fake/fake-model", label: label,
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
	})
	assert.Equal(t, 2, code, "a held job directory exits 2, got %d:\n%s", code, errOut)
	assert.Contains(t, errOut, "held by a live run: pid=", "the refusal does not name the holder:\n%s", errOut)
	assert.Contains(t, errOut, "job directory", "the refusal does not name the job directory:\n%s", errOut)
}

// The data home is per slot, so a second run in one slot under another label is refused
// by the slot lease (native.go:623-629).
func TestPrepareRefusesASlotHeldByALiveRunUnderAnotherLabel(t *testing.T) {
	windowsIsNotABench(t)
	root, slot := aSlot(t)
	release, err := swarm.StartSlotLease(slot, "other-label")
	require.NoError(t, err, "the holder could not take the slot lease")
	defer release()
	_, code, errOut := nativePrepareRefusal(t, nativeRunConfig{
		binary: executableStandIn(t), model: "fake/fake-model", label: "mine",
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
	})
	assert.Equal(t, 2, code, "a held slot exits 2, got %d:\n%s", code, errOut)
	assert.Contains(t, errOut, "the slot", "the refusal does not name the slot:\n%s", errOut)
	assert.Contains(t, errOut, "held by a live run: pid=", "the refusal does not name the holder:\n%s", errOut)
}

// A public-class worker never sees a card that clones an unlisted repo: exit 1 with the
// CARD REFUSED line, before any directory is made (native.go:438-444).
func TestPrepareRefusesAPublicWorkerCardNamingAnUnlistedRepo(t *testing.T) {
	windowsIsNotABench(t)
	root, slot := aSlot(t)
	_, code, errOut := nativePrepareRefusal(t, nativeRunConfig{
		binary: executableStandIn(t), model: "fake/fake-model", label: "lbl",
		card:   []byte("git clone https://github.com/private/repo.git\n"),
		slotDir: slot, root: root, deadline: 30 * time.Second,
		worker: &swarm.Worker{Name: "pub-worker", Class: "public", Provider: "fake", Model: "fake-model"},
	})
	assert.Equal(t, 1, code, "a public worker's unlisted card exits 1, got %d:\n%s", code, errOut)
	assert.Contains(t, errOut, swarm.PublicRefusalLine("private/repo", "pub-worker"), "the refusal is not the public-class line:\n%s", errOut)
}

// An auth source looser than 0600 is refused (native.go:722), and the leases the refused
// run had already taken are released: a second run is not refused as held.
func TestPrepareReleasesTheLeasesWhenTheAuthFileIsTooLoose(t *testing.T) {
	windowsIsNotABench(t)
	root, slot := aSlot(t)
	auth := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(auth, []byte(`{"fake":"a-secret"}`), 0o644))
	cfg := nativeRunConfig{
		binary: executableStandIn(t), model: "fake/fake-model", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
		authFile: auth,
	}
	_, code, errOut := nativePrepareRefusal(t, cfg)
	assert.Equal(t, 2, code, "a loose auth file exits 2, got %d:\n%s", code, errOut)
	assert.Contains(t, errOut, "would not be 0600", "the refusal does not name the mode rule:\n%s", errOut)
	_, _, second := nativePrepareRefusal(t, cfg)
	assert.NotContains(t, second, "held by a live run", "the refused run did not release its leases; a second run was refused as held:\n%s", second)
}

// prepare's first act is to name its phase: onPhase is called with "prepare" before any
// other phase (native.go:408-410).
func TestPrepareCallsOnPhasePrepareFirst(t *testing.T) {
	windowsIsNotABench(t)
	root, slot := aSlot(t)
	var phases []string
	_, code, _ := nativePrepareRefusal(t, nativeRunConfig{
		binary: filepath.Join(t.TempDir(), "no-such-binary"),
		model:  "fake/fake-model", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root, deadline: 30 * time.Second,
		onPhase: func(phase string) { phases = append(phases, phase) },
	})
	assert.Equal(t, 2, code, "a missing binary exits 2")
	require.NotEmpty(t, phases, "onPhase was never called")
	assert.Equal(t, "prepare", phases[0], "onPhase was called with %q first, want prepare", phases[0])
}
