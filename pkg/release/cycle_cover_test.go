// Unit coverage for ExecAnsible.Play, the one function the per-function
// coverage table showed at zero. Play is the production edge to
// ansible-playbook: it runs one child through subproc.Context. Its injectable
// seam for callers is Ansible, and the fake that fills it (fakeAnsible) is
// already tested; this file reaches the concrete edge itself. The refusal is
// reached through a context already ended, so exec.Cmd.Start returns before it
// forks and no child starts. The success path needs a real ansible-playbook and
// a real inventory and belongs to the functional tier; it is not reached here:
// no sleeps, no real time, no network, no subprocess, no Redis or Postgres.
package release

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cycleCoverCancelled is a context cancelled before the call. exec.Cmd.Start
// returns its error before os.StartProcess, so Play reaches its production body
// and its refusal without a child process.
func cycleCoverCancelled(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// cycleCoverFakeProgram writes an executable into t.TempDir() and returns its
// path. It is the value of the package's own seam, ExecAnsible.Path; the
// cancelled context is what refuses the child, so the program never runs.
func cycleCoverFakeProgram(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-ansible-playbook")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755))
	return path
}

// TestCycleCoverPlayRefusesACancelledContext pins Play's body and its refusal:
// a play run under a context that has already ended returns the context's error
// with the empty output it captured, so a caller can tell a refused child from
// a play that ran and printed nothing.
func TestCycleCoverPlayRefusesACancelledContext(t *testing.T) {
	t.Parallel()

	got, err := ExecAnsible{Path: cycleCoverFakeProgram(t)}.
		Play(cycleCoverCancelled(t), []string{"-i", "/fleet/nova-inventory", "/src/fleet/tools.yml", "--check"})
	require.Error(t, err, "a cancelled context must refuse the play")
	assert.ErrorIs(t, err, context.Canceled, "the refusal is the context's own reason")
	assert.Empty(t, got, "a refused play carries no output")
}

// TestCycleCoverPlayRefusesAPastDeadline pins the same body under the other
// context ending: a deadline already behind it refuses the child with the
// deadline's error, so a build that ran out of time reads as a refusal and not
// as a play with no receipts.
func TestCycleCoverPlayRefusesAPastDeadline(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithDeadline(t.Context(), time.Unix(1, 0).UTC())
	t.Cleanup(cancel)
	got, err := ExecAnsible{Path: cycleCoverFakeProgram(t)}.
		Play(ctx, []string{"-i", "/fleet/nova-inventory", "/src/fleet/tools.yml"})
	require.Error(t, err, "a deadline already passed must refuse the play")
	assert.ErrorIs(t, err, context.DeadlineExceeded, "the refusal is the deadline's own reason")
	assert.Empty(t, got, "a refused play carries no output")
}
