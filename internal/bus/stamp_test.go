package bus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A message moves sent, delivered, read, acted for its recipient and never
// back: a redelivery, or a stamp of a state behind the one it holds, is
// dropped and the time stays. A friend's answer (re) is her act on it.
// Overdue is each message still sent after the age, oldest first, and it
// leaves the list once a reader takes it. The stamp is the store's, written
// by the bus for the store it is given (SPEC-BUS.md, message-receipts-r2.w1;
// tla/Bus2.tla, ReceiptNeverMovesBack).
func TestReceiptsMoveDeliveredReadActedAndDropDuplicates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := NewFake(time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC), "rowan", "ada", "studio")
	f.Friends = []string{"rowan", "ada"}
	b := &Bus{Store: f}
	stage := func(as, id string) Stage {
		t.Helper()
		got, _, err := b.Stages(ctx, as, id)
		require.NoError(t, err)
		require.Len(t, got, 1, "%s has a receipt for %s", as, id)
		return got[0]
	}

	m1, err := b.Send(ctx, Message{From: "rowan", To: []string{"ada"}, CC: []string{"studio"}, Subject: "s1", Body: "one"})
	require.NoError(t, err)
	assert.Equal(t, Stage{ID: m1.ID, State: StateSent, At: m1.At}, stage("ada", m1.ID), "sent, at the store's time")
	assert.Equal(t, StateSent, stage("studio", m1.ID).State, "a machine's stream has a receipt too")

	late, _, err := b.Overdue(ctx, 10*time.Minute)
	require.NoError(t, err)
	assert.Empty(t, late, "nothing is overdue the moment it is sent")
	f.Advance(11 * time.Minute)
	late, now, err := b.Overdue(ctx, 10*time.Minute)
	require.NoError(t, err)
	require.Len(t, late, 2, "every stream counts, a machine's too")
	assert.Equal(t, []string{"ada", "studio"}, []string{late[0].Name, late[1].Name})
	assert.Equal(t, m1.ID, late[0].ID)
	assert.Equal(t, now.Sub(m1.At), late[0].Age)
	assert.Greater(t, late[0].Age, 10*time.Minute)

	// ada's reader takes it off her stream: delivered. studio's is still short of it.
	e, ok, err := b.Recv(ctx, "ada", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m1.ID, e.Fields["id"])
	delivered := stage("ada", m1.ID)
	assert.Equal(t, StateDelivered, delivered.State)
	late, _, err = b.Overdue(ctx, 10*time.Minute)
	require.NoError(t, err)
	require.Len(t, late, 1)
	assert.Equal(t, "studio", late[0].Name, "ada's is delivered, studio's is not")

	// read, then a stamp of an earlier state, then acted, then earlier ones again: never back.
	f.Advance(time.Minute)
	n, err := b.Stamp(ctx, "ada", StateRead, m1.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	read := stage("ada", m1.ID)
	assert.Equal(t, StateRead, read.State)
	assert.True(t, read.At.After(delivered.At))
	f.Advance(time.Minute)
	n, err = b.Stamp(ctx, "ada", StateDelivered, m1.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "a stamp of a state behind the message's moves nothing")
	assert.Equal(t, read, stage("ada", m1.ID), "its state and its time stay")
	n, err = b.Stamp(ctx, "ada", StateRead, m1.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "the same state again is dropped")
	_, err = b.Stamp(ctx, "ada", StateActed, m1.ID)
	require.NoError(t, err)
	acted := stage("ada", m1.ID)
	assert.Equal(t, StateActed, acted.State)

	// a claim hands it in again after ClaimAfter. Delivery stays at-least-once,
	// and the redelivered id is dropped: the receipt stays acted.
	f.Advance(ClaimAfter)
	e, ok, err = b.Recv(ctx, "ada", 0)
	require.NoError(t, err)
	require.True(t, ok, "at-least-once: the pending message is handed in again")
	assert.Equal(t, m1.ID, e.Fields["id"])
	assert.Equal(t, acted, stage("ada", m1.ID), "a redelivery moves nothing back")
	n, err = b.Stamp(ctx, "ada", StateRead, m1.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "a second read of an acted id is dropped")

	// ada answers a message she has not been handed: the answer is her act on it.
	m2, err := b.Send(ctx, Message{From: "rowan", To: []string{"ada"}, Subject: "s2", Body: "two"})
	require.NoError(t, err)
	_, err = b.Send(ctx, Message{From: "ada", To: []string{"rowan"}, Subject: "re s2", Re: m2.ID, Body: "done"})
	require.NoError(t, err)
	assert.Equal(t, StateActed, stage("ada", m2.ID).State)
	assert.Equal(t, StateSent, stage("rowan", mustLastID(t, b, "rowan")).State, "her answer is itself sent to rowan")

	all, _, err := b.Stages(ctx, "ada")
	require.NoError(t, err)
	require.Len(t, all, 2, "no ids asked: every receipt, in the order sent")
	assert.Equal(t, []string{m1.ID, m2.ID}, []string{all[0].ID, all[1].ID})

	_, err = b.Stamp(ctx, "ada", "seen", m1.ID)
	assert.ErrorAs(t, err, new(*Refusal))
	_, err = b.Stamp(ctx, "Ada", StateRead, m1.ID)
	assert.ErrorAs(t, err, new(*Refusal))
}

func mustLastID(t *testing.T, b *Bus, name string) string {
	t.Helper()
	got, _, err := b.Stages(context.Background(), name)
	require.NoError(t, err)
	require.NotEmpty(t, got)
	return got[len(got)-1].ID
}

// A receipt the store will not take is told to OnReceiptError and never stops
// the recv or the send it rides on.
func TestAReceiptTheStoreRefusesNeverStopsARecvOrASend(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := NewFake(time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC), "rowan", "ada")
	f.Friends = []string{"ada"}
	var told []error
	b := &Bus{Store: &refusingStamp{Fake: f}, OnReceiptError: func(err error) { told = append(told, err) }}
	m, err := b.Send(ctx, Message{From: "rowan", To: []string{"ada"}, Subject: "s", Body: "x"})
	require.NoError(t, err)
	e, ok, err := b.Recv(ctx, "ada", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m.ID, e.Fields["id"])
	require.Len(t, told, 1)
	assert.ErrorIs(t, told[0], assert.AnError)
}

type refusingStamp struct{ *Fake }

func (refusingStamp) Stamp(context.Context, string, string, ...string) (int64, error) {
	return 0, assert.AnError
}
