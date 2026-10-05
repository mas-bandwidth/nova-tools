package main

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// TestLandRefusesAnEmptyCommitThatClaimsChanges tests that checkEmptyCommit refuses
// an empty commit whose RESULT.md claims changes (docs/SPEC-SPRINT.md section 7, the lander's
// checks): given an empty diff and a claiming RESULT.md it refuses with the finding
// "empty commit: the result claims changes the diff does not show", given an empty diff
// and a nothing-to-do result it passes, and given a non-empty diff it passes.
func TestLandRefusesAnEmptyCommitThatClaimsChanges(t *testing.T) {
	t.Parallel()

	const wantFinding = "empty commit: the result claims changes the diff does not show"

	t.Run("empty diff with claiming result refuses", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name   string
			result string
		}{
			{
				name:   "verdict ok",
				result: "head: 2d720d219ff5\nbranch: sprint/c1.w1\nverdict: ok\nreport: done\n",
			},
			{
				name:   "step line with commit sha",
				result: "STEP 1. Commit 2d720d219ff5\n",
			},
			{
				name:   "non-empty diff stat files changed",
				result: "diff stat: 1 file changed, 5 insertions(+)\n",
			},
			{
				name:   "nothing-to-do in prose beside verdict ok",
				result: "verdict: ok\nhead: 2d720d219ff5\nI found nothing to do about the lint, then changed land.go\n",
			},
			{
				name:   "nothing-to-do in prose beside a step sha",
				result: "STEP 1. Commit 2d720d219ff5\nthere was nothing to do after that\n",
			},
			{
				name:   "no result text at all fails closed",
				result: "",
			},
			{
				name:   "non-empty diff stat table row",
				result: "cmd/nova-sprint/land.go | 10 ++++++++++\n",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got := checkEmptyCommit(true, tc.result)
				assert.Equal(t, wantFinding, got)
			})
		}
	})

	t.Run("empty diff with nothing-to-do result passes", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name   string
			result string
		}{
			{
				name:   "nothing-to-do report line",
				result: "verdict: nothing\nhead: 2d720d219ff5\nnothing to do: already clean\n",
			},
			{
				name:   "nothing-to-do report field",
				result: "head: 2d720d219ff5\nreport: nothing-to-do at this head\n",
			},
			{
				name:   "every step dash",
				result: "STEP 1. -\nSTEP 2. -\n",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got := checkEmptyCommit(true, tc.result)
				assert.Empty(t, got)
			})
		}
	})

	t.Run("an all-letter word is not a commit sha", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, checkEmptyCommit(true, "STEP 1. decade defaced -\n"))
		assert.Empty(t, checkEmptyCommit(true, "STEP 1. facade -\nSTEP 2. -\n"))
	})

	t.Run("claimText carries the recorded finish", func(t *testing.T) {
		t.Parallel()
		ok := &sprint.Card{Fields: map[string]string{"ok": "yes", "head": "2d720d219ff5", "report": "done"}}
		assert.Equal(t, wantFinding, checkEmptyCommit(true, claimText(ok)))
		bare := &sprint.Card{Fields: map[string]string{"ok": "yes", "head": "2d720d219ff5"}}
		assert.Equal(t, wantFinding, checkEmptyCommit(true, claimText(bare)), "ok with no report still claims")
		none := &sprint.Card{Fields: map[string]string{"ok": "yes", "head": "2d720d219ff5", "report": "nothing to do: the card was right"}}
		assert.Empty(t, checkEmptyCommit(true, claimText(none)))
		assert.Equal(t, wantFinding, checkEmptyCommit(true, claimText(nil)), "no work card fails closed")
		dash := &sprint.Card{Fields: map[string]string{"ok": "yes", "report": "STEP 1. -\nSTEP 2. -\n"}}
		assert.Empty(t, checkEmptyCommit(true, claimText(dash)), "dash-only result claims no change")
	})

	t.Run("non-empty diff passes", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name   string
			result string
		}{
			{
				name:   "claiming verdict ok with non-empty diff",
				result: "verdict: ok\n1 file changed, 10 insertions(+)\n",
			},
			{
				name:   "step sha with non-empty diff",
				result: "STEP 1. Commit 2d720d219ff5\n",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				got := checkEmptyCommit(false, tc.result)
				assert.Empty(t, got)
			})
		}
	})
}

// TestLandEmptyCommitClaimsCannotHideBehindNothing pins the claimed changes even
// when a report labels itself nothing-to-do (SPEC-SPRINT section 7).
func TestLandEmptyCommitClaimsCannotHideBehindNothing(t *testing.T) {
	t.Parallel()
	for _, result := range []string{
		"verdict: nothing\nSTEP 1. Commit 2d720d219ff5\n",
		"nothing to do: already done\n1 file changed, 2 insertions(+)\n",
		"STEP 1. -\n1 file changed, 2 insertions(+)\n",
	} {
		assert.NotEmpty(t, checkEmptyCommit(true, result), result)
	}
}

// TestLandEmptyCommitDiffErrorIsEnvironmental prevents an unreadable start from
// authorizing a landing; no card is blamed (SPEC-SPRINT section 7).
func TestLandEmptyCommitDiffErrorIsEnvironmental(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	before := r.git(r.worker, "rev-parse", "HEAD")
	head := r.head("c1", "main", "changed.txt", "changed\n")
	l := &lander{a: r.a, diffs: map[string]string{}, scope: map[string][]string{}}
	card, env, empty := l.checkCard(context.Background(), r.worker,
		landCard{id: "c1", head: head, start: strings.Repeat("a", 40), base: "main", result: "verdict: ok"}, before)
	assert.Empty(t, card)
	assert.False(t, empty)
	assert.Contains(t, env, "start-to-head diff")
	assert.Empty(t, l.diffs)
}

func TestLandEmptyCommitLandingPath(t *testing.T) {
	t.Parallel()
	exerciseEmptyCommitLanding(t)
}

// exerciseEmptyCommitLanding drives the public land verb with local git and the
// in-memory store, checking the remote and recorded state (SPEC-SPRINT section 7).
func exerciseEmptyCommitLanding(t *testing.T) {
	t.Helper()
	for _, tc := range []struct {
		name, report, stageOverride          string
		changed, staged, refuse, environment bool
	}{
		{name: "empty claiming", report: "done: 1 file changed, 10 insertions", refuse: true},
		{name: "empty nothing", report: "nothing to do: already clean"},
		{name: "empty dash steps", report: "STEP 1. -"},
		{name: "changed claiming", report: "done: 1 file changed", changed: true},
		{name: "mixed nothing and claim", report: "nothing to do: 1 file changed", refuse: true},
		{name: "staged carry but no new work", report: "done: 1 file changed", staged: true, refuse: true},
		{name: "malformed stage", report: "done", staged: true, stageOverride: "not-a-sha", environment: true},
		{name: "missing stage object", report: "done", staged: true, stageOverride: strings.Repeat("a", 40), environment: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newLandRig(t)
			r.ok("add --stream s1 --count 1 --one")
			r.git(r.worker, "switch", "-q", "--no-track", "-c", "sprint/s1-1", "refs/remotes/origin/main")
			report := tc.report
			if tc.staged {
				staged := r.commit("carried.txt", "prior work\n", "staged carry")
				if tc.stageOverride != "" {
					staged = tc.stageOverride
				}
				report = "stage: staged=" + staged + " tip=" + r.git(r.remote, "rev-parse", "main") + " of main carry=carried attempt=1 prev=" + r.git(r.remote, "rev-parse", "main") + "; " + report
			}
			if tc.changed {
				r.commit("change.txt", "new work\n", "work")
			} else {
				r.git(r.worker, "commit", "--allow-empty", "-q", "-m", "empty head")
			}
			head := r.git(r.worker, "rev-parse", "HEAD")
			if tc.staged {
				report = "pushed=" + head + " to sprint/s1-1: " + report
			}
			r.git(r.worker, "push", "-q", "origin", "refs/heads/sprint/s1-1:refs/heads/sprint/s1-1")
			r.deal(1)
			r.ok("take --as m1 --limit 100")
			r.ok("finish --as m1 s1-1.w1@1 --head " + head + " --report '" + report + "'")
			r.ok("ask")
			r.ok("read --as reader-a --ok --limit 100")
			r.ok("read --as reader-b --ok --limit 100")
			r.ok("accept --read-ok")
			r.markProtected()
			before := r.git(r.remote, "rev-parse", "main")
			code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
			if tc.environment {
				require.NotEqual(t, 0, code, out+errs)
				assert.Contains(t, errs, "start-to-head diff")
				assert.NotContains(t, errs, "empty commit: the result claims")
				assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"))
				assert.Equal(t, sprint.Merging, r.primary("s1-1").Col)
			} else if tc.refuse {
				require.Equal(t, 1, code, out+errs)
				assert.Contains(t, errs, "empty commit: the result claims changes the diff does not show")
				assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"))
				pr := r.primary("s1-1")
				assert.Equal(t, sprint.Review, pr.Col)
				assert.Equal(t, "empty commit: the result claims changes the diff does not show", pr.F("return_reason"))
			} else {
				require.Equal(t, 0, code, out+errs)
				assert.Equal(t, sprint.Landed, r.primary("s1-1").Col)
				assert.NotEqual(t, before, r.git(r.remote, "rev-parse", "main"))
			}
			r.clean()
		})
	}
}
