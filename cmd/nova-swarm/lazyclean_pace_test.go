package main

import (
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The cleaner's pace (2026-10-17): every 2 s on every seat a lazy round listed the slots
// and the results and read 4 of the build cache's 256 subdirectories, the whole cache every
// 128 s, while nothing was there to remove and the cache sat far under its limit. Each round
// here is an explicit call on a clock that starts at 1000 s; nothing waits.

// TestTheLazyRoundSetsItsOwnPace: a round that removed nothing comes back lazyQuiet later; a
// round that removed its bound and left more comes back lazyBacklog later until the backlog
// is gone; the round that finishes it lazyEvery later; then quiet again.
func TestTheLazyRoundSetsItsOwnPace(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	now := time.Unix(1000, 0)
	p.r.Epoch(14)
	assert.Equal(t, lazyQuiet, p.r.lazy(now), "nothing to remove: the quiet pace")

	launches := lazyRound/5 + 3 // past one round's bound
	for i := range launches {
		p.oldLaunch("c"+strconv.Itoa(i), 5, now)
	}
	now = now.Add(leftoverIdle + time.Second)
	assert.Equal(t, lazyBacklog, p.r.lazy(now), "a full round with more left: the backlog pace")
	assert.Equal(t, lazyEvery, p.r.lazy(now), "the round that empties the backlog")
	assert.Equal(t, lazyQuiet, p.r.lazy(now), "and quiet again")

	p.r.tagged = make(chan ended, 1)
	p.r.tagged <- ended{name: "x"}
	assert.Equal(t, lazyEvery, p.r.lazy(now), "a tagged launch waits: the round yields")
}

// TestTheCacheFarUnderItsLimitIsMeasuredOnlyEveryTenMinutes: the first round measures the
// cache whole; while that measure is at or under the low-water mark no round reads the cache
// again until cacheRemeasure has passed, and then one reads it whole. A cache over its limit
// is a backlog: it is read and trimmed lazyBacklog apart until it is down.
func TestTheCacheFarUnderItsLimitIsMeasuredOnlyEveryTenMinutes(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	p.r.cacheLimit = 100 << 10 // low-water mark 80 KiB
	now := time.Unix(1000, 0)
	dir := p.r.goBuildCache()
	for i := range 10 {
		buildEntry(t, dir, "a"+strconv.Itoa(i), 1024, now)
	}
	assert.Equal(t, lazyQuiet, p.r.lazy(now))
	assert.Equal(t, int64(10<<10), p.r.cache.Total(), "the first round measures the whole cache")

	buildEntry(t, dir, "late", 1024, now)
	for at := now; at.Before(now.Add(cacheRemeasure)); at = at.Add(lazyQuiet) {
		p.r.lazy(at)
	}
	assert.Equal(t, int64(10<<10), p.r.cache.Total(), "far under the limit: no round reads the cache inside cacheRemeasure")
	p.r.lazy(now.Add(cacheRemeasure))
	assert.Equal(t, int64(11<<10), p.r.cache.Total(), "at cacheRemeasure it is measured whole again")

	// over the limit, every entry three hours unused: trimmed at the backlog pace
	over := newPool(t)
	over.r.cacheLimit = 4 << 10
	old := time.Unix(1000, 0)
	odir := over.r.goBuildCache()
	for i := range 10 {
		buildEntry(t, odir, "o"+strconv.Itoa(i), 1024, old)
	}
	at := old.Add(3 * time.Hour)
	require.Equal(t, lazyBacklog, over.r.lazy(at), "measured over the limit: a backlog")
	rounds := 1
	for over.r.cache.Total() > 4<<10 && rounds < 200 {
		at = at.Add(lazyBacklog)
		over.r.lazy(at)
		rounds++
	}
	assert.LessOrEqual(t, over.r.cache.Total(), int64(4<<10), "trimmed under its limit")
	assert.LessOrEqual(t, rounds, 1+256/cacheDirsPerRound, "within one sweep of backlog rounds")
}
