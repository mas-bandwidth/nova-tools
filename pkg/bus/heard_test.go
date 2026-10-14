package bus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var heardStart = time.Date(2026, 10, 13, 18, 0, 0, 0, time.UTC)

// marks is the two heard hashes of the fake store, read raw as the sprint does.
func marks(t *testing.T, s Store) (sent, recv map[string]string) {
	t.Helper()
	hs, err := s.Marks(context.Background(), HeardSendKey, HeardRecvKey)
	require.NoError(t, err)
	return hs[0], hs[1]
}

// A send marks its sender heard, in the send's own transaction and at the
// store's clock to the millisecond; a refused send, a send whose store is down
// and a retry under a token that finds its record mark nothing. The mark is
// overwritten, so the hash holds one field per name (tla/BusHeard.tla: Send).
func TestASendMarksTheSenderHeardInItsOwnTransaction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := NewFake(heardStart, "ada", "bob")
	b := &Bus{Store: f}

	_, err := b.Send(ctx, Message{From: "ada", To: []string{"zed"}, Subject: "s", Body: "x"})
	require.Error(t, err)
	sent, _ := marks(t, f)
	assert.Empty(t, sent, "a refused send marks nobody")

	m, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "s", Body: "x"})
	require.NoError(t, err)
	sent, recv := marks(t, f)
	assert.Equal(t, map[string]string{"ada": "2026-10-13T18:00:02.000Z"}, sent, "the store's time, to the millisecond (the second Roster/Members trip of the fake)")
	assert.Equal(t, 1, f.Len(LogKey))
	assert.Empty(t, recv, "a send marks the sender's sending and never its receiving (tla/BusHeard.tla: SendNeverMarksRecv)")
	assert.True(t, sent["ada"] >= HeardStamp(m.At), "the mark is not before the message's at")

	f.Advance(time.Hour)
	_, err = b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "s2", Body: "y"})
	require.NoError(t, err)
	sent, _ = marks(t, f)
	assert.Len(t, sent, 1, "overwritten, never added to")
	assert.Equal(t, "2026-10-13T19:00:03.000Z", sent["ada"])

	// a token retry that finds its record writes nothing, the mark with it
	tok := Message{From: "bob", To: []string{"ada"}, Subject: "s3", Body: "z", Token: "t1"}
	_, err = b.Send(ctx, tok)
	require.NoError(t, err)
	sent, _ = marks(t, f)
	first := sent["bob"]
	require.NotEmpty(t, first, "a send under a token marks the sender in the same step")
	f.Advance(time.Minute)
	_, err = b.Send(ctx, tok)
	require.NoError(t, err)
	sent, _ = marks(t, f)
	assert.Equal(t, first, sent["bob"], "the retry wrote nothing")

	f.Fail = errors.New("down")
	_, err = b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "s4", Body: "w"})
	require.Error(t, err)
	f.Fail = nil
	sent, _ = marks(t, f)
	assert.Equal(t, "2026-10-13T19:00:03.000Z", sent["ada"], "a send that failed marks nothing")
}

// A recv marks its reader at the store's clock whether or not a message waits,
// and so does an ack by id; neither marks the sender side; a recv of a name
// the roster does not hold marks nothing (tla/BusHeard.tla: Recv, Ack).
func TestARecvAndAnAckMarkTheReaderHeard(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := NewFake(heardStart, "ada", "bob")
	b := &Bus{Store: f}

	_, _, err := b.Recv(ctx, "zed", 0)
	require.Error(t, err)
	_, recv := marks(t, f)
	assert.Empty(t, recv, "an unknown name marks nothing")

	_, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.False(t, ok)
	sent, recv := marks(t, f)
	assert.Equal(t, map[string]string{"bob": "2026-10-13T18:00:02.000Z"}, recv, "an empty recv is a reader that is there")
	assert.Empty(t, sent)

	m, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "s", Body: "x"})
	require.NoError(t, err)
	f.Advance(5 * time.Minute)
	e, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m.ID, e.Message().ID)
	_, recv = marks(t, f)
	assert.Len(t, recv, 1)
	assert.Equal(t, "2026-10-13T18:05:04.000Z", recv["bob"])

	f.Advance(5 * time.Minute)
	acked, err := b.Ack(ctx, "bob", []string{m.ID})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{m.ID: true}, acked)
	_, recv = marks(t, f)
	assert.Equal(t, "2026-10-13T18:10:", recv["bob"][:17], "the ack marked it again, ten minutes on")
	assert.Equal(t, ".000Z", recv["bob"][19:])

	// ack of nothing pending is still the session reading
	f.Advance(time.Minute)
	_, err = b.Ack(ctx, "ada", []string{"NOPE"})
	require.NoError(t, err)
	_, recv = marks(t, f)
	assert.Contains(t, recv, "ada")
}

// A mark the store does not take never fails the verb: a recv still reads and
// an ack still acks, the failure going to OnStampError; the delivery is whole
// without it (tla/BusHeard.tla: the marks are outside the delivery machine).
func TestAHeardMarkThatFailsNeverFailsTheVerb(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := NewFake(heardStart, "ada", "bob")
	var told []error
	b := &Bus{Store: noMarks{f}, OnStampError: func(err error) { told = append(told, err) }}
	m, err := (&Bus{Store: f}).Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "s", Body: "x"})
	require.NoError(t, err)

	e, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err, "the recv reads")
	require.True(t, ok)
	assert.Equal(t, m.ID, e.Message().ID)
	acked, err := b.Ack(ctx, "bob", []string{m.ID})
	require.NoError(t, err, "the ack acks")
	assert.Equal(t, map[string]bool{m.ID: true}, acked)
	require.Len(t, told, 2)
	assert.ErrorContains(t, told[0], "heard mark of bob not written")
	_, recv := marks(t, f)
	assert.Empty(t, recv)
}

// noMarks is a store that takes no write without a stream, the heard marks'
// shape, and every other command as the wrapped store does.
type noMarks struct{ *Fake }

func (n noMarks) AddAll(ctx context.Context, streams []string, fields map[string]string, marks ...Mark) error {
	if len(streams) == 0 {
		return errors.New("marks refused")
	}
	return n.Fake.AddAll(ctx, streams, fields, marks...)
}

// Heard is a proof proven, or a recv or ack under PushFresh ago that the
// friend daemon's fresh word that the session is down does not outweigh; a
// send, an old recv and a stale proof are not. Deaf is a name that sent
// lately and is not heard.
func TestAStandingIsHeardByAProofOrByReceivingLately(t *testing.T) {
	t.Parallel()
	at := heardStart
	up := PushProof{Name: "bob", Harness: "claude", Up: true, At: at}
	down := PushProof{Name: "bob", Harness: "claude", Reason: "no session answer", At: at}
	for _, c := range []struct {
		name  string
		s     Standing
		after time.Duration
		heard bool
		deaf  bool
	}{
		{"nothing at all", Standing{Proof: PushProof{Name: "bob"}}, time.Hour, false, false},
		{"a proof proven", Standing{Proof: up}, 0, true, false},
		{"a proof gone stale", Standing{Proof: up}, PushFresh, false, false},
		{"a recv just now", Standing{Proof: PushProof{Name: "bob"}, Received: at}, 0, true, false},
		{"a recv just under PushFresh", Standing{Proof: PushProof{Name: "bob"}, Received: at}, PushFresh - time.Second, true, false},
		{"a recv at PushFresh", Standing{Proof: PushProof{Name: "bob"}, Received: at}, PushFresh, false, false},
		{"a recv the store's clock stepped back from", Standing{Proof: PushProof{Name: "bob"}, Received: at.Add(time.Minute)}, 0, true, false},
		{"a send alone is not hearing", Standing{Proof: PushProof{Name: "bob"}, Sent: at}, time.Minute, false, true},
		{"a send and an old recv is deaf", Standing{Proof: PushProof{Name: "bob"}, Sent: at.Add(PushFresh), Received: at}, PushFresh, false, true},
		{"a fresh down proof outweighs a recv (the daemon reads for a session that is not there)", Standing{Proof: down, Received: at}, time.Minute, false, false},
		{"a down proof gone stale no longer does", Standing{Proof: down, Received: at.Add(PushFresh)}, PushFresh, true, false},
		{"a proven proof is heard whatever the recv", Standing{Proof: up, Sent: at}, time.Minute, true, false},
	} {
		now := at.Add(c.after)
		assert.Equal(t, c.heard, c.s.Heard(now), c.name)
		assert.Equal(t, c.deaf, c.s.Deaf(now), c.name)
		assert.Equal(t, c.heard, c.s.Unheard(now) == "", c.name)
		assert.Equal(t, c.heard, c.s.DeafLine(now) == "", c.name)
	}
}

// The NOTE says when the name last received and sent, that the message is
// queued and arrives when the receiver runs, and the re-arm steps as commands
// that exist; it reads as no failure and names no push state.
func TestTheUnheardNoteSaysWhenAndHowToReArm(t *testing.T) {
	t.Parallel()
	at := heardStart
	s := Standing{Proof: PushProof{Name: "cy"}, Received: at, Sent: at.Add(12 * time.Minute)}
	now := at.Add(14 * time.Minute)
	line := s.Unheard(now)
	assert.Contains(t, line, "cy last received 14m0s ago (last sent 2m0s ago): the message is queued on cy's stream and arrives when its receiver runs")
	assert.Contains(t, line, "nova-hook status --as cy")
	assert.Contains(t, line, "nova-hook hook --as cy --role <comms|friend> under the session's Monitor")
	assert.Contains(t, line, "Monitor nova-bus recv --as cy --forever --exec '<deliver-into-session>'")
	assert.NotContains(t, line, "push=")
	assert.NotContains(t, line, "no proven push")

	never := Standing{Proof: PushProof{Name: "m1"}}.Unheard(now)
	assert.Contains(t, never, "m1 has never received on the bus (never sent): the message is queued")

	downed := Standing{Proof: PushProof{Name: "cy", Up: false, Reason: "no session answer", At: now}, Received: at}.Unheard(now)
	assert.Contains(t, downed, "and its friend daemon says the session did not answer its SESSION CHECK (no session answer)")

	refusal := s.DeafLine(now)
	assert.Contains(t, refusal, "deaf: cy last received 14m0s ago (last sent 2m0s ago)")
	assert.Contains(t, refusal, "the remedy:")
	assert.Contains(t, refusal, "nova-hook hook --as cy --role <comms|friend>")
}

// Silent is said to a receiver that was not running (its last recv is past
// PushFresh), never to one that received lately or never has; HeardReading,
// the gate of a gated recv, lets a name that has never received through (the
// recv is the receive) and refuses only a fresh down word of its daemon.
func TestAReceiverIsToldItWasSilentAndTheReadingGateNeverDeadlocks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := NewFake(heardStart, "ada", "bob")
	b := &Bus{Store: f}

	line, err := b.Silent(ctx, "bob")
	require.NoError(t, err)
	assert.Empty(t, line, "a first recv has nothing to say")
	require.NoError(t, b.HeardReading(ctx, "bob"), "a name that never received is let through by the recv that is its first")

	_, _, err = b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	line, err = b.Silent(ctx, "bob")
	require.NoError(t, err)
	assert.Empty(t, line, "received a moment ago")

	f.Advance(PushFresh)
	line, err = b.Silent(ctx, "bob")
	require.NoError(t, err)
	assert.Contains(t, line, "bob's receiver was not running: it last received 10m")
	assert.Contains(t, line, "Monitor nova-bus recv --as bob --forever --exec '<deliver-into-session>'")

	_, err = b.ProvePush(ctx, PushProof{Name: "bob", Harness: "claude", Reason: "no session answer"})
	require.NoError(t, err)
	err = b.HeardReading(ctx, "bob")
	var r *Refusal
	require.ErrorAs(t, err, &r)
	assert.Contains(t, r.Error(), "deaf: bob")
	assert.Contains(t, r.Error(), "its friend daemon says the session did not answer")
}

// Standings reads the proof and both marks of every name in the order asked,
// in one pipeline, a value that is no instant as never.
func TestStandingsReadTheProofAndBothMarks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := NewFake(heardStart, "ada", "bob", "cy")
	b := &Bus{Store: f}
	_, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "s", Body: "x"})
	require.NoError(t, err)
	_, _, err = b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.NoError(t, f.AddAll(ctx, nil, nil, Mark{Key: HeardRecvKey, Field: "cy", Value: "garbage"}))

	ss, now, err := b.Standings(ctx, "bob", "ada", "cy")
	require.NoError(t, err)
	require.Len(t, ss, 3)
	assert.Equal(t, "bob", ss[0].Name())
	assert.False(t, ss[0].Received.IsZero())
	assert.True(t, ss[0].Sent.IsZero())
	assert.False(t, ss[1].Sent.IsZero())
	assert.True(t, ss[1].Received.IsZero())
	assert.True(t, ss[2].Received.IsZero(), "a value that is no instant is never")
	assert.Equal(t, "never", ss[2].RecvWord(now))
	assert.Equal(t, "1s", ss[0].RecvWord(now))
}
