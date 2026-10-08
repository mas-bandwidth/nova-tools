package main

import (
	"strconv"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStopReturnNeedsOwnedCancellationBeforeExplicitRestart(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1 --readers reader-a,reader-b")
	ta.ok("add --stream s1 --count 1 --one")
	ta.deal(1)
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	require.Len(t, q.Cards, 1)
	card := q.Cards[0]
	word := card.ID + "@" + strconv.Itoa(max(card.Gen, 1))
	ta.ok("take --as m1 " + word)
	ta.ok("stop --reason 'operator pause' --until 1h")
	code, _, why := ta.do("start")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, why, "m1:"+word)
	assert.Contains(t, why, "stop-return")
	ta.ok("stop-return --as m1 --epoch 0 " + word + " --reason 'owned child exited'")
	ta.json("queue --as m1", &q)
	require.Len(t, q.Cards, 1)
	assert.Equal(t, sprint.Ready, q.Cards[0].Col)
	assert.Equal(t, card.ID, q.Cards[0].ID)
	assert.Equal(t, card.Gen+1, q.Cards[0].Gen)
	assert.Contains(t, ta.ok("start"), "after=RUNNING")
}
