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
	deadline := time.Now().Add(30 * time.Second)
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
// This is the stale-owner recovery the exclusion must never grow over.
func TestADeadHoldersLeaseIsTakenOver(t *testing.T) {
	job := t.TempDir()
	path := filepath.Join(job, JobLeaseName)
	host, _ := os.Hostname()
	// A REAPED CHILD's pid: a process that really ran and really ended, so the kernel's
	// answer is the test's fact and not an assumption about which numbers are never alive.
	body := fmt.Sprintf("pid=%d\nhost=%s\nlabel=dead-card\nnonce=deadbeef\nstarted=%s\n",
		deadPID(t), host, time.Now().UTC().Format(time.RFC3339))
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

// STELLA'S P1, WITNESS ONE (#1585, her HOLD on 6146897a). The first repair created the
// lease with O_EXCL and wrote the record afterwards. She held an owned descriptor open at
// exactly that boundary: a competitor read the empty file, called it pid 0, called pid 0
// dead, removed it and succeeded -- and the first creator's write then went to an inode
// with no name. Two launchers, both believing they owned the path.
//
// AN INCOMPLETE RECORD IS NOT EVIDENCE OF A DEAD OWNER. The barrier here is the descriptor
// itself, not a duration: the file is created and left empty for exactly as long as the
// assertions take.
func TestAnUnfinishedClaimIsNotReclaimedAsADeadOwner(t *testing.T) {
	job := t.TempDir()
	path := filepath.Join(job, JobLeaseName)

	// The create-before-write boundary, held open: a claim that exists and says nothing yet.
	claim, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer claim.Close()
	before, err := claim.Stat()
	if err != nil {
		t.Fatal(err)
	}

	release, err := StartJobLease(job, "competitor")
	if err == nil {
		release()
		t.Fatal("a competitor reclaimed an unfinished claim as a dead owner: an empty record is not a pid 0 that is not alive, it is an owner this run cannot read yet (#1585, Stella's P1)")
	}
	if _, ok := HeldJobLease(err); !ok {
		t.Errorf("the refusal is %v; an unfinished record is HELD by an unknown owner, and the caller has to be able to say so", err)
	}

	// AND THE PATH IS STILL THE FIRST CLAIM'S INODE. Nothing unlinked it, so the creator's
	// own write still lands on the file that bears the name.
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the competitor removed the unfinished claim: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("the path no longer names the first claim's file: the competitor unlinked it and the creator's write would go to an inode with no name (#1585, Stella's P1)")
	}
}

// The other half of rule 2: an unreadable record is HELD, not held forever. Once its
// heartbeat is older than the stale bound there is nobody left to protect, and the next run
// takes the directory. Without this the refusal would be a job directory nobody can ever
// use again.
func TestAnUnfinishedClaimIsTakenOverOnceItIsStale(t *testing.T) {
	job := t.TempDir()
	path := filepath.Join(job, JobLeaseName)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * JobLeaseStale)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	release, err := StartJobLease(job, "card-1")
	if err != nil {
		t.Fatalf("an unreadable record older than the stale bound still refused the run: %v", err)
	}
	defer release()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "label=card-1\n") {
		t.Errorf("the stale record is still on disk:\n%s", raw)
	}
}

// STELLA'S P1, WITNESS TWO (#1585). With `.lease` an owned directory, BOTH takes returned
// nil: the caller was told it had protection it did not have, and two launches proceeded.
// Failing to establish ownership is a refusal with a name, never a silent success.
func TestALeaseThatCannotBeEstablishedIsARefusalAndNotASilentSuccess(t *testing.T) {
	job := t.TempDir()
	// A path that is there and is not a record: this run cannot tell who holds the
	// directory, and under rule 3 it may not guess.
	if err := os.Mkdir(filepath.Join(job, JobLeaseName), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, who := range []string{"first", "second"} {
		release, err := StartJobLease(job, who)
		if err == nil {
			release()
			t.Fatalf("%s was told it holds a job directory it could not take: a take that establishes nothing must refuse, or two launchers proceed with no exclusion at all (#1585, Stella's P1)", who)
		}
		if !strings.Contains(err.Error(), JobLeaseName) {
			t.Errorf("%s: the refusal does not name the path it could not take: %v", who, err)
		}
	}
}

// An unwritable job directory is the same class: no lease can be written there, so no run
// can prove it owns the place, so no run starts. Skipped for a user the mode bits do not
// bind, which is the one way this could pass for the wrong reason.
func TestAJobDirectoryThatCannotHoldALeaseRefusesTheRun(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root is not bound by the directory's mode, so this proves nothing here")
	}
	job := t.TempDir()
	if err := os.Chmod(job, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(job, 0o755) })
	release, err := StartJobLease(job, "card-1")
	if err == nil {
		release()
		t.Fatal("a run was told it holds a job directory it cannot even write a lease into")
	}
}

// RULE 4, THE JOIN. `close(done)` does not join a tick that has ALREADY been selected, and
// that tick can publish the lease again after the release removed it -- leaving a finished
// job with a file that says a launcher is alive inside it, which is the whole thing the
// lease exists to prevent. Stella raised this at source level; this is the receipt.
//
// TWO BARRIERS AND NO CLOCK. The heartbeat is held at the START of its beat, and the
// release is held immediately AFTER its remove, so the two are ordered by the test and by
// nothing else -- there is no wait on a duration anywhere in here, and no scheduling
// outcome to be lucky about:
//
//	beat parked -> release runs -> beat released (it republishes) -> release released
//
// With the join, the release cannot reach its remove until the beat has finished and the
// heartbeat has stopped, so the remove is LAST and the lease is gone. Without it, the
// remove happens first against a path that is already empty, the republish lands after it,
// and the lease is still there when the run has ended.
func TestAReleaseWaitsForTheHeartbeatBeforeItRemovesTheLease(t *testing.T) {
	job := t.TempDir()
	path := filepath.Join(job, JobLeaseName)
	ticks := make(chan time.Time)
	atBeat, afterRemove := make(chan struct{}), make(chan struct{})

	release, err := startJobLeaseTicking(job, "card-1", ticks, func() {},
		jobLeaseHooks{atBeat: atBeat, afterRemove: afterRemove})
	if err != nil {
		t.Fatalf("the take was refused: %v", err)
	}

	// The lease goes missing, so the beat that is about to run REPAIRS it -- the write
	// that must not be allowed to land after the run has ended.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	ticks <- time.Now() // the heartbeat now sits at its barrier, inside this beat

	returned := make(chan struct{})
	go func() { release(); close(returned) }()

	<-atBeat      // the beat proceeds: it finds the lease gone and publishes it again
	<-afterRemove // the release has passed its remove
	<-returned

	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("the finished run left a lease behind: a beat already in flight wrote it back after the release, and the reaper now reads a finished job as live (#1585): %v", err)
	}
}

// Nothing is left beside the lease either: the temp file the publication links from is this
// run's own and is removed, so a job directory never accumulates them.
func TestTakingTheLeaseLeavesNoTemporaryFileBehind(t *testing.T) {
	job := t.TempDir()
	release, err := StartJobLease(job, "card-1")
	if err != nil {
		t.Fatalf("the take was refused: %v", err)
	}
	defer release()
	entries, err := os.ReadDir(job)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != JobLeaseName {
			t.Errorf("the job directory holds %q beside the lease", e.Name())
		}
	}
}

// staleRecord writes a lease nobody holds any more, in whichever of the two ways a record
// can be abandoned, and backdates the heartbeat where the rule needs it. It is the fixture
// the competing-takers tests start from.
func staleRecord(t *testing.T, job, how string) {
	t.Helper()
	path := filepath.Join(job, JobLeaseName)
	switch how {
	case "dead-pid":
		host, _ := os.Hostname()
		body := fmt.Sprintf("pid=%d\nhost=%s\nlabel=gone\nnonce=abandoned\nstarted=%s\n",
			deadPID(t), host, time.Now().UTC().Format(time.RFC3339))
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	case "unknown-and-stale":
		if err := os.WriteFile(path, []byte("half a li"), 0o644); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-2 * JobLeaseStale)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("no such abandoned record: %s", how)
	}
}

// COMPETING STALE TAKERS (Stella, #1585, after the atomic publication landed). Publication
// being atomic does not make RECLAMATION atomic. Two runs can both read one abandoned
// record and both judge it stale; the first clears it and links its own live lease into
// place; and the second -- deciding from a record that is no longer there, and removing a
// PATH rather than that record -- unlinks the winner's live lease and takes the directory
// from underneath it. That is the same class as the defect this whole PR is about, arriving
// one layer down.
//
// THE BARRIER IS THE RECLAMATION ITSELF, and there is no clock anywhere in here: B is held
// at the instant it has read and judged the abandoned record and is about to clear it, A
// runs to completion inside that window, and only then is B let go.
//
//	B reads and judges -> [held] -> A reclaims, publishes, is live -> B released -> B clears
//
// Both ways a record can be abandoned are driven, because the two recoveries are kept
// deliberately apart: a pid this kernel asked about and was answered for, and a record it
// cannot read at all which only age can retire.
func TestACompetingStaleTakerNeverRemovesTheWinnersLiveLease(t *testing.T) {
	for _, how := range []string{"dead-pid", "unknown-and-stale"} {
		t.Run(how, func(t *testing.T) {
			job := t.TempDir()
			path := filepath.Join(job, JobLeaseName)
			staleRecord(t, job, how)

			atReclaim, holdReclaim := make(chan struct{}), make(chan struct{})
			type taken struct {
				release func()
				err     error
			}
			bDone := make(chan taken, 1)
			go func() {
				release, err := startJobLeaseTicking(job, "B", make(chan time.Time), func() {},
					jobLeaseHooks{atReclaim: atReclaim, holdReclaim: holdReclaim})
				bDone <- taken{release, err}
			}()

			// B has read the abandoned record and judged it: it is about to clear it, and
			// it is held exactly there.
			<-atReclaim

			// A wins the path, from the same abandoned record, and is now the live holder.
			releaseA, err := StartJobLease(job, "A")
			if err != nil {
				t.Fatalf("A could not reclaim the abandoned record: %v", err)
			}
			defer releaseA()
			mine, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(mine), "label=A\n") {
				t.Fatalf("A did not win the path:\n%s", mine)
			}

			// B is let go, and clears the record it judged -- which is not there any more.
			close(holdReclaim)
			// Any FURTHER reclamation B makes runs free: the barrier was for the one
			// window this test is about, and a drained channel cannot deadlock a run that
			// goes round its loop again.
			go func() {
				for range atReclaim {
				}
			}()
			got := <-bDone
			if got.err == nil {
				t.Fatal("the losing reclaimer took the job directory from the run that won it: two launchers, one job directory (#1585, competing stale takers)")
			}
			if _, ok := HeldJobLease(got.err); !ok {
				t.Errorf("the loser's refusal is %v; the winner is a live holder and has to be named as one", got.err)
			}

			// AND A'S LEASE IS STILL ON DISK, BYTE FOR BYTE. This is the assertion the
			// whole test exists for: a reclaimer removed a live lease it had never read.
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("the losing reclaimer removed the winner's live lease: %v (#1585)", err)
			}
			if string(after) != string(mine) {
				t.Errorf("the losing reclaimer rewrote the winner's lease:\nA published:\n%s\nnow:\n%s", mine, after)
			}
		})
	}
}

// A reclamation that puts a record back leaves no tombstone behind either: the job
// directory holds the lease and nothing else when the dust settles.
func TestACompetingReclamationLeavesNoTombstoneBehind(t *testing.T) {
	job := t.TempDir()
	staleRecord(t, job, "dead-pid")

	atReclaim, holdReclaim := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := startJobLeaseTicking(job, "B", make(chan time.Time), func() {},
			jobLeaseHooks{atReclaim: atReclaim, holdReclaim: holdReclaim})
		done <- err
	}()
	<-atReclaim
	releaseA, err := StartJobLease(job, "A")
	if err != nil {
		t.Fatalf("A could not reclaim the abandoned record: %v", err)
	}
	defer releaseA()
	close(holdReclaim)
	go func() {
		for range atReclaim {
		}
	}()
	if err := <-done; err == nil {
		t.Fatal("the loser was told it holds the directory")
	}

	entries, err := os.ReadDir(job)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != JobLeaseName {
			t.Errorf("the job directory holds %q beside the lease", e.Name())
		}
	}
}

// AGE NEVER STEALS A LIVE CLAIM. The two recoveries are separate rules and must stay so: a
// record whose pid THIS kernel can see alive is held however old its heartbeat is, and only
// a record this kernel cannot be asked about is ever retired by elapsed time.
func TestAnOldHeartbeatNeverRetiresALivePid(t *testing.T) {
	job := t.TempDir()
	path := filepath.Join(job, JobLeaseName)
	release, err := StartJobLease(job, "card-1")
	if err != nil {
		t.Fatalf("the take was refused: %v", err)
	}
	defer release()
	// Far past the reaper's stale bound, with the launcher very much alive.
	old := time.Now().Add(-100 * JobLeaseStale)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	second, err := StartJobLease(job, "card-2")
	if err == nil {
		second()
		t.Fatal("an old heartbeat retired a claim whose pid this kernel can see alive: a card in one long model call is exactly that, and it is the whole reason the lease exists (#1499, #1585)")
	}
	if _, ok := HeldJobLease(err); !ok {
		t.Errorf("the refusal is %v; a live pid is a holder and has to be named as one", err)
	}
}
