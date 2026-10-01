//go:build functional

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/testbin"
)

// The card contract's cases (docs/SPEC-CARD-CONTRACT.md), each a scripted
// child under the real native, its finish judged by the member as the sprint
// sees it. They began as a cold read's probes of the first build.

// caseEnv is one card's world: a bare origin, the URL the card names it by,
// the environment the member adds for its git and its native children, the
// child's log and a gh that records what the member asks of it.
type caseEnv struct {
	dir, origin, repoURL, logs, gh string
	env                            []string
	briefOverride                  string // the brief, when a case writes its own
}

// newCaseEnv makes a bare origin at one commit. remote names it by an https
// URL that git's insteadOf maps to the bare (the member's own git
// configuration), with a bench mirror under the member's HOME cloned now, so a
// commit pushed later leaves it stale.
func newCaseEnv(t *testing.T, remote bool) *caseEnv {
	t.Helper()
	dir := t.TempDir()
	e := &caseEnv{dir: dir, origin: filepath.Join(dir, "origin.git"), logs: filepath.Join(dir, "logs")}
	require.NoError(t, os.MkdirAll(e.logs, 0o755))
	seed := filepath.Join(dir, "seed")
	runGit(t, "", "init", "-q", "-b", "main", "--", seed)
	write(t, filepath.Join(seed, "f"), "base\n")
	gitAs(t, seed, "add", "f")
	gitAs(t, seed, "commit", "-q", "-m", "base")
	runGit(t, "", "clone", "-q", "--bare", "--", seed, e.origin)
	e.repoURL = e.origin
	if remote {
		e.repoURL = "https://example.com/probe/repo"
		home := filepath.Join(dir, "home")
		runGit(t, "", "clone", "-q", "--mirror", "--", e.origin, filepath.Join(home, "nova-bench", "mirror", "repo.git"))
		cfg := filepath.Join(dir, "gitconfig")
		write(t, cfg, "[url \""+e.origin+"\"]\n\tinsteadOf = "+e.repoURL+"\n")
		e.env = []string{"HOME=" + home, "GIT_CONFIG_GLOBAL=" + cfg}
	}
	e.env = append(e.env, "GH_TOKEN=a-forge-token-the-child-never-sees")
	e.gh = filepath.Join(dir, "gh")
	require.NoError(t, testbin.WriteExecutable(e.gh, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+dir+"/gh.args'\ncat > '"+dir+"/gh.body'\necho https://example.com/o/n/pull/42\n"), 0o755))
	return e
}

func (e *caseEnv) brief() string {
	if e.briefOverride != "" {
		return e.briefOverride
	}
	first, rest, _ := strings.Cut(memberCard, "\n")
	return first + "\nbase-repo: " + e.repoURL + "\nBASE: main\n" + rest
}

// script is the child: its stderr goes to the case's log.
func (e *caseEnv) script(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(e.dir, "child.sh")
	require.NoError(t, testbin.WriteExecutable(p, []byte("#!/bin/sh\nL="+e.logs+"/child.log\nexec 2>>$L\n"+body+"\n"), 0o755))
	return p
}

func (e *caseEnv) log() string {
	b, err := os.ReadFile(filepath.Join(e.logs, "child.log"))
	if os.IsNotExist(err) {
		return ""
	}
	return string(b)
}

func (e *caseEnv) runner(bin, harness, model string) *nativeRunner {
	root := filepath.Join(e.dir, "m1")
	return &nativeRunner{self: builtTool, sprintBin: bin, harness: harness, model: model, root: root,
		slots: filepath.Join(root, "slots"), resultsRoot: filepath.Join(root, "results"), deadline: time.Minute,
		tokens: "unmetered", noWall: true, stderr: io.Discard, env: e.env}
}

func (e *caseEnv) pusher(rn *nativeRunner, bin string) *gitPusher {
	pu := newGitPusher(rn.root, rn.slots, bin)
	pu.gh, pu.env = e.gh, e.env
	return pu
}

func (e *caseEnv) member(t *testing.T) {
	t.Helper()
	root := filepath.Join(e.dir, "m1")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "slots"), 0o755))
	write(t, filepath.Join(root, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
}

// sprintRun runs one card through the twin and the member loop, one command
// at a time, and returns the card's story and the member's output.
func (e *caseEnv) sprintRun(t *testing.T, harness, model string) (story, out string) {
	t.Helper()
	bin := builtSprint(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	d := &memberDrive{t: t, addr: "mem:" + filepath.Join(e.dir, "sprint.twin"), bin: bin}
	d.must("init", "--members", "m1:1")
	d.must("add", "--stream", "a", "--count", "1", "--brief", e.brief())
	d.must("start")
	e.member(t)
	rn := e.runner(bin, harness, model)
	ob := &lockedBuf{}
	m := member.New(member.Config{As: "m1", Width: 1}, &execSprint{bin: bin, actor: "m1", env: []string{"NOVA_SPRINT_REDIS=" + d.addr}}, rn, e.pusher(rn, bin), ob)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("story:\n%s\nmember:\n%s\nchild:\n%s", story, ob.String(), e.log())
		}
	})
	for !strings.Contains(story, "m1 finished attempt 1") {
		require.NoError(t, ctx.Err(), "the card did not finish in time")
		d.must("tick")
		_, err := m.Tick(time.Now())
		require.NoError(t, err)
		story = d.must("card", "a-1")
		time.Sleep(100 * time.Millisecond)
	}
	return story, ob.String()
}

// directRun starts one packet under the real native and judges its end as the
// member does: the result, the push and the finish.
func (e *caseEnv) directRun(t *testing.T, p member.Packet, harness, model string) (member.Result, member.Push, member.Finish, string) {
	t.Helper()
	bin := builtSprint(t)
	e.member(t)
	rn := e.runner(bin, harness, model)
	c, err := rn.Start(p)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for !c.Done() {
		require.NoError(t, ctx.Err(), "the child did not end in time:\n%s", e.log())
		time.Sleep(100 * time.Millisecond)
	}
	r := c.Result()
	if p.Kind == "read" {
		return r, member.Push{}, "", ""
	}
	push := member.Push{None: "the result names no commit"}
	if r.Head != "" {
		push = e.pusher(rn, bin).Push(p, r)
	}
	fin, why := member.Judge(r, push)
	return r, push, fin, why
}

// pushedWork puts attempt one's commit on origin's sprint/a-1.w1 and returns it.
func pushedWork(t *testing.T, e *caseEnv) string {
	t.Helper()
	w := filepath.Join(e.dir, "w")
	runGit(t, "", "clone", "-q", "--", e.origin, w)
	write(t, filepath.Join(w, "f"), "base\nattempt one\n")
	gitAs(t, w, "commit", "-q", "-am", "attempt one")
	runGit(t, w, "push", "-q", "origin", "HEAD:refs/heads/sprint/a-1.w1")
	return gitAs(t, w, "rev-parse", "HEAD")
}

// A claude child that clones the card's repository by its scp-form ssh URL,
// works on a feature branch of its own and pushes it: the member pushes that
// head to the card's branch and opens the pull request from it.
func TestAnSSHCloneOnAFeatureBranchFinishesOk(t *testing.T) {
	t.Parallel()
	e := newCaseEnv(t, true)
	h := e.script(t, `set -e
git clone git@example.com:probe/repo.git work
cd work
git checkout -q -b feat
echo change >> f
git commit -q -am "the change"
git push -u origin feat
gh pr create --title T --body B >&2`)
	story, _ := e.sprintRun(t, h, "anthropic/claude-sonnet-5-5")
	assert.NotContains(t, story, "FAILED")
	pushed := strings.TrimSpace(runGit(t, e.origin, "rev-parse", "refs/heads/sprint/a-1.w1"))
	assert.Equal(t, "the change", strings.TrimSpace(runGit(t, e.origin, "log", "-1", "--format=%s", pushed)))
	assert.Contains(t, e.log(), "feat -> sprint/a-1.w1", "the push answer names the branch the sprint pushes")
	args, err := os.ReadFile(filepath.Join(e.dir, "gh.args"))
	require.NoError(t, err)
	assert.Contains(t, string(args), "--head\nsprint/a-1.w1\n")
}

// A child that opens a pull request and committed nothing is failed, no commit.
func TestAPullRequestWithNoCommitIsFailed(t *testing.T) {
	t.Parallel()
	e := newCaseEnv(t, false)
	story, out := e.sprintRun(t, e.script(t, `gh pr create --title T --body B >&2`), "fake/claude-x")
	assert.Contains(t, story, "FAILED")
	assert.Contains(t, story+out, "no commit")
	assert.NoFileExists(t, filepath.Join(e.dir, "gh.args"), "no pull request for nothing")
}

// A push origin refuses is a failed finish with git's line.
func TestAPushOriginRefusesIsFailed(t *testing.T) {
	t.Parallel()
	e := newCaseEnv(t, false)
	require.NoError(t, testbin.WriteExecutable(filepath.Join(e.origin, "hooks", "pre-receive"), []byte("#!/bin/sh\necho 'the hook says no' >&2\nexit 1\n"), 0o755))
	story, out := e.sprintRun(t, e.script(t, `set -e
cd repo
echo change >> f
git commit -q -am "the change"
git push
gh pr create --title T --body B >&2`), "fake/claude-x")
	assert.Contains(t, story, "FAILED")
	assert.Contains(t, story+out, "push refused")
}

// Inside the frame every gh write and every other clone is refused with one
// line, and the child's environment carries no forge credential.
func TestTheFrameRefusesWritesAndHoldsNoCredential(t *testing.T) {
	t.Parallel()
	e := newCaseEnv(t, false)
	h := e.script(t, `for c in "pr merge 1" "pr close 1" "issue create --title x --body y" "api /user" "-R o/r pr merge 1" "pr edit 1 --title z" "release create v1" "repo delete o/r" "pr list" "repo view"; do
  gh $c >/dev/null 2>$L.tmp; echo "GH[$c] rc=$? lines=$(wc -l < $L.tmp | tr -d ' ')" >&2
done
git clone https://example.com/other/repo other 2>$L.tmp; echo "CLONE rc=$?" >&2
echo "ENVNAMES: $(env | cut -d= -f1 | grep -E '^GH_|^GITHUB_|^SSH_AUTH_SOCK|^GIT_ASKPASS' | tr '\n' ' ')." >&2
set -e
cd repo
echo change >> f
git commit -q -am "the change"
git push
gh pr create --title T --body B >&2`)
	story, _ := e.sprintRun(t, h, "fake/claude-x")
	assert.NotContains(t, story, "FAILED")
	log := e.log()
	assert.Equal(t, 10, strings.Count(log, " rc=2 lines=1\n"), "every gh write refused, one line each:\n%s", log)
	assert.Contains(t, log, "CLONE rc=128")
	assert.Contains(t, log, "ENVNAMES: .", "no forge credential reaches the child")
}

// A read stages the head under review and its verdict is the review.
func TestAReadIsAReview(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, cmd, verdict, report string }{
		{"broken", `gh pr review --request-changes --body "f:2 wrong"`, "broken", "f:2 wrong"},
		{"approve", `gh pr review --approve --body "looks right"`, "ok", "looks right"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newCaseEnv(t, false)
			h1 := pushedWork(t, e)
			h := e.script(t, `gh pr diff >&2; `+tc.cmd+` >&2`)
			p := member.Packet{Card: "a-1.r1", Kind: "read", As: "m1", Primary: "a-1", Stream: "a", Attempt: 1, Epoch: 1,
				Brief: e.brief(), WorkBranch: "sprint/a-1.w1", Head: h1, WorkBase: "main"}
			r, _, _, _ := e.directRun(t, p, h, "anthropic/claude-x")
			assert.Equal(t, tc.verdict, r.Verdict)
			assert.Equal(t, tc.report, r.Report)
			assert.Contains(t, e.log(), "+attempt one", "the diff is the head under review against the base")
		})
	}
}

// A rework is staged at attempt one's pushed head, and is ok only with a
// commit of its own: whatever the bench mirror knew, and whatever the child
// did to the checkout's remote (red on the first build of this layer).
func TestAReworkIsOkOnlyWithACommitOfItsOwn(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, body string
		remote     bool
		fin        member.Finish
	}{
		{"no commit", "", false, member.FinishFailed},
		{"a commit", "cd repo && echo two >> f && git commit -q -am two && git push && cd .. && ", false, member.FinishOK},
		{"no commit on a stale mirror", "", true, member.FinishFailed},
		{"no commit, the remote removed", "git -C repo remote remove origin && ", false, member.FinishFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newCaseEnv(t, tc.remote)
			h1 := pushedWork(t, e)
			h := e.script(t, `echo "STAGED $(git -C repo rev-parse HEAD) on $(git -C repo branch --show-current)" >&2; `+tc.body+`gh pr create --title T2 --body B2 >&2`)
			p := member.Packet{Card: "a-1.w2", Kind: "work", As: "m1", Primary: "a-1", Stream: "a", Attempt: 2, Gen: 2, Epoch: 1,
				Brief: e.brief(), Branch: "sprint/a-1.w2", Base: "sprint/a-1.w1", BaseHead: h1, Fix: "fix the thing"}
			r, push, fin, why := e.directRun(t, p, h, "anthropic/claude-x")
			assert.True(t, r.Ran, "the shim's finish alone is a published result: native says OK")
			assert.Contains(t, e.log(), "STAGED "+h1+" on sprint/a-1.w2")
			assert.Equal(t, tc.fin, fin, "push %+v: %s", push, why)
		})
	}
}

// A claude child that, after gh pr create, also writes RESULT.md as an older
// card template asked overwrites nothing: the finish is the shim's record, and
// the child's RESULT.md rides in the pull request body for the readers.
func TestARESULTWrittenAfterThePullRequestRidesInItsBody(t *testing.T) {
	t.Parallel()
	e := newCaseEnv(t, false)
	h := e.script(t, `set -e
cd repo
echo change >> f
git commit -q -am "the change"
git push
gh pr create --title T --body B >&2
cd ..
printf 'RESULT: a-1.w1 sha=x\n\n## Head\nrev: %s\n\n## One line\ndone, gate green\n' "$(git -C repo rev-parse HEAD)" > RESULT.md`)
	story, _ := e.sprintRun(t, h, "fake/claude-x")
	assert.NotContains(t, story, "FAILED")
	body, err := os.ReadFile(filepath.Join(e.dir, "gh.body"))
	require.NoError(t, err)
	assert.Contains(t, string(body), "B\n\n## RESULT.md\n\nRESULT: a-1.w1")
	assert.Contains(t, string(body), "done, gate green")
}

// A child with nothing to do says so, and the finish is failed, nothing to do,
// for the coordinator to judge: every work card ends with a commit.
func TestNothingToDoIsAFailedFinishForTheCoordinator(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, model, body string }{
		{"claude", "fake/claude-x", `gh pr create --title "nothing: the check already holds" >&2`},
		{"plain", "fake/fake-model", `printf 'head: -\nbranch: -\nverdict: nothing\ngate: go test\noutput: -\nreport: the check already holds\n' > RESULT.md`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newCaseEnv(t, false)
			story, _ := e.sprintRun(t, e.script(t, tc.body), tc.model)
			assert.Contains(t, story, "FAILED")
			assert.Contains(t, story, "nothing to do: the check already holds")
		})
	}
}

// The child of a member started in a polluted environment sees none of it: the
// member starts native with an allowlist, and only the secret --pass names
// reaches the harness (docs/SPEC-CARD-CONTRACT.md; red when the member handed
// its whole environment on).
func TestTheChildSeesOnlyTheAllowlist(t *testing.T) {
	t.Parallel()
	e := newCaseEnv(t, false)
	e.env = append(e.env, "CLAUDE_CODE_MESSAGING_TOKEN=m", "AWS_SECRET_ACCESS_KEY=a", "FOO_PASSWORD=p", "FOO=bar",
		"PROBE_API_KEY=passed", "LANG=C.UTF-8")
	h := e.script(t, `echo "ENV: $(env | cut -d= -f1 | sort | tr '\n' ' ')" >&2
set -e
cd repo
echo change >> f
git commit -q -am "the change"
git push
gh pr create --title T --body B >&2`)
	bin := builtSprint(t)
	e.member(t)
	rn := e.runner(bin, h, "fake/claude-x")
	rn.pass = []string{"PROBE_API_KEY"}
	p := member.Packet{Card: "a-1.w1", Kind: "work", As: "m1", Primary: "a-1", Stream: "a", Attempt: 1, Gen: 1, Epoch: 1,
		Brief: e.brief(), Branch: "sprint/a-1.w1"}
	c, err := rn.Start(p)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	for !c.Done() {
		require.NoError(t, ctx.Err(), "the child did not end in time:\n%s", e.log())
		time.Sleep(100 * time.Millisecond)
	}
	names := " " + strings.TrimSpace(strings.SplitN(strings.SplitN(e.log(), "ENV: ", 2)[1], "\n", 2)[0]) + " "
	for _, gone := range []string{"GH_TOKEN", "CLAUDE_CODE_MESSAGING_TOKEN", "AWS_SECRET_ACCESS_KEY", "FOO_PASSWORD", "FOO"} {
		assert.NotContains(t, names, " "+gone+" ")
	}
	for _, kept := range []string{"PROBE_API_KEY", "LANG", "PATH", "HOME"} {
		assert.Contains(t, names, " "+kept+" ")
	}
}

// A claude reader's review is its read: with no RESULT.md of its own, its gh pr
// review alone reaches the sprint as ok or broken (native counts the shim's
// finish as the published result; red when it did not, and the read was left
// with no verdict).
func TestAClaudeReadersReviewAloneIsItsVerdict(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, review, col string }{
		{"approve", `gh pr review --approve --body "the gate is green"`, "ok"},
		{"request changes", `gh pr review --request-changes --body "f:2 the line is wrong"`, "broken"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newCaseEnv(t, false)
			bin := builtSprint(t)
			d := &memberDrive{t: t, addr: "mem:" + filepath.Join(e.dir, "sprint.twin"), bin: bin}
			d.must("init", "--members", "m1:1", "--readers", "reader-a,reader-b") // a read asks two readers; reader-a's is the one run here
			d.must("add", "--stream", "a", "--count", "1", "--brief", e.brief())
			d.must("start")
			e.member(t)
			work := e.script(t, `set -e
cd repo
echo change >> f
git commit -q -am "the change"
git push
gh pr create --title T --body B >&2`)
			rn := e.runner(bin, work, "fake/claude-x")
			wm := member.New(member.Config{As: "m1", Width: 1}, &execSprint{bin: bin, actor: "m1", env: []string{"NOVA_SPRINT_REDIS=" + d.addr}}, rn, e.pusher(rn, bin), &lockedBuf{})

			readerRoot := filepath.Join(e.dir, "reader-a")
			require.NoError(t, os.MkdirAll(filepath.Join(readerRoot, "slots"), 0o755))
			write(t, filepath.Join(readerRoot, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Reader\treader@example.com\n")
			readHarness := filepath.Join(e.dir, "reader.sh")
			require.NoError(t, testbin.WriteExecutable(readHarness, []byte("#!/bin/sh\ngh pr diff | grep -q '^+change' || exit 1\n"+tc.review+"\n"), 0o755))
			rr := &nativeRunner{self: builtTool, sprintBin: bin, harness: readHarness, model: "fake/claude-reader", root: readerRoot,
				slots: filepath.Join(readerRoot, "slots"), resultsRoot: filepath.Join(readerRoot, "results"), deadline: time.Minute,
				tokens: "unmetered", noWall: true, stderr: io.Discard, env: e.env}
			rout := &lockedBuf{}
			reader := member.New(member.Config{As: "reader-a", Width: 1, Reader: true}, &execSprint{bin: bin, actor: "reader-a", env: []string{"NOVA_SPRINT_REDIS=" + d.addr}}, rr, nil, rout)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			for cellInt(d.where(), "readers", "reader-a", tc.col) == 0 {
				require.NoError(t, ctx.Err(), "the read did not reach %s:\n%s\nreader:\n%s", tc.col, d.must("card", "a-1"), rout.String())
				require.NotContains(t, rout.String(), "no verdict", "the review alone is the verdict")
				d.must("tick")
				_, err := wm.Tick(time.Now())
				require.NoError(t, err)
				_, err = reader.Tick(time.Now())
				require.NoError(t, err)
				time.Sleep(100 * time.Millisecond)
			}
			assert.Contains(t, rout.String(), "read a-1.r1.reader-a ok=")
		})
	}
}

// A recipe the brief's Stage: header names is in the job when the child starts,
// and a recipe the member does not have refuses the launch, which the finish
// reports failed (docs/SPEC-CARD-CONTRACT.md, staged recipes).
func TestAStagedRecipeIsInTheJob(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		have   bool
		failed bool
	}{{"present", true, false}, {"missing", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newCaseEnv(t, false)
			if tc.have {
				write(t, filepath.Join(e.dir, "m1", "recipes", "pr", "4926.md"), "the rewriter recipe\n")
			}
			first, rest, _ := strings.Cut(e.brief(), "\n")
			e.briefOverride = first + "\nStage: pr/4926.md\n" + rest
			story, _ := e.sprintRun(t, e.script(t, `set -e
cp recipes/pr/4926.md repo/recipe.md
cd repo
git add recipe.md
git commit -q -m "from the recipe"
git push
gh pr create --title T --body B >&2`), "fake/claude-x")
			assert.Equal(t, tc.failed, strings.Contains(story, "FAILED"), story)
		})
	}
}
