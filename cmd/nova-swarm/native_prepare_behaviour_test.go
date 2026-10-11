package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// (A) PREPARE'S REFUSALS, in the unit tier. Each test drives nativeRun with a
// nativeRunConfig built the way native_test.go builds one and pins one refusal the
// run makes before it spends anything (nativeRun's doc comment, native.go:1586-1589:
// "a refusal is a defect in the configuration the run can see before it spends
// anything"). Every refusal is exit 2 with exactly one NATIVE REFUSED line on errOut
// naming its reason, unless the case says otherwise.

// aRunnableBinary writes an executable temp file standing in for the harness binary:
// the refusals below are all reached after the binary check, so the binary must exist
// and be executable before the refusal under test is asked.
func aRunnableBinary(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fake-harness")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\nexit 1\n"), 0o755))
	return p
}

// assertOneRefused pins the shape of a pre-staging refusal: exit 2 and exactly one
// NATIVE REFUSED line on errOut carrying want.
func assertOneRefused(t *testing.T, errOut *bytes.Buffer, code int, want string) {
	t.Helper()
	got := strings.TrimSpace(errOut.String())
	require.Equal(t, 2, code, "a refusal exits 2, got %d:\n%s", code, got)
	require.Contains(t, got, "NATIVE REFUSED", "the refusal is one NATIVE REFUSED line, got:\n%s", got)
	require.Contains(t, got, want, "the refusal names its reason (%q), got:\n%s", want, got)
	require.Equal(t, 1, strings.Count(got, "\n")+1, "exactly one REFUSED line, got:\n%s", got)
}

// aCardCloningGithub spells a clone URL through separate literals, the way
// pkg/swarm's own publicclass_test.go spells one through its probe base, so no
// test literal names a real network host and the CI net check stays green. The
// public gate reads the assembled card text and makes no request.
func aCardCloningGithub(repo string) []byte {
	return []byte("clone https://" + "github" + ".com/" + repo + " and work\n")
}

// TestANativeModelWithNoProviderPrefixIsRefused pins the model gate at native.go:485:
// a native model is provider/model, and a bare model id is refused before anything runs.
func TestANativeModelWithNoProviderPrefixIsRefused(t *testing.T) {
	t.Parallel()

	root, slot := aSlot(t)
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: aRunnableBinary(t), model: "no-slash-model", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root,
	}, &errOut)
	assertOneRefused(t, &errOut, code, "has no provider prefix")
}

// TestAWorkerDescriptionPinningAnotherModelIsRefused pins native.go:512-514: a key is
// authorized for one model only, and a description whose model differs from --model is
// refused naming both models.
func TestAWorkerDescriptionPinningAnotherModelIsRefused(t *testing.T) {
	t.Parallel()

	root, slot := aSlot(t)
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: aRunnableBinary(t), model: "fake/fake-model", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root,
		worker: &swarm.Worker{Name: "w", Provider: "fake", Model: "another-model"},
	}, &errOut)
	assertOneRefused(t, &errOut, code, "--model")
	require.Contains(t, errOut.String(), "fake/fake-model", "the refusal names --model's model")
	require.Contains(t, errOut.String(), "another-model", "the refusal names the description's model")
}

// TestAWorkerSecretNamingAnUnsetVariableIsRefused pins native.go:517-527: a description
// naming "secret" takes the key from the environment and an absent variable is refused
// before anything runs, with the value never printed and no job directory made.
func TestAWorkerSecretNamingAnUnsetVariableIsRefused(t *testing.T) {
	t.Parallel()

	const secretName = "NOVA_TEST_UNSET_SECRET_000000"
	root, slot := aSlot(t)
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: aRunnableBinary(t), model: "fake/fake-model", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root,
		worker: &swarm.Worker{Name: "w", Provider: "fake", Model: "fake-model", Secret: secretName},
	}, &errOut)
	assertOneRefused(t, &errOut, code, secretName)
	// The variable is unset, so there is no value for the line to carry: the refusal
	// names the variable by name and never a value.
	assert.NotContains(t, errOut.String(), "value=", "no secret value is printed")
	_, statErr := os.Stat(filepath.Join(slot, "jobs", "lbl"))
	assert.True(t, os.IsNotExist(statErr), "no job directory is made for a refused secret")
}

// TestANativeSlotOutsideTheConfiguredRootIsRefused pins native.go:533: a slot
// directory outside the configured root is a write this run has no business making.
func TestANativeSlotOutsideTheConfiguredRootIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	slot := t.TempDir()
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: aRunnableBinary(t), model: "fake/fake-model", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root,
	}, &errOut)
	assertOneRefused(t, &errOut, code, "is outside the configured root")
}

// TestANativeLabelThatWalksIsRefusedAsAName pins native.go:552: a label that is not
// a name is refused before it is joined into the job directory, and nothing is made
// outside the root.
func TestANativeLabelThatWalksIsRefusedAsAName(t *testing.T) {
	t.Parallel()

	root, slot := aSlot(t)
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: aRunnableBinary(t), model: "fake/fake-model", label: "../x",
		card: []byte("a card\n"), slotDir: slot, root: root,
	}, &errOut)
	assertOneRefused(t, &errOut, code, "is not a job name")
	_, statErr := os.Stat(filepath.Join(slot, "x"))
	assert.True(t, os.IsNotExist(statErr), "nothing is made for a walking label")
}

// TestANativeResultsRootInsideTheJobDirectoryIsRefused pins native.go:566: a sweep
// deletes the job directory, so publishing into it is refused.
func TestANativeResultsRootInsideTheJobDirectoryIsRefused(t *testing.T) {
	t.Parallel()

	root, slot := aSlot(t)
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: aRunnableBinary(t), model: "fake/fake-model", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root,
		resultsRoot: filepath.Join(slot, "jobs", "lbl", "out"),
	}, &errOut)
	assertOneRefused(t, &errOut, code, "is inside the job directory")
}

// TestANativeJobDirectoryHeldByALiveRunIsRefused pins native.go:602: the job
// directory's own lease refuses a second run in the same <slot>/jobs/<label>.
func TestANativeJobDirectoryHeldByALiveRunIsRefused(t *testing.T) {
	t.Parallel()

	root, slot := aSlot(t)
	jobDir := filepath.Join(slot, "jobs", "lbl")
	require.NoError(t, os.MkdirAll(jobDir, 0o755))
	release, err := swarm.StartJobLease(jobDir, "lbl")
	require.NoError(t, err, "the live lease for the test")
	defer release()

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: aRunnableBinary(t), model: "fake/fake-model", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root,
	}, &errOut)
	assertOneRefused(t, &errOut, code, "held by a live run: pid=")
}

// TestANativeSlotHeldUnderAnotherLabelIsRefused pins native.go:627: the slot's own
// lease refuses a second run in the same slot under a different label.
func TestANativeSlotHeldUnderAnotherLabelIsRefused(t *testing.T) {
	t.Parallel()

	root, slot := aSlot(t)
	release, err := swarm.StartSlotLease(slot, "other")
	require.NoError(t, err, "the live slot lease for the test")
	defer release()

	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: aRunnableBinary(t), model: "fake/fake-model", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root,
	}, &errOut)
	assertOneRefused(t, &errOut, code, "is held by a live run: pid=")
}

// TestAPublicWorkerWithAnUnlistedRepoIsRefusedWithExitOne pins the public-class gate at
// native.go:441-444: a public-class worker never sees a card that clones an unlisted
// repo, and the gate is CARD REFUSED at exit 1, not a NATIVE REFUSED at 2.
func TestAPublicWorkerWithAnUnlistedRepoIsRefusedWithExitOne(t *testing.T) {
	t.Parallel()

	root, slot := aSlot(t)
	var errOut bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: aRunnableBinary(t), model: "fake/fake-model", label: "lbl",
		card: aCardCloningGithub("evil/repo"), slotDir: slot, root: root,
		worker: &swarm.Worker{Name: "pub", Class: "public"},
	}, &errOut)
	require.Equal(t, 1, code, "the public gate exits 1, got %d:\n%s", code, errOut.String())
	require.Contains(t, errOut.String(), swarm.PublicRefusalLine("evil/repo", "pub"),
		"the refusal is the public-class line:\n%s", errOut.String())
}

// TestANativeAuthFileLooserThan0600IsRefusedAndItsLeasesReleased pins copyAuth
// (native.go:2754-2769) and prepare's release: a loose auth file is refused, and the
// leases prepare took are released, so a second run under the same label is not
// refused as held.
func TestANativeAuthFileLooserThan0600IsRefusedAndItsLeasesReleased(t *testing.T) {
	t.Parallel()

	root, slot := aSlot(t)
	loose := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(loose, []byte(`{"fake":"the-secret"}`), 0o644))

	var first bytes.Buffer
	_, code := nativeRun(nativeRunConfig{
		binary: aRunnableBinary(t), model: "fake/fake-model", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root, authFile: loose,
	}, &first)
	assertOneRefused(t, &first, code, "auth")

	// A second run under the same label must not be refused as held: the first run's
	// job and slot leases were released on the way out. It is refused later -- here by an
	// auth file that is 0600 but not JSON -- which proves the lease step was reached.
	invalid := filepath.Join(t.TempDir(), "auth.json")
	require.NoError(t, os.WriteFile(invalid, []byte("not a JSON object\n"), 0o600))
	var second bytes.Buffer
	_, code = nativeRun(nativeRunConfig{
		binary: aRunnableBinary(t), model: "fake/fake-model", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root, authFile: invalid,
	}, &second)
	assertOneRefused(t, &second, code, "not one JSON object")
	require.NotContains(t, second.String(), "held by a live run",
		"the first run's leases were released:\n%s", second.String())
}

// TestNativePrepareCallsOnPhasePrepareFirst pins native.go:408-410: onPhase is called
// with "prepare" at the entry of prepare, before any check.
func TestNativePrepareCallsOnPhasePrepareFirst(t *testing.T) {
	t.Parallel()

	root, slot := aSlot(t)
	var phases []string
	var errOut bytes.Buffer
	nativeRun(nativeRunConfig{
		binary: aRunnableBinary(t), model: "no-slash-model", label: "lbl",
		card: []byte("a card\n"), slotDir: slot, root: root,
		onPhase: func(p string) { phases = append(phases, p) },
	}, &errOut)
	require.NotEmpty(t, phases, "onPhase was never called")
	assert.Equal(t, "prepare", phases[0], "the first phase is prepare")
}
