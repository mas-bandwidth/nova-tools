package main

import (
	"bytes"
	"strings"
	"testing"
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
	failCmd       string // a command prefix that exits 1
}

func (w revertWorld) answer(t *testing.T) func(c cmdSpec) (string, int, error) {
	return func(c cmdSpec) (string, int, error) {
		line := strings.Join(append([]string{c.Name}, c.Args...), " ")
		if w.failCmd != "" && strings.HasPrefix(line, w.failCmd) {
			return "", 1, nil
		}
		switch {
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
	code, out, errb, r := runRevert(t, defaultWorld(), "--run-attempt", "2")
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
	code, out, _, r := runRevert(t, w, "--run-attempt", "2")
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
	_, _, _, r := runRevert(t, w, "--run-attempt", "2")
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
	code, out, _, r := runRevert(t, w, "--run-attempt", "2")
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
	code, out, _, _ := runRevert(t, w, "--run-attempt", "2")
	if code != 0 || !strings.Contains(out, "PR already exists for revert/aaaaaaaaaaaa; reusing it.") || !strings.Contains(out, "revert PR #9 open on revert/aaaaaaaaaaaa") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}
