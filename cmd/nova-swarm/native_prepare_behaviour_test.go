package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// THE REFUSALS prepare MAKES BEFORE IT SPENDS ANYTHING (nativeRun's doc, native.go:1524:
// "A refusal is a defect in the configuration the run can see before it spends anything,
// and it names one reason."). Each is driven through nativeRun, the entry cmdNative calls,
// with a nativeRunConfig built the way native_test.go does: aSlot for the root and slot,
// and a temp executable standing in for the harness binary. A refusal is exit 2 and exactly
// one NATIVE REFUSED line naming its reason, unless the case says otherwise.

// prepareStubBinary is the harness binary these refusals reach for: it exists and is
// executable, so the binary check passes and the run reaches the behaviour under test. No
// refusal here starts it.
func prepareStubBinary(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fake-harness")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	return p
}

// prepareRefusalConfig is the frozen configuration these tests start from: a valid model,
// a valid label and the slot under the root. Each test overrides the one field it judges.
func prepareRefusalConfig(t *testing.T, root, slot string) nativeRunConfig {
	t.Helper()
	return nativeRunConfig{
		binary: prepareStubBinary(t), model: "fake/fake-model", label: "a-label",
		card: []byte("a card\n"), slotDir: slot, root: root, noSharedCaches: true,
	}
}

// assertOneRefusalRequires asserts the one REFUSED line the refusal grammar requires.
func assertOneRefusal(t *testing.T, errOut bytes.Buffer, word string) {
	t.Helper()
	require.Contains(t, errOut.String(), "NATIVE REFUSED", "the refusal is one REFUSED line, got:\n%s", errOut.String())
	require.Contains(t, errOut.String(), word, "the refusal names its reason (%s):\n%s", word, errOut.String())
	got := strings.Count(strings.TrimSpace(errOut.String()), "\n") + 1
	require.Equal(t, 1, got, "exactly one REFUSED line, got %d:\n%s", got, errOut.String())
}

// TestANativeModelWithNoProviderPrefixIsRefused pins prepare's model gate: a native model
// is provider/model, one slash, both sides nonempty (native.go, providerOf).
func TestANativeModelWithNoProviderPrefixIsRefused(t *testing.T) {
	t.Parallel()
	windowsIsNotABench(t)

	root, slot := aSlot(t)
	cfg := prepareRefusalConfig(t, root, slot)
	cfg.model = "fakemodel"
	var errOut bytes.Buffer
	_, code := nativeRun(cfg, &errOut)
	require.Equal(t, 2, code, "a model with no provider prefix exits 2, got %d:\n%s", code, errOut.String())
	assertOneRefusal(t, errOut, "has no provider prefix")
}

// TestANativeWorkerDescribingAnotherModelIsRefused pins prepare's worker gate: a key is
// authorized for one model only, and the description pins the model this run launches; a
// mismatch names BOTH models (native.go, the worker description).
func TestANativeWorkerDescribingAnotherModelIsRefused(t *testing.T) {
	t.Parallel()
	windowsIsNotABench(t)

	root, slot := aSlot(t)
	cfg := prepareRefusalConfig(t, root, slot)
	cfg.worker = &swarm.Worker{Provider: "fake", Model: "other-model"}
	var errOut bytes.Buffer
	_, code := nativeRun(cfg, &errOut)
	require.Equal(t, 2, code, "a worker description pinning another model exits 2, got %d:\n%s", code, errOut.String())
	assertOneRefusal(t, errOut, "differs from the worker description's model")
	require.Contains(t, errOut.String(), "fake/fake-model", "the refusal names --model:\n%s", errOut.String())
	require.Contains(t, errOut.String(), "other-model", "the refusal names the description's model:\n%s", errOut.String())
}

// TestANativeWorkerSecretNamingAnUnsetVariableIsRefused pins the secret gate: a description's
// secret is taken from THIS process's environment, and an absent or empty variable is refused
// before anything runs, with no job directory made (native.go, the worker description).
func TestANativeWorkerSecretNamingAnUnsetVariableIsRefused(t *testing.T) {
	t.Parallel()
	windowsIsNotABench(t)

	const absent = "NOVA_PREPARE_SECRET_THAT_IS_NOT_SET"
	require.NoError(t, os.Unsetenv(absent))
	root, slot := aSlot(t)
	cfg := prepareRefusalConfig(t, root, slot)
	cfg.worker = &swarm.Worker{Provider: "fake", Model: "fake-model", Secret: absent}
	var errOut bytes.Buffer
	_, code := nativeRun(cfg, &errOut)
	require.Equal(t, 2, code, "an unset worker secret exits 2, got %d:\n%s", code, errOut.String())
	assertOneRefusal(t, errOut, absent)
	_, statErr := os.Stat(filepath.Join(slot, "jobs", cfg.label))
	assert.True(t, os.IsNotExist(statErr), "the refusal made a job directory %s", filepath.Join(slot, "jobs", cfg.label))
}

// TestANativeSlotOutsideTheRootIsRefused pins the slot boundary: a slot outside the configured
// root is a write this run has no business making (native.go, the slot is under the root).
func TestANativeSlotOutsideTheRootIsRefused(t *testing.T) {
	t.Parallel()
	windowsIsNotABench(t)

	root, _ := aSlot(t)
	outside := filepath.Join(t.TempDir(), "slot-outside")
	require.NoError(t, os.MkdirAll(outside, 0o755))
	cfg := prepareRefusalConfig(t, root, outside)
	var errOut bytes.Buffer
	_, code := nativeRun(cfg, &errOut)
	require.Equal(t, 2, code, "a slot outside the root exits 2, got %d:\n%s", code, errOut.String())
	assertOneRefusal(t, errOut, "is outside the configured root")
	_, statErr := os.Stat(filepath.Join(outside, "jobs"))
	assert.True(t, os.IsNotExist(statErr), "the refusal made a job directory outside the root")
}

// TestANativeLabelThatWalksIsRefused pins the label-as-a-name gate: a label of ../x is judged
// as a NAME before it is joined into anything, so no directory outside the root is made
// (native.go, the label is a name, not a path).
func TestANativeLabelThatWalksIsRefused(t *testing.T) {
	t.Parallel()
	windowsIsNotABench(t)

	root, slot := aSlot(t)
	cfg := prepareRefusalConfig(t, root, slot)
	cfg.label = "../x"
	var errOut bytes.Buffer
	_, code := nativeRun(cfg, &errOut)
	require.Equal(t, 2, code, "a walking label exits 2, got %d:\n%s", code, errOut.String())
	assertOneRefusal(t, errOut, "is not a job name")
	_, statErr := os.Stat(filepath.Join(slot, "jobs"))
	assert.True(t, os.IsNotExist(statErr), "the refusal made a job directory")
	_, escape := os.Stat(filepath.Join(root, "x"))
	assert.True(t, os.IsNotExist(escape), "the walking label made %s outside the slot", filepath.Join(root, "x"))
}

// TestANativeResultsRootInsideTheJobIsRefused pins the publish boundary: a sweep deletes the
// job directory, so a results root inside it is refused (native.go, the results root is not
// the job).
func TestANativeResultsRootInsideTheJobIsRefused(t *testing.T) {
	t.Parallel()
	windowsIsNotABench(t)

	root, slot := aSlot(t)
	cfg := prepareRefusalConfig(t, root, slot)
	cfg.resultsRoot = filepath.Join(slot, "jobs", cfg.label)
	var errOut bytes.Buffer
	_, code := nativeRun(cfg, &errOut)
	require.Equal(t, 2, code, "a results root inside the job exits 2, got %d:\n%s", code, errOut.String())
	assertOneRefusal(t, errOut, "is inside the job directory")
}

// TestANativeJobDirectoryHeldByALiveRunIsRefused pins the job lease: two native runs on one
// physical job directory share one data home, one temp directory and one set of logs, so the
// second is refused HERE (native.go, the lease).
func TestANativeJobDirectoryHeldByALiveRunIsRefused(t *testing.T) {
	t.Parallel()
	windowsIsNotABench(t)

	root, slot := aSlot(t)
	jobDir := filepath.Join(slot, "jobs", "held")
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	release, err := swarm.StartJobLease(jobDir, "held")
	require.NoError(t, err, "taking the lease the second run must be refused for")
	t.Cleanup(release)

	cfg := prepareRefusalConfig(t, root, slot)
	cfg.label = "held"
	var errOut bytes.Buffer
	_, code := nativeRun(cfg, &errOut)
	require.Equal(t, 2, code, "a job directory held by a live run exits 2, got %d:\n%s", code, errOut.String())
	assertOneRefusal(t, errOut, "held by a live run: pid=")
}

// TestANativeSlotHeldByAnotherLabelIsRefused pins the slot lease: two labels in one slot share
// one data home and one opencode.db, so a second run under another label is refused
// (native.go, the slot is held by exactly one worker).
func TestANativeSlotHeldByAnotherLabelIsRefused(t *testing.T) {
	t.Parallel()
	windowsIsNotABench(t)

	root, slot := aSlot(t)
	release, err := swarm.StartSlotLease(slot, "other-label")
	require.NoError(t, err, "taking the slot lease the second run must be refused for")
	t.Cleanup(release)

	cfg := prepareRefusalConfig(t, root, slot)
	cfg.label = "my-label"
	var errOut bytes.Buffer
	_, code := nativeRun(cfg, &errOut)
	require.Equal(t, 2, code, "a slot held under another label exits 2, got %d:\n%s", code, errOut.String())
	assertOneRefusal(t, errOut, "held by a live run: pid=")
	require.Contains(t, errOut.String(), "the slot", "the refusal names the slot lease:\n%s", errOut.String())
}

// TestAPublicWorkerWithAnUnlistedRepoIsCardRefused pins the public-class gate (CARD-8390): a
// public-class worker never sees a card that clones an unlisted repo, refused with CARD
// REFUSED at exit 1 before any directory is made (native.go, the public-class gate).
func TestAPublicWorkerWithAnUnlistedRepoIsCardRefused(t *testing.T) {
	t.Parallel()
	windowsIsNotABench(t)

	root, slot := aSlot(t)
	cfg := prepareRefusalConfig(t, root, slot)
	cfg.worker = &swarm.Worker{Name: "public-w", Class: swarm.WorkerClassPublic}
	cfg.card = []byte("REPO: https://github.com/evil/private\n\nDo the work.\n")
	var errOut bytes.Buffer
	_, code := nativeRun(cfg, &errOut)
	require.Equal(t, 1, code, "a public worker with an unlisted repo exits 1, got %d:\n%s", code, errOut.String())
	require.Contains(t, errOut.String(), swarm.PublicRefusalLine("evil/private", "public-w"), "the gate prints CARD REFUSED:\n%s", errOut.String())
}

// TestANativeAuthFileLooserThan0600IsRefusedAndLeasesReleased pins the auth-copy rule and the
// release it owes: a source looser than 0600 is refused (copyAuth), and the leases that run
// took are released, so a second run with the same configuration is refused for the same
// reason and never as held (native.go, the auth copy; the lease).
func TestANativeAuthFileLooserThan0600IsRefusedAndLeasesReleased(t *testing.T) {
	t.Parallel()
	windowsIsNotABench(t)

	auth := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(auth, []byte(`{"fake":"the-fake-secret"}`), 0o644))
	root, slot := aSlot(t)
	cfg := prepareRefusalConfig(t, root, slot)
	cfg.authFile = auth

	var first bytes.Buffer
	_, code := nativeRun(cfg, &first)
	require.Equal(t, 2, code, "a loose auth file exits 2, got %d:\n%s", code, first.String())
	assertOneRefusal(t, first, "0600")

	var second bytes.Buffer
	_, code = nativeRun(cfg, &second)
	require.Equal(t, 2, code, "the second run exits 2 for the auth, got %d:\n%s", code, second.String())
	require.NotContains(t, second.String(), "held by a live run", "the first run's leases were not released:\n%s", second.String())
	require.Contains(t, second.String(), "0600", "the second run is refused for the auth rule:\n%s", second.String())
}

// TestANativeOnPhasePreparesFirst pins the phase hook: onPhase is called at the entry of each
// named phase function, with "prepare" first (native.go, nativeRunConfig.onPhase).
func TestANativeOnPhasePreparesFirst(t *testing.T) {
	t.Parallel()
	windowsIsNotABench(t)

	root, slot := aSlot(t)
	cfg := prepareRefusalConfig(t, root, slot)
	cfg.model = "fakemodel"
	var phases []string
	cfg.onPhase = func(phase string) { phases = append(phases, phase) }
	var errOut bytes.Buffer
	_, _ = nativeRun(cfg, &errOut)
	require.NotEmpty(t, phases, "onPhase was never called")
	require.Equal(t, "prepare", phases[0], "the first phase is %q, want prepare", phases[0])
}
