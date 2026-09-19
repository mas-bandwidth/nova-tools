package swarm

import (
	"errors"
	"fmt"
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
	release, err := StartJobLease(job, "card-1")
	if err != nil {
		t.Fatalf("the take was refused: %v", err)
	}
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
	release, err := startJobLeaseEvery(job, "card-1", 10*time.Millisecond)
	if err != nil {
		t.Fatalf("the take was refused: %v", err)
	}
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
	release, err := StartJobLease(job, "card-1")
	if err != nil {
		t.Fatalf("the take was refused: %v", err)
	}
	release()
	release()
	if _, err := os.Lstat(filepath.Join(job, JobLeaseName)); !os.IsNotExist(err) {
		t.Fatalf("the lease outlived the run: %v", err)
	}
}

// ISSUE #1585, Stella's finding. Two `nova-swarm native` runs used one physical
// `<slot>/jobs/<label>`: the bench store gave each a different seat, but their job
// liveness files were one path. The first to exit removed `<job>/.lease` -- the file the
// reaper reads -- and the second, still running, lost its protection: its heartbeat only
// called Chtimes, which cannot restore a file that is gone.
//
// Three things are asked of the lease here, and each has its own test below:
//
//  1. the take is EXCLUSIVE, so a second live run in one job directory is refused before
//     it touches anything the first one owns;
//  2. the release is PID-FENCED, so a run only ever removes the lease it took;
//  3. the heartbeat REWRITES a lease that went missing, so a hand or an older binary
//     that removes one cannot leave a live job unprotected.
//
// None of these waits on a duration: the barriers are the files themselves.

// (1) A LIVE LEASE IS NOT TAKEN TWICE. The second take is refused, by name, and the file
// on disk is still the FIRST holder's, byte for byte.
func TestASecondTakeOnALiveJobLeaseIsRefused(t *testing.T) {
	job := t.TempDir()
	release, err := StartJobLease(job, "card-1")
	if err != nil {
		t.Fatalf("the first take was refused: %v", err)
	}
	defer release()
	first, err := os.ReadFile(filepath.Join(job, JobLeaseName))
	if err != nil {
		t.Fatal(err)
	}

	second, err := StartJobLease(job, "card-1")
	if err == nil {
		second()
		t.Fatal("a second run took a lease on a job directory a live run holds; both would then write one data home, one tmp and one log, and the first to end would remove the other's lease (#1585)")
	}
	var held *JobLeaseHeldError
	if !errors.As(err, &held) {
		t.Fatalf("the refusal is %v (%T), and a caller has to be able to name the holder: want a *JobLeaseHeldError", err, err)
	}
	if held.Holder.PID != os.Getpid() {
		t.Errorf("the refusal names pid %d; the holder is %d", held.Holder.PID, os.Getpid())
	}
	if !strings.Contains(err.Error(), "card-1") {
		t.Errorf("the refusal does not name the holder's card: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(job, JobLeaseName))
	if err != nil {
		t.Fatalf("the refused take removed the live lease: %v", err)
	}
	if string(after) != string(first) {
		t.Errorf("the refused take rewrote the live lease:\nbefore:\n%s\nafter:\n%s", first, after)
	}
}

// (2) A RELEASE REMOVES ITS OWN LEASE AND NO OTHER. This is the exact sequence of #1585
// with the ordering the issue reports: the first run's release lands while a second run
// holds the path. The barrier is the file: the second take happens on the same
// goroutine, after the path is free, so nothing here waits on a clock.
func TestAReleaseNeverRemovesAnotherRunsLease(t *testing.T) {
	job := t.TempDir()
	path := filepath.Join(job, JobLeaseName)

	releaseA, err := StartJobLease(job, "card-1")
	if err != nil {
		t.Fatalf("A could not take the lease: %v", err)
	}
	// The path becomes free the way it does on the bench -- by a hand, an older binary or
	// the first run's own unfenced release -- and B, a different run, takes it.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	releaseB, err := StartJobLease(job, "card-2")
	if err != nil {
		t.Fatalf("B could not take the free lease: %v", err)
	}
	defer releaseB()
	bBody, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// A ends. Its release must not touch B's lease: B is alive, and a job whose lease is
	// gone is a job the reaper will delete out from under it.
	releaseA()

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("A's release removed B's lease, and B is still running: %v (#1585)", err)
	}
	if string(after) != string(bBody) {
		t.Errorf("A's release rewrote B's lease:\nB wrote:\n%s\nnow:\n%s", bBody, after)
	}
	if !strings.Contains(string(after), "label=card-2\n") {
		t.Errorf("the lease on disk is not B's:\n%s", after)
	}
}

// (3) THE HEARTBEAT RESTORES A LEASE THAT WENT MISSING. Chtimes on a path that is not
// there does nothing at all, which is how the second run in #1585 lost its protection
// without ever being told. A held lease that disappears is written again, with this
// holder's own body.
func TestTheHeartbeatRewritesALeaseThatWentMissing(t *testing.T) {
	job := t.TempDir()
	path := filepath.Join(job, JobLeaseName)
	release, err := startJobLeaseEvery(job, "card-1", time.Millisecond)
	if err != nil {
		t.Fatalf("the take was refused: %v", err)
	}
	defer release()
	mine, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	// The heartbeat is a ticker, so this is a poll for its next tick and not a wait on a
	// chosen duration: the deadline only bounds a failure.
	deadline := time.Now().Add(10 * time.Second)
	for {
		raw, err := os.ReadFile(path)
		if err == nil && len(raw) > 0 {
			if string(raw) != string(mine) {
				t.Fatalf("the heartbeat rewrote the lease as somebody else:\nwant:\n%s\ngot:\n%s", mine, raw)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the lease is still missing: a held lease that is removed is never restored, and the reaper now reads this live job as finished work (#1585)")
		}
	}
}

// A lease whose launcher is gone is not a live lease: the next run takes the directory
// over rather than refusing forever. A crashed launcher must not cost a card ten minutes.
func TestADeadHoldersLeaseIsTakenOver(t *testing.T) {
	job := t.TempDir()
	path := filepath.Join(job, JobLeaseName)
	host, _ := os.Hostname()
	// A pid that is not alive. 0 is never a live process to signal, and Alive says so.
	body := fmt.Sprintf("pid=0\nhost=%s\nlabel=dead-card\nnonce=deadbeef\nstarted=%s\n",
		host, time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	release, err := StartJobLease(job, "card-1")
	if err != nil {
		t.Fatalf("a lease whose launcher is dead was treated as live: %v", err)
	}
	defer release()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "label=card-1\n") {
		t.Errorf("the dead holder's lease is still on disk:\n%s", raw)
	}
}
