package gocache

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The floor and the low-water mark (2026-10-04): a busy 24-slot member wrote 13-14 GiB
// of build cache in three hours against a 10 GiB limit, and the trim removed, every round,
// entries running builds still read; those builds failed. Each case runs on a fixed clock:
// now is a value handed to each round, and nothing here waits.

// at is the fixed clock of these tests: 1:45 PM, three quarters into an hour.
var at = time.Date(2026, 10, 4, 13, 45, 0, 0, time.UTC)

// TestAnEntryYoungerThanTheFloorIsNeverRemoved pins the floor: a cache far over its limit
// whose entries are all younger than the floor loses none of them, the default floor and a
// named one alike; an entry past the floor goes.
func TestAnEntryYoungerThanTheFloorIsNeverRemoved(t *testing.T) {
	t.Parallel()
	for _, floor := range []time.Duration{0, 5 * time.Hour} {
		t.Run("floor "+floor.String(), func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(t.TempDir(), "go-build")
			b := Bounds{Limit: 4 << 10, Slack: SlackOf(4 << 10), Floor: floor, Dirs: Subdirs, Remove: 1000}
			effective := b.floor()
			var young []string
			for i := range 20 { // spread over the floor, the youngest a minute old
				age := time.Minute + time.Duration(i)*(effective-2*time.Minute)/19
				young = append(young, cacheEntry(t, dir, "young"+strconv.Itoa(i), 1024, at.Add(-age)))
			}
			old := cacheEntry(t, dir, "old", 1024, at.Add(-effective-time.Hour))
			var tr Trim
			trimRounds(t, &tr, dir, at, b, 4)
			assert.NoFileExists(t, old, "the entry past the floor goes")
			for i, path := range young {
				assert.FileExists(t, path, "entry %d, used within the floor %s, is kept", i, effective)
			}
			assert.Greater(t, tr.Total(), b.Limit, "the cache stays over its limit rather than lose an entry a build may read")
		})
	}
}

// TestOverTheLimitTheOldestGoDownToTheLowWaterMarkThenNothing pins the hysteresis: over the
// limit, the oldest entries go until the cache is at the low-water mark (80% of the limit);
// growing again under the limit removes nothing; passing the limit again removes the oldest
// down to the mark again.
func TestOverTheLimitTheOldestGoDownToTheLowWaterMarkThenNothing(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "go-build")
	b := Bounds{Limit: 10 << 10, Slack: SlackOf(10 << 10), Dirs: Subdirs, Remove: 1000}
	require.Equal(t, int64(2<<10), b.Slack, "the low-water mark is 80% of the limit")
	var paths []string
	for i := range 12 { // 12 KiB, over 10: entry i last used 50-i hours ago
		paths = append(paths, cacheEntry(t, dir, "e"+strconv.Itoa(i), 1024, at.Add(-time.Duration(50-i)*time.Hour)))
	}
	var tr Trim
	removed := trimRounds(t, &tr, dir, at, b, 4)
	assert.Equal(t, []int{0, 4, 0, 0}, removed, "measured, then the four oldest in one round, then nothing")
	for i, path := range paths {
		if i < 4 {
			assert.NoFileExists(t, path, "entry %d, among the oldest", i)
		} else {
			assert.FileExists(t, path, "entry %d", i)
		}
	}
	assert.Equal(t, int64(8<<10), tr.Total(), "down to the low-water mark")

	// growing back to the limit, but not past it, removes nothing
	for i := range 2 {
		paths = append(paths, cacheEntry(t, dir, "g"+strconv.Itoa(i), 1024, at.Add(-3*time.Hour)))
	}
	assert.Equal(t, []int{0, 0, 0}, trimRounds(t, &tr, dir, at, b, 3), "under the limit the trim waits")
	assert.Equal(t, int64(10<<10), tr.Total())

	// past the limit again: the oldest go, down to the mark
	paths = append(paths, cacheEntry(t, dir, "past", 1024, at.Add(-3*time.Hour)))
	removed = trimRounds(t, &tr, dir, at, b, 3)
	assert.Equal(t, 3, removed[0]+removed[1]+removed[2])
	for i, path := range paths[:7] {
		assert.NoFileExists(t, path, "entry %d, the oldest left, went second", i)
	}
	assert.Equal(t, int64(8<<10), tr.Total())
}

// TestACacheOverItsLimitAllInUseRemovesNothingAndSaysSoOnceAnHour pins the quiet case: a
// cache over its limit with every entry younger than the floor loses nothing, and InUse is
// set on the first round that finds it, then not again until SayEvery has passed, however
// many rounds run in between.
func TestACacheOverItsLimitAllInUseRemovesNothingAndSaysSoOnceAnHour(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "go-build")
	b := Bounds{Limit: 4 << 10, Slack: SlackOf(4 << 10), Floor: 3 * time.Hour, Dirs: Subdirs, Remove: 1000}
	var paths []string
	for i := range 10 { // every entry used in the last 45 minutes, so all stay within the floor for the 70 minutes the rounds run
		paths = append(paths, cacheEntry(t, dir, "e"+strconv.Itoa(i), 1024, at.Add(-time.Duration(i*5)*time.Minute)))
	}
	var tr Trim
	first := tr.Round(dir, at, b)
	assert.False(t, first.InUse, "nothing is said before the cache is measured")
	var said []int
	for round := range 140 { // a round every 30 seconds: 70 minutes
		now := at.Add(time.Duration(round) * 30 * time.Second)
		c := tr.Round(dir, now, b)
		require.Zero(t, c.Failed, c.Why)
		assert.Zero(t, c.Removed, "round %d", round)
		if c.InUse {
			said = append(said, round)
		}
	}
	assert.Equal(t, []int{0, 120}, said, "said on the first round over, then an hour later, never once a round")
	for _, path := range paths {
		assert.FileExists(t, path)
	}
	assert.Equal(t, int64(10<<10), tr.Total())

	// a dry hold of the same cache says it too, and removes nothing
	hold := Hold(dir, at, Bounds{Limit: 4 << 10, Slack: SlackOf(4 << 10), Remove: 1000, Dry: true})
	assert.True(t, hold.InUse)
	assert.Zero(t, hold.Removed)
	_, err := os.Stat(paths[0])
	require.NoError(t, err)
}
