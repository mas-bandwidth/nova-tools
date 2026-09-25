package card

// wrapper_commit.go is the commit step (#2932 rev 4, replaces the bash
// harvest's commit_step): wrapper step 6, before copyOut, commits
// <job>/out/repo onto the attempt's branch nova/<S>/<label>-a<attempt>, and
// end.record carries that commit as pushed_sha. Harvest never commits; it
// pushes this commit from the results dir.
//
//   - The message is the harness's RESULT line 1; the author and committer
//     are `nova-card <bench>` (no person).
//   - Card scratch (ScratchFiles) is left out of the commit, wherever it sits.
//   - A changed file over MaxCommitFile means no commit: pushed_sha "-" and
//     the wrapper line says OVERSIZE <file>.
//   - A native run (nova-swarm native) clones in its own job directory, not
//     under out; with NOVA_CARD_OUT in its environment it hands that repo and
//     the card's RESULT.md to out (swarm.HandOffCardOut), so this step has one
//     place to look whichever runner the bench used.
//   - No out/repo, or nothing to commit, is pushed_sha "-" (NO-COMMIT): the
//     card's friend read is the report rule (#3036), never harvest. A code
//     card whose model said DONE and committed nothing ends FAILED reason
//     no-commit (NoCommitEnd), in done/fail, never a done/ok nobody harvests.
//   - There is no rebase and no mirror refresh: a moved base is the lander's
//     update-branch (#3139).

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// MaxCommitFile is the largest file the commit step commits (1 MB).
const MaxCommitFile = 1 << 20

// ScratchFiles are the card's own files, never committed.
var ScratchFiles = []string{"RESULT.md", "notes.txt", "REPORT.md", "usage.tsv", "harness-output.log", "repo.bundle"}

// NoCommit is pushed_sha for a card that committed nothing.
const NoCommit = "-"

// CommitResult is what the commit step did: SHA is the commit (or "-"), Note
// is COMMITTED, NO-COMMIT or OVERSIZE <file>.
type CommitResult struct {
	SHA  string
	Note string
}

// CommitOutput commits the uncommitted work in repo onto branch with message,
// authored `nova-card <bench>`. A repo with no .git is NO-COMMIT.
func CommitOutput(repo, branch, message, bench string) (CommitResult, error) {
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		return CommitResult{SHA: NoCommit, Note: "NO-COMMIT"}, nil
	}
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=nova-card", "GIT_AUTHOR_EMAIL="+bench,
		"GIT_COMMITTER_NAME=nova-card", "GIT_COMMITTER_EMAIL="+bench,
		"GIT_TERMINAL_PROMPT=0")
	git := func(args ...string) (string, error) {
		cmd := exec.Command("git", append([]string{"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = repo
		cmd.Env = env
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
		}
		return strings.TrimSpace(out.String()), nil
	}
	pathspec := []string{"--", "."}
	for _, f := range ScratchFiles {
		pathspec = append(pathspec, ":(exclude,glob)**/"+f)
	}
	if _, err := git(append([]string{"add", "-A"}, pathspec...)...); err != nil {
		return CommitResult{}, err
	}
	staged, err := git("diff", "--cached", "--name-only", "-z", "--no-renames", "--diff-filter=ACMRT")
	if err != nil {
		return CommitResult{}, err
	}
	for _, f := range strings.Split(staged, "\x00") {
		if f == "" {
			continue
		}
		if fi, err := os.Lstat(filepath.Join(repo, filepath.FromSlash(f))); err == nil && fi.Mode().IsRegular() && fi.Size() > MaxCommitFile {
			_, _ = git("reset", "-q")
			return CommitResult{SHA: NoCommit, Note: "OVERSIZE " + f}, nil
		}
	}
	if _, err := git("checkout", "-q", "-B", branch); err != nil {
		return CommitResult{}, err
	}
	if _, err := git("diff", "--cached", "--quiet"); err != nil {
		if strings.TrimSpace(message) == "" {
			message = "nova-card " + branch
		}
		if _, err := git("commit", "-q", "--no-verify", "-m", message); err != nil {
			return CommitResult{}, err
		}
	} else if ahead, err := git("rev-list", "--count", "HEAD", "--not", "--remotes"); err != nil || ahead == "0" {
		// Nothing staged and nothing the harness committed that no remote has.
		return CommitResult{SHA: NoCommit, Note: "NO-COMMIT"}, nil
	}
	sha, err := git("rev-parse", "HEAD")
	if err != nil {
		return CommitResult{}, err
	}
	return CommitResult{SHA: sha, Note: "COMMITTED"}, nil
}

// NoCommitWhy is the end's why for a code card that said DONE and committed
// nothing.
const NoCommitWhy = "model said DONE but committed nothing (NO-COMMIT)"

// NoCommitKinds are the card kinds that legitimately commit nothing: a
// script card, a read and a report. Every other kind (fix, recut, port,
// docs-guard, and a model card with no KIND) is a code card.
var NoCommitKinds = []string{KindScript, typedrec.KindRead, typedrec.KindReport}

// CodeKind reports whether a card of kind must commit to be done.
func CodeKind(kind string) bool {
	for _, k := range NoCommitKinds {
		if k == kind {
			return false
		}
	}
	return true
}

// NoCommitEnd is the end step's no-commit rule (quack-0925d, 2026-09-25): a
// code card the harness ended DONE, whose commit step found nothing to commit
// (NO-COMMIT) and whose model's line 2 in <out>/RESULT.md is DONE, ends
// FAILED reason no-commit with NoCommitWhy, so ns_card_end moves it to
// done/fail in the same call. The model's RESULT.md (its DONE line) is left
// as it is, for the result record. A card whose model said ABSTAIN or
// BLOCKED, wrote no line 2, or is not a code card, or a commit step that
// committed (or refused OVERSIZE), is returned unchanged with ok false.
func NoCommitEnd(kind, out string, end WrapperEnd) (WrapperEnd, bool) {
	if end.Outcome != "DONE" || end.Commit != "NO-COMMIT" || !CodeKind(kind) {
		return end, false
	}
	raw, err := os.ReadFile(filepath.Join(out, "RESULT.md"))
	if err != nil || typedrec.SplitModel(raw, kind).Status != typedrec.StatusDone {
		return end, false
	}
	end.Outcome, end.Reason, end.Why = "FAILED", "no-commit", NoCommitWhy
	return end, true
}
