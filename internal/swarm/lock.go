package swarm

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"
)

// takeFileLock uses a kernel lock on a named file, waits up to the supplied bound, and
// returns an idempotent release function. The kernel releases the lock when its holder exits.
//
// The kernel lock does not provide a fair queue, so same-process callers take turns through
// lockTurn. The file lock still excludes other processes while the critical section runs.
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
