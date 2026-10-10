package friend

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/bus/bustest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The finding of 2026-10-04 (fr-delivery-receipts.w1): the sprint server's
// notes to friends failed for two hours on WRONGPASS and only a log line said
// so, and a friend's harness got no note for 80 minutes while her beat said
// up. A message to a friend is owed a receipt from her session, never from
// her daemon; the friends table reads what is owed; and a send that fails on
// the login or the connection is one alarm, raised at the first failure and
// cleared at the next success (docs/SPEC-BUS.md and docs/SPEC-FRIEND.md,
// fr-delivery-receipts.w1).
func TestAnUndeliveredMessageShowsAndAnAuthFailureAlarms(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// a session that never acknowledges: the daemon pushes the turn in, the
	// turn ends at exit 0 and the daemon acks the stream, and no receipt comes
	r := newRig(t)
	r.store.Friends = []string{"bob"}
	m := r.send(t, "ada", "card c1 dealt", "your card is in your inbox")
	r.run(t, 4)
	require.Len(t, r.delivered, 1, "the daemon pushed the message into the session")
	pending, fresh, err := r.bus.Peek(ctx, "bob")
	require.NoError(t, err)
	assert.Empty(t, pending, "the daemon acked the stream at exit 0")
	assert.Empty(t, fresh)

	now := r.now.Add(10 * time.Minute)
	got, err := r.bus.Undelivered(ctx, "bob", "ada")
	require.NoError(t, err)
	require.Len(t, got, 2)
	bob := got[0]
	assert.Equal(t, "bob", bob.Name)
	assert.Equal(t, 1, bob.Count, "the daemon's ack is no receipt: the message is undelivered until the session says so")
	assert.Equal(t, m.ID, bob.OldestID)
	assert.Equal(t, m.At, bob.OldestAt)
	assert.Equal(t, 0, got[1].Count, "ada is owed nothing: no message to her is a friend's")

	// the session's own acknowledgement is the receipt
	n, err := r.bus.Receipt(ctx, "bob", []string{m.ID})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	got, err = r.bus.Undelivered(ctx, "bob")
	require.NoError(t, err)
	assert.Equal(t, 0, got[0].Count)

	// a bus that refuses the login: one alarm naming the store and the user,
	// never the password, raised at the first failure and cleared at the next success
	refusing := bustest.NewFake(t0, "rowan", "bob")
	refusing.Friends = []string{"bob"}
	refusing.Fail = errors.New("WRONGPASS invalid username-password pair or user is disabled.")
	var raised, cleared []bus.Alarm
	c := &Courier{
		Bus:   &bus.Bus{Store: refusing},
		Watch: &bus.Watch{Store: "bus.tailnet:6379", User: "sprint", Raise: func(a bus.Alarm) { raised = append(raised, a) }, Clear: func(a bus.Alarm) { cleared = append(cleared, a) }},
		Now:   func() time.Time { return now },
	}
	note := bus.Message{From: "rowan", To: []string{"bob"}, Subject: "card c2 dealt", Body: "your card is in your inbox"}
	for range 54 {
		_, err := c.Send(ctx, note)
		require.Error(t, err)
	}
	require.Len(t, raised, 1, "54 failures are one alarm")
	a := raised[0]
	assert.Equal(t, "bus.tailnet:6379", a.Store)
	assert.Equal(t, "sprint", a.User)
	assert.Equal(t, bus.AlarmAuth, a.Class)
	assert.Contains(t, a.Text(), "bus.tailnet:6379")
	assert.Contains(t, a.Text(), "user sprint")
	assert.Contains(t, a.Text(), "WRONGPASS")
	assert.NotContains(t, a.Text(), "password=", "the alarm never carries a password")
	assert.Empty(t, cleared)
	assert.True(t, c.Watch.Open())

	refusing.Fail = nil
	sent, err := c.Send(ctx, note)
	require.NoError(t, err)
	require.Len(t, cleared, 1, "the next success clears it")
	assert.Equal(t, 54, cleared[0].Failures, "the clear says how many sends failed in the outage")
	assert.False(t, c.Watch.Open())
	_, err = c.Send(ctx, note)
	require.NoError(t, err)
	assert.Len(t, raised, 1)
	assert.Len(t, cleared, 1, "a success with no alarm open clears nothing")
	got, err = (&bus.Bus{Store: refusing}).Undelivered(ctx, "bob")
	require.NoError(t, err)
	assert.Equal(t, 2, got[0].Count, "the courier's notes are owed receipts like any message to a friend")
	assert.Equal(t, sent.ID, got[0].OldestID)

	// a dial that fails is the same alarm, of the connection
	c.Bus = nil
	c.Open = func(context.Context) (*bus.Bus, func(), error) {
		return nil, nil, errors.New("dial tcp 100.64.0.9:6379: connect: connection refused")
	}
	_, err = c.Send(ctx, note)
	require.Error(t, err)
	require.Len(t, raised, 2)
	assert.Equal(t, bus.AlarmConnection, raised[1].Class)
	// a refusal of the message itself is the sender's mistake, never an alarm
	c.Open, c.Bus = nil, &bus.Bus{Store: refusing}
	_, err = c.Send(ctx, bus.Message{From: "rowan", To: []string{"nobody"}, Subject: "x", Body: "y"})
	var refusal *bus.Refusal
	require.ErrorAs(t, err, &refusal)
	assert.Len(t, cleared, 1, "the store answered a refusal, which is no success of a send: the alarm stays open")
	assert.True(t, c.Watch.Open())
}
