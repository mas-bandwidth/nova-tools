package main

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The role observed from real queue/Take/read-begin receipts is replayed by
// tla/PriorityQueuedSource.tla. Producer ids come from queue primary fields.
// Existing physical consumers are created before the producer is promoted.
func TestPriorityQueuedSourceBridge(t *testing.T) {
	t.Parallel()
	t.Run("next free member slot", func(t *testing.T) {
		t.Parallel()
		r, active := workedRig(t, 2, 2)
		before := priorityQueueCards(t, r, "m")
		var ready []queueCard
		for _, c := range before {
			if c.Col == "ready" {
				ready = append(ready, c)
			}
		}
		require.Len(t, ready, 2)
		later := ready[1]
		require.NotEmpty(t, later.Primary)
		r.boss("priority " + later.Primary + " --blocker --reason 'queued producer promotion'")
		require.Empty(t, taken(t, r.one("take", "--as", "m", "--limit", "1", "--epoch", "0", "--json")), "promotion steals no active slot")
		res := r.one("finish", "--as", "m", active[0], "--epoch", "0", "--report", "done", "--head", "0123456789abcdef0123456789abcdef01234567")
		require.Equal(t, 0, res.Code, res.Stderr)
		starts := taken(t, r.one("take", "--as", "m", "--limit", "1", "--epoch", "0", "--json"))
		require.Len(t, starts, 1)
		id, _, _ := strings.Cut(starts[0], "@")
		observed := "earlier"
		if id == later.ID {
			observed = "later"
		}
		assertPriorityQueueObservation(t, observed)
	})
	t.Run("already asked read packet and begin", func(t *testing.T) {
		t.Parallel()
		r := askedRig(t, 2)
		before := priorityQueueCards(t, r, "r")
		require.Len(t, before, 2)
		for _, c := range before {
			assert.Equal(t, "reader", c.Priority)
		}
		later := before[1]
		require.NotEmpty(t, later.Primary)
		r.boss("priority " + later.Primary + " --blocker --reason 'asked producer promotion'")
		after := r.queueWith("--as", "r", "--json", "--packets", "1")
		for _, c := range priorityQueueCards(t, r, "r") {
			assert.Equal(t, "reader", c.Priority, "promotion changes urgency within the reader role")
		}
		packets := after.packeted()
		require.Len(t, packets, 1)
		observed := "earlier"
		if packets[0] == later.ID {
			observed = "later"
		}
		assertPriorityQueueObservation(t, observed)
		res := r.one("read", "--as", "r", "--begin", priorityQueueLease(later), "--epoch", "0")
		require.Equal(t, 0, res.Code, res.Stderr)
		assert.Equal(t, []string{later.ID}, r.queueWith("--as", "r", "--json").ids("reading"))
	})
	t.Run("fixed fix role preserves producer order", func(t *testing.T) {
		t.Parallel()
		r := askedRig(t, 2)
		before := priorityQueueCards(t, r, "r")
		require.Len(t, before, 2)
		later := before[1]
		for _, c := range before {
			res := r.one("read", "--as", "r", "--broken", priorityQueueLease(c), "--finding", "line 1: retain the missing value", "--usage", "input=1000 output=100", "--epoch", "0")
			require.Equal(t, 0, res.Code, res.Stderr)
		}
		r.boss("rework " + before[0].Primary + " " + later.Primary + " --fix 'retain the missing value'")
		r.boss("priority " + later.Primary + " --blocker --reason 'fix role retains updated producer urgency'")
		ready := priorityQueueCards(t, r, "m")
		var laterID string
		for _, c := range ready {
			if c.Col == "ready" {
				assert.Equal(t, "fix", c.Priority)
				if c.Primary == later.Primary {
					laterID = c.ID
				}
			}
		}
		require.NotEmpty(t, laterID)
		starts := taken(t, r.one("take", "--as", "m", "--limit", "1", "--epoch", "0", "--json"))
		require.Len(t, starts, 1)
		id, _, _ := strings.Cut(starts[0], "@")
		observed := "earlier"
		if id == laterID {
			observed = "later"
		}
		assertPriorityQueueObservation(t, observed)
	})
}

func priorityQueueCards(t *testing.T, r *serverRig, row string) []queueCard {
	t.Helper()
	res := r.one("queue", "--as", row, "--json")
	require.Equal(t, 0, res.Code, res.Stderr)
	var q struct{ Cards []queueCard }
	require.NoError(t, json.Unmarshal([]byte(res.Stdout), &q))
	return q.Cards
}

func assertPriorityQueueObservation(t *testing.T, role string) {
	t.Helper()
	data, err := os.ReadFile("../../tla/PriorityQueuedSource.tla")
	require.NoError(t, err)
	assert.Contains(t, string(data), "QueueSourceObserved == <<\""+role+"\">>", "the real consumer selected must replay through the model")
}

// Legacy paid-reader cards have no generation field and accept a bare id. A
// generated lease, including any returned lease, names the actual queue tag.
func priorityQueueLease(c queueCard) string {
	if c.Gen > 0 {
		return c.ID + "@" + strconv.Itoa(c.Gen)
	}
	return c.ID
}
