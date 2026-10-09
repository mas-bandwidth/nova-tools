package main

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/sprintwire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkerStopReturnReplyLossReplaysThroughServerAfterRestart(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1 --readers reader-a,reader-b")
	ta.ok("add --stream s1 --count 1 --one")
	ta.deal(1)
	var q struct{ Cards []queueCard }
	ta.json("queue --as m1", &q)
	require.Len(t, q.Cards, 1)
	card := q.Cards[0]
	gen := max(card.Gen, 1)
	ta.ok("take --as m1 " + card.ID + "@" + strconv.Itoa(gen))
	ta.ok("stop --reason 'owner cancelling child' --until 1h")
	argv := append(friend.StopReturnArgv("m1", card.ID, gen, "0"), "--json")
	send := func() output {
		t.Helper()
		response := ta.a.serveFrom(sprintwire.Request{Verbs: [][]string{argv}}, true)
		require.Len(t, response.Results, 1)
		r := response.Results[0]
		require.Zero(t, r.Code, r.Stdout+r.Stderr)
		var out output
		require.NoError(t, json.Unmarshal([]byte(r.Stdout), &out))
		return out
	}
	first := send() // the server commits, but its reply can be lost at the worker
	require.False(t, first.Replay)
	require.Len(t, first.Moved, 1)
	ta.ok("start")
	ta.ok("take --as m1 " + card.ID + "@" + strconv.Itoa(gen+1))
	old := ta
	ta = newTestApp(t)
	ta.m, ta.now, ta.live = old.m, old.now, old.live
	ta.a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return ta.m, nil }
	ta.m.LogWait = func(d time.Duration) { ta.a.sleep(d) }
	again := send()
	assert.True(t, again.Replay)
	assert.Equal(t, first.Op, again.Op)
	ta.json("queue --as m1", &q)
	require.Len(t, q.Cards, 1)
	assert.Equal(t, sprint.Working, q.Cards[0].Col)
	assert.Equal(t, gen+1, q.Cards[0].Gen, "old ACK cannot return the current child")
}
