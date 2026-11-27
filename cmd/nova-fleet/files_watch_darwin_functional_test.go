//go:build functional

package main

import (
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// roles/health/files/nova-files-watch, the Mac's open-files watch: silent under the threshold, one FILES line naming
// the top holders at or over it, and an unreadable count said.
func TestFunctionalFilesWatchDarwin(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("nova-files-watch reads macOS's kern.num_files: macOS only")
	}
	run := func(env ...string) string {
		cmd := exec.Command("bash", "roles/health/files/nova-files-watch")
		cmd.Env = append(os.Environ(), env...)
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		return string(out)
	}
	assert.Empty(t, run("FILES_WATCH_COUNT=49999", "FILES_WATCH_MAX=50000"), "under the threshold it writes nothing")
	out := run("FILES_WATCH_COUNT=50000", "FILES_WATCH_MAX=50000", "FILES_WATCH_TOP=3")
	assert.Regexp(t, regexp.MustCompile(`FILES \S+ open=50000 max=\d+ over=50000 top: \S.*:\d+=\d+`), out)
	assert.LessOrEqual(t, len(regexp.MustCompile(`:\d+=\d+`).FindAllString(out, -1)), 3, "at most FILES_WATCH_TOP holders")
	assert.Contains(t, run("FILES_WATCH_COUNT=nope"), `FILES-FAIL kern.num_files unreadable: "nope" (run: sysctl kern.num_files)`)
}
