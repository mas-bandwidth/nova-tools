package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// The landed series (docs/SPEC-SPRINT.md, where --landed-series; the owner, 2026-10-05): the
// cards landed per 10 minutes over the last 24 hours, split friends and fleet by the worker of
// the landed attempt. A twin's lines, no store and no clock.
func TestTheLandedSeriesCountsEachCardOnceByItsWorker(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 12, 5, 0, 0, time.UTC) // inside the bucket that begins 12:00
	at := func(min int) time.Time {
		return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC).Add(time.Duration(min) * time.Minute)
	}
	mv := func(min int, table, card, from, to string) Line {
		return Line{Kind: LineMove, At: at(min), Table: table, Card: card, From: from, To: to}
	}
	lines := []Line{
		// a friend's card: worked by friend.ada, landed by the lander at 11:55
		mv(-20, "fleet", "a-1.w1", "friend.ada:working", "friend.ada:ok"),
		mv(-5, "work", "a-1", "s1:merging", "s1:landed"),
		// a fleet machine's card: an earlier attempt failed on m2, the last ok is m1's
		mv(-40, "fleet", "b-1.w1", "m2:working", "m2:failed"),
		mv(-30, "fleet", "b-1.w2", "m1:working", "m1:ok"),
		mv(-2, "work", "b-1", "s1:merging", "s1:landed"),
		// landed twice (a clear and a re-land): once, at its first time
		mv(-1, "work", "b-1", "s1:merging", "s1:landed"),
		// a sentinel's release is not work
		mv(-3, "work", "gate", "s1:waiting", "s1:landed"),
		// a card the log never saw worked
		mv(-4, "work", "c-1", "s1:merging", "s1:landed"),
		// a reader's ok is not the work attempt's
		mv(-15, "readers", "d-1.r1.reader-a", "reader-a:reading", "reader-a:ok"),
		mv(-6, "work", "d-1", "s1:merging", "s1:landed"),
		// a set move lands two cards in one line
		{Kind: LineMove, At: at(-8), Table: "work", Card: "e-1", Cards: []string{"e-1", "e-2"}, From: "s1:merging", To: "s1:landed"},
		mv(-9, "fleet", "e-2.w1", "friend.bo:working", "friend.bo:ok"),
		mv(-10, "fleet", "e-1.w1", "m3:working", "m3:ok"),
		// outside the window: 25 h back
		mv(-25*60, "work", "old-1", "s1:merging", "s1:landed"),
	}
	s := LandedSeriesOf(lines, now)
	assert.Equal(t, 600, s.BucketSeconds)
	assert.Equal(t, 144, s.Buckets)
	assert.Equal(t, at(0).Add(-143*10*time.Minute), s.Start, "144 buckets, the last the one now is in")
	assert.Len(t, s.Friends, 144)
	assert.Len(t, s.Fleet, 144)
	last := func(i int) int { return 143 + i } // bucket index of the 10 minutes starting 12:00 + 10i
	// 11:50-12:00 holds the landings at 11:52 to 11:59: b-1, c-1, d-1 (unknown: no work ok), e-1, e-2, a-1
	assert.Equal(t, 2, s.Friends[last(-1)], "a-1 by friend.ada and e-2 by friend.bo")
	assert.Equal(t, 2, s.Fleet[last(-1)], "b-1 by m1, e-1 by m3: the last ok of a card's work attempt, once")
	assert.Equal(t, 0, s.Friends[last(0)]+s.Fleet[last(0)])
	assert.Equal(t, LandedTotals{Friends: 2, Fleet: 2, Unknown: 2}, s.Totals, "c-1 and d-1 have no worker; the sentinel and the old card are not counted")
	assert.Equal(t, LandedTotals{Friends: 2, Fleet: 2, Unknown: 2}, s.LastHour)
	assert.Equal(t, map[string]int{"ada": 1, "bo": 1, "m1": 1, "m3": 1}, s.Workers)
}
