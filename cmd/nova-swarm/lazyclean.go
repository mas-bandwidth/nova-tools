package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcontract"
	"github.com/mas-bandwidth/nova-tools/pkg/gocache"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/safepath"
	"github.com/mas-bandwidth/nova-tools/pkg/swarm"
)

// THE CLEANER'S LAZY WORK (docs/SPEC-SWARM.md, `member`).
//
// What an ended launch leaves is removed by clean (slotclean.go), but only its checkout:
// the small files beside it, its results and the failed directories kept for inspection
// stay, epoch after epoch, and the build cache every launch shares only grows (Go itself
// removes an entry only after five days unused, with no bound on size). A loop that ran a
// day of sprints held thousands of such entries and gigabytes of results and cache. So the
// cleaner, between tagged launches and never on the member's pass, does two more jobs, a
// bounded amount of each per round (lazy):
//
//   - old epochs: an entry of the slots (a launch's directory, its .native.log, .card.md,
//     frame and .pid) or of the results (a launch's directory) whose launch is of an epoch
//     older than the sprint's current epoch minus one is removed (cleanOld). The current
//     epoch and the one before it are never touched: the previous epoch's logs are what is
//     read after a run is stopped and cleared. A name that does not parse as a launch's, a
//     link, and anything of a launch that is running or claimed is left alone, always.
//   - the build cache: the Go build cache this loop's launches share (GOCACHE, nativeChildEnv)
//     is held under --gocache-limit (gocache.Limit, 20 GiB), least recently used entries removed
//     first down to 80% of it, never one used in the last two hours (pkg/gocache).

// keepEpochs is how many epochs, the current one included, keep their slot entries and
// results: the current one, which is running, and the one before it, whose logs are read
// after a run is stopped and cleared. Older ones belong to sprints cleared twice over, and
// nothing reads them again.
const keepEpochs = 2

// lazyRound is the most old-epoch entries one round removes (or tries to). It bounds a
// round by entry count, not by time: an entry may be a whole launch directory, and how long
// its removal takes is not measured here. A round stops partway when a launch is tagged.
const lazyRound = 32

// The cleaner's pace, with no tagged launch to clean: the next lazy round comes lazyEvery
// after a round that removed something, lazyQuiet after one that removed nothing, and
// lazyBacklog after one that removed its bound and left more (old epochs past lazyRound, a
// cache over its limit or still trimming toward its low-water mark), so a backlog of a few thousand entries is still gone
// within minutes. A round every 2 s whatever it found listed the slots and the results
// (1,096 and 309 entries), re-read the pid files of what it still could not remove, and
// read 4 of the cache's subdirectories, the whole 16 GB, 77k-file cache every 128 s, some
// 350 to 1,200 stats a second on every seat, forever (mini-m5-1, 2026-10-17).
const (
	lazyEvery   = 30 * time.Second
	lazyQuiet   = 60 * time.Second
	lazyBacklog = 2 * time.Second
)

// The build cache's trim (pkg/gocache): held under the member's --gocache-limit, then
// down to the low-water mark a fifth under it (gocache.SlackOf). While the last measured
// size is at or under the low-water mark there is nothing to trim, and the cache is only
// measured again, whole, once every cacheRemeasure; above it, each round reads
// cacheDirsPerRound of the cache's 256 subdirectories (measures, and trims when over), the
// size a running sum. cacheRemovePerRound is the most entries one round removes.
const (
	cacheDirsPerRound   = 4
	cacheRemovePerRound = 256
	cacheRemeasure      = 10 * time.Minute
)

// Epoch records the sprint's epoch the member's pass read (member.Epocher): a store, never a
// wait. The cleaner reads it on its own clock.
func (r *nativeRunner) Epoch(epoch uint64) { r.epoch.Store(epoch + 1) }

// urgent says a tagged launch waits for the cleaner: lazy work yields to it.
func (r *nativeRunner) urgent() bool { return len(r.tagged) > 0 }

// lazy is one round of the cleaner's lazy work, when no tagged launch waits: old epochs'
// entries, then the build cache. Each says one line when it removed something. It returns
// how long until the next round (lazyEvery, lazyQuiet, lazyBacklog).
func (r *nativeRunner) lazy(now time.Time) time.Duration {
	if r.urgent() {
		return lazyEvery
	}
	o := r.cleanOld(now)
	if o.removed > 0 || o.failed > 0 {
		fmt.Fprintf(r.stderr, "CLEAN old epochs: removed %d entries, %s freed, %d left%s\n", o.removed, oneline.Escape(sizeWord(o.freed)), o.left, oneline.Escape(o.failures()))
	}
	removed, backlog := o.removed > 0, o.removed > 0 && o.left > 0
	pace := func() time.Duration {
		switch {
		case backlog:
			return lazyBacklog
		case removed:
			return lazyEvery
		}
		return lazyQuiet
	}
	if r.urgent() {
		return pace()
	}
	dir := r.goBuildCache()
	if dir == "" {
		return pace()
	}
	limit := r.gocacheLimit()
	low := limit - gocache.SlackOf(limit)
	dirs := cacheDirsPerRound
	if r.cacheAt.IsZero() || r.cache.Total() <= low {
		// nothing to trim at the last measure: measured again, whole, once every cacheRemeasure
		if !r.cacheAt.IsZero() && now.Sub(r.cacheAt) < cacheRemeasure {
			return pace()
		}
		dirs = gocache.Subdirs
		r.cacheAt = now
	}
	c := r.cache.Round(dir, now, gocache.Bounds{Limit: limit, Slack: gocache.SlackOf(limit), Dirs: dirs, Remove: cacheRemovePerRound})
	// a cache over its limit, or trimming toward the low-water mark, is a backlog: its rounds
	// stay lazyBacklog apart until it is down (and over its limit with every entry in use,
	// the alarm said once an hour, they stay so as they always were)
	removed = removed || c.Removed > 0
	backlog = backlog || c.Size > limit || c.Removed > 0 && c.Size > low
	if c.Removed > 0 || c.Failed > 0 {
		failures := lazyCount{failed: c.Failed, why: c.Why}.failures()
		fmt.Fprintf(r.stderr, "CLEAN go build cache: removed %d entries, %s freed, %s now, limit %s%s\n", c.Removed,
			oneline.Escape(sizeWord(c.Freed)), oneline.Escape(sizeWord(c.Size)), oneline.Escape(sizeWord(limit)), oneline.Escape(failures))
	}
	if c.InUse {
		fmt.Fprintf(r.stderr, "CLEAN go build cache: over the limit, all entries in use: raise the limit (%s now, limit %s, every entry used in the last two hours; nothing is removed; said once an hour; nova-swarm member --gocache-limit <GiB>)\n",
			oneline.Escape(sizeWord(c.Size)), oneline.Escape(sizeWord(limit)))
	}
	return pace()
}

// gocacheLimit is the size the build cache is held under: the member's --gocache-limit,
// else gocache.Limit.
func (r *nativeRunner) gocacheLimit() int64 {
	if r.cacheLimit > 0 {
		return r.cacheLimit
	}
	return gocache.Limit
}

// goBuildCache is the build cache this loop's launches share: native's GOCACHE under the
// root (nativeCacheDir, nativeChildEnv); "" with no root.
func (r *nativeRunner) goBuildCache() string {
	if nativeCacheDir(nativeRunConfig{root: r.root}) != "" {
		return swarm.GoBuildCacheDir(r.root)
	}
	return ""
}

// lazyCount is what one round of old epochs did: removed, failed (each said once, why the
// first), the bytes freed, and the old entries left.
type lazyCount struct {
	removed, failed int
	freed           int64
	left            int
	why             string
}

func (c *lazyCount) fail(path string, err error) {
	c.failed++
	if c.why == "" {
		c.why = path + ": " + err.Error()
	}
}

// failures is the line's tail when a removal failed: how many, and the first's reason.
func (c lazyCount) failures() string {
	if c.failed == 0 {
		return ""
	}
	// the line escapes it whole
	return ", " + strconv.Itoa(c.failed) + " not removed (" + c.why + "); each is not tried again until the member restarts"
}

// launchFiles are what a launch writes beside its directory in the slots (Start): its log,
// its card, its frame and its pid file.
var launchFiles = []string{".native.log", ".card.md", cardcontract.FrameName, ".pid"}

// launchEpochRE is a launch name's epoch (launchName): its last element.
var launchEpochRE = regexp.MustCompile(`\.e([0-9]+)$`)

// oldEntry is one entry of the slots or the results, of a launch of an old epoch.
type oldEntry struct {
	root, name, launch string
	epoch              uint64
	dir                bool
}

// launchEntry parses one entry of the slots or the results (inResults) as a launch's: a
// directory named as a launch (launchDirRE), or, in the slots, a regular file named as a
// launch followed by one of launchFiles. Anything else (a link, another name, another
// kind) does not parse, and is never removed.
func launchEntry(root string, d fs.DirEntry, inResults bool) (oldEntry, bool) {
	name := d.Name()
	launch := ""
	switch {
	case d.Type()&fs.ModeSymlink != 0:
		return oldEntry{}, false
	case d.IsDir():
		launch = name
	case !inResults && d.Type().IsRegular():
		for _, suffix := range launchFiles {
			if n, ok := strings.CutSuffix(name, suffix); ok {
				launch = n
				break
			}
		}
	}
	if launch == "" || !safepath.NameOK(launch) || !launchDirRE.MatchString(launch) {
		return oldEntry{}, false
	}
	m := launchEpochRE.FindStringSubmatch(launch)
	epoch, err := strconv.ParseUint(m[1], 10, 64)
	if err != nil {
		return oldEntry{}, false // an epoch past uint64 is no launch's
	}
	return oldEntry{root: root, name: name, launch: launch, epoch: epoch, dir: d.IsDir()}, true
}

// cleanOld removes up to lazyRound entries of the slots and the results whose launch is of
// an epoch older than the current one minus one (keepEpochs), oldest epoch first, and
// counts what it removed, the bytes freed and the old entries left. Nothing is removed
// before a pass has read the epoch, while a tagged launch waits, or of a launch that is
// running, claimed, or active in the last leftoverIdle (removeOld).
func (r *nativeRunner) cleanOld(now time.Time) (c lazyCount) {
	cur := r.epoch.Load()
	if cur == 0 {
		return c // no pass has read the sprint's epoch yet
	}
	cur--
	var old []oldEntry
	for _, root := range []string{r.slots, r.resultsRoot} {
		if root == "" || r.oldFailed[root] {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			r.noteOldFailed(root)
			c.fail(root, err)
			continue
		}
		for _, d := range entries {
			e, ok := launchEntry(root, d, root == r.resultsRoot && root != r.slots)
			if !ok || e.epoch+keepEpochs > cur || r.oldFailed[filepath.Join(root, e.name)] {
				continue
			}
			old = append(old, e)
		}
	}
	sort.SliceStable(old, func(i, j int) bool { return old[i].epoch < old[j].epoch })
	for _, e := range old {
		if c.removed+c.failed >= lazyRound || r.urgent() {
			c.left++
			continue
		}
		path := filepath.Join(e.root, e.name)
		gone, freed, err := r.removeOld(e, now)
		switch {
		case err != nil:
			r.noteOldFailed(path)
			c.fail(path, err)
			c.left++
		case gone:
			c.removed++
			c.freed += freed
		default:
			c.left++ // running, claimed, or active: a later round
		}
	}
	return c
}

// noteOldFailed marks a path whose listing or removal failed: said once, never tried again
// by this process, so a round does not repeat the same failure every lazyEvery.
func (r *nativeRunner) noteOldFailed(path string) {
	if r.oldFailed == nil {
		r.oldFailed = map[string]bool{}
	}
	r.oldFailed[path] = true
}

// removeOld removes one old entry unless its launch is live (claimed by Start, or ended
// and not yet cleaned), its pid file names a live process, or it was active within
// leftoverIdle; gone says it was removed (or was already gone). It holds removing, as
// removeEnded does, so a start's claim of the name and the removal exclude each other. A
// directory goes as clean removes one (removeLaunch: by name, never through a link); a file
// only when it is still a regular file, never through a link (safepath.RemoveUnder).
func (r *nativeRunner) removeOld(e oldEntry, now time.Time) (gone bool, freed int64, err error) {
	r.removing.Lock()
	defer r.removing.Unlock()
	r.mu.Lock()
	live := r.live[e.launch]
	r.mu.Unlock()
	if live || livePID(filepath.Join(r.slots, e.launch+".pid")) > 0 || now.Sub(lastActivity(r.slots, e.launch)) < leftoverIdle {
		return false, 0, nil
	}
	path := filepath.Join(e.root, e.name)
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return true, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	switch {
	case e.dir && fi.IsDir():
		freed = treeSize(path)
		if e.root == r.slots {
			err = r.removeLaunch(e.name)
		} else {
			err = safepath.RemoveUnderRoots(path, e.root)
		}
	case !e.dir && fi.Mode().IsRegular():
		freed = fi.Size()
		err = safepath.RemoveUnder(e.root, path)
	default:
		return false, 0, fmt.Errorf("%s changed kind since it was listed; it is left alone", path)
	}
	if err != nil {
		return false, 0, err
	}
	r.mu.Lock()
	delete(r.kept, e.launch)
	r.mu.Unlock()
	return true, freed, nil
}

// treeSize is the bytes of the regular files under dir, links not followed; what cannot be
// read counts nothing (the size is the line's report, never a decision).
func treeSize(dir string) (n int64) {
	if err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, ierr := d.Info(); ierr == nil {
				n += fi.Size()
			}
		}
		return nil // an unreadable part is skipped, never the end of the count
	}); err != nil {
		return 0 // unreachable while the walk is never stopped; no size is better than a wrong one
	}
	return n
}

// sizeWord is a byte count as a person reads it.
func sizeWord(n int64) string {
	switch {
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/gib)
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
