package bus

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The TLA+ modules beside this package state the bus's design as state, the
// operations the code owns as actions: tla/Bus2.tla is the delivery machine
// (Send, Recv, Ack over the streams and their consumer group), tla/Bus2Receipts.tla
// EXTENDS it with the receipts, and tla/BusSendOnce.tla is a send under a
// token. Each test below drives one model's sequence of operations over the
// package's in-memory fake (fake_test.go) and checks one invariant's formula,
// replaying the counterexample its recorded witness carries: the MCBus2Broken*.cfg
// cases are reversals the code holds against, and the BusSendOnce witnesses are
// the module's own (no .cfg records them); the invariants no witness breaks
// are checked against the reversal their formula names in the module.

// TestModelBus2OnEveryStreamOrNone checks OnEveryStreamOrNone (tla/Bus2.tla):
// a message in the log is on every one of its recipients' streams, or on none.
// It replays MCBus2BrokenPartial.cfg, the witness of a send that writes the
// streams one by one and may stop between them (no MULTI/EXEC): the send
// here names three recipients, and even a send whose response is lost has the
// entry on all of them at once, while a send the store refused has it on none.
func TestModelBus2OnEveryStreamOrNone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, f := rig(t, "ada", "bob", "cy", "dee")
	m, err := b.Send(ctx, Message{From: "ada", To: []string{"bob", "cy"}, CC: []string{"dee"}, Subject: "s", Body: "x"})
	require.NoError(t, err)
	ids := map[string]bool{}
	for _, s := range []string{StreamOf("bob"), StreamOf("cy"), StreamOf("dee"), LogKey} {
		assert.Equal(t, 1, f.Len(s), "%s holds the message exactly once", s)
		es, err := b.Store.Range(ctx, s, "-", "+", 10)
		require.NoError(t, err)
		require.NotEmpty(t, es, "%s holds the message", s)
		ids[es[0].Entry] = true
		assert.Equal(t, m.ID, es[0].Message().ID, "%s holds the message sent", s)
	}
	assert.Len(t, ids, 1, "one entry id on every stream: one transaction, not one write per stream")

	// or on none: a send whose answer is lost committed whole, and one the
	// store refused wrote nothing at all.
	lost, lf := rig(t, "ada", "bob")
	lf.Lose = errLost
	_, err = lost.Send(ctx, msg("ada", "bob"))
	require.ErrorIs(t, err, errLost, "the caller sees the lost response, not success")
	assert.Equal(t, 1, lf.Len(StreamOf("bob")), "the lost send committed on every stream")
	assert.Equal(t, 1, lf.Len(LogKey), "the log with it")
	down, df := rig(t, "ada", "bob")
	df.Fail = assert.AnError
	_, err = down.Send(ctx, msg("ada", "bob"))
	require.Error(t, err)
	assert.Equal(t, 0, df.Len(StreamOf("bob")), "a refused send writes no stream")
	assert.Equal(t, 0, df.Len(LogKey), "and no log")
}

// TestModelBus2OnStreamWasSent checks OnStreamWasSent (tla/Bus2.tla): every
// message on a stream was sent, and to that recipient -- nothing is delivered
// that was not sent. Each stream below holds only the messages that name it,
// and every entry on a stream parses to a message the log holds.
func TestModelBus2OnStreamWasSent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, f := rig(t, "ada", "bob", "cy", "dee")
	m1, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	m2, err := b.Send(ctx, msg("ada", "cy"))
	require.NoError(t, err)
	log, err := b.Log(ctx, "-")
	require.NoError(t, err)
	require.Len(t, log, 2, "both messages were sent")
	sent := map[string]bool{log[0].Message().ID: true, log[1].Message().ID: true}
	require.Contains(t, sent, m1.ID)
	require.Contains(t, sent, m2.ID)
	for _, s := range []string{StreamOf("bob"), StreamOf("cy")} {
		es, err := b.Store.Range(ctx, s, "-", "+", 10)
		require.NoError(t, err)
		require.Len(t, es, 1, "%s holds one message", s)
		assert.True(t, sent[es[0].Message().ID], "an entry on a stream is a message that was sent")
	}
	bobs, err := b.Store.Range(ctx, StreamOf("bob"), "-", "+", 10)
	require.NoError(t, err)
	assert.Equal(t, m1.ID, bobs[0].Message().ID, "bob's stream holds the message that names him, and no other")
	cys, err := b.Store.Range(ctx, StreamOf("cy"), "-", "+", 10)
	require.NoError(t, err)
	assert.Equal(t, m2.ID, cys[0].Message().ID, "cy's stream holds the message that names her, and no other")
	assert.Equal(t, 0, f.Len(StreamOf("dee")), "a name no message names holds nothing")
}

// TestModelBus2NothingLost checks NothingLost (tla/Bus2.tla): nothing is
// lost -- a sent message is acked, or still on the stream for recv to hand
// out (new or pending). Three messages go out: one is read and acked, one
// its reader dies holding (idle past ClaimAfter), one is never delivered; the
// union of the acked and what a recv can still hand out is all three.
func TestModelBus2NothingLost(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, f := rig(t, "ada", "bob")
	var sent []string
	for range 3 {
		m, err := b.Send(ctx, msg("ada", "bob"))
		require.NoError(t, err)
		sent = append(sent, m.ID)
	}
	// a reader takes the first message and acks it
	e, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	did, err := b.AckEntry(ctx, "bob", e.Entry)
	require.NoError(t, err)
	require.True(t, did)
	acked := []string{e.Message().ID}
	// a second reader takes the second and dies holding it: the clock runs
	// past ClaimAfter, so the claim can hand it to a live one again
	e2, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotEqual(t, acked[0], e2.Message().ID, "a second message was handed out")
	f.Advance(ClaimAfter)
	pending, fresh, err := b.Peek(ctx, "bob")
	require.NoError(t, err)
	var have []string
	for _, es := range [][]Entry{pending, fresh} {
		for _, e := range es {
			have = append(have, e.Message().ID)
		}
	}
	have = append(have, acked...)
	assert.ElementsMatch(t, sent, have, "acked, pending or new: every sent message is one of them")
	// and a live reader is handed the rest, each once
	var handed []string
	for {
		e, ok, err := b.Recv(ctx, "bob", 0)
		require.NoError(t, err)
		if !ok {
			break
		}
		did, err := b.AckEntry(ctx, "bob", e.Entry)
		require.NoError(t, err)
		require.True(t, did, "what recv hands out, ack takes")
		handed = append(handed, e.Message().ID)
	}
	assert.ElementsMatch(t, sent, append(acked, handed...), "nothing was lost on the way")
}

// TestModelBus2PendingBeforeNew checks PendingBeforeNew (tla/Bus2.tla): a
// recv that hands out a new message found nothing pending for that recipient
// that a dead consumer held -- after a crash, what the dead consumer held
// comes first. It replays MCBus2BrokenNewFirst.cfg, the witness of a recv
// that reads new entries before it claims pending ones: a reader dies
// holding the first message, a second arrives, and the next recv hands out
// the dead reader's message, never the new one first.
func TestModelBus2PendingBeforeNew(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, f := rig(t, "ada", "bob")
	m1, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	// a reader takes m1 and dies holding it
	held, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, m1.ID, held.Message().ID)
	f.Advance(ClaimAfter) // the holder is dead once its entry is idle past ClaimAfter
	m2, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	e, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m1.ID, e.Message().ID, "the dead reader's message is handed out before any new one")
	e, ok, err = b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m2.ID, e.Message().ID, "and only then the new one")
}

// TestModelBus2HeldStaysHeld checks HeldStaysHeld (tla/Bus2.tla): a message
// held by a live consumer stays with it -- it is delivered once while held,
// and only an ack or the holder's death moves it. It replays
// MCBus2BrokenSteal.cfg, the witness of a recv that claims a pending entry
// whoever holds it and however briefly (XAUTOCLAIM with min-idle 0): a second
// reader takes a message a live one is delivering, so one message is
// delivered twice.
func TestModelBus2HeldStaysHeld(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, f := rig(t, "ada", "bob")
	m1, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	m2, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	first, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, m1.ID, first.Message().ID)
	// the holder is live while its entry is idle under ClaimAfter: a second
	// reader gets the next message, never m1
	second, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m2.ID, second.Message().ID, "a live holder keeps its message")
	_, ok, err = b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	assert.False(t, ok, "both messages are held, and nothing new is there")
	// only the holder's death moves it: idle past ClaimAfter, the claim
	// hands m1 to whichever reader asks
	f.Advance(ClaimAfter)
	again, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m1.ID, again.Message().ID, "the dead holder's message moves; a live one's never did")
}

// TestModelBus2AckOnlyDelivered checks AckOnlyDelivered (tla/Bus2.tla): only
// a delivered message is acked -- nothing goes from new to acked. It replays
// MCBus2BrokenAckUndelivered.cfg, the witness of an ack that takes any id on
// the stream, delivered or not: acking a message nobody was handed changes
// nothing, says so, and leaves the message deliverable.
func TestModelBus2AckOnlyDelivered(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, _ := rig(t, "ada", "bob")
	m1, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	m2, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	// nothing delivered yet: ack by id and by entry both say no
	got, err := b.Ack(ctx, "bob", []string{m1.ID, m2.ID})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{m1.ID: false, m2.ID: false}, got, "acking a message never delivered changes nothing")
	did, err := b.AckEntry(ctx, "bob", "0-0")
	require.NoError(t, err)
	assert.False(t, did, "acking an entry nobody holds says no")
	// delivery first, then the ack takes
	e, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, m1.ID, e.Message().ID)
	did, err = b.AckEntry(ctx, "bob", e.Entry)
	require.NoError(t, err)
	assert.True(t, did, "the delivered message is acked")
	// m2 is still new: acking it says no, and it stays deliverable
	got, err = b.Ack(ctx, "bob", []string{m2.ID})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{m2.ID: false}, got, "a new message is never acked")
	e2, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m2.ID, e2.Message().ID, "the new message is still deliverable: its ack changed nothing")
}

// TestModelBus2AckedStaysAcked checks AckedStaysAcked (tla/Bus2.tla): ack is
// idempotent -- once acked, acked, and the second ack changes nothing. It
// replays MCBus2BrokenAckReopens.cfg, the witness of an ack that deletes and
// re-adds: the second ack would put the message back on the stream as new,
// for a reader to be handed again.
func TestModelBus2AckedStaysAcked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, f := rig(t, "ada", "bob")
	m, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	e, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	did, err := b.AckEntry(ctx, "bob", e.Entry)
	require.NoError(t, err)
	require.True(t, did)
	// the second ack changes nothing and says so
	again, err := b.AckEntry(ctx, "bob", e.Entry)
	require.NoError(t, err)
	assert.False(t, again, "the second ack changes nothing")
	got, err := b.Ack(ctx, "bob", []string{m.ID})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{m.ID: false}, got, "ack by id after the entry ack is a no-op too")
	// the message never comes back: nothing pending, nothing new, one entry
	// on the stream, and no reader is handed it again
	pending, fresh, err := b.Peek(ctx, "bob")
	require.NoError(t, err)
	assert.Empty(t, pending, "the acked message is not pending again")
	assert.Empty(t, fresh, "the acked message is not new again")
	assert.Equal(t, 1, f.Len(StreamOf("bob")), "the stream keeps its one entry: the ack re-adds nothing")
	_, ok, err = b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	assert.False(t, ok, "a reader is never handed the acked message again")
}

// TestModelBus2EveryMessageIsAcked checks EveryMessageIsAcked (tla/Bus2.tla),
// the liveness: every sent message is acked by every recipient it names, once
// the crashes stop (they are bounded) and some consumer keeps reading and
// acking (Fairness). A reader crashes holding a message; a live one keeps
// reading and acking, and every recipient's log of messages ends acked.
func TestModelBus2EveryMessageIsAcked(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, f := rig(t, "ada", "bob", "cy")
	var ids []string
	for range 3 {
		m, err := b.Send(ctx, msg("ada", "bob", "cy"))
		require.NoError(t, err)
		ids = append(ids, m.ID)
	}
	// bob's reader takes the first message and crashes holding it
	_, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	f.Advance(ClaimAfter) // the crash: its entry goes idle past ClaimAfter
	drain := func(as string) []string {
		var acked []string
		for range len(ids) + 1 {
			e, ok, err := b.Recv(ctx, as, 0)
			require.NoError(t, err)
			if !ok {
				return acked
			}
			did, err := b.AckEntry(ctx, as, e.Entry)
			require.NoError(t, err)
			require.True(t, did, "what recv hands out, ack takes")
			acked = append(acked, e.Message().ID)
		}
		require.Fail(t, "the drain never ran dry", "recipient %s", as)
		return nil
	}
	// the claim hands the held message to the live reader, and reading and
	// acking goes on until nothing waits
	assert.ElementsMatch(t, ids, drain("bob"), "every message bob was sent ended acked")
	assert.ElementsMatch(t, ids, drain("cy"), "and every message cy was sent")
	pending, fresh, err := b.Peek(ctx, "bob")
	require.NoError(t, err)
	assert.Empty(t, pending, "nothing waits pending")
	assert.Empty(t, fresh, "nothing waits new")
	pending, fresh, err = b.Peek(ctx, "cy")
	require.NoError(t, err)
	assert.Empty(t, pending, "nothing waits pending")
	assert.Empty(t, fresh, "nothing waits new")
}

// TestModelBus2ReceiptsReceiptNeverMovesBack checks ReceiptNeverMovesBack
// (tla/Bus2Receipts.tla): a receipt never moves back, under any of the
// machine's actions. It replays MCBus2BrokenBackStamp.cfg, the witness of a
// recv that writes delivered over whatever the receipt held (HSET, not the
// rule): the message is read, its ack is lost so the claim hands it in again,
// and recv's delivered stamp leaves the receipt at read.
func TestModelBus2ReceiptsReceiptNeverMovesBack(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, f := rig(t, "rowan", "ada")
	f.Friends = []string{"ada"}
	m, err := b.Send(ctx, msg("rowan", "ada"))
	require.NoError(t, err)
	e, ok, err := b.Recv(ctx, "ada", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, m.ID, e.Message().ID)
	prior, err := b.Stamp(ctx, "ada", Read, m.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{Delivered}, prior, "recv had stamped delivered")
	// the ack is lost: the entry stays pending, and after ClaimAfter the
	// claim hands it in again (LoseAck); recv stamps delivered once more
	f.Advance(ClaimAfter)
	again, ok, err := b.Recv(ctx, "ada", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, m.ID, again.Message().ID, "the claim hands the read message in again")
	assert.Equal(t, Read, again.Stage, "recv found the receipt at read, and its delivered stamp moved nothing back")
	got, _, err := b.Stages(ctx, "ada", m.ID)
	require.NoError(t, err)
	assert.Equal(t, Read, got[0].State, "the receipt stays at read: delivered never overwrites it")
	// acted moves it forward, and no stamp after that moves it back
	prior, err = b.Stamp(ctx, "ada", Acted, m.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{Read}, prior)
	for _, back := range []string{Delivered, Read} {
		_, err := b.Stamp(ctx, "ada", back, m.ID)
		require.NoError(t, err)
	}
	got, _, err = b.Stages(ctx, "ada", m.ID)
	require.NoError(t, err)
	assert.Equal(t, Acted, got[0].State, "a receipt never moves back")
}

// TestModelBus2ReceiptsActedImpliesDelivered checks ActedImpliesDelivered
// (tla/Bus2Receipts.tla): a message with a receipt past none was taken off
// its recipient's stream (pending, or acked since) -- only delivered starts
// a receipt. No cfg witness turns this on alone (MCBus2Receipts.cfg holds it
// with Broken = "none"); the reversal is the rule Forward itself (stages.go):
// a read or acted stamp, or a reply's own stamp, on a message never
// delivered writes nothing.
func TestModelBus2ReceiptsActedImpliesDelivered(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, f := rig(t, "rowan", "ada")
	f.Friends = []string{"ada"}
	m, err := b.Send(ctx, msg("rowan", "ada"))
	require.NoError(t, err)
	// stamps on a message never delivered write nothing
	for _, s := range []string{Read, Acted} {
		prior, err := b.Stamp(ctx, "ada", s, m.ID)
		require.NoError(t, err)
		assert.Equal(t, []string{""}, prior, "%s on a message never delivered writes no receipt", s)
	}
	// a reply naming it (re) is its receipt only once delivered: the send's
	// own Forward mark moves nothing when there is nothing to move
	_, err = b.Send(ctx, Message{From: "ada", To: []string{"rowan"}, Subject: "re", Re: m.ID, Body: "seen"})
	require.NoError(t, err)
	got, _, err := b.Stages(ctx, "ada", m.ID)
	require.NoError(t, err)
	assert.Equal(t, "", got[0].State, "a reply to a message never delivered is no receipt of it")
	// delivered starts one, and the message is off the stream for its
	// reader: pending, gone from the fresh list
	e, ok, err := b.Recv(ctx, "ada", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, m.ID, e.Message().ID)
	got, _, err = b.Stages(ctx, "ada", m.ID)
	require.NoError(t, err)
	assert.Equal(t, Delivered, got[0].State)
	pending, fresh, err := b.Peek(ctx, "ada")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, m.ID, pending[0].Message().ID, "a message with a receipt was taken off its stream: pending")
	assert.Empty(t, fresh, "it is not new any more")
	// acked since: the receipt stands, and the message is in neither list
	did, err := b.AckEntry(ctx, "ada", e.Entry)
	require.NoError(t, err)
	require.True(t, did)
	pending, fresh, err = b.Peek(ctx, "ada")
	require.NoError(t, err)
	assert.Empty(t, pending, "the acked message is in no list")
	assert.Empty(t, fresh, "the acked message is in no list")
	got, _, err = b.Stages(ctx, "ada", m.ID)
	require.NoError(t, err)
	assert.Equal(t, Delivered, got[0].State, "the receipt stands after the ack")
}

// TestModelBus2ReceiptsNoIdActedTwice checks NoIdActedTwice
// (tla/Bus2Receipts.tla): no message id is acted twice -- no two turns
// carrying it end acted. It replays MCBus2BrokenPushDup.cfg, the witness of
// a take that pushes every message it is handed, acted or not: the turn ends
// acted, the ack is lost, the claim hands the message in again, and the
// redelivery says acted (Entry.Stage), so the take drops it instead of
// pushing a second turn.
func TestModelBus2ReceiptsNoIdActedTwice(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, f := rig(t, "rowan", "ada")
	f.Friends = []string{"ada"}
	m, err := b.Send(ctx, msg("rowan", "ada"))
	require.NoError(t, err)
	e, ok, err := b.Recv(ctx, "ada", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, m.ID, e.Message().ID)
	// the turn carrying it: read, then ended at exit 0 -- acted
	_, err = b.Stamp(ctx, "ada", Read, m.ID)
	require.NoError(t, err)
	prior, err := b.Stamp(ctx, "ada", Acted, m.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{Read}, prior)
	// the ack is lost (LoseAck): after ClaimAfter the claim hands it in
	// again, and the redelivery says acted
	f.Advance(ClaimAfter)
	again, ok, err := b.Recv(ctx, "ada", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, m.ID, again.Message().ID, "delivery is at least once: the claim hands it in again")
	assert.Equal(t, Acted, again.Stage, "the redelivery says acted: the take drops it, no second turn carries it")
	// acting again moves nothing: the receipt stands at acted
	prior, err = b.Stamp(ctx, "ada", Acted, m.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{Acted}, prior, "acting again answers acted and moves nothing")
	got, _, err := b.Stages(ctx, "ada", m.ID)
	require.NoError(t, err)
	assert.Equal(t, Acted, got[0].State)
}

// TestModelBusSendOnceOneMessageWithinLife checks OneMessageWithinLife
// (tla/BusSendOnce.tla): two messages under one token are a life apart at
// least -- inside the life a retry, however often, is the one message. It
// replays the module's reversed witnesses: "checkapart" (the record read in
// one step and the message written in another, so two racing retries both
// write), "newid" (a send that finds the record writes again with a new id)
// and "dropearly" (the record's key expires before the life ends).
func TestModelBusSendOnceOneMessageWithinLife(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, f := rig(t, "ada", "bob", "cy")
	b.TokenLife = time.Hour
	// the write commits, its response is lost
	f.Lose = errLost
	_, err := b.Send(ctx, tokened("t-1"))
	require.ErrorIs(t, err, errLost)
	one(t, f)
	log, err := b.Log(ctx, "-")
	require.NoError(t, err)
	require.Len(t, log, 1)
	orig := log[0].Message()
	// inside the life, however often: every retry is the one message
	for range 3 {
		f.Advance(10 * time.Minute)
		got, err := b.Send(ctx, tokened("t-1"))
		require.NoError(t, err)
		assert.Equal(t, orig.ID, got.ID, "a retry inside the life answers the original id, never a new one")
		one(t, f)
	}
	// racing retries: the record is read and the message written in one step,
	// so however many send at once, all answer the one message. The bus here
	// has no Rand of its own: crypto/rand is what a fresh process would use,
	// and a retry's answer is the record's, never the id it drew.
	racing := &Bus{Store: f, TokenLife: b.TokenLife}
	var wg sync.WaitGroup
	ids := make([]string, 4)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := racing.Send(ctx, tokened("t-1"))
			assert.NoError(t, err)
			ids[i] = m.ID
		}()
	}
	wg.Wait()
	for _, id := range ids {
		assert.Equal(t, orig.ID, id, "a racing retry answers the original id")
	}
	one(t, f)
	// the record outlives the life: at its edge the retry still answers the
	// original, never a second message
	f.Advance(orig.At.Add(b.TokenLife).Sub(f.now) - 5*time.Second)
	edge, err := b.Send(ctx, tokened("t-1"))
	require.NoError(t, err)
	assert.Equal(t, orig.ID, edge.ID, "the record is kept as long as a retry is honoured")
	one(t, f)
	// past the life the retry is refused: a second message inside the life
	// never happens
	f.Advance(time.Minute)
	_, err = b.Send(ctx, tokened("t-1"))
	var r *Refusal
	require.ErrorAs(t, err, &r)
	assert.Contains(t, err.Error(), "past its life", "the refusal says the message went")
	one(t, f)
}

// TestModelBusSendOnceAnswerIsTheOriginal checks AnswerIsTheOriginal
// (tla/BusSendOnce.tla): an answer names the message written under its token
// with its arguments -- a retry answers the original, and other arguments are
// never told they went. It replays the module's "nofingerprint" witness: the
// record answered whatever the arguments, so a changed body is told it was
// sent.
func TestModelBusSendOnceAnswerIsTheOriginal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, f := rig(t, "ada", "bob", "cy")
	first, err := b.Send(ctx, tokened("t-1"))
	require.NoError(t, err)
	// the retry answers the original: its id, its at, the arguments sent
	got, err := b.Send(ctx, tokened("t-1"))
	require.NoError(t, err)
	assert.Equal(t, first.ID, got.ID, "the retry answers the original message's id")
	assert.Equal(t, first.At, got.At, "and its at, not the retry's")
	log, err := b.Log(ctx, "-")
	require.NoError(t, err)
	require.Len(t, log, 1)
	assert.Equal(t, first.ID, log[0].Message().ID, "the answer names the message written under the token")
	assert.Equal(t, "the body\n", log[0].Message().Body, "the message written carries the arguments it was sent with")
	// other arguments are never told they went: the same token, a changed
	// body, is refused naming the message that went
	changed := tokened("t-1")
	changed.Body = "another body\n"
	_, err = b.Send(ctx, changed)
	var r *Refusal
	require.ErrorAs(t, err, &r)
	assert.Contains(t, err.Error(), `the token "t-1" already sent `+first.ID, "the refusal names the message that went")
	one(t, f)
}
