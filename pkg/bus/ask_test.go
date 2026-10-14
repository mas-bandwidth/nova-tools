package bus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An ask carries a deadline the store stamps from its own clock (at + wait);
// a send that is no ask carries none, and an ask without one is refused:
// never wait for ever (SPEC-BUS.md, asks).
func TestAnAskHasAServerDeadlineAndOnlyAnAskHasOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	start := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	f := NewFake(start, "rowan", "ada", "bob")
	b := &Bus{Store: f}

	_, err := b.Send(ctx, Message{From: "rowan", To: []string{"ada"}, Subject: "q", Body: "?", Kind: KindAsk})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "an ask wants a deadline")

	_, err = b.Send(ctx, Message{From: "rowan", To: []string{"ada"}, Subject: "q", Body: "?", Wait: time.Minute})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "a deadline is for an ask")

	_, err = b.Send(ctx, Message{From: "rowan", To: []string{"ada"}, Subject: "q", Body: "?", Kind: KindTimeout})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "the bus's own")

	m, err := b.Send(ctx, Message{From: "rowan", To: []string{"ada"}, Subject: "q", Body: "?", Kind: KindAsk, Wait: 10 * time.Minute})
	require.NoError(t, err)
	assert.Equal(t, m.At.Add(10*time.Minute), m.Due, "the deadline is the store's at plus the wait")

	asks, now, err := b.Asks(ctx, "rowan")
	require.NoError(t, err)
	require.Len(t, asks, 1)
	assert.Equal(t, m.ID, asks[0].ID)
	assert.Equal(t, m.Due, asks[0].Due)
	assert.Equal(t, []string{"ada"}, asks[0].To)
	assert.Equal(t, AskOpen, asks[0].State(now))

	e, err := f.Range(ctx, StreamOf("ada"), "-", "+", 0)
	require.NoError(t, err)
	assert.Equal(t, m.Due, e[0].Message().Due, "the entry carries the deadline")
}

// An ask is answered only by a message from one of its recipients naming it
// (re), at the store's time; a reply from anyone else answers nothing.
func TestAnAskIsAnsweredOnlyByARecipientsReply(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	start := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	f := NewFake(start, "rowan", "ada", "bob")
	b := &Bus{Store: f}
	ask, err := b.Send(ctx, Message{From: "rowan", To: []string{"ada"}, Subject: "q", Body: "?", Kind: KindAsk, Wait: 10 * time.Minute})
	require.NoError(t, err)

	_, err = b.Send(ctx, Message{From: "bob", To: []string{"rowan"}, Subject: "re q", Body: "not mine", Re: ask.ID})
	require.NoError(t, err)
	asks, now, err := b.Asks(ctx, "rowan")
	require.NoError(t, err)
	assert.Equal(t, AskOpen, asks[0].State(now), "bob is no recipient")

	f.Advance(time.Minute)
	reply, err := b.Send(ctx, Message{From: "ada", To: []string{"rowan"}, Subject: "re q", Body: "yes", Re: ask.ID})
	require.NoError(t, err)
	asks, now, err = b.Asks(ctx, "rowan")
	require.NoError(t, err)
	assert.Equal(t, AskAnswered, asks[0].State(now))
	assert.Equal(t, "ada", asks[0].AnsweredBy)
	assert.Equal(t, reply.At, asks[0].AnsweredAt)

	f.Advance(time.Hour)
	_, err = b.Send(ctx, Message{From: "ada", To: []string{"rowan"}, Subject: "re q again", Body: "still yes", Re: ask.ID})
	require.NoError(t, err)
	asks, now, err = b.Asks(ctx, "rowan")
	require.NoError(t, err)
	assert.Equal(t, reply.At, asks[0].AnsweredAt, "the first reply stands: a second one past the deadline does not make it late")
	assert.Equal(t, AskAnswered, asks[0].State(now))

	n, err := b.ExpireAsks(ctx)
	require.NoError(t, err)
	assert.Empty(t, n, "an answered ask never times out")
}

// Past its deadline an open ask times out exactly once: one message to the
// sender, kind timeout, re the ask, its subject the alarm line; a second
// expiry delivers nothing.
func TestAnOpenAskPastItsDeadlineTimesOutOnceAndWakesTheSender(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	start := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	f := NewFake(start, "rowan", "ada", "bob")
	f.Friends = []string{"rowan", "ada", "bob"}
	b := &Bus{Store: f}
	ask, err := b.Send(ctx, Message{From: "rowan", To: []string{"ada"}, Subject: "are you there", Body: "?", Kind: KindAsk, Wait: 5 * time.Minute})
	require.NoError(t, err)

	got, err := b.ExpireAsks(ctx)
	require.NoError(t, err)
	assert.Empty(t, got, "nothing before the deadline")

	f.Advance(5 * time.Minute)
	got, err = b.ExpireAsks(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, ask.ID, got[0].Ask.ID)

	es, err := f.Range(ctx, StreamOf("rowan"), "-", "+", 0)
	require.NoError(t, err)
	require.Len(t, es, 1, "one timeout on the sender's stream")
	m := es[0].Message()
	assert.Equal(t, KindTimeout, m.Kind)
	assert.Equal(t, ask.ID, m.Re)
	assert.Equal(t, got[0].MessageID, m.ID)
	assert.Equal(t, []string{"rowan"}, m.To)
	assert.NotEqual(t, "rowan", m.From, "a wait skips its own messages; the timeout must wake it")
	assert.Regexp(t, `^ASK TIMEOUT `+ask.ID+` ada after 5m[0-9]+s: are you there$`, m.Subject)

	again, err := b.ExpireAsks(ctx)
	require.NoError(t, err)
	assert.Empty(t, again, "exactly once")
	es, err = f.Range(ctx, StreamOf("rowan"), "-", "+", 0)
	require.NoError(t, err)
	assert.Len(t, es, 1)

	asks, now, err := b.Asks(ctx, "rowan")
	require.NoError(t, err)
	assert.Equal(t, AskTimedOut, asks[0].State(now))
	assert.Equal(t, got[0].MessageID, asks[0].TimeoutID)

	f.Advance(time.Minute)
	_, err = b.Send(ctx, Message{From: "ada", To: []string{"rowan"}, Subject: "late", Body: "sorry", Re: ask.ID})
	require.NoError(t, err)
	asks, now, err = b.Asks(ctx, "rowan")
	require.NoError(t, err)
	assert.Equal(t, AskTimedOut, asks[0].State(now), "an answer past the deadline does not undo the timeout")
	assert.Equal(t, "ada", asks[0].AnsweredBy, "but who answered late is kept")
}

// Two expiries racing over the same ask (the alarm run by two loops) deliver
// one timeout between them: the store's once-key decides.
func TestTwoExpiriesOfOneAskDeliverOneTimeout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	start := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	f := NewFake(start, "rowan", "ada")
	b := &Bus{Store: f}
	ask, err := b.Send(ctx, Message{From: "rowan", To: []string{"ada"}, Subject: "q", Body: "?", Kind: KindAsk, Wait: time.Minute})
	require.NoError(t, err)
	f.Advance(2 * time.Minute)
	a, _, err := b.Asks(ctx, "rowan")
	require.NoError(t, err)
	// both read the ask open; each tries to deliver
	_, first, err := b.timeOut(ctx, a[0], f.now, nil)
	require.NoError(t, err)
	_, second, err := b.timeOut(ctx, a[0], f.now, nil)
	require.NoError(t, err)
	assert.True(t, first)
	assert.False(t, second)
	assert.Equal(t, 1, f.Len(StreamOf("rowan")), ask.ID)
}

// Liveness from asks, by the store's clock: up answered in time, deaf took it
// and did not answer, unreached never took it, down nothing heard in the window.
func TestLivenessFromAsks(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	asks := []Ask{
		{ID: "A1", From: "rowan", To: []string{"ada"}, At: ago(time.Hour), Due: ago(50 * time.Minute), AnsweredBy: "ada", AnsweredAt: ago(55 * time.Minute)},
		{ID: "B1", From: "rowan", To: []string{"bob"}, At: ago(time.Hour), Due: ago(50 * time.Minute)},
		{ID: "C1", From: "rowan", To: []string{"cy"}, At: ago(time.Hour), Due: ago(50 * time.Minute)},
		{ID: "D1", From: "rowan", To: []string{"di"}, At: ago(48 * time.Hour), Due: ago(47 * time.Hour), AnsweredBy: "di", AnsweredAt: ago(48 * time.Hour)},
		{ID: "E1", From: "rowan", To: []string{"ed"}, At: ago(time.Minute), Due: now.Add(time.Hour)},        // still open: no word yet
		{ID: "A0", From: "rowan", To: []string{"ada"}, At: ago(3 * time.Hour), Due: ago(170 * time.Minute)}, // older, deaf; the newest decides
	}
	delivered := map[string]map[string]bool{"bob": {"B1": true}, "ada": {"A0": true}}
	got := LivenessOf(asks, delivered, []string{"ada", "bob", "cy", "di", "ed"}, now, 24*time.Hour)
	states := map[string]string{}
	for _, l := range got {
		states[l.Name] = l.State
	}
	assert.Equal(t, map[string]string{"ada": LiveUp, "bob": LiveDeaf, "cy": LiveUnreached, "di": LiveDown, "ed": LiveDown}, states)
	assert.Equal(t, "A1", got[0].AskID)
}
