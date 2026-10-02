package swarm

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A held lease names the launcher's own pid, so the reaper can ask the operating system
// whether the launcher is alive rather than asking a log how long it has been quiet.
func TestJobLeaseNamesTheLauncherPid(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	release, err := StartJobLease(job, "card-1")
	require.NoError(t, err, "the take was refused: %v", err)
	defer release()

	raw, err := os.ReadFile(filepath.Join(job, JobLeaseName))
	require.NoError(t, err, "the launcher took no lease: %v", err)
	want := "pid=" + strconv.Itoa(os.Getpid())
	require.Contains(t, string(raw), want+"\n", "the lease does not name this process:\n%s\nwant a line %q", raw, want)
	assert.Contains(t, string(raw), "label=card-1\n", "the lease does not name the card:\n%s", raw)
}

// The mtime is the heartbeat: a card that says nothing for an hour still has a lease that
// was touched moments ago, which is the whole point of the file.
func TestJobLeaseHeartbeatsItsMtime(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	job := t.TempDir()
	path := filepath.Join(job, JobLeaseName)
	release, err := startJobLeaseEvery(job, "card-1", 10*time.Millisecond)
	require.NoError(t, err, "the take was refused: %v", err)
	defer release()

	st, err := os.Stat(path)
	require.NoError(t, err, "the launcher took no lease: %v", err)
	first := st.ModTime()
	// Backdate it and let one tick land: the heartbeat must carry it forward again.
	old := first.Add(-time.Hour)
	require.NoError(t, os.Chtimes(path, old, old))
	deadline := time.Now().Add(2 * time.Second)
	for {
		st, err = os.Stat(path)
		require.NoError(t, err, "the lease went missing while it was held: %v", err)
		if st.ModTime().After(old) {
			return
		}
		require.False(t, time.Now().After(deadline), "the lease mtime is still %s after 2s: the heartbeat does not beat", st.ModTime())
		time.Sleep(5 * time.Millisecond)
	}
}

// A released lease is gone: a finished job leaves nothing behind that claims to be alive,
// and releasing twice is not an error.
func TestJobLeaseReleaseRemovesTheFile(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	release, err := StartJobLease(job, "card-1")
	require.NoError(t, err, "the take was refused: %v", err)
	release()
	release()
	_, err = os.Lstat(filepath.Join(job, JobLeaseName))
	require.True(t, os.IsNotExist(err), "the lease outlived the run: %v", err)
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
	t.Parallel()

	job := t.TempDir()
	release, err := StartJobLease(job, "card-1")
	require.NoError(t, err, "the first take was refused: %v", err)
	defer release()
	first, err := os.ReadFile(filepath.Join(job, JobLeaseName))
	require.NoError(t, err)

	second, err := StartJobLease(job, "card-1")
	if err == nil {
		second()
	}
	require.Error(t, err, "a second run took a lease on a job directory a live run holds; both would then write one data home, one tmp and one log, and the first to end would remove the other's lease (#1585)")
	var held *JobLeaseHeldError
	require.ErrorAs(t, err, &held, "the refusal is %v (%T), and a caller has to be able to name the holder: want a *JobLeaseHeldError", err, err)
	assert.Equal(t, os.Getpid(), held.Holder.PID, "the refusal names pid %d; the holder is %d", held.Holder.PID, os.Getpid())
	assert.Contains(t, err.Error(), "card-1", "the refusal does not name the holder's card: %v", err)
	after, err := os.ReadFile(filepath.Join(job, JobLeaseName))
	require.NoError(t, err, "the refused take removed the live lease: %v", err)
	assert.Equal(t, string(first), string(after), "the refused take rewrote the live lease:\nbefore:\n%s\nafter:\n%s", first, after)
}

// (2) A RELEASE REMOVES ITS OWN LEASE AND NO OTHER. This is the exact sequence of #1585
// with the ordering the issue reports: the first run's release lands while a second run
// holds the path. The barrier is the file: the second take happens on the same
// goroutine, after the path is free, so nothing here waits on a clock.
func TestAReleaseNeverRemovesAnotherRunsLease(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	path := filepath.Join(job, JobLeaseName)

	releaseA, err := StartJobLease(job, "card-1")
	require.NoError(t, err, "A could not take the lease: %v", err)
	// The path becomes free the way it does on the bench -- by a hand, an older binary or
	// the first run's own unfenced release -- and B, a different run, takes it.
	require.NoError(t, os.Remove(path))
	releaseB, err := StartJobLease(job, "card-2")
	require.NoError(t, err, "B could not take the free lease: %v", err)
	defer releaseB()
	bBody, err := os.ReadFile(path)
	require.NoError(t, err)

	// A ends. Its release must not touch B's lease: B is alive, and a job whose lease is
	// gone is a job the reaper will delete out from under it.
	releaseA()

	after, err := os.ReadFile(path)
	require.NoError(t, err, "A's release removed B's lease, and B is still running: %v (#1585)", err)
	assert.Equal(t, string(bBody), string(after), "A's release rewrote B's lease:\nB wrote:\n%s\nnow:\n%s", bBody, after)
	assert.Contains(t, string(after), "label=card-2\n", "the lease on disk is not B's:\n%s", after)
}

// (3) THE HEARTBEAT RESTORES A LEASE THAT WENT MISSING. Chtimes on a path that is not
// there does nothing at all, which is how the second run in #1585 lost its protection
// without ever being told. A held lease that disappears is written again, with this
// holder's own body.
func TestTheHeartbeatRewritesALeaseThatWentMissing(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	path := filepath.Join(job, JobLeaseName)
	release, err := startJobLeaseEvery(job, "card-1", time.Millisecond)
	require.NoError(t, err, "the take was refused: %v", err)
	defer release()
	mine, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))

	// The heartbeat is a ticker, so this is a poll for its next tick and not a wait on a
	// chosen duration: the deadline only bounds a failure.
	deadline := time.Now().Add(30 * time.Second)
	for {
		raw, err := os.ReadFile(path)
		if err == nil && len(raw) > 0 {
			require.Equal(t, string(mine), string(raw), "the heartbeat rewrote the lease as somebody else:\nwant:\n%s\ngot:\n%s", mine, raw)
			return
		}
		require.False(t, time.Now().After(deadline), "the lease is still missing: a held lease that is removed is never restored, and the reaper now reads this live job as finished work (#1585)")
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
	t.Parallel()

	job := t.TempDir()
	path := filepath.Join(job, JobLeaseName)

	// The create-before-write boundary, held open: a claim that exists and says nothing yet.
	claim, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	require.NoError(t, err)
	defer claim.Close()
	before, err := claim.Stat()
	require.NoError(t, err)

	release, err := StartJobLease(job, "competitor")
	if err == nil {
		release()
	}
	require.Error(t, err, "a competitor reclaimed an unfinished claim as a dead owner: an empty record is not a pid 0 that is not alive, it is an owner this run cannot read yet (#1585, Stella's P1)")
	_, ok := HeldJobLease(err)
	assert.True(t, ok, "the refusal is %v; an unfinished record is HELD by an unknown owner, and the caller has to be able to say so", err)

	// AND THE PATH IS STILL THE FIRST CLAIM'S INODE. Nothing unlinked it, so the creator's
	// own write still lands on the file that bears the name.
	after, err := os.Stat(path)
	require.NoError(t, err, "the competitor removed the unfinished claim: %v", err)
	require.True(t, os.SameFile(before, after), "the path no longer names the first claim's file: the competitor unlinked it and the creator's write would go to an inode with no name (#1585, Stella's P1)")
}

// The other half of rule 2: an unreadable record is HELD, not held forever. Once its
// heartbeat is older than the stale bound there is nobody left to protect, and the next run
// takes the directory. Without this the refusal would be a job directory nobody can ever
// use again.
func TestAnUnfinishedClaimIsTakenOverOnceItIsStale(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	path := filepath.Join(job, JobLeaseName)
	require.NoError(t, os.WriteFile(path, nil, 0o644))
	old := time.Now().Add(-2 * JobLeaseStale)
	require.NoError(t, os.Chtimes(path, old, old))
	release, err := StartJobLease(job, "card-1")
	require.NoError(t, err, "an unreadable record older than the stale bound still refused the run: %v", err)
	defer release()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(raw), "label=card-1\n", "the stale record is still on disk:\n%s", raw)
}

// STELLA'S P1, WITNESS TWO (#1585). With `.lease` an owned directory, BOTH takes returned
// nil: the caller was told it had protection it did not have, and two launches proceeded.
// Failing to establish ownership is a refusal with a name, never a silent success.
func TestALeaseThatCannotBeEstablishedIsARefusalAndNotASilentSuccess(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	// A path that is there and is not a record: this run cannot tell who holds the
	// directory, and under rule 3 it may not guess.
	require.NoError(t, os.Mkdir(filepath.Join(job, JobLeaseName), 0o755))
	for _, who := range []string{"first", "second"} {
		release, err := StartJobLease(job, who)
		if err == nil {
			release()
		}
		require.Error(t, err, "%s was told it holds a job directory it could not take: a take that establishes nothing must refuse, or two launchers proceed with no exclusion at all (#1585, Stella's P1)", who)
		assert.Contains(t, err.Error(), JobLeaseName, "%s: the refusal does not name the path it could not take: %v", who, err)
	}
}

// An unwritable job directory is the same class: no lease can be written there, so no run
// can prove it owns the place, so no run starts. Skipped for a user the mode bits do not
// bind, which is the one way this could pass for the wrong reason.
func TestAJobDirectoryThatCannotHoldALeaseRefusesTheRun(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root is not bound by the directory's mode, so this proves nothing here")
	}
	job := t.TempDir()
	require.NoError(t, os.Chmod(job, 0o555))
	t.Cleanup(func() { _ = os.Chmod(job, 0o755) })
	release, err := StartJobLease(job, "card-1")
	if err == nil {
		release()
	}
	require.Error(t, err, "a run was told it holds a job directory it cannot even write a lease into")
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
	t.Parallel()

	job := t.TempDir()
	path := filepath.Join(job, JobLeaseName)
	ticks := make(chan time.Time)
	atBeat, afterRemove := make(chan struct{}), make(chan struct{})

	release, err := startJobLeaseTicking(job, "card-1", ticks, func() {},
		jobLeaseHooks{atBeat: atBeat, afterRemove: afterRemove})
	require.NoError(t, err, "the take was refused: %v", err)

	// The lease goes missing, so the beat that is about to run REPAIRS it -- the write
	// that must not be allowed to land after the run has ended.
	require.NoError(t, os.Remove(path))
	ticks <- time.Now() // the heartbeat now sits at its barrier, inside this beat

	returned := make(chan struct{})
	go func() { release(); close(returned) }()

	<-atBeat      // the beat proceeds: it finds the lease gone and publishes it again
	<-afterRemove // the release has passed its remove
	<-returned

	_, err = os.Lstat(path)
	require.True(t, os.IsNotExist(err), "the finished run left a lease behind: a beat already in flight wrote it back after the release, and the reaper now reads a finished job as live (#1585): %v", err)
}

// Nothing is left beside the lease either: the temp file the publication links from is this
// run's own and is removed, so a job directory never accumulates them.
func TestTakingTheLeaseLeavesNoTemporaryFileBehind(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	release, err := StartJobLease(job, "card-1")
	require.NoError(t, err, "the take was refused: %v", err)
	defer release()
	entries, err := os.ReadDir(job)
	require.NoError(t, err)
	for _, e := range entries {
		assert.Equal(t, JobLeaseName, e.Name(), "the job directory holds %q beside the lease", e.Name())
	}
}

// AGE NEVER STEALS A LIVE CLAIM. The two recoveries are separate rules and must stay so: a
// record whose pid THIS kernel can see alive is held however old its heartbeat is, and only
// a record this kernel cannot be asked about is ever retired by elapsed time.
func TestAnOldHeartbeatNeverRetiresALivePid(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	path := filepath.Join(job, JobLeaseName)
	release, err := StartJobLease(job, "card-1")
	require.NoError(t, err, "the take was refused: %v", err)
	defer release()
	// Far past the reaper's stale bound, with the launcher very much alive.
	old := time.Now().Add(-100 * JobLeaseStale)
	require.NoError(t, os.Chtimes(path, old, old))
	second, err := StartJobLease(job, "card-2")
	if err == nil {
		second()
	}
	require.Error(t, err, "an old heartbeat retired a claim whose pid this kernel can see alive: a card in one long model call is exactly that, and it is the whole reason the lease exists (#1499, #1585)")
	_, ok := HeldJobLease(err)
	assert.True(t, ok, "the refusal is %v; a live pid is a holder and has to be named as one", err)
}

// Feature 87 hold (#2857): the renew decision must stop renewing once the beat file
// exists and has not moved; before it exists the tick renews (connecting grace).
func TestLeaseProviderBeatRenewsOnlyOnAdvance(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	beat := filepath.Join(job, ProviderBeatName)
	var last time.Time
	renew, last := providerBeatRenews(beat, last)
	require.True(t, renew, "no beat file yet: renew=%v last=%v, want renew with zero last", renew, last)
	require.True(t, last.IsZero(), "no beat file yet: renew=%v last=%v, want renew with zero last", renew, last)
	t0 := time.Now().Add(-time.Minute).Truncate(time.Second)
	require.NoError(t, touchProviderBeat(job, t0))
	renew, last = providerBeatRenews(beat, last)
	require.True(t, renew, "first beat: renew=%v last=%v, want renew at %v", renew, last, t0)
	require.True(t, last.Equal(t0), "first beat: renew=%v last=%v, want renew at %v", renew, last, t0)
	for i := 0; i < 3; i++ {
		renew, last = providerBeatRenews(beat, last)
		require.False(t, renew, "tick %d with an unmoved beat file renewed the lease", i)
	}
	t1 := t0.Add(10 * time.Second)
	require.NoError(t, touchProviderBeat(job, t1))
	renew, last = providerBeatRenews(beat, last)
	require.True(t, renew, "advanced beat: renew=%v last=%v, want renew at %v", renew, last, t1)
	require.True(t, last.Equal(t1), "advanced beat: renew=%v last=%v, want renew at %v", renew, last, t1)
	require.NoError(t, os.Remove(beat))
	renew, _ = providerBeatRenews(beat, last)
	require.False(t, renew, "beat file removed after being seen still renewed")
}

// touchProviderBeat must create the file when absent (the first sample may already
// show turns > 0, where a bare Chtimes silently did nothing).
func TestLeaseTouchProviderBeatCreatesAbsentFile(t *testing.T) {
	t.Parallel()

	job := t.TempDir()
	now := time.Now().Add(-time.Hour).Truncate(time.Second)
	require.NoError(t, touchProviderBeat(job, now))
	st, err := os.Stat(filepath.Join(job, ProviderBeatName))
	require.NoError(t, err, "beat file not created: %v", err)
	require.True(t, st.ModTime().Equal(now), "beat mtime %v, want %v", st.ModTime(), now)
}
