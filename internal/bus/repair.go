package bus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

// staleIndexLockAge is how long an index.lock must have sat before wait may remove it.
//
// Sixty seconds is the bound that already kept a friend's checkout alive from outside
// this tool: long enough that a git which is still writing the index is not mistaken for
// one that was killed, short enough that the next tick is not another hour of the same
// refusal. A lock younger than that is left where it is, and nothing is said about a
// repair that did not happen.
const staleIndexLockAge = 60 * time.Second

// WaitRecovery is what RecoverWaitFastForward actually changed, and nothing it only
// considered. LockCleared is true only after the lock file is gone. Discarded lists
// repo-relative paths that were dirty and are clean afterwards. A caller that prints a
// repair from any other signal is claiming a repair this function did not do.
type WaitRecovery struct {
	Moved       bool
	LockCleared bool
	Discarded   []string
	// LockChanged is true when the stale index.lock was replaced or touched during the
	// process scan and so was left in place (ErrIndexLockChanged).
	LockChanged bool
}

// ClearStaleIndexLock removes the checkout's git index.lock when it is older than
// staleIndexLockAge, this account owns the lock file, and no live git process owns the
// checkout. A fresh lock is left alone whoever owns it. A stale lock owned by another
// account, or whose owner cannot be read, is refused before any process is looked at
// (see ownershipUnknown).
//
// THE FAILURE THIS CLOSES. A killed git leaves index.lock behind. Every later merge and
// commit then fails with "File exists" and the next tick fails the same way, forever,
// because nothing in the tick removes the lock. The lock is this run's own leftover when
// it is old and no git is still using the checkout. A fresh lock, a lock a live git still
// owns, and a lock that is still there after the remove are not removed and are not
// reported as removed.
//
// now is the machine clock, compared with the lock file's own mtime. It is not the note
// clock a verb freezes in tests: a frozen September would call a lock written today
// either ancient or not yet born, and both answers are lies.
func ClearStaleIndexLock(dir string, now time.Time) (bool, error) {
	return clearStaleIndexLock(dir, now, gitProcesses)
}

// clearStaleIndexLock is ClearStaleIndexLock with the process scan supplied.
// A test hands back an incomplete scan — a cwd git with no -C whose cwd could
// not be read, or a permission error — and the lock must still be here afterwards.
func clearStaleIndexLock(dir string, now time.Time, scan func() ([]gitProc, error)) (bool, error) {
	return clearStaleIndexLockAs(dir, now, scan, indexLockOwner, effectiveUID())
}

// indexLockOwnerOverrides holds the owners the CLI tests set on their own fixture locks,
// which cannot be chowned without root. An entry matches one lock file by os.SameFile,
// so a test running in parallel with its own lock is never touched. Nothing but
// SetIndexLockOwnerForTest writes it.
var indexLockOwnerOverrides sync.Map // *indexLockOwnerOverride -> struct{}

type indexLockOwnerOverride struct {
	fi  os.FileInfo
	uid uint32
}

// SetIndexLockOwnerForTest makes ClearStaleIndexLock read the lock file at path as owned
// by uid, until the returned restore runs. It is for tests outside this package that
// drive the wait end to end; the file itself is not changed.
func SetIndexLockOwnerForTest(path string, uid uint32) (restore func(), err error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	o := &indexLockOwnerOverride{fi: fi, uid: uid}
	indexLockOwnerOverrides.Store(o, struct{}{})
	return func() { indexLockOwnerOverrides.Delete(o) }, nil
}

// indexLockOwner is lockFileOwner unless a test has set this lock's owner.
func indexLockOwner(fi os.FileInfo) (uint32, bool) {
	uid, found := uint32(0), false
	indexLockOwnerOverrides.Range(func(k, _ any) bool {
		if o := k.(*indexLockOwnerOverride); os.SameFile(o.fi, fi) {
			uid, found = o.uid, true
			return false
		}
		return true
	})
	if found {
		return uid, true
	}
	return lockFileOwner(fi)
}

// effectiveUID is this process's account as the lock owner and the process scan compare
// it. On an OS with no uid it is a value no lock owner reads as.
func effectiveUID() uint32 {
	return uint32(os.Geteuid())
}

// lockOwnerForeignErr and lockOwnerUnknownErr are the two refusals about the lock file
// itself. The wait prints them after "WAIT REFUSED: ".
func lockOwnerForeignErr(uid uint32) error {
	return fmt.Errorf("index.lock is owned by uid %d, not this account; ask its owner or the bench admin", uid)
}

var errLockOwnerUnknown = errors.New("index.lock owner cannot be read, so it is not known to be this account's; ask its owner or the bench admin")

// clearStaleIndexLockAs is clearStaleIndexLock with the lock file's owner reader and this
// account's uid supplied. The age rule comes first; the owner of a stale lock is read
// from the Lstat of the lock itself, and only a stale lock owned by self goes on to the
// process scan.
func clearStaleIndexLockAs(dir string, now time.Time, scan func() ([]gitProc, error), lockOwner func(os.FileInfo) (uint32, bool), self uint32) (bool, error) {
	lock, err := indexLockPath(dir)
	if err != nil {
		return false, err
	}
	fi, err := os.Lstat(lock)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if fi.IsDir() {
		return false, fmt.Errorf("%s is a directory; refusing to remove it", lock)
	}
	// Lstat, not Stat: a symlink planted at the lock path is removed as a symlink.
	// Following it would delete whatever it pointed at.
	// A fresh lock is some git at work, whoever owns it: left alone, and the wait goes on.
	age := now.Sub(fi.ModTime())
	if age <= staleIndexLockAge {
		return false, nil
	}
	owner, ok := lockOwner(fi)
	if !ok {
		return false, errLockOwnerUnknown
	}
	if owner != self {
		return false, lockOwnerForeignErr(owner)
	}
	owns, err := gitOwnsCheckoutScan(dir, scan)
	if err != nil {
		// Not knowing is not the same as knowing the lock is leftover. Leave it.
		return false, err
	}
	if owns {
		return false, nil
	}
	// The scan took time, and the path may now hold a different lock: the old one finished
	// and a new git took the path, or someone touched it. Only the lock that was inspected
	// is removed: the same device and inode, the same owner, and no newer mtime.
	if changed, err := indexLockChanged(lock, fi, owner, lockOwner); err != nil || changed {
		if os.IsNotExist(err) {
			return false, nil
		}
		if err == nil {
			err = ErrIndexLockChanged
		}
		return false, err
	}
	if err := os.Remove(lock); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if _, statErr := os.Lstat(lock); !os.IsNotExist(statErr) {
		return false, fmt.Errorf("index.lock is still there after removing it")
	}
	return true, nil
}

// ErrIndexLockChanged is the answer when the index.lock at the path is no longer the one
// that was inspected by the time the scan finished. Nothing is removed; the wait prints
// "WAIT: " and this sentence and goes on, as it does for a fresh lock.
var ErrIndexLockChanged = errors.New("index.lock changed during the scan; waiting")

// indexLockChanged re-reads the lock path after the scan. A path now empty is returned as
// the Lstat error (IsNotExist). changed is true when the file there is not the inspected
// one (os.SameFile: device and inode), its owner is not the inspected owner, or its mtime
// is newer than the inspected mtime.
func indexLockChanged(lock string, inspected os.FileInfo, owner uint32, lockOwner func(os.FileInfo) (uint32, bool)) (bool, error) {
	now, err := os.Lstat(lock)
	if err != nil {
		return false, err
	}
	if !os.SameFile(inspected, now) {
		return true, nil
	}
	if uid, ok := lockOwner(now); !ok || uid != owner {
		return true, nil
	}
	return now.ModTime().After(inspected.ModTime()), nil
}

// indexLockPath is the lock file beside this checkout's index. It is asked of git, because
// a linked worktree keeps the index outside the work tree and `<bus>/.git/index.lock` is
// the wrong file there.
func indexLockPath(dir string) (string, error) {
	gd, err := GitDir(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(gd, "index.lock"), nil
}

// DiscardPath restores one repo-relative path to HEAD, or removes it when it is untracked.
//
// It is how wait throws away its own generated BEAT and nothing else. discarded is true
// only when the path was dirty and is clean afterwards. A symlink is never followed: an
// untracked link is removed as a link, and a tracked one is restored by git, which
// replaces the worktree entry rather than writing through it.
func DiscardPath(dir, rel string) (bool, error) {
	if err := discardablePath(rel); err != nil {
		return false, err
	}
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := insideRoot(dir, full); err != nil {
		return false, err
	}
	dirty, err := PathDirty(dir, rel)
	if err != nil {
		return false, err
	}
	if !dirty {
		return false, nil
	}
	untracked, err := pathUntracked(dir, rel)
	if err != nil {
		return false, err
	}
	fi, lerr := os.Lstat(full)
	switch {
	case lerr != nil && !os.IsNotExist(lerr):
		return false, lerr
	case lerr == nil && fi.Mode()&os.ModeSymlink != 0 && untracked:
		if err := os.Remove(full); err != nil {
			return false, err
		}
	case lerr == nil && untracked && fi.Mode().IsRegular():
		if err := os.Remove(full); err != nil {
			return false, err
		}
	case lerr == nil && untracked:
		return false, fmt.Errorf("%s is not a regular file; refusing to discard it", rel)
	default:
		if _, err := git(dir, "restore", "--source=HEAD", "--staged", "--worktree", "--", rel); err != nil {
			return false, err
		}
	}
	still, err := PathDirty(dir, rel)
	if err != nil {
		return false, err
	}
	if still {
		return false, fmt.Errorf("%s is still dirty after discarding it", rel)
	}
	return true, nil
}

// discardablePath refuses a path that could leave the checkout. The paths wait passes are
// lane-relative and contain neither a slash escape nor a parent segment; anything else is
// not a generated lane file and is not discarded.
func discardablePath(rel string) error {
	if rel == "" || rel == "." || filepath.IsAbs(rel) || strings.Contains(rel, "\\") || strings.Contains(rel, "..") {
		return fmt.Errorf("refusing to discard %q", rel)
	}
	return nil
}

// DirtyPaths lists repo-relative paths with uncommitted changes. The tool's own per-clone
// state directory is not one of them: it is not a note and not somebody else's work, and
// a wait must not refuse a fast-forward over it or claim to have repaired it.
func DirtyPaths(dir string) ([]string, error) {
	out, err := git(dir, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return porcelainPaths(out), nil
}

func porcelainPaths(out string) []string {
	recs := strings.Split(out, "\x00")
	var paths []string
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if len(rec) < 4 {
			continue
		}
		status, p := rec[:2], rec[3:]
		if strings.ContainsAny(status, "RC") && i+1 < len(recs) {
			i++
		}
		if p == BusStateDir || strings.HasPrefix(p, BusStateDir+"/") {
			continue
		}
		paths = append(paths, p)
	}
	return paths
}

func pathUntracked(dir, rel string) (bool, error) {
	out, err := git(dir, "status", "--porcelain", "-z", "--untracked-files=all", "--", rel)
	if err != nil {
		return false, err
	}
	recs := strings.Split(out, "\x00")
	saw := false
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if len(rec) < 4 {
			continue
		}
		status, p := rec[:2], rec[3:]
		if strings.ContainsAny(status, "RC") && i+1 < len(recs) {
			i++
		}
		if p != rel {
			continue
		}
		saw = true
		if status != "??" {
			return false, nil
		}
	}
	return saw, nil
}

// RecoverWaitFastForward fetches remote/branch and fast-forwards when the checkout is
// behind. It is wait's poll, with the two recoveries a killed tick needs and no others.
//
// A stale index.lock is removed before the fetch's merge, on ClearStaleIndexLock's terms.
// When the checkout is behind and every dirty path is one of owned — wait's own generated
// lane file, which is BEAT and not CURSOR — a dirty owned path that would block the
// fast-forward is discarded and the fast-forward is retried. Any other dirty path is
// refused by name and left byte for byte as it was: this function does not reset, does
// not clean, and does not checkout a path it was not given. A checkout that is level,
// ahead, or diverged is handled as FetchAndFastForward handles it, and a dirty file there
// is not a reason to discard anything.
func RecoverWaitFastForward(dir, remote, branch string, owned []string, now time.Time) (WaitRecovery, error) {
	var rec WaitRecovery
	if err := ValidGitArg("remote", remote); err != nil {
		return rec, err
	}
	if err := ValidGitArg("branch", branch); err != nil {
		return rec, err
	}
	cleared, err := ClearStaleIndexLock(dir, now)
	if errors.Is(err, ErrIndexLockChanged) {
		rec.LockChanged = true
		err = nil
	}
	if err != nil {
		return rec, err
	}
	rec.LockCleared = cleared
	if _, err := git(dir, "fetch", remote, branch); err != nil {
		return rec, fmt.Errorf("the fetch that would say whether anything has arrived on %s/%s failed: %w", remote, branch, err)
	}
	ref := trackingRef(dir, remote, branch)
	target, err := ResolveCommit(dir, ref)
	if err != nil {
		return rec, err
	}
	head, err := HeadCommit(dir)
	if err != nil {
		return rec, err
	}
	if head == target {
		return rec, nil
	}
	behind, err := isAncestorOf(dir, head, target)
	if err != nil {
		return rec, err
	}
	if !behind {
		ahead, aerr := isAncestorOf(dir, target, head)
		if aerr != nil {
			return rec, aerr
		}
		if ahead {
			return rec, nil
		}
		return rec, fmt.Errorf("this checkout and %s have both moved since they last agreed, so nothing here can be fast-forwarded onto the bus's history; %s", ref, pullRebaseAdvice)
	}
	dirty, err := DirtyPaths(dir)
	if err != nil {
		return rec, err
	}
	ownedSet := map[string]bool{}
	for _, p := range owned {
		ownedSet[p] = true
	}
	var foreign []string
	for _, p := range dirty {
		if !ownedSet[p] {
			foreign = append(foreign, p)
		}
	}
	if len(foreign) > 0 {
		sort.Strings(foreign)
		return rec, &foreignDirtyError{ref: ref, paths: foreign}
	}
	blocking, err := blockingPaths(dir, ref, dirty)
	if err != nil {
		return rec, err
	}
	for _, p := range blocking {
		discarded, derr := DiscardPath(dir, p)
		if derr != nil {
			return rec, derr
		}
		if discarded {
			rec.Discarded = append(rec.Discarded, p)
		}
	}
	if _, err := git(dir, "merge", "--ff-only", ref); err != nil {
		return rec, fmt.Errorf("this checkout is behind %s and the fast-forward onto it failed: %w", ref, err)
	}
	rec.Moved = true
	return rec, nil
}

// BehindRemote reports whether HEAD is a strict ancestor of the remote-tracking ref.
//
// A beat commit on a checkout that is still behind is not a repair and not a push: it is
// a new local commit on a stale base, and the next tick is diverged instead of behind.
// wait asks this before it lands a beat, and skips the commit when the answer is yes.
func BehindRemote(dir, remote, branch string) (bool, error) {
	ref := trackingRef(dir, remote, branch)
	target, err := ResolveCommit(dir, ref)
	if err != nil {
		return false, err
	}
	head, err := HeadCommit(dir)
	if err != nil {
		return false, err
	}
	if head == target {
		return false, nil
	}
	return isAncestorOf(dir, head, target)
}

// foreignDirtyError is a behind checkout whose fast-forward would have to discard a file
// wait does not own. The path is named and left untouched.
type foreignDirtyError struct {
	ref   string
	paths []string
}

func (e *foreignDirtyError) Error() string {
	if e == nil || len(e.paths) == 0 {
		return "this checkout is behind and a file this tool does not own is dirty; that file is not this tool's to discard"
	}
	if len(e.paths) == 1 {
		return fmt.Sprintf("this checkout is behind %s and %s is dirty; that file is not this tool's to discard", e.ref, e.paths[0])
	}
	return fmt.Sprintf("this checkout is behind %s and %s are dirty; those files are not this tool's to discard", e.ref, strings.Join(e.paths, ", "))
}

// blockingPaths is the dirty paths a fast-forward onto ref would refuse to overwrite:
// a path whose committed content changes between HEAD and ref, or an untracked path that
// already exists in ref.
func blockingPaths(dir, ref string, dirty []string) ([]string, error) {
	if len(dirty) == 0 {
		return nil, nil
	}
	out, err := git(dir, "diff", "--name-only", "-z", "--no-renames", "HEAD", ref)
	if err != nil {
		return nil, err
	}
	changed := map[string]bool{}
	for _, p := range strings.Split(out, "\x00") {
		if p != "" {
			changed[p] = true
		}
	}
	var block []string
	for _, p := range dirty {
		if changed[p] {
			block = append(block, p)
			continue
		}
		untracked, uerr := pathUntracked(dir, p)
		if uerr != nil {
			return nil, uerr
		}
		if !untracked {
			continue
		}
		exists, eerr := existsInTree(dir, ref, p)
		if eerr != nil {
			return nil, eerr
		}
		if exists {
			block = append(block, p)
		}
	}
	return block, nil
}

func existsInTree(dir, rev, rel string) (bool, error) {
	_, err := git(dir, "cat-file", "-e", rev+":"+rel)
	if err == nil {
		return true, nil
	}
	msg := err.Error()
	if strings.Contains(msg, "does not exist") || strings.Contains(msg, "Not a valid object") || strings.Contains(msg, "exists on disk, but not in") {
		return false, nil
	}
	return false, err
}

// ownershipDiagCap is how long the "we could not tell" sentence may be. The refusal
// that carries it is one line; a ps or lsof transcript is not a diagnostic.
const ownershipDiagCap = 200

// ownershipUnknown is the sentence every incomplete scan returns. The reason after
// the colon is short and has no newline: cwd unreadable, lsof failed, a permission
// error collapsed onto one line.
//
// Which account a lock or a git belongs to is judged from evidence, in this order. A
// fresh lock is some git at work, whoever owns it, and is left alone while the wait goes
// on. A stale lock's owner uid is read from its own Lstat: a stale lock owned by another
// account is never this caller's to clear, nor is one whose owner cannot be read, and
// both are refused before any process is looked at. Only a stale lock owned by this
// account goes on. Then every git, of any account, is placed by its readable cwd or
// by a command line that names an absolute work tree or git dir (-C, --git-dir,
// --work-tree); one placed at this checkout is an owner and the lock stays. A git of this
// account that nothing places makes the scan unknown, with this sentence. A git of
// another account that nothing places is skipped: this account cannot read its cwd (lsof
// omits it, /proc/<pid>/cwd answers EACCES), so it cannot be told from any other process
// on the host, and counting it made every wait on a shared bench refuse whenever another
// account ran git (CI run 36268521205: the runner account `nova` refused on the
// coordinator's gits). The skip removes no other account's lock: that account's git
// holds its own lock file, which the first rule refuses. What stays out of sight is a
// git of another account working in this checkout, with no absolute location on its
// command line, under a lock this account made. On linux the account is the owner of
// /proc/<pid>, and a non-dumpable process (one that changed credentials, or set
// PR_SET_DUMPABLE to 0) has its /proc/<pid> owned by root: a git of this account in that
// state reads as another account's and, when nothing places it, is skipped too. The
// account is the effective uid (os.Geteuid, ps uid, the owner of /proc/<pid> and of the
// lock file) because lsof, /proc and file ownership follow it.
const ownershipUnknown = "cannot tell whether a git process owns this checkout"

func ownershipUnknownErr(why string) error {
	why = boundDiag(why)
	msg := ownershipUnknown
	if why != "" {
		msg += ": " + why
	}
	if len(msg) > ownershipDiagCap {
		msg = msg[:ownershipDiagCap]
	}
	return errors.New(msg)
}

// boundDiag folds a scanner error onto one short line. The raw text of ps or lsof
// is not included by callers; this only caps whatever reason they already chose.
func boundDiag(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// procReadFailed separates a process that is gone from one that is still there
// and cannot be inspected. A gone process (procGone) is not an owner. Any other
// error is an inspection denial, and the lock stays.
func procReadFailed(err error) (vanished bool, unknown error) {
	if err == nil {
		return false, nil
	}
	if procGone(err) {
		return true, nil
	}
	return false, ownershipUnknownErr(err.Error())
}

// procGone reports whether a read of a process's metadata failed because the process
// no longer exists. ENOENT is a pid whose /proc entry is already gone. ESRCH is the
// same process one step earlier: Linux answers a read of /proc/<pid>/comm, cmdline,
// stat or cwd with "no such process" while an exited task is being torn down (#3029).
// On a host running a test package in parallel some process is always in that window,
// and treating it as an unreadable live process refused every wait that met it.
func procGone(err error) bool {
	return os.IsNotExist(err) || errors.Is(err, syscall.ESRCH)
}

// procView is one process's metadata as a scanner managed to read it, errors included.
// dead is a zombie or an otherwise exited process: an empty cmdline on a dead
// process is not a live git whose metadata could not be read.
type procView struct {
	commErr error
	comm    string
	cmdErr  error
	cmdline []byte
	cwdErr  error
	cwd     string
	dead    bool
	// owner is the process's effective uid when ownerKnown. A process of another account
	// that cannot be placed is skipped rather than unknown (see ownershipUnknown).
	owner      uint32
	ownerKnown bool
}

// procReader is how readProcView sees one /proc entry: owner is the entry's owner uid
// (ok false when the stat has no uid), readFile and readlink are os.ReadFile and
// os.Readlink on linux. A test drives it with a fake on every OS.
type procReader struct {
	owner    func(pid string) (uid uint32, ok bool, err error)
	readFile func(name string) ([]byte, error)
	readlink func(name string) (string, error)
}

// readProcView reads one pid. The owner of /proc/<pid> is the process's effective uid,
// recorded for classifyViews; the process is read the same way whoever owns it, so a
// command line of another account that names this checkout is still seen. An owner stat
// that fails because the pid is gone is the vanished case; any other failure leaves the
// owner unknown, and the process is then judged as this account's.
func readProcView(pid string, r procReader) procView {
	var v procView
	uid, ok, err := r.owner(pid)
	if err != nil {
		if procGone(err) {
			v.commErr = err
			return v
		}
	} else if ok {
		v.owner, v.ownerKnown = uid, true
	}
	comm, err := r.readFile("/proc/" + pid + "/comm")
	if err != nil {
		v.commErr = err
		return v
	}
	v.comm = string(comm)
	name := strings.TrimSpace(v.comm)
	if name != "git" && name != "git.exe" {
		return v
	}
	raw, err := r.readFile("/proc/" + pid + "/cmdline")
	if err != nil {
		v.cmdErr = err
		return v
	}
	v.cmdline = raw
	// An empty cmdline is how a zombie git looks. State Z or X is dead, and a
	// pid that vanished while we were reading it is dead too. A live process
	// with an empty cmdline is not: that stays unclassified unless its cwd places it.
	if len(splitNUL(raw)) == 0 {
		dead, derr := procDead(pid, r)
		if procGone(derr) {
			v.dead = true
		} else if derr == nil {
			v.dead = dead
		}
	}
	cwd, err := r.readlink("/proc/" + pid + "/cwd")
	if err != nil {
		v.cwdErr = err
		return v
	}
	v.cwd = cwd
	return v
}

// procDead reports whether pid is a zombie or already dead. ENOENT or ESRCH (procGone)
// means the pid vanished, which the caller treats as dead. Any other error means the
// state could not be read; the caller must not invent "dead" from that.
func procDead(pid string, r procReader) (bool, error) {
	raw, err := r.readFile("/proc/" + pid + "/stat")
	if err != nil {
		return false, err
	}
	dead, ok := procStatDead(string(raw))
	if !ok {
		return false, fmt.Errorf("stat")
	}
	return dead, nil
}

// procStatDead reads the state character out of /proc/pid/stat. Z and X are not
// live processes. ok is false when the line is not a stat line.
func procStatDead(stat string) (dead bool, ok bool) {
	i := strings.LastIndex(stat, ")")
	if i < 0 || i+2 >= len(stat) || stat[i+1] != ' ' {
		return false, false
	}
	switch stat[i+2] {
	case 'Z', 'X':
		return true, true
	default:
		return false, true
	}
}

// gitProcFromView classifies one process. skip means it is not a live git this
// scan has to place (it vanished, it is dead, or it is not git). An error means
// the process is still live and its metadata could not be read, so ownership of
// that one process is unknown. The caller still classifies the rest of the scan:
// one empty cmdline must not hide a later owner, and must not by itself answer
// "nobody owns this checkout".
func gitProcFromView(v procView) (gitProc, bool, error) {
	if v.commErr != nil {
		vanished, uerr := procReadFailed(v.commErr)
		if uerr != nil {
			return gitProc{}, false, uerr
		}
		if vanished {
			return gitProc{}, true, nil
		}
	}
	name := strings.TrimSpace(v.comm)
	if name != "git" && name != "git.exe" {
		return gitProc{}, true, nil
	}
	if v.cmdErr != nil {
		vanished, uerr := procReadFailed(v.cmdErr)
		if uerr != nil {
			return gitProc{}, false, uerr
		}
		if vanished {
			return gitProc{}, true, nil
		}
	}
	args := splitNUL(v.cmdline)
	// A dead git's cmdline is empty. That is not an unreadable live process.
	if v.cmdErr == nil && len(args) == 0 && v.dead {
		return gitProc{}, true, nil
	}
	p := gitProc{command: strings.Join(args, " "), args: args}
	if v.cwdErr != nil {
		vanished, uerr := procReadFailed(v.cwdErr)
		if vanished {
			return gitProc{}, true, nil
		}
		if uerr != nil {
			if commandLocatesAbsolutely(p) {
				return p, false, nil
			}
			if len(args) == 0 {
				return gitProc{}, false, ownershipUnknownErr("cmdline empty")
			}
			return gitProc{}, false, uerr
		}
	}
	if v.cwd == "" {
		if commandLocatesAbsolutely(p) {
			return p, false, nil
		}
		if len(args) == 0 {
			return gitProc{}, false, ownershipUnknownErr("cmdline empty")
		}
		return gitProc{}, false, ownershipUnknownErr("cwd unreadable")
	}
	p.cwd = v.cwd
	p.cwdKnown = true
	return p, false, nil
}

// classifyViews finishes the scan. A process that cannot be classified is remembered
// and the rest are still classified, so a known owner later in the list is not dropped
// on the floor because an earlier cmdline was empty.
// A view is placed the same way whichever account owns it; a view of another account
// (known owner not self) that cannot be placed is skipped, not unknown.
func classifyViews(views []procView, self uint32) ([]gitProc, error) {
	var procs []gitProc
	var unknown error
	for _, v := range views {
		p, skip, err := gitProcFromView(v)
		if err != nil {
			if v.ownerKnown && v.owner != self {
				continue
			}
			if unknown == nil {
				unknown = err
			}
			continue
		}
		if skip {
			continue
		}
		procs = append(procs, p)
	}
	return procs, unknown
}

func splitNUL(b []byte) []string {
	parts := strings.Split(string(b), "\x00")
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// gitProc is one live git process: its command line, its argv when the OS gives it to us
// separated, and its current directory when we could read one. cwdKnown is false when
// the cwd could not be read. An empty cwd with cwdKnown false is not "this git is
// nowhere"; it is "we do not know", and the lock stays unless the command line itself
// names an absolute work tree.
type gitProc struct {
	command  string
	args     []string
	cwd      string
	cwdKnown bool
}

// gitOwnsCheckout reports whether a live git process is operating on dir. An error means
// the question could not be answered, which the caller treats as "do not remove the lock".
func gitOwnsCheckout(dir string) (bool, error) {
	return gitOwnsCheckoutScan(dir, gitProcesses)
}

func gitOwnsCheckoutScan(dir string, scan func() ([]gitProc, error)) (bool, error) {
	names, err := ownerNames(dir)
	if err != nil {
		return false, err
	}
	procs, scanErr := scan()
	for _, p := range procs {
		if procOwns(names, p) {
			// A known owner answers the question. An unclassified process elsewhere
			// in the same scan does not un-answer it, and does not hide it.
			return true, nil
		}
	}
	if scanErr != nil {
		return false, scanErr
	}
	for _, p := range procs {
		if !p.cwdKnown && !commandLocatesAbsolutely(p) {
			return false, ownershipUnknownErr("cwd unreadable")
		}
	}
	return false, nil
}

// commandLocatesAbsolutely reports whether the command line names an absolute work
// tree or git dir of its own, so a missing cwd is not what the ownership decision
// depends on. A relative -C still depends on the cwd and does not count.
func commandLocatesAbsolutely(p gitProc) bool {
	args := p.args
	if len(args) == 0 && p.command != "" {
		args = strings.Fields(p.command)
	}
	for i, a := range args {
		switch {
		case a == "-C" || a == "--git-dir" || a == "--work-tree":
			if i+1 >= len(args) {
				return false
			}
			return filepath.IsAbs(args[i+1])
		case strings.HasPrefix(a, "--git-dir=") || strings.HasPrefix(a, "--work-tree="):
			return filepath.IsAbs(a[strings.IndexByte(a, '=')+1:])
		case strings.HasPrefix(a, "-C") && len(a) > 2:
			return filepath.IsAbs(a[2:])
		}
	}
	return false
}

// ownerNames is every spelling of the checkout and its git directory that a process list
// might show. On Darwin /var is /private/var; a match on only one of them would either
// miss a live git or, worse, fail to recognise it and remove the lock under it.
func ownerNames(dir string) ([]string, error) {
	names, err := identityPaths(dir)
	if err != nil {
		return nil, err
	}
	gd, err := GitDir(dir)
	if err != nil {
		return nil, err
	}
	gnames, err := identityPaths(gd)
	if err != nil {
		return nil, err
	}
	return append(names, gnames...), nil
}

func identityPaths(dir string) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	add := func(p string) {
		p = filepath.Clean(p)
		if p == "" || p == "." || p == string(filepath.Separator) {
			return
		}
		for _, e := range out {
			if e == p {
				return
			}
		}
		out = append(out, p)
	}
	add(abs)
	if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		add(resolved)
	}
	return out, nil
}

// procOwns reports whether p is a git working in one of names (the checkout or its git
// directory) or in a directory inside it. A path that merely shares a prefix — /bus and
// /bus2 — does not own /bus.
func procOwns(names []string, p gitProc) bool {
	if cwd := filepath.Clean(p.cwd); cwd != "" && cwd != "." {
		for _, n := range names {
			if pathWithin(n, cwd) {
				return true
			}
		}
	}
	for _, n := range names {
		if n == "" {
			continue
		}
		if commandMentionsPath(p.command, n) {
			return true
		}
		for _, a := range p.args {
			if pathWithin(n, a) {
				return true
			}
		}
	}
	return false
}

func pathWithin(parent, child string) bool {
	if parent == "" || child == "" {
		return false
	}
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if !filepath.IsAbs(parent) || !filepath.IsAbs(child) {
		return parent == child
	}
	if parent == child {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	return true
}

// commandMentionsPath reports whether command carries path as a whole path and not as the
// prefix of a longer one. The character before it may be a flag boundary (-C, --git-dir=)
// so `git -C/path` still counts.
func commandMentionsPath(command, path string) bool {
	if command == "" || path == "" {
		return false
	}
	rest := command
	for {
		i := strings.Index(rest, path)
		if i < 0 {
			return false
		}
		end := i + len(path)
		before := i == 0 || isPathBoundary(rest[i-1]) || strings.HasSuffix(rest[:i], "-C") || strings.HasSuffix(rest[:i], "--git-dir=") || strings.HasSuffix(rest[:i], "--work-tree=")
		after := end == len(rest) || isPathBoundary(rest[end])
		if before && after {
			return true
		}
		rest = rest[i+1:]
	}
}

func isPathBoundary(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '=', '"', '\'', '/', ':':
		return true
	default:
		return false
	}
}
