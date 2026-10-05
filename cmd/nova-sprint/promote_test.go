package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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
		case s.live, "refs/heads/" + s.live, "refs/remotes/origin/" + s.live, "origin/" + s.live:
			return s.tip, nil
		case "dev", "refs/heads/dev", "refs/remotes/origin/dev", "origin/dev":
			return s.baseSHA, nil
		case "refs/promoted/last":
			return "", errors.New("missing")
		default:
			if strings.Contains(rev, "promo/") {
				return s.tip, nil
			}
			return "", errors.New("unknown rev " + rev)
		}
	case "log":
		return s.logText, nil
	case "branch", "fetch", "checkout", "merge":
		return "", nil
	case "rev-list":
		if s.tip == s.baseSHA {
			return "0", nil
		}
		return "2", nil
	case "config":
		return s.config(args)
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
	case strings.Contains(joined, "enqueuePullRequest"):
		return `{"data":{"enqueuePullRequest":{"mergeQueueEntry":{"id":"MQE_1"}}}}`, nil
	case args[0] == "api":
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
