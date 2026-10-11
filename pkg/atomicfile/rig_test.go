package atomicfile

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// rig is the package's test harness (docs/STANDARD.md section 8: a rig is a
// helper struct owning the plumbing): a directory of its own, one write, and
// the checks every test here repeats. It also owns the subprocess seam the
// permission tests need, because a process-wide umask cannot be set inside a
// parallel test without one; pkg/testkit has no file-permission
// mechanics, so the rig stays package-specific as its HARNESS.md asks.

type rig struct {
	t   *testing.T
	dir string
}

// newRig builds a rig over a fresh temp dir the test binary cleans up.
func newRig(t *testing.T) *rig {
	t.Helper()
	return &rig{t: t, dir: t.TempDir()}
}

// rerun runs body when this process carries the named environment pair, and
// otherwise re-runs the calling test in a fresh subprocess that does: a
// process-wide umask belongs to a process of its own, never to a parallel
// test's. The subprocess must pass, and its output comes with the refusal.
func (r *rig) rerun(env string, body func()) {
	r.t.Helper()
	name, value, _ := strings.Cut(env, "=")
	if os.Getenv(name) == value {
		body()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^"+r.t.Name()+"$")
	cmd.Env = append(os.Environ(), env)
	out, err := cmd.CombinedOutput()
	require.NoError(r.t, err, "subprocess %s failed: %v\n%s", env, err, string(out))
}

// write writes body to name in the rig's dir with Write and returns the path.
func (r *rig) write(name, body string, perm os.FileMode, opts ...Option) string {
	r.t.Helper()
	path := filepath.Join(r.dir, name)
	require.NoError(r.t, Write(path, []byte(body), perm, opts...), "Write(%q) failed", path)
	return path
}

// permIs checks the permission bits the file at path carries.
func (r *rig) permIs(path string, want os.FileMode) {
	r.t.Helper()
	info, err := os.Stat(path)
	require.NoError(r.t, err, "Stat(%q) failed", path)
	require.Equal(r.t, want, info.Mode().Perm(), "perm of %q = %04o, want %04o", path, info.Mode().Perm(), want)
}
