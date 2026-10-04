package refmodel_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
)

// Behaviours the 2026-10-02 break-it probes found no unit test for: each test
// here goes red when its line is broken.

// mergingTwo is a stream s merging with two primaries queued, a before b.
func mergingTwo() refmodel.State {
	s := refmodel.New(nil, nil, coordinator)
	s.Streams["s"] = refmodel.Stream{State: refmodel.SMerging}
	for i, id := range []string{"a", "b"} {
		s.Primaries[id] = refmodel.Primary{Stream: "s", Kind: refmodel.KindPrimary, State: refmodel.Merging, Score: float64(i + 1)}
		s.Merge[id] = refmodel.MergeCard{Place: refmodel.Queued}
	}
	return s
}

// MergeStop takes a fact on a card of the batch only: the batch is the first
// n queued, n bounded by the queue, so a card queued behind the batch is
// refused and a batch longer than the queue is the whole queue.
func TestMergeStopActsOnTheBatchBoundedByTheQueue(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		batch   int
		card    string
		refused bool
	}{
		{"the first of a batch of one", 1, "a", false},
		{"behind a batch of one", 1, "b", true},
		{"the last of a batch of two", 2, "b", false},
		{"a batch longer than the queue", 5, "b", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			n, err := refmodel.MergeStop(mergingTwo(), "s", c.batch, c.card, refmodel.CConflict, "")
			if c.refused {
				var r *refmodel.Refusal
				require.ErrorAs(t, err, &r, "%s: %v", c.name, err)
				assert.Contains(t, r.Why, "is not in the batch", "%s: %v", c.name, err)
				return
			}
			require.NoError(t, err, c.name)
			assert.Equal(t, refmodel.Stuck, n.Merge[c.card].Place, c.name)
			assert.Equal(t, refmodel.SStopped, n.Streams["s"].State, c.name)
		})
	}
}

// StreamNames is the streams in name order, however the map holds them.
func TestStreamNamesAreInNameOrder(t *testing.T) {
	t.Parallel()
	want := []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot"}
	for range 32 {
		s := refmodel.New(nil, nil, coordinator)
		for _, i := range []int{4, 1, 5, 0, 3, 2} {
			s.Streams[want[i]] = refmodel.Stream{State: refmodel.SWaiting}
		}
		require.Equal(t, want, s.StreamNames())
	}
}
