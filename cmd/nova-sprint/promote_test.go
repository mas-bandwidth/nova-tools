package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
)

// promoteScript is the fake git and gh the step talks to. It records every
// call and answers the failure path: the pull request is opened, admitted with
// no strategy flag, and a merge-group run has failed.
type promoteScript struct {
	live, tip, baseSHA, logText, groupLog string
	gitCalls, ghCalls                     [][]string
	gated                                 string
	cfg                                   map[string]string
	enqueued                              bool
}

func (s *promoteScript) config(args []string) (string, error) {
	if s.cfg == nil {
		s.cfg = map[string]string{}
	}
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "--get"):
		key := args[len(args)-1]
		v, ok := s.cfg[key]
		if !ok {
			return "", errors.New("no config")
		}
		return v, nil
	case strings.Contains(joined, "--unset"):
		delete(s.cfg, args[len(args)-1])
		return "", nil
	default:
		if len(args) < 2 {
			return "", errors.New("config")
		}
		s.cfg[args[len(args)-2]] = args[len(args)-1]
		return "", nil
	}
}

func (s *promoteScript) git(_ context.Context, _ string, args ...string) (string, error) {
	s.gitCalls = append(s.gitCalls, append([]string(nil), args...))
	switch args[0] {
	case "rev-parse":
		rev := args[len(args)-1]
		switch rev {
		case s.live, "refs/heads/" + s.live:
			return s.tip, nil
		case "dev", "refs/heads/dev":
			return s.baseSHA, nil
		case "refs/promoted/last":
			return "", errors.New("missing")
		default:
			if strings.HasPrefix(rev, "promo/") {
				return s.tip, nil
			}
			return "", errors.New("unknown rev " + rev)
		}
	case "log":
		return s.logText, nil
	case "branch":
		return "", nil
	case "config":
		return s.config(args)
	case "checkout", "switch":
		return "", nil
	case "symbolic-ref":
		return s.live, nil
	case "merge", "fetch":
		return "", nil
	case "ls-files":
		return "", nil
	case "push", "update-ref":
		return "", nil
	default:
		return "", errors.New("unexpected git " + strings.Join(args, " "))
	}
}

func (s *promoteScript) gh(_ context.Context, _ string, args ...string) (string, error) {
	s.ghCalls = append(s.ghCalls, append([]string(nil), args...))
	joined := strings.Join(args, " ")
	switch {
	case args[0] == "pr" && args[1] == "create":
		return "https://example.invalid/nova-tools/pull/42", nil
	case args[0] == "pr" && args[1] == "view":
		return `{"id":"PR_node_1","state":"OPEN"}`, nil
	case args[0] == "pr" && args[1] == "checks":
		return `[]`, nil
	case strings.Contains(joined, "enqueuePullRequest"):
		s.enqueued = true
		return `{"data":{"enqueuePullRequest":{"mergeQueueEntry":{"id":"MQE_1"}}}}`, nil
	case args[0] == "api":
		if !s.enqueued {
			return `{"data":{"node":{"mergeQueueEntry":null}}}`, nil
		}
		return `{"data":{"node":{"mergeQueueEntry":{"id":"MQE_1","state":"AWAITING_CHECKS"}}}}`, nil
	case args[0] == "run" && args[1] == "list":
		return `[{"databaseId":7,"conclusion":"failure","status":"completed","name":"ci"}]`, nil
	case args[0] == "run" && args[1] == "view":
		return s.groupLog, nil
	default:
		return "", errors.New("unexpected gh " + joined)
	}
}

func (s *promoteScript) ghFlag(cmd, flag string) string {
	for _, c := range s.ghCalls {
		if len(c) < 2 || c[0] != "pr" || c[1] != cmd {
			continue
		}
		for i, a := range c {
			if a == flag && i+1 < len(c) {
				return c[i+1]
			}
		}
	}
	return ""
}

func (s *promoteScript) ghHas(fragment string) bool {
	for _, c := range s.ghCalls {
		if strings.Contains(strings.Join(c, " "), fragment) {
			return true
		}
	}
	return false
}

// TestPromoteCutsAFrozenBranchAndNeverTheLiveTip is the promote step with fake
// git and gh: the cut branch name, the pull request head (the frozen branch,
// never the live sprint branch), the admission call with no strategy flag, and
// one judgment when a merge-group run fails.
func TestPromoteCutsAFrozenBranchAndNeverTheLiveTip(t *testing.T) {
	t.Parallel()
	const (
		live = "sprint/live"
		tip  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		base = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	groupLog := "running the tree gate\n--- FAIL: TestTree (0.02s)\nFAIL\t./cmd/nova-sprint\t0.02s\n"
	s := &promoteScript{
		live: live, tip: tip, baseSHA: base,
		logText:  "land s1-2 (sprint stream s1)\nland s1-1 (sprint stream s1)\n",
		groupLog: groupLog,
	}
	p := &promoter{
		dir: t.TempDir(), live: live, base: "dev",
		now:    time.Date(2026, 10, 4, 16, 0, 0, 0, time.UTC),
		gitRun: s.git, ghRun: s.gh,
		gate: func(_ context.Context, _ string, sha string) (string, error) {
			s.gated = sha
			return "", nil
		},
	}
	out, code := p.step(context.Background(), io.Discard, io.Discard)
	require.Equal(t, 1, code, "a failed merge-group run is the judgment, not a clean pass")
	require.Equal(t, "promo/2026-10-04-1", out.Branch, "the cut branch")
	require.Equal(t, "promo/2026-10-04-1", out.Head, "the pull request head")
	require.NotEqual(t, live, out.Head, "the live sprint branch is never the pull request head")
	require.Equal(t, tip, s.gated, "the tree gate runs on the frozen commit")
	require.Equal(t, []string{"s1-1", "s1-2"}, out.Cards)
	require.Contains(t, out.Body, "s1-1")
	require.Contains(t, out.Body, "s1-2")

	require.Equal(t, "promo/2026-10-04-1", s.ghFlag("create", "--head"))
	require.NotEqual(t, live, s.ghFlag("create", "--head"))
	require.Equal(t, "dev", s.ghFlag("create", "--base"))
	var pushed bool
	for _, c := range s.gitCalls {
		if c[0] != "push" {
			continue
		}
		pushed = true
		joined := strings.Join(c, " ")
		require.Contains(t, joined, "refs/heads/promo/2026-10-04-1:refs/heads/promo/2026-10-04-1")
		require.NotContains(t, joined, "refs/heads/"+live)
	}
	require.True(t, pushed, "the frozen branch is pushed: %v", s.gitCalls)

	require.True(t, s.ghHas("enqueuePullRequest"), "admission is the enqueue mutation, gh calls %v", s.ghCalls)
	require.True(t, s.ghHas("mergeQueueEntry"), "a query confirms the queue entry, gh calls %v", s.ghCalls)
	for _, c := range s.ghCalls {
		for _, a := range c {
			require.NotContains(t, []string{"--squash", "--rebase", "--merge"}, a, "no strategy flag: %v", c)
			require.False(t, strings.HasPrefix(a, "--a") && strings.Contains(a, "uto"), "no auto flag: %v", c)
		}
	}
	require.NotNil(t, out.Judgment, "one judgment")
	require.Equal(t, []string{"fix-and-recut", "skip"}, out.Judgment.Decisions)
	require.Contains(t, out.Judgment.Tail, "FAIL: TestTree")
	require.Empty(t, out.Promoted, "a failed merge-group run does not record promoted --sha")

	// the same branch does not raise a second judgment
	again, code := p.step(context.Background(), io.Discard, io.Discard)
	require.Equal(t, 1, code)
	require.Nil(t, again.Judgment, "the judgment was already raised")
}

// fakePromoteForge is a fake forge implementation for promote tests.
type fakePromoteForge struct {
	prNumber       string
	prID           string
	prState        string
	mergeSHA       string
	checksPassed   bool
	failedCheck    string
	entryID        string
	groupFailed    bool
	groupCheckName string
	groupLogText   string

	createdBase, createdHead, createdTitle, createdBody string
	enqueuedID                                          string
	onEnqueue                                           func(*fakePromoteForge)
}

func (f *fakePromoteForge) CreatePR(_ context.Context, base, head, title, body string) (string, error) {
	f.createdBase = base
	f.createdHead = head
	f.createdTitle = title
	f.createdBody = body
	if f.prNumber == "" {
		f.prNumber = "42"
	}
	return f.prNumber, nil
}

func (f *fakePromoteForge) PRView(_ context.Context, _ string) (PRView, error) {
	id := f.prID
	if id == "" {
		id = "PR_" + f.prNumber
	}
	state := f.prState
	if state == "" {
		state = "OPEN"
	}
	return PRView{
		ID:       id,
		State:    state,
		MergeSHA: f.mergeSHA,
	}, nil
}

func (f *fakePromoteForge) PRChecks(_ context.Context, _ string) (bool, string, error) {
	return f.checksPassed, f.failedCheck, nil
}

func (f *fakePromoteForge) Enqueue(_ context.Context, prID string) (string, error) {
	f.enqueuedID = prID
	if f.entryID == "" {
		f.entryID = "MQ_1"
	}
	if f.onEnqueue != nil {
		f.onEnqueue(f)
	}
	return f.entryID, nil
}

func (f *fakePromoteForge) ConfirmEntry(_ context.Context, _ string) (string, error) {
	if f.enqueuedID == "" {
		return "", nil
	}
	return f.entryID, nil
}

func (f *fakePromoteForge) MergeGroupStatus(_ context.Context, _ string) (bool, string, string, error) {
	return f.groupFailed, f.groupCheckName, f.groupLogText, nil
}

type promoteRig struct {
	t                 *testing.T
	dir, remote, work string
	env               []string
}

func newPromoteRig(t *testing.T) *promoteRig {
	t.Helper()
	dir := t.TempDir()
	r := &promoteRig{
		t:      t,
		dir:    dir,
		remote: filepath.Join(dir, "origin.git"),
		work:   filepath.Join(dir, "work"),
		env: []string{
			"GIT_CONFIG_GLOBAL=" + os.DevNull,
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=promoter",
			"GIT_AUTHOR_EMAIL=promoter@example.invalid",
			"GIT_COMMITTER_NAME=promoter",
			"GIT_COMMITTER_EMAIL=promoter@example.invalid",
		},
	}
	r.git("", "init", "-q", "--bare", "-b", "dev", r.remote)
	r.git("", "clone", "-q", r.remote, r.work)
	r.commit("README", "base\n", "base commit")
	r.git(r.work, "push", "-q", "origin", "HEAD:refs/heads/dev")
	return r
}

func (r *promoteRig) git(where string, args ...string) string {
	r.t.Helper()
	res, err := gitrun.Run(context.Background(), gitrun.Options{C: where, Env: r.env, OwnRepo: where != ""}, args...)
	require.NoError(r.t, err, "git %v: %s", args, res.Stderr)
	return strings.TrimSpace(string(res.Stdout))
}

func (r *promoteRig) commit(file, text, msg string) string {
	r.t.Helper()
	p := filepath.Join(r.work, file)
	require.NoError(r.t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(r.t, os.WriteFile(p, []byte(text), 0o600))
	r.git(r.work, "add", file)
	r.git(r.work, "commit", "-q", "-m", msg)
	return r.git(r.work, "rev-parse", "HEAD")
}

// runPromoteVerb runs `nova-sprint promote` for n passes of one verb on a, or on
// an app with no store when a is nil. The forge is the one the verb reads.
// promoteArmed stays unset: the verb calls the step.
func runPromoteVerb(t *testing.T, a *app, rig *promoteRig, forge *fakePromoteForge, now time.Time, passes int) (stdout, stderr string) {
	t.Helper()
	promoteForge = forge
	t.Cleanup(func() { promoteForge = nil })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	n := 0
	if a == nil {
		a = &app{getenv: func(string) string { return "" }, now: func() time.Time { return now }}
	}
	a.gitEnv = rig.env
	a.notify = func(context.Context) (context.Context, context.CancelFunc) {
		return ctx, func() {}
	}
	a.after = func(time.Duration) <-chan time.Time {
		n++
		if n >= passes {
			cancel()
		}
		ch := make(chan time.Time, 1)
		ch <- now
		return ch
	}
	var out, errb bytes.Buffer
	code := a.run([]string{
		"promote", "--every", "1h",
		"--repo-dir", rig.work,
		"--branch", "sprint/live",
		"--base", "dev",
	}, &out, &errb)
	require.Equal(t, 0, code, "stdout:\n%s\nstderr:\n%s", out.String(), errb.String())
	require.Nil(t, promoteArmed, "the promote verb does not set the land-loop hook")
	require.NotContains(t, errb.String(), "nova-sprint promote:", "stderr:\n%s", errb.String())
	return out.String(), errb.String()
}

func runPromoteDry(t *testing.T, rig *promoteRig, now time.Time) string {
	t.Helper()
	a := &app{
		getenv: func(string) string { return "" },
		now:    func() time.Time { return now },
		gitEnv: rig.env,
	}
	var out, errb bytes.Buffer
	code := a.run([]string{
		"promote", "--dry-run", "--every", "1h",
		"--repo-dir", rig.work,
		"--branch", "sprint/live",
		"--base", "dev",
	}, &out, &errb)
	require.Equal(t, 0, code, "stderr: %s", errb.String())
	require.Nil(t, promoteArmed)
	return out.String()
}

// TestPromoteCarriesACutToARecordedPromotion tests the promote verb with a twin
// repository and a fake forge:
// 1. A clean cut is merged with the target, opened, queued and recorded.
// 2. A conflicting target stops with the judgment naming the files.
// 3. A failed queue run raises one judgment naming the failing check.
func TestPromoteCarriesACutToARecordedPromotion(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

	// 1. Clean cut is merged with the target, opened, queued and recorded
	t.Run("Clean", func(t *testing.T) {
		rig := newPromoteRig(t)
		ta := newTestApp(t)
		ta.ok("init --readers reader-a,reader-b --members m1")
		cut := "promo/" + ta.a.now().Format("2006-01-02") + "-1"
		rig.git(rig.work, "checkout", "-q", "-b", "sprint/live")
		rig.commit("card-1.txt", "card 1 work\n", "land s1-1 (sprint stream s1)")
		rig.commit("card-2.txt", "card 2 work\n", "land s1-2 (sprint stream s1)")
		rig.git(rig.work, "push", "-q", "origin", "HEAD:refs/heads/sprint/live")

		// Make a commit on origin's dev so the target branch has work to merge
		// into the cut; the clone's own dev is left behind it, so only a fetch
		// of the target brings it in.
		rig.git(rig.work, "checkout", "-q", "dev")
		rig.commit("dev-file.txt", "dev base work\n", "target dev commit")
		rig.git(rig.work, "push", "-q", "origin", "HEAD:refs/heads/dev")
		rig.git(rig.work, "reset", "-q", "--hard", "HEAD~1")
		rig.git(rig.work, "update-ref", "-d", "refs/remotes/origin/dev")
		rig.git(rig.work, "checkout", "-q", "sprint/live")

		forge := &fakePromoteForge{
			prNumber:     "101",
			prID:         "PR_node_101",
			checksPassed: true,
			entryID:      "MQE_101",
			onEnqueue: func(f *fakePromoteForge) {
				f.prState = "MERGED"
				f.mergeSHA = rig.git(rig.work, "rev-parse", "refs/heads/"+cut)
			},
		}

		stdout, _ := runPromoteVerb(t, ta.a, rig, forge, now, 1)
		recorded := rig.git(rig.work, "rev-parse", "refs/promoted/last")
		require.Equal(t, forge.mergeSHA, recorded)
		require.Contains(t, stdout, "promoted the sprint branch into dev", "the promoted step ran on the sprint's store")
		kept, err := ta.m.Snapshot()
		require.NoError(t, err)
		require.Contains(t, string(kept), recorded, "the store holds the merge as promoted --sha records it")
		rig.git(rig.remote, "cat-file", "-e", "refs/heads/"+cut+":dev-file.txt")
		require.Contains(t, stdout, "PROMOTE CUT")
		require.Contains(t, stdout, "PROMOTE QUEUE")
		require.Contains(t, stdout, "promoted --sha "+recorded)
		require.NotContains(t, stdout, "JUDGMENT")

		originCut := rig.git(rig.remote, "log", "-1", "--format=%s", "refs/heads/"+cut)
		require.Contains(t, originCut, "merge dev into "+cut)

		currentBranch := rig.git(rig.work, "symbolic-ref", "--short", "HEAD")
		require.Equal(t, "sprint/live", currentBranch)
		require.Equal(t, "dev", forge.createdBase)
		require.Equal(t, cut, forge.createdHead)
		require.Equal(t, "PR_node_101", forge.enqueuedID)
		ta.clean()
	})

	// 2. Conflicting target stops with the judgment naming the files
	t.Run("Conflict", func(t *testing.T) {
		rig := newPromoteRig(t)
		rig.git(rig.work, "checkout", "-q", "-b", "sprint/live")
		rig.commit("conflict.txt", "live stream line\n", "land s1-1 (sprint stream s1)")
		rig.git(rig.work, "push", "-q", "origin", "HEAD:refs/heads/sprint/live")

		// Conflicting change on target branch dev
		rig.git(rig.work, "checkout", "-q", "dev")
		rig.commit("conflict.txt", "divergent dev line\n", "dev conflicting commit")
		rig.git(rig.work, "push", "-q", "origin", "HEAD:refs/heads/dev")
		rig.git(rig.work, "checkout", "-q", "sprint/live")

		reflog := rig.git(rig.work, "reflog", "--format=%gs")
		dry := runPromoteDry(t, rig, now)
		require.Contains(t, dry, "PROMOTE DRY-RUN")
		require.Contains(t, dry, "nothing was cut")
		require.Equal(t, reflog, rig.git(rig.work, "reflog", "--format=%gs"), "dry-run checks nothing out")
		require.Empty(t, rig.git(rig.work, "branch", "--list", "promo/*"))
		require.Equal(t, "sprint/live", rig.git(rig.work, "symbolic-ref", "--short", "HEAD"))

		forge := &fakePromoteForge{checksPassed: true}
		stdout, _ := runPromoteVerb(t, nil, rig, forge, now, 2)
		require.Equal(t, 1, strings.Count(stdout, "JUDGMENT conflict"), stdout)
		require.Contains(t, stdout, "files=conflict.txt")
		require.Contains(t, stdout, "decisions=fix-and-recut,skip")
		require.Contains(t, stdout, "judgment=already")
		require.NotContains(t, stdout, "promo/2026-10-05-2")
		require.NotContains(t, stdout, "promoted --sha")
		require.Empty(t, forge.createdHead, "no pull request on a conflict")
		require.Empty(t, rig.git(rig.work, "branch", "--list", "promo/*"), "the cut branch is deleted")
		require.Equal(t, "sprint/live", rig.git(rig.work, "symbolic-ref", "--short", "HEAD"))
		require.Empty(t, rig.git(rig.work, "status", "--porcelain"))
	})

	// 3. Failed queue run raises one judgment naming the failing check
	t.Run("FailedQueueRun", func(t *testing.T) {
		rig := newPromoteRig(t)
		rig.git(rig.work, "checkout", "-q", "-b", "sprint/live")
		rig.commit("file.txt", "clean line\n", "land s1-1 (sprint stream s1)")
		rig.git(rig.work, "push", "-q", "origin", "HEAD:refs/heads/sprint/live")

		forge := &fakePromoteForge{
			prNumber:       "102",
			prID:           "PR_node_102",
			prState:        "OPEN",
			checksPassed:   true,
			entryID:        "MQE_102",
			groupFailed:    true,
			groupCheckName: "ci/functional-gate",
			groupLogText:   "FAIL: TestFunctionalFailure (1.23s)\n",
		}

		stdout, _ := runPromoteVerb(t, nil, rig, forge, now, 1)
		require.Contains(t, stdout, "JUDGMENT merge-group failed")
		require.Contains(t, stdout, "check=ci/functional-gate")
		require.Contains(t, stdout, "FAIL: TestFunctionalFailure")
		require.Contains(t, stdout, "decisions=fix-and-recut,skip")
		require.NotContains(t, stdout, "promoted --sha")
		require.Equal(t, "promo/2026-10-05-1", forge.createdHead)
		require.Equal(t, "PR_node_102", forge.enqueuedID)
		require.Equal(t, "sprint/live", rig.git(rig.work, "symbolic-ref", "--short", "HEAD"))
	})
}
