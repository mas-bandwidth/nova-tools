package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workMove is a work-table move's log line, the shape the store writes
// (moveLine): the primary, its place before and its place after.
func workMove(at time.Time, card, from, to string) Line {
	return Line{Kind: LineMove, At: at, Table: Work, Card: card, Stream: "s1", From: from, To: to}
}

// mergeFacts is the facts for the merge queue's check: the shared fake with a
// window and a bar.
func mergeFacts(now time.Time, window, bar time.Duration, lines ...Line) fakeRelease {
	return fakeRelease{now: now, lines: lines, dealtMax: DealtMaxDefault, mergeWindow: window, mergeP90: bar}
}

// TestPercentileNearestRankIsTheValueAtItsRank is the percentile function on
// its own, over known lists (docs/SPEC-RELEASE.md, subsection
// release-check-merge-queue-p90-b.w7).
func TestPercentileNearestRankIsTheValueAtItsRank(t *testing.T) {
	t.Parallel()
	min := func(n int) time.Duration { return time.Duration(n) * time.Minute }

	t.Run("the rank is ceil(q*n), one-based", func(t *testing.T) {
		t.Parallel()
		xs := []time.Duration{min(10), min(20), min(30), min(40), min(50), min(60), min(70), min(80), min(90), min(100)}
		got, ok := PercentileNearestRank(xs, 0.9)
		require.True(t, ok)
		assert.Equal(t, min(90), got, "ceil(0.9*10) = 9, the ninth of ten")
	})

	t.Run("a twenty sample list takes the eighteenth", func(t *testing.T) {
		t.Parallel()
		var xs []time.Duration
		for i := 1; i <= 20; i++ {
			xs = append(xs, min(i))
		}
		got, ok := PercentileNearestRank(xs, 0.9)
		require.True(t, ok)
		assert.Equal(t, min(18), got)
	})

	t.Run("one sample is its own percentile", func(t *testing.T) {
		t.Parallel()
		got, ok := PercentileNearestRank([]time.Duration{min(7)}, 0.9)
		require.True(t, ok)
		assert.Equal(t, min(7), got)
	})

	t.Run("the caller's list is not reordered", func(t *testing.T) {
		t.Parallel()
		xs := []time.Duration{min(30), min(10), min(20)}
		_, _ = PercentileNearestRank(xs, 0.9)
		assert.Equal(t, []time.Duration{min(30), min(10), min(20)}, xs)
	})

	t.Run("no sample has no percentile", func(t *testing.T) {
		t.Parallel()
		_, ok := PercentileNearestRank(nil, 0.9)
		assert.False(t, ok)
	})
}

// TestReleaseCheckFailsWhenTheMergeQueueAgeP90IsOverTheBar is the card's test
// (docs/SPEC-RELEASE.md, subsection release-check-merge-queue-p90-b.w7): the
// p90 of the time each card spent in merging over the last window, by the
// nearest rank, fails over the bar; on fail the evidence prints the p90, the
// count and the oldest card still merging; the check is a pure function over
// the log, so it opens no socket and reads no clock of its own.
func TestReleaseCheckFailsWhenTheMergeQueueAgeP90IsOverTheBar(t *testing.T) {
	t.Parallel()

	t.Run("a queue whose p90 is over the bar fails, naming the oldest still merging", func(t *testing.T) {
		t.Parallel()
		f := mergeFacts(hr(24), MergeQueueWindowDefault, MergeQueueP90Default,
			workMove(hr(1), "s1-1", "s1:review", "s1:merging"),
			workMove(hr(1.2), "s1-1", "s1:merging", "s1:landed"),
			workMove(hr(2), "s1-2", "s1:review", "s1:merging"),
			workMove(hr(2.1), "s1-2", "s1:merging", "s1:landed"),
			workMove(hr(3), "s1-3", "s1:review", "s1:merging"),
			workMove(hr(4), "s1-3", "s1:merging", "s1:landed"),
			workMove(hr(5), "s1-4", "s1:review", "s1:merging"),
		)
		r := MergeQueueP90(f)
		assert.False(t, r.OK, r.Line())
		assert.Equal(t, CheckMergeQueueP90, r.Name)
		assert.Contains(t, r.Evidence, "n=4", "four cards merged in the window")
		assert.Contains(t, r.Evidence, "s1-4", "the oldest still merging")
		assert.Contains(t, r.Evidence, "19h0m0s", "its age at now")
		assert.Equal(t, "RELEASE CHECK "+CheckMergeQueueP90+" fail "+r.Evidence, r.Line())
	})

	t.Run("a queue under the bar is ok, and says the count and the p90", func(t *testing.T) {
		t.Parallel()
		f := mergeFacts(hr(24), MergeQueueWindowDefault, MergeQueueP90Default,
			workMove(hr(1), "s1-1", "s1:review", "s1:merging"),
			workMove(hr(1.2), "s1-1", "s1:merging", "s1:landed"),
			workMove(hr(2), "s1-2", "s1:review", "s1:merging"),
			workMove(hr(2.1), "s1-2", "s1:merging", "s1:landed"),
		)
		r := MergeQueueP90(f)
		assert.True(t, r.OK, r.Line())
		assert.Contains(t, r.Evidence, "p90 12m0s")
		assert.Contains(t, r.Evidence, "n=2")
		assert.Contains(t, r.Evidence, "no card is still merging")
		assert.Equal(t, "RELEASE CHECK "+CheckMergeQueueP90+" ok "+r.Evidence, r.Line())
	})

	t.Run("a card that left merging without landing still counts its time", func(t *testing.T) {
		t.Parallel()
		f := mergeFacts(hr(24), MergeQueueWindowDefault, MergeQueueP90Default,
			workMove(hr(3), "s1-1", "s1:review", "s1:merging"),
			workMove(hr(3.75), "s1-1", "s1:merging", "s1:review"),
		)
		r := MergeQueueP90(f)
		assert.False(t, r.OK, "45m is over the 30m bar")
		assert.Contains(t, r.Evidence, "45m0s")
	})

	t.Run("the bar is what the facts say, not a constant", func(t *testing.T) {
		t.Parallel()
		lines := []Line{
			workMove(hr(1), "s1-1", "s1:review", "s1:merging"),
			workMove(hr(1.5), "s1-1", "s1:merging", "s1:landed"),
		}
		assert.True(t, MergeQueueP90(mergeFacts(hr(24), MergeQueueWindowDefault, 40*time.Minute, lines...)).OK)
		assert.False(t, MergeQueueP90(mergeFacts(hr(24), MergeQueueWindowDefault, 10*time.Minute, lines...)).OK)
	})

	t.Run("a spell that ended before the window is not counted", func(t *testing.T) {
		t.Parallel()
		f := mergeFacts(hr(48), MergeQueueWindowDefault, MergeQueueP90Default,
			workMove(hr(1), "s1-1", "s1:review", "s1:merging"),
			workMove(hr(2), "s1-1", "s1:merging", "s1:landed"),
		)
		r := MergeQueueP90(f)
		assert.True(t, r.OK, r.Line())
		assert.Contains(t, r.Evidence, "n=0")
	})

	t.Run("a card still merging since before the window counts with its age now", func(t *testing.T) {
		t.Parallel()
		f := mergeFacts(hr(48), MergeQueueWindowDefault, MergeQueueP90Default,
			workMove(hr(1), "s1-1", "s1:review", "s1:merging"),
			workMove(hr(2), "s1-2", "s1:review", "s1:merging"),
			workMove(hr(3), "s1-2", "s1:merging", "s1:landed"),
		)
		r := MergeQueueP90(f)
		assert.False(t, r.OK)
		assert.Contains(t, r.Evidence, "n=1", "only the card that was merging inside the window")
		assert.Contains(t, r.Evidence, "s1-1")
		assert.Contains(t, r.Evidence, "47h0m0s")
	})

	t.Run("a log of no merges is ok and says n=0", func(t *testing.T) {
		t.Parallel()
		r := MergeQueueP90(mergeFacts(hr(24), MergeQueueWindowDefault, MergeQueueP90Default))
		assert.True(t, r.OK, r.Line())
		assert.Contains(t, r.Evidence, "n=0")
	})

	t.Run("the check is in the registry with its bar stated", func(t *testing.T) {
		t.Parallel()
		found := false
		for _, c := range ReleaseChecks {
			if c.Name == CheckMergeQueueP90 {
				found = true
				assert.NotEmpty(t, c.Bar)
			}
		}
		assert.True(t, found, "the registry carries the merge queue's check")
	})
}
