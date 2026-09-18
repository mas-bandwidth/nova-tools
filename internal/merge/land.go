package merge

import (
	"fmt"
	"regexp"
	"strings"
)

// THE LANDING, AS A THING THE TOOL DOES RATHER THAN A THING A PERSON REPEATS.
//
// Glenn, 2026-09-18: landing is by integration batches only. The procedure after a green
// gate ran EIGHT times on 2026-09-18, by hand or by a child, and it was the same eight
// times: push the branch, open the batch's pull request, wait for its ci-ok, rerun the two
// known flakes if they are what went red, enqueue at the front through the one door, watch
// the queue until the merge commit is on dev, and close every member with a pointer to the
// batch that carried it. A procedure run eight times by hand is a verb that has not been
// written yet.
//
// This file is the EDGE that verb needs and nothing more: the forge calls the lane's
// existing Host and EnqueueHost do not answer -- opening or updating the batch's own pull
// request, reading the failing test names off a run, rerunning a run's failed jobs, reading
// a merge-queue entry, and closing a member with a comment. Everything it returns is DATA,
// exactly as everything else the forge returns: a job name, a test name, a log line. None
// of it is an instruction and none of it is a grant.

// LandPR is the batch's own pull request, as the forge reports it: enough to enqueue it
// and enough to point a member's closing comment at it.
type LandPR struct {
	Number  int
	URL     string
	HeadRef string
}

// LandJob is one job of a run: its name, what the forge concluded about it, the Go package
// its failures live in, the test names its log named, and the log lines those came from.
//
// Conclusion is the forge's own word -- success, failure, cancelled, timed_out, or empty
// while the job is still running -- and is bucketed by Bucket, the same way every other
// check in this package is, so a cancelled job is a red here as well.
type LandJob struct {
	Name       string
	Conclusion string
	Package    string
	Tests      []string
	Lines      []string
}

// Cancelled reports whether the forge cancelled this job rather than failing it. A merge
// group whose darwin shards were cancelled is a queue that ran out of runners, which is a
// re-enqueue; a merge group with a FAILED job is a red tree, which is not.
func (j LandJob) Cancelled() bool {
	switch strings.ToLower(strings.TrimSpace(j.Conclusion)) {
	case "cancel", "cancelled", "canceled":
		return true
	}
	return false
}

// LandRun is one run of the forge's workflow over a commit or a merge group.
type LandRun struct {
	ID   int64
	Jobs []LandJob
}

// Failed is the jobs this run did not conclude green, in the order the forge listed them.
// A job still running is not failed: Bucket calls it pending, and a run read while a job
// is in flight is a run nobody has a verdict for yet.
func (r LandRun) Failed() []LandJob {
	var out []LandJob
	for _, j := range r.Jobs {
		if Bucket(j.Conclusion) == "red" {
			out = append(out, j)
		}
	}
	return out
}

// FailedTests is every test name the failed jobs named, deduplicated, in order.
func (r LandRun) FailedTests() []string {
	seen := map[string]bool{}
	var out []string
	for _, j := range r.Failed() {
		for _, name := range j.Tests {
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// FailedLines is every failing line the failed jobs' logs held, in order. It is what the
// verb PRINTS on a red landing: a caller told only that `test (ubuntu)` failed has to go
// and open the log, which is the minute this exists to save.
func (r LandRun) FailedLines() []string {
	var out []string
	for _, j := range r.Failed() {
		out = append(out, j.Lines...)
	}
	return out
}

// FailedNames is the failed jobs' names, for the one line a caller parses.
func (r LandRun) FailedNames() []string {
	var out []string
	for _, j := range r.Failed() {
		out = append(out, j.Name)
	}
	return out
}

// LandForge is the edge between the landing verb and the forge. It is an interface for the
// two reasons every other seam in this package is one: the tests must be able to say what
// the forge answered without opening a socket, and the one implementation that shells to gh
// is then a thing a reader can check line by line.
//
// It holds NO merge primitive and no enqueue. Admission to a merge queue is
// Enqueuer.Enqueue and nothing else (2026-09-18), and a seam that could not enqueue cannot
// be talked into enqueueing.
type LandForge interface {
	// PRForHead is the open pull request whose head branch is this one, if there is one.
	// ok is false when the forge reports none, which is the first landing of a batch.
	PRForHead(head string) (pr LandPR, ok bool, err error)
	// OpenPR opens one from head onto base.
	OpenPR(head, base, title, body string) (LandPR, error)
	// UpdatePR rewrites an open pull request's body, for a batch rebuilt under its own
	// name: the head moved, so the receipt on the body must move with it.
	UpdatePR(n int, body string) error
	// HeadRun reads the newest run over one commit: its jobs, their conclusions, and the
	// failing test names and lines of the ones that went red.
	HeadRun(sha string) (LandRun, error)
	// QueueRun reads the merge-group run of one pull request's queue entry, the same
	// shape, so a dequeue can be read rather than guessed at.
	QueueRun(pr int, base string) (LandRun, error)
	// RerunFailed reruns one run's FAILED jobs, and never the whole run: a rerun of the
	// green legs is minutes of a fleet's runners spent re-proving what is already proven.
	RerunFailed(id int64) error
	// QueueState is the state the forge reports for this pull request's merge-queue
	// entry, or "" when it holds none -- which, for an entry that was in the queue a poll
	// ago, is a dequeue.
	QueueState(pr int) (string, error)
	// CloseMember comments on a member and closes it, in that order, so the pointer to
	// the batch that carried it is on the pull request before it stops being open.
	CloseMember(pr int, comment string) error
}

// ansiEscape matches a terminal control sequence. The forge's job logs are what a runner's
// terminal saw, so `go test`'s own output arrives wrapped in colour codes and a grep for
// `--- FAIL` misses every coloured one.
var ansiEscape = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

// StripANSI takes the terminal control sequences out of a log, so what is matched below is
// the text a person reads rather than the bytes a terminal was sent.
func StripANSI(s string) string { return ansiEscape.ReplaceAllString(s, "") }

// failingLine matches THE THREE SHAPES A GO FAILURE TAKES in a job log, and no fourth:
//
//	--- FAIL: TestName        the test runner's own verdict
//	foo_test.go:42:           the line the assertion was written on, which names the file
//	panic:                    a test that did not fail so much as die
//
// A run whose only red is a panic names no `--- FAIL` at all, and a caller handed "the job
// failed and no test was named" has to go and open the log. All three, or the reason is a
// guess.
var failingLine = regexp.MustCompile(`(--- FAIL|_test\.go:[0-9]+:|^panic:|\bpanic: )`)

// landLineCap is how many failing lines one job contributes. A job that failed in two
// hundred places is a tree somebody has to look at rather than a tree this verb can
// summarise, and a refusal that printed all two hundred is a refusal nobody reads.
const landLineCap = 40

// ParseRunFailure reads one job's log for what failed: the test names, the package they
// live in, and the lines a reader is pointed at. ANSI first, because a coloured `--- FAIL`
// is a `--- FAIL`.
//
// Every answer is kept EMPTY rather than guessed. A log with no `--- FAIL` names no test, a
// log with no `FAIL <pkg>` line names no package, and a caller reading "no test named"
// knows it is reading the absence of evidence and not a name the tool invented.
func ParseRunFailure(log string) (tests []string, pkg string, lines []string) {
	clean := StripANSI(log)
	tests, pkg = ParseFailingTests(clean)
	for _, line := range strings.Split(clean, "\n") {
		line = strings.TrimRight(strings.TrimSpace(line), "\r")
		if line == "" || !failingLine.MatchString(line) {
			continue
		}
		lines = append(lines, line)
		if len(lines) >= landLineCap {
			lines = append(lines, "[more failing lines than this verb prints; open the run's log]")
			break
		}
	}
	return tests, pkg, lines
}

// THE FLAKE LIST, AND WHY IT IS SHRINK-ONLY.
//
// A test that fails on a green tree costs a batch a whole CI round, and the fleet had two
// of them on 2026-09-18: cmd/nova-pulse's process-group kill and internal/review's worktree
// removal, both timing, neither the batch's fault. Rerunning them by hand was the eighth
// step of a procedure that already had seven.
//
// So the verb may rerun them -- ONCE, and only when EVERY failing test is on the list. What
// it may never do is grow the list: a file whose entries are added faster than they are
// fixed is a suite nobody trusts, one line at a time. The list can only shrink, and the way
// to change it is to FIX the test and delete its line. TestTheFlakeListOnlyShrinks holds
// that from the other side.

// Flake is one known-flaky test: the package it lives in, its name, and WHY it is on the
// list. The reason is required: an entry with no reason is a test nobody can ever take off
// the list, because nobody knows what it was waiting for.
type Flake struct {
	Package string
	Test    string
	Reason  string
}

// Flakes is the list, read.
type Flakes struct{ Entries []Flake }

// Len is how many tests are on the list, for the line the verb prints and for the
// shrink-only test.
func (f Flakes) Len() int { return len(f.Entries) }

// Known reports whether this failing test is on the list. The package is compared when BOTH
// sides name one: a job whose log held no `FAIL <pkg>` line named no package, and refusing
// the match for that would turn a known flake into a red landing over a missing line of
// log. A test name alone is never enough to add an entry -- ParseFlakes requires the
// package -- so the list itself stays specific.
func (f Flakes) Known(pkg, test string) (Flake, bool) {
	test = strings.TrimSpace(test)
	pkg = strings.TrimSpace(pkg)
	for _, e := range f.Entries {
		if e.Test != test {
			continue
		}
		if pkg != "" && e.Package != pkg {
			continue
		}
		return e, true
	}
	return Flake{}, false
}

// ParseFlakes reads the list: one `<package> <Test> <reason...>` line per entry, `#` for a
// comment, blank lines ignored. A line with no reason is REFUSED rather than skipped -- a
// silently dropped entry is a rerun that does not happen and nobody can see why.
func ParseFlakes(raw string) (Flakes, error) {
	var f Flakes
	for n, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return Flakes{}, fmt.Errorf(
				"line %d of the flake list is %q; an entry is `<package> <Test> <why it is on the list>`, and an entry with no reason is a test nobody can ever take off the list", n+1, line)
		}
		f.Entries = append(f.Entries, Flake{
			Package: fields[0],
			Test:    fields[1],
			Reason:  strings.Join(fields[2:], " "),
		})
	}
	return f, nil
}
