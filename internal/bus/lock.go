package bus

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// One nova-bus per checkout.
//
// THE FAILURE THIS CLOSES. Every verb here works by reading the checkout, writing into it,
// and asking git about it -- and two invocations on ONE checkout interleave all three. Two
// `inbox --advance` runs read the same OPEN list and each writes it back without the
// other's work; a `send` mid-commit is a dirty tree the other one refuses over, or worse,
// stages; a rebase started by one is a checkout the other finds detached. None of those is
// a race the tool can win by being careful, because the shared state is a directory rather
// than a variable, and none of them is what the push protocol's retry is for: that is two
// benches, on two checkouts, which is the case this tool was built for and handles. This is
// two of ME, on one checkout, which is a mistake and should say so.
//
// So a run takes a lock on the checkout and holds it to the end. The second one WAITS --
// briefly, because the first is usually a fetch away from finishing -- and then refuses
// with a sentence rather than corrupting anything.
//
// The lock is an flock on a file inside the git directory. Inside the git directory because
// that is per-CHECKOUT: two linked worktrees of one repository are two checkouts and must
// not block each other, and a lock at the bus root would be a file on the bus that
// every reader would then have to know is not a note. An flock rather than an O_EXCL
// sentinel because the kernel drops it when the process dies: a run killed with the lock
// held leaves nothing for the next one to clear, which is the failure mode that makes
// lock-file schemes worse than no lock at all.

// LockName is the lock file, in the checkout's git directory.
const LockName = "nova-bus.lock"

// ErrLockHeld indicates that the requested lock could not be acquired within the wait duration.
var ErrLockHeld = errors.New("lock held")

// lockClock is the lock's view of time: the deadline it reads and the poll it waits
// between attempts. It is a seam so a test drives the whole bounded wait with no wall
// time -- the fake's Sleep advances Now, so a 500ms budget is over in a few instant
// polls. Production passes realLockClock; a nil clock takes it too.
type lockClock interface {
	Now() time.Time
	Sleep(time.Duration)
}

// realLockClock is the production clock: the machine's.
type realLockClock struct{}

func (realLockClock) Now() time.Time        { return time.Now() }
func (realLockClock) Sleep(d time.Duration) { time.Sleep(d) }

// staleLockRemovalWindow bounds how long a sentinel removal is retried when the
// platform reports a transient collision. On Windows a file that was just
// closed can sit in a delete-pending or sharing-violating state for a moment,
// so a single os.Remove is not enough to prove the lock was released.
const staleLockRemovalWindow = 250 * time.Millisecond

// maxStaleRecoveries bounds how many dead-holder sentinels one acquisition will
// clear, so a delete-and-recreate cannot turn the wait loop into a spin.
const maxStaleRecoveries = 3

// sentinelPath is the sibling file whose existence means a sentinel lock is
// held. It is beside the lock file the caller opened so that the lock stays in
// the git directory and dies with it.
func sentinelPath(name string) string {
	return filepath.Clean(name) + ".held"
}

// removeLockFile removes a lock artifact, retrying over a short bounded window
// while the platform calls the failure a transient lock collision. A missing
// file is success: the lock is already gone.
func removeLockFile(path string) error {
	deadline := time.Now().Add(staleLockRemovalWindow)
	for {
		err := os.Remove(path)
		if err == nil || errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if !platformTransientLockCollision(err) || !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(lockPoll)
	}
}

// clearStaleSentinel reports whether it removed the sentinel beside lockPath
// because the holder the lock file names is no longer alive.
//
// On the sentinel platforms (Windows and the generic exclusive-create build) a
// killed process cannot have its lock dropped by the kernel, so the sentinel
// outlives it and the next run would otherwise wait out its whole budget for a
// process that will never release anything. The pid stamped into the lock file
// is the only thing that distinguishes a dead holder's lock from a live one, so
// it is read and checked rather than inferred from the file's existence.
//
// On unix the kernel drops an flock when its holder dies, so there is normally
// no sentinel to clear; if one is left in the repository by a Windows run, this
// clears it the same way rather than refusing over a file nothing owns.
func clearStaleSentinel(lockPath string) bool {
	sentinel := sentinelPath(lockPath)
	if _, err := os.Stat(sentinel); err != nil {
		return false
	}
	holder := ReadLockHolder(lockPath)
	pid, err := strconv.Atoi(holder)
	if err != nil || pid <= 0 {
		// No pid to judge: an older or unwritten record. Never clear a lock
		// whose holder cannot be shown to be gone.
		return false
	}
	if processAlive(pid) {
		return false
	}
	// Confirm the holder is still the same dead one before removing, so a live
	// acquirer that has created the sentinel but not yet stamped its pid cannot
	// have its lock cleared out from under it.
	if again := ReadLockHolder(lockPath); again != holder {
		return false
	}
	if err := removeLockFile(sentinel); err != nil {
		return false
	}
	return true
}

// LockFile takes an exclusive advisory lock on path, waiting up to wait for it, and returns the
// release function. The release is safe to call more than once.
//
// On Unix this is an flock (advisory lock) that dies with the process.
// On Windows this is an O_EXCL sentinel file (.held).
//
// When the lock is taken, LockFile stamps the current process PID into the file so
// waiters and refusals can name the holder.
// If wait is 0, LockFile attempts to acquire the lock once without waiting.
// If wait > 0, LockFile polls every 25ms until the deadline.
// If the lock cannot be acquired within wait, it returns an error wrapping ErrLockHeld.
func LockFile(path string, wait time.Duration) (func(), error) {
	return lockFile(path, wait, tryLockFile, nil)
}

func lockFile(path string, wait time.Duration, try func(f *os.File) (ok bool, retryable bool, err error), clk lockClock) (func(), error) {
	if clk == nil {
		clk = realLockClock{}
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("the lock at %s could not be opened: %w", path, err)
	}
	deadline := clk.Now().Add(wait)
	var lastErr error
	recoveries := 0
	for {
		ok, retryable, lockErr := try(f)
		if lockErr != nil {
			if !retryable || wait == 0 {
				f.Close()
				return nil, fmt.Errorf("the lock at %s could not be taken: %w", path, lockErr)
			}
			lastErr = lockErr
		} else {
			lastErr = nil
		}
		if ok {
			stampLockHolder(f)
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
		if clearStaleSentinel(path) && recoveries < maxStaleRecoveries {
			// The sentinel is a dead run's lock, not a live one: its holder is
			// gone, so waiting out the budget would be waiting for a process
			// that can never release anything. Try again at once. The cap keeps
			// a pathological delete-and-recreate from spinning this loop.
			recoveries++
			continue
		}
		if wait == 0 || !clk.Now().Before(deadline) {
			f.Close()
			if lastErr != nil {
				return nil, fmt.Errorf("the lock at %s could not be taken: %w", path, lastErr)
			}
			holder := ReadLockHolder(path)
			return nil, fmt.Errorf("the lock at %s is held by process %s; waited %s: %w", path, holder, wait, ErrLockHeld)
		}
		clk.Sleep(jitter(lockPoll))
	}
}

// jitter spreads the retries of several waiters so they do not wake together and collide again (lesson 66).
func jitter(d time.Duration) time.Duration {
	return d + time.Duration(rand.Int63n(int64(d/2)+1))
}

// stampLockHolder writes the current PID to the lock file.
func stampLockHolder(f *os.File) {
	line := fmt.Sprintf("pid=%d at=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	if err := f.Truncate(0); err != nil {
		return
	}
	if _, err := f.Seek(0, 0); err != nil {
		return
	}
	_, _ = f.WriteString(line)
	_ = f.Sync()
}

// ReadLockHolder reads the pid written into the lock file by its holder.
// It handles both bare "<pid>" and "pid=<n> at=<stamp>" formats.
// If unreadable, empty, or missing, returns "-".
func ReadLockHolder(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "-"
	}
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return "-"
	}
	for _, tok := range strings.Fields(s) {
		if v, ok := strings.CutPrefix(tok, "pid="); ok {
			if _, err := strconv.Atoi(v); err == nil {
				return v
			}
		}
	}
	if _, err := strconv.Atoi(s); err == nil {
		return s
	}
	return "-"
}

// LockCheckout takes this checkout's lock, waiting up to wait for it, and returns the
// release. The release is safe to call more than once.
//
// A bus that is not a git checkout at all has nothing to lock and is not locked: the only
// verbs that reach one are the full reads, which write nothing, and refusing them for the
// want of a `.git` would be refusing a bus for a reason that is not about the bus.
func LockCheckout(busDir string, wait time.Duration) (func(), error) {
	return lockCheckoutAt(busDir, wait, nil)
}

// lockCheckoutAt is LockCheckout with the clock injected, so a test can drive the wait
// for a held lock to its end without spending the machine's time doing it.
func lockCheckoutAt(busDir string, wait time.Duration, clk lockClock) (func(), error) {
	gd, err := GitDir(busDir)
	if err != nil {
		return func() {}, nil
	}
	path := filepath.Join(gd, LockName)
	release, err := lockFile(path, wait, tryLockFile, clk)
	if err != nil {
		if errors.Is(err, ErrLockHeld) {
			return nil, fmt.Errorf("another nova-bus is already running on this checkout and still holds %s; this run waited %s for it and will not work beside it, because two runs on one checkout write one OPEN list and one index -- run this again when that one has finished", path, wait)
		}
		return nil, fmt.Errorf("the lock that keeps two runs off one checkout could not be opened at %s: %w", path, err)
	}
	return release, nil
}

// lockPoll is how often the wait re-tries. It is a poll rather than a blocking flock
// because the wait is BOUNDED: a blocking take would give the refusal no way to happen, and
// the refusal is the point.
const lockPoll = 25 * time.Millisecond
