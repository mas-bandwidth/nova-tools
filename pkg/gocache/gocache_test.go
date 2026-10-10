package gocache

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The trim (gocache.go): a cache over its limit loses its least recently used entries,
// oldest first, until it is the slack under it; never an entry used in the last hour, never
// anything that is not an entry. Each round is an explicit call: nothing here waits.

// cacheEntry writes one build cache entry (an output, -d) of size bytes in the cache's
// subdirectory for its hash, last used at; it returns its path.
func cacheEntry(t *testing.T, dir, seed string, size int, at time.Time) string {
	t.Helper()
	sum := sha256.Sum256([]byte(seed))
	h := hex.EncodeToString(sum[:])
	path := filepath.Join(dir, h[:2], h+"-d")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(strings.Repeat("x", size)), 0o644))
	require.NoError(t, os.Chtimes(path, at, at))
	return path
}

// trimRounds runs the trim until a round removes nothing after the cache was measured,
// at most rounds times, and returns every round's removals.
func trimRounds(t *testing.T, tr *Trim, dir string, now time.Time, b Bounds, rounds int) (removed []int) {
	t.Helper()
	for range rounds {
		c := tr.Round(dir, now, b)
		require.Zero(t, c.Failed, c.Why)
		removed = append(removed, c.Removed)
	}
	return removed
}

// TestTheBuildCacheIsTrimmedUnderItsLimitOldestFirst pins the trim: a cache over its limit
// loses its least recently used entries, oldest first, until it is the slack under the
// limit; what is not a cache entry is never touched.
func TestTheBuildCacheIsTrimmedUnderItsLimitOldestFirst(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "go-build")
	now := time.Now()
	var paths []string
	for i := range 20 { // 20 KiB: entry i last used 100-i hours ago, so paths[0] is the oldest
		paths = append(paths, cacheEntry(t, dir, "e"+strconv.Itoa(i), 1024, now.Add(-time.Duration(100-i)*time.Hour)))
	}
	others := []string{filepath.Join(dir, "README"), filepath.Join(dir, "trim.txt"), filepath.Join(dir, "00", "not-an-entry")}
	for _, path := range others {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte("not an entry\n"), 0o644))
		require.NoError(t, os.Chtimes(path, now.Add(-1000*time.Hour), now.Add(-1000*time.Hour)))
	}
	b := Bounds{Limit: 10 << 10, Slack: 2 << 10, Dirs: 64, Remove: 1000}
	var tr Trim
	trimRounds(t, &tr, dir, now, b, 12)

	for i, path := range paths {
		if i < 12 {
			assert.NoFileExists(t, path, "entry %d, among the 12 oldest, is removed", i)
		} else {
			assert.FileExists(t, path, "entry %d, among the 8 newest, is kept", i)
		}
	}
	assert.Equal(t, int64(8<<10), tr.Total(), "the cache is the slack under its limit")
	for _, path := range others {
		assert.FileExists(t, path, "what is not a cache entry is never removed")
	}
}

// TestAnEntryUsedInTheLastHourIsNeverRemoved pins the guard: entries whose time is within
// Recent (Go may have used them in the last hour) stay, even with the cache left over
// its limit.
func TestAnEntryUsedInTheLastHourIsNeverRemoved(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "go-build")
	now := time.Now()
	var recent, old []string
	for i := range 15 {
		recent = append(recent, cacheEntry(t, dir, "recent"+strconv.Itoa(i), 1024, now.Add(-time.Duration(i*8)*time.Minute)))
	}
	for i := range 5 {
		old = append(old, cacheEntry(t, dir, "old"+strconv.Itoa(i), 1024, now.Add(-time.Duration(48+i)*time.Hour)))
	}
	b := Bounds{Limit: 10 << 10, Slack: 2 << 10, Dirs: Subdirs, Remove: 1000}
	var tr Trim
	trimRounds(t, &tr, dir, now, b, 4)

	for _, path := range old {
		assert.NoFileExists(t, path)
	}
	for _, path := range recent {
		assert.FileExists(t, path, "an entry used within %s is kept", Recent)
	}
	assert.Greater(t, tr.Total(), b.Limit, "the cache stays over its limit rather than lose an entry in use")
}

// TestACacheUnderItsLimitLosesNothing pins the quiet case: under the limit, every round
// reads and removes nothing.
func TestACacheUnderItsLimitLosesNothing(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "go-build")
	now := time.Now()
	var paths []string
	for i := range 5 {
		paths = append(paths, cacheEntry(t, dir, "e"+strconv.Itoa(i), 1024, now.Add(-1000*time.Hour)))
	}
	var tr Trim
	removed := trimRounds(t, &tr, dir, now, Bounds{Limit: 10 << 10, Slack: 2 << 10, Dirs: 64, Remove: 1000}, 10)
	assert.Equal(t, make([]int, 10), removed)
	for _, path := range paths {
		assert.FileExists(t, path)
	}
	assert.Equal(t, int64(5<<10), tr.Total())
}

// TestOneCacheRoundRemovesAtMostItsBound pins the trim's bound: no round removes more than
// its bound, and rounds go on until the cache is under its limit.
func TestOneCacheRoundRemovesAtMostItsBound(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "go-build")
	now := time.Now()
	for i := range 20 {
		cacheEntry(t, dir, "e"+strconv.Itoa(i), 1024, now.Add(-time.Duration(100-i)*time.Hour))
	}
	b := Bounds{Limit: 10 << 10, Slack: 2 << 10, Dirs: Subdirs, Remove: 3}
	var tr Trim
	removed := trimRounds(t, &tr, dir, now, b, 8)
	total := 0
	for i, n := range removed {
		assert.LessOrEqual(t, n, b.Remove, "round %d", i)
		total += n
	}
	assert.Equal(t, 12, total)
	assert.Equal(t, int64(8<<10), tr.Total())
}

// TestHoldTrimsAWholeCacheInOneCallAndDryRemovesNothing pins the one-shot trim: Hold
// measures and trims the whole cache in one call, and Dry counts the same removal and
// removes nothing.
func TestHoldTrimsAWholeCacheInOneCallAndDryRemovesNothing(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "go-build")
	now := time.Now()
	var paths []string
	for i := range 20 {
		paths = append(paths, cacheEntry(t, dir, "e"+strconv.Itoa(i), 1024, now.Add(-time.Duration(100-i)*time.Hour)))
	}
	b := Bounds{Limit: 10 << 10, Slack: 2 << 10, Remove: 1000, Dry: true}
	dry := Hold(dir, now, b)
	assert.Equal(t, 12, dry.Removed)
	assert.Equal(t, int64(12<<10), dry.Freed)
	for _, path := range paths {
		assert.FileExists(t, path, "a dry hold removes nothing")
	}
	b.Dry = false
	assert.Equal(t, dry, Hold(dir, now, b), "the hold frees what the dry one said")
	for i, path := range paths {
		if i < 12 {
			assert.NoFileExists(t, path)
		} else {
			assert.FileExists(t, path)
		}
	}
}
