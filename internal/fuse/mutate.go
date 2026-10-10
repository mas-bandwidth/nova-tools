package fuse

import (
	"os"
	"path/filepath"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/filelock"
)

// LockSuffix names the sibling lock file every box mutation holds: <box>.lock.
// The box itself cannot be the lock: a write publishes by rename (note 3), so a
// lock taken on the bytes a writer read is a lock on an inode the next writer
// replaces, and two writers then hold "the lock" at once.
const LockSuffix = ".lock"

// BoxLockPath is the lock file beside the box at path.
func BoxLockPath(path string) string { return path + LockSuffix }

// lockTimeout is how long a mutation waits for the box's lock before it refuses
// and writes nothing. A refusal names the holder, which pkg/filelock writes
// into the lock file.
const lockTimeout = 10 * time.Second

// MutateBox is the one way a box is changed: one cross-process read-modify-write.
// It takes the box's lock (pkg/filelock, whose design is tla/FileLock.tla),
// reads the box under it, hands the read to change and publishes what change
// returns, so no writer publishes a snapshot older than the state it overwrote
// (tla/FuseBox.tla: LostUpdate is the action this removes, and LockdownMonotone
// is the invariant that keeps -- a lockdown blown while a soft writer holds its
// read survives that writer's publish, because the soft writer reads it first).
//
// change answers three things: the box to publish, whether to publish it, and a
// reason not to go on. It runs exactly once, under the lock, and it is where a
// caller's own policy on a read that failed lives -- ReadBox's error is handed to
// it unchanged, so ErrNoBox and an unreadable box stay told apart (note 1).
// Publishing nothing is not an error: a caller whose state already stands writes
// nothing and keeps the record it found.
//
// A dry run (write false) neither takes the lock nor creates the lock file: it
// reads, lets change decide, and makes every check the write would make
// (planBox) without writing.
//
// init is not a mutation and does not come through here: CreateBox publishes
// exclusively (atomicfile.NoReplace), so it refuses a box another writer made
// instead of replacing a state it did not read.
func MutateBox(path string, write bool, change func(b Box, readErr error) (Box, bool, error)) error {
	if !write {
		b, readErr := ReadBox(path)
		_, publish, err := change(b, readErr)
		if err != nil {
			return err
		}
		if !publish {
			return nil
		}
		return planBox(path)
	}
	target := path
	if target != "" {
		target = filepath.Clean(target)
	}
	lock, lockErr := boxLock(target)
	if lockErr == nil {
		defer lock.Unlock() // ignored: the box is published by then, and the kernel drops the lock when the descriptor closes
	}
	// The read happens whatever became of the lock, so a caller's own policy on a
	// box it could not read answers as it does on a dry run; a lock that could not
	// be taken only stops the publish.
	b, readErr := ReadBox(path)
	next, publish, err := change(b, readErr)
	if err != nil {
		return err
	}
	if !publish {
		return nil
	}
	if lockErr != nil {
		return lockErr
	}
	return WriteBox(path, next)
}

// boxLock takes the box's cross-process lock, making the directory the lock file
// lives in first: a lockdown on a path whose directory is not there yet makes the
// box, and its lock file lives beside the box.
func boxLock(target string) (*filelock.FileLock, error) {
	if err := checkBoxAncestors(target); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, err
	}
	return filelock.Lock(BoxLockPath(target), "nova-fuse box", lockTimeout)
}
