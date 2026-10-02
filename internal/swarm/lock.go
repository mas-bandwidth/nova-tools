package swarm

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"
)

// takeFileLock is the body of the bench slot store's lock: a kernel lock on a named file,
// waited for until wait runs out, released by a function that is safe to call twice. It is
// the kernel drops it when the holder dies.
// internal/filelock's (tla/FileLock.tla), the same flock on the same file the earlier
// binaries took, so an old and a new nova-swarm still exclude each other during an
// upgrade (lock_compat_unix_test.go); elsewhere it is still lock_other.go's.
//
// The kernel lock is not a queue. A waiter that lost slept for its poll, and the
// goroutine that had just released the file took it again before the sleeper woke. On a
// busy bench that is not a stuck holder -- eight takes in one process, each critical
// section slow enough that a sleeper never landed in the gap -- and the sleeper still
// waited out the whole bound.
// file lock is still what keeps another process out, and still what the kernel drops
// when the holder dies.
func takeFileLock(path string, wait time.Duration) (func(), error) {
	turn := lockTurnFor(path)
	deadline := time.Now().Add(wait)
	if !turn.acquire(time.Until(deadline)) {
		return nil, fmt.Errorf("another nova-swarm holds %s and this run waited %s for it", path, wait)
	}
	unlock, held, err := takeKernelLock(path, deadline)
	if err != nil || held {
		turn.release()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("another nova-swarm holds %s and this run waited %s for it", path, wait)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			unlock()
			turn.release()
		})
	}, nil
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
