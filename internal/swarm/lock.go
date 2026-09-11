package swarm

import (
	"fmt"
	"os"
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
	path := p.Path(name)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("the lock at %s could not be opened: %w", path, err)
	}
	deadline := time.Now().Add(wait)
	for {
		ok, lockErr := tryLockFile(f)
		if lockErr != nil {
			f.Close()
			return nil, fmt.Errorf("the lock at %s could not be taken: %w", path, lockErr)
		}
		if ok {
			released := false
			return func() {
				if released {
					return
				}
				released = true
				unlockFile(f)
				f.Close()
			}, nil
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, fmt.Errorf("another nova-swarm holds %s and this run waited %s for it", path, wait)
		}
		time.Sleep(lockPoll)
	}
}

// SlotsWait is how long a slot transition waits for the lock. It is short because every
// holder of it does one read, one compare and one rename and then releases: a wait longer
// than this is a holder that is waiting on something, which this lock forbids.
const SlotsWait = 10 * time.Second

const lockPoll = 15 * time.Millisecond
