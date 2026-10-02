package update

// Stella's deciding-delta witnesses for #141, added first and run red before
// the repairs that make them pass. They name the two properties the earlier
// implementation broke: one snapshot writer must never delete another
// snapshot's live temporary, and the writer's own data-map keys must survive
// its own reader.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStellaSnapshotWriterPreservesOtherSnapshotsTemp(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	unlockA, err := lockSnapshot(context.Background(), a)
	if err != nil {
		require.NoError(t, err, err)
	}
	defer unlockA()
	unlockB, err := lockSnapshot(context.Background(), b)
	if err != nil {
		require.NoError(t, err, err)
	}
	defer unlockB()
	// B holds its own lock and has a live, open temporary exactly as writeSnapshot creates it.
	f, err := os.CreateTemp(dir, snapshotTempPrefix+"*")
	if err != nil {
		require.NoError(t, err, err)
	}
	defer f.Close()
	if _, err = f.WriteString("synthetic B bytes"); err != nil {
		require.NoError(t, err, err)
	}
	if err = writeSnapshot(a, emptySnapshot()); err != nil {
		require.NoError(t, err, err)
	}
	got, err := os.ReadFile(f.Name())
	if err != nil || string(got) != "synthetic B bytes" {
		require.Failf(t, "", "A removed/changed B's live temporary: %q %v", got, err)
	}
}

func TestStellaSnapshotRoundTripKeepsDistinctMapKeys(t *testing.T) {
	t.Parallel()

	for _, names := range [][]string{{"Tool", "tool"}, {"outil-é"}} {
		s := emptySnapshot()
		for _, name := range names {
			s.Observed[name] = observed{Raw: "1.2.3", Status: "tool", At: "synthetic"}
		}
		p := filepath.Join(t.TempDir(), "s.json")
		if err := writeSnapshot(p, s); err != nil {
			require.NoError(t, err, err)
		}
		got, err := readSnapshot(p)
		if err != nil {
			assert.NoErrorf(t, err, "writer's own %q map cannot be read: %v", names, err)
			continue
		}
		if len(got.Observed) != len(names) {
			assert.Lenf(t, got.Observed, len(names), "keys collapsed")
		}
	}
}
