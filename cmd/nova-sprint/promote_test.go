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
	"github.com/mas-bandwidth/nova-tools/internal/testgit"
)

// promoteScript is the fake git and gh the step talks to. It records every
// call and answers the failure path: the pull request is opened, admitted with
// no strategy flag, and a merge-group run has failed.
type promoteScript struct {
	live, tip, baseSHA, logText, groupLog string
	gitCalls, ghCalls                     [][]string
	gated                                 string
	cfg                                   map[string]string
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
			if strings.HasPrefix(rev, "promo/") || strings.HasPrefix(rev, "promote/") {
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
	case "push", "update-ref", "fetch":
		return "", nil
	default:
		return "", errors.New("unexpected git " + strings.Join(args, " "))
	}
}

func (s *promoteScript) gh(_ context.Context, _ string, args ...string) (string, error) {
	s.ghCalls = append(s.ghCalls, append([]string(nil), args...))
	if s.cfg == nil {
		s.cfg = map[string]string{}
	}
	joined := strings.Join(args, " ")
	switch {
	case args[0] == "pr" && args[1] == "create":
		return "https://example.invalid/nova-tools/pull/42", nil
	case args[0] == "pr" && args[1] == "view":
		return `{"id":"PR_node_1","state":"OPEN"}`, nil
	case args[0] == "pr" && args[1] == "checks":
		return `[{"name":"ci","bucket":"pass"}]`, nil
	case strings.Contains(joined, "enqueuePullRequest"):
		s.cfg["enqueued"] = "1"
		return `{"data":{"enqueuePullRequest":{"mergeQueueEntry":{"id":"MQE_1"}}}}`, nil
	case args[0] == "api":
		if s.cfg["enqueued"] != "1" {
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
	require.Equal(t, "promote/2026-10-04-1", out.Branch, "the cut branch")
	require.Equal(t, "promote/2026-10-04-1", out.Head, "the pull request head")
	require.NotEqual(t, live, out.Head, "the live sprint branch is never the pull request head")
	require.Equal(t, tip, s.gated, "the tree gate runs on the frozen commit")
	require.Equal(t, []string{"s1-1", "s1-2"}, out.Cards)
	require.Contains(t, out.Body, "s1-1")
	require.Contains(t, out.Body, "s1-2")

	require.Equal(t, "promote/2026-10-04-1", s.ghFlag("create", "--head"))
	require.NotEqual(t, live, s.ghFlag("create", "--head"))
	require.Equal(t, "dev", s.ghFlag("create", "--base"))
	var pushed bool
	for _, c := range s.gitCalls {
		if c[0] != "push" {
			continue
		}
		pushed = true
		joined := strings.Join(c, " ")
		require.Contains(t, joined, "refs/heads/promote/2026-10-04-1:refs/heads/promote/2026-10-04-1")
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

// fakePromoForge simulates the forge for promote: opening a PR, passing CI checks,
// admitting the PR to the merge queue, creating the merge commit on the base branch
// of the remote bare repository, and auto-deleting the throwaway head branch.
type fakePromoForge struct {
	bare      string
	head      string
	base      string
	mergedSHA string
	enqueued  bool
	env       []string
}

func (f *fakePromoForge) OpenPR(ctx context.Context, base, head, title, body string) (string, error) {
	f.base = base
	f.head = head
	return "101", nil
}

func (f *fakePromoForge) View(ctx context.Context, number string) (prView, error) {
	return prView{ID: "PR_101", Merged: f.mergedSHA}, nil
}

func (f *fakePromoForge) Checks(ctx context.Context, number string) (prChecks, error) {
	return prChecks{State: checksPass, Name: "ci"}, nil
}

func (f *fakePromoForge) Confirm(ctx context.Context, id string) (string, error) {
	if !f.enqueued {
		return "", nil
	}
	return "MQE_101", nil
}

func (f *fakePromoForge) Enqueue(ctx context.Context, id string) (string, error) {
	f.enqueued = true
	devTip, err := gitrun.Run(ctx, gitrun.Options{C: f.bare, Env: f.env, OwnRepo: true}, "rev-parse", "refs/heads/"+f.base)
	if err != nil {
		return "", err
	}
	headTip, err := gitrun.Run(ctx, gitrun.Options{C: f.bare, Env: f.env, OwnRepo: true}, "rev-parse", "refs/heads/"+f.head)
	if err != nil {
		return "", err
	}
	tree, err := gitrun.Run(ctx, gitrun.Options{C: f.bare, Env: f.env, OwnRepo: true}, "rev-parse", "refs/heads/"+f.head+"^{tree}")
	if err != nil {
		return "", err
	}
	mCommit, err := gitrun.Run(ctx, gitrun.Options{C: f.bare, Env: f.env, OwnRepo: true}, "commit-tree", strings.TrimSpace(string(tree.Stdout)), "-p", strings.TrimSpace(string(devTip.Stdout)), "-p", strings.TrimSpace(string(headTip.Stdout)), "-m", "Merge pull request #101 from "+f.head)
	if err != nil {
		return "", err
	}
	f.mergedSHA = strings.TrimSpace(string(mCommit.Stdout))
	if _, err := gitrun.Run(ctx, gitrun.Options{C: f.bare, Env: f.env, OwnRepo: true}, "update-ref", "refs/heads/"+f.base, f.mergedSHA); err != nil {
		return "", err
	}
	// GitHub automatically deletes head branches upon merge:
	if _, err := gitrun.Run(ctx, gitrun.Options{C: f.bare, Env: f.env, OwnRepo: true}, "branch", "-D", f.head); err != nil {
		return "", err
	}
	return "MQE_101", nil
}

func (f *fakePromoForge) FailedRuns(ctx context.Context, filter ...string) ([]ghRunRow, error) {
	return nil, nil
}

func (f *fakePromoForge) RunLog(ctx context.Context, id int) (string, error) {
	return "", nil
}

func (f *fakePromoForge) Repo(ctx context.Context) (string, error) {
	return "mas-bandwidth/nova", nil
}

// TestPromoteMergesFromAThrowawayBranchAndKeepsTheBase tests that promote cuts
// a throwaway branch promote/<date>-<n> from the base tip, gates the base tip before
// pushing, pushes the throwaway branch, opens a PR against dev, merges it, updates
// refs/promoted/last, and that GitHub auto-deleting the merged PR head leaves the base
// sprint branch intact on origin.
func TestPromoteMergesFromAThrowawayBranchAndKeepsTheBase(t *testing.T) {
	t.Parallel()
	require.Contains(t, promoteCheckDefault, "make test-functional-container PKGS=./...", "the whole-tree gate includes the functional tier")
	root := t.TempDir()
	bare := filepath.Join(root, "origin.git")
	dir := filepath.Join(root, "work")
	require.NoError(t, os.Mkdir(bare, 0o755))
	require.NoError(t, os.Mkdir(dir, 0o755))

	env := testgit.Environ("GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	git := func(where string, args ...string) string {
		t.Helper()
		res, err := gitrun.Run(context.Background(), gitrun.Options{C: where, Env: testgit.Environ("GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1"), OwnRepo: true}, args...)
		require.NoError(t, err, "git %v\n%s\n%s", args, res.Stderr, res.Stdout)
		return strings.TrimSpace(string(res.Stdout))
	}
	gitErr := func(where string, args ...string) error {
		t.Helper()
		_, err := gitrun.Run(context.Background(), gitrun.Options{C: where, Env: testgit.Environ("GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1"), OwnRepo: true}, args...)
		return err
	}
	syncRemote := func(ref string) {
		t.Helper()
		git(bare, "fetch", "-q", dir, "refs/heads/"+ref+":refs/heads/"+ref)
	}

	// Initialize bare origin repository with dev branch
	git(bare, "init", "-q", "--bare", "-b", "dev")
	// Initialize local work repo
	git(dir, "init", "-q", "-b", "dev")
	git(dir, "remote", "add", "origin", bare)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# dev base\n"), 0o644))
	git(dir, "add", "README.md")
	git(dir, "commit", "-q", "-m", "init dev")
	syncRemote("dev")

	// Cut live sprint branch from dev
	const live = "sprint/live"
	git(dir, "checkout", "-q", "-b", live)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c1.txt"), []byte("card 1\n"), 0o644))
	git(dir, "add", "c1.txt")
	git(dir, "commit", "-q", "-m", "land card-1 (sprint stream s1)")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c2.txt"), []byte("card 2\n"), 0o644))
	git(dir, "add", "c2.txt")
	git(dir, "commit", "-q", "-m", "land card-2 (sprint stream s1)")
	syncRemote(live)
	tip := git(dir, "rev-parse", "HEAD")
	gitRun := func(ctx context.Context, where string, args ...string) (string, error) {
		if len(args) > 0 && args[0] == "push" {
			if len(args) != 3 || args[1] != "origin" {
				return "", errors.New("unexpected push " + strings.Join(args, " "))
			}
			return git(bare, "fetch", "-q", where, args[2]), nil
		}
		res, err := gitrun.Run(ctx, gitrun.Options{C: where, Env: env, OwnRepo: true}, args...)
		return strings.TrimSpace(string(res.Stdout)), err
	}

	forge := &fakePromoForge{bare: bare, env: env}
	var gatedSHA string
	p := &promoter{
		dir:    dir,
		live:   live,
		base:   "dev",
		env:    env,
		forge:  forge,
		gitRun: gitRun,
		now:    time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC),
		gate: func(_ context.Context, _ string, sha string) (string, error) {
			gatedSHA = sha
			return "", nil
		},
	}

	var stdout, stderr bytes.Buffer
	out, code := p.step(context.Background(), &stdout, &stderr)
	require.Zero(t, code, "promote step should succeed: %s\n%s", stdout.String(), stderr.String())

	// 1. Gating ran on the base tip before pushing
	require.Equal(t, tip, gatedSHA, "gate checked the base tip")

	// 2. The PR head is a throwaway branch, NEVER the live base branch
	require.True(t, strings.HasPrefix(out.Branch, "promote/2026-10-06-"), "branch prefix must be promote/<date>-<n>, got %s", out.Branch)
	require.NotEqual(t, live, out.Head, "PR head must not be the live base branch")
	require.Equal(t, out.Branch, forge.head, "forge PR head was the throwaway branch")

	// 3. The PR merged into dev and was recorded
	require.NotEmpty(t, out.Promoted, "promote recorded the merge SHA")
	require.Equal(t, forge.mergedSHA, out.Promoted, "promoted SHA matches forge merge SHA")
	localPromoted := git(dir, "rev-parse", "refs/promoted/last")
	require.Equal(t, forge.mergedSHA, localPromoted, "refs/promoted/last was updated")

	// 4. Output reports promoted --sha
	require.Contains(t, stdout.String(), "promoted --sha "+forge.mergedSHA)

	// 5. CRITICAL: The base branch on origin remains intact at its tip!
	originLiveTip := git(bare, "rev-parse", "refs/heads/"+live)
	require.Equal(t, tip, originLiveTip, "the base branch sprint/live remains intact at tip on origin")

	// 6. The throwaway branch on origin was deleted by forge auto-delete
	err := gitErr(bare, "rev-parse", "--verify", "refs/heads/"+out.Branch)
	require.Error(t, err, "throwaway branch %s was deleted on origin", out.Branch)
}

// TestPromoteCheckHasAWholeTreeGateDefault pins that --check defaults to a
// real whole-tree gate (build, vet, every package's tests), so an unset check
// gates the base tip instead of skipping it.
func TestPromoteCheckHasAWholeTreeGateDefault(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	fs, _ := ta.a.verbSetup("promote")
	def := *promoteCheckFlag(fs)
	require.Equal(t, promoteCheckDefault, def, "--check's default is the whole-tree gate")
	require.Contains(t, def, "go build ./...", "the gate builds the tree")
	require.Contains(t, def, "go vet ./...", "the gate vets the tree")
	require.Contains(t, def, "go test ./...", "the gate runs every package's tests")
	require.Contains(t, def, "make test-functional-container PKGS=./...", "the gate runs the functional tier in its container")
}

// TestPromoteRefusesAnEmptyCheckInsteadOfSkippingIt pins that a promoter with
// no injected gate and an empty --check refuses to promote: an unset check is
// never a silent skip.
func TestPromoteRefusesAnEmptyCheckInsteadOfSkippingIt(t *testing.T) {
	t.Parallel()
	p := &promoter{dir: t.TempDir()}
	_, err := p.runGate(context.Background(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	require.Error(t, err, "an empty check must refuse, not return an empty gate")
	require.Contains(t, err.Error(), "no tree gate", "the refusal names the missing gate")
}
