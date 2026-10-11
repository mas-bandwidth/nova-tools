//go:build unix

package tokens

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// rig is the package's test harness (docs/STANDARD.md section 8: a rig is a
// helper struct owning the plumbing): one output directory of its own, the
// non-regular nodes the refusal tests stage at the lock path, the guarded
// take every refusal test repeats, and one checker per refusal family.
// pkg/testkit has no FIFO or flock mechanics, so the rig stays
// package-specific as its HARNESS.md asks.

type rig struct {
	t   *testing.T
	out string
}

// newRig builds a rig over a fresh output directory the test binary cleans up.
func newRig(t *testing.T) *rig {
	t.Helper()
	return &rig{t: t, out: t.TempDir()}
}

// fifo stands a FIFO at the lock path and returns the path.
func (r *rig) fifo() string {
	r.t.Helper()
	path := filepath.Join(r.out, LockName)
	require.NoError(r.t, syscall.Mkfifo(path, 0600))
	return path
}

// take tries the output directory's lock and releases it when taken.
func (r *rig) take() error {
	r.t.Helper()
	release, err := TakeFoldLock(r.out, 0)
	if release != nil {
		release()
	}
	return err
}

// requireRefused fails the test when the lock take succeeded.
func (r *rig) requireRefused(err error) {
	r.t.Helper()
	require.Error(r.t, err, "FIFO lock was accepted")
}

// requireFIFO fails the test when the path is gone or is no longer a FIFO.
func (r *rig) requireFIFO(path string) {
	r.t.Helper()
	info, statErr := os.Lstat(path)
	require.Falsef(r.t, statErr != nil || info.Mode()&os.ModeNamedPipe == 0, "FIFO changed: %v (%v)", info, statErr)
}
