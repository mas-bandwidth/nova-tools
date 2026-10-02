package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// The cleaner's lazy work (lazyclean.go): what launches of epochs older than the current
// one minus one left in the slots and the results goes, a bounded number a round, never
// anything running, claimed or unparsed, never through a link; the shared build cache is
// held under its limit, oldest first, never an entry used in the last hour. Each round is
// driven by an explicit call: nothing here waits on the cleaner's clock.

// oldLaunch stages what a work launch of card in epoch leaves once it ended: its directory
// with a file in it, the files beside it, and its results; every time is at.
func (p *pool) oldLaunch(card string, epoch uint64, at time.Time) string {
	p.t.Helper()
	name := launchName(member.Packet{Card: card, Kind: "work", Gen: 1, Epoch: epoch})
	paths := []string{filepath.Join(p.slots, name, "jobs", card, "main.go"), filepath.Join(p.results, name, "RESULT.md")}
	for _, sib := range launchFiles[:3] {
		paths = append(paths, filepath.Join(p.slots, name+sib))
	}
	for _, path := range paths {
		write(p.t, path, "left behind\n")
	}
	for _, path := range append(paths, filepath.Join(p.slots, name), filepath.Join(p.results, name)) {
		require.NoError(p.t, os.Chtimes(path, at, at))
	}
	return name
}

// entries counts a launch's five entries still there: its directory and results, and the
// three files beside the directory.
func (p *pool) entries(name string) (n int) {
	p.t.Helper()
	for _, path := range []string{filepath.Join(p.slots, name), filepath.Join(p.results, name),
		filepath.Join(p.slots, name+".native.log"), filepath.Join(p.slots, name+".card.md"), filepath.Join(p.slots, name+".frame.json")} {
		if _, err := os.Lstat(path); err == nil {
			n++
		}
	}
	return n
}

// drain runs the old-epoch job until a round removes nothing, at most rounds times.
func (p *pool) drain(now time.Time, rounds int) {
	p.t.Helper()
	for range rounds {
		if c := p.r.cleanOld(now); c.removed == 0 {
			return
		}
	}
	p.t.Fatalf("the old epochs were not cleaned in %d rounds", rounds)
}

// TestOldEpochsGoAndTheCurrentAndPreviousStay pins the rule: with the sprint at epoch 14,
// every entry of a launch of epoch 3 or 12 (its directory, the files beside it, its results)
// is removed, and every entry of epochs 13 and 14 is intact; nothing goes before a pass has
// told the epoch; a name that does not parse as a launch's entry is left alone.
func TestOldEpochsGoAndTheCurrentAndPreviousStay(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	hourAgo := time.Now().Add(-time.Hour)
	byEpoch := map[uint64]string{}
	for _, e := range []uint64{3, 12, 13, 14} {
		byEpoch[e] = p.oldLaunch("c"+strconv.FormatUint(e, 10), e, hourAgo)
	}
	unparsed := []string{
		filepath.Join(p.slots, "notes.txt"),
		filepath.Join(p.slots, byEpoch[3]+".extra"),    // a file of no kind a launch writes
		filepath.Join(p.slots, "c3.g1.e3x.native.log"), // no epoch at the end of the name
		filepath.Join(p.slots, "cache", "kept"),
		filepath.Join(p.results, "pr7-bench-slot1", "kept"),
		filepath.Join(p.results, "c9.g1.e3.native.log"), // a file in the results is no launch's
	}
	for _, path := range unparsed {
		write(t, path, "not a launch's\n")
		require.NoError(t, os.Chtimes(path, hourAgo, hourAgo))
	}

	c := p.r.cleanOld(time.Now())
	assert.Zero(t, c.removed, "no pass has told the epoch: nothing is judged old")
	p.r.Epoch(14)
	p.drain(time.Now(), 10)

	assert.Zero(t, p.entries(byEpoch[3]), "epoch 3 is gone")
	assert.Zero(t, p.entries(byEpoch[12]), "epoch 12 is gone")
	assert.Equal(t, 5, p.entries(byEpoch[13]), "the previous epoch is intact")
	assert.Equal(t, 5, p.entries(byEpoch[14]), "the current epoch is intact")
	for _, path := range unparsed {
		assert.FileExists(t, path, "a name that does not parse is left alone")
	}
	assert.DirExists(t, p.slots)
	assert.DirExists(t, p.results)
	assert.Empty(t, p.errb.String())
}

// TestARunningOrClaimedOldLaunchIsNeverRemoved pins the claim: a launch of an old epoch
// this process runs (claimed), one whose pid file names a live process, and one active
// within leftoverIdle keep every entry, whatever their epoch.
func TestARunningOrClaimedOldLaunchIsNeverRemoved(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	hourAgo := time.Now().Add(-time.Hour)
	claimed := p.oldLaunch("claimed", 3, hourAgo)
	p.r.started(claimed)
	running := p.oldLaunch("running", 3, hourAgo)
	write(t, filepath.Join(p.slots, running+".pid"), strconv.Itoa(os.Getpid())+"\n")
	active := p.oldLaunch("active", 3, time.Now())
	ended := p.oldLaunch("ended", 3, hourAgo)

	p.r.Epoch(14)
	p.drain(time.Now(), 10)

	assert.Zero(t, p.entries(ended), "an ended launch of an old epoch goes")
	for _, name := range []string{claimed, running, active} {
		assert.Equal(t, 5, p.entries(name), "%s is never removed", name)
	}
	assert.FileExists(t, filepath.Join(p.slots, running+".pid"))
	c := p.r.cleanOld(time.Now())
	assert.Equal(t, 16, c.left, "what is held is counted as left (three launches, five entries each, and the pid file)")
}

// TestALinkInTheSlotsIsNeverFollowed pins the links: an entry named like an old launch's
// that is a link (to a directory or to a file outside the slots) is left as it is, and its
// target is never reached.
func TestALinkInTheSlotsIsNeverFollowed(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	outside := t.TempDir()
	write(t, filepath.Join(outside, "precious"), "never removed\n")
	require.NoError(t, os.Symlink(outside, filepath.Join(p.slots, "link.g1.e3")))
	require.NoError(t, os.Symlink(filepath.Join(outside, "precious"), filepath.Join(p.slots, "link.g1.e3.native.log")))
	require.NoError(t, os.Symlink(outside, filepath.Join(p.results, "link.g1.e3")))
	ended := p.oldLaunch("ended", 3, time.Now().Add(-time.Hour))

	p.r.Epoch(14)
	p.drain(time.Now(), 10)

	assert.Zero(t, p.entries(ended))
	for _, link := range []string{filepath.Join(p.slots, "link.g1.e3"), filepath.Join(p.slots, "link.g1.e3.native.log"), filepath.Join(p.results, "link.g1.e3")} {
		fi, err := os.Lstat(link)
		require.NoError(t, err, "%s is left", link)
		assert.NotZero(t, fi.Mode()&os.ModeSymlink)
	}
	assert.FileExists(t, filepath.Join(outside, "precious"), "a link's target is never reached")
}

// TestOneRoundRemovesAtMostItsBoundAndSaysSo pins the round's bound and its line: a
// backlog over lazyRound loses at most lazyRound entries a round, and the round says how
// many, the bytes and what is left, in one line; a round with nothing to do says nothing.
func TestOneRoundRemovesAtMostItsBoundAndSaysSo(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	hourAgo := time.Now().Add(-time.Hour)
	launches := lazyRound/5 + 3 // five entries each: past one round's bound
	for i := range launches {
		p.oldLaunch("c"+strconv.Itoa(i), 5, hourAgo)
	}
	p.r.Epoch(14)
	c := p.r.cleanOld(time.Now())
	assert.Equal(t, lazyRound, c.removed)
	assert.Equal(t, launches*5-lazyRound, c.left)
	assert.Positive(t, c.freed)

	p.r.lazy(time.Now())
	line := strings.TrimSpace(p.errb.String())
	assert.Regexp(t, `^CLEAN old epochs: removed [0-9]+ entries, [0-9.]+ (B|KiB|MiB|GiB) freed, 0 left$`, line)
	assert.NotContains(t, line, "\n", "one line a round")

	p.errb.Reset()
	p.r.lazy(time.Now())
	assert.Empty(t, p.errb.String(), "nothing to do says nothing")
}

// TestThePassNeverWaitsOnTheCleaner pins the separation: telling the epoch and ending a
// launch return at once while the cleaner holds a removal; a launch ended with a cleaner is
// tagged, not removed inline; and the lazy work yields while a tagged launch waits.
func TestThePassNeverWaitsOnTheCleaner(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	old := p.oldLaunch("old", 3, time.Now().Add(-time.Hour))
	name := p.launch("c1", 1, time.Now())
	p.r.started(name)
	p.r.tagged = make(chan ended, cleanQueue) // a cleaner whose goroutine this test plays

	p.r.removing.Lock() // the cleaner mid-removal
	p.r.Epoch(14)
	p.r.Ended(member.Packet{Card: "c1", Kind: "work", Gen: 1, Epoch: 1}, false)
	p.r.removing.Unlock()

	assert.True(t, p.exists(name), "the ended launch is tagged, not removed in the pass")
	p.r.lazy(time.Now())
	assert.Equal(t, 5, p.entries(old), "lazy work yields to a tagged launch")

	p.r.clean(<-p.r.tagged)
	assert.False(t, p.exists(name))
	p.r.lazy(time.Now())
	assert.Zero(t, p.entries(old), "with nothing tagged, the lazy round runs")
}

// cacheEntry writes one build cache entry (an output, -d) of size bytes in the cache's
// subdirectory for its hash, last used at; it returns its path.
func cacheEntry(t *testing.T, dir, seed string, size int, at time.Time) string {
	t.Helper()
	sum := sha256.Sum256([]byte(seed))
	h := hex.EncodeToString(sum[:])
	path := filepath.Join(dir, h[:2], h+"-d")
	write(t, path, strings.Repeat("x", size))
	require.NoError(t, os.Chtimes(path, at, at))
	return path
}

// trimRounds runs the trim until a round removes nothing after the cache was measured,
// at most rounds times, and returns every round's removals.
func trimRounds(t *testing.T, tr *cacheTrim, dir string, now time.Time, b cacheBounds, rounds int) (removed []int) {
	t.Helper()
	for range rounds {
		c := tr.round(dir, now, b)
		require.Zero(t, c.failed, c.why)
		removed = append(removed, c.removed)
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
		write(t, path, "not an entry\n")
		require.NoError(t, os.Chtimes(path, now.Add(-1000*time.Hour), now.Add(-1000*time.Hour)))
	}
	b := cacheBounds{limit: 10 << 10, slack: 2 << 10, dirs: 64, remove: 1000}
	var tr cacheTrim
	trimRounds(t, &tr, dir, now, b, 12)

	for i, path := range paths {
		if i < 12 {
			assert.NoFileExists(t, path, "entry %d, among the 12 oldest, is removed", i)
		} else {
			assert.FileExists(t, path, "entry %d, among the 8 newest, is kept", i)
		}
	}
	assert.Equal(t, int64(8<<10), tr.total(), "the cache is the slack under its limit")
	for _, path := range others {
		assert.FileExists(t, path, "what is not a cache entry is never removed")
	}
}

// TestAnEntryUsedInTheLastHourIsNeverRemoved pins the guard: entries whose time is within
// cacheRecent (Go may have used them in the last hour) stay, even with the cache left over
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
	b := cacheBounds{limit: 10 << 10, slack: 2 << 10, dirs: cacheSubdirs, remove: 1000}
	var tr cacheTrim
	trimRounds(t, &tr, dir, now, b, 4)

	for _, path := range old {
		assert.NoFileExists(t, path)
	}
	for _, path := range recent {
		assert.FileExists(t, path, "an entry used within %s is kept", cacheRecent)
	}
	assert.Greater(t, tr.total(), b.limit, "the cache stays over its limit rather than lose an entry in use")
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
	var tr cacheTrim
	removed := trimRounds(t, &tr, dir, now, cacheBounds{limit: 10 << 10, slack: 2 << 10, dirs: 64, remove: 1000}, 10)
	assert.Equal(t, make([]int, 10), removed)
	for _, path := range paths {
		assert.FileExists(t, path)
	}
	assert.Equal(t, int64(5<<10), tr.total())
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
	b := cacheBounds{limit: 10 << 10, slack: 2 << 10, dirs: cacheSubdirs, remove: 3}
	var tr cacheTrim
	removed := trimRounds(t, &tr, dir, now, b, 8)
	total := 0
	for i, n := range removed {
		assert.LessOrEqual(t, n, b.remove, "round %d", i)
		total += n
	}
	assert.Equal(t, 12, total)
	assert.Equal(t, int64(8<<10), tr.total())
}

// TestTheTrimmedCacheIsTheOneLaunchesUse pins the cache's path: the lazy trim's directory
// is the GOCACHE native hands every launch under the same root.
func TestTheTrimmedCacheIsTheOneLaunchesUse(t *testing.T) {
	t.Parallel()
	p := newPool(t)
	env := nativeChildEnv("data", "job", "tmp", nativeCacheDir(nativeRunConfig{root: p.r.root}), "", "", "", "")
	assert.Contains(t, env, "GOCACHE="+p.r.goBuildCache())
	assert.Empty(t, (&nativeRunner{}).goBuildCache(), "no root, no cache")
	assert.Equal(t, "2.0 GiB", sizeWord(2*gib))
	assert.Equal(t, fmt.Sprintf("%d B", 12), sizeWord(12))
}
