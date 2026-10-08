package bus

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A proof's state at the store's now: none with no proof, down whatever its
// age when its daemon says the session did not answer, stale at PushFresh,
// proven below; the deaf line names the name, its age and the remedy, and is
// empty for a proven one (SPEC-BUS.md, bus-requires-inbox-push-proof).
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
		if c.state == PushProven {
			assert.Empty(t, c.p.Deaf(now))
			continue
		}
		assert.Contains(t, c.p.Deaf(now), "deaf: bob has no proven push since "+c.age+": ")
		assert.Contains(t, c.p.Deaf(now), "nova-friend install --as bob --harness <h> --dir <d>")
	}
	assert.Contains(t, down.Deaf(at), "(no session answer)")
	assert.Contains(t, up.Deaf(at.Add(time.Hour)), "its daemon (claude) last renewed it 1h0m0s ago")
	assert.Contains(t, none.Deaf(at), "no daemon has recorded one")
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

// Hearing is the gate a nova-bus Bus opens over: Send checks the message
// first and refuses a deaf sender, recipient or cc after, all at once and
// writing nothing; Recv refuses a deaf recipient and never makes its group;
// the bare store (the daemon's) is not gated; peek, ack and log pass.
func TestHearingGatesSendAndRecvAndNothingElse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := NewFake(time.Date(2026, 10, 5, 18, 0, 0, 0, time.UTC), "ada", "bob", "cy")
	gated, bare := &Bus{Store: Hearing(f)}, &Bus{Store: f}
	m := Message{From: "ada", To: []string{"bob"}, CC: []string{"cy"}, Subject: "s", Body: "x"}

	_, err := gated.Send(ctx, Message{From: "ada", To: []string{"zed"}, Subject: "s", Body: "x"})
	assert.ErrorContains(t, err, "zed is no known name")
	assert.NotContains(t, err.Error(), "deaf", "the message's own problems first")
	_, err = gated.Send(ctx, m)
	var r *Refusal
	require.ErrorAs(t, err, &r)
	assert.Len(t, r.Problems, 3, "ada, bob and cy, at once: %v", r.Problems)
	assert.Equal(t, 0, f.Len(LogKey))
	_, _, err = gated.Recv(ctx, "bob", 0)
	assert.ErrorContains(t, err, "deaf: bob")
	_, exists, err := f.Group(ctx, StreamOf("bob"), "bob")
	require.NoError(t, err)
	assert.False(t, exists, "a deaf recv makes no group")
	assert.ErrorContains(t, gated.Heard(ctx, "ada"), "deaf: ada")

	_, err = bare.Send(ctx, m)
	require.NoError(t, err, "the daemon's own sends are not gated")

	for _, n := range []string{"ada", "bob", "cy"} {
		_, err := bare.ProvePush(ctx, PushProof{Name: n, Harness: "claude", Up: true})
		require.NoError(t, err)
	}
	sent, err := gated.Send(ctx, m)
	require.NoError(t, err)
	e, ok, err := gated.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, gated.Heard(ctx, "ada", "bob", "cy"))

	f.Advance(PushFresh)
	_, err = gated.Send(ctx, m)
	assert.ErrorContains(t, err, "deaf: ada has no proven push since 10m")
	_, _, err = gated.Recv(ctx, "bob", 0)
	assert.ErrorContains(t, err, "deaf: bob")
	pending, _, err := gated.Peek(ctx, "bob")
	require.NoError(t, err)
	assert.Len(t, pending, 1, "a deaf name may still peek")
	acked, err := gated.Ack(ctx, "bob", []string{e.Message().ID})
	require.NoError(t, err)
	assert.True(t, acked[e.Message().ID], "and ack")
	got, err := gated.Log(ctx, "-")
	require.NoError(t, err)
	assert.Len(t, got, 2, "and read the log")
	assert.Equal(t, sent.ID, got[1].Message().ID)
}

// LogNewest is the log's end, newest first, where Log from the start is its
// oldest window: a reader of a long log reaches the latest message by it.
func TestLogNewestIsTheLogsEndNewestFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := NewFake(time.Date(2026, 10, 5, 18, 0, 0, 0, time.UTC), "ada", "bob")
	b := &Bus{Store: f}
	var ids []string
	for i := range 3 {
		m, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: fmt.Sprint("s", i), Body: "x"})
		require.NoError(t, err)
		ids = append(ids, m.ID)
	}
	got, err := b.LogNewest(ctx)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, []string{ids[2], ids[1], ids[0]}, []string{got[0].Message().ID, got[1].Message().ID, got[2].Message().ID})
	after, err := b.Log(ctx, "("+got[1].Entry)
	require.NoError(t, err)
	require.Len(t, after, 1, "after the middle entry: the last")
	assert.Equal(t, ids[2], after[0].Message().ID)
	f.Fail = errors.New("down")
	_, err = b.LogNewest(ctx)
	assert.EqualError(t, err, "down")
}
