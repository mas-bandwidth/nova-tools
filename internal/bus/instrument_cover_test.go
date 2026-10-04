package bus

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

// CommitsWalkedIn reads the rev-list counter of one bus root. The walk itself lives on a
// git-led path, so the counter is moved through the package's own seam, countersFor, and
// read back through the exported getter: no subprocess, no store, no clock.

func TestInstrumentCoverCommitsWalkedInReadsTheBusesOwnCounter(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	assert.Equal(t, int64(0), CommitsWalkedIn(root), "an untouched bus reports no walked commits")

	countersFor(root).commitsWalked.Add(3)
	assert.Equal(t, int64(3), CommitsWalkedIn(root), "the getter reads the counter the walk moved")

	countersFor(root).commitsWalked.Add(2)
	assert.Equal(t, int64(5), CommitsWalkedIn(root), "each walk adds to the running total")
}

func TestInstrumentCoverCommitsWalkedInRefusesToMingleTwoBuses(t *testing.T) {
	t.Parallel()

	one := t.TempDir()
	two := t.TempDir()

	countersFor(one).commitsWalked.Add(4)
	assert.Equal(t, int64(4), CommitsWalkedIn(one))
	assert.Equal(t, int64(0), CommitsWalkedIn(two), "a count under one root is not read under another")
}

func TestInstrumentCoverCommitsWalkedInResolvesOneCounterPerRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	countersFor(root).commitsWalked.Add(7)

	cases := []struct {
		name string
		path string
	}{
		{"the same path", root},
		{"a trailing separator", root + string(filepath.Separator)},
		{"a dot segment", filepath.Join(root, ".")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, int64(7), CommitsWalkedIn(tc.path),
				"every spelling of the root lands on the one counter for the bus")
		})
	}
}
