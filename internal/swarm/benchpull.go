package swarm

// The pull: what comes back from a bench when a remote slot ends (SPEC-SWARM.md,
// "Benches" -- "Three files come back after the run ... into the local root at
// <root>/<bench>-<n>/jobs/<label>/: RESULT.md, usage.tsv, and the last 64 KiB of
// native.log").
//
// It is written from a bench run that LOST its results on 2026-09-15, and every rule below
// is one of the ways that run lost them:
//
//   - The remote slot's shell had returned but RESULT.md was not on disk yet, so a pull
//     that copied immediately copied nothing. The pull waits for the file, bounded.
//   - The copy was one rsync with an include filter, and a filter that matches nothing
//     succeeds: rsync exited 0, nothing came back, and the batch read a missing file as a
//     card that abstained. Each file is its own explicit scp, so a file that did not come
//     back is a copy that says so.
//   - The card had written RESULT.md inside the repository it cloned, not in the job
//     directory, so a pull that looked only at the job found nothing that was plainly
//     there. The pull looks under repo/ and one level below it, copies the file up, and
//     prints the line that says it did -- the card is still wrong, and a person reading
//     the packet is told where the file was found rather than told nothing came back.
//   - A bench that could not be reached at all scored like a card that abstained on its
//     own work. It is its own reason token now: bench-unreachable.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// pullResultWait is how long the pull waits for RESULT.md to appear on the bench after the
// remote slot's ssh has returned. The spec's own retry at gather is 30 s, and this is that
// number: long enough for a file the harness has just written to land, short enough that a
// card which wrote none does not hold the batch.
const pullResultWait = 30 * time.Second

// pullPollInterval is how often the wait asks. One ssh per second for at most thirty is
// inside the bench cost the spec names.
const pullPollInterval = time.Second

// pullLogTail is how much of native.log comes back: the last 64 KiB, as the spec says.
const pullLogTail = 64 * 1024

// errBenchUnreachable is what a pull returns when the bench itself could not be reached --
// as opposed to a bench that answered and holds no RESULT.md, which is an ordinary
// no-result abstain and not this.
var errBenchUnreachable = errors.New("bench unreachable")

// pullClock is the pull's view of time: the deadline it reads and the poll it waits
// between asks. It is a seam so a test runs the bounded wait to its end with no wall
// time; realClock is the production one and nil takes it.
type pullClock interface {
	Now() time.Time
	Sleep(time.Duration)
}

// benchPull is one card's pull: the bench it ran on, the job directory there, and the job
// directory here.
type benchPull struct {
	host      string        // the ssh alias
	remoteJob string        // <root>/<n>/jobs/<label> on the bench
	localJob  string        // <root>/<bench>-<n>/jobs/<label> here
	label     string        // the card's label, named on the copied-up note
	wait      time.Duration // how long to wait for RESULT.md to exist remotely
	poll      time.Duration // how often to ask
	notes     io.Writer     // where BATCH NOTE lines go
	clock     pullClock     // nil means the real clock
}

// pullFromBench waits for the card's RESULT.md, copies the three files back by one explicit
// scp each, and returns errBenchUnreachable when the bench could not be reached at all.
// A bench that answers and holds no result is not an error here: the gather scores that as
// the missing result it is.
func pullFromBench(p benchPull) error {
	if p.wait == 0 {
		p.wait = pullResultWait
	}
	if p.poll == 0 {
		p.poll = pullPollInterval
	}
	clk := p.clock
	if clk == nil {
		clk = realClock{}
	}
	if err := os.MkdirAll(p.localJob, 0o755); err != nil {
		return err
	}
	result := p.remoteJob + "/RESULT.md"
	found, err := waitForRemoteFile(p.host, result, p.wait, p.poll, clk)
	if err != nil {
		return err
	}
	if !found {
		// The card wrote its result somewhere else. Under repo/, or one level below it, is
		// where it has actually been found; anywhere else is not guessed at.
		if from, err := findResultUnderRepo(p.host, p.remoteJob); err != nil {
			return err
		} else if from != "" {
			if err := sshRun(p.host, "cp", from, result); err != nil {
				return err
			}
			if p.notes != nil {
				fmt.Fprintf(p.notes, "BATCH NOTE %s RESULT.md copied up from %s\n", oneline.Field(p.label), oneline.Field(from))
			}
			found = true
		}
	}
	// ONE SCP PER FILE, never a filter. RESULT.md is the file whose absence changes the
	// card's score, so its copy is the only one whose failure is the pull's failure; the
	// other two are absent on plenty of honest runs and their copy says so by the file not
	// being here.
	if found {
		if err := scpFile(p.host, result, filepath.Join(p.localJob, "RESULT.md")); err != nil {
			return err
		}
	}
	_ = scpFile(p.host, p.remoteJob+"/usage.tsv", filepath.Join(p.localJob, "usage.tsv"))
	log := filepath.Join(p.localJob, "native.log")
	if err := scpFile(p.host, p.remoteJob+"/native.log", log); err == nil {
		if err := truncateToTail(log, pullLogTail); err != nil {
			return err
		}
	}
	return nil
}

// waitForRemoteFile asks the bench whether a file is there, once per poll, until it is or
// the wait runs out. A bench that cannot be reached is errBenchUnreachable; one that answers
// "no file" for the whole wait is (false, nil), which is a card with no result and not a
// broken bench.
//
// ssh's own exit 255 is the first ask's answer and the last: a host that could not be
// reached -- a name that does not resolve, a refused connection, a rejected key -- will not
// come up in the next second, so the wait answers immediately rather than spending the whole
// window on a poll that cannot change. That is also what keeps the test that drives it off
// the machine's clock: the one place a real wait is the answered-but-not-yet-written file,
// and there the clock is injected.
func waitForRemoteFile(host, path string, wait, poll time.Duration, clk pullClock) (bool, error) {
	deadline := clk.Now().Add(wait)
	for {
		err := sshRun(host, "test", "-f", path)
		if err == nil {
			return true, nil
		}
		if isUnreachable(err) {
			return false, errBenchUnreachable
		}
		// The bench answered: the file is not there yet.
		if !clk.Now().Before(deadline) {
			return false, nil
		}
		clk.Sleep(poll)
	}
}

// findResultUnderRepo looks for the result the card wrote in the wrong place: inside the
// repository it cloned, or one directory below that. It returns the first path found, or
// the empty string when there is none.
func findResultUnderRepo(host, job string) (string, error) {
	// ssh joins its arguments into one command line for the remote shell, so the globs and
	// the redirect are the remote shell's: a `ls` of two patterns, quiet about the ones
	// that match nothing.
	out, err := sshOutput(host, "ls", "-1",
		job+"/repo/RESULT.md", job+"/repo/*/RESULT.md", "2>/dev/null")
	if err != nil && isUnreachable(err) {
		return "", errBenchUnreachable
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, "/RESULT.md") {
			return line, nil
		}
	}
	return "", nil
}

// scpFile copies one remote file to one local path. One file, named on both sides: no
// recursion, no include filter, nothing that can succeed while copying nothing.
func scpFile(host, remote, local string) error {
	cmd := exec.Command("scp", host+":"+remote, local)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("scp %s: %s", remote, strings.TrimSpace(string(out)))
	}
	return nil
}

func sshRun(host string, args ...string) error {
	cmd := exec.Command("ssh", append([]string{host}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return &sshError{code: exitCodeOf(err), out: strings.TrimSpace(string(out)), err: err}
	}
	return nil
}

func sshOutput(host string, args ...string) (string, error) {
	cmd := exec.Command("ssh", append([]string{host}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return string(out), &sshError{code: exitCodeOf(err), err: err}
	}
	return string(out), nil
}

// sshError carries the exit code, because 255 is ssh's own: it is how ssh says it could
// not reach the host, and every other code is the remote command's own answer.
type sshError struct {
	code int
	out  string
	err  error
}

func (e *sshError) Error() string {
	if e.out != "" {
		return fmt.Sprintf("ssh exit %d: %s", e.code, e.out)
	}
	return fmt.Sprintf("ssh exit %d", e.code)
}

func exitCodeOf(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// isUnreachable reports whether an ssh failure was ssh's own rather than the remote
// command's: exit 255 is ssh saying it could not reach the host, and -1 is ssh not being
// runnable here at all.
func isUnreachable(err error) bool {
	var se *sshError
	if errors.As(err, &se) {
		return se.code == 255 || se.code < 0
	}
	return errors.Is(err, errBenchUnreachable)
}

// truncateToTail keeps the last n bytes of a file, which is how the log comes back bounded
// without asking the bench to cut it.
func truncateToTail(path string, n int64) error {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= n {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(info.Size()-n, io.SeekStart); err != nil {
		return err
	}
	tail, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	return os.WriteFile(path, tail, 0o644)
}
