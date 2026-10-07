package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// friend give (docs/SPEC-SPRINT.md section 1, a friend's card taken back): the seat's undo of
// friend take, which clears the take's mark (taken_from) so the deal may deal the card to
// her again; on the twin, no socket.

// givenAndDealt takes s1-1 back from amy, says the ticks deal it to no one, gives it back,
// and says the next ticks deal it to her.
func givenAndDealt(t *testing.T, ta *testApp) {
	t.Helper()
	ta.ok("tick")
	var c cardView
	ta.json("card s1-1", &c)
	require.Equal(t, sprint.FriendRow("amy"), c.Work[0].Row)
	ta.ok("friend take amy s1-1 --reason 'her harness was down'")
	ta.ok("tick")
	ta.ok("tick")
	ta.json("card s1-1", &c)
	require.Len(t, c.Work, 1)
	assert.Equal(t, sprint.Withdrawn, c.Work[0].Col, "taken from her, it is dealt to no one")
	assert.Equal(t, sprint.Ready, c.Primary.Col)

	out := ta.ok("friend give amy s1-1 --reason 'the outage was the harness'")
	assert.Contains(t, out, "MOVED s1-1 may be dealt to amy again (the outage was the harness)")
	assert.Contains(t, out, "FRIEND-GIVE OK moved=1 refused=0")
	var given cardView // a fresh one: json merges into a map already filled
	ta.json("card s1-1", &given)
	assert.Empty(t, given.Work[0].F(sprint.FieldTakenFrom), "the take's mark is cleared")
	ta.ok("tick")
	ta.ok("tick")
	ta.json("card s1-1", &c)
	require.Len(t, c.Work, 1, "the same card, no second attempt")
	assert.Equal(t, sprint.FriendRow("amy"), c.Work[0].Row, "dealt to her again")
	assert.Equal(t, sprint.Working, c.Primary.Col)
	ta.clean()
}

func TestFriendGiveLetsTheDealDealACardTakenFromHerToHerAgain(t *testing.T) {
	t.Parallel()
	ta, _ := takeApp(t, 1, nil, "amy")
	givenAndDealt(t, ta)
}

func TestAPinnedCardTakenFromItsFriendIsDealtToNoOneUntilGivenBack(t *testing.T) {
	t.Parallel()
	ta, _ := friendCardApp(t, "only friend amy", "amy", "bob")
	ta.ok("fleet down m1")
	ta.ok("fleet down m2")
	ta.a.tip = func(context.Context, string, string) (string, error) { return "", nil }
	givenAndDealt(t, ta) // bob up with room all along: the pin admits only her
}

func TestFriendGiveRefusesACardNeverTakenFromHer(t *testing.T) {
	t.Parallel()
	ta, _ := takeApp(t, 2, nil, "amy", "bob")
	ta.ok("friend down bob")
	ta.ok("tick")
	code, _, errs := ta.do("friend give amy s1-1")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "REFUSED s1-1: s1-1 is working, not ready or waiting")
	assert.Contains(t, errs, "FRIEND-GIVE FAILED moved=0 refused=1")

	ta.ok("friend take amy s1-1 s1-2")
	code, _, errs = ta.do("friend give bob s1-1")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "REFUSED s1-1: s1-1.w1 was never taken back from friend bob (taken from friend amy)")
	code, _, errs = ta.do("friend give cat s1-1")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "no friend cat on the friends table")
	code, _, errs = ta.do("friend give amy s1-9")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "REFUSED s1-9: no card s1-9")

	// the rest named are given, each refusal its own line
	code, out, errs := ta.do("friend give amy s1-1 s1-9")
	assert.Equal(t, 1, code)
	assert.Contains(t, out, "MOVED s1-1 may be dealt to amy again (given back by the coordinator)")
	assert.Contains(t, errs, "FRIEND-GIVE FAILED moved=1 refused=1")
	code, _, errs = ta.do("friend give amy s1-1")
	assert.Equal(t, 1, code)
	assert.Contains(t, errs, "REFUSED s1-1: s1-1.w1 was never taken back from friend amy (taken from no friend)", "given once, its mark is gone")
	ta.clean()
}
