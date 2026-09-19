package swarm

// THE JOB LEASE: the launcher's own word that a card is alive (issue #1499), and the job
// directory's ownership (issue #1585).
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
// IT IS ALSO THE ADMISSION TO THE JOB DIRECTORY. Two `native` runs were given one physical
// <slot>/jobs/<label>: the bench store gave each its own seat, but the job directory, the
// data home, the temp directory and the logs under it were one set of paths, and the first
// run to exit removed the other's lease -- which the other's heartbeat, a bare Chtimes,
// could not put back. SPEC-SWARM is not ambiguous about whether that is lawful: under
// **Slots**, a worker has "its own data home", "its own job directory", and "a slot is held
// by exactly one worker"; under **the races, taken out**, two workers on one data home is
// the 2026-09-10 `database is locked` failure, closed on purpose. So two live runs in one
// job directory is never a thing to make safe -- it is a thing to refuse.
//
// FOUR RULES HOLD IT, AND THE FIRST TWO ARE STELLA'S (her HOLD on the first repair, which
// still admitted two launchers in two demonstrated ways):
//
//  1. OWNERSHIP IS PUBLISHED WHOLE. The lease is written to a temp file in the same
//     directory, flushed to the platform, and LINKED into place -- so the name .lease never
//     exists holding a partial record. The first repair created the file with O_EXCL and
//     wrote afterwards, and a competitor that read the file in that window saw an empty
//     record, called it pid 0, called pid 0 dead, and took the path out from under a
//     creator whose own write then went to an unlinked inode.
//  2. AN INCOMPLETE RECORD IS NEVER EVIDENCE OF A DEAD OWNER. A lease that cannot be parsed
//     -- empty, truncated, half a line -- is HELD BY AN UNKNOWN OWNER, and stays held until
//     its heartbeat is older than JobLeaseStale. Only then is it reclaimed.
//  3. FAILING TO ESTABLISH OWNERSHIP IS A REFUSAL, NEVER A SILENT SUCCESS. This file used to
//     hand back a do-nothing release when it could not write, on the grounds that the
//     reaper's other rules still protected the job. That was defensible while the lease was
//     only advice to a reaper. It is not defensible now that the lease is what keeps two
//     launchers out of one directory: `.lease` as a directory, an unwritable job directory
//     or an unreadable record all used to end with BOTH runs proceeding. Every one of them
//     is now an error, and `native` exits 2 on it with a remedy. See SPEC-SWARM, "One live
//     run per job directory".
//  4. THE RELEASE IS FENCED AND JOINED. A release removes the lease only while the file is
//     still the one this run published -- pid AND a per-run nonce -- and only after the
//     heartbeat has stopped, so a tick already selected cannot write the lease back after
//     the run that owned it has ended.
//
// The lease and its temp file are opened O_NOFOLLOW, like the capture beside it: the job
// directory is the card's own writable place, and a symlink planted there by an earlier run
// of the same card would carry this process's write out of the wall.

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

// JobLeaseStale is how old a heartbeat may be before a lease whose OWNER CANNOT BE ASKED
// ABOUT stops counting as held. It is the reaper's own window
// (scripts/bench-hygiene.sh, HYGIENE_LEASE_STALE_MIN=10), read the same way and for the
// same reason: it is the only word there is about a record naming no pid this kernel can
// answer for. A lease whose pid this host CAN be asked about never reaches it.
const JobLeaseStale = 10 * time.Minute

// jobLeaseAttempts bounds the take: each attempt either publishes, refuses, or reclaims one
// dead holder and tries again. Exhausting them is a refusal and never a silent success --
// a path this process loses three times running is a path it cannot prove it owns.
const jobLeaseAttempts = 3

// JobLease is what a lease file says. Beat is the file's mtime, which is the heartbeat, and
// Known says whether the record parsed at all: an incomplete record names no owner and is
// never read as a dead one.
type JobLease struct {
	PID     int
	Host    string
	Label   string
	Nonce   string
	Started string
	Beat    time.Time
	Known   bool
	Path    string
}

// String is the one line a refusal names a holder with.
func (l JobLease) String() string {
	if !l.Known {
		return fmt.Sprintf("an unfinished or unreadable record, last beat %s", l.Beat.UTC().Format(time.RFC3339))
	}
	return fmt.Sprintf("pid=%d host=%s label=%s started=%s", l.PID, l.Host, l.Label, l.Started)
}

// Live reports whether this lease still speaks for a running launcher.
//
// THE KERNEL IS ASKED WHEREVER IT CAN BE: a pid on this host that is gone is gone, and the
// next run of that card takes the directory over rather than waiting out a window for a
// launcher that crashed. Two records cannot be asked about -- one written on another host,
// and one that did not parse -- and for those the heartbeat is the only word there is, so
// they are HELD until they are older than JobLeaseStale. That is the reaper's second
// clause, and it is here for the same reason the reaper has it: a liveness file whose owner
// cannot be checked is not thereby a dead owner.
func (l JobLease) Live(now time.Time) bool {
	if !l.Known {
		return now.Sub(l.Beat) < JobLeaseStale
	}
	host, _ := os.Hostname()
	if l.Host != "" && l.Host != host {
		return now.Sub(l.Beat) < JobLeaseStale
	}
	return Alive(l.PID, "")
}

// JobLeaseHeldError is the refusal a take gets when a run already holds the job directory.
// It carries the holder so the caller's one refusal line can name it.
type JobLeaseHeldError struct {
	Dir    string
	Holder JobLease
}

func (e *JobLeaseHeldError) Error() string {
	return fmt.Sprintf("the job directory %s is held by a live run (%s); two runs in one job directory share one data home, one temp directory and one set of logs, and the first to end removes the other's lease",
		e.Dir, e.Holder)
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

// ReadJobLease reads the lease in jobDir. A job directory with no lease answers an error
// satisfying errors.Is(err, os.ErrNotExist). A path that is THERE but is not a regular file
// -- a directory, a symlink, a socket -- is an error of its own: this process cannot tell
// whether that job directory is held, and rule 3 says it must not guess.
func ReadJobLease(jobDir string) (JobLease, error) {
	path := filepath.Join(jobDir, JobLeaseName)
	st, err := os.Lstat(path)
	if err != nil {
		return JobLease{}, err
	}
	if !st.Mode().IsRegular() {
		return JobLease{}, fmt.Errorf("%s is not a regular file (mode %s), so this run cannot read who holds this job directory", path, st.Mode())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return JobLease{}, err
	}
	lease := JobLease{Path: path, Beat: st.ModTime()}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "pid":
			if n, err := strconv.Atoi(value); err == nil && n > 0 {
				lease.PID, lease.Known = n, true
			}
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
// It REFUSES rather than returning a release that protects nothing. A *JobLeaseHeldError
// says another run holds the directory -- the same-path exclusion of issue #1585 -- and any
// other error says this run could not establish ownership at all, which under rule 3 is the
// same refusal with a different reason. The caller exits on either, before it has written
// anything the holder owns.
//
// The release is safe to call more than once. It stops the heartbeat, WAITS for it, and
// then removes the file only while the file is still this run's -- pid and nonce both.
func StartJobLease(jobDir, label string) (release func(), err error) {
	return startJobLeaseEvery(jobDir, label, JobLeaseHeartbeat)
}

func startJobLeaseEvery(jobDir, label string, every time.Duration) (func(), error) {
	t := time.NewTicker(every)
	return startJobLeaseTicking(jobDir, label, t.C, t.Stop, jobLeaseHooks{})
}

// jobLeaseHooks are barriers a TEST holds this machinery at, so that a beat and a release
// can be put in a known order with no duration chosen anywhere. Both are nil in the binary.
type jobLeaseHooks struct {
	atBeat      chan<- struct{} // the heartbeat waits here at the START of a beat, before its work
	afterRemove chan<- struct{} // the release waits here AFTER it has removed the lease
}

// startJobLeaseTicking is the whole of it, with the heartbeat's clock handed in.
func startJobLeaseTicking(jobDir, label string, ticks <-chan time.Time, stopTicks func(), hooks jobLeaseHooks) (func(), error) {
	path := filepath.Join(jobDir, JobLeaseName)
	host, _ := os.Hostname()
	nonce := newLeaseNonce()
	body := fmt.Sprintf("pid=%d\nhost=%s\nlabel=%s\nnonce=%s\nstarted=%s\n",
		os.Getpid(), host, label, nonce, time.Now().UTC().Format(time.RFC3339))

	if err := publishJobLease(path, body); err != nil {
		stopTicks()
		return func() {}, err
	}

	done := make(chan struct{})
	var beating sync.WaitGroup
	beating.Add(1)
	go func() {
		defer beating.Done()
		defer stopTicks()
		for {
			select {
			case <-done:
				return
			case now, ok := <-ticks:
				if !ok {
					return
				}
				if hooks.atBeat != nil {
					hooks.atBeat <- struct{}{}
				}
				if err := os.Chtimes(path, now, now); err == nil {
					continue
				}
				// THE REPAIR. Chtimes on a path that is not there does nothing and says
				// nothing, which is how a live job lost its protection in #1585. Publish
				// the same lease again: if somebody else now holds the path, this is
				// refused and their lease is left exactly alone.
				_ = publishJobLease(path, body)
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			// THE JOIN, before the remove. A tick already selected can be inside its own
			// repair; without this wait it would publish the lease again AFTER the run
			// that owned it had ended, and leave a finished job looking alive.
			close(done)
			beating.Wait()
			releaseJobLeaseIfOurs(path, os.Getpid(), nonce)
			if hooks.afterRemove != nil {
				hooks.afterRemove <- struct{}{}
			}
		})
	}, nil
}

// publishJobLease puts a COMPLETE lease at path and answers nil only when this call now owns
// it. A *JobLeaseHeldError means somebody else does; any other error means this process
// could not establish ownership and the caller must refuse (rule 3).
func publishJobLease(path, body string) error {
	dir := filepath.Dir(path)
	for attempt := 0; attempt < jobLeaseAttempts; attempt++ {
		tmp, err := writeJobLeaseTemp(dir, body)
		if err != nil {
			return err
		}
		// THE PUBLICATION IS THE LINK. It fails if the name exists, which makes it the
		// atomic claim, and what it puts there is a record that was already whole and
		// already on the platform -- so no reader ever sees a partial lease (rule 1).
		linkErr := os.Link(tmp, path)
		_ = os.Remove(tmp)
		if linkErr == nil {
			return nil
		}
		if !errors.Is(linkErr, os.ErrExist) {
			return fmt.Errorf("the job lease %s could not be published: %w", path, linkErr)
		}

		held, rerr := ReadJobLease(dir)
		if rerr != nil {
			if errors.Is(rerr, os.ErrNotExist) {
				continue // it went away between the link and the read; claim it again
			}
			return fmt.Errorf("the job lease %s is there and cannot be read, so this run cannot tell whether the job directory is held: %w", path, rerr)
		}
		if held.Live(time.Now()) {
			return &JobLeaseHeldError{Dir: dir, Holder: held}
		}
		// A dead holder's lease is not a lease. Remove exactly it -- the same record this
		// call just read, by pid and nonce -- and claim the path again.
		if !releaseJobLeaseIfOurs(path, held.PID, held.Nonce) {
			return fmt.Errorf("the job lease %s names a holder that is gone (%s) and it could not be cleared, so this run cannot take the job directory", path, held)
		}
	}
	return fmt.Errorf("the job lease %s could not be taken in %d attempts; another run is claiming and clearing it, and this run will not start without proving it owns its job directory", path, jobLeaseAttempts)
}

// writeJobLeaseTemp writes the whole record to a file beside the lease and flushes it, so
// that what the link publishes is complete on disk and not only in this process.
func writeJobLeaseTemp(dir, body string) (string, error) {
	tmp := filepath.Join(dir, JobLeaseName+"."+newLeaseNonce()+".tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|ONoFollow, 0o644)
	if err != nil {
		return "", fmt.Errorf("the job lease could not be written under %s: %w", dir, err)
	}
	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("the job lease could not be written under %s: %w", dir, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("the job lease could not be flushed under %s: %w", dir, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("the job lease could not be closed under %s: %w", dir, err)
	}
	return tmp, nil
}

// releaseJobLeaseIfOurs removes the lease only while the file on disk still names pid and
// nonce. There is no atomic compare-and-unlink on these systems, so this is a read and then
// a remove: the window is two system calls wide, and what it buys is that a release arriving
// after ANOTHER run has taken the path -- the whole of #1585 -- removes nothing. It answers
// whether the path is now free of that record.
func releaseJobLeaseIfOurs(path string, pid int, nonce string) bool {
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
