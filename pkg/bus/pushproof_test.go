package bus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A proof's state at the store's now: none with no proof, down whatever its
// age when its daemon says the session did not answer, stale at PushFresh,
// proven below (SPEC-BUS.md, bus-requires-inbox-push-proof); what the states
// make of a name's hearing is heard_test.go's.
func TestAProofIsProvenUpAndYoungerThanTenMinutes(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 5, 18, 0, 0, 0, time.UTC)
	up := PushProof{Name: "bob", Harness: "claude", Up: true, At: at}
	down := PushProof{Name: "bob", Harness: "claude", Reason: "no session answer", At: at}
	none := PushProof{Name: "bob"}
	for _, c := range []struct {
		p     PushProof
		after time.Duration
		state string
		age   string
	}{
		{up, 0, PushProven, "0s"},
		{up, PushFresh - time.Second, PushProven, "9m59s"},
		{up, PushFresh, PushStale, "10m0s"},
		{up, -time.Minute, PushProven, "0s"}, // a store clock that stepped back is no negative age
		{down, time.Second, PushDown, "1s"},
		{none, time.Hour, PushNone, "never"},
	} {
		now := at.Add(c.after)
		assert.Equal(t, c.state, c.p.State(now), "%+v at +%s", c.p, c.after)
		assert.Equal(t, c.age, c.p.AgeWord(now))
	}
}

// ProvePush stamps the proof with the store's time and writes it in one
// transaction; PushProofs reads every name in one trip, a name with none or
// a value that is no proof as none; a bad name is refused.
func TestProvePushWritesAndPushProofsReads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := NewFake(time.Date(2026, 10, 5, 18, 0, 0, 0, time.UTC), "ada", "bob", "cy")
	b := &Bus{Store: f}
	p, err := b.ProvePush(ctx, PushProof{Name: "bob", Harness: "codex", Nonce: "n1", Up: true})
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 10, 5, 18, 0, 1, 0, time.UTC), p.At, "the store's time")
	require.NoError(t, f.AddAll(ctx, nil, nil, Mark{Key: PushKey, Field: "cy", Value: "not json"}))

	got, now, err := b.PushProofs(ctx, "ada", "bob", "cy")
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, PushNone, got[0].State(now))
	assert.Equal(t, PushProven, got[1].State(now))
	assert.Equal(t, "n1", got[1].Nonce)
	assert.Equal(t, "codex", got[1].Harness)
	assert.Equal(t, "cy", got[2].Name)
	assert.Equal(t, PushNone, got[2].State(now), "a value that is no proof proves nothing")

	_, err = b.ProvePush(ctx, PushProof{Name: "Bob"})
	var r *Refusal
	require.ErrorAs(t, err, &r)
	f.Fail = errors.New("down")
	_, _, err = b.PushProofs(ctx, "bob")
	assert.EqualError(t, err, "down")
}

// Hearing advises and never gates (the finding of 2026-10-08, issue
// #5450: a claude friend and a machine with no daemon could never be written
// to or read as): Send lands a message whoever is unheard, Recv reads for an
// unheard name and makes its group, and Unheard is the lines a verb prints
// beside its result, one per unheard name in order, deduplicated, empty once
// every name is proven; the message's own problems are still refused first.
func TestThePushProofAdvisesAndNeverRefuses(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := NewFake(time.Date(2026, 10, 5, 18, 0, 0, 0, time.UTC), "ada", "bob", "cy")
	b := &Bus{Store: f}
	m := Message{From: "ada", To: []string{"bob"}, CC: []string{"cy"}, Subject: "s", Body: "x"}

	_, err := b.Send(ctx, Message{From: "ada", To: []string{"zed"}, Subject: "s", Body: "x"})
	assert.ErrorContains(t, err, "zed is no known name", "the message's own problems are refused")
	sent, err := b.Send(ctx, m)
	require.NoError(t, err, "a message to names nothing proven hears lands")
	assert.Equal(t, 1, f.Len(LogKey))
	lines, err := b.Unheard(ctx, "ada", "bob", "cy", "ada")
	require.NoError(t, err)
	require.Len(t, lines, 3, "ada, bob and cy, once each, in order: %v", lines)
	assert.Contains(t, lines[0], "ada has never received on the bus (last sent ")
	assert.Contains(t, lines[2], "cy has never received on the bus (never sent)")

	e, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err, "an unheard name reads")
	require.True(t, ok)
	assert.Equal(t, sent.ID, e.Message().ID)
	_, exists, err := f.Group(ctx, StreamOf("bob"), "bob")
	require.NoError(t, err)
	assert.True(t, exists, "and has its group")

	for _, n := range []string{"ada", "bob", "cy"} {
		_, err := b.ProvePush(ctx, PushProof{Name: n, Harness: "codex", Nonce: "n", Up: true})
		require.NoError(t, err)
	}
	lines, err = b.Unheard(ctx, "ada", "bob", "cy")
	require.NoError(t, err)
	assert.Empty(t, lines, "every name proven: nothing to say")

	f.Advance(PushFresh)
	lines, err = b.Unheard(ctx, "ada", "bob")
	require.NoError(t, err)
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], "ada has never received on the bus (last sent ", "a stale proof says nothing of receiving")
	_, err = b.Send(ctx, m)
	require.NoError(t, err, "stale is advice too")
	assert.Equal(t, 2, f.Len(LogKey))

	f.Fail = errors.New("down")
	_, err = b.Unheard(ctx, "ada")
	assert.EqualError(t, err, "down")
}
