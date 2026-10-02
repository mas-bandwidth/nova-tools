//go:build darwin && novadisk

// The ONE real end-to-end test of the run verb: a real APFS volume, a real seatbelt wall,
// a real child, and a real delete. It is behind the `novadisk` build tag on purpose —
// eight CI runners share the machine this repository is built on, and a suite that made
// and destroyed volumes on every `go test ./...` would be a hazard rather than a test.
//
// Run it by hand, on a Mac, when the disposable-volume body changes:
//
//	go test -tags novadisk -run TestARealRunLeavesNothingBehind ./cmd/nova-sandbox/
//
// It needs NO sudo: `diskutil apfs addVolume` and `diskutil apfs deleteVolume` on the boot
// container are the ordinary user's to run (measured on this Studio, macOS 26, 2026-09-18).
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestARealRunLeavesNothingBehind(t *testing.T) {
	t.Parallel()

	needDarwin(t)
	name := "e2e" + strconv.Itoa(os.Getpid())
	volume := "/Volumes/" + volumePrefix + name

	r := withEnv(run, []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"}).Do(t, "run", "--name", name, "--size", "64m", "--timeout", "2m", "--",
		"/bin/sh", "-c", "echo hi > out; sleep 1")
	t.Logf("exit %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)

	require.Equal(t, 0, r.Code, "the real run exited %d, want 0", r.Code)
	assert.Contains(t, r.Stderr, "SANDBOX DONE name="+name+" exit=0", "the run printed no receipt for a clean exit")
	// The volume is GONE: the mount point, and the machine's own list of volumes.
	_, err := os.Lstat(volume)
	assert.Error(t, err, "%s is still mounted after the run; the whole point is that nothing survives", volume)
	_, err = os.Lstat(filepath.Join(volume, "work", "out"))
	assert.Error(t, err, "the file the command wrote is still readable; the volume it was on was not deleted")
	listed, err := exec.Command("/usr/sbin/diskutil", "apfs", "list").Output()
	require.NoError(t, err, "diskutil apfs list: %s", err)
	assert.NotContains(t, string(listed), volumePrefix+name, "a volume named %s%s is still in `diskutil apfs list` after the run", volumePrefix, name)
}
