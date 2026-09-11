package wake

import (
	"fmt"
	"os"
	"strconv"
	"strings"
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
// THE SHAPE IS internal/bus's, deliberately, and this is a copy rather than a
// call: internal/bus's lock is exactly right -- an flock the kernel drops when
// the process dies, an O_EXCL sentinel where there is no flock -- but it takes
// a bus checkout and locks a file inside its git directory, and its tryLockFile
// and unlockFile are unexported. The clean fix is one exported LockFile in
// internal/bus that both callers use; that is a change to a shared package and
// is owed rather than taken. What is NOT copied is the waiting: a second
// watcher does not wait at all, because a watch runs for its whole --max and
// waiting out a twenty-minute holder is not a refusal anybody wants.
//
// The lock file holds the pid of the run that took it, so the refusal can name
// the holder rather than saying only that somebody has it (rule 4).

// LockState takes the exclusive lock on <state>.lock for the whole call. The
// second run over one path is refused, and the refusal names the holder's pid
// and the path.
func LockState(path string) (release func(), holder string, err error) {
	lock := LockName(path)
	f, err := os.OpenFile(lock, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, "", fmt.Errorf("the lock that keeps two watches off one state file could not be opened at %s: %w", lock, err)
	}
	ok, lockErr := tryLockFile(f)
	if lockErr != nil {
		f.Close()
		return nil, "", fmt.Errorf("the lock at %s could not be taken: %w", lock, lockErr)
	}
	if !ok {
		f.Close()
		return nil, readHolder(lock), nil
	}
	if err := f.Truncate(0); err == nil {
		f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
		f.Sync()
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		unlockFile(f)
		f.Close()
	}, "", nil
}

// readHolder reads the pid the holder wrote. A lock file with nothing readable
// in it answers "-": the refusal still names the path, which is the half a
// person acts on.
func readHolder(lock string) string {
	raw, err := os.ReadFile(lock)
	if err != nil {
		return "-"
	}
	pid := strings.TrimSpace(string(raw))
	if pid == "" {
		return "-"
	}
	if _, err := strconv.Atoi(pid); err != nil {
		return "-"
	}
	return pid
}
