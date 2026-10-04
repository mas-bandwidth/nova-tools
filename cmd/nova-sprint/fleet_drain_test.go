package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fleet up <m> --width 0 drains the member: no new deal reaches it, its untaken
// ready cards are levelled away, and a working card finishes where it is, where
// fleet down would deal it again (the comfort list of 2026-10-03, item 5: holding
// a machine meant fleet down, which redeals its cards). A width is set again by
// fleet up --width <n>.
func TestFleetUpWidthZeroDrainsAMember(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --readers reader-a,reader-b --members m1:1,m2:4")
	ta.ok("add --stream s1 --count 6")
	ta.ok("start")
	ta.ok("tick")
	ta.ok("take --as m1 --limit 1")
	var q struct {
		Cards []queueCard
		Width int
	}
	ta.json("queue --as m1", &q)
	require.Len(t, q.Cards, 2, "m1 holds its width working and as many again ready: %+v", q.Cards)
	working := ""
	for _, x := range q.Cards {
		if x.Col == "working" {
			working = x.ID
		}
	}
	require.NotEmpty(t, working, "the take left no working card: %+v", q.Cards)

	out := ta.ok("fleet up m1 --width 0")
	assert.Contains(t, out, "MOVED m1 up width=0 (drains: no new deals, its working cards finish)")
	ta.ok("tick")
	ta.json("queue --as m1", &q)
	if assert.Len(t, q.Cards, 1, "the working card stays, the untaken ready card is levelled away: %+v", q.Cards) {
		assert.Equal(t, working, q.Cards[0].ID)
		assert.Equal(t, "working", q.Cards[0].Col)
	}
	assert.Equal(t, 0, q.Width)
	ta.json("queue --as m2", &q)
	assert.Len(t, q.Cards, 5, "every card not working on m1 is on m2: %+v", q.Cards)

	ta.ok("finish --as m1 " + working + "@1 --head 9f3c2e1 --report 'tests green'")
	ta.ok("tick")
	ta.json("queue --as m1", &q)
	assert.Empty(t, q.Cards, "a drained member is dealt nothing")

	ta.ok("fleet up m1 --width 2")
	ta.ok("tick")
	ta.json("queue --as m1", &q)
	assert.NotEmpty(t, q.Cards, "a width set again takes deals")
}
