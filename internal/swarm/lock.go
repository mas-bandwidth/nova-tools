package swarm

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// TWO KERNEL LOCKS, AND EACH DIES WITH ITS HOLDER (rule 17).
//
//	run.lock    excludes dispatchers only, and is held by `run` for its whole life.
//	slots.lock  protects each brief slot-state transition -- reserve, identify, orphan,
//	            release -- for the read, the compare and the rename of ONE slot file and
//	            nothing longer, and is NEVER held while waiting for a process, a handshake
//	            or a timeout.
//
// The second lock exists because the first cannot be both: a supervisor identifying itself
// takes slots.lock while its parent still holds run.lock for the whole run, and a lock the
// child had to take from its parent would deadlock the handshake against itself (Stella's
// final read, 2026-09-11).

// TakeLock takes the named lock inside the pool, waiting up to wait for it, and returns the
// release. The release is safe to call more than once.
func (p *Pool) TakeLock(name string, wait time.Duration) (func(), error) {
	return takeFileLock(p.Path(name), wait)
}

// takeFileLock is the body both kernel locks and the bench slot store share: an flock on a
// named file, polled until wait runs out, released by a function that is safe to call twice.
// It is a file lock rather than a file whose existence means "held" for rule 17's reason --
// the kernel drops it when the holder dies however it dies.
//
// The flock is not a queue. A waiter that lost slept for lockPoll, and the goroutine that
// had just released the file took it again before the sleeper woke. On a busy bench that
// is not a stuck holder -- eight takes in one process, each critical section slow enough
// that a sleeper never landed in the gap -- and the sleeper still waited out the whole
// bound (studio shard, TestConcurrentTakesKeepEveryTake: "waited 10s"). Same-process
// callers therefore take a turn first. The turn is a queue; the flock is still what keeps
// another process out, and still what the kernel drops when the holder dies.
func takeFileLock(path string, wait time.Duration) (func(), error) {
	turn := lockTurnFor(path)
	deadline := time.Now().Add(wait)
	if !turn.acquire(time.Until(deadline)) {
		return nil, fmt.Errorf("another nova-swarm holds %s and this run waited %s for it", path, wait)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		turn.release()
		return nil, fmt.Errorf("the lock at %s could not be opened: %w", path, err)
	}
	for {
		ok, lockErr := tryLockFile(f)
		if lockErr != nil {
			f.Close()
			turn.release()
			return nil, fmt.Errorf("the lock at %s could not be taken: %w", path, lockErr)
		}
		if ok {
			var once sync.Once
			return func() {
				once.Do(func() {
					unlockFile(f)
					f.Close()
					turn.release()
				})
			}, nil
		}
		if !time.Now().Before(deadline) {
			f.Close()
			turn.release()
			return nil, fmt.Errorf("another nova-swarm holds %s and this run waited %s for it", path, wait)
		}
		time.Sleep(lockPoll)
	}
}

// lockTurn is one in-process queue for a lock path. Sending takes the turn and
// receiving gives it back. A caller already waiting is ahead of the one that
// just released and asks again, which is the barging a poll cannot stop.
type lockTurn struct {
	ch chan struct{}
}

var lockTurns sync.Map

func lockTurnFor(path string) *lockTurn {
	fresh := &lockTurn{ch: make(chan struct{}, 1)}
	actual, _ := lockTurns.LoadOrStore(filepath.Clean(path), fresh)
	return actual.(*lockTurn)
}

// acquire waits up to wait for the turn. A non-positive wait tries once and
// does not queue: TakeLock(run.lock, 0) is a probe, not a waiter.
func (t *lockTurn) acquire(wait time.Duration) bool {
	if wait <= 0 {
		select {
		case t.ch <- struct{}{}:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case t.ch <- struct{}{}:
		return true
	case <-timer.C:
		return false
	}
}

func (t *lockTurn) release() { <-t.ch }

// SlotsWait is how long a slot transition waits for the lock. It is short because every
// holder of it does one read, one compare and one rename and then releases: a wait longer
// than this is a holder that is waiting on something, which this lock forbids.
const SlotsWait = 10 * time.Second

const lockPoll = 15 * time.Millisecond
