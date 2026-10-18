package bus

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEveryAppendTrimsItsStreamAboutItsBound pins finding 5 of 2026-10-17:
// no XADD carried a bound, and bus2:log held 83,559 entries (140 MB) and
// bus2:to:rowan 52,280 (119 MB). Each append now trims the stream it writes
// (XADD MAXLEN ~, the shape of nova-config's cap log): the log about LogMax,
// each recipient stream about ToMax, by the same one script, a once-send's
// included; no entry's fields change. The store is testkit.PipeStore, with
// the bounds made small.
func TestEveryAppendTrimsItsStreamAboutItsBound(t *testing.T) {
	t.Parallel()
	mr, c := testkit.PipeStore(t) // the store over net.Pipe: no socket
	mr.SetTime(time.Unix(1000, 0))
	ctx := context.Background()
	require.NoError(t, c.SAdd(ctx, friendsKey, "ada", "bob", "cy").Err())
	b := &Bus{Store: Redis{C: c, LogMax: 7, ToMax: 3}}
	var last Message
	for i := range 12 {
		m := Message{From: "ada", To: []string{"bob"}, Subject: fmt.Sprintf("n%d", i), Body: "x"}
		if i%2 == 1 {
			m.Once = fmt.Sprintf("bus2:once:test:%d", i) // the once path is the same script
		}
		var err error
		last, err = b.Send(ctx, m)
		require.NoError(t, err)
	}
	_, err := b.Send(ctx, Message{From: "ada", To: []string{"cy"}, Subject: "to cy", Body: "x"})
	require.NoError(t, err)
	assert.LessOrEqual(t, c.XLen(ctx, StreamOf("bob")).Val(), int64(3), "a recipient stream is held about ToMax")
	assert.Equal(t, int64(1), c.XLen(ctx, StreamOf("cy")).Val(), "a stream under its bound keeps all")
	assert.LessOrEqual(t, c.XLen(ctx, LogKey).Val(), int64(7), "the log is held about LogMax")
	es, err := c.XRevRangeN(ctx, StreamOf("bob"), "+", "-", 1).Result()
	require.NoError(t, err)
	require.Len(t, es, 1)
	fields := map[string]string{}
	for k, v := range es[0].Values {
		fields[k], _ = v.(string) // ignored: every field is a string
	}
	got := Parse(fields)
	assert.Equal(t, last.ID, got.ID, "the newest stays")
	assert.Equal(t, "n11", got.Subject, "and its record is as sent: a trim changes no field")

	lm, tm := Redis{}.bounds()
	assert.Equal(t, int64(200000), lm, "the log's default bound: about five weeks at 2026-10-17's rate")
	assert.Equal(t, int64(50000), tm, "a recipient's default bound: over a week of the busiest stream")
}

// TestAWholeHashReadPrunesTheMarksOfWhatWasTrimmed pins the other half of
// finding 5: the receipt and owed hashes were never cleared (bus2:receipt:rowan
// 35,524 fields, bus2:owed:rowan 22,979), and Stages, Undelivered and Overdue
// read them whole. The read that already holds a hash clears the fields of the
// messages older than the recipient stream's first entry by PruneMargin; a
// message still on the stream, and one trimmed less than the margin before
// the first entry left, keeps its marks, and a field that is no message id is
// left alone.
func TestAWholeHashReadPrunesTheMarksOfWhatWasTrimmed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := NewFake(time.Unix(1000, 0).UTC(), "ada", "bob")
	f.Friends = []string{"ada", "bob"}
	b := &Bus{Store: f}
	var sent []Message
	for i, gap := range []time.Duration{0, time.Hour, time.Hour, 30 * time.Second, time.Hour} {
		f.Advance(gap)
		m, err := b.Send(ctx, Message{From: "ada", To: []string{"bob"}, Subject: fmt.Sprintf("n%d", i), Body: "x"})
		require.NoError(t, err)
		sent = append(sent, m)
	}
	_, err := b.Stamp(ctx, "bob", Delivered, sent[0].ID, sent[1].ID, sent[2].ID, sent[3].ID, sent[4].ID)
	require.NoError(t, err)
	f.mu.Lock()
	f.hashes[StagesOf("bob")]["not-a-ulid"] = "read 1"
	f.hashes[OwedOf("bob")]["not-a-ulid"] = "2026-10-17T00:00:00Z"
	f.streams[StreamOf("bob")] = f.streams[StreamOf("bob")][3:] // trimmed: n3 is the first entry left
	f.mu.Unlock()

	bl, err := b.Undelivered(ctx, "bob")
	require.NoError(t, err)
	// n0 and n1 are an hour and more older than n3: gone. n2 is 30 s older: inside the margin, kept.
	assert.Equal(t, 4, bl[0].Count, "n2, n3, n4 and the odd field stay owed")
	assert.Equal(t, sent[2].ID, bl[0].OldestID)
	f.mu.Lock()
	owed := len(f.hashes[OwedOf("bob")])
	f.mu.Unlock()
	assert.Equal(t, 4, owed, "the store cleared them, not only the answer")

	st, _, err := b.Stages(ctx, "bob")
	require.NoError(t, err)
	var ids []string
	for _, s := range st {
		ids = append(ids, s.ID)
	}
	assert.Equal(t, []string{sent[2].ID, sent[3].ID, sent[4].ID, "not-a-ulid"}, ids, "the receipts of n0 and n1 are cleared")

	_, _, err = b.Overdue(ctx, time.Minute)
	require.NoError(t, err)

	// a stream with no entry says nothing is trimmed: nothing is cleared
	f.mu.Lock()
	f.streams[StreamOf("bob")] = nil
	f.mu.Unlock()
	bl, err = b.Undelivered(ctx, "bob")
	require.NoError(t, err)
	assert.Equal(t, 4, bl[0].Count)

	ms, ok := ulidMillis(sent[0].ID)
	assert.True(t, ok)
	assert.Equal(t, sent[0].At.UnixMilli()/1000, ms/1000, "a message id carries its store time")
	_, ok = ulidMillis("01M4RF7R!PDW5G2T3H9HNNXE9P")
	assert.False(t, ok)
}
