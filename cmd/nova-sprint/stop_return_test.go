package main

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStopReturnNeedsOwnedCancellationBeforeExplicitRestart(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1 --readers reader-a,reader-b")
	ta.ok("add --stream s1 --count 1 --one")
	ta.deal(1)
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	require.Len(t, q.Cards, 1)
	card := q.Cards[0]
	word := card.ID + "@" + strconv.Itoa(max(card.Gen, 1))
	ta.ok("take --as m1 " + word)
	ta.ok("stop --reason 'operator pause' --until 1h")
	code, _, why := ta.do("start")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, why, "m1:"+word)
	assert.Contains(t, why, "stop-return")
	ta.ok("stop-return --as m1 --epoch 0 " + word + " --reason 'owned child exited'")
	ta.json("queue --as m1", &q)
	require.Len(t, q.Cards, 1)
	assert.Equal(t, sprint.Ready, q.Cards[0].Col)
	assert.Equal(t, card.ID, q.Cards[0].ID)
	assert.Equal(t, card.Gen+1, q.Cards[0].Gen)
	assert.Contains(t, ta.ok("start"), "after=RUNNING")
}

// A reader can lose either command's reply and retry its immutable queue
// packet through a new CLI process. Begin and verdict use separate op IDs.
func TestNamedReadReplyLossReplaysBeginAndVerdict(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1 --readers reader-a,reader-b")
	ta.inReview(1)
	ta.ok("ask")
	var q struct{ Cards []queueCard }
	ta.json("queue --as reader-a", &q)
	require.Len(t, q.Cards, 1)
	card := q.Cards[0]
	word := card.ID + "@" + strconv.Itoa(max(card.Gen, 1))
	restart := func() {
		old := ta
		ta = newTestApp(t)
		ta.m, ta.now, ta.live = old.m, old.now, old.live
		ta.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return ta.m, nil }
		ta.m.LogWait = func(d time.Duration) { ta.a.sleep(d) }
	}
	read := func(line string) output {
		t.Helper()
		var out output
		require.NoError(t, json.Unmarshal([]byte(ta.ok(line)), &out))
		return out
	}
	const beginOp, verdictOp = "reader-a-begin-s1-1", "reader-a-verdict-s1-1"
	begin := "read --as reader-a --begin " + word + " --epoch 0 --op " + beginOp + " --json"
	first := read(begin) // its response is lost before the caller observes it
	require.False(t, first.Replay)
	require.Len(t, first.Moved, 1)
	restart()
	again := read(begin)
	assert.True(t, again.Replay)
	assert.Equal(t, first.Moved, again.Moved)
	code, _, _ := ta.do("read --as reader-a --ok " + word + " --epoch 0 --op " + beginOp + " --json")
	assert.NotEqual(t, 0, code, "a verdict cannot reuse the begin operation")
	verdict := "read --as reader-a --ok " + word + " --epoch 0 --op " + verdictOp + " --json"
	first = read(verdict) // this response is lost too
	require.False(t, first.Replay)
	require.Len(t, first.Moved, 1)
	restart()
	again = read(verdict)
	assert.True(t, again.Replay)
	assert.Equal(t, first.Moved, again.Moved)
	var closed *sprint.Card
	for _, c := range ta.readersOf("s1-1") {
		if c.ID == card.ID {
			closed = c
		}
	}
	require.NotNil(t, closed)
	assert.Equal(t, sprint.OK, closed.Col)
}

func TestReadBeginRejectsCachedPacketAfterStopReturn(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1 --readers reader-a,reader-b")
	ta.inReview(1)
	ta.ok("ask")
	var q struct{ Cards []queueCard }
	ta.json("queue --as reader-a", &q)
	require.Len(t, q.Cards, 1)
	card := q.Cards[0]
	stale := card.ID + "@" + strconv.Itoa(max(card.Gen, 1))
	ta.ok("read --as reader-a --begin " + stale)
	ta.ok("stop --reason 'operator pause' --until 1h")
	ta.ok("stop-return --as reader-a --epoch 0 " + stale + " --reason 'reader child exited'")
	ta.ok("start")
	code, _, why := ta.do("read --as reader-a --begin " + stale)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, why, "stale: generation")
	code, _, why = ta.do("read --as reader-a --begin " + card.ID)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, why, "names no generation")
	ta.ok("read --as reader-a --begin " + card.ID + "@" + strconv.Itoa(max(card.Gen, 1)+1))
}
