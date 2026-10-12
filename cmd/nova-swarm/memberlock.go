package main

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/pkg/filelock"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// ONE MEMBER OF A NAME PER SLOTS DIRECTORY (v1.2.16).
//
// A member that starts ends every lane an earlier member of its name left running under its
// slots directory (nativeRunner.reapOrphans, pkg/swarm/lanereap.go "A LANE OUTLIVES NO
// MEMBER"). The selection is by name and slots alone, and nothing in a process's environment
// says whether the member that started it is dead: two daemons of one name by mistake, a
// hand-run `member --once --as <name>` beside the daemon, a card's own test reusing a fleet
// machine's name and root, and the newcomer would take the live member's running lanes for
// orphans and end them (the cold read of nova-tools#5647). So a member holds the kernel's lock
// on one file under its slots, `.member-<as>.lock`, for its whole run (pkg/filelock: flock on
// unix, LockFileEx on Windows). The kernel drops the lock with the process, however it dies,
// so a held lock is a live member of this name over these slots and never a dead one; a second
// member of the name is refused before it reaps anything, naming the holder. The descriptor is
// the member's alone: os.OpenFile opens it close-on-exec, and a lane's native is started with
// the stdio it is given and nothing else (cmd/nova-swarm's functional test reads native's open
// files to hold it). The lock is modelled as MemberLock in tla/StopCancels.tla, with the
// reversed witness MCStopCancelsBrokenNoLock.cfg (no lock: a live member's lane is reaped).

// memberLockPath is the lock file of the member named as over slots: a dotfile beside the
// launch directories, never a launch directory (the sweep reads only those).
func memberLockPath(slots, as string) string {
	return filepath.Join(slots, ".member-"+as+".lock")
}

// takeMemberLock takes the member lock of name as under slots without waiting
// (filelock.TryLock) and hands it to the caller, who holds it for the process's whole run. A
// lock another process holds is a member of this name running over these slots now, and the
// answer is an error naming it, for cmdMember to refuse with. Where this platform has no file
// lock (filelock.ErrNotSupported) there is no lock and the lock returned is nil, which unlocks
// as nothing; the orphan reap signals nothing on such a platform either
// (pkg/swarm/lanereap_nounix.go).
func takeMemberLock(slots, as string) (*filelock.FileLock, error) {
	path := memberLockPath(slots, as)
	l, err := filelock.TryLock(path, "nova-swarm member --as "+as)
	if err == nil {
		return l, nil
	}
	if errors.Is(err, filelock.ErrNotSupported) {
		return nil, nil
	}
	if he, ok := filelock.AsHeldError(err); ok {
		return nil, fmt.Errorf("a member named %s is running over %s already (its lock %s is held by %s): two members of one name over one slots directory would each end the other's running lanes as orphans, so this one does not start; stop the one that runs, or give this one its own --as or --root",
			oneline.Field(as), oneline.Field(slots), oneline.Field(path), he.Holder)
	}
	return nil, fmt.Errorf("the member lock %s could not be taken: %s", oneline.Field(path), oneline.Err(err))
}

// memberDirs is the member's three directories as the member uses them: root made absolute,
// slots and results defaulting under it (<root>/slots, <root>/results) and made absolute when
// given. A lane's mark is its launch directory under slots, and an orphan is told by its mark
// being directly under THIS member's slots (swarm.OrphanOf), so the slots path has to read the
// same however the member was started: a relative --root or --slots would otherwise form a
// mark that never matches once the working directory differs, or that matches another member's
// whose relative path reads the same from its own working directory.
func memberDirs(root, slots, results string) (string, string, string, error) {
	var err error
	if root, err = filepath.Abs(root); err != nil {
		return "", "", "", fmt.Errorf("--root %s: %s", oneline.Field(root), oneline.Err(err))
	}
	if slots == "" {
		slots = filepath.Join(root, "slots")
	} else if slots, err = filepath.Abs(slots); err != nil {
		return "", "", "", fmt.Errorf("--slots %s: %s", oneline.Field(slots), oneline.Err(err))
	}
	if results == "" {
		results = filepath.Join(root, "results")
	} else if results, err = filepath.Abs(results); err != nil {
		return "", "", "", fmt.Errorf("--results-root %s: %s", oneline.Field(results), oneline.Err(err))
	}
	return root, slots, results, nil
}
