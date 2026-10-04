package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func init() {
	register(verb{
		name:    "revert-on-red",
		summary: "the mechanical revert of the push that turned main red",
		help: `usage: go run ./tools/ci revert-on-red --run-attempt N [--push-revert]

Run by revert-on-red.yml on the checked-out head_sha of a failed ci workflow_run
on main. On the first attempt of the run it re-runs the run's failed jobs once (a
flake guard) and stops: the rerun's own completion re-enters the workflow with
run-attempt 2, and only a rerun that is red too is reverted.

Otherwise one of three guards stops it with a notice and exit 0 (a skip is not a
red; a guard decides only on what GitHub ANSWERED, never on a question it could
not ask):
  * the head commit is itself a revert        -> no revert loops
  * main has moved past this commit           -> the newer run decides
  * the parent commit's ci run was not green  -> the red predates this push
                                                 (no ci push run for it is the
                                                 same: no green baseline)
Then it reverts the push (git revert -m 1 for a merge commit, a plain revert
otherwise) and, BY DEFAULT, opens a revert/<sha> pull request against main, LEAVES
IT OPEN, and files ONE needs-owner issue naming it. It pushes nothing to main, enables
no auto-merge and lands nothing on its own: CI lands nothing by itself, a person
or a batch does. It posts ONE comment on the merged pull request, naming the
revert.

--push-revert is the direct path, off unless it is given: the verb pushes the
revert commit straight to main, and only when the ruleset refuses that push falls
back to the pull request above. revert-on-red.yml does not set it; it turns on
only after the pull request form has fired correctly once on a real red push.

A GitHub API call that fails (the parent's run lookup, the failed run's job
list, the merged pull request lookup, the comment, the rerun) is never a skip:
the verb exits 1 and files ONE needs-owner issue naming the call, before any
revert is pushed when the call comes before the revert (every lookup does), so
a person decides what a blind verb could not.

Environment: GITHUB_REPOSITORY, GITHUB_TOKEN, HEAD_SHA, RUN_ID (all required).
git and gh run in the working directory, which is the checkout of HEAD_SHA.

exit 0  reverted, or skipped by a guard, or rerun once
exit 1  a git or gh step failed (a gh failure also files the needs-owner issue),
        or the needs-owner issue for an open revert PR could not be filed
exit 2  usage, or a required variable is unset
`,
		do: func(e env, args []string) int { return revertOnRed(e, osCmdRunner{}, args) },
	})
}

const revertBotName = "github-actions[bot]"
const revertBotEmail = "41898282+github-actions[bot]@users.noreply.github.com"

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// revertOnRed is the verb over a runner.
func revertOnRed(e env, r cmdRunner, args []string) int {
	attempt, pushRevert := "", false
	for i := 0; i < len(args); i++ {
		if args[i] == "--run-attempt" && i+1 < len(args) {
			attempt = args[i+1]
			i++
			continue
		}
		if args[i] == "--push-revert" {
			pushRevert = true
			continue
		}
		fmt.Fprintf(e.stderr, "revert-on-red: unknown argument %q; usage: go run ./tools/ci revert-on-red --run-attempt N [--push-revert]\n", args[i])
		return 2
	}
	vars := map[string]string{}
	for _, k := range []string{"GITHUB_REPOSITORY", "GITHUB_TOKEN", "HEAD_SHA", "RUN_ID"} {
		if vars[k] = e.getenv(k); vars[k] == "" {
			fmt.Fprintf(e.stderr, "revert-on-red: %s is not set\n", k)
			return 2
		}
	}
	rv := &reverter{
		e: e, r: r,
		repo: vars["GITHUB_REPOSITORY"], head: vars["HEAD_SHA"], runID: vars["RUN_ID"], pushRevert: pushRevert,
		ghEnv: []string{"GH_TOKEN=" + vars["GITHUB_TOKEN"]},
	}
	if attempt == "1" {
		return rv.rerunOnce()
	}
	if attempt != "" {
		fmt.Fprintf(e.stdout, "run %s is red on attempt %s; the rerun is red too, proceeding to the revert.\n", rv.runID, attempt)
	}
	return rv.revert()
}

type reverter struct {
	e     env
	r     cmdRunner
	repo  string
	head  string
	runID string
	ghEnv []string
	// pushRevert: push the revert commit straight to main (falling back to the
	// pull request when the ruleset refuses). False, the default, opens the pull
	// request and pushes nothing to main.
	pushRevert bool
}

func (v *reverter) notice(format string, a ...any) {
	fmt.Fprintf(v.e.stdout, "::notice::"+format+"\n", a...)
}

func (v *reverter) git(args ...string) (string, int) {
	out, code, err := capture(v.r, cmdSpec{Name: "git", Args: args, Dir: v.e.dir, Stderr: v.e.stderr})
	if err != nil {
		fmt.Fprintf(v.e.stderr, "revert-on-red: git: %v\n", err)
		return "", 127
	}
	return out, code
}

// gh runs gh with the token; its stderr is always shown, because no gh failure
// is ever read as "nothing found".
func (v *reverter) gh(args ...string) (string, int) {
	c := cmdSpec{Name: "gh", Args: args, Dir: v.e.dir, Env: v.ghEnv, Stderr: v.e.stderr}
	out, code, err := capture(v.r, c)
	if err != nil {
		fmt.Fprintf(v.e.stderr, "revert-on-red: gh: %v\n", err)
		return "", 127
	}
	return out, code
}

func (v *reverter) fail(format string, a ...any) int {
	fmt.Fprintf(v.e.stderr, "revert-on-red: "+format+"\n", a...)
	return 1
}

// undecided is the exit of a verb that could not ask GitHub something it had to
// know: it is red (exit 1), and it files ONE needs-owner issue (the way
// nightly-report files its issue: gh issue create on the repository) naming the
// call, so a person decides. It never reads the failure as "skip". If the issue
// cannot be filed either, that is said on stderr and the exit is still 1.
func (v *reverter) undecided(what string) int {
	fmt.Fprintf(v.e.stderr, "revert-on-red: %s; not deciding for a person, exiting red and filing a needs-owner issue\n", what)
	title := fmt.Sprintf("needs-owner: revert-on-red could not decide on %s", shortSHA(v.head))
	body := fmt.Sprintf("revert-on-red ran for ci run %s at %s on main and could not ask GitHub something it had to know:\n\n%s\n\nIt did not skip and it opened no revert for this failure. Decide by hand whether main at %s is to be reverted.", v.runID, v.head, what, shortSHA(v.head))
	v.fileIssue(title, body)
	return 1
}

// fileIssue files one issue on the repository (the way nightly-report does) and
// says whether it could. A failure to file is said on stderr and is never silent.
func (v *reverter) fileIssue(title, body string) bool {
	out, code := v.gh("issue", "create", "--repo", v.repo, "--title", title, "--body", body)
	if out != "" {
		fmt.Fprintln(v.e.stdout, out)
	}
	if code != 0 {
		fmt.Fprintf(v.e.stderr, "revert-on-red: gh issue create exited %d; no needs-owner issue was filed\n", code)
		return false
	}
	return true
}

// rerunOnce is the flake guard: the first red of a run is only a trigger to run
// its failed jobs again.
func (v *reverter) rerunOnce() int {
	if _, code := v.gh("run", "rerun", v.runID, "--failed"); code != 0 {
		return v.undecided(fmt.Sprintf("gh run rerun %s --failed exited %d", v.runID, code))
	}
	v.notice("run %s failed on attempt 1; re-ran its failed jobs once (flake guard). The rerun's own workflow_run completion re-enters this workflow with run_attempt 2, and only a rerun that is red too is reverted.", v.runID)
	return 0
}

func (v *reverter) revert() int {
	short := shortSHA(v.head)

	// guard 1: never revert a revert (no revert loops)
	subject, code := v.git("log", "-1", "--format=%s", v.head)
	if code != 0 {
		return v.fail("git log of %s exited %d", short, code)
	}
	body, code := v.git("log", "-1", "--format=%B", v.head)
	if code != 0 {
		return v.fail("git log of %s exited %d", short, code)
	}
	if strings.HasPrefix(subject, "Revert ") || strings.HasPrefix(subject, "revert ") || strings.Contains(body, "This reverts commit") {
		v.notice("head %s is itself a revert (\"%s\"); no revert loops, skipping.", short, subject)
		return 0
	}

	// main must still be at the failed run's commit
	if _, code := v.git("fetch", "origin", "main"); code != 0 {
		return v.fail("git fetch origin main exited %d", code)
	}
	tip, code := v.git("rev-parse", "origin/main")
	if code != 0 {
		return v.fail("git rev-parse origin/main exited %d", code)
	}
	if tip != v.head {
		v.notice("main has moved past %s (now %s); the newer run decides, skipping.", short, shortSHA(tip))
		return 0
	}

	// guard 2: the parent commit's ci run on main must have been green
	parent, code := v.git("rev-parse", "--verify", v.head+"^")
	if code != 0 {
		if parent, code = v.git("rev-parse", "--verify", v.head+"~1"); code != 0 {
			return v.fail("%s has no parent commit", short)
		}
	}
	fmt.Fprintf(v.e.stdout, "head=%s parent=%s\n", short, shortSHA(parent))
	conclusion, err := v.parentConclusion(parent)
	if err != nil {
		return v.undecided(err.Error())
	}
	if conclusion == "" {
		v.notice("no ci push run found for parent %s; no green baseline, skipping.", shortSHA(parent))
		return 0
	}
	if conclusion != "success" {
		v.notice("parent %s ci run concluded %s; the red predates this push, skipping.", shortSHA(parent), conclusion)
		return 0
	}

	failing, err := v.failingJobs()
	if err != nil {
		return v.undecided(err.Error())
	}
	if failing == "" {
		failing = "no failing job named"
	}
	// the merged PR is asked BEFORE the revert is pushed: a lookup that fails
	// must leave nothing pushed to main.
	pr, err := v.mergedPR()
	if err != nil {
		return v.undecided(err.Error())
	}

	// the revert
	for _, kv := range [][]string{{"user.name", revertBotName}, {"user.email", revertBotEmail}} {
		if _, code := v.git("config", kv[0], kv[1]); code != 0 {
			return v.fail("git config %s exited %d", kv[0], code)
		}
	}
	parents, code := v.git("rev-list", "--parents", "-n", "1", v.head)
	if code != 0 {
		return v.fail("git rev-list exited %d", code)
	}
	if len(strings.Fields(parents)) >= 3 {
		fmt.Fprintln(v.e.stdout, "merge commit: reverting with -m 1")
		_, code = v.git("revert", "-m", "1", "--no-edit", v.head)
	} else {
		fmt.Fprintln(v.e.stdout, "plain commit: reverting")
		_, code = v.git("revert", "--no-edit", v.head)
	}
	if code != 0 {
		return v.fail("git revert of %s exited %d", short, code)
	}
	msg := fmt.Sprintf("revert %s: main red on %s (mechanical revert-on-red; fix forward on a branch)", short, failing)
	if _, code := v.git("commit", "--amend", "-q", "-m", msg); code != 0 {
		return v.fail("git commit --amend exited %d", code)
	}
	newSHA, code := v.git("rev-parse", "HEAD")
	if code != 0 {
		return v.fail("git rev-parse HEAD exited %d", code)
	}
	fmt.Fprintf(v.e.stdout, "revert commit=%s\n", shortSHA(newSHA))

	// push to main, or open a revert/<sha> PR and leave it open
	if code := v.land(msg, newSHA); code != 0 {
		return code
	}

	// ONE comment on the merged PR, found by the commit's PR association
	if pr != "" {
		comment := fmt.Sprintf("Main was red on %s. Reverted by %s (mechanical revert-on-red); fix forward on a branch.", failing, shortSHA(newSHA))
		if _, code := v.gh("api", fmt.Sprintf("repos/%s/issues/%s/comments", v.repo, pr), "-f", "body="+comment); code != 0 {
			return v.undecided(fmt.Sprintf("commenting on #%s exited %d; the revert is already pushed or its pull request opened (see the run's log)", pr, code))
		}
		fmt.Fprintf(v.e.stdout, "commented on #%s naming revert %s\n", pr, shortSHA(newSHA))
	} else {
		v.notice("no merged PR found for %s; posting no comment.", short)
	}
	return 0
}

// land puts the revert where a person can land it. By default that is a
// revert/<sha> pull request against main, LEFT OPEN, deliberately (auto-merge is
// not an enqueue at all but a standing instruction the forge executes later with
// nobody in the room), and one needs-owner issue naming it: CI lands nothing by
// itself, and the issue is what makes a red main something a person sees. Only
// with pushRevert does it first push the revert commit to main; a direct push the
// ruleset refuses falls back to the pull request.
func (v *reverter) land(msg, newSHA string) int {
	short, newShort := shortSHA(v.head), shortSHA(newSHA)
	if v.pushRevert {
		var log strings.Builder
		code, err := v.r.Run(cmdSpec{Name: "git", Args: []string{"push", "origin", "HEAD:main"}, Dir: v.e.dir, Stdout: &log, Stderr: &log})
		if err == nil && code == 0 {
			fmt.Fprintf(v.e.stdout, "pushed revert %s to main\n", newShort)
			return 0
		}
		if err != nil {
			return v.fail("git push: %v", err)
		}
		io.WriteString(v.e.stdout, log.String())
		fmt.Fprintln(v.e.stdout, "direct push refused by the ruleset; opening a revert PR for somebody to land")
	} else {
		fmt.Fprintln(v.e.stdout, "opening a revert PR for somebody to land (--push-revert is not set: nothing is pushed to main)")
	}
	branch := "revert/" + short
	if _, code := v.git("branch", "-f", branch, "HEAD"); code != 0 {
		return v.fail("git branch -f %s exited %d", branch, code)
	}
	if _, code := v.git("push", "origin", branch); code != 0 {
		if _, code := v.git("push", "-f", "origin", branch); code != 0 {
			return v.fail("git push of %s exited %d", branch, code)
		}
	}
	prBody := fmt.Sprintf("Mechanical revert-on-red. ci failed on main at %s (run %s); reverted as %s. This pull request lands nothing by itself: main is red until somebody merges it. Fix forward on a branch.", short, v.runID, newShort)
	if _, code := v.gh("pr", "create", "--base", "main", "--head", branch, "--title", msg, "--body", prBody); code != 0 {
		fmt.Fprintf(v.e.stdout, "PR already exists for %s; reusing it.\n", branch)
	}
	num, code := v.gh("pr", "view", branch, "--json", "number", "--jq", ".number")
	if code != 0 {
		return v.undecided(fmt.Sprintf("gh pr view %s exited %d; the revert branch is pushed", branch, code))
	}
	fmt.Fprintf(v.e.stdout, "revert PR #%s open on %s\n", num, branch)
	v.notice("revert PR #%s is OPEN on %s and lands nothing by itself: main is red until somebody lands it -- merge it by hand.", num, branch)
	title := fmt.Sprintf("needs-owner: main is red at %s; revert PR #%s awaits landing", short, num)
	body := fmt.Sprintf("revert-on-red reverted the push that turned main red (ci run %s at %s) and opened pull request #%s on %s. It pushed nothing to main and merges nothing: merge the pull request to land the revert, or close it and fix forward.", v.runID, v.head, num, branch)
	if !v.fileIssue(title, body) {
		return 1
	}
	return 0
}

// parentConclusion is the conclusion of the ci workflow's push run on main at
// the parent commit, looked for on the first five pages of runs; "" when every
// page answered and none names it. A run with no conclusion yet reads "null",
// which is not success. A page that cannot be fetched or read is an error: a
// lookup that failed is never "no run found".
func (v *reverter) parentConclusion(parent string) (string, error) {
	for page := 1; page <= 5; page++ {
		out, code := v.gh("api", fmt.Sprintf("repos/%s/actions/workflows/ci.yml/runs?branch=main&event=push&per_page=100&page=%d", v.repo, page))
		if code != 0 {
			return "", fmt.Errorf("gh api of ci.yml runs page %d exited %d", page, code)
		}
		var runs struct {
			WorkflowRuns []struct {
				HeadSHA    string  `json:"head_sha"`
				Conclusion *string `json:"conclusion"`
			} `json:"workflow_runs"`
		}
		if err := json.Unmarshal([]byte(out), &runs); err != nil {
			return "", fmt.Errorf("gh api of ci.yml runs page %d printed no run list: %v", page, err)
		}
		for _, run := range runs.WorkflowRuns {
			if run.HeadSHA != parent {
				continue
			}
			if run.Conclusion == nil {
				return "null", nil
			}
			return *run.Conclusion, nil
		}
	}
	return "", nil
}

// failingJobs names the jobs of the failed run that did not succeed and were
// not skipped, comma separated; "" when none is named. A lookup that fails is
// an error, never "none named".
func (v *reverter) failingJobs() (string, error) {
	out, code := v.gh("api", fmt.Sprintf("repos/%s/actions/runs/%s/jobs?per_page=100", v.repo, v.runID))
	if code != 0 {
		return "", fmt.Errorf("gh api of run %s jobs exited %d", v.runID, code)
	}
	var jobs struct {
		Jobs []struct {
			Name       string  `json:"name"`
			Conclusion *string `json:"conclusion"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(out), &jobs); err != nil {
		return "", fmt.Errorf("gh api of run %s jobs printed no job list: %v", v.runID, err)
	}
	var names []string
	for _, j := range jobs.Jobs {
		if j.Conclusion != nil && (*j.Conclusion == "success" || *j.Conclusion == "skipped") {
			continue
		}
		names = append(names, j.Name)
	}
	return strings.Join(names, ", "), nil
}

// mergedPR is the number of the first merged pull request the head commit
// belongs to, "" when it belongs to none. A lookup that fails is an error,
// never "no pull request".
func (v *reverter) mergedPR() (string, error) {
	out, code := v.gh("api", fmt.Sprintf("repos/%s/commits/%s/pulls", v.repo, v.head))
	if code != 0 {
		return "", fmt.Errorf("gh api of the pull requests of %s exited %d", shortSHA(v.head), code)
	}
	var prs []struct {
		Number   int     `json:"number"`
		MergedAt *string `json:"merged_at"`
	}
	if err := json.Unmarshal([]byte(out), &prs); err != nil {
		return "", fmt.Errorf("gh api of the pull requests of %s printed no list: %v", shortSHA(v.head), err)
	}
	for _, p := range prs {
		if p.MergedAt != nil {
			return fmt.Sprint(p.Number), nil
		}
	}
	return "", nil
}
