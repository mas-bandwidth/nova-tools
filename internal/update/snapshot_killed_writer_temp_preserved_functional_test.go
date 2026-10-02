//go:build functional

package update

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Two snapshots sharing one directory, written by independent reporter
// processes. A killed writer's temporary must not be swept by the other
// snapshot's writer (a leftover is preferable to deleting another writer's
// work), and the killed write retries cleanly on top of the leftover.
func TestStellaTwoSnapshotsOneDirectoryPreservesKilledWritersTemp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGKILL on a process group stages the death; the owed Windows validation is named in the pull request")
	}
	bin := filepath.Join(buildTreeBinaries(t), exeName("nova-update"))
	dir := t.TempDir()
	snapA := filepath.Join(dir, "a.json")
	snapB := filepath.Join(dir, "b.json")
	t.Setenv("NOVA_UPDATE_HELPER", "1")
	runReport := func(m, snap string) {
		t.Helper()
		if out, err := exec.Command(bin, "report", "--file", m, "--snapshot", snap).CombinedOutput(); err != nil {
			require.NoErrorf(t, err, "%v\n%s", err, out)
		}
	}
	// Two independent writers settle a baseline in the same directory.
	runReport(manifest(t, row("x", "tool", printer(t, "v1.0.0"), "npm:unused", "none")), snapA)
	runReport(manifest(t, row("y", "tool", printer(t, "v2.0.0"), "npm:unused", "none")), snapB)

	// Kill a writer for snapA mid-write, repeatedly until a live temporary
	// actually survives the kill, which is the evidence this test needs.
	killed := 0
	surviving := false
	for attempt := 0; attempt < 12 && !surviving; attempt++ {
		p := manifest(t, row("x", "tool", printer(t, fmt.Sprintf("v3.%d.0", attempt)), "npm:unused", "none"))
		c := exec.Command(bin, "report", "--file", p, "--snapshot", snapA)
		setGroup(c)
		if err := c.Start(); err != nil {
			require.NoError(t, err, err)
		}
		if err := assignGroup(c); err != nil {
			require.NoError(t, err, err)
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
	runReport(manifest(t, row("y", "tool", printer(t, "v2.1.0"), "npm:unused", "none")), snapB)
	if temps, _ := filepath.Glob(filepath.Join(dir, snapshotTempPrefix+"*")); len(temps) == 0 {
		require.NotEqualValuesf(t, 0, len(temps), "snapB's writer swept snapA's killed temporary")
	}
	if st, err := readSnapshot(snapB); err != nil || len(st.Observed) != 1 {
		require.Failf(t, "", "snapB is not intact after its own write: %v", err)
	}

	// The killed retry: a fresh writer for snapA finishes cleanly on top of the
	// leftover temporary.
	runReport(manifest(t, row("x", "tool", printer(t, "v1.0.0"), "npm:unused", "none")), snapA)
	if st, err := readSnapshot(snapA); err != nil || len(st.Observed) != 1 {
		require.Failf(t, "", "the retry could not use the surviving snapshot: %v", err)
	}
}
