package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A server restart after a committed batch prefix must not repeat that prefix.
// The second verb's unknown result remains retryable with its original identity.
func TestServerReviewRetriesAPartialBatchAfterRestart(t *testing.T) {
	t.Parallel()
	ta := newTestApp(t)
	ta.ok("init --members m1:2 --readers reader-a,reader-b")
	ta.ok("add --stream s1 --count 4")
	ta.ok("fleet up m1")
	ta.ok("start")
	ta.ok("tick")
	ta.ok("stop --reason r --until 9999h")
	ta.ok("start") // the retry checks worker takes, which STOP now fences
	ta.a.serveAddr = "mem:0"
	req := sprintwire.Request{Verbs: [][]string{
		{"take", "--as", "m1", "--limit", "1", "--epoch", "0", "--op", "partial-first", "--json"},
		{"take", "--as", "m1", "--limit", "1", "--epoch", "0", "--op", "partial-second", "--json"},
	}}
	acquires := 0
	ta.m.Fail = func(point string) error {
		if point == "acquire" {
			acquires++
		}
		if acquires >= 2 {
			return errors.New("injected store outage after committed prefix")
		}
		return nil
	}
	first := ta.a.serveFrom(req, false)
	require.Len(t, first.Results, 2)
	require.Equal(t, 0, first.Results[0].Code, first.Results[0].Stderr)
	require.Equal(t, 2, first.Results[1].Code, first.Results[1].Stdout+first.Results[1].Stderr)
	ta.m.Fail = nil
	restarted := newApp(ta.a.getenv)
	restarted.backend = ta.a.backend
	restarted.now = ta.a.now
	restarted.sleep = ta.a.sleep
	restarted.serveAddr = "mem:0"
	t.Cleanup(restarted.close)
	again := restarted.serveFrom(req, false)
	require.Len(t, again.Results, 2)
	for i, result := range again.Results {
		require.Equal(t, 0, result.Code, result.Stdout+result.Stderr)
		var out output
		require.NoError(t, json.Unmarshal([]byte(result.Stdout), &out))
		assert.Equal(t, i == 0, out.Replay, "only the already committed prefix replays")
		assert.Len(t, out.Moved, 1)
	}
	var q struct {
		Cards []queueCard `json:"cards"`
	}
	require.NoError(t, json.Unmarshal([]byte(ta.ok("queue --as m1 --json")), &q))
	working := 0
	for _, c := range q.Cards {
		if c.Col == "working" {
			working++
		}
	}
	assert.Equal(t, 2, working, "two logical takes across partial failure and restart, not three")
}
