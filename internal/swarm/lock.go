package swarm

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/filelock"
)

// takeFileLock is the body of the bench slot store's lock: internal/filelock's kernel
// lock on a named file (tla/FileLock.tla), waited for until wait runs out, released by a
// function that is safe to call twice. It is a kernel lock rather than a file whose
// existence means "held" for rule 17's reason -- the kernel drops it when the holder dies
// however it dies.
//
// The kernel lock is not a queue. A waiter that lost slept for the poll, and the
// goroutine that had just released the file took it again before the sleeper woke. On a
// busy bench that is not a stuck holder -- eight takes in one process, each critical
// section slow enough that a sleeper never landed in the gap -- and the sleeper still
// waited out the whole bound (studio shard, TestConcurrentTakesKeepEveryTake: "waited
// 10s"). Same-process callers therefore take a turn first. The turn is a queue; the
// file lock is still what keeps another process out, and still what the kernel drops
// when the holder dies.
func takeFileLock(path string, wait time.Duration) (func(), error) {
	turn := lockTurnFor(path)
	deadline := time.Now().Add(wait)
	if !turn.acquire(time.Until(deadline)) {
		return nil, fmt.Errorf("another nova-swarm holds %s and this run waited %s for it", path, wait)
	}
	l, err := filelock.Lock(path, "nova-swarm", max(time.Until(deadline), 0))
	if err != nil {
		turn.release()
		if errors.Is(err, filelock.ErrHeld) || errors.Is(err, filelock.ErrBusy) {
			return nil, fmt.Errorf("another nova-swarm holds %s and this run waited %s for it", path, wait)
		}
		return nil, fmt.Errorf("the lock at %s could not be taken: %w", path, err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			// ignored: release has no caller to report to; the kernel lock goes with the descriptor either way
			_ = l.Unlock()
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
