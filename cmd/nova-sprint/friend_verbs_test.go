package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	ta.ok("tick") // amy alone: s1-1 working, s1-2 ready
	ta.ok("friend sync --root " + root)
	require.Equal(t, "queued", queueStates(t, root, "amy")["s1-2.w1"])
	ta.ok("friend up bob")
	ta.ok("friend beat bob")

	out := ta.ok("friend level")
	assert.Contains(t, out, "s1-2.w1 friend.amy:ready -> friend.bob:working gen=2; moved=1 to bob(1) from amy(1)")
	assert.Contains(t, out, "FRIEND-LEVEL OK moved=1")
	out = ta.ok("friend sync --root " + root)
	assert.Contains(t, out, "FRIEND-CARD DELIVERED friend=bob card=s1-2.w1 job=s1-2.w1.g2")
	assert.Equal(t, "taken", queueStates(t, root, "amy")["s1-2.w1"], "not hers to start any more")
	assert.Equal(t, "working", queueStates(t, root, "bob")["s1-2.w1"])
	assert.Contains(t, ta.ok("friend level"), "FRIEND-LEVEL OK moved=0")
	ta.clean()
}

// stats fills a friend's run wall and report lag from her finish records, where they were
// dashes: her card reports no usage, so her run is her take to her report (REPORT.md's
// time, kept on the card as reported) and her lag her report to the sync that finished it.
func TestStatsTimesAFriendsRunFromHerReport(t *testing.T) {
	t.Parallel()
	ta, root := friendCardApp(t, "friend amy", "amy")
	ta.ok("tick") // dealt and taken at t0
	ta.ok("friend sync --root " + root)
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
	ta.ok("tick") // s1-1 working, s1-2 ready behind it, s1-3 waits (friend sync sets her width back to 8)
	ta.ok("friend sync --root " + root)
	taken := ta.a.now()
	ta.a.sleep(70 * time.Minute)
	outboxReport(t, root, "amy", "s1-1.w1", "Verdict: LAND\nHead: "+landHead+"\n\nDone.\n")
	report := filepath.Join(root, "amy-working", "outbox", "s1-1.w1", "REPORT.md")
	require.NoError(t, os.Chtimes(report, taken.Add(time.Hour), taken.Add(time.Hour)))
	ta.ok("friend sync --root " + root)
	ta.ok("friend beat amy")
	ta.ok("tick") // s1-3 dealt to her and taken, s1-1's hour behind her

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
