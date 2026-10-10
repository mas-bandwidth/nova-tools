//go:build functional

package bus

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/testredis"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// live is a bus over a throwaway redis-server whose roster names ada and bob,
// the shape pkg/bus's fake imitates; every rule the unit tests pin on
// the fake runs here once against the real commands.
func live(t *testing.T) (*Bus, *redis.Client, context.Context) {
	t.Helper()
	addr := testredis.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	require.NoError(t, c.SAdd(ctx, friendsKey, "ada", "bob").Err())
	require.NoError(t, c.SAdd(ctx, machinesKey, "m1").Err())
	return &Bus{Store: Redis{C: c}}, c, ctx
}

func TestRedisStoreRunsTheWholeLoop(t *testing.T) {
	t.Parallel()
	b, c, ctx := live(t)

	names, err := b.Names(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"ada", "bob", "m1"}, names)

	m1, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, CC: []string{"m1"}, Subject: "one", Body: "first\n"})
	require.NoError(t, err)
	m2, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "two", Body: "second\n", Re: m1.ID})
	require.NoError(t, err)
	assert.Less(t, m1.ID, m2.ID)
	assert.WithinDuration(t, time.Now(), m1.At, time.Minute, "at is the server's time")
	for _, k := range []string{StreamOf("bob"), StreamOf("m1"), LogKey} {
		n, err := c.XLen(ctx, k).Result()
		require.NoError(t, err)
		assert.EqualValues(t, map[string]int64{StreamOf("bob"): 2, StreamOf("m1"): 1, LogKey: 2}[k], n, k)
	}

	// peek before any group exists: everything new
	pending, fresh, err := b.Peek(ctx, "bob")
	require.NoError(t, err)
	assert.Empty(t, pending)
	assert.Len(t, fresh, 2)

	e, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m1.ID, e.Message().ID)
	assert.Equal(t, "first\n", e.Message().Body)

	// a second reader at once: m1 is held (idle under ClaimAfter), so m2 comes
	e2, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m2.ID, e2.Message().ID, "a held message is not handed out twice (Bus2.tla HeldStaysHeld)")

	pending, fresh, err = b.Peek(ctx, "bob")
	require.NoError(t, err)
	assert.Len(t, pending, 2)
	assert.Empty(t, fresh)

	_, ok, err = b.Recv(ctx, "bob", 100*time.Millisecond)
	require.NoError(t, err)
	assert.False(t, ok, "a block that runs out is no error, and nothing held is handed out")

	acked, err := b.Ack(ctx, "bob", []string{m1.ID, "NOPE"})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{m1.ID: true, "NOPE": false}, acked)
	acked, err = b.Ack(ctx, "bob", []string{m1.ID})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{m1.ID: false}, acked, "idempotent")
	done, err := b.AckEntry(ctx, "bob", e2.Entry)
	require.NoError(t, err)
	assert.True(t, done)

	// the reader of a message died: the store's idle clock says so, and the
	// store's own command hands it out again (the idle is set by hand, as
	// XAUTOCLAIM reads it, so no test waits a minute)
	m3, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "three", Body: "third\n"})
	require.NoError(t, err)
	e3, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m3.ID, e3.Message().ID)
	require.NoError(t, c.XClaim(ctx, &redis.XClaimArgs{Stream: StreamOf("bob"), Group: "bob", Consumer: Consumer, MinIdle: 0, Messages: []string{e3.Entry}}).Err())
	require.NoError(t, c.Do(ctx, "XCLAIM", StreamOf("bob"), "bob", Consumer, "0", e3.Entry, "IDLE", (ClaimAfter+time.Second).Milliseconds()).Err())
	e4, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m3.ID, e4.Message().ID, "a message its reader lost comes back after ClaimAfter (Bus2.tla PendingBeforeNew)")

	got, err := b.Log(ctx, "-")
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, m1.ID, got[0].Message().ID)
	assert.Equal(t, []string{"m1"}, got[0].Message().CC)
	assert.Equal(t, m1.ID, got[1].Message().Re)

	// nothing was deleted
	for _, k := range []string{StreamOf("bob"), StreamOf("m1"), LogKey} {
		n, err := c.XLen(ctx, k).Result()
		require.NoError(t, err)
		assert.NotZero(t, n, k)
	}
}

func TestRedisStoreRefusesWhatTheFakeRefuses(t *testing.T) {
	t.Parallel()
	b, c, ctx := live(t)
	_, err := b.Send(ctx, Message{From: "ada", To: []string{"zed"}, Subject: "s", Body: "x"})
	var r *Refusal
	require.ErrorAs(t, err, &r)
	assert.Contains(t, err.Error(), "zed is no known name")
	pending, fresh, err := b.Peek(ctx, "nobody")
	require.NoError(t, err)
	assert.Empty(t, pending)
	assert.Empty(t, fresh)
	_, _, err = b.Recv(ctx, "nobody", 0)
	require.ErrorAs(t, err, &r)
	n, err := c.Exists(ctx, StreamOf("nobody")).Result()
	require.NoError(t, err)
	assert.Zero(t, n, "recv of an unknown name makes no stream")
	acked, err := b.Ack(ctx, "nobody", []string{"X"})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{"X": false}, acked)
}

// The push proof against the real commands: a proof is one HSET in a
// transaction with no stream, read back by HGETALL; Unheard names the
// unproven, and a send and a recv go through whoever is unheard.
func TestRedisStoreKeepsThePushProof(t *testing.T) {
	t.Parallel()
	b, c, ctx := live(t)
	m := Message{From: "ada", To: []string{"bob"}, Subject: "s", Body: "x"}

	lines, err := b.Unheard(ctx, "ada", "bob")
	require.NoError(t, err)
	require.Len(t, lines, 2)
	assert.Contains(t, lines[0], "push=none for ada: no proven push since never")
	first, err := b.Send(ctx, m)
	require.NoError(t, err, "unheard is advice, not a gate")
	e, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, first.ID, e.Message().ID)
	n, err := c.Exists(ctx, LogKey, StreamOf("bob")).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)

	for _, name := range []string{"ada", "bob"} {
		p, err := b.ProvePush(ctx, PushProof{Name: name, Harness: "claude", Nonce: "n-" + name, Up: true})
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now(), p.At, time.Minute, "at is the server's time")
	}
	got, now, err := b.PushProofs(ctx, "ada", "bob", "m1")
	require.NoError(t, err)
	assert.Equal(t, []string{PushProven, PushProven, PushNone}, []string{got[0].State(now), got[1].State(now), got[2].State(now)})
	assert.Equal(t, "n-bob", got[1].Nonce)

	lines, err = b.Unheard(ctx, "ada", "bob", "m1")
	require.NoError(t, err)
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "push=none for m1")
}

// Enroll on the real commands: a friend row the roster misses is added to the
// set `friends`, a name already there (a friend or a machine) is not, and a
// send to the name added then goes through (names.go).
func TestRedisEnrollMakesAFriendRowAKnownName(t *testing.T) {
	t.Parallel()
	b, c, ctx := live(t)
	_, err := b.Send(ctx, Message{From: "ada", To: []string{"bud-a"}, Subject: "s", Body: "x"})
	require.ErrorContains(t, err, "bud-a is no known name")
	added, err := b.Enroll(ctx, "bud-a", "ada", "m1")
	require.NoError(t, err)
	assert.Equal(t, []string{"bud-a"}, added)
	friends, err := c.SMembers(ctx, friendsKey).Result()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"ada", "bob", "bud-a"}, friends)
	_, err = b.Send(ctx, Message{From: "ada", To: []string{"bud-a"}, Subject: "s", Body: "x"})
	require.NoError(t, err)
}

// The send-once script on the real server: the record and the message in one
// step, a retry that finds the record writing nothing and answering the
// original, the same token with another body refused, the record's expiry
// set to the cleanup, and the receipt marks made as AddAll makes them.
func TestRedisStoreSendsOnceUnderAToken(t *testing.T) {
	t.Parallel()
	b, c, ctx := live(t)
	b.TokenCleanup = 48 * time.Hour
	m := Message{From: "ada", To: []string{"bob"}, CC: []string{"m1"}, Subject: "once", Body: "one body\n", Token: "t-live"}
	first, err := b.Send(ctx, m)
	require.NoError(t, err)

	reborn := &Bus{Store: Redis{C: c}} // a process started again
	got, err := reborn.Send(ctx, m)
	require.NoError(t, err)
	assert.Equal(t, first.ID, got.ID)
	assert.Equal(t, first.At, got.At)
	for _, k := range []string{StreamOf("bob"), StreamOf("m1"), LogKey} {
		n, err := c.XLen(ctx, k).Result()
		require.NoError(t, err)
		assert.EqualValues(t, 1, n, k)
	}
	log, err := b.Log(ctx, "-")
	require.NoError(t, err)
	require.Len(t, log, 1)
	assert.Equal(t, Message{ID: first.ID, From: "ada", To: []string{"bob"}, CC: []string{"m1"}, Subject: "once", Kind: KindStatus, At: first.At, Body: "one body\n"}, log[0].Message())
	owed, err := c.HGetAll(ctx, OwedOf("bob")).Result()
	require.NoError(t, err)
	assert.Equal(t, map[string]string{first.ID: first.At.Format(time.RFC3339)}, owed, "bob is a friend: the script marked the receipt owed")

	ttl, err := c.PTTL(ctx, SentOf("ada", "t-live")).Result()
	require.NoError(t, err)
	assert.InDelta(t, (48 * time.Hour).Seconds(), ttl.Seconds(), 60, "the record expires at the cleanup")

	m.Body = "another body\n"
	_, err = b.Send(ctx, m)
	var r *Refusal
	require.ErrorAs(t, err, &r)
	assert.Contains(t, err.Error(), "already sent "+first.ID)
	n, err := c.XLen(ctx, LogKey).Result()
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)

	// a reply under a token clears the sender's own receipt in the script, as AddAll does
	_, err = b.Send(ctx, Message{From: "bob", To: []string{"ada"}, Subject: "re", Body: "got it\n", Re: first.ID, Token: "t-reply"})
	require.NoError(t, err)
	owed, err = c.HGetAll(ctx, OwedOf("bob")).Result()
	require.NoError(t, err)
	assert.Empty(t, owed)
}

// The friend's ACL line from SPEC-BUS.md on the real server, as ada: a send
// under a token goes and its retry answers the original, while SET and GET on
// the roster, the log, a stream and another sender's record are refused. The
// reversed witness is the same line with +get +set on the root, which lets
// SET friends through.
func TestRedisFriendACLSetsOnlyItsOwnTokenRecords(t *testing.T) {
	t.Parallel()
	_, admin, ctx := live(t)
	line := strings.ReplaceAll(friendACL(t), "<f>", "ada")
	apply := func(user, line string) *redis.Client {
		args := []any{"ACL", "SETUSER", user}
		for _, w := range strings.Fields(strings.TrimPrefix(line, "ACL SETUSER ada ")) {
			if w == ">(password)" {
				w = ">pw"
			}
			args = append(args, w)
		}
		// a selector is one argument to ACL SETUSER
		var joined []any
		for i := 0; i < len(args); i++ {
			w, _ := args[i].(string)
			if strings.HasPrefix(w, "(") {
				sel := w
				for !strings.HasSuffix(sel, ")") {
					i++
					sel += " " + args[i].(string)
				}
				joined = append(joined, sel)
				continue
			}
			joined = append(joined, args[i])
		}
		require.NoError(t, admin.Do(ctx, joined...).Err())
		c := redis.NewClient(&redis.Options{Addr: admin.Options().Addr, Username: user, Password: "pw"})
		t.Cleanup(func() { _ = c.Close() })
		return c
	}

	ada := apply("ada", line)
	b := &Bus{Store: Redis{C: ada}}
	m := Message{From: "ada", To: []string{"bob"}, Subject: "acl", Body: "x\n", Token: "t-acl"}
	first, err := b.Send(ctx, m)
	require.NoError(t, err)
	again, err := b.Send(ctx, m)
	require.NoError(t, err)
	assert.Equal(t, first.ID, again.ID)

	for _, k := range []string{friendsKey, machinesKey, LogKey, StreamOf("bob"), OwedOf("bob"), PushKey, SentOf("bob", "t")} {
		assert.ErrorContains(t, ada.Set(ctx, k, "x", 0).Err(), "NOPERM", "SET %s", k)
		assert.ErrorContains(t, ada.Get(ctx, k).Err(), "NOPERM", "GET %s", k)
	}
	require.NoError(t, ada.Set(ctx, SentOf("ada", "own"), "x", 0).Err())

	// the receipts under the line: recv stamps delivered, the daemon read, a reply acted
	to, err := (&Bus{Store: Redis{C: admin}}).Send(ctx, Message{From: "bob", To: []string{"ada"}, Subject: "for ada", Body: "x\n"})
	require.NoError(t, err)
	var stampErr error
	b.OnStampError = func(err error) { stampErr = err }
	_, ok, err := b.Recv(ctx, "ada", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, stampErr)
	_, err = b.Stamp(ctx, "ada", Read, to.ID)
	require.NoError(t, err)
	_, err = b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "re", Body: "x\n", Re: to.ID})
	require.NoError(t, err)
	got, _, err := b.Stages(ctx, "ada", to.ID)
	require.NoError(t, err)
	assert.Equal(t, Acted, got[0].State)
	friends, err := admin.SMembers(ctx, friendsKey).Result()
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"ada", "bob"}, friends, "the roster is untouched")

	wide := apply("wide", strings.Replace(line, " (~bus2:sent:ada:* +get +set)", " +get +set", 1))
	require.NoError(t, wide.Set(ctx, "machines", "x", 0).Err(), "reversed: +set on the root overwrites a roster key")
}

// The receipts on a real store: recv stamps delivered at the server's time,
// a stamp moves only forward and never starts past delivered, a reply acts
// in the send's transaction and in the token's script, a message the claim
// hands in again comes back acted, and overdue lists only what no reader
// took (SPEC-BUS.md, message-receipts).
func TestRedisReceiptsMoveForwardOnlyAndOverdueListsTheUntaken(t *testing.T) {
	t.Parallel()
	b, c, ctx := live(t)
	m1, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "one", Body: "x"})
	require.NoError(t, err)
	m2, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "two", Body: "x"})
	require.NoError(t, err)
	late, now, err := b.Overdue(ctx, 0)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), now, time.Minute)
	require.Len(t, late, 2)
	assert.Equal(t, m1.ID, late[0].ID)

	e, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "", e.Stage)
	got, now, err := b.Stages(ctx, "bob", m1.ID, m2.ID)
	require.NoError(t, err)
	assert.Equal(t, Delivered, got[0].State)
	assert.WithinDuration(t, now, got[0].At, 2*time.Second, "the server's time")
	assert.Equal(t, "", got[1].State)
	late, _, err = b.Overdue(ctx, 0)
	require.NoError(t, err)
	require.Len(t, late, 1)
	assert.Equal(t, m2.ID, late[0].ID)

	prior, err := b.Stamp(ctx, "bob", Acted, m2.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{""}, prior, "never acted before delivered")
	prior, err = b.Stamp(ctx, "bob", Read, m1.ID, m2.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{Delivered, ""}, prior)
	prior, err = b.Stamp(ctx, "bob", Delivered, m1.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{Read}, prior)
	v, err := c.HGet(ctx, StagesOf("bob"), m1.ID).Result()
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(v, "read "), "never back: %q", v)

	_, err = b.Send(ctx, Message{From: "bob", To: []string{"ada"}, Subject: "re one", Body: "x", Re: m1.ID})
	require.NoError(t, err)
	e2, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, m2.ID, e2.Message().ID)
	_, err = b.Send(ctx, Message{From: "bob", To: []string{"ada"}, Subject: "re two", Body: "x", Re: m2.ID, Token: "t-re"})
	require.NoError(t, err)
	got, _, err = b.Stages(ctx, "bob", m1.ID, m2.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{Acted, Acted}, []string{got[0].State, got[1].State}, "a reply acts, with a token or without")

	require.NoError(t, b.Store.Release(ctx, StreamOf("bob"), "bob", e.Entry))
	again, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m1.ID, again.Message().ID)
	assert.Equal(t, Acted, again.Stage, "handed in again, it says it was acted")
	got, _, err = b.Stages(ctx, "bob")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, Acted, got[0].State)
}
