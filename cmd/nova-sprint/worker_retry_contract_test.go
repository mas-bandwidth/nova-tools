package main

import (
	"encoding/json"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The HTTP adapter must preserve this engine contract when a reply is lost.
// This pins the engine only; the transport tests must exercise actual lost replies.
// Libraries considered: the existing testApp, encoding/json and testify suffice.
func TestWorkerTakeRetryIdentityAndEmptyResultContract(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		emptyFirst bool
		retryID    string
		working    int
		replay     bool
	}{
		{name: "committed take replays with its original identity", retryID: "wire-take", working: 2, replay: true},
		{name: "new identity can take different ready cards", retryID: "wire-take-new", working: 4},
		{name: "an empty take is reevaluated when cards become ready", emptyFirst: true, retryID: "wire-take", working: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ta := newTestApp(t)
			ta.ok("init --members m1:2 --readers reader-a,reader-b")
			ta.ok("fleet up m1")
			deal := func() {
				ta.ok("add --stream s1 --count 4")
				ta.ok("start")
				ta.ok("tick")
				ta.ok("stop")
			}
			if !tc.emptyFirst {
				deal()
			}
			// Its reply is deliberately not used to decide the retry's arguments.
			ta.ok("take --as m1 --limit 2 --epoch 0 --op wire-take --json")
			if tc.emptyFirst {
				deal()
			}
			var result output
			require.NoError(t, json.Unmarshal([]byte(ta.ok("take --as m1 --limit 2 --epoch 0 --op "+tc.retryID+" --json")), &result))
			assert.Equal(t, tc.replay, result.Replay)
			assert.Len(t, result.Moved, 2)
			var queue struct {
				Cards []queueCard `json:"cards"`
			}
			require.NoError(t, json.Unmarshal([]byte(ta.ok("queue --as m1 --json")), &queue))
			var working []string
			for _, card := range queue.Cards {
				if card.Col == sprint.Working {
					working = append(working, card.ID)
				}
			}
			assert.Len(t, working, tc.working)
		})
	}
}
