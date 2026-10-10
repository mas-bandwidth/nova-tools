package bus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A message's receipt moves delivered (recv), read (the turn carrying it
// started), acted (the turn ended at exit 0, or a reply naming it) and never
// back; overdue lists a message still short of delivered past its age; and a
// message the claim hands in again after it was acted comes back saying so,
// its receipt still acted, so the take drops it (SPEC-BUS.md,
// message-receipts; tla/Bus2Receipts.tla ReceiptNeverMovesBack, ActedImpliesDelivered,
// NoIdActedTwice).
func TestReceiptsMoveDeliveredReadActedAndDropDuplicates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	start := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	f := NewFake(start, "rowan", "ada", "bob")
	f.Friends = []string{"ada", "bob"}
	var stampErrs []error
	b := &Bus{Store: f, OnStampError: func(err error) { stampErrs = append(stampErrs, err) }}

	m1, err := b.Send(ctx, Message{From: "rowan", To: []string{"ada"}, Subject: "s1", Body: "one"})
	require.NoError(t, err)
	m2, err := b.Send(ctx, Message{From: "rowan", To: []string{"ada"}, Subject: "s2", Body: "two"})
	require.NoError(t, err)
	m3, err := b.Send(ctx, Message{From: "rowan", To: []string{"bob"}, Subject: "s3", Body: "three"})
	require.NoError(t, err)

	t.Run("nothing is overdue before its age", func(t *testing.T) {
		late, _, err := b.Overdue(ctx, 10*time.Minute)
		require.NoError(t, err)
		assert.Empty(t, late)
	})

	f.Advance(11 * time.Minute)
	t.Run("a message never delivered past its age is overdue, on every stream", func(t *testing.T) {
		late, _, err := b.Overdue(ctx, 10*time.Minute)
		require.NoError(t, err)
		require.Len(t, late, 3)
		assert.Equal(t, []string{m1.ID, m2.ID, m3.ID}, []string{late[0].ID, late[1].ID, late[2].ID})
		assert.Equal(t, "ada", late[0].Name)
		assert.Equal(t, "bob", late[2].Name)
		assert.Equal(t, "new", late[0].State)
		assert.GreaterOrEqual(t, late[0].Age, 11*time.Minute)
	})

	e, ok, err := b.Recv(ctx, "ada", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, m1.ID, e.Message().ID)
	assert.Equal(t, "", e.Stage, "a first delivery finds no receipt")
	deliveredAt := f.now // the store's time of the stamp

	t.Run("recv stamps delivered at the store's time; overdue drops it", func(t *testing.T) {
		got, now, err := b.Stages(ctx, "ada", m1.ID, m2.ID)
		require.NoError(t, err)
		assert.Equal(t, Delivered, got[0].State)
		assert.Equal(t, deliveredAt, got[0].At)
		assert.Equal(t, "", got[1].State, "m2 is not delivered")
		assert.Equal(t, now.Sub(got[0].At), got[0].Age(now))
		late, _, err := b.Overdue(ctx, 10*time.Minute)
		require.NoError(t, err)
		require.Len(t, late, 2)
		assert.Equal(t, m2.ID, late[0].ID)
		assert.Equal(t, m3.ID, late[1].ID)
	})

	t.Run("read, then acted by a reply; never back", func(t *testing.T) {
		prior, err := b.Stamp(ctx, "ada", Read, m1.ID)
		require.NoError(t, err)
		assert.Equal(t, []string{Delivered}, prior)
		prior, err = b.Stamp(ctx, "ada", Delivered, m1.ID)
		require.NoError(t, err)
		assert.Equal(t, []string{Read}, prior)
		got, _, err := b.Stages(ctx, "ada", m1.ID)
		require.NoError(t, err)
		assert.Equal(t, Read, got[0].State, "delivered after read moves nothing")

		_, err = b.Send(ctx, Message{From: "ada", To: []string{"rowan"}, Subject: "re s1", Re: m1.ID, Body: "done"})
		require.NoError(t, err)
		got, _, err = b.Stages(ctx, "ada", m1.ID)
		require.NoError(t, err)
		assert.Equal(t, Acted, got[0].State, "a reply naming it acts on it")
		acked, err := b.AckEntry(ctx, "ada", e.Entry)
		require.NoError(t, err)
		require.True(t, acked)
		for _, back := range []string{Delivered, Read} {
			_, err = b.Stamp(ctx, "ada", back, m1.ID)
			require.NoError(t, err)
		}
		got, _, err = b.Stages(ctx, "ada", m1.ID)
		require.NoError(t, err)
		assert.Equal(t, Acted, got[0].State)
	})

	t.Run("never read or acted before delivered", func(t *testing.T) {
		for _, s := range []string{Read, Acted} {
			prior, err := b.Stamp(ctx, "ada", s, m2.ID)
			require.NoError(t, err)
			assert.Equal(t, []string{""}, prior)
		}
		_, err := b.Send(ctx, Message{From: "bob", To: []string{"rowan"}, Subject: "re s3", Re: m3.ID, Body: "seen in the log"})
		require.NoError(t, err)
		got, _, err := b.Stages(ctx, "bob", m3.ID)
		require.NoError(t, err)
		assert.Equal(t, "", got[0].State, "a reply to a message never delivered is no receipt of it")
	})

	t.Run("a redelivery after acted comes back acted, and stays acted", func(t *testing.T) {
		e2, ok, err := b.Recv(ctx, "ada", 0)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, m2.ID, e2.Message().ID)
		_, err = b.Stamp(ctx, "ada", Read, m2.ID)
		require.NoError(t, err)
		_, err = b.Stamp(ctx, "ada", Acted, m2.ID) // the turn ended at exit 0; its ack was lost
		require.NoError(t, err)
		f.Advance(ClaimAfter)
		again, ok, err := b.Recv(ctx, "ada", 0)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, m2.ID, again.Message().ID, "delivery is at least once: the claim hands it in again")
		assert.Equal(t, Acted, again.Stage, "the take is told it was acted")
		got, _, err := b.Stages(ctx, "ada", m2.ID)
		require.NoError(t, err)
		assert.Equal(t, Acted, got[0].State)
	})

	t.Run("a state that is none is refused, and so is a bad name", func(t *testing.T) {
		_, err := b.Stamp(ctx, "ada", "seen", m1.ID)
		var refusal *Refusal
		assert.ErrorAs(t, err, &refusal)
		_, _, err = b.Stages(ctx, "Bad Name")
		assert.ErrorAs(t, err, &refusal)
	})

	t.Run("a stamp the store refuses never stops a recv; it is said", func(t *testing.T) {
		_, err := b.Send(ctx, Message{From: "rowan", To: []string{"ada"}, Subject: "s4", Body: "four"})
		require.NoError(t, err)
		f.FailForward = assert.AnError
		e4, ok, err := b.Recv(ctx, "ada", 0)
		f.FailForward = nil
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, "s4", e4.Message().Subject)
		require.Len(t, stampErrs, 1)
		assert.Empty(t, e4.Stage)
	})
	assert.Len(t, stampErrs, 1)
}
