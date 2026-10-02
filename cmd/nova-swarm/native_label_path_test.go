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
)

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

// A relative --harness names a file from where native was run, and the child
// starts in its job directory, so the path is made absolute when it is
// checked (USE defect 7: native -h's own example, --harness ./harness,
// passed the check and then failed to start, "no such file or directory").
func TestNativeRunsARelativeHarnessFromWhereItWasNamed(t *testing.T) {
	t.Parallel()

	wd, err := os.Getwd()
	require.NoError(t, err)
	rel, err := filepath.Rel(wd, nativeHarness(t))
	if err != nil {
		t.Skipf("no relative path from %s to the harness: %v", wd, err)
	}
	require.False(t, filepath.IsAbs(rel), "the harness path is relative: %s", rel)
	base := t.TempDir()
	root := filepath.Join(base, "swarm-root")
	slot := filepath.Join(root, "1")
	require.NoError(t, os.MkdirAll(slot, 0o755))
	write(t, filepath.Join(root, "identity.tsv"),
		"owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	var errOut bytes.Buffer
	res, code := nativeRun(nativeRunConfig{
		binary: rel, model: "fake/fake-model", label: "card-1",
		card:    []byte("a card\n"),
		slotDir: slot, root: root, deadline: time.Minute, noWall: true,
	}, &errOut)
	assert.NotContains(t, errOut.String(), "could not be started", "a relative harness did not start (exit %d):\n%s", code, errOut.String())
	assert.NotEqual(t, 2, code, "a relative harness:\n%s", errOut.String())
	assert.Equal(t, "ok", res.harness, "the harness answered:\n%s", errOut.String())
}
