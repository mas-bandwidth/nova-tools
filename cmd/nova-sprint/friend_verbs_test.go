package main

import (
	"testing"

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
	return w.Tables[sprint.Friends][friend]["width"]
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
