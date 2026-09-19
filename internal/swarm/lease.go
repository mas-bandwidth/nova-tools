package swarm

// THE JOB LEASE: the launcher's own word that a card is alive (issue #1499).
//
// The bench's hygiene pass has to decide, hourly, which job directories are finished work
// and which are a card still doing its job. It used to decide by SILENCE -- no new bytes in
// harness-output.log for fifteen minutes -- and a card in one long model call or one long
// compile is silent and working. On 2026-09-19 that heuristic deleted <slot>/data and
// <slot>/tmp, a running card's HOME and TMPDIR, out from under two certify passes.
//
// A launcher knows what a heuristic can only guess, so it says so on disk: `nova-swarm
// native` writes <job>/.lease before the child starts, carrying the launcher's pid, and
// heartbeats the file's mtime while the child runs. The reaper (scripts/bench-hygiene.sh)
// reads exactly this: a job whose lease names a live pid, or whose heartbeat is younger
// than its stale window, is LIVE and is never touched. The file is removed when the run
// ends, so a finished job leaves nothing behind that pretends to be alive.
//
// The lease is opened O_NOFOLLOW, like the capture beside it: the job directory is the
// card's own writable place, and a symlink planted there by an earlier run of the same
// card would carry this process's write out of the wall.
//
// THE LEASE IS ALSO THE JOB DIRECTORY'S OWNERSHIP (issue #1585). Two `native` runs were
// given one physical `<slot>/jobs/<label>`: the bench store gave each its own seat, but
// the job directory, the data home, the temp directory and the logs under it were one set
// of paths, and the first run to exit removed the other's lease -- which the other's
// heartbeat, a bare Chtimes, could not put back. SPEC-SWARM is not ambiguous about
// whether that is lawful: under **Slots**, a worker has "its own data home", "its own job
// directory", and "a slot is held by exactly one worker"; under **the races, taken out**,
// two workers on one data home is the 2026-09-10 `database is locked` failure, closed on
// purpose. So two live runs in one job directory is never a thing to make safe -- it is a
// thing to refuse. Three properties hold that here:
//
//   - THE TAKE IS EXCLUSIVE. The file is created O_EXCL, which is the atomic claim, and a
//     take that finds a live holder is refused with a *JobLeaseHeldError naming it. The
//     caller refuses the launch before it has modified one byte of shared state.
//   - THE RELEASE IS FENCED. A release removes the lease only while the file on disk is
//     still the one this run wrote, identified by pid AND a per-run nonce. Another run's
//     lease is never removed.
//   - THE HEARTBEAT REPAIRS. A held lease that goes missing -- a hand, an older binary, a
//     `rm -rf` in the wrong directory -- is written again on the next tick, because a live
//     job with no lease is a job the reaper is entitled to delete.
//
// A lease whose launcher is DEAD is not live and is taken over: a crashed launcher must
// not cost the next run of that card a wait.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// JobLeaseName is the lease file's name inside the job directory. The reaper knows it by
// this name and by nothing else.
const JobLeaseName = ".lease"

// JobLeaseHeartbeat is how often a held lease's mtime is bumped. It is far under the ten
// minutes the reaper allows a heartbeat to age, so a bench under load that misses a tick
// or two still reads as live.
const JobLeaseHeartbeat = 30 * time.Second

// JobLease is what a lease file says.
type JobLease struct {
	PID     int
	Host    string
	Label   string
	Nonce   string
	Started string
	Path    string
}

// String is the one line a refusal names a holder with.
func (l JobLease) String() string {
	return fmt.Sprintf("pid=%d host=%s label=%s started=%s", l.PID, l.Host, l.Label, l.Started)
}

// Live reports whether this lease still speaks for a running launcher: THE KERNEL IS
// ASKED, and a pid that is gone is gone, so the next run of that card takes the directory
// over rather than waiting out a window for a launcher that crashed.
//
// The reaper's second clause -- a heartbeat younger than HYGIENE_LEASE_STALE_MIN counts as
// live -- is the REAPER's, and deliberately not read here. It exists because a bench script
// may not be able to see the pid it is asked about; this process can, on the only machine a
// slot directory lives on. It is also a modification time deciding something, and nothing
// in this package decides anything by one (revision_test.go).
func (l JobLease) Live() bool {
	return Alive(l.PID, "")
}

// JobLeaseHeldError is the refusal a take gets when a live run already holds the job
// directory. It carries the holder so the caller's one refusal line can name it.
type JobLeaseHeldError struct {
	Dir    string
	Holder JobLease
}

// HeldJobLease answers the holder a take was refused for, when that is why it was refused.
// It is here rather than at the caller so a launcher needs no errors.As of its own.
func HeldJobLease(err error) (JobLease, bool) {
	var held *JobLeaseHeldError
	if errors.As(err, &held) {
		return held.Holder, true
	}
	return JobLease{}, false
}

func (e *JobLeaseHeldError) Error() string {
	return fmt.Sprintf("the job directory %s is held by a live run (%s); two runs in one job directory share one data home, one temp directory and one set of logs, and the first to end removes the other's lease",
		e.Dir, e.Holder)
}

// ReadJobLease reads the lease in jobDir. A job directory with no lease answers an error
// satisfying errors.Is(err, os.ErrNotExist).
func ReadJobLease(jobDir string) (JobLease, error) {
	path := filepath.Join(jobDir, JobLeaseName)
	raw, err := os.ReadFile(path)
	if err != nil {
		return JobLease{}, err
	}
	lease := JobLease{Path: path}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "pid":
			lease.PID, _ = strconv.Atoi(value)
		case "host":
			lease.Host = value
		case "label":
			lease.Label = value
		case "nonce":
			lease.Nonce = value
		case "started":
			lease.Started = value
		}
	}
	return lease, nil
}

// StartJobLease takes the lease on jobDir for the running child and returns the release.
//
// It REFUSES, with a *JobLeaseHeldError, when a live run already holds the directory: that
// is the same-path exclusion of issue #1585, and the caller refuses the launch on it
// before it modifies any shared state. A lease whose holder is dead is removed and taken.
//
// The release is safe to call more than once, and it removes the file only while the file
// is still this run's -- pid and nonce both. A lease that cannot be WRITTEN at all is not
// an error the run fails on (the reaper's other rules, an age and a shape, still protect
// the job): the caller gets a release that does nothing and the run goes on.
func StartJobLease(jobDir, label string) (release func(), err error) {
	return startJobLeaseEvery(jobDir, label, JobLeaseHeartbeat)
}

func startJobLeaseEvery(jobDir, label string, every time.Duration) (func(), error) {
	path := filepath.Join(jobDir, JobLeaseName)
	host, _ := os.Hostname()
	nonce := newLeaseNonce()
	body := fmt.Sprintf("pid=%d\nhost=%s\nlabel=%s\nnonce=%s\nstarted=%s\n",
		os.Getpid(), host, label, nonce, time.Now().UTC().Format(time.RFC3339))

	written, err := writeLeaseExclusive(path, body)
	if err != nil {
		return func() {}, err
	}
	if !written {
		// The path could not be created and nobody live holds it: an unwritable job
		// directory, a symlink where the lease should be, a full disk. That is not a
		// reason to fail the run, and it never was.
		return func() {}, nil
	}

	done := make(chan struct{})
	var once sync.Once
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case now := <-t.C:
				if err := os.Chtimes(path, now, now); err == nil {
					continue
				}
				// THE REPAIR. Chtimes on a path that is not there does nothing and says
				// nothing, which is how a live job lost its protection in #1585. Write
				// the same lease again, exclusively: if somebody else now holds the path
				// this loses the race and leaves their lease alone, which is right.
				_, _ = writeLeaseExclusive(path, body)
			}
		}
	}()
	return func() {
		once.Do(func() {
			close(done)
			releaseLeaseIfOurs(path, os.Getpid(), nonce)
		})
	}, nil
}

// writeLeaseExclusive creates the lease with O_EXCL, which is the atomic claim on the job
// directory. It answers (true, nil) when this call made the file; (false, *JobLeaseHeldError)
// when a live run holds it; and (false, nil) when the file could not be made for a reason
// that is not somebody else's lease.
func writeLeaseExclusive(path, body string) (bool, error) {
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|ONoFollow, 0o644)
		if err == nil {
			_, werr := f.WriteString(body)
			cerr := f.Close()
			if werr != nil || cerr != nil {
				return false, nil
			}
			return true, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return false, nil
		}
		// Somebody's lease is there. Whose, and is it alive?
		held, rerr := ReadJobLease(filepath.Dir(path))
		if rerr != nil {
			// It went away between the create and the read: try the create once more.
			continue
		}
		if held.Live() {
			return false, &JobLeaseHeldError{Dir: filepath.Dir(path), Holder: held}
		}
		// A dead holder's lease is not a lease. Remove exactly it and take the path.
		if !releaseLeaseIfOurs(path, held.PID, held.Nonce) {
			return false, nil
		}
	}
	return false, nil
}

// releaseLeaseIfOurs removes the lease only while the file on disk still names pid and
// nonce. There is no atomic compare-and-unlink on these systems, so this is a read and
// then a remove: the window is two system calls wide, and what it buys is that a release
// arriving after ANOTHER run has taken the path -- the whole of #1585 -- removes nothing.
func releaseLeaseIfOurs(path string, pid int, nonce string) bool {
	held, err := ReadJobLease(filepath.Dir(path))
	if err != nil {
		return errors.Is(err, os.ErrNotExist)
	}
	if held.PID != pid || held.Nonce != nonce {
		return false
	}
	return os.Remove(path) == nil
}

// newLeaseNonce is what tells two runs apart when their pids cannot: the same process
// taking the lease twice, and a pid the operating system has re-issued.
func newLeaseNonce() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}
