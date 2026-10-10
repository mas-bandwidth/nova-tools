package friend

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluatePeers(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 10, 12, 0, 15, 0, time.UTC)
	records := map[string]map[string]string{
		PresenceKey("alice"): {
			"name":    "alice",
			"seen":    now.Add(-2 * time.Second).Format(time.RFC3339Nano),
			"harness": "claude",
			"route":   "push",
			"asleep":  "0",
			"queue":   "1",
			"working": "2",
			"width":   "4",
			"proved":  now.Add(-5 * time.Second).Format(time.RFC3339Nano),
			"version": "1",
		},
		PresenceKey("bob"): {
			"name":    "bob",
			"seen":    now.Add(-3 * time.Second).Format(time.RFC3339Nano),
			"harness": "opencode",
			"route":   "defer",
			"asleep":  "1",
			"queue":   "0",
			"working": "1",
			"width":   "2",
			"version": "1",
		},
		PresenceKey("charlie"): {
			"name":    "charlie",
			"seen":    now.Add(-15 * time.Second).Format(time.RFC3339Nano),
			"harness": "codex",
			"route":   "passive",
			"version": "1",
		},
	}

	extraNames := []string{"david"}
	rep := EvaluatePeers(records, extraNames, now, 0)

	require.Equal(t, "ok", rep.Status)
	require.Equal(t, "OK", rep.Word)
	require.Equal(t, 1, rep.Up)
	require.Equal(t, 1, rep.Asleep)
	require.Equal(t, 2, rep.Down)
	require.Equal(t, 4, rep.Of)
	require.Len(t, rep.Peers, 4)

	// In name order: alice, bob, charlie, david
	assert.Equal(t, "alice", rep.Peers[0].Name)
	assert.Equal(t, "up", rep.Peers[0].State)
	assert.Equal(t, "2.0", rep.Peers[0].AgeText)
	assert.Equal(t, "5", rep.Peers[0].ProvedText)
	assert.Equal(t, "claude", rep.Peers[0].Harness)
	assert.Equal(t, "push", rep.Peers[0].Route)
	assert.Equal(t, 1, rep.Peers[0].Queue)
	assert.Equal(t, 2, rep.Peers[0].Working)
	assert.Equal(t, 4, rep.Peers[0].Width)
	assert.Equal(t, "1", rep.Peers[0].Version)

	assert.Equal(t, "bob", rep.Peers[1].Name)
	assert.Equal(t, "asleep", rep.Peers[1].State)
	assert.Equal(t, "3.0", rep.Peers[1].AgeText)
	assert.Equal(t, "-", rep.Peers[1].ProvedText)

	assert.Equal(t, "charlie", rep.Peers[2].Name)
	assert.Equal(t, "down", rep.Peers[2].State)
	assert.Equal(t, "15.0", rep.Peers[2].AgeText)

	assert.Equal(t, "david", rep.Peers[3].Name)
	assert.Equal(t, "down", rep.Peers[3].State)
	assert.Equal(t, "-", rep.Peers[3].Seen)
	assert.Equal(t, "-", rep.Peers[3].AgeText)
	assert.Equal(t, "-", rep.Peers[3].Harness)
	assert.Equal(t, "-", rep.Peers[3].Route)
	assert.Equal(t, "-", rep.Peers[3].ProvedText)
	assert.Equal(t, "-", rep.Peers[3].Version)

	lines := rep.Lines()
	require.Len(t, lines, 5)
	assert.Contains(t, lines[0], "PEER name=alice state=up age=2.0")
	assert.Contains(t, lines[1], "PEER name=bob state=asleep age=3.0")
	assert.Contains(t, lines[2], "PEER name=charlie state=down age=15.0")
	assert.Equal(t, "PEER name=david state=down age=- seen=- harness=- route=- queue=0 working=0 width=0 proved=- version=-", lines[3])
	assert.Equal(t, "PEERS OK up=1 asleep=1 down=2 of=4", lines[4])

	// JSON check
	raw, err := json.Marshal(rep)
	require.NoError(t, err)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal(raw, &parsed))
	assert.Equal(t, "ok", parsed["status"])
	assert.Equal(t, "OK", parsed["word"])
	assert.Equal(t, float64(1), parsed["up"])
	assert.Equal(t, float64(1), parsed["asleep"])
	assert.Equal(t, float64(2), parsed["down"])
	assert.Equal(t, float64(4), parsed["of"])
}

func TestEvaluatePeersMax(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 10, 12, 0, 15, 0, time.UTC)
	extraNames := []string{"a", "b", "c"}
	rep := EvaluatePeers(nil, extraNames, now, 2)

	require.Equal(t, 2, len(rep.Peers))
	require.Equal(t, 3, rep.Of)
	require.NotEmpty(t, rep.MoreLine)
	assert.Contains(t, rep.MoreLine, "PEERS MORE kind=PEER shown=2 total=3")

	lines := rep.Lines()
	require.Len(t, lines, 4)
	assert.Equal(t, rep.MoreLine, lines[2])
	assert.Equal(t, "PEERS OK up=0 asleep=0 down=3 of=3", lines[3])
}
