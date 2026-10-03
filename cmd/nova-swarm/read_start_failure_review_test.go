package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

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
