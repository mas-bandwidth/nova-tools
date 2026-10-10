package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNovaWorkerIsTheToolAndNovaSwarmIsItsShim is the rename's contract. The
// tool's name is nova-worker; nova-swarm still works for one release through a
// shim that says so (docs/SPEC-WORKER.md). It builds both binaries from the
// tree rather than a list, so the shim cannot pass by being the tool.
func TestNovaWorkerIsTheToolAndNovaSwarmIsItsShim(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	worker, err := build(dir, "nova-worker", "./cmd/nova-worker")
	require.NoError(t, err, "building nova-worker")
	shim, err := build(dir, "nova-swarm", "./cmd/nova-swarm")
	require.NoError(t, err, "building nova-swarm")

	// nova-worker's help names nova-worker and never nova-swarm: the old name
	// is the shim's, not the tool's.
	workerOut, _, workerCode := runBinary(t, worker, "help")
	require.Equal(t, 0, workerCode, "`nova-worker help` must exit 0")
	assert.Contains(t, workerOut, "nova-worker", "`nova-worker help` does not name the tool:\n%s", workerOut)
	assert.NotContains(t, workerOut, "nova-swarm", "`nova-worker help` still names nova-swarm:\n%s", workerOut)

	// nova-swarm prints the deprecation line on stderr and the SAME stdout as
	// nova-worker help: the shim is a passthrough, not a second tool.
	shimOut, shimErr, shimCode := runBinary(t, shim, "help")
	require.Equal(t, 0, shimCode, "`nova-swarm help` must exit 0")
	assert.Contains(t, shimErr, "nova-swarm is now nova-worker; this name is removed in the next release",
		"`nova-swarm help` does not print the deprecation line on stderr:\n%s", shimErr)
	assert.Equal(t, workerOut, shimOut, "`nova-swarm help`'s stdout differs from nova-worker's")

	// A refused invocation's exit code passes through unchanged: an unknown
	// verb is exit 2, and the shim must not rewrite it.
	_, _, refused := runBinary(t, shim, "zz-no-such-verb")
	assert.Equal(t, 2, refused, "the shim did not pass an unknown verb's exit code through")
}

// runBinary runs a built tool with args and returns its streams and exit code.
func runBinary(t *testing.T, bin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr, "running %s %v", filepath.Base(bin), args)
		code = exitErr.ExitCode()
	}
	return out.String(), errb.String(), code
}
