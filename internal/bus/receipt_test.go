package bus

import (
	"context"
	"strings"
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

// TestReceiptsMoveDeliveredReadActedAndDropDuplicates is the card's gate: the
// lifecycle, and one named test for each invariant tla/Bus2.tla checks with a
// reversed witness beside it (SPEC-BUS.md, message-receipts.w1).
func TestReceiptsMoveDeliveredReadActedAndDropDuplicates(t *testing.T) {
	t.Parallel()
	t.Run("Lifecycle", receiptLifecycle)

	// (1) read is stamped only after the adapter accepts the turn: a recv,
	// the daemon's look, stamps delivered and nothing past it, and read never
	// starts a receipt. The daemon's half, read only when Deliver answers
	// exit 0: internal/friend TestAReceiptIsReadOnlyWhenTheAdapterAcceptsTheTurn
	// (tla/Bus2.tla Accept, the readearly witness).
	t.Run("ReadOnlyAfterTheAdapterAccepts", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := NewFake(time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), "ada", "bob")
		b := &Bus{Store: f}
		m1, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "s1", Body: "one"})
		require.NoError(t, err)
		m2, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "s2", Body: "two"})
		require.NoError(t, err)
		_, ok, err := b.Recv(ctx, "bob", 0)
		require.NoError(t, err)
		require.True(t, ok)
		got, err := b.Receipts(ctx, "bob", m1.ID)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, ReceiptDelivered, got[0].State, "the reader's take is delivered, never read")
		require.NoError(t, b.MarkReceipts(ctx, "bob", ReceiptRead, m2.ID))
		got, err = b.Receipts(ctx, "bob", m2.ID)
		require.NoError(t, err)
		assert.Empty(t, got, "read never starts a receipt: m2 was never handed to a reader")
	})

	// (2) the duplicate drop is keyed on the store: Acted reads the receipt
	// record, so a bus with no memory of the turn (a daemon restarted, the
	// old reader dead between the turn's end and its ack) still answers the
	// redelivered id acted. The daemon's drop, its record line and ack:
	// internal/friend TestARestartedDaemonDropsARedeliveryTheStoreSaysWasActed
	// (tla/Bus2.tla Take, the memdrop witness).
	t.Run("DuplicateDropIsKeyedOnTheStore", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := NewFake(time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), "ada", "bob")
		before := &Bus{Store: f}
		m, err := before.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "s1", Body: "one"})
		require.NoError(t, err)
		_, ok, err := before.Recv(ctx, "bob", 0)
		require.NoError(t, err)
		require.True(t, ok)
		require.NoError(t, before.MarkReceipts(ctx, "bob", ReceiptActed, m.ID))

		after := &Bus{Store: f} // the restart: nothing carried over but the store
		f.Advance(ClaimAfter + time.Second)
		again, ok, err := after.Recv(ctx, "bob", 0)
		require.NoError(t, err)
		require.True(t, ok, "the unacked message is handed in again")
		require.Equal(t, m.ID, again.Message().ID)
		acted, err := after.Acted(ctx, "bob", m.ID)
		require.NoError(t, err)
		assert.True(t, acted, "the store says acted: the take drops it")
	})

	// (3) MarkReceipts is forward-only and atomic: one store call, never a
	// read followed by a separate write, refusing to move a receipt back
	// (tla/Bus2.tla ReceiptNeverMovesBack, the readwrite witness).
	t.Run("MarkReceiptsIsOneForwardStep", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := NewFake(time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), "ada", "bob")
		b := &Bus{Store: f}
		m, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "s1", Body: "one"})
		require.NoError(t, err)
		_, ok, err := b.Recv(ctx, "bob", 0)
		require.NoError(t, err)
		require.True(t, ok)
		for _, step := range []struct{ state, want string }{
			{ReceiptActed, ReceiptActed}, {ReceiptRead, ReceiptActed}, {ReceiptDelivered, ReceiptActed},
		} {
			trips := f.Trips
			require.NoError(t, b.MarkReceipts(ctx, "bob", step.state, m.ID))
			assert.Equal(t, 1, f.Trips-trips, "one store call for %s", step.state)
			got, err := b.Receipts(ctx, "bob", m.ID)
			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, step.want, got[0].State, "marking %s", step.state)
		}
		assert.Error(t, b.MarkReceipts(ctx, "bob", "seen", m.ID), "no such state")
		for _, c := range []struct{ cur, state, want string }{
			{"", ReceiptDelivered, ReceiptDelivered}, {"", ReceiptRead, ""}, {"", ReceiptActed, ""},
			{"delivered 2026-10-04T12:00:00Z", ReceiptRead, ReceiptRead},
			{"read 2026-10-04T12:00:00Z", ReceiptDelivered, "read"},
			{"acted 2026-10-04T12:00:00Z", ReceiptRead, "acted"},
		} {
			next, _ := Advance(c.cur, c.state, time.Date(2026, 10, 4, 12, 1, 0, 0, time.UTC))
			state, _, _ := strings.Cut(next, " ")
			assert.Equal(t, c.want, state, "%q then %s", c.cur, c.state)
		}
	})

	// (4) the reply path: a message whose re names another moves the
	// sender's receipt of it to acted only when her reader was handed it,
	// in the send's own transaction, so acted implies delivered
	// (tla/Bus2.tla Reply and ActedImpliesDelivered, the replyundelivered
	// witness).
	t.Run("ReplyActsOnlyOnADeliveredMessage", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		f := NewFake(time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), "ada", "bob")
		f.Friends = []string{"ada", "bob"}
		b := &Bus{Store: f}
		m1, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "s1", Body: "one"})
		require.NoError(t, err)
		m2, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "s2", Body: "two"})
		require.NoError(t, err)
		_, ok, err := b.Recv(ctx, "bob", 0) // m1 only
		require.NoError(t, err)
		require.True(t, ok)
		for _, id := range []string{m1.ID, m2.ID} {
			_, err = b.Send(ctx, Message{From: "bob", To: []string{"ada"}, Subject: "re", Re: id, Body: "on it"})
			require.NoError(t, err)
		}
		got, err := b.Receipts(ctx, "bob", m1.ID)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, ReceiptActed, got[0].State, "answered and delivered: acted")
		got, err = b.Receipts(ctx, "bob", m2.ID)
		require.NoError(t, err)
		assert.Empty(t, got, "answered but never handed to bob's reader: no receipt, never acted")
	})
}

// receiptLifecycle is the receipt lifecycle (delivered -> read -> acted),
// forward-only transitions, overdue detection, reply receipts (re), and the
// redelivery of an acted message (SPEC-BUS.md, message-receipts.w1).
func receiptLifecycle(t *testing.T) {
	ctx := context.Background()
	t0 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	f := NewFake(t0, "ada", "bob")
	f.Friends = []string{"ada", "bob"}
	b := &Bus{Store: f}

	// 1. Ada sends m1 to Bob
	m1, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "s1", Body: "one"})
	require.NoError(t, err)

	// Initially, no receipts for bob
	rcpts, err := b.Receipts(ctx, "bob", "")
	require.NoError(t, err)
	assert.Empty(t, rcpts)

	// Overdue check: at t0, not overdue (> 10m)
	overdue, err := b.Overdue(ctx, 10*time.Minute)
	require.NoError(t, err)
	assert.Empty(t, overdue)

	// Advance past 10m: m1 is now overdue (short of delivered)
	f.Advance(11 * time.Minute)
	overdue, err = b.Overdue(ctx, 10*time.Minute)
	require.NoError(t, err)
	require.Len(t, overdue, 1)
	assert.Equal(t, m1.ID, overdue[0].Message.ID)
	assert.Equal(t, "bob", overdue[0].To)
	assert.Equal(t, StreamOf("bob"), overdue[0].Stream)
	assert.True(t, overdue[0].Age >= 11*time.Minute)

	// 2. Bob receives m1 -> marks receipt "delivered"
	e, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m1.ID, e.Message().ID)

	rcpts, err = b.Receipts(ctx, "bob", m1.ID)
	require.NoError(t, err)
	require.Len(t, rcpts, 1)
	assert.Equal(t, ReceiptDelivered, rcpts[0].State)
	assert.Equal(t, m1.ID, rcpts[0].ID)

	// Once delivered, m1 is no longer overdue
	overdue, err = b.Overdue(ctx, 10*time.Minute)
	require.NoError(t, err)
	assert.Empty(t, overdue)

	// 3. Forward progression: mark "read"
	f.Advance(time.Minute)
	err = b.MarkReceipts(ctx, "bob", ReceiptRead, m1.ID)
	require.NoError(t, err)
	rcpts, err = b.Receipts(ctx, "bob", m1.ID)
	require.NoError(t, err)
	require.Len(t, rcpts, 1)
	assert.Equal(t, ReceiptRead, rcpts[0].State)

	// Attempting to move backward to "delivered" is a no-op: stays "read"
	err = b.MarkReceipts(ctx, "bob", ReceiptDelivered, m1.ID)
	require.NoError(t, err)
	rcpts, err = b.Receipts(ctx, "bob", m1.ID)
	require.NoError(t, err)
	require.Len(t, rcpts, 1)
	assert.Equal(t, ReceiptRead, rcpts[0].State)

	// 4. Forward progression: mark "acted"
	f.Advance(time.Minute)
	err = b.MarkReceipts(ctx, "bob", ReceiptActed, m1.ID)
	require.NoError(t, err)
	rcpts, err = b.Receipts(ctx, "bob", m1.ID)
	require.NoError(t, err)
	require.Len(t, rcpts, 1)
	assert.Equal(t, ReceiptActed, rcpts[0].State)

	// Attempting to move backward to "read" or "delivered" is a no-op
	err = b.MarkReceipts(ctx, "bob", ReceiptRead, m1.ID)
	require.NoError(t, err)
	err = b.MarkReceipts(ctx, "bob", ReceiptDelivered, m1.ID)
	require.NoError(t, err)
	rcpts, err = b.Receipts(ctx, "bob", m1.ID)
	require.NoError(t, err)
	require.Len(t, rcpts, 1)
	assert.Equal(t, ReceiptActed, rcpts[0].State)

	// 5. Reply with "re": Ada sends m2 to Bob, Bob replies naming m2.ID
	m2, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "s2", Body: "two"})
	require.NoError(t, err)

	// Bob receives m2 -> delivered
	e2, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m2.ID, e2.Message().ID)

	rcpts, err = b.Receipts(ctx, "bob", m2.ID)
	require.NoError(t, err)
	require.Len(t, rcpts, 1)
	assert.Equal(t, ReceiptDelivered, rcpts[0].State)

	// Bob sends reply to Ada with re=m2.ID -> marks Bob's receipt of m2 as "acted"
	_, err = b.Send(ctx, Message{From: "bob", To: []string{"ada"}, Subject: "re: s2", Re: m2.ID, Body: "acknowledged"})
	require.NoError(t, err)

	rcpts, err = b.Receipts(ctx, "bob", m2.ID)
	require.NoError(t, err)
	require.Len(t, rcpts, 1)
	assert.Equal(t, ReceiptActed, rcpts[0].State, "reply with re marks answered message acted")

	// 6. Idempotent take: delivery is at-least-once, so an acted message can
	// be handed in again (a claim after ClaimAfter, the reader having died
	// before its ack). The redelivery leaves the receipt acted, and Acted is
	// what the daemon's take asks before it pushes (the drop, its record
	// line and its ack: internal/friend TestDaemonDropsDuplicateDeliveryOfActedMessage
	// and TestARestartedDaemonDropsARedeliveryTheStoreSaysWasActed).
	acted, err := b.Acted(ctx, "bob", m1.ID)
	require.NoError(t, err)
	assert.True(t, acted, "m1's turn ended acted")
	f.Advance(ClaimAfter + time.Second)
	again, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok, "the unacked m1 is handed in again")
	assert.Equal(t, m1.ID, again.Message().ID)
	rcpts, err = b.Receipts(ctx, "bob", m1.ID)
	require.NoError(t, err)
	require.Len(t, rcpts, 1)
	assert.Equal(t, ReceiptActed, rcpts[0].State, "a redelivery never moves an acted receipt back to delivered")
	acted, err = b.Acted(ctx, "bob", again.Message().ID)
	require.NoError(t, err)
	assert.True(t, acted, "the redelivered id is one the take drops")
	m3, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "s3", Body: "three"})
	require.NoError(t, err)
	acted, err = b.Acted(ctx, "bob", m3.ID)
	require.NoError(t, err)
	assert.False(t, acted, "a message never acted is taken")
	acted, err = b.Acted(ctx, "bob", m2.ID)
	require.NoError(t, err)
	assert.True(t, acted, "answered (re) is acted too")
	_, err = b.Acted(ctx, "Bad Name", m1.ID)
	var refusal *Refusal
	assert.ErrorAs(t, err, &refusal)

	// 7. History is no alarm: a message a reader took before receipts were
	// kept (no receipt at all) is delivered, by the group; only what no
	// reader was ever handed is short of delivered.
	_, err = b.Send(ctx, Message{From: "bob", To: []string{"ada"}, Subject: "old", Body: "before receipts"})
	require.NoError(t, err)
	require.NoError(t, f.EnsureGroup(ctx, StreamOf("ada"), "ada"))
	old, err := f.Read(ctx, StreamOf("ada"), "ada", "ada", 0, 10) // a reader with no receipts: bob's answer and "old"
	require.NoError(t, err)
	require.Len(t, old, 2)
	f.Advance(time.Hour)
	overdue, err = b.Overdue(ctx, 10*time.Minute)
	require.NoError(t, err)
	require.Len(t, overdue, 1, "m3, never handed to bob's reader, and not ada's old message")
	assert.Equal(t, m3.ID, overdue[0].Message.ID)

	// Check receipts listing order: sorted by time, then id
	allRcpts, err := b.Receipts(ctx, "bob", "")
	require.NoError(t, err)
	require.Len(t, allRcpts, 2, "m3 was never delivered: no receipt")
	assert.Equal(t, m1.ID, allRcpts[0].ID)
	assert.Equal(t, m2.ID, allRcpts[1].ID)
}
