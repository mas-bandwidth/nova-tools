package darwincheck

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// system_cover_test.go reaches the five one-line answers OSSystem gives about
// the machine: GOOS, Environ, Sleep, LookPath and IsSocket. The wall itself is
// measured on a darwin machine (tools/sandboxcheck and TestOSSystem...); these
// tests pin only what the seam returns, so the functions the check calls are
// reached by the unit tier and not only by an operator's run.

// GOOS is the platform the check is built for: it gates the whole suite.
func TestSystemCoverGOOSReportsTheBuildPlatform(t *testing.T) {
	t.Parallel()
	assert.Equal(t, runtime.GOOS, OSSystem{}.GOOS())
}

// Environ is the process environment the check passes to a tool-generated
// profile (`withEnv`), so it must be the process's own, whole.
func TestSystemCoverEnvironIsTheProcessEnvironment(t *testing.T) {
	t.Parallel()
	assert.Equal(t, os.Environ(), OSSystem{}.Environ())
}

// Sleep is the wall the socket wait polls with; a zero duration proves the
// hand-off without spending wall time.
func TestSystemCoverSleepAcceptsAZeroDuration(t *testing.T) {
	t.Parallel()
	assert.NotPanics(t, func() { OSSystem{}.Sleep(0) })
}

// LookPath answers `command -v`: a program on PATH is found by absolute path,
// and a name on no PATH entry is refused with an empty path and false, which is
// what fails git_present.
func TestSystemCoverLookPathFindsAProgramAndRefusesAnAbsentOne(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		prog string
		ok   bool
	}{
		{"a program on PATH is found", "sh", true},
		{"a name on no PATH entry is refused", "nova-cover-absent-program", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := OSSystem{}.LookPath(tc.prog)
			assert.Equal(t, tc.ok, ok)
			if tc.ok {
				assert.True(t, filepath.IsAbs(got), "LookPath(%q) = %q, want an absolute path", tc.prog, got)
			} else {
				assert.Empty(t, got, "a refused name returned a path")
			}
		})
	}
}

// IsSocket is the check's question about a listener: only a unix-domain socket
// is a socket, so a regular file and an absent path are both refused and the
// socket wait cannot pass on the wrong file. The true branch needs a live
// listener bound at a path short enough for sun_path; the check's own listen
// gets that from a relative name under a long scratch directory, and a unit
// test has no seam to bind one without a subprocess or the denied shared temp
// directory, so the refusal branch is what this test reaches.
func TestSystemCoverIsSocketRefusesAPathThatIsNotASocket(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	file := filepath.Join(dir, "f")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))

	cases := []struct {
		name string
		path string
	}{
		{"a regular file is not a socket", file},
		{"an absent path is not a socket", filepath.Join(dir, "absent")},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.False(t, OSSystem{}.IsSocket(tc.path))
		})
	}
}
