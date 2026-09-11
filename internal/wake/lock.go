package wake

import (
	"errors"
	"fmt"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// One writer per --state.
//
// THE FAILURE THIS CLOSES. Two watches sharing one --state path each write the
// whole map, so the later write erases everything the earlier one learned --
// including the sighting memory, which silently resurrects standing errors, and
// including entry values, which silently re-report churn. The atomic rename
// does not close it: the rename makes each write whole, and two whole writes of
// different truths is the race.
//
// THE LOCK is internal/bus.LockFile: an flock the kernel drops when the process
// dies, an O_EXCL sentinel where there is no flock. What is NOT copied is the
// waiting: a second watcher does not wait at all, because a watch runs for its
// whole --max and waiting out a twenty-minute holder is not a refusal anybody
// wants.
//
// The lock file holds the pid of the run that took it, so the refusal can name
// the holder rather than saying only that somebody has it (rule 4).

// LockState takes the exclusive lock on <state>.lock for the whole call. The
// second run over one path is refused, and the refusal names the holder's pid
// and the path.
func LockState(path string) (release func(), holder string, err error) {
	lock := LockName(path)
	rel, err := bus.LockFile(lock, 0)
	if err != nil {
		if errors.Is(err, bus.ErrLockHeld) {
			return nil, bus.ReadLockHolder(lock), nil
		}
		return nil, "", fmt.Errorf("the lock that keeps two watches off one state file could not be opened at %s: %w", lock, err)
	}
	return rel, "", nil
}
