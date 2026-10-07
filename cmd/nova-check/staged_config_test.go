package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoCodeStagedDoesNotRunAProgramNamedInRepoConfig pins security#77 finding 1:
// `nova-check nocode --staged --dir <repo>` runs git against the checked repository.
// A repository configuring core.fsmonitor must not have its hook/monitor program executed
// by the advisory gate.
func TestNoCodeStagedDoesNotRunAProgramNamedInRepoConfig(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("skipping shell script probe on windows")
	}

	dir := stLab(t)
	probe := filepath.Join(t.TempDir(), "fsmonitor-probe.txt")
	script := filepath.Join(t.TempDir(), "fsmonitor.sh")
	scriptContent := fmt.Sprintf("#!/bin/sh\necho probe >> %s\nexit 0\n", probe)
	require.NoError(t, os.WriteFile(script, []byte(scriptContent), 0o755))

	stGit(t, dir, "config", "core.fsmonitor", script)
	mustWrite(t, dir, "bad.py", "print('evil')\n")
	stGit(t, dir, "-c", "core.fsmonitor=false", "add", "bad.py")
	_ = os.Remove(probe)

	exit, stdout, stderr := runCheck(t, "nocode", "--staged", "--dir", dir)
	require.EqualValues(t, 1, exit, "exit = %d, want 1; stdout:\n%s\nstderr:\n%s", exit, stdout, stderr)
	assert.Contains(t, stderr, `NOCODE FINDING subject=bad.py reason="code extension .py (floor-list)"`, "stderr = %q", stderr)
	assert.NoFileExists(t, probe, "probe file should not exist, but core.fsmonitor script was executed")
}
