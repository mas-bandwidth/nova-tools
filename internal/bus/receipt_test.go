package bus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A message is owed a receipt by each friend it names but the sender, never
// by a machine; a friend's reply naming it (re) is her receipt, in the send's
// own transaction; a receipt is idempotent; and a send that fails marks
// nothing (SPEC-BUS.md, fr-delivery-receipts.w1).
func TestAFriendOwesAReceiptUntilHerSessionAcksOrAnswers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := NewFake(time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), "rowan", "ada", "bob", "studio")
	f.Friends = []string{"ada", "bob"}
	b := &Bus{Store: f}

	m1, err := b.Send(ctx, Message{From: "rowan", To: []string{"ada", "studio"}, CC: []string{"bob"}, Subject: "s1", Body: "one"})
	require.NoError(t, err)
	m2, err := b.Send(ctx, Message{From: "ada", To: []string{"ada", "bob"}, Subject: "s2", Body: "two"})
	require.NoError(t, err)
	got, err := b.Undelivered(ctx, "ada", "bob", "studio")
	require.NoError(t, err)
	assert.Equal(t, 1, got[0].Count, "ada owes no receipt for her own message")
	assert.Equal(t, m1.ID, got[0].OldestID)
	assert.Equal(t, 2, got[1].Count)
	assert.Equal(t, m1.ID, got[1].OldestID, "the oldest by the store's clock")
	assert.Equal(t, m1.At, got[1].OldestAt)
	assert.Equal(t, 0, got[2].Count, "a machine is owed nothing")
	assert.Equal(t, time.Duration(0), got[2].Age(m1.At.Add(time.Hour)))
	assert.Equal(t, time.Hour, got[1].Age(m1.At.Add(time.Hour)))

	// bob answers m1: his receipt of it, in the reply's transaction
	_, err = b.Send(ctx, Message{From: "bob", To: []string{"rowan"}, Subject: "re s1", Re: m1.ID, Body: "got it"})
	require.NoError(t, err)
	got, err = b.Undelivered(ctx, "bob")
	require.NoError(t, err)
	assert.Equal(t, 1, got[0].Count)
	assert.Equal(t, m2.ID, got[0].OldestID)

	// ada's session acks by id: her receipt, though no reader holds it pending
	acked, err := b.Ack(ctx, "ada", []string{m1.ID})
	require.NoError(t, err)
	assert.False(t, acked[m1.ID], "never read off the stream: not pending")
	n, err := b.Receipt(ctx, "ada", []string{m1.ID})
	require.NoError(t, err)
	assert.Equal(t, 0, n, "a second receipt finds nothing owed")
	got, err = b.Undelivered(ctx, "ada")
	require.NoError(t, err)
	assert.Equal(t, 0, got[0].Count)

	// WouldAck writes nothing, the receipt included
	_, err = b.WouldAck(ctx, "bob", []string{m2.ID})
	require.NoError(t, err)
	got, err = b.Undelivered(ctx, "bob")
	require.NoError(t, err)
	assert.Equal(t, 1, got[0].Count)

	// a store that fails marks nothing
	f.Fail = assert.AnError
	_, err = b.Send(ctx, Message{From: "rowan", To: []string{"bob"}, Subject: "s3", Body: "three"})
	require.Error(t, err)
	f.Fail = nil
	got, err = b.Undelivered(ctx, "bob")
	require.NoError(t, err)
	assert.Equal(t, 1, got[0].Count)

	_, err = b.Undelivered(ctx, "Bad Name")
	var refusal *Refusal
	assert.ErrorAs(t, err, &refusal)
}

// A message's receipt moves delivered, read, acted and never back, by the
// store's clock; a field begins only at delivered; an answer (re) is acted; a
// message still short of delivered past the bound is overdue; and a message
// handed in again after ClaimAfter moves nothing back: the receipt stays acted
// for the daemon to drop it (SPEC-BUS.md, message-receipts-r2.w2).
func TestReceiptsMoveDeliveredReadActedAndDropDuplicates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	start := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	f := NewFake(start, "rowan", "ada")
	f.Friends = []string{"ada"}
	b := &Bus{Store: f}
	state := func(as, id string) string {
		t.Helper()
		got, _, err := b.Receipts(ctx, as, id)
		require.NoError(t, err)
		require.Len(t, got, 1)
		return got[0].State
	}

	m1, err := b.Send(ctx, Message{From: "rowan", To: []string{"ada"}, Subject: "one", Body: "one"})
	require.NoError(t, err)
	m2, err := b.Send(ctx, Message{From: "rowan", To: []string{"ada"}, Subject: "two", Body: "two"})
	require.NoError(t, err)
	assert.Equal(t, "none", state("ada", m1.ID))

	t.Run("overdue lists what no reader took, past the bound, and nothing before it", func(t *testing.T) {
		late, _, err := b.Overdue(ctx, 10*time.Minute)
		require.NoError(t, err)
		assert.Empty(t, late)
		f.Advance(11 * time.Minute)
		late, now, err := b.Overdue(ctx, 10*time.Minute)
		require.NoError(t, err)
		require.Len(t, late, 2)
		assert.Equal(t, []string{m1.ID, m2.ID}, []string{late[0].ID, late[1].ID}, "oldest first")
		assert.Equal(t, "ada", late[0].Name)
		assert.Greater(t, now.Sub(late[0].At), 10*time.Minute)
	})

	t.Run("a field begins only at delivered", func(t *testing.T) {
		n, err := b.Advance(ctx, "ada", ReceiptActed, m1.ID)
		require.NoError(t, err)
		assert.Equal(t, 0, n)
		assert.Equal(t, "none", state("ada", m1.ID))
	})

	t.Run("a recv delivers, and the message is no longer overdue", func(t *testing.T) {
		e, ok, err := b.Recv(ctx, "ada", 0)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, m1.ID, e.Message().ID)
		assert.Equal(t, ReceiptDelivered, state("ada", m1.ID))
		late, _, err := b.Overdue(ctx, 10*time.Minute)
		require.NoError(t, err)
		require.Len(t, late, 1)
		assert.Equal(t, m2.ID, late[0].ID)
	})

	t.Run("a receipt moves forward only, by the store's time", func(t *testing.T) {
		f.Advance(time.Minute)
		n, err := b.Advance(ctx, "ada", ReceiptRead, m1.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, n)
		got, now, err := b.Receipts(ctx, "ada", m1.ID)
		require.NoError(t, err)
		assert.Equal(t, ReceiptRead, got[0].State)
		assert.Less(t, got[0].Age(now), 5*time.Second, "read at the store's now")
		for _, back := range []string{ReceiptDelivered, ReceiptRead} {
			n, err = b.Advance(ctx, "ada", back, m1.ID)
			require.NoError(t, err)
			assert.Equal(t, 0, n, "%s is not forward of read", back)
		}
		n, err = b.Advance(ctx, "ada", ReceiptActed, m1.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, n)
		for _, back := range ReceiptStates {
			n, err = b.Advance(ctx, "ada", back, m1.ID)
			require.NoError(t, err)
			assert.Equal(t, 0, n, "%s never moves acted back", back)
		}
		assert.Equal(t, ReceiptActed, state("ada", m1.ID))
	})

	t.Run("a message handed in again moves nothing back", func(t *testing.T) {
		f.Advance(ClaimAfter) // the reader that took m1 never acked: the claim hands it in again
		e, ok, err := b.Recv(ctx, "ada", 0)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, m1.ID, e.Message().ID, "at-least-once: the same message again")
		assert.Equal(t, ReceiptActed, state("ada", m1.ID), "its receipt stays acted, which tells the daemon to drop it")
	})

	t.Run("an answer naming a delivered message is acted", func(t *testing.T) {
		e, ok, err := b.Recv(ctx, "ada", 0)
		require.NoError(t, err)
		require.True(t, ok)
		require.Equal(t, m2.ID, e.Message().ID)
		assert.Equal(t, ReceiptDelivered, state("ada", m2.ID))
		_, err = b.Send(ctx, Message{From: "ada", To: []string{"rowan"}, Subject: "re two", Re: m2.ID, Body: "done"})
		require.NoError(t, err)
		assert.Equal(t, ReceiptActed, state("ada", m2.ID))
	})

	t.Run("a refusal names what it wants", func(t *testing.T) {
		var refusal *Refusal
		_, err := b.Advance(ctx, "ada", "seen", m1.ID)
		assert.ErrorAs(t, err, &refusal)
		_, err = b.Advance(ctx, "Bad Name", ReceiptRead, m1.ID)
		assert.ErrorAs(t, err, &refusal)
		_, _, err = b.Receipts(ctx, "Bad Name")
		assert.ErrorAs(t, err, &refusal)
	})
}

// An alarm is raised once per outage and cleared once, and its text names the
// store and the user and never more of a refused login than its word.
func TestASendAlarmIsOncePerOutage(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	var raised, cleared []Alarm
	w := &Watch{Store: "s:6379", User: "sprint", Raise: func(a Alarm) { raised = append(raised, a) }, Clear: func(a Alarm) { cleared = append(cleared, a) }}
	w.Observe(at, nil)
	w.Observe(at, &Refusal{[]string{"no"}})
	assert.Empty(t, raised)
	assert.False(t, w.Open())
	secret := errContaining("NOAUTH Authentication required. hunter2")
	w.Observe(at, secret)
	w.Observe(at.Add(time.Minute), errContaining("i/o timeout"))
	require.Len(t, raised, 1)
	assert.Equal(t, AlarmAuth, raised[0].Class)
	assert.Equal(t, "login refused (NOAUTH)", raised[0].Reason)
	assert.NotContains(t, raised[0].Text(), "hunter2", "the alarm carries the refusal's word, never the rest of its text")
	w.Observe(at.Add(2*time.Minute), nil)
	require.Len(t, cleared, 1)
	assert.Equal(t, 2, cleared[0].Failures)
	assert.Contains(t, cleared[0].Text(), "bus store s:6379 answers user sprint again: 2 sends failed (auth)")
	w.Observe(at.Add(3*time.Minute), nil)
	assert.Len(t, cleared, 1)
}

type errContaining string

func (e errContaining) Error() string { return string(e) }
