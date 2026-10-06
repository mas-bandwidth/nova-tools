package bus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A message moves delivered, read, acted and never back; a redelivery after
// ClaimAfter leaves it where it is; a reply naming it is its acted; and
// overdue lists the one still short of delivered past the age, and no other
// (SPEC-BUS.md, receipts; tla/Bus2.tla, ReceiptsOnlyForward).
func TestReceiptsMoveDeliveredReadActedAndDropDuplicates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := NewFake(time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC), "rowan", "ada")
	b := &Bus{Store: f}
	state := func(as, id string) (string, time.Time) {
		t.Helper()
		got, _, err := b.Stages(ctx, as, id)
		require.NoError(t, err)
		require.Len(t, got, 1)
		return got[0].State, got[0].At
	}

	m1, err := b.Send(ctx, Message{From: "rowan", To: []string{"ada"}, Subject: "one", Body: "one"})
	require.NoError(t, err)
	assert.Equal(t, StateNone, func() string { s, _ := state("ada", m1.ID); return s }(), "sent, not taken")

	// overdue: nothing at once, the message once it has waited past the age
	late, _, err := b.Overdue(ctx, 10*time.Minute)
	require.NoError(t, err)
	assert.Empty(t, late)
	f.Advance(11 * time.Minute)
	late, _, err = b.Overdue(ctx, 10*time.Minute)
	require.NoError(t, err)
	require.Len(t, late, 1)
	assert.Equal(t, Late{To: "ada", ID: m1.ID, From: "rowan", Subject: "one", Age: late[0].Age}, late[0])
	assert.GreaterOrEqual(t, late[0].Age, 11*time.Minute)

	// delivered: the reader takes it off its stream
	e, ok, err := b.Recv(ctx, "ada", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, m1.ID, e.Message().ID)
	s, deliveredAt := state("ada", m1.ID)
	assert.Equal(t, StateDelivered, s)
	assert.False(t, deliveredAt.IsZero())
	late, _, err = b.Overdue(ctx, 10*time.Minute)
	require.NoError(t, err)
	assert.Empty(t, late, "delivered is no longer overdue")

	// read, then a delivered write finds it past delivered and moves nothing
	f.Advance(time.Minute)
	n, err := b.Forward(ctx, "ada", StateRead, m1.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	n, err = b.Forward(ctx, "ada", StateDelivered, m1.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "never back")
	n, err = b.Forward(ctx, "ada", StateRead, m1.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "the same state again is no move")

	// at-least-once: the claim after ClaimAfter hands it in again, and it stays read
	f.Advance(ClaimAfter + time.Minute)
	e, ok, err = b.Recv(ctx, "ada", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, m1.ID, e.Message().ID)
	s, _ = state("ada", m1.ID)
	assert.Equal(t, StateRead, s, "a redelivery moves nothing back")

	// acted, and read after it moves nothing
	n, err = b.Forward(ctx, "ada", StateActed, m1.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	n, err = b.Forward(ctx, "ada", StateRead, m1.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	s, actedAt := state("ada", m1.ID)
	assert.Equal(t, StateActed, s)
	assert.True(t, actedAt.After(deliveredAt))

	// a reply naming a message is its acted, with no delivered or read before it
	m2, err := b.Send(ctx, Message{From: "rowan", To: []string{"ada"}, Subject: "two", Body: "two"})
	require.NoError(t, err)
	_, err = b.Send(ctx, Message{From: "ada", To: []string{"rowan"}, Subject: "re two", Re: m2.ID, Body: "done"})
	require.NoError(t, err)
	s, _ = state("ada", m2.ID)
	assert.Equal(t, StateActed, s)

	// receipts lists each message of the recipient with its state, oldest first
	all, now, err := b.Stages(ctx, "ada")
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, []string{m1.ID, m2.ID}, []string{all[0].ID, all[1].ID})
	assert.False(t, now.IsZero())

	// a name or a state that is none is refused
	_, err = b.Forward(ctx, "Bad Name", StateRead, m1.ID)
	var refusal *Refusal
	assert.ErrorAs(t, err, &refusal)
	_, err = b.Forward(ctx, "ada", "seen", m1.ID)
	assert.ErrorAs(t, err, &refusal)
	_, err = b.Forward(ctx, "ada", StateNone, m1.ID)
	assert.ErrorAs(t, err, &refusal, "none is no receipt to write")
}
