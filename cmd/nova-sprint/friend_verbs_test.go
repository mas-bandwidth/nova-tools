package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// The verbs friends lacked that machines have (the owner, 2026-10-04: "What else is like
// this? Missing verbs we need for friends, that machines already have"), on the twin.

// friendWidth is the friend's width as where --json shows it.
func friendWidth(ta *testApp, friend string) string {
	var w whereView
	ta.json("where", &w)
	return cellText(w.Tables[sprint.Friends][friend]["width"])
}

func TestFriendUpWidthSetsHerWidthAsFleetUpWidthSetsAMembers(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	ta.ok("friend sync")
	assert.Equal(t, "8", friendWidth(ta, "amy"))
	out := ta.ok("friend up amy --width 3")
	assert.Contains(t, out, "FRIEND-UP OK amy held=false width=3")
	assert.Equal(t, "3", friendWidth(ta, "amy"))
	ta.ok("friend down amy")
	ta.ok("friend up amy")
	assert.Equal(t, "3", friendWidth(ta, "amy"), "a release without --width leaves it as it is")

	code, _, errs := ta.do("friend up amy --width 0")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--width: a width wants a whole number from 1 to")
	code, _, _ = ta.do("friend down amy --width 3")
	assert.Equal(t, 2, code, "friend down takes no width")

	// friend sync sets her nova-config row's again, as fleet sync sets a member's
	ta.ok("friend sync")
	assert.Equal(t, "8", friendWidth(ta, "amy"))
}

// whereFriends is where --json's friends, by name.
func whereFriends(ta *testApp) map[string]store.FriendRow {
	var w struct {
		Friends []store.FriendRow `json:"friends"`
	}
	ta.json("where", &w)
	out := map[string]store.FriendRow{}
	for _, f := range w.Friends {
		out[f.Name] = f
	}
	return out
}

// A friend's machinery beats with her counts and her load, as a machine's beats with its
// load; the beat took her name alone and refused them (Johnny, 2026-10-04).
func TestFriendBeatTakesHerCountsAndLoadAsFleetBeatTakesALoad(t *testing.T) {
	t.Parallel()
	ta, _ := friendApp(t, "amy")
	ta.ok("friend sync")
	out := ta.ok("friend beat amy --working 2 --queue 3 --width 4 --running s1-1.w1,s1-2.w1 --load 40%")
	assert.Contains(t, out, "FRIEND-BEAT OK amy at=")
	assert.Contains(t, out, " working=2 queue=3 width=4 load=40.0% running=s1-1.w1,s1-2.w1")
	f := whereFriends(ta)["amy"]
	require.NotNil(t, f.Report)
	assert.Equal(t, 2, *f.Report.Working)
	assert.Equal(t, 3, *f.Report.Queue)
	assert.Equal(t, 4, *f.Report.Width)
	assert.Equal(t, []string{"s1-1.w1", "s1-2.w1"}, f.Report.Running)
	assert.Equal(t, 40.0, f.Load)
	assert.Equal(t, 8, f.Width, "her width is the roster's, not her word")
	assert.Equal(t, sprint.Up, f.Status)

	// a beat with none reports none: the last one's counts are not kept
	ta.ok("friend beat amy")
	f = whereFriends(ta)["amy"]
	assert.Nil(t, f.Report)
	assert.Zero(t, f.Load)

	for _, bad := range []string{"--working -1", "--queue x", "--width 0", "--load lots"} {
		code, _, errs := ta.do("friend beat amy " + bad)
		assert.Equal(t, 2, code, bad)
		assert.Contains(t, errs, "wants", bad)
	}
	// and the server runs it with them
	_, _, why := workerVerb([]string{"friend", "beat", "amy", "--working", "2", "--queue", "3", "--width", "4", "--load", "40"})
	assert.Empty(t, why)
	_, _, why = workerVerb([]string{"friend", "beat", "amy", "--width", "0"})
	assert.NotEmpty(t, why)
}

// friend level on the twin: a card queued on one friend moves to another of her class with
// room, which friend sync delivers as a new job, and the queue file of the friend it left
// marks it taken.
func TestFriendLevelMovesAQueuedCardAndTheQueueFilesFollow(t *testing.T) {
	t.Parallel()
	ta, root := takeApp(t, 4, nil, "amy", "bob")
	ta.ok("friend up amy --width 1")
	ta.ok("friend up bob --width 1")
	ta.ok("friend down bob")
	ta.ok("tick") // amy alone, width 1: s1-1 and s1-2 ready (a deal is not a start)
	ta.ok("friend sync --root " + root)
	require.Equal(t, "queued", queueStates(t, root, "amy")["s1-2.w1"])
	ta.ok("friend up bob")
	ta.ok("friend beat bob")

	assert.Contains(t, ta.dry("friend level --dry-run"), "FRIEND-LEVEL DRY-RUN up=amy,bob; nothing was changed")
	out := ta.ok("friend level")
	assert.Contains(t, out, "s1-2.w1 friend.amy:ready -> friend.bob:ready gen=2; moved=1 to bob(1) from amy(1)")
	assert.Contains(t, out, "FRIEND-LEVEL OK moved=1")
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=bob card=s1-2.w1 job=s1-2.w1.g2")
	assert.Equal(t, "taken", queueStates(t, root, "amy")["s1-2.w1"], "not hers to start any more")
	assert.Equal(t, "queued", queueStates(t, root, "bob")["s1-2.w1"], "a level is not a start")
	assert.Contains(t, ta.ok("friend level"), "FRIEND-LEVEL OK moved=0")
	ta.clean()
}

// friend level respects a friend's delivery mode: a one-shot friend has room 1, so only 1 card moves.
func TestFriendLevelRespectsOneShotDeliveryMode(t *testing.T) {
	t.Parallel()
	ta, cfg := friendApp(t)
	ta.a.tip = func(_ context.Context, _, _ string) (string, error) { return "", nil }
	_, err := cfg.Insert(context.Background(), config.KindFriend, config.Row{Name: "amy", Fields: map[string]string{"tiers": "flash", "width": "2", "mode": "batch"}}, "t")
	require.NoError(t, err)
	_, err = cfg.Insert(context.Background(), config.KindFriend, config.Row{Name: "bob", Fields: map[string]string{"tiers": "flash", "width": "2", "mode": "one-shot"}}, "t")
	require.NoError(t, err)

	root := t.TempDir()
	ta.ok("friend sync --root " + root)
	ta.ok("friend beat amy")
	ta.ok("friend beat bob")

	for i := 1; i <= 4; i++ {
		id := fmt.Sprintf("s1-%d", i)
		brief := filepath.Join(t.TempDir(), id+".md")
		require.NoError(t, os.WriteFile(brief, []byte(passingBrief(id+": a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: friend")), 0o644))
		ta.ok("add --stream s1 --brief-dir " + filepath.Dir(brief))
	}
	ta.ok("start")

	ta.ok("friend down bob")
	ta.ok("tick") // amy (batch, width 2) fills her room of 4, every card ready
	ta.ok("friend sync --root " + root)

	ta.ok("friend up bob")
	ta.ok("friend beat bob")

	out := ta.ok("friend level")
	// bob has room 1 (one-shot mode), so only 1 card moves, and it stays ready
	assert.Contains(t, out, "FRIEND-LEVEL OK moved=1")
	assert.Contains(t, ta.ok("friend level"), "FRIEND-LEVEL OK moved=0")

	f := whereFriends(ta)
	assert.Equal(t, 0, f["amy"].Working)
	assert.Equal(t, 3, f["amy"].Ready)
	assert.Equal(t, 0, f["bob"].Working)
	assert.Equal(t, 1, f["bob"].Ready)
	ta.clean()
}

// stats fills a friend's run wall and report lag from her finish records, where they were
// dashes: her card reports no usage, so her run is her take to her report (REPORT.md's
// time, kept on the card as reported) and her lag her report to the sync that finished it.
func TestStatsTimesAFriendsRunFromHerReport(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick") // dealt ready at t0
	ta.ok("friend sync --root " + root)
	ta.ok("friend beat amy --running s1-1.w1")
	ta.ok("tick") // she starts it; the run wall is this take
	taken := ta.a.now()
	ta.a.sleep(10 * time.Minute)
	outboxReport(t, root, "amy", "s1-1.w1", "Verdict: LAND\nHead: "+landHead+"\n\nDone.\n")
	report := filepath.Join(root, "amy-working", "outbox", "s1-1.w1", "REPORT.md")
	require.NoError(t, os.Chtimes(report, taken.Add(8*time.Minute), taken.Add(8*time.Minute)))
	ta.ok("friend sync --root " + root)

	var c cardView
	ta.json("card s1-1", &c)
	assert.Equal(t, taken.Add(8*time.Minute).UTC().Format(time.RFC3339), c.Work[0].F(sprint.FieldReported))
	var ps sprint.PassStats
	ta.json("stats", &ps)
	require.Len(t, ps.Work, 1)
	assert.Equal(t, sprint.FriendRow("amy"), ps.Work[0].Member)
	assert.Equal(t, sprint.Measure{Median: 480, Max: 480, N: 1}, ps.Work[0].RunWall, "her take to her report")
	assert.Equal(t, sprint.Measure{Median: 120, Max: 120, N: 1}, ps.Work[0].ReportLag, "her report to the finish")
	ta.clean()
}

// On the twin: once amy has an ok attempt whose run (her take to her report) was an hour,
// a card she takes after it carries her deadline, three hours from its take, past the
// fleet's two, as where --json --cards shows it; one taken before it keeps the fleet's.
func TestAFriendsNextCardsDeadlineFollowsHerRunWall(t *testing.T) {
	t.Parallel()
	ta, root := takeApp(t, 3, map[string]string{"sprint/s1-1.w1.g1.e0": landHead}, "amy")
	ta.ok("friend up amy --width 1")
	ta.ok("tick") // s1-1 and s1-2 ready, s1-3 waits (a deal is not a start)
	ta.ok("friend sync --root " + root)
	ta.ok("friend beat amy --running s1-1.w1")
	ta.ok("tick") // she starts s1-1; s1-2 stays ready behind it
	taken := ta.a.now()
	ta.a.sleep(70 * time.Minute)
	outboxReport(t, root, "amy", "s1-1.w1", "Verdict: LAND\nHead: "+landHead+"\n\nDone.\n")
	report := filepath.Join(root, "amy-working", "outbox", "s1-1.w1", "REPORT.md")
	require.NoError(t, os.Chtimes(report, taken.Add(time.Hour), taken.Add(time.Hour)))
	ta.ok("friend sync --root " + root) // her finish promotes s1-2, taken before the sample is recorded
	ta.ok("friend beat amy")
	ta.ok("tick") // s1-3 dealt ready; this tick cannot start it
	ta.ok("friend beat amy --running s1-3.w1")
	ta.ok("tick") // she starts s1-3 after the hour, so its deadline is three times that wall

	var w whereView
	ta.json("where --cards", &w)
	limit := map[string]time.Duration{}
	for _, c := range w.Cards {
		assert.Equal(t, sprint.Working, c.State, c.ID)
		limit[c.ID] = c.Deadline.Sub(c.Since)
	}
	assert.Equal(t, map[string]time.Duration{"s1-2.w1": 2 * time.Hour, "s1-3.w1": 3 * time.Hour}, limit, "s1-2 taken before her ok attempt: the fleet's two hours; s1-3 after it: three times her one hour")
	ta.clean()
}
