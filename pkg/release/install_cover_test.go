// Unit coverage for ExecVersion, the production answer to what a binary at a
// path reports (install.go). The per-function coverage table showed it at zero.
// It is reached through the package's own behavior -- no seam wraps the child it
// runs -- so the two branches a unit test can reach without a subprocess are
// pinned here: the Stat refusal for a path that is not there, and the exec edge,
// reached with a context already done so os/exec refuses at Start before it
// forks. The success branch (runCommand returning a line) needs a live child and
// belongs to the functional tier; it is not reached here: no sleeps, no real
// time, no network, no subprocess, no Redis or Postgres.
package release

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// installCoverDoneContext is a context cancelled before the call. os/exec
// refuses an already-done context in Start, before os.StartProcess, so
// ExecVersion reaches its exec edge and returns the context's error with no
// child process.
func installCoverDoneContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// TestInstallCoverExecVersion pins both branches of ExecVersion a unit test can
// reach: a path that does not exist is refused before any child is considered,
// and an existing path reaches the exec edge, where a done context refuses it.
func TestInstallCoverExecVersion(t *testing.T) {
	t.Parallel()

	present := filepath.Join(t.TempDir(), "nova-bus")
	require.NoError(t, os.WriteFile(present, []byte("not an executable"), 0o755))
	missing := filepath.Join(t.TempDir(), "no-such-tool")

	rows := []struct {
		name string
		path string
		ctx  func(t *testing.T) context.Context
		want string
	}{
		{name: "a path that is not there is refused by name", path: missing, ctx: func(t *testing.T) context.Context {
			t.Helper()
			return context.Background()
		}, want: missing},
		{name: "an existing path reaches the exec edge and the done context refuses", path: present, ctx: installCoverDoneContext, want: "context canceled"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			line, err := ExecVersion(row.ctx(t), row.path)
			require.Error(t, err, "ExecVersion must return the refusal, not a version")
			assert.Empty(t, line, "a refusal carries no version line, got %q", line)
			assert.Contains(t, err.Error(), row.want, "the refusal names what refused it")
		})
	}
}
