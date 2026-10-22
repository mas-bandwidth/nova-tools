//go:build unix

package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcontract"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
)

// A work card keeps the slot's own tmp/<label>: only a reader's scratch moved inside its
// job, and a work card's must stay outside the job directory admission git-inits (issue
// #460). A frame-less unit test is the same shape as a work card.
func TestAWorkCardsScratchIsTheSlotsTmp(t *testing.T) {
	t.Parallel()
	slot := t.TempDir()
	read := nativeTmpDir(nativeRunConfig{slotDir: slot, label: "w.r1", frame: &cardcontract.Frame{Kind: "read"}}, filepath.Join(slot, "jobs", "w.r1"))
	assert.Equal(t, filepath.Join(slot, "jobs", "w.r1", "tmp"), read)
	work := nativeTmpDir(nativeRunConfig{slotDir: slot, label: "c.w1", frame: &cardcontract.Frame{Kind: "work"}}, filepath.Join(slot, "jobs", "c.w1"))
	assert.Equal(t, filepath.Join(slot, "tmp", "c.w1"), work)
	bare := nativeTmpDir(nativeRunConfig{slotDir: slot, label: "a-label"}, filepath.Join(slot, "jobs", "a-label"))
	assert.Equal(t, filepath.Join(slot, "tmp", "a-label"), bare)
}

// TestTheChildEnvNamesOneScratch: TMPDIR, TMP and TEMP all name the one directory the run
// hands the child, so a harness or library that reads any of the three by name writes
// inside the wall.
func TestTheChildEnvNamesOneScratch(t *testing.T) {
	t.Parallel()
	env := nativeChildEnvFrom([]string{"TMP=/caller", "TEMP=/caller", "TMPDIR=/caller"}, "data", "job", "scratch", "", "", "", "", nil)
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		got, ok := lookup(env, name)
		require.True(t, ok, "the child was handed no %s", name)
		assert.Equal(t, "scratch", got, "%s = %q, want the one scratch directory", name, got)
	}
}

// TestAReadWallRefusalEndsAsTheReadersEnvironment pins the detection a read's wall refusal
// lands on: a read whose run log carries the wall's WALL line (swarm.WallRefusedLine's
// `WALL REFUSED ...` or swarm.WallLine's `WALL task=...`) ends as the reader's own
// ENVIRONMENT, never the work's verdict (member.EndEnvironment). This is the mapping at
// cmd/nova-swarm/member.go (`if c.read && nativeWall.Match(b)`); without it a wall that
// refused a write would still leave every read green and the fix would be a no-op. The same
// WALL line on a work card is never an environment end.
func TestAReadWallRefusalEndsAsTheReadersEnvironment(t *testing.T) {
	t.Parallel()
	logPath := filepath.Join(t.TempDir(), "native.log")
	// the two real lines the wall prints on a refused write (docs/SPEC-SANDBOX.md):
	// WallRefusedLine's `WALL REFUSED <what> <path> task=<id> step=<n>` and WallLine's
	// `WALL task=<id> path=<path> step=<n>`.
	write(t, logPath, "WALL REFUSED write /tmp/x task=w.r1 step=1\nWALL task=w.r1 path=/tmp/x step=1\n")
	readChild := &nativeChild{read: true, logPath: logPath}
	require.Equal(t, member.EndEnvironment, readChild.Result().End, "a read the wall refused ends in the reader's own environment, never a missing verdict")
	workChild := &nativeChild{read: false, logPath: logPath}
	require.NotEqual(t, member.EndEnvironment, workChild.Result().End, "the same WALL line on a work card is the work's wall end, never an environment")
}
