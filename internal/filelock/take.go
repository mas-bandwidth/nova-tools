//go:build unix || windows

package filelock

import (
	"fmt"
	"os"
)

// maxAsks bounds how many times a taker that only askers kept out tries again
// before it answers busy.
const maxAsks = 3

// takeExclusive asks the kernel for the exclusive lock on f without waiting,
// and decides what a refusal means. A refusal alone is not "held": Probe holds
// the shared lock for an instant, and flock and LockFileEx refuse the exclusive
// lock while any shared lock is held, so a taker that lands in that instant
// would be told held with nobody holding. The design (tla/FileLock.tla: the
// Blocked action asks the shared lock, the Peek action lets go and tries again
// or answers busy; the HeldIsTrue invariant is what this keeps) has the refused
// taker ask for the shared lock too. Refused again, there is an exclusive
// holder: the answer is a *HeldError carrying the note it wrote. Granted, the
// only others in the way are askers (probes, or other refused takers asking):
// let go and try again, maxAsks times in all, and then the answer is ErrBusy,
// never ErrHeld.
func takeExclusive(f *os.File, path string) error {
	for ask := 1; ; ask++ {
		ok, err := tryLockFile(f)
		if err != nil {
			return fmt.Errorf("filelock %q: %w", path, err)
		}
		if ok {
			return nil
		}
		shared, err := trySharedLock(f)
		if err != nil {
			return fmt.Errorf("filelock %q: %w", path, err)
		}
		if !shared {
			return &HeldError{Path: path, Holder: readExistingStamp(f)}
		}
		unlockFile(f)
		if ask == maxAsks {
			return fmt.Errorf("filelock %q: busy after %d asks: %w", path, maxAsks, ErrBusy)
		}
	}
}
