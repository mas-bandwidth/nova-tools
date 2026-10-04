package bus2

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var start = time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)

// rig is a bus over a fresh fake with a fixed random source, so ids are
// reproducible and no test shares a store.
func rig(t *testing.T, names ...string) (*Bus, *Fake) {
	t.Helper()
	f := NewFake(start, names...)
	n := byte(0)
	return &Bus{Store: f, Rand: func(b []byte) (int, error) {
		for i := range b {
			n++
			b[i] = n
		}
		return len(b), nil
	}}, f
}

func msg(from string, to ...string) Message {
	return Message{From: from, To: to, Subject: "hello", Body: "the body\n"}
}

func TestSendRefusesEveryProblemAtOnce(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		m    Message
		says []string
	}{
		{"empty body", Message{From: "ada", To: []string{"bob"}, Subject: "s"}, []string{"the body is empty"}},
		{"body over the limit", Message{From: "ada", To: []string{"bob"}, Subject: "s", Body: strings.Repeat("x", MaxBody+1)}, []string{"at most 1048576"}},
		{"bad name", Message{From: "Ada", To: []string{"b_ob"}, Subject: "s", Body: "x"}, []string{`"Ada" is not lowercase`, `"b_ob" is not lowercase`}},
		{"no recipient", Message{From: "ada", Subject: "s", Body: "x"}, []string{"no recipient"}},
		{"no subject", Message{From: "ada", To: []string{"bob"}, Body: "x"}, []string{"the subject is empty"}},
		{"everything at once", Message{From: "", To: []string{"B"}}, []string{"a name is empty", `"B" is not`, "the body is empty", "the subject is empty"}},
		{"unknown recipient", Message{From: "ada", To: []string{"zed"}, CC: []string{"bob"}, Subject: "s", Body: "x"}, []string{"zed is no known name", "nova-config friend add zed"}},
		{"unknown sender", Message{From: "nobody", To: []string{"bob"}, Subject: "s", Body: "x"}, []string{"nobody is no known name"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b, f := rig(t, "ada", "bob")
			_, err := b.Send(context.Background(), c.m)
			var r *Refusal
			require.ErrorAs(t, err, &r)
			for _, s := range c.says {
				assert.Contains(t, err.Error(), s)
			}
			assert.Equal(t, 0, f.Len(LogKey), "a refused send writes nothing")
		})
	}
}

func TestSendWritesEveryStreamAndTheLogOnce(t *testing.T) {
	t.Parallel()
	b, f := rig(t, "ada", "bob", "cy")
	m, err := b.Send(context.Background(), Message{From: "ada", To: []string{"bob", "cy", "bob"}, CC: []string{"ada"}, Subject: "s", Body: "x", Re: "R1"})
	require.NoError(t, err)
	assert.Len(t, m.ID, 26)
	assert.Equal(t, start.Add(time.Second), m.At, "at is the store's time, to the second, not the client's")
	assert.Equal(t, []string{"bob", "cy"}, m.To, "a recipient named twice is one")
	for _, s := range []string{StreamOf("bob"), StreamOf("cy"), StreamOf("ada"), LogKey} {
		assert.Equal(t, 1, f.Len(s), s)
	}
	assert.Equal(t, 2, f.Trips, "a send is two trips: the roster and time, then the one transaction")
	got, err := b.Log(context.Background(), "-")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, m, got[0].Message(), "what the log holds is what was sent")
}

func TestSendIdsRiseWithTime(t *testing.T) {
	t.Parallel()
	b, _ := rig(t, "ada", "bob")
	m1, err := b.Send(context.Background(), msg("ada", "bob"))
	require.NoError(t, err)
	m2, err := b.Send(context.Background(), msg("ada", "bob"))
	require.NoError(t, err)
	assert.Less(t, m1.ID, m2.ID, "a ULID made a second later sorts after")
	assert.True(t, m2.At.After(m1.At))
}

func TestRecvHandsPendingBeforeNewAndAckEndsIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, f := rig(t, "ada", "bob")
	m1, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	m2, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)

	e, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m1.ID, e.Message().ID, "the oldest new message first")

	// a second reader at once: the live reader keeps m1, so m2 is handed out
	e2, ok, err := b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m2.ID, e2.Message().ID, "a message held under ClaimAfter is not handed out again (Bus2.tla HeldStaysHeld)")

	// the reader of m1 died: after ClaimAfter it is handed out again, before anything new
	acked, err := b.AckEntry(ctx, "bob", e2.Entry)
	require.NoError(t, err)
	assert.True(t, acked)
	_, ok, err = b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	assert.False(t, ok, "nothing new, and m1 is still held")
	f.Advance(ClaimAfter)
	m3, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	e, ok, err = b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m1.ID, e.Message().ID, "a pending message its reader lost is handed out before any new one (Bus2.tla PendingBeforeNew)")

	acked, err = b.AckEntry(ctx, "bob", e.Entry)
	require.NoError(t, err)
	assert.True(t, acked)
	acked, err = b.AckEntry(ctx, "bob", e.Entry)
	require.NoError(t, err)
	assert.False(t, acked, "acking twice is a no-op (Bus2.tla AckIdempotent)")

	e, ok, err = b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, m3.ID, e.Message().ID)

	_, ok, err = b.Recv(ctx, "ada", 0)
	require.NoError(t, err)
	assert.False(t, ok, "nothing for ada")
}

func TestRecvRefusesAnUnknownNameAndMakesNoStream(t *testing.T) {
	t.Parallel()
	b, f := rig(t, "ada")
	_, _, err := b.Recv(context.Background(), "bobb", 0)
	var r *Refusal
	require.ErrorAs(t, err, &r)
	assert.Contains(t, err.Error(), "bobb is no known name")
	assert.Contains(t, err.Error(), "nova-config friend add bobb")
	assert.Equal(t, 0, f.Len(StreamOf("bobb")), "no stream was made for a name nobody can send to")
}

func TestAckByMessageIdIsIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, _ := rig(t, "ada", "bob")
	m1, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)
	m2, err := b.Send(ctx, msg("ada", "bob"))
	require.NoError(t, err)

	got, err := b.Ack(ctx, "bob", []string{m1.ID})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{m1.ID: false}, got, "nothing is pending before a recv: no group yet")

	_, _, err = b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	got, err = b.Ack(ctx, "bob", []string{m1.ID, m2.ID, "NOPE"})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{m1.ID: true, m2.ID: false, "NOPE": false}, got, "m1 was pending, m2 not delivered yet")
	got, err = b.Ack(ctx, "bob", []string{m1.ID})
	require.NoError(t, err)
	assert.Equal(t, map[string]bool{m1.ID: false}, got, "the same ack again changes nothing")

	pending, fresh, err := b.Peek(ctx, "bob")
	require.NoError(t, err)
	assert.Empty(t, pending)
	require.Len(t, fresh, 1)
	assert.Equal(t, m2.ID, fresh[0].Message().ID)
}

func TestPeekReadsOnly(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, f := rig(t, "ada", "bob")
	for range 3 {
		_, err := b.Send(ctx, msg("ada", "bob"))
		require.NoError(t, err)
	}
	pending, fresh, err := b.Peek(ctx, "bob")
	require.NoError(t, err)
	assert.Empty(t, pending)
	assert.Len(t, fresh, 3, "before any recv every message is new")
	_, _, err = b.Recv(ctx, "bob", 0)
	require.NoError(t, err)
	pending, fresh, err = b.Peek(ctx, "bob")
	require.NoError(t, err)
	assert.Len(t, pending, 1)
	assert.Len(t, fresh, 2)
	pending, fresh, err = b.Peek(ctx, "bob")
	require.NoError(t, err)
	assert.Len(t, pending, 1, "a peek moves nothing")
	assert.Len(t, fresh, 2)
	_, _, err = b.Peek(ctx, "ada")
	require.NoError(t, err)
	_, made := f.groups[StreamOf("ada")+"/ada"]
	assert.False(t, made, "a peek makes no group")
}

func TestLogIsEverythingOldestFirst(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	b, _ := rig(t, "ada", "bob")
	m1, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: "one", Body: "x"})
	require.NoError(t, err)
	m2, err := b.Send(ctx, Message{From: "bob", To: []string{"ada"}, Subject: "two", Body: "y", Re: m1.ID})
	require.NoError(t, err)
	got, err := b.Log(ctx, "-")
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, m1, got[0].Message())
	assert.Equal(t, m2, got[1].Message())
}

func TestAStoreThatIsDownIsAnError(t *testing.T) {
	t.Parallel()
	b, f := rig(t, "ada", "bob")
	f.Fail = errors.New("dial tcp: connection refused")
	ctx := context.Background()
	_, err := b.Send(ctx, msg("ada", "bob"))
	assert.ErrorIs(t, err, f.Fail)
	_, _, err = b.Recv(ctx, "bob", 0)
	assert.ErrorIs(t, err, f.Fail)
	_, _, err = b.Peek(ctx, "bob")
	assert.ErrorIs(t, err, f.Fail)
	_, err = b.Ack(ctx, "bob", []string{"X"})
	assert.ErrorIs(t, err, f.Fail)
	_, err = b.Log(ctx, "-")
	assert.ErrorIs(t, err, f.Fail)
	_, err = b.Names(ctx)
	assert.ErrorIs(t, err, f.Fail)
}

func TestNamesAreSortedAndUnique(t *testing.T) {
	t.Parallel()
	b, _ := rig(t, "zed", "ada", "zed")
	got, err := b.Names(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []string{"ada", "zed"}, got)
}

func TestCheckName(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"ada": "", "a-1": "", strings.Repeat("a", 64): "",
		"":                      "a name is empty",
		"Ada":                   "not lowercase",
		"a b":                   "not lowercase",
		strings.Repeat("a", 65): "65 bytes, at most 64",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			if want == "" {
				assert.Empty(t, CheckName(in))
			} else {
				assert.Contains(t, CheckName(in), want)
			}
		})
	}
}

func TestParseRoundTrips(t *testing.T) {
	t.Parallel()
	m := Message{ID: "01J", From: "ada", To: []string{"bob", "cy"}, CC: []string{"dee"}, Subject: "s", Re: "00X", At: start, Body: "multi\nline\n"}
	assert.Equal(t, m, Parse(m.Fields()))
	assert.Equal(t, Message{}, Parse(map[string]string{}), "an empty entry is the zero message, not a refusal")
}

func TestULIDIsCrockfordAndTimeOrdered(t *testing.T) {
	t.Parallel()
	b, _ := rig(t)
	a, err := b.ulid(start)
	require.NoError(t, err)
	z, err := b.ulid(start.Add(time.Hour))
	require.NoError(t, err)
	assert.Regexp(t, `^[0-9A-HJKMNP-TV-Z]{26}$`, a)
	assert.Less(t, a, z)
	assert.Equal(t, "01M40T5AG0", a[:10], "the first ten characters are the millisecond time (computed apart from this code)")
	assert.NotEqual(t, a[10:], z[10:], "the random half differs")
}
