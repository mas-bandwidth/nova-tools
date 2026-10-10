//go:build functional

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
	"github.com/mas-bandwidth/nova-tools/pkg/testbin"
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
	runGit(t, "", "init", "-q", "-b", driveBase, "--", seed)
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
	return first + "\nbase-repo: " + e.repoURL + "\nBASE: " + driveBase + "\n" + rest
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

func (e *caseEnv) runner(harness, model string) *nativeRunner {
	root := filepath.Join(e.dir, "m1")
	return &nativeRunner{self: builtTool, harness: harness, model: model, root: root,
		slots: filepath.Join(root, "slots"), resultsRoot: filepath.Join(root, "results"), deadline: time.Minute,
		tokens: "unmetered", noWall: true, stderr: io.Discard, env: e.env}
}

func (e *caseEnv) pusher(rn *nativeRunner) *gitPusher {
	pu := newGitPusher(rn.root, rn.slots)
	pu.gh, pu.env = e.gh, e.env
	return pu
}

func (e *caseEnv) member(t *testing.T) {
	t.Helper()
	require.NoError(t, buildShared(), "building the binaries these tests run")
	root := filepath.Join(e.dir, "m1")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "slots"), 0o755))
	write(t, filepath.Join(root, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
}

// directRun starts one packet under the real native and judges its end as the
// member does: the result, the push and the finish.
func (e *caseEnv) directRun(t *testing.T, p member.Packet, harness, model string) (member.Result, member.Push, member.Finish, string) {
	t.Helper()
	e.member(t)
	rn := e.runner(harness, model)
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
		push = e.pusher(rn).Push(p, r)
	}
	fin, why := member.Judge(r, push)
	return r, push, fin, why
}

// landedOnMain lands a commit on origin's driveBase after pushedWork (in its clone), as another card
// landing moves a stream's base, and returns its new tip.
func landedOnMain(t *testing.T, e *caseEnv) string {
	t.Helper()
	w := filepath.Join(e.dir, "w")
	gitAs(t, w, "switch", "-q", "-C", driveBase, "origin/"+driveBase)
	write(t, filepath.Join(w, "g"), "landed since\n")
	gitAs(t, w, "add", "g")
	gitAs(t, w, "commit", "-q", "-m", "landed since")
	runGit(t, w, "push", "-q", "origin", driveBase)
	return gitAs(t, w, "rev-parse", "HEAD")
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
				Brief: e.brief(), WorkBranch: "sprint/a-1.w1", Head: h1, WorkBase: driveBase}
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

// A rework whose base moved after attempt one is staged, end to end (the member's frame,
// native's stage, the slot's staged commit, the member's push), at a new carry on the base's
// tip, said by native's STAGE CARRY line: a finish from that commit is ok, and one from attempt
// one's old head is refused for not descending from the staged commit (nova-tools#5215; red
// when native stages a rework as a first attempt). A read at attempt two stays on its head.
func TestAReworkAfterTheBaseMovedIsStagedAtANewCarryOnItsTip(t *testing.T) {
	t.Parallel()
	staged := regexp.MustCompile(`STAGED ([0-9a-f]{40}) parent ([0-9a-f]{40})`)
	for _, tc := range []struct {
		name, body string
		fin        member.Finish
		why        string
	}{
		{"from the staged commit", "cd repo && echo two >> f && git commit -q -am two && git push && cd .. && ", member.FinishOK, ""},
		{"from the old head", "cd repo && git checkout -q -B old \"$H1\" && echo two >> f && git commit -q -am two && git push origin old && cd .. && ",
			member.FinishFailed, "does not descend from the staged commit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newCaseEnv(t, false)
			h1 := pushedWork(t, e)
			tip := landedOnMain(t, e)

			h := e.script(t, "H1="+h1+`; echo "STAGED $(git -C repo rev-parse HEAD) parent $(git -C repo rev-parse HEAD^)" >&2; `+tc.body+`gh pr create --title T2 --body B2 >&2`)
			p := member.Packet{Card: "a-1.w2", Kind: "work", As: "m1", Primary: "a-1", Stream: "a", Attempt: 2, Gen: 2, Epoch: 1,
				Brief: e.brief(), Branch: "sprint/a-1.w2", Base: "sprint/a-1.w1", BaseHead: h1, BaseFrom: 1, Fix: "fix the thing"}
			r, push, fin, why := e.directRun(t, p, h, "anthropic/claude-x")
			m := staged.FindStringSubmatch(e.log())
			require.NotNil(t, m, e.log())
			assert.NotEqual(t, h1, m[1], "the rework is not staged at attempt one's head")
			assert.Equal(t, tip, m[2], "the staged commit is one carry on the base's new tip")
			assert.Equal(t, "staged="+m[1][:12]+" tip="+tip[:12]+" of "+driveBase+" carry=carried attempt=1 prev="+h1[:12], r.Carry, "native's STAGE CARRY line")
			assert.Equal(t, tc.fin, fin, "push %+v: %s", push, why)
			assert.Contains(t, why, tc.why)
		})
	}

	e := newCaseEnv(t, false)
	h1 := pushedWork(t, e)
	landedOnMain(t, e)
	h := e.script(t, `echo "READ STAGED $(git -C repo rev-parse HEAD)" >&2`)
	read := member.Packet{Card: "a-1.r2", Kind: "read", As: "r1", Primary: "a-1", Stream: "a", Attempt: 2, Gen: 1, Epoch: 1,
		Brief: e.brief(), Head: h1, WorkBranch: "sprint/a-1.w1", WorkBase: driveBase}
	r, _, _, _ := e.directRun(t, read, h, "anthropic/claude-x")
	assert.Contains(t, e.log(), "READ STAGED "+h1, "a read at attempt two is staged at the head it reads")
	assert.Empty(t, r.Carry)
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
	e.member(t)
	rn := e.runner(h, "fake/claude-x")
	rn.pass = []string{"PROBE_API_KEY"}
	p := member.Packet{Card: "a-1.w1", Kind: "work", As: "m1", Primary: "a-1", Stream: "a", Attempt: 1, Gen: 1, Epoch: 1,
		Brief: e.brief(), Branch: "sprint/a-1.w1"}
	c, err := rn.Start(p)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
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

// driveBase is the branch every drive's twin origin starts on and its cards are
// cut on: a sprint branch, as every stream but the promotion stream lands on one
// (dev and main are protected, sprint.PromotionBaseWhy).
const driveBase = "sprint/drive"

// memberCard is the brief of the test's cards: a card that passes the card lint
// (`add` holds every brief to it), cut from `nova-swarm template --name card` to the
// fake harness's job: the task in place of the placeholder, the RULES paragraph
// with every rule sentence as the template prints it (swarm.ChildRulesParagraph),
// and the steps the harness stands for.
var memberCard = "RESULT: <label> sha=<sha12>\n" +
	"You are a child of the coordinator: one task, one worktree, one branch, unattended. This card is the whole of the task and it stands alone in front of a stranger; nothing outside it is owed to you.\n" +
	"Deadline: finish within 5 minutes.\n\n" +
	swarm.ChildRulesParagraph() + "\n" +
	"THE TASK. Check the member loop: do nothing to the tree and write RESULT.md, line 1 of this card, then the head, the branch and the report that the member loop carries to the card.\n\n" +
	"STEP 1. Enter your worktree and read this card.\n" +
	"STEP 2. Write RESULT.md: line 1 is line 1 of this card; under it the head and the report, in under 80 lines."
