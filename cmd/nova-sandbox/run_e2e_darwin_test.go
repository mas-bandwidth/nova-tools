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
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestARealRunLeavesNothingBehind(t *testing.T) {
	needDarwin(t)
	name := "e2e" + strconv.Itoa(os.Getpid())
	volume := "/Volumes/" + volumePrefix + name

	var out, errb bytes.Buffer
	code := run([]string{"run", "--name", name, "--size", "64m", "--timeout", "2m", "--",
		"/bin/sh", "-c", "echo hi > out; sleep 1"},
		nil, &out, &errb, []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin"})
	t.Logf("exit %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errb.String())

	if code != 0 {
		t.Fatalf("the real run exited %d, want 0", code)
	}
	if !strings.Contains(errb.String(), "SANDBOX DONE name="+name+" exit=0") {
		t.Errorf("the run printed no receipt for a clean exit")
	}
	// The volume is GONE: the mount point, and the machine's own list of volumes.
	if _, err := os.Lstat(volume); err == nil {
		t.Errorf("%s is still mounted after the run; the whole point is that nothing survives", volume)
	}
	if _, err := os.Lstat(filepath.Join(volume, "work", "out")); err == nil {
		t.Errorf("the file the command wrote is still readable; the volume it was on was not deleted")
	}
	listed, err := exec.Command("/usr/sbin/diskutil", "apfs", "list").Output()
	if err != nil {
		t.Fatalf("diskutil apfs list: %s", err)
	}
	if strings.Contains(string(listed), volumePrefix+name) {
		t.Errorf("a volume named %s%s is still in `diskutil apfs list` after the run", volumePrefix, name)
	}
}
