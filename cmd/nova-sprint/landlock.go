package main

// landlock.go is the lander's exclusion (docs/SPEC-SPRINT.md section 7, one lander at a
// time). A land's clone is one checkout: its HEAD, its index and its batch branches are
// shared by whoever runs git in it. On 2026-10-04 a coordinator's land by hand and the
// server's lander ran in the same clone at once: at 3:28 PM a debt merge was committed
// while the other lander had land/fleetnames checked out, so the fleetnames card was
// gated on the debt card's tree; and a batch whose branch the other lander had reset read
// its tip from the shared HEAD, pushed a commit the base already held (a push of
// nothing) and reported fix-general-tla-tableorder-tla landed at 3:32:40 PM with its
// merge on no branch of origin. So:
//
//   - the server's lander holds the land root's lock (<land root>/lander.lock) for as
//     long as its loop runs (holdLanderLock), and a land by hand takes the same lock for
//     its whole run: a land by hand refuses at once, before it reads or runs anything,
//     while the server's lander (or another land) holds it, naming the holder;
//   - every land holds each clone it uses (<clone>/.git/nova-sprint-land.lock) from its
//     first use to its end: a clone another process holds refuses the batch at once,
//     nothing fetched, pushed or reported, the holder named. A --repo-dir clone two land
//     roots share is held the same way.
//
// The locks are internal/filelock's (flock, released by the kernel when the holder dies,
// the holder's pid, host, start and label written in the file for a refusal to name).

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/filelock"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// landerLockName is the land root's lock file: whoever holds it is the one lander.
const landerLockName = "lander.lock"

// cloneLockName is a clone's lock file, in its .git directory.
const cloneLockName = "nova-sprint-land.lock"

// takeLanderLock takes the land root's lock for label, without waiting: nil and the
// lock, or why it is held (heldWhy's words).
func (a *app) takeLanderLock(label string) (*filelock.FileLock, string) {
	root, err := a.landRoot()
	if err != nil {
		return nil, "" // no land root: no clone kept there to share; the clone locks still hold
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, "the land directory " + root + " could not be made: " + oneline.Err(err)
	}
	l, err := filelock.TryLock(filepath.Join(root, landerLockName), label)
	if err != nil {
		return nil, heldWhy("the lander of "+root, err)
	}
	return l, ""
}

// holdLanderLock is the server's lander taking the land root's lock for its loop: true
// when held (released by the kernel when the server ends); false with why when another
// holds it, the loop to try again next round.
func (a *app) holdLanderLock() (bool, string) {
	if a.landerLock != nil {
		return true, ""
	}
	l, why := a.takeLanderLock("nova-sprint run --land (the server's lander, pid " + strconv.Itoa(os.Getpid()) + ")")
	if why != "" {
		return false, why
	}
	a.landerLock = l
	return true, ""
}

// lockClone takes the clone's lock for the rest of this land; "" when held (now or
// already), else why not.
func (l *lander) lockClone(dir string) string {
	if l.locks == nil {
		l.locks = map[string]*filelock.FileLock{}
	}
	if _, ok := l.locks[dir]; ok {
		return ""
	}
	gitDir := filepath.Join(dir, ".git")
	if fi, err := os.Stat(gitDir); err != nil || !fi.IsDir() {
		gitDir = dir // a clone whose .git is a file (a worktree): the lock beside its files
	}
	fl, err := filelock.TryLock(filepath.Join(gitDir, cloneLockName), "nova-sprint land by "+dashed(l.c.actor)+" (pid "+strconv.Itoa(os.Getpid())+")")
	if err != nil {
		return heldWhy("the clone "+dir, err) + "; nothing was fetched, pushed or reported; run land again when it is done"
	}
	l.locks[dir] = fl
	return ""
}

// unlockClones lets go of every clone this land held.
func (l *lander) unlockClones() {
	for dir, fl := range l.locks {
		_ = fl.Unlock() // ignored: the kernel lets go of it when the process ends anyway
		delete(l.locks, dir)
	}
}

// heldWhy is a lock that could not be taken, in a refusal's words: what is held and by
// whom (the holder's pid, host, start and label), or why the lock failed.
func heldWhy(what string, err error) string {
	if h, ok := filelock.AsHeldError(err); ok {
		holder := h.Holder.String()
		if holder == "-" {
			holder = "a holder that wrote no stamp"
		}
		return what + " is in use by another lander: " + holder
	}
	if errors.Is(err, filelock.ErrHeld) {
		return what + " is in use by another lander"
	}
	return what + " could not be locked: " + strings.TrimSpace(oneline.Err(err))
}
