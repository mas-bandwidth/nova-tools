package pulse

// ONE WRITER PER QUEUE.
//
// bin/pulse-loop.sh's line 5 is a pidfile: a second loop on the same queue refuses and
// exits 1. Nothing in nova-pulse had that. `run`, `fill` and `manager` all write the same
// queue -- <queue>/NEXT read-modify-write (manager.go's nextNumber), the launched markers
// fill writes, the receipts and the ledger -- and two of them on one queue hand the same
// card number out twice and race the markers that hold a lane. That is the dogfood edge of
// 2026-09-18, gap 2.
//
// The lock is <queue>/.lock, created with O_EXCL, carrying the holder's pid, the kernel's
// start stamp for that pid, the verb and when it started. A second writer refuses at exit 2
// and NAMES the holder, because "the queue is busy" with no pid is a person guessing which
// of their own windows to kill.
//
// STALE IF DEAD, like nova-sandbox's owner marker. A lock whose holder is gone is not a
// lock: a SIGKILLed loop would otherwise stop the bench until somebody noticed the file.
// Both halves must hold for a lock to stand -- the pid is RUNNING, and the process wearing
// that number is the one that wrote the file -- because a pid is a small number the
// operating system hands out again. A lock file with no readable pid is an orphan and is
// taken, since the alternative is a queue nobody can ever write again.
//
// ONE WRITER IS ONE PROCESS. `loop` runs run, fill and manager in one tick of one process,
// and each of them takes the lock; a lock this process already holds is handed back as a
// handle that releases nothing, so the loop's own three verbs are not three writers.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// lockAlive and lockStart are the kernel probes this file judges a holder by. They are
// variables, as cmd/nova-sandbox's reaper does it, so a test drives BOTH halves -- a live
// holder and a dead one -- with no process to spawn and no pid to guess. A test that
// guessed a pid ("ours plus one") would pass or fail by what else the machine is running.
var (
	lockAlive = swarm.Alive
	lockStart = swarm.StartStamp
)

// QueueLockName is the lock file inside the queue directory. It begins with a dot and is
// not a card-<n>.md, so every glob the queue verbs run steps over it.
const QueueLockName = ".lock"

// LockHolder is what a lock file says about who holds it.
type LockHolder struct {
	PID   int
	Start string // the kernel's start stamp for that pid, or "-" where the machine gives none
	Verb  string
	At    string
}

// Line names the holder in one line, which is what a refused second writer is owed.
func (h LockHolder) Line() string {
	return fmt.Sprintf("pid=%d verb=%s since=%s", h.PID, oneline.Field(nonEmpty(h.Verb, "-")), oneline.Field(nonEmpty(h.At, "-")))
}

// QueueLock is a held queue lock. Release gives it back; a handle for a lock this process
// already held releases nothing.
type QueueLock struct {
	path string
	own  bool
}

// LockedError is a second writer's refusal: the lock, and who is holding it.
type LockedError struct {
	Path   string
	Holder LockHolder
}

func (e *LockedError) Error() string {
	return fmt.Sprintf("the queue is locked by another writer (%s); one writer per queue -- wait for it, or remove %s if that process is gone",
		e.Holder.Line(), oneline.Field(e.Path))
}

// LockQueue takes <queue>/.lock for the named verb. It returns a handle, or a *LockedError
// naming the live holder. A stale lock -- one whose holder is not running, or is a
// different process now wearing that pid -- is taken over, once, and never waited on.
func LockQueue(queue, verb string) (*QueueLock, error) {
	return lockQueueAt(queue, verb, time.Now().UTC(), os.Getpid())
}

// lockQueueAt is LockQueue with the clock and the pid handed in, so a test drives both
// halves -- a live holder and a dead one -- with no process to spawn.
func lockQueueAt(queue, verb string, now time.Time, self int) (*QueueLock, error) {
	if strings.TrimSpace(queue) == "" {
		return nil, fmt.Errorf("cannot lock a queue with no directory; refusing to guess (name the queue directory)")
	}
	if err := os.MkdirAll(queue, 0o755); err != nil {
		return nil, fmt.Errorf("cannot open the queue %s: %s (name a writable directory)", oneline.Field(queue), oneline.Err(err))
	}
	path := filepath.Join(queue, QueueLockName)
	// Two turns and no more: take it, or clear ONE stale holder and take it. A loop here
	// would be a wait, and a wait with no deadline is the thing we do not write.
	for turn := 0; turn < 2; turn++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			body := fmt.Sprintf("pid=%d\nstart=%s\nverb=%s\nat=%s\n",
				self, nonEmpty(strings.TrimSpace(lockStart(self)), "-"),
				oneline.Field(verb), now.Format(time.RFC3339))
			_, werr := f.WriteString(body)
			cerr := f.Close()
			if werr != nil || cerr != nil {
				_ = os.Remove(path)
				return nil, fmt.Errorf("the lock %s could not be written: %s", oneline.Field(path), oneline.Err(errors.Join(werr, cerr)))
			}
			return &QueueLock{path: path, own: true}, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("cannot take the lock %s: %s", oneline.Field(path), oneline.Err(err))
		}
		holder := readLockHolder(path)
		// Our own: `loop` runs three verbs that each ask for this lock, and one process is
		// one writer. The handle releases nothing, so the lock outlives the inner verb.
		if holder.PID == self {
			return &QueueLock{path: path}, nil
		}
		if lockHolderLive(holder) {
			return nil, &LockedError{Path: path, Holder: holder}
		}
		// Confirm it is still the same dead holder before removing, so a live writer that
		// created the file but has not yet written its pid keeps its lock.
		if again := readLockHolder(path); again != holder {
			continue
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("the stale lock %s could not be removed: %s", oneline.Field(path), oneline.Err(err))
		}
	}
	return nil, &LockedError{Path: path, Holder: readLockHolder(path)}
}

// Release gives the lock back. A handle for a lock this process already held releases
// nothing: the outer holder is still writing.
func (l *QueueLock) Release() {
	if l == nil || !l.own {
		return
	}
	l.own = false
	_ = os.Remove(l.path)
}

// readLockHolder reads the lock file's fields. Anything unreadable is the zero holder,
// whose pid is 0 and which lockHolderLive answers is not live.
func readLockHolder(path string) LockHolder {
	var h LockHolder
	raw, err := os.ReadFile(path)
	if err != nil {
		return h
	}
	for _, line := range strings.Split(string(raw), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "pid":
			h.PID, _ = strconv.Atoi(v)
		case "start":
			h.Start = v
		case "verb":
			h.Verb = v
		case "at":
			h.At = v
		}
	}
	return h
}

// lockHolderLive answers the one question a second writer may not get wrong. Both halves
// must hold: the pid is running, AND the process running under that number is the one that
// wrote the lock. Where the machine gives no start stamp the comparison is dash against
// dash and "alive" is the answer -- the process is real and only the evidence is missing.
func lockHolderLive(h LockHolder) bool {
	if h.PID <= 0 {
		return false
	}
	if !lockAlive(h.PID, h.Start) {
		return false
	}
	now := strings.TrimSpace(lockStart(h.PID))
	if now == "" || now == "-" || h.Start == "" || h.Start == "-" {
		return true
	}
	return now == h.Start
}
