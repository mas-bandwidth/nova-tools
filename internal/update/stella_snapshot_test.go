package update

// Stella's deciding-delta witnesses for #141, added first and run red before
// the repairs that make them pass. They name the two properties the earlier
// implementation broke: one snapshot writer must never delete another
// snapshot's live temporary, and the writer's own data-map keys must survive
// its own reader.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestStellaSnapshotWriterPreservesOtherSnapshotsTemp(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")
	unlockA, err := lockSnapshot(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	defer unlockA()
	unlockB, err := lockSnapshot(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	defer unlockB()
	// B holds its own lock and has a live, open temporary exactly as writeSnapshot creates it.
	f, err := os.CreateTemp(dir, snapshotTempPrefix+"*")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err = f.WriteString("synthetic B bytes"); err != nil {
		t.Fatal(err)
	}
	if err = writeSnapshot(a, emptySnapshot()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(f.Name())
	if err != nil || string(got) != "synthetic B bytes" {
		t.Fatalf("A removed/changed B's live temporary: %q %v", got, err)
	}
}

func TestStellaSnapshotRoundTripKeepsDistinctMapKeys(t *testing.T) {
	for _, names := range [][]string{{"Tool", "tool"}, {"outil-é"}} {
		s := emptySnapshot()
		for _, name := range names {
			s.Observed[name] = observed{Raw: "1.2.3", Status: "tool", At: "synthetic"}
		}
		p := filepath.Join(t.TempDir(), "s.json")
		if err := writeSnapshot(p, s); err != nil {
			t.Fatal(err)
		}
		got, err := readSnapshot(p)
		if err != nil {
			t.Errorf("writer's own %q map cannot be read: %v", names, err)
			continue
		}
		if len(got.Observed) != len(names) {
			t.Errorf("keys collapsed")
		}
	}
}

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
			t.Fatalf("%v\n%s", err, out)
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
	runReport(manifest(t, row("y", "tool", printer(t, "v2.1.0"), "npm:unused", "none")), snapB)
	if temps, _ := filepath.Glob(filepath.Join(dir, snapshotTempPrefix+"*")); len(temps) == 0 {
		t.Fatalf("snapB's writer swept snapA's killed temporary")
	}
	if st, err := readSnapshot(snapB); err != nil || len(st.Observed) != 1 {
		t.Fatalf("snapB is not intact after its own write: %v", err)
	}

	// The killed retry: a fresh writer for snapA finishes cleanly on top of the
	// leftover temporary.
	runReport(manifest(t, row("x", "tool", printer(t, "v1.0.0"), "npm:unused", "none")), snapA)
	if st, err := readSnapshot(snapA); err != nil || len(st.Observed) != 1 {
		t.Fatalf("the retry could not use the surviving snapshot: %v", err)
	}
}
