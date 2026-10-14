package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// childStderr is a fake Exec's failed child: a real plutil writes its reason on
// stderr, and Env.Exec hands a check stdout and an error only, so the error
// carries the child's stderr with its non-zero exit (docs/SPEC-DOCTOR.md, "plists").
type childStderr struct {
	stderr string
	exit   error
}

func (e childStderr) Error() string { return e.stderr }
func (e childStderr) Unwrap() error { return e.exit }

// A plutil that refuses a plist writes the reason on stderr, so the check names the
// path and that reason, not an empty one (docs/SPEC-DOCTOR.md, "plists").
func TestPlistFailureKeepsTheChildsStderrReason(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "home", "Library", "LaunchAgents")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	path := filepath.Join(dir, "com.nova.friend-stopgap.plist")
	require.NoError(t, os.WriteFile(path, []byte("<plist>bad &</plist>"), 0o644))

	fake := friendEnv{goos: "darwin", fakeEnv: fakeEnv{env: map[string]string{"HOME": "home"}, root: root,
		exec: func(name string, _ ...string) (string, error) {
			if name == "plutil" {
				return "", childStderr{stderr: "Unexpected character\n", exit: errors.New("exit 1")}
			}
			return "", errors.New("unexpected " + name)
		}}}

	r := checkPlists(context.Background(), fake)
	assert.Equal(t, Fail, r.Status)
	assert.Contains(t, r.Evidence, "Unexpected character")
}
