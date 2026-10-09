package bus

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errLost is the answer of a write that committed and whose response never
// came back: a connection cut after EXEC, a deadline that ran out on the way
// home.
var errLost = errors.New("read tcp 100.76.0.9:6379: i/o timeout")

// tokened is a message to bob and cy with cc ada, sent under the token.
func tokened(token string) Message {
	return Message{From: "ada", To: []string{"cy", "bob"}, CC: []string{"ada"}, Subject: "s", Body: "the body\n", Token: token}
}

// one says the message is on every stream it names exactly once.
func one(t *testing.T, f *Fake) {
	t.Helper()
	for _, s := range []string{StreamOf("bob"), StreamOf("cy"), StreamOf("ada"), LogKey} {
		assert.Equal(t, 1, f.Len(s), "one logical message on %s", s)
	}
}

// The acceptance of the card: the write commits, its response is lost, and
// the caller retries the same operation; there is one logical message per
// recipient and the retry answers the original message (id and at), which is
// the receipt the first call never got to print.
func TestARetriedSendUnderTheSameTokenMakesOneMessage(t *testing.T) {
	t.Parallel()
	b, f := rig(t, "ada", "bob", "cy")
	ctx := context.Background()

	f.Lose = errLost
	_, err := b.Send(ctx, tokened("t-1"))
	require.ErrorIs(t, err, errLost, "the caller sees the lost response, not success")
	one(t, f)

	f.Advance(time.Minute)
	got, err := b.Send(ctx, tokened("t-1"))
	require.NoError(t, err)
	one(t, f)
	log, err := b.Log(ctx, "-")
	require.NoError(t, err)
	require.Len(t, log, 1)
	orig := log[0].Message()
	assert.Equal(t, orig.ID, got.ID, "the retry answers the original id")
	assert.Equal(t, orig.At, got.At, "and the original at, not the retry's")
	assert.Equal(t, []string{"bob", "cy"}, got.To)

	again, err := b.Send(ctx, tokened("t-1"))
	require.NoError(t, err)
	assert.Equal(t, got, again, "every retry answers the same receipt")
	one(t, f)
}

func TestTheSameTokenWithOtherArgumentsIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	changes := map[string]func(*Message){
		"body":      func(m *Message) { m.Body = "another body\n" },
		"subject":   func(m *Message) { m.Subject = "other" },
		"recipient": func(m *Message) { m.To = []string{"bob"} },
		"cc":        func(m *Message) { m.CC = nil },
		"re":        func(m *Message) { m.Re = "01ANSWERS" },
		"kind":      func(m *Message) { m.Kind = KindReport },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b, f := rig(t, "ada", "bob", "cy")
			first, err := b.Send(ctx, tokened("t-1"))
			require.NoError(t, err)
			m := tokened("t-1")
			change(&m)
			_, err = b.Send(ctx, m)
			var r *Refusal
			require.ErrorAs(t, err, &r)
			assert.Contains(t, err.Error(), `the token "t-1" already sent `+first.ID)
			assert.Contains(t, err.Error(), "a new message wants a new token")
			one(t, f)
		})
	}
	t.Run("the same arguments in another order are the same send", func(t *testing.T) {
		t.Parallel()
		b, f := rig(t, "ada", "bob", "cy")
		first, err := b.Send(ctx, tokened("t-1"))
		require.NoError(t, err)
		m := tokened("t-1")
		m.To = []string{"bob", "cy", "bob"}
		m.Kind = KindStatus // the default spelled out
		got, err := b.Send(ctx, m)
		require.NoError(t, err)
		assert.Equal(t, first.ID, got.ID)
		one(t, f)
	})
}

// A token belongs to its sender: another sender's same word is its own send.
func TestATokenIsTheSenders(t *testing.T) {
	t.Parallel()
	b, f := rig(t, "ada", "bob", "cy")
	ctx := context.Background()
	a, err := b.Send(ctx, Message{From: "ada", To: []string{"cy"}, Subject: "s", Body: "x", Token: "t-1"})
	require.NoError(t, err)
	c, err := b.Send(ctx, Message{From: "bob", To: []string{"cy"}, Subject: "s", Body: "x", Token: "t-1"})
	require.NoError(t, err)
	assert.NotEqual(t, a.ID, c.ID)
	assert.Equal(t, 2, f.Len(StreamOf("cy")))
}

// The token is in the store, never in the process: a sender that died after
// its write committed and starts again (a new Bus, its own random source)
// retries and gets the original.
func TestARetryAfterAProcessRestartMakesOneMessage(t *testing.T) {
	t.Parallel()
	b, f := rig(t, "ada", "bob", "cy")
	ctx := context.Background()
	f.Lose = errLost
	_, err := b.Send(ctx, tokened("t-restart"))
	require.ErrorIs(t, err, errLost)

	reborn := &Bus{Store: f} // crypto/rand: a fresh process would make another id
	got, err := reborn.Send(ctx, tokened("t-restart"))
	require.NoError(t, err)
	log, err := reborn.Log(ctx, "-")
	require.NoError(t, err)
	require.Len(t, log, 1)
	assert.Equal(t, log[0].Message().ID, got.ID)
	one(t, f)
}

// A receipt interrupted: the send's answer is lost, and before the sender
// retries, the recipient's session has read the message and given its
// receipt. The retry writes nothing, so the message is not owed again, and
// it answers the receipt the first call should have printed.
func TestARetryAfterTheRecipientsReceiptOwesNothingAgain(t *testing.T) {
	t.Parallel()
	b, f := rig(t, "ada", "bob", "cy")
	f.Friends = []string{"ada", "bob", "cy"}
	ctx := context.Background()
	f.Lose = errLost
	_, err := b.Send(ctx, tokened("t-receipt"))
	require.ErrorIs(t, err, errLost)

	log, err := b.Log(ctx, "-")
	require.NoError(t, err)
	require.Len(t, log, 1)
	id := log[0].Message().ID
	owed, err := b.Undelivered(ctx, "bob", "cy")
	require.NoError(t, err)
	assert.Equal(t, 1, owed[0].Count, "bob owes the receipt of the committed message")
	n, err := b.Receipt(ctx, "bob", []string{id})
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	got, err := b.Send(ctx, tokened("t-receipt"))
	require.NoError(t, err)
	assert.Equal(t, id, got.ID)
	owed, err = b.Undelivered(ctx, "bob", "cy")
	require.NoError(t, err)
	assert.Equal(t, 0, owed[0].Count, "a retry never marks a read message owed again")
	assert.Equal(t, 1, owed[1].Count, "cy still owes hers, once")
	one(t, f)
}

// A store that failed before the write committed answers the same error,
// and the retry is the first send.
func TestARetryAfterAFailureBeforeTheCommitSendsOnce(t *testing.T) {
	t.Parallel()
	b, f := rig(t, "ada", "bob", "cy")
	ctx := context.Background()
	f.Fail = errLost
	_, err := b.Send(ctx, tokened("t-1"))
	require.ErrorIs(t, err, errLost)
	assert.Equal(t, 0, f.Len(LogKey))
	f.Fail = nil
	got, err := b.Send(ctx, tokened("t-1"))
	require.NoError(t, err)
	assert.Len(t, got.ID, 26)
	one(t, f)
}

// Two retries racing (a sender that timed out and a watchdog that resends)
// make one message and the same answer.
func TestRacingRetriesMakeOneMessage(t *testing.T) {
	t.Parallel()
	b, f := rig(t, "ada", "bob", "cy")
	ctx := context.Background()
	var wg sync.WaitGroup
	ids := make([]string, 8)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := b.Send(ctx, tokened("t-race"))
			assert.NoError(t, err)
			ids[i] = m.ID
		}()
	}
	wg.Wait()
	for _, id := range ids {
		assert.Equal(t, ids[0], id)
	}
	one(t, f)
}

func TestTheTokenLifeAndItsCleanupHaveDefaults(t *testing.T) {
	t.Parallel()
	var b Bus
	assert.Equal(t, DefaultTokenLife, b.tokenLife())
	assert.Equal(t, DefaultTokenCleanup, b.tokenCleanup())
	assert.Equal(t, 24*time.Hour, DefaultTokenLife)
	assert.Equal(t, 7*24*time.Hour, DefaultTokenCleanup)
	b = Bus{TokenLife: time.Hour, TokenCleanup: time.Minute}
	assert.Equal(t, time.Hour, b.tokenCleanup(), "a cleanup before the life ends is the life: a token is never dropped while a retry is honoured")
}

// Within its life a token's retry answers the original; past its life and
// before its cleanup a retry is refused, never sent again (the sender is
// told the message went, and when); after its cleanup the store has dropped
// the record, and the token is new.
func TestATokenLivesThenIsRefusedThenIsCleanedUp(t *testing.T) {
	t.Parallel()
	b, f := rig(t, "ada", "bob", "cy")
	b.TokenLife, b.TokenCleanup = time.Hour, 3*time.Hour
	ctx := context.Background()
	first, err := b.Send(ctx, tokened("t-1"))
	require.NoError(t, err)

	f.Advance(time.Hour - 2*time.Second) // the next trip adds a second
	got, err := b.Send(ctx, tokened("t-1"))
	require.NoError(t, err)
	assert.Equal(t, first.ID, got.ID, "inside its life")

	f.Advance(time.Minute)
	_, err = b.Send(ctx, tokened("t-1"))
	var r *Refusal
	require.ErrorAs(t, err, &r)
	assert.Contains(t, err.Error(), `the token "t-1" sent `+first.ID+" at 2026-10-03T12:00:01Z, past its life of 1h0m0s")
	one(t, f)

	f.Advance(2 * time.Hour)
	again, err := b.Send(ctx, tokened("t-1"))
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, again.ID, "after the cleanup the token is new")
	assert.Equal(t, 2, f.Len(LogKey))
	assert.Equal(t, 1, f.Records(), "the first record was dropped when its cleanup came, as Redis expires the key; the new send holds its own")
	f.Advance(3 * time.Hour)
	assert.Equal(t, 0, f.Records(), "and that one goes at its cleanup too")
}

func TestATokenIsChecked(t *testing.T) {
	t.Parallel()
	b, f := rig(t, "ada", "bob", "cy")
	for _, tok := range []string{"has space", "x/y", string(make([]byte, MaxToken+1))} {
		_, err := b.Send(context.Background(), tokened(tok))
		var r *Refusal
		require.ErrorAs(t, err, &r, tok)
		assert.Contains(t, err.Error(), "the token")
	}
	assert.Equal(t, 0, f.Len(LogKey))
}

// No token is the send as before: every call is a new message, and a lost
// response retried is a second one. The token is the caller's to give.
func TestNoTokenIsANewMessageEveryCall(t *testing.T) {
	t.Parallel()
	b, f := rig(t, "ada", "bob", "cy")
	ctx := context.Background()
	f.Lose = errLost
	_, err := b.Send(ctx, tokened(""))
	require.ErrorIs(t, err, errLost)
	_, err = b.Send(ctx, tokened(""))
	require.NoError(t, err)
	assert.Equal(t, 2, f.Len(LogKey))
	assert.Equal(t, 0, f.Records())
}

// A retry of a message whose recipient went unheard since it was sent still
// answers the original, nothing written; and a first send to an unheard name
// lands too, the proof being advice and never a gate.
func TestARetryToANameThatWentDeafAnswersTheOriginal(t *testing.T) {
	t.Parallel()
	b, f := rig(t, "ada", "bob", "cy")
	ctx := context.Background()
	prove := func(up bool, names ...string) {
		_, now, err := f.Roster(ctx)
		require.NoError(t, err)
		for _, n := range names {
			_, err := b.ProvePush(ctx, PushProof{Name: n, Harness: "claude", Nonce: "n", Proven: now, Up: up, At: now})
			require.NoError(t, err)
		}
	}
	prove(true, "ada", "bob", "cy")
	f.Lose = errLost
	_, err := b.Send(ctx, tokened("t-deaf"))
	require.ErrorIs(t, err, errLost)
	prove(false, "bob")

	got, err := b.Send(ctx, tokened("t-deaf"))
	require.NoError(t, err)
	log, err := b.Log(ctx, "-")
	require.NoError(t, err)
	assert.Equal(t, log[0].Message().ID, got.ID)
	one(t, f)

	_, err = b.Send(ctx, tokened("t-new"))
	require.NoError(t, err, "a first send to an unheard name lands")
	assert.Equal(t, 2, f.Len(LogKey))
	lines, err := b.Unheard(ctx, "ada", "bob")
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "push=down for bob")
}
