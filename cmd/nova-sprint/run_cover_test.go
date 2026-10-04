package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunCoverStartProfileWritesWholeAndStopsTwice covers startProfile
// (cmd/nova-sprint/run.go:377, the finding's 0.0%): its main path begins a CPU
// profile to a file, its stop ends it and closes the file whole, a stop called
// twice is once through the sync.Once, and the process's one CPU profile makes
// a second start refuse ("already in use"), so that refusal is covered beside
// the main path in the same test, sequential by the one profile a process
// holds. The file is written in the test's own TempDir; nothing is dialled,
// no time is slept, and no subprocess runs.
func TestRunCoverStartProfileWritesWholeAndStopsTwice(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "cpu.out")
	stop, err := startProfile(path)
	require.NoError(t, err)
	require.NotNil(t, stop, "a begun profile names its stop")
	// the process holds one CPU profile: a second startProfile refuses until
	// the first's stop, so this refusal sits inside the hold of this test
	again, err := startProfile(filepath.Join(dir, "again.out"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already in use", err.Error())
	assert.Nil(t, again, "a refused start names no stop")
	require.NoError(t, stop(), "the stop ends the profile and closes the file")
	require.NoError(t, stop(), "a stop called twice is once through the sync.Once")
	assert.FileExists(t, path, "the stop wrote the profile whole before the close")
	st, err := os.Stat(path)
	require.NoError(t, err)
	assert.Positive(t, st.Size(), "the profile file holds its records")
}

// TestRunCoverStartProfileRefusesAPathItCannotCreate covers startProfile's
// refusal: os.Create's error is returned unchanged, no stop is named, and no
// profile is begun. The refusal touches no CPU profile, so it runs beside the
// main path's test. The cases are paths os.Create refuses on every platform.
func TestRunCoverStartProfileRefusesAPathItCannotCreate(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		path string
	}{
		{
			name: "a directory that does not exist",
			path: filepath.Join(t.TempDir(), "missing", "cpu.out"),
		},
		{
			name: "a path that is already a directory",
			path: t.TempDir(),
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			stop, err := startProfile(c.path)
			require.Error(t, err, "os.Create's error is the refusal")
			assert.Nil(t, stop, "a refused start names no stop")
		})
	}
}
