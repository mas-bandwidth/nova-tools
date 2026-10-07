package bus

// The bus model's invariants as unit tests over the package's in-memory fake
// (the Store in fake_test.go). Each test is named for its invariant, cites its
// module, and drives the sequence of operations the invariant's reversed
// witness records (tla/MCBus2*.cfg), so a change that lets the code drift
// from the model turns it red. The models: tla/Bus2.tla (the delivery
// machine), tla/Bus2Receipts.tla (the receipts beside it) and
// tla/BusSendOnce.tla (a send under a token).

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// entryIDs is the messages' ids in the entries' order, the shape the model's
// log holds.
func entryIDs(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Message().ID
	}
	return out
}

// OnEveryStreamOrNone: a message is on every one of its recipients' streams,
// or on none: a sent message is reachable by every recipient it names
// (tla/Bus2.tla: OnEveryStreamOrNone). The "partial" witness writes some
// streams and stops; one send is one AddAll, so a store that refuses it
// leaves every stream and the log empty.
func TestBus2OnEveryStreamOrNone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, f := rig(t, "ada", "bob", "cy")
	f.Fail = assert.AnError
	_, err := b.Send(ctx, Message{From: "ada", To: []string{"bob", "cy"}, Subject: "s", Body: "x"})
	require.ErrorIs(t, err, f.Fail)
	for _, s := range []string{StreamOf("bob"), StreamOf("cy"), LogKey} {
		assert.Equal(t, 0, f.Len(s), "a send that did not commit leaves %s empty", s)
	}
	f.Fail = nil
	m, err := b.Send(ctx, Message{From: "ada", To: []string{"bob", "cy"}, Subject: "s", Body: "x"})
	require.NoError(t, err)
	for _, s := range []string{StreamOf("bob"), StreamOf("cy")} {
		got := f.streams[s]
		require.Len(t, got, 1, s)
		assert.Equal(t, m, got[0].Message(), "every recipient's stream holds the sent message")
	}
	log, err := b.Log(ctx, "-")
	require.NoError(t, err)
	require.Len(t, log, 1)
	assert.Equal(t, m, log[0].Message())
}

// OnStreamWasSent: a message on a stream was sent, and to that recipient:
// nothing is delivered that was not sent (tla/Bus2.tla: OnStreamWasSent). A
// recipient no send names has no message, and a recv for it hands none out.
func TestBus2OnStreamWasSent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, f := rig(t, "ada", "bob", "cy")
	m, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "s", Body: "x"})
	require.NoError(t, err)
	assert.Equal(t, 0, f.Len(StreamOf("cy")), "cy was named by no send")
	for _, e := range f.streams[StreamOf("bob")] {
		got := e.Message()
		assert.Equal(t, m.ID, got.ID)
		assert.Contains(t, got.To, "bob")
	}
	_, ok, err := b.Recv(ctx, "cy", 0)
	require.NoError(t, err)
	assert.False(t, ok, "nothing was sent to cy")
}

// NothingLost: a sent message is acked, or still on its recipient's stream
// for recv to hand out (new or pending) (tla/Bus2.tla: NothingLost). The log
// holds it at every step.
func TestBus2NothingLost(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, _ := rig(t, "ada", "bob")
	m, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	_, fresh, err := b.Peek(ctx, "bob")
	require.NoError(t, err)
	assert.Equal(t, []string{m.ID}, entryIDs(fresh), "sent, never delivered: new")
	e, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	pending, fresh, err := b.Peek(ctx, "bob")
	require.NoError(t, err)
	assert.Equal(t, []string{m.ID}, entryIDs(pending), "delivered, not acked: pending")
	assert.Empty(t, fresh)
	acked, err := b.AckEntry(ctx, "bob", e.Entry)
	require.NoError(t, err)
	require.True(t, acked)
	pending, fresh, err = b.Peek(ctx, "bob")
	require.NoError(t, err)
	assert.Empty(t, pending, "acked: off the pending list, never lost from the log")
	assert.Empty(t, fresh)
	log, err := b.Log(ctx, "-")
	require.NoError(t, err)
	require.Len(t, log, 1)
	assert.Equal(t, m, log[0].Message())
}

// PendingBeforeNew: a recv that hands out a new message found nothing pending
// that a dead consumer held; after a reader's death what it held comes first
// (tla/Bus2.tla: PendingBeforeNew). The "newfirst" witness reads new entries
// before it claims; a live holder keeps its message, so the claim waits for
// ClaimAfter.
func TestBus2PendingBeforeNew(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, f := rig(t, "ada", "bob")
	m1, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	e1, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, m1.ID, e1.Message().ID)
	m2, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	e2, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m2.ID, e2.Message().ID, "the live holder keeps m1")
	f.Advance(ClaimAfter)
	m3, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	again, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m1.ID, again.Message().ID, "the dead reader's pending message is handed out first")
	_, fresh, err := b.Peek(ctx, "bob")
	require.NoError(t, err)
	assert.Equal(t, []string{m3.ID}, entryIDs(fresh), "the new message is still new")
}

// HeldStaysHeld: a message held by a live consumer stays with it: it is
// delivered once while held, and only an ack or the holder's death moves it
// (tla/Bus2.tla: HeldStaysHeld). The "steal" witness claims with min-idle 0
// and delivers it twice.
func TestBus2HeldStaysHeld(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, _ := rig(t, "ada", "bob")
	m1, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	e1, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, m1.ID, e1.Message().ID)
	_, ok, err = b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	assert.False(t, ok, "the live holder keeps m1, and nothing new waits")
	pending, _, err := b.Peek(ctx, "bob")
	require.NoError(t, err)
	assert.Equal(t, []string{m1.ID}, entryIDs(pending))
}

// AckOnlyDelivered: only a delivered message is acked; nothing goes from new
// to acked (tla/Bus2.tla: AckOnlyDelivered). The "ackundelivered" witness acks
// any id on the stream; an ack by id of one never delivered answers false and
// leaves it new.
func TestBus2AckOnlyDelivered(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, _ := rig(t, "ada", "bob")
	m, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	got, err := b.Ack(ctx, "bob", []string{m.ID})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{m.ID: false}, got, "a new message is not acked")
	_, fresh, err := b.Peek(ctx, "bob")
	require.NoError(t, err)
	assert.Equal(t, []string{m.ID}, entryIDs(fresh), "it is still new")
	_, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	got, err = b.Ack(ctx, "bob", []string{m.ID})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{m.ID: true}, got, "delivered: acked")
}

// AckedStaysAcked: once acked, acked; a second ack changes nothing and the
// message is never handed out as new again (tla/Bus2.tla: AckedStaysAcked).
// The "ackreopens" witness deletes and re-adds an acked message as new.
func TestBus2AckedStaysAcked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, _ := rig(t, "ada", "bob")
	m, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	_, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	got, err := b.Ack(ctx, "bob", []string{m.ID})
	require.NoError(t, err)
	require.Equal(t, map[string]bool{m.ID: true}, got)
	got, err = b.Ack(ctx, "bob", []string{m.ID})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{m.ID: false}, got, "the same ack again changes nothing")
	_, ok, err = b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	assert.False(t, ok, "an acked message is never handed out as new again")
	pending, fresh, err := b.Peek(ctx, "bob")
	require.NoError(t, err)
	assert.Empty(t, pending)
	assert.Empty(t, fresh)
}

// EveryMessageIsAcked: every message a recipient was sent is acked once the
// crashes stop and a consumer keeps reading (tla/Bus2.tla: EveryMessageIsAcked).
// Over the fake the loop is bounded: each sent message is handed out once.
func TestBus2EveryMessageIsAcked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, _ := rig(t, "ada", "bob")
	sent := map[string]bool{}
	for range 3 {
		m, err := b.Send(ctx, msg("ada", "bob"))
		require.NoError(t, err)
		sent[m.ID] = true
	}
	acked := map[string]bool{}
	for range len(sent) {
		e, ok, err := b.Recv(ctx, "bob", 0)
		require.NoError(t, err)
		require.True(t, ok, "a sent message still waits")
		got, err := b.Ack(ctx, "bob", []string{e.Message().ID})
		require.NoError(t, err)
		require.True(t, got[e.Message().ID])
		acked[e.Message().ID] = true
	}
	assert.Equal(t, sent, acked, "every sent message was acked once")
	pending, fresh, err := b.Peek(ctx, "bob")
	require.NoError(t, err)
	assert.Empty(t, pending)
	assert.Empty(t, fresh)
}

// ReceiptNeverMovesBack: a receipt never moves back (tla/Bus2Receipts.tla:
// ReceiptNeverMovesBack). The "backstamp" witness writes delivered over
// whatever the receipt held; a stamp of an earlier state answers the state it
// found and writes nothing.
func TestBus2ReceiptsReceiptNeverMovesBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, _ := rig(t, "ada", "bob")
	m, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	for _, step := range []struct{ state, prior string }{
		{Delivered, ""},
		{Read, Delivered},
		{Acted, Read},
		{Delivered, Acted},
		{Read, Acted},
	} {
		prior, err := b.Stamp(ctx, "bob", step.state, m.ID)
		require.NoError(t, err)
		assert.Equal(t, []string{step.prior}, prior, "the receipt at %s", step.state)
	}
	got, _, err := b.Stages(ctx, "bob", m.ID)
	require.NoError(t, err)
	assert.Equal(t, Acted, got[0].State, "the highest state stands")
}

// ActedImpliesDelivered: a receipt past none means the message was taken off
// its recipient's stream, pending or acked since (tla/Bus2Receipts.tla:
// ActedImpliesDelivered). A message never delivered has no receipt: read or
// acted on none moves nothing, and a reply naming it stamps nothing.
func TestBus2ReceiptsActedImpliesDelivered(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, _ := rig(t, "ada", "bob")
	m, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	for _, state := range []string{Read, Acted} {
		prior, err := b.Stamp(ctx, "bob", state, m.ID)
		require.NoError(t, err)
		assert.Equal(t, []string{""}, prior, "none cannot start at %s", state)
	}
	got, _, err := b.Stages(ctx, "bob", m.ID)
	require.NoError(t, err)
	assert.Equal(t, "", got[0].State)
	_, err = b.Send(ctx, Message{From: "bob", To: []string{"ada"}, Subject: "re", Body: "x", Re: m.ID})
	require.NoError(t, err)
	got, _, err = b.Stages(ctx, "bob", m.ID)
	require.NoError(t, err)
	assert.Equal(t, "", got[0].State, "a reply to an undelivered message is no receipt")
	_, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	_, err = b.Stamp(ctx, "bob", Acted, m.ID)
	require.NoError(t, err)
	got, _, err = b.Stages(ctx, "bob", m.ID)
	require.NoError(t, err)
	assert.Equal(t, Acted, got[0].State)
}

// NoIdActedTwice: no message id is acted twice: a redelivery after acted comes
// back saying so and the receipt stays acted (tla/Bus2Receipts.tla:
// NoIdActedTwice). The "pushdup" witness pushes every message it is handed,
// acted or not.
func TestBus2ReceiptsNoIdActedTwice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, f := rig(t, "ada", "bob")
	m, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	_, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	_, err = b.Stamp(ctx, "bob", Read, m.ID)
	require.NoError(t, err)
	_, err = b.Stamp(ctx, "bob", Acted, m.ID)
	require.NoError(t, err)
	f.Advance(ClaimAfter)
	again, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok, "a delivery is at least once")
	assert.Equal(t, m.ID, again.Message().ID)
	assert.Equal(t, Acted, again.Stage, "the take is told it was acted, never acted again")
	prior, err := b.Stamp(ctx, "bob", Acted, m.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{Acted}, prior, "acted stays acted")
}

// OneMessageWithinLife: two messages under one token are a life apart; inside
// the life a retry, however often, is the one message (tla/BusSendOnce.tla:
// OneMessageWithinLife). The "checkapart", "newid" and "dropearly" witnesses
// each write a second message inside the life; past the life a retry is
// refused, never written again.
func TestBusSendOnceOneMessageWithinLife(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, f := rig(t, "ada", "bob", "cy")
	first, err := b.Send(ctx, tokened("t-1"))
	require.NoError(t, err)
	for range 3 {
		again, err := b.Send(ctx, tokened("t-1"))
		require.NoError(t, err)
		assert.Equal(t, first.ID, again.ID, "a retry inside the life is the one message")
	}
	one(t, f)
	f.Advance(DefaultTokenLife)
	_, err = b.Send(ctx, tokened("t-1"))
	var r *Refusal
	require.ErrorAs(t, err, &r)
	assert.Contains(t, err.Error(), "past its life")
	one(t, f)
}

// AnswerIsTheOriginal: an answer names the message written under its token
// with its arguments; other arguments are never told they went
// (tla/BusSendOnce.tla: AnswerIsTheOriginal). The "nofingerprint" witness
// answers the record whatever the arguments.
func TestBusSendOnceAnswerIsTheOriginal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, f := rig(t, "ada", "bob", "cy")
	first, err := b.Send(ctx, tokened("t-1"))
	require.NoError(t, err)
	again, err := b.Send(ctx, tokened("t-1"))
	require.NoError(t, err)
	assert.Equal(t, first, again, "the retry answers the original message, id and at")
	m := tokened("t-1")
	m.Body = "another body\n"
	_, err = b.Send(ctx, m)
	var r *Refusal
	require.ErrorAs(t, err, &r)
	assert.Contains(t, err.Error(), "already sent "+first.ID)
	one(t, f)
}
