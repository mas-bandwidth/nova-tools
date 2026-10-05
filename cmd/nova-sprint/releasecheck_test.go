package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// release check (docs/SPEC-SPRINT.md, subsection release-check-frame): a read that prints one
// RELEASE CHECK line per check and RELEASE OK or RELEASE NOT READY, on the twin store, with
// the clock the test moves and no socket.

func TestReleaseCheckOnAQuietSprintIsOK(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead")
	code, out, errs := ta.do("release check")
	require.Equal(t, 0, code, "%s %s", out, errs)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Len(t, lines, 2, out)
	assert.True(t, strings.HasPrefix(lines[0], "RELEASE CHECK no-stuck-friend ok "), lines[0])
	assert.Equal(t, "RELEASE OK checks=1", lines[1])

	var rep sprint.ReleaseReport
	ta.json("release check", &rep)
	assert.True(t, rep.Ready)
	assert.Equal(t, 1, rep.Checks)
	assert.Equal(t, "RELEASE OK checks=1", rep.Summary)
}

func TestReleaseCheckNamesAFriendStuckInTheLastFourHours(t *testing.T) {
	t.Parallel()
	ta, _ := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	before := ta.applies()
	ta.a.sleep(3 * time.Hour) // her card, taken at the deal, is past its two hours since the second
	code, out, errs := ta.do("release check")
	require.Equal(t, 1, code, "%s %s", out, errs)
	assert.Contains(t, out, "RELEASE CHECK no-stuck-friend fail friend amy stuck from ")
	assert.Contains(t, out, "s1-1.w1 not finished")
	assert.Contains(t, out, "nova-sprint card s1-1")
	assert.True(t, strings.HasSuffix(strings.TrimSpace(out), "RELEASE NOT READY failed=1"), out)
	assert.Equal(t, before, ta.applies(), "release check writes nothing")

	code, out, _ = ta.do("release check --json")
	require.Equal(t, 1, code)
	var rep sprint.ReleaseReport
	require.NoError(t, json.Unmarshal([]byte(out), &rep))
	assert.False(t, rep.Ready)
	assert.Equal(t, 1, rep.Failed)

	// a stream the glob does not name has no friend stuck in it
	code, out, errs = ta.do("release check --streams other*")
	assert.Equal(t, 0, code, "%s %s", out, errs)
}

func TestReleaseCheckFailsWhenSprintClearedInsideWindow(t *testing.T) {
	t.Parallel()
	ta, _ := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	ta.a.sleep(1 * time.Hour)
	ta.ok("clear --confirm sprint") // clear closes epoch 0 and starts epoch 1 inside the 4h window
	code, out, errs := ta.do("release check")
	require.Equal(t, 1, code, "%s %s", out, errs)
	assert.Contains(t, out, "RELEASE CHECK no-stuck-friend fail coverage incomplete: sprint cleared at ")
	assert.Contains(t, out, "RELEASE NOT READY failed=1")
}

func TestReleaseCheckPassesWhenSprintClearedBeforeWindow(t *testing.T) {
	t.Parallel()
	ta, _ := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick")
	ta.ok("clear --confirm sprint")
	ta.a.sleep(5 * time.Hour) // clear was 5 hours ago, so epoch 1 covers the full 4-hour window
	code, out, errs := ta.do("release check")
	require.Equal(t, 0, code, "%s %s", out, errs)
	assert.Contains(t, out, "RELEASE CHECK no-stuck-friend ok ")
	assert.Contains(t, out, "RELEASE OK checks=1")
}

func TestReleaseCheckPassesWhenCardTakenBackInsideDeadline(t *testing.T) {
	t.Parallel()
	ta, _ := friendCardApp(t, "friend", "amy")
	ta.a.tip = func(_ context.Context, _, _ string) (string, error) { return "", nil }
	ta.ok("tick")
	ta.a.sleep(1 * time.Hour) // 1 hour into 2h deadline
	ta.ok("friend take amy s1-1 --reason 'rebalance'")
	ta.a.sleep(2 * time.Hour) // hour 3 since deal, hour 2 since withdrawal (untaken deadline is 6h = hour 7)
	code, out, errs := ta.do("release check")
	require.Equal(t, 0, code, "%s %s", out, errs)
	assert.Contains(t, out, "RELEASE CHECK no-stuck-friend ok ")
	assert.Contains(t, out, "RELEASE OK checks=1")
}

func TestReleaseCheckRefusesAnUnknownCheckAndWords(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead")
	code, _, errs := ta.do("release check --check nope")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "no release check named nope; the checks are no-stuck-friend")
	code, _, errs = ta.do("release check s1-1")
	assert.Equal(t, 2, code, errs)
	code, _, errs = ta.do("release check --streams [")
	assert.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "not a glob")
}

func TestAddRefusesACardCalledCheckAndReleaseOfACardStillWorks(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1 --coordinator lead")
	code, _, errs := ta.do("add --stream s1 check --one --actor lead --brief-file " + proBriefFile(t))
	assert.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "a card cannot be called check")
	code, _, errs = ta.do("add --stream s1 --sentinel check --actor lead")
	assert.Equal(t, 2, code, errs)
	assert.Contains(t, errs, "a card cannot be called check")

	// release <id> still releases a held card, and release check is not it
	ta.ok("add --stream s1 held-1 --one --held --actor lead --brief-file " + proBriefFile(t))
	out := ta.ok("release held-1 --reason 'read it' --actor lead")
	assert.Contains(t, out, "held-1")
	code, _, _ = ta.do("release check")
	assert.Equal(t, 0, code)
}
