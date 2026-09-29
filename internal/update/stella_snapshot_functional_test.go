//go:build functional

package update

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func snapshotPrinter(s string) string {
	return strings.Join([]string{os.Args[0], "-test.run=TestHelperProcess", "--", "print", base64.StdEncoding.EncodeToString([]byte(s))}, " ")
}

// Two snapshots sharing one directory, written by independent reporter
// processes. A killed writer's temporary must not be swept by the other
// snapshot's writer (a leftover is preferable to deleting another writer's
// work), and the killed write retries cleanly on top of the leftover.
func TestStellaTwoSnapshotsOneDirectoryPreservesKilledWritersTemp(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("SIGKILL on a process group stages the death; the owed Windows validation is named in the pull request")
	}
	bin := filepath.Join(buildTreeBinaries(t), exeName("nova-update"))
	dir := t.TempDir()
	snapA := filepath.Join(dir, "a.json")
	snapB := filepath.Join(dir, "b.json")
	runReport := func(m, snap string) {
		t.Helper()
		cmd := exec.Command(bin, "report", "--file", m, "--snapshot", snap)
		cmd.Env = append(os.Environ(), "NOVA_UPDATE_HELPER=1", "GORACE=atexit_sleep_ms=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v\n%s", err, out)
		}
	}
	// Two independent writers settle a baseline in the same directory.
	runReport(manifest(t, row("x", "tool", snapshotPrinter("v1.0.0"), "npm:unused", "none")), snapA)
	runReport(manifest(t, row("y", "tool", snapshotPrinter("v2.0.0"), "npm:unused", "none")), snapB)

	// Kill a writer for snapA mid-write, repeatedly until a live temporary
	// actually survives the kill, which is the evidence this test needs.
	killed := 0
	surviving := false
	for attempt := 0; attempt < 12 && !surviving; attempt++ {
		p := manifest(t, row("x", "tool", snapshotPrinter(fmt.Sprintf("v3.%d.0", attempt)), "npm:unused", "none"))
		c := exec.Command(bin, "report", "--file", p, "--snapshot", snapA)
		c.Env = append(os.Environ(), "NOVA_UPDATE_HELPER=1", "GORACE=atexit_sleep_ms=0")
		setGroup(c)
		if err := c.Start(); err != nil {
			t.Fatal(err)
		}
		if err := assignGroup(c); err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() { _ = c.Wait(); close(done) }()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			temps, _ := filepath.Glob(filepath.Join(dir, snapshotTempPrefix+"*"))
			if len(temps) > 0 {
				if killGroup(c) == nil {
					killed++
				}
				break
			}
			select {
			case <-done:
				deadline = time.Now()
			default:
			}
			time.Sleep(time.Millisecond)
		}
		<-done
		if temps, _ := filepath.Glob(filepath.Join(dir, snapshotTempPrefix+"*")); len(temps) > 0 {
			surviving = true
		}
	}
	if !surviving {
		t.Skipf("no killed writer left a surviving temporary in 12 attempts (killed %d); the no-sweep rule is UNPROVEN in this run", killed)
	}

	// A separate writer for snapB must not sweep the killed writer's temporary,
	// and must still write snapB correctly.
	runReport(manifest(t, row("y", "tool", snapshotPrinter("v2.1.0"), "npm:unused", "none")), snapB)
	if temps, _ := filepath.Glob(filepath.Join(dir, snapshotTempPrefix+"*")); len(temps) == 0 {
		t.Fatalf("snapB's writer swept snapA's killed temporary")
	}
	if st, err := readSnapshot(snapB); err != nil || len(st.Observed) != 1 {
		t.Fatalf("snapB is not intact after its own write: %v", err)
	}

	// The killed retry: a fresh writer for snapA finishes cleanly on top of the
	// leftover temporary.
	runReport(manifest(t, row("x", "tool", snapshotPrinter("v1.0.0"), "npm:unused", "none")), snapA)
	if st, err := readSnapshot(snapA); err != nil || len(st.Observed) != 1 {
		t.Fatalf("the retry could not use the surviving snapshot: %v", err)
	}
}
