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
