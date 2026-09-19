package swarm

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A held lease names the launcher's own pid, so the reaper can ask the operating system
// whether the launcher is alive rather than asking a log how long it has been quiet.
func TestJobLeaseNamesTheLauncherPid(t *testing.T) {
	job := t.TempDir()
	release := StartJobLease(job, "card-1")
	defer release()

	raw, err := os.ReadFile(filepath.Join(job, JobLeaseName))
	if err != nil {
		t.Fatalf("the launcher took no lease: %v", err)
	}
	want := "pid=" + strconv.Itoa(os.Getpid())
	if !strings.Contains(string(raw), want+"\n") {
		t.Fatalf("the lease does not name this process:\n%s\nwant a line %q", raw, want)
	}
	if !strings.Contains(string(raw), "label=card-1\n") {
		t.Errorf("the lease does not name the card:\n%s", raw)
	}
}

// The mtime is the heartbeat: a card that says nothing for an hour still has a lease that
// was touched moments ago, which is the whole point of the file.
func TestJobLeaseHeartbeatsItsMtime(t *testing.T) {
	job := t.TempDir()
	path := filepath.Join(job, JobLeaseName)
	release := startJobLeaseEvery(job, "card-1", 10*time.Millisecond)
	defer release()

	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the launcher took no lease: %v", err)
	}
	first := st.ModTime()
	// Backdate it and let one tick land: the heartbeat must carry it forward again.
	old := first.Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		st, err = os.Stat(path)
		if err != nil {
			t.Fatalf("the lease went missing while it was held: %v", err)
		}
		if st.ModTime().After(old) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the lease mtime is still %s after 2s: the heartbeat does not beat", st.ModTime())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A released lease is gone: a finished job leaves nothing behind that claims to be alive,
// and releasing twice is not an error.
func TestJobLeaseReleaseRemovesTheFile(t *testing.T) {
	job := t.TempDir()
	release := StartJobLease(job, "card-1")
	release()
	release()
	if _, err := os.Lstat(filepath.Join(job, JobLeaseName)); !os.IsNotExist(err) {
		t.Fatalf("the lease outlived the run: %v", err)
	}
}
