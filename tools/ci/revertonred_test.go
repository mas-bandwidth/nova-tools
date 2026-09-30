package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	redHead   = "aaaaaaaaaaaa1111111111111111111111111111"
	redParent = "bbbbbbbbbbbb2222222222222222222222222222"
)

// revertWorld is a scripted git and gh: each field is what that command answers,
// and a command the world does not answer by name fails the test, so a verb that
// reaches for something new is seen.
type revertWorld struct {
	subject, body string
	originMain    string
	parents       string // the line `git rev-list --parents -n 1` prints
	parentRuns    string // the JSON of the ci runs page
	jobs          string // the JSON of the failed run's jobs
	pulls         string // the JSON of the head commit's pull requests
	pushMainCode  int
	pushBranchRC  int
	prCreateCode  int
	rerunCode     int
	issueCode     int
	badJSON       string // a command prefix that answers exit 0 with text that is no JSON
	failCmd       string // a command prefix that exits 1
}

func (w revertWorld) answer(t *testing.T) func(c cmdSpec) (string, int, error) {
	return func(c cmdSpec) (string, int, error) {
		line := strings.Join(append([]string{c.Name}, c.Args...), " ")
		if w.failCmd != "" && strings.HasPrefix(line, w.failCmd) {
			return "", 1, nil
		}
		if w.badJSON != "" && strings.HasPrefix(line, w.badJSON) {
			return "<html>502</html>", 0, nil
		}
		switch {
		case strings.HasPrefix(line, "gh issue create"):
			return "https://github.com/o/r/issues/99\n", w.issueCode, nil
		case strings.HasPrefix(line, "git log -1 --format=%s"):
			return w.subject + "\n", 0, nil
		case strings.HasPrefix(line, "git log -1 --format=%B"):
			return w.body + "\n\n", 0, nil
		case line == "git fetch origin main":
			return "", 0, nil
		case line == "git rev-parse origin/main":
			return w.originMain + "\n", 0, nil
		case line == "git rev-parse --verify "+redHead+"^":
			return redParent + "\n", 0, nil
		case strings.HasPrefix(line, "gh api repos/o/r/actions/workflows/ci.yml/runs?"):
			return w.parentRuns, 0, nil
		case strings.HasPrefix(line, "gh api repos/o/r/actions/runs/77/jobs"):
			return w.jobs, 0, nil
		case strings.HasPrefix(line, "git config user."):
			return "", 0, nil
		case strings.HasPrefix(line, "git rev-list --parents"):
			return w.parents + "\n", 0, nil
		case strings.HasPrefix(line, "git revert"), strings.HasPrefix(line, "git commit --amend"):
			return "", 0, nil
		case line == "git rev-parse HEAD":
			return "cccccccccccc3333333333333333333333333333\n", 0, nil
		case line == "git push origin HEAD:main":
			return "remote: refused\n", w.pushMainCode, nil
		case strings.HasPrefix(line, "git branch -f"):
			return "", 0, nil
		case strings.HasPrefix(line, "git push"):
			return "", w.pushBranchRC, nil
		case strings.HasPrefix(line, "gh pr create"):
			return "", w.prCreateCode, nil
		case strings.HasPrefix(line, "gh pr view"):
			return "9\n", 0, nil
		case strings.HasPrefix(line, "gh api repos/o/r/commits/"):
			return w.pulls, 0, nil
		case strings.HasPrefix(line, "gh api repos/o/r/issues/"):
			return "", 0, nil
		case strings.HasPrefix(line, "gh run rerun"):
			return "", w.rerunCode, nil
		}
		t.Errorf("unscripted command: %s", line)
		return "", 1, nil
	}
}

func defaultWorld() revertWorld {
	return revertWorld{
		subject:    "Merge pull request #5 from a/b",
		body:       "Merge pull request #5 from a/b\n\nthe change",
		originMain: redHead,
		parents:    redHead + " " + redParent + " dddd",
		parentRuns: `{"workflow_runs":[{"head_sha":"other","conclusion":"failure"},{"head_sha":"` + redParent + `","conclusion":"success"}]}`,
		jobs:       `{"jobs":[{"name":"lint","conclusion":"success"},{"name":"test (2)","conclusion":"failure"},{"name":"lisp","conclusion":"skipped"},{"name":"e2e","conclusion":"cancelled"}]}`,
		pulls:      `[{"number":3,"merged_at":null},{"number":5,"merged_at":"2026-01-01T00:00:00Z"}]`,
	}
}

func revertEnv(attempt string) (env, *bytes.Buffer, *bytes.Buffer) {
	var out, errb bytes.Buffer
	vars := map[string]string{"GITHUB_REPOSITORY": "o/r", "GITHUB_TOKEN": "tok", "HEAD_SHA": redHead, "RUN_ID": "77"}
	return env{stdout: &out, stderr: &errb, getenv: func(k string) string { return vars[k] }}, &out, &errb
}

func runRevert(t *testing.T, w revertWorld, args ...string) (int, string, string, *fakeCmdRunner) {
	t.Helper()
	e, out, errb := revertEnv("")
	r := &fakeCmdRunner{answer: w.answer(t)}
	code := revertOnRed(e, r, args)
	return code, out.String(), errb.String(), r
}

func TestRevertOnRedRevertsAMergeCommitAndPushesToMain(t *testing.T) {
	t.Parallel()
	code, out, errb, r := runRevert(t, defaultWorld(), "--run-attempt", "2", "--push-revert")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q\n%s", code, errb, out)
	}
	for _, want := range []string{
		"run 77 is red on attempt 2; the rerun is red too, proceeding to the revert.",
		"head=aaaaaaaaaaaa parent=bbbbbbbbbbbb",
		"merge commit: reverting with -m 1",
		"revert commit=cccccccccccc",
		"pushed revert cccccccccccc to main",
		"commented on #5 naming revert cccccccccccc",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	lines := strings.Join(r.lines(), "\n")
	for _, want := range []string{
		"git revert -m 1 --no-edit " + redHead,
		"git commit --amend -q -m revert aaaaaaaaaaaa: main red on test (2), e2e (mechanical revert-on-red; fix forward on a branch)",
		"git push origin HEAD:main",
		"gh api repos/o/r/issues/5/comments -f body=Main was red on test (2), e2e. Reverted by cccccccccccc (mechanical revert-on-red); fix forward on a branch.",
	} {
		if !strings.Contains(lines, want) {
			t.Errorf("commands lack %q:\n%s", want, lines)
		}
	}
	for _, c := range r.calls {
		if c.Name == "gh" && !strings.Contains(strings.Join(c.Env, " "), "GH_TOKEN=tok") {
			t.Errorf("gh ran without the token: %v", c.Args)
		}
	}
}

func TestRevertOnRedRevertsAPlainCommitWithoutM1(t *testing.T) {
	t.Parallel()
	w := defaultWorld()
	w.parents = redHead + " " + redParent
	code, out, _, r := runRevert(t, w, "--run-attempt", "2")
	if code != 0 || !strings.Contains(out, "plain commit: reverting") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !r.ran("git revert --no-edit "+redHead) || r.ran("git revert -m") {
		t.Fatalf("commands %q", r.lines())
	}
}

func TestRevertOnRedNeverRevertsARevert(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct{ subject, body string }{
		"capital":  {"Revert \"thing\"", "x"},
		"lower":    {"revert abc: main red", "x"},
		"its body": {"undo thing", "This reverts commit 123abc."},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := defaultWorld()
			w.subject, w.body = tc.subject, tc.body
			code, out, _, r := runRevert(t, w, "--run-attempt", "2")
			if code != 0 || !strings.Contains(out, "is itself a revert") || !strings.Contains(out, "no revert loops, skipping.") {
				t.Fatalf("exit %d\n%s", code, out)
			}
			if r.ran("git revert") || r.ran("git push") || r.ran("git fetch") {
				t.Fatalf("a revert was reverted: %q", r.lines())
			}
		})
	}
}

func TestRevertOnRedSkipsWhenMainMovedOn(t *testing.T) {
	t.Parallel()
	w := defaultWorld()
	w.originMain = "eeeeeeeeeeee5555"
	code, out, _, r := runRevert(t, w, "--run-attempt", "2")
	if code != 0 || !strings.Contains(out, "main has moved past aaaaaaaaaaaa (now eeeeeeeeeeee); the newer run decides, skipping.") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if r.ran("git revert") || r.ran("gh api") {
		t.Fatalf("commands %q", r.lines())
	}
}

func TestRevertOnRedSkipsWhenTheParentWasNotGreen(t *testing.T) {
	t.Parallel()
	for conclusion, want := range map[string]string{
		`"failure"`:   "ci run concluded failure; the red predates this push, skipping.",
		`"cancelled"`: "ci run concluded cancelled; the red predates this push, skipping.",
		`null`:        "ci run concluded null; the red predates this push, skipping.",
	} {
		t.Run(conclusion, func(t *testing.T) {
			t.Parallel()
			w := defaultWorld()
			w.parentRuns = `{"workflow_runs":[{"head_sha":"` + redParent + `","conclusion":` + conclusion + `}]}`
			code, out, _, r := runRevert(t, w, "--run-attempt", "2")
			if code != 0 || !strings.Contains(out, "::notice::parent bbbbbbbbbbbb "+want) {
				t.Fatalf("exit %d\n%s", code, out)
			}
			if r.ran("git revert") {
				t.Fatalf("reverted over a parent that was not green: %q", r.lines())
			}
		})
	}
}

func TestRevertOnRedSkipsWhenThereIsNoParentRunOnAnyOfFivePages(t *testing.T) {
	t.Parallel()
	w := defaultWorld()
	w.parentRuns = `{"workflow_runs":[{"head_sha":"someone-else","conclusion":"success"}]}`
	code, out, _, r := runRevert(t, w, "--run-attempt", "2")
	if code != 0 || !strings.Contains(out, "no ci push run found for parent bbbbbbbbbbbb; no green baseline, skipping.") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	pages := 0
	for _, l := range r.lines() {
		if strings.Contains(l, "workflows/ci.yml/runs?") {
			pages++
		}
	}
	if pages != 5 {
		t.Fatalf("looked at %d pages, want 5", pages)
	}
	if r.ran("git revert") {
		t.Fatal("reverted with no green baseline")
	}
}

func TestRevertOnRedWithNoJobNamedSaysSo(t *testing.T) {
	t.Parallel()
	w := defaultWorld()
	w.jobs = `{"jobs":[{"name":"lint","conclusion":"success"}]}`
	code, _, _, r := runRevert(t, w, "--run-attempt", "2")
	if code != 0 || !strings.Contains(strings.Join(r.lines(), "\n"), "main red on no failing job named (mechanical") {
		t.Fatalf("exit %d, commands %q", code, r.lines())
	}
}

func TestRevertOnRedLeavesARefusedPushAsAnOpenPullRequest(t *testing.T) {
	t.Parallel()
	w := defaultWorld()
	w.pushMainCode = 1
	code, out, _, r := runRevert(t, w, "--run-attempt", "2", "--push-revert")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{
		"remote: refused",
		"direct push refused by the ruleset; opening a revert PR for somebody to land",
		"revert PR #9 open on revert/aaaaaaaaaaaa",
		"::notice::revert PR #9 is OPEN on revert/aaaaaaaaaaaa and lands nothing by itself: main is red until somebody lands it -- merge it by hand.",
		"commented on #5 naming revert cccccccccccc",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if !r.ran("git branch -f revert/aaaaaaaaaaaa HEAD") || !r.ran("git push origin revert/aaaaaaaaaaaa") || !r.ran("gh pr create --base main --head revert/aaaaaaaaaaaa") {
		t.Fatalf("commands %q", r.lines())
	}
	if r.ran("git push -f") {
		t.Fatal("force-pushed a branch whose plain push worked")
	}
}

// The law the old script carried: CI lands nothing on its own. A revert pull
// request is left open, and no command the verb runs merges one or turns
// auto-merge on.
func TestRevertOnRedNeverMergesAndNeverEnablesAutoMerge(t *testing.T) {
	t.Parallel()
	w := defaultWorld()
	w.pushMainCode = 1
	_, _, _, r := runRevert(t, w, "--run-attempt", "2", "--push-revert")
	for _, l := range r.lines() {
		f := strings.Fields(l)
		if len(f) >= 3 && f[0] == "gh" && f[1] == "pr" && f[2] == "merge" {
			t.Errorf("merged a pull request: %s", l)
		}
		for _, a := range f {
			if a == "--auto" || strings.HasPrefix(a, "--auto=") || a == "--merge" || a == "--squash" {
				t.Errorf("enabled auto-merge: %s", l)
			}
		}
	}
}

func TestRevertOnRedForcesTheBranchOnlyAfterAPlainPushFailsAndFailsIfThatFailsToo(t *testing.T) {
	t.Parallel()
	w := defaultWorld()
	w.pushMainCode = 1
	w.pushBranchRC = 1
	w.prCreateCode = 1
	code, out, _, r := runRevert(t, w, "--run-attempt", "2", "--push-revert")
	if code != 1 {
		// both the plain and the forced push are scripted to fail
		t.Fatalf("exit %d, want 1 when even the forced push fails\n%s", code, out)
	}
	if !r.ran("git push -f origin revert/aaaaaaaaaaaa") {
		t.Fatalf("commands %q", r.lines())
	}
}

func TestRevertOnRedPostsNoCommentWhenNoMergedPullRequestIsFound(t *testing.T) {
	t.Parallel()
	w := defaultWorld()
	w.pulls = `[{"number":3,"merged_at":null}]`
	code, out, _, r := runRevert(t, w, "--run-attempt", "2")
	if code != 0 || !strings.Contains(out, "::notice::no merged PR found for aaaaaaaaaaaa; posting no comment.") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if r.ran("gh api repos/o/r/issues/") {
		t.Fatal("commented with no merged pull request")
	}
}

func TestRevertOnRedFirstAttemptRerunsTheFailedJobsOnceAndReverts(t *testing.T) {
	t.Parallel()
	code, out, _, r := runRevert(t, defaultWorld(), "--run-attempt", "1")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if got := r.lines(); len(got) != 1 || got[0] != "gh run rerun 77 --failed" {
		t.Fatalf("commands %q: the first red is only a rerun trigger", got)
	}
	if !strings.Contains(out, "::notice::run 77 failed on attempt 1; re-ran its failed jobs once (flake guard).") {
		t.Fatalf("stdout %q", out)
	}
}

func TestRevertOnRedFailedRerunIsAFailure(t *testing.T) {
	t.Parallel()
	w := defaultWorld()
	w.rerunCode = 1
	code, _, errb, _ := runRevert(t, w, "--run-attempt", "1")
	if code != 1 || !strings.Contains(errb, "gh run rerun 77 --failed exited 1") {
		t.Fatalf("exit %d stderr %q", code, errb)
	}
}

func TestRevertOnRedGitFailuresAreFailuresNotSkips(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{"git log", "git fetch", "git revert", "git commit --amend", "git config user.name", "git rev-list"} {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()
			w := defaultWorld()
			w.failCmd = cmd
			code, _, errb, _ := runRevert(t, w, "--run-attempt", "2")
			if code != 1 || !strings.Contains(errb, "revert-on-red:") {
				t.Fatalf("exit %d stderr %q", code, errb)
			}
		})
	}
}

func TestRevertOnRedRefusesWhatItIsNotGiven(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"GITHUB_REPOSITORY", "GITHUB_TOKEN", "HEAD_SHA", "RUN_ID"} {
		var out, errb bytes.Buffer
		e := env{stdout: &out, stderr: &errb, getenv: func(n string) string {
			if n == k {
				return ""
			}
			return "x"
		}}
		if code := revertOnRed(e, &fakeCmdRunner{}, nil); code != 2 || !strings.Contains(errb.String(), k+" is not set") {
			t.Errorf("%s unset: exit %d stderr %q", k, code, errb.String())
		}
	}
	e, _, errb := revertEnv("")
	if code := revertOnRed(e, &fakeCmdRunner{}, []string{"--bogus"}); code != 2 || !strings.Contains(errb.String(), `unknown argument "--bogus"`) {
		t.Errorf("unknown flag: exit %d stderr %q", code, errb)
	}
}

func TestShortSHAIsTwelveCharacters(t *testing.T) {
	t.Parallel()
	if got := shortSHA(redHead); got != "aaaaaaaaaaaa" {
		t.Fatalf("shortSHA = %q", got)
	}
	if got := shortSHA("abc"); got != "abc" {
		t.Fatalf("shortSHA(abc) = %q", got)
	}
}

func TestRevertOnRedReusesAPullRequestThatAlreadyExists(t *testing.T) {
	t.Parallel()
	w := defaultWorld()
	w.pushMainCode = 1
	w.prCreateCode = 1
	code, out, _, _ := runRevert(t, w, "--run-attempt", "2", "--push-revert")
	if code != 0 || !strings.Contains(out, "PR already exists for revert/aaaaaaaaaaaa; reusing it.") || !strings.Contains(out, "revert PR #9 open on revert/aaaaaaaaaaaa") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

// The law: a GitHub API call that fails is never read as "nothing found". The
// verb exits red and files ONE needs-owner issue, and for every lookup (which
// all come before the revert) it has pushed nothing to main and opened no PR.
func TestRevertOnRedAnAPIFailureIsRedAndFilesTheIssueAndPushesNothing(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		fail, bad string
		want      string
	}{
		"parent runs page 1 fails":      {fail: "gh api repos/o/r/actions/workflows/ci.yml/runs?branch=main&event=push&per_page=100&page=1", want: "ci.yml runs page 1 exited 1"},
		"parent runs page 3 fails":      {fail: "gh api repos/o/r/actions/workflows/ci.yml/runs?branch=main&event=push&per_page=100&page=3", want: "ci.yml runs page 3 exited 1"},
		"parent runs are no JSON":       {bad: "gh api repos/o/r/actions/workflows/", want: "printed no run list"},
		"failed run's jobs fail":        {fail: "gh api repos/o/r/actions/runs/77/jobs", want: "run 77 jobs exited 1"},
		"failed run's jobs are no JSON": {bad: "gh api repos/o/r/actions/runs/77/jobs", want: "printed no job list"},
		"merged PR lookup fails":        {fail: "gh api repos/o/r/commits/", want: "the pull requests of aaaaaaaaaaaa exited 1"},
		"merged PR lookup is no JSON":   {bad: "gh api repos/o/r/commits/", want: "printed no list"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := defaultWorld()
			w.failCmd, w.badJSON = tc.fail, tc.bad
			if name == "parent runs page 3 fails" {
				// pages 1 and 2 answer without naming the parent, so page 3 is reached
				w.parentRuns = `{"workflow_runs":[{"head_sha":"someone-else","conclusion":"success"}]}`
			}
			code, out, errb, r := runRevert(t, w, "--run-attempt", "2")
			assert.Equal(t, 1, code, "stderr %q stdout %q", errb, out)
			assert.Contains(t, errb, tc.want)
			assert.Contains(t, errb, "not deciding for a person")
			assert.NotContains(t, out, "skipping", "a failed lookup must never read as a skip")
			assert.Equal(t, 1, countPrefix(r, "gh issue create"), "one needs-owner issue: %q", r.lines())
			for _, forbidden := range []string{"git revert", "git push", "git branch", "gh pr create", "gh api repos/o/r/issues/"} {
				assert.False(t, r.ran(forbidden), "%s ran after a failed lookup: %q", forbidden, r.lines())
			}
			for _, c := range r.calls {
				if c.Name == "gh" && len(c.Args) > 1 && c.Args[0] == "issue" {
					line := strings.Join(c.Args, " ")
					assert.Contains(t, line, "--repo o/r")
					assert.Contains(t, line, "needs-owner: revert-on-red could not decide on aaaaaaaaaaaa")
					assert.Contains(t, line, tc.want)
				}
			}
		})
	}
}

func countPrefix(r *fakeCmdRunner, prefix string) int {
	n := 0
	for _, l := range r.lines() {
		if strings.HasPrefix(l, prefix) {
			n++
		}
	}
	return n
}

func TestRevertOnRedAFailedRerunFilesTheIssueToo(t *testing.T) {
	t.Parallel()
	w := defaultWorld()
	w.rerunCode = 1
	code, _, errb, r := runRevert(t, w, "--run-attempt", "1")
	assert.Equal(t, 1, code)
	assert.Contains(t, errb, "gh run rerun 77 --failed exited 1")
	assert.Equal(t, 1, countPrefix(r, "gh issue create"))
}

// The comment is the one call after the revert: a failure still files the issue
// and is red, and says the revert already landed.
func TestRevertOnRedAFailedCommentIsRedAndFilesTheIssue(t *testing.T) {
	t.Parallel()
	w := defaultWorld()
	w.failCmd = "gh api repos/o/r/issues/5/comments"
	code, out, errb, r := runRevert(t, w, "--run-attempt", "2", "--push-revert")
	assert.Equal(t, 1, code, "stdout %q", out)
	assert.Contains(t, errb, "commenting on #5 exited 1; the revert is already landed")
	assert.Equal(t, 1, countPrefix(r, "gh issue create"))
}

func TestRevertOnRedIsStillRedWhenTheIssueCannotBeFiledEither(t *testing.T) {
	t.Parallel()
	w := defaultWorld()
	w.failCmd = "gh api repos/o/r/actions/runs/77/jobs"
	w.issueCode = 1
	code, _, errb, r := runRevert(t, w, "--run-attempt", "2")
	assert.Equal(t, 1, code)
	assert.Contains(t, errb, "gh issue create exited 1; no needs-owner issue was filed")
	assert.False(t, r.ran("git revert"))
}

// The safe form, and the default: a still-red run opens a revert pull request and
// files the issue, and nothing is pushed to main.
func TestRevertOnRedByDefaultOpensAPullRequestAndPushesNothingToMain(t *testing.T) {
	t.Parallel()
	code, out, errb, r := runRevert(t, defaultWorld(), "--run-attempt", "2")
	require.Equal(t, 0, code, "stderr %q\n%s", errb, out)
	assert.False(t, r.ran("git push origin HEAD:main"), "pushed a revert to main without --push-revert: %q", r.lines())
	for _, l := range r.lines() {
		assert.False(t, strings.HasPrefix(l, "git push") && strings.Contains(l, "main") && !strings.Contains(l, "revert/"), "a push that names main: %s", l)
	}
	assert.True(t, r.ran("git branch -f revert/aaaaaaaaaaaa HEAD"))
	assert.True(t, r.ran("git push origin revert/aaaaaaaaaaaa"))
	assert.True(t, r.ran("gh pr create --base main --head revert/aaaaaaaaaaaa"), "%q", r.lines())
	assert.Equal(t, 1, countPrefix(r, "gh issue create"), "one needs-owner issue for the open revert PR: %q", r.lines())
	assert.Contains(t, out, "revert PR #9 open on revert/aaaaaaaaaaaa")
	assert.Contains(t, out, "commented on #5 naming revert cccccccccccc")
	for _, c := range r.calls {
		if c.Name == "gh" && len(c.Args) > 1 && c.Args[0] == "issue" {
			line := strings.Join(c.Args, " ")
			assert.Contains(t, line, "needs-owner: main is red at aaaaaaaaaaaa; revert PR #9 awaits landing")
			assert.Contains(t, line, "--repo o/r")
		}
		if c.Name == "gh" && len(c.Args) > 1 && c.Args[0] == "pr" && c.Args[1] == "create" {
			line := strings.Join(c.Args, " ")
			assert.Contains(t, line, "--title revert aaaaaaaaaaaa: main red on test (2), e2e (mechanical revert-on-red; fix forward on a branch)")
			assert.Contains(t, line, "ci failed on main at aaaaaaaaaaaa (run 77); reverted as cccccccccccc")
		}
	}
}

// The mutation of the default: with --push-revert the same run pushes to main,
// and opens no pull request and files no issue.
func TestRevertOnRedWithPushRevertPushesToMain(t *testing.T) {
	t.Parallel()
	code, out, errb, r := runRevert(t, defaultWorld(), "--run-attempt", "2", "--push-revert")
	require.Equal(t, 0, code, "stderr %q\n%s", errb, out)
	assert.True(t, r.ran("git push origin HEAD:main"), "%q", r.lines())
	assert.False(t, r.ran("gh pr create"))
	assert.False(t, r.ran("gh issue create"))
}

// A needs-owner issue that cannot be filed for the open revert PR is red: the
// open pull request with nobody told is exactly the silence the issue prevents.
func TestRevertOnRedIsRedWhenTheIssueForTheRevertPRCannotBeFiled(t *testing.T) {
	t.Parallel()
	w := defaultWorld()
	w.issueCode = 1
	code, _, errb, r := runRevert(t, w, "--run-attempt", "2")
	assert.Equal(t, 1, code)
	assert.Contains(t, errb, "no needs-owner issue was filed")
	assert.True(t, r.ran("gh pr create"))
}

// revert-on-red.yml never sets --push-revert: the direct path stays off until the
// pull request form has fired correctly once on a real red push.
func TestRevertOnRedWorkflowDoesNotSetPushRevert(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("../../.github/workflows/revert-on-red.yml")
	require.NoError(t, err)
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			continue
		}
		assert.NotContains(t, l, "push-revert", "revert-on-red.yml sets the direct-push flag")
	}
}
