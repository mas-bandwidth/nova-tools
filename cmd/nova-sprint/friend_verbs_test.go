package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
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
