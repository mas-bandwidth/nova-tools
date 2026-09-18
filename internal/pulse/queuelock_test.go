package pulse

// ONE WRITER PER QUEUE (queuelock.go). bin/pulse-loop.sh's pidfile refused a second loop;
// nova-pulse had nothing, so two shifts on one queue handed out the same card number and
// raced the markers that hold a lane (the manager dogfood, gap 2).
//
// No process is spawned here and no pid is guessed. The lock is a file with a pid in it, and
// the liveness probe is a seam (queuelock.go), so the two states that matter -- a live holder
// and a dead one -- are TAUGHT rather than found. A test that reached for "our pid plus one"
// would pass or fail by what else the machine happened to be running.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// deadPID and livePID are two process numbers the liveness probe is TAUGHT about, so no
// test here depends on what else the machine happens to be running.
const (
	deadPID = 4242
	livePID = 4243
)

// teachLiveness makes livePID the one live process besides this one. It is put back after
// the test, so nothing leaks into the next.
func teachLiveness(t *testing.T) {
	t.Helper()
	alive, start := lockAlive, lockStart
	t.Cleanup(func() { lockAlive, lockStart = alive, start })
	lockAlive = func(pid int, _ string) bool { return pid == livePID || pid == os.Getpid() }
	lockStart = func(int) string { return "-" }
}

// TestSecondWriterRefusesNamingTheHolder: one writer holds the queue and a second is
// refused, and the refusal NAMES the holder -- "the queue is busy" with no pid is a person
// guessing which of their own windows to kill.
func TestSecondWriterRefusesNamingTheHolder(t *testing.T) {
	teachLiveness(t)
	queue := t.TempDir()
	first, err := LockQueue(queue, "loop")
	if err != nil {
		t.Fatalf("the first writer could not take the lock: %v", err)
	}
	defer first.Release()

	// A second WRITER is a second process, so the pid it presents is not this one's.
	_, err = lockQueueAt(queue, "fill", time.Now().UTC(), livePID)
	var locked *LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("the second writer was allowed in: err=%v", err)
	}
	if locked.Holder.Verb != "loop" {
		t.Fatalf("the refusal names verb %q, want loop: %v", locked.Holder.Verb, err)
	}
	if locked.Holder.PID != os.Getpid() {
		t.Fatalf("the refusal names pid %d, want %d", locked.Holder.PID, os.Getpid())
	}
	for _, want := range []string{"pid=", "verb=loop", "since="} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not carry %q: %v", want, err)
		}
	}
}

// TestSecondFillOnALockedQueueExitsTwo: the refusal reaches the verb, not just the library.
// A second fill on a queue somebody is already writing exits 2 and launches nothing.
func TestSecondFillOnALockedQueueExitsTwo(t *testing.T) {
	teachLiveness(t)
	queue := t.TempDir()
	ready, launched := filepath.Join(queue, "ready"), filepath.Join(queue, "launched")
	writeCard(t, ready, "card-001.md", "RESULT: CARD\n")
	// Another process is holding it: a pid that is not ours, and alive.
	writeHolder(t, queue, livePID, "run")

	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched, Queue: queue,
		Machines: machinesFile(t, queue, []string{"bench-a"}, nil),
		Benches:  []string{"bench-a"}, Once: true,
		Stdout: &out, Stderr: &errb,
		Capacity: laneCap{"bench-a": 10}, Launcher: l,
	})
	if code != 2 {
		t.Fatalf("fill exit = %d, want 2 on a queue another writer holds", code)
	}
	if len(l.calls) != 0 {
		t.Fatalf("the refused fill launched %q", l.calls)
	}
	if !strings.Contains(errb.String(), "verb=run") {
		t.Fatalf("the refusal does not name the holder: %q", errb.String())
	}
}

// TestStaleLockIsTakenOver: a loop that was SIGKILLed leaves its lock behind, and a lock
// whose holder is gone is not a lock. The alternative is a bench that stops until somebody
// notices a file, which is the failure this rule may not have.
func TestStaleLockIsTakenOver(t *testing.T) {
	teachLiveness(t)
	queue := t.TempDir()
	writeHolder(t, queue, deadPID, "loop")

	lock, err := LockQueue(queue, "fill")
	if err != nil {
		t.Fatalf("a lock held by a dead process was not taken over: %v", err)
	}
	defer lock.Release()
	if got := readLockHolder(filepath.Join(queue, QueueLockName)); got.PID != os.Getpid() || got.Verb != "fill" {
		t.Fatalf("the lock still names %+v, want this process holding it as fill", got)
	}
}

// TestOneProcessIsOneWriter: `loop` runs run, fill and manager in one tick and each asks for
// the lock. One process is one writer, so the inner ask is handed a handle that releases
// nothing -- and the lock survives the inner verb returning.
func TestOneProcessIsOneWriter(t *testing.T) {
	teachLiveness(t)
	queue := t.TempDir()
	outer, err := LockQueue(queue, "loop")
	if err != nil {
		t.Fatal(err)
	}
	inner, err := LockQueue(queue, "fill")
	if err != nil {
		t.Fatalf("the loop's own inner verb was refused its queue: %v", err)
	}
	inner.Release()
	if _, err := os.Stat(filepath.Join(queue, QueueLockName)); err != nil {
		t.Fatalf("the inner verb's release dropped the loop's lock: %v", err)
	}
	outer.Release()
	if _, err := os.Stat(filepath.Join(queue, QueueLockName)); !os.IsNotExist(err) {
		t.Fatalf("the lock outlived its holder: %v", err)
	}
}

// writeHolder plants a lock file naming one pid, which is how both halves of the rule are
// driven with no process to spawn.
func writeHolder(t *testing.T, queue string, pid int, verb string) {
	t.Helper()
	body := "pid=" + itoa(pid) + "\nstart=-\nverb=" + verb + "\nat=2026-09-18T12:00:00Z\n"
	if err := os.WriteFile(filepath.Join(queue, QueueLockName), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// THE FOUR RACES STELLA'S COLD READ OF #1430 FOUND (defect 2). Each is red against the
// O_EXCL-then-write version and green against the link-and-nonce one.

// TestPausedPublisherIsNeverRobbed: `O_EXCL` creates an EMPTY file and the content arrives
// after. Two readers in that window both saw a lock with no pid, both called it an orphan,
// and one deleted a LIVE owner's lock. Creation and content are one step now, so there is no
// window to catch: the lock path never exists holding half a record.
func TestPausedPublisherIsNeverRobbed(t *testing.T) {
	teachLiveness(t)
	queue := t.TempDir()
	path := filepath.Join(queue, QueueLockName)

	// The window, made by hand: a lock file that exists and says nothing, exactly what the
	// old protocol left on disk between create and write.
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// Whatever a second writer does with it, it may not be taken while a publisher could be
	// mid-flight -- and this one IS empty, so the only safe answers are "locked" or "the
	// record is not ours to judge". What it may never do is hand the lock out AND leave the
	// original in place for its owner to release over the top.
	lock, err := lockQueueAt(queue, "fill", time.Now().UTC(), livePID)
	if err == nil {
		// It recovered the empty file. That is allowed only because the record is
		// unreadable AND the recovery went through .lock.take; the new record must be whole.
		holder := readLockHolder(path)
		if holder.Nonce == "" || holder.PID != livePID {
			t.Fatalf("the lock was handed out holding half a record: %+v", holder)
		}
		lock.Release()
		return
	}
	var locked *LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("an empty lock answered %v, want a lock or a refusal", err)
	}
}

// TestOldOwnerReleaseDoesNotDeleteItsReplacement: an owner that looked dead has its lock
// recovered, rightly. When it comes back and releases, it must not unlink the lock the
// recoverer is now holding -- which is a queue with two writers and nothing saying so.
func TestOldOwnerReleaseDoesNotDeleteItsReplacement(t *testing.T) {
	teachLiveness(t)
	queue := t.TempDir()
	path := filepath.Join(queue, QueueLockName)

	// The old owner takes it...
	old, err := LockQueue(queue, "loop")
	if err != nil {
		t.Fatal(err)
	}
	oldNonce := readLockHolder(path).Nonce
	// ...then looks dead, and a second writer recovers it.
	lockAlive = func(pid int, _ string) bool { return pid == livePID }
	replacement, err := lockQueueAt(queue, "fill", time.Now().UTC(), livePID)
	if err != nil {
		t.Fatalf("a lock whose holder is gone was not recovered: %v", err)
	}
	newNonce := readLockHolder(path).Nonce
	if newNonce == "" || newNonce == oldNonce {
		t.Fatalf("the recovery did not replace the record: %q", newNonce)
	}

	// The old owner comes back and gives its lock back, as every verb does on the way out.
	old.Release()
	if got := readLockHolder(path); got.Nonce != newNonce {
		t.Fatalf("the old owner's release deleted its replacement's lock: %+v", got)
	}
	replacement.Release()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the replacement could not release its own lock: %v", err)
	}
}

// TestCompetingTakeoverIsSerialized: two writers both find a dead holder. Only one may
// unlink and relink; the other is refused rather than deleting the winner's fresh lock.
func TestCompetingTakeoverIsSerialized(t *testing.T) {
	teachLiveness(t)
	queue := t.TempDir()
	writeHolder(t, queue, deadPID, "loop")

	// The second recoverer is already inside the recovery window: its .lock.take is there.
	take := filepath.Join(queue, TakeLockName)
	if err := os.WriteFile(take, []byte("pid=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := lockQueueAt(queue, "fill", time.Now().UTC(), livePID)
	var locked *LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("two writers recovered one stale lock at once: err=%v", err)
	}
	if !strings.Contains(err.Error(), "recovering a stale lock") {
		t.Fatalf("the refusal does not say a recovery is in flight: %v", err)
	}

	// A recoverer that died mid-way leaves .lock.take behind; it is cleared once, past the
	// stale bound, and the next turn takes the lock.
	later := time.Now().UTC().Add(2 * takeStale)
	if _, err := lockQueueAt(queue, "fill", later, livePID); err == nil {
		t.Fatalf("an abandoned .lock.take was cleared and the lock taken on the same turn")
	}
	lock, err := lockQueueAt(queue, "fill", later, livePID)
	if err != nil {
		t.Fatalf("an abandoned recovery blocked the queue forever: %v", err)
	}
	lock.Release()
}

// TestReentrancyIsByNonceNotByPid: a pid is a small number the operating system hands out
// again, so "the holder's pid equals mine" let this process walk into a lock it never took
// and release it. A lock is ours when its NONCE is one we are holding; a live pid we cannot
// account for is somebody else's lock and is refused, not entered.
func TestReentrancyIsByNonceNotByPid(t *testing.T) {
	teachLiveness(t)
	queue := t.TempDir()
	path := filepath.Join(queue, QueueLockName)
	// A lock wearing this process's pid that this process never took.
	body := "pid=" + itoa(os.Getpid()) + "\nstart=-\nnonce=notours\nverb=loop\nat=2026-09-18T12:00:00Z\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	lock, err := LockQueue(queue, "fill")
	var locked *LockedError
	if !errors.As(err, &locked) {
		t.Fatalf("this process walked into a lock it never took: lock=%+v err=%v", lock, err)
	}
	if got := readLockHolder(path); got.Nonce != "notours" {
		t.Fatalf("the refused writer touched the record: %+v", got)
	}
	// And releasing a handle it does not own leaves the record where it is.
	(&QueueLock{path: path, nonce: "invented", own: true}).Release()
	if got := readLockHolder(path); got.Nonce != "notours" {
		t.Fatalf("a handle with an invented identity deleted somebody's lock: %+v", got)
	}
}
