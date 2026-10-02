package member

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQueueReviewReturnedReadGetsItsPacketWhenCooldownEnds(t *testing.T) {
	t.Parallel()
	s := &packetServer{epoch: 7, cards: []queueCard{readCard(0)}}
	g := packetRig(Config{As: "r", Width: 1, Reader: true}, s)
	now := time.Unix(0, 0)
	_, err := g.m.Tick(now)
	require.NoError(t, err)
	require.Equal(t, []string{"p000.r1.r"}, g.r.started())
	g.r.child("p000.r1.r").end(Result{Ran: false, Report: "no verdict"})
	_, err = g.m.Tick(now)
	require.NoError(t, err)
	require.Zero(t, g.m.Running())
	require.Equal(t, "asked", s.cards[0].Col, "the return asks the read again in place")

	s.forget()
	_, err = g.m.Tick(now.Add(ReadStageRetry - time.Second))
	require.NoError(t, err)
	assert.Empty(t, s.verbs()[1:], "before cooldown ends, only the queue is read")
	assert.Zero(t, s.lastHanded(), "the cooling read needs no packet")
	assert.Len(t, g.r.started(), 1)

	s.forget()
	_, err = g.m.Tick(now.Add(ReadStageRetry))
	require.NoError(t, err)
	assert.Equal(t, 1, s.lastHanded(), "at cooldown expiry its packet must be requested again")
	assert.Len(t, g.r.started(), 2, "the returned read must be begun again once its delay ends")
	assert.Equal(t, "reading", s.cards[0].Col)
	assert.Equal(t, 1, g.m.Running(), "the retry occupies its free lane")
}
