package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// The base-gate rule (the coordinator, 2026-10-04, measured at 1:28 PM: five merge streams stopped on
// "go build ./...: package internal/runtime/gc/scan is not in std" from the toolchain, a
// transient failure of the lander's tree gate on base dev; the same build passed at 1:44 PM
// and the streams sat 16 minutes until a person resumed them). A base that fails its tree
// gate is gated again after 2 minutes, and again after 5; only the third failure stops the
// stream, and the stop is the coordinator's judgment with the error. A green base is
// gated once (the cache). docs/SPEC-SPRINT.md section 8, answered by rule.
func TestABaseTreeGateFailureIsRetriedBeforeTheStreamStops(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for f, content := range goModule {
		p := filepath.Join(dir, f)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	}
	mainGo := filepath.Join(dir, "main.go")
	now := time.Date(2026, 10, 4, 17, 28, 0, 0, time.UTC)
	l := &lander{baseGateCache: map[string]string{}, baseGateFails: map[string]*baseGateFail{}, now: func() time.Time { return now }}
	ctx := context.Background()

	require.NoError(t, os.WriteFile(mainGo, []byte(buildRed), 0o600))
	why, stop := l.treeGateBase(ctx, dir, "dev-1")
	assert.Contains(t, why, "syntax error")
	assert.Contains(t, why, "gated again at 17:30:00 UTC (failure 1 of 3)")
	assert.False(t, stop, "the first failure stops nothing")
	// inside the retry, nothing is run: the failure is said again with its retry
	require.NoError(t, os.WriteFile(mainGo, []byte(goModule["main.go"]), 0o600))
	now = now.Add(time.Minute)
	why, stop = l.treeGateBase(ctx, dir, "dev-1")
	assert.Contains(t, why, "gated again at 17:30:00 UTC")
	assert.False(t, stop)
	// at the retry the base is green again: the transient failure is forgotten
	now = now.Add(time.Minute)
	why, stop = l.treeGateBase(ctx, dir, "dev-1")
	assert.Equal(t, "", why)
	assert.False(t, stop)

	// a base that stays red: 2 minutes, then 5, then the stop
	require.NoError(t, os.WriteFile(mainGo, []byte(buildRed), 0o600))
	_, stop = l.treeGateBase(ctx, dir, "dev-2")
	assert.False(t, stop)
	now = now.Add(sprint.BaseGateRetries[0])
	why, stop = l.treeGateBase(ctx, dir, "dev-2")
	assert.Contains(t, why, "(failure 2 of 3)")
	assert.False(t, stop)
	now = now.Add(sprint.BaseGateRetries[1])
	why, stop = l.treeGateBase(ctx, dir, "dev-2")
	assert.True(t, stop, "the third failure stops the stream")
	assert.Contains(t, why, "failed its tree gate 3 times")
	_, stop = l.treeGateBase(ctx, dir, "dev-2")
	assert.True(t, stop, "every stream on that base stops, each with the error")
}

// With the rule off the base's failure is cached for its commit, as before the rule: every
// landing on it is refused, and no stream stops.
func TestTheBaseGateRuleOffCachesTheFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for f, content := range goModule {
		p := filepath.Join(dir, f)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(buildRed), 0o600))
	now := time.Date(2026, 10, 4, 17, 28, 0, 0, time.UTC)
	l := &lander{baseGateCache: map[string]string{}, baseGateFails: map[string]*baseGateFail{}, now: func() time.Time { return now }, rulesOff: []string{sprint.RuleBaseGate}}
	why, stop := l.treeGateBase(context.Background(), dir, "dev-1")
	assert.Contains(t, why, "syntax error")
	assert.False(t, stop)
	now = now.Add(time.Hour)
	again, stop := l.treeGateBase(context.Background(), dir, "dev-1")
	assert.Equal(t, why, again, "cached for its commit")
	assert.False(t, stop)
}

// The base's third failure stops the stream through the merge step: a judgment with the
// error, the cards still queued.
func TestTheBaseStopIsAJudgmentWithTheError(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1,m2 --readers reader-a,reader-b,reader-c")
	ta.ok("add --stream s1 --count 1 --one")
	ta.ok("merge --stream s1 --base-red 'the base dev failed its tree gate 3 times: go build ./...: package internal/runtime/gc/scan is not in std'")
	out := ta.ok("inbox")
	assert.Contains(t, out, sprint.NBaseRed)
	assert.Contains(t, out, "package internal/runtime/gc/scan is not in std")
	ta.ok("resume --stream s1 --did 'the base is green again'")
	ta.clean()
}

// Through land: a red base is refused twice, gated again at 2 and 5 minutes, and its third
// failure stops the stream with the error, the cards still queued.
func TestLandStopsAStreamOnlyOnTheBasesThirdFailure(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.files("the base's change", map[string]string{"bad.go": vetRed})
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	r.ok("add --stream s1 --count 2")
	heads := map[string]string{"s1-1": r.card("s1-1", map[string]string{"ok.go": "package main\n\nfunc ok() {}\n"}), "s1-2": r.card("s1-2", map[string]string{"NOTES.md": "fine too\n"})}
	r.queued(heads, "s1-1", "s1-2")
	land := func() (int, string) {
		code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
		return code, out + errs
	}
	code, out := land()
	assert.Equal(t, 1, code, out)
	assert.Contains(t, out, "(failure 1 of 3)")
	assert.Equal(t, "merging", r.streamState("s1"), "no stop on the first failure")
	r.after(sprint.BaseGateRetries[0])
	_, out = land()
	assert.Contains(t, out, "(failure 2 of 3)")
	r.after(sprint.BaseGateRetries[1])
	_, out = land()
	assert.Contains(t, out, "fact=base")
	assert.Equal(t, "stopped base", r.streamState("s1"), "the third failure stops the stream")
	assert.Equal(t, map[string]string{"s1-1": "merging/queued", "s1-2": "merging/queued"}, r.places("s1-1", "s1-2"), "no card is blamed")
	inbox := r.ok("inbox")
	assert.Contains(t, inbox, sprint.NBaseRed)
	assert.Contains(t, inbox, "failed its tree gate 3 times")
}
