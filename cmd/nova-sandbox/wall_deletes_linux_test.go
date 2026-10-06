//go:build linux

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fleet's reproduction of 2026-10-05, run through the real tool: a member's wall is
// the job dir as cwd, the data home as its own --write and HOME, a tmp and a cache, and
// opencode's database in $HOME/opencode unlinks its rollback journal on every commit.
// Inside the wall a file created in the data home is removed, and the SANDBOX OK line
// names every root deletes are allowed in on deletes=, the data home among them
// (docs/SPEC-SANDBOX.md, "deletes-in-every-write-root").
func TestTheWallsLineNamesTheRootsDeletesAreAllowedIn(t *testing.T) {
	t.Parallel()

	needLandlock(t)
	j := newJob(t)
	slot := j.base
	jobDir := filepath.Join(slot, "jobs", "card")
	data := filepath.Join(slot, "data")
	tmp := filepath.Join(slot, "tmp", "card")
	cache := filepath.Join(slot, "cache")
	for _, d := range []string{jobDir, data, tmp, cache} {
		require.NoError(t, os.MkdirAll(d, 0o755))
	}
	probe := filepath.Join(data, "opencode", "probe")
	script := `mkdir -p "$HOME/opencode" && touch "$HOME/opencode/probe" && rm "$HOME/opencode/probe"; echo rm=$?`
	env := []string{"HOME=" + data, "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	code, out, errOut := j.runTool(t, env,
		"--read", j.read, "--write", jobDir, "--write", data, "--write", tmp, "--write", cache,
		"--cwd", jobDir, "--", "/bin/sh", "-c", script)
	require.Equal(t, 0, code, "the walled run failed: stdout=%q stderr=%q", out, errOut)
	assert.Contains(t, out, "rm=0", "a file created in the data home could not be removed inside the wall: %q", errOut)
	assert.NoFileExists(t, probe)

	var line string
	for _, l := range strings.Split(errOut, "\n") {
		if strings.HasPrefix(l, "SANDBOX OK ") {
			line = l
		}
	}
	require.NotEmpty(t, line, "no SANDBOX OK line: %q", errOut)
	var deletes string
	for _, f := range strings.Fields(line) {
		if v, ok := strings.CutPrefix(f, "deletes="); ok {
			deletes = v
		}
	}
	assert.Equal(t, strings.Join([]string{jobDir, data, tmp, cache}, ","), deletes,
		"deletes= does not name every --write root, in order: %q", line)
}
