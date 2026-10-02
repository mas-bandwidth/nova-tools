package main

import (
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// THE CLEANER (docs/SPEC-SWARM.md, `member`). One goroutine, for as long as the process
// lives, and never the member's pass: it retires the launches the pass tags (slotclean.go,
// Ended), and with none waiting does a bounded round of lazy work every lazyEvery: the sweep
// of launches the queue no longer holds, the done entries past their day, the cap over the
// slots directory (slotclean.go), and the build cache every launch of this loop shares
// (GOCACHE, nativeChildEnv), held under cacheLimit, least recently used entries first, never
// one used in the last hour (trimCache; Go itself removes an entry only after five days
// unused, with no bound on size, and a day of cards grew one past 50 GiB).

// cleanQueue is how many tagged launches wait for the cleaner at once.
const cleanQueue = 1024

// lazyRound is the most launches or entries one lazy job removes a round. It bounds a round
// by count, not by time: an entry may be a whole launch directory, and how long its removal
// takes is not measured here. A round stops partway when a launch is tagged.
const lazyRound = 32

// lazyEvery is how often the cleaner, with no tagged launch to retire, runs a lazy round: a
// backlog of a few thousand entries is gone within minutes, and an idle round is a directory
// listing or two.
const lazyEvery = 2 * time.Second

// The build cache's bound. cacheLimit is the size the shared cache is held under (the
// owner's order of magnitude, 10 GiB; a day of cards grew one past 50); once over it, the
// trim removes until it is cacheSlack under it, so it does not trim again every round.
const (
	cacheLimit int64 = 10 * gib
	cacheSlack int64 = 2 * gib
)

// cacheRecent is how recently used an entry of the build cache is never removed. Go marks
// an entry used by setting its modification time, and only when that is over an hour old
// (the go command's cache package, its mtimeInterval), so an entry whose time is two hours old has not
// been used in the last hour: a build that just looked it up never finds it gone.
const cacheRecent = 2 * time.Hour

// cacheDirsPerRound is how many of the cache's 256 subdirectories a round reads (measures,
// and trims when over): the size is never measured by walking the whole cache at once, but
// as a running sum, each subdirectory's part re-read once every 64 rounds. cacheRemovePerRound
// is the most entries one round removes.
const (
	cacheDirsPerRound   = 4
	cacheRemovePerRound = 256
)

// cacheSubdirs is the number of subdirectories of a Go build cache: two hex digits.
const cacheSubdirs = 256

// cleaner starts the runner's queue of launches to retire and the one goroutine that works
// it, for as long as the process lives; with no tagged launch waiting, every lazyEvery it
// does a bounded round of its lazy work (lazy).
func (r *nativeRunner) cleaner() {
	r.tagged = make(chan string, cleanQueue)
	go func() {
		lazy := time.NewTicker(lazyEvery)
		for {
			select {
			case name := <-r.tagged:
				r.retire(name, time.Now())
			case now := <-lazy.C:
				r.lazy(now)
			}
		}
	}()
}

// urgent says a tagged launch waits for the cleaner: lazy work yields to it.
func (r *nativeRunner) urgent() bool { return len(r.tagged) > 0 }

// lazy is one round of the cleaner's lazy work, when no tagged launch waits: the sweep, the
// done entries past their day, the cap, then the build cache. Each says one line when it
// removed something.
func (r *nativeRunner) lazy(now time.Time) {
	for _, job := range []func(time.Time){func(now time.Time) { r.sweep(now) }, r.expire, r.capRound} {
		if r.urgent() {
			return
		}
		job(now)
	}
	dir := r.goBuildCache()
	if r.urgent() || dir == "" {
		return
	}
	if c := r.cache.round(dir, now, cacheBounds{limit: cacheLimit, slack: cacheSlack, dirs: cacheDirsPerRound, remove: cacheRemovePerRound}); c.removed > 0 || c.failed > 0 {
		fmt.Fprintf(r.stderr, "CLEAN go build cache: removed %d entries, %s freed, %s now, limit %s%s\n", c.removed,
			oneline.Escape(sizeWord(c.freed)), oneline.Escape(sizeWord(c.size)), oneline.Escape(sizeWord(cacheLimit)), oneline.Escape(c.failures()))
	}
}

// goBuildCache is the build cache this loop's launches share: native's GOCACHE under the
// root (nativeCacheDir, nativeChildEnv); "" with no root.
func (r *nativeRunner) goBuildCache() string {
	if d := nativeCacheDir(nativeRunConfig{root: r.root}); d != "" {
		return filepath.Join(d, "go-build")
	}
	return ""
}

// lazyCount is what one job did: removed, failed (each said once, why the first), the bytes
// freed, and the cache's size.
type lazyCount struct {
	removed, failed int
	freed           int64
	size            int64
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
	return ", " + strconv.Itoa(c.failed) + " not removed (" + c.why + ")"
}

// treeSize is the bytes of the regular files under dir, links not followed; what cannot be
// read counts nothing (the size is the line's report and the cap's measure, never a reason
// to remove a working slot).
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

// cacheBounds are a trim's limits: the constants above in the member, a test's own.
type cacheBounds struct {
	limit, slack int64
	dirs, remove int
}

// cacheTrim is the running measure of a Go build cache and its trim. sizes and hours are
// each subdirectory's bytes, in all and by the hour (unix) of each entry's last use, as
// last read; measured counts the subdirectories read at least once (they are read in order
// from 00, so the first 256 reads measure them all); over is set when the measured size
// passed the limit and cleared when it fell to the limit less the slack.
type cacheTrim struct {
	sizes    [cacheSubdirs]int64
	hours    [cacheSubdirs]map[int64]int64
	measured int
	next     int
	over     bool
	failed   map[string]bool // paths a read or a removal failed on: said once, not tried again
}

// cacheEntryRE is an entry of a Go build cache: an action (-a) or an output (-d), named by
// its hash (the go command's cache package). Anything else in the cache is never removed or counted.
var cacheEntryRE = regexp.MustCompile(`^[0-9a-f]{64}-[ad]$`)

// round reads b.dirs of the cache's subdirectories, the next ones in turn, and, once the
// whole cache has been measured and its size is over the limit, removes from those
// subdirectories the entries last used before the cutoff (cutoff) and over cacheRecent ago,
// at most b.remove a round. Oldest first, to the hour: Go records a use to the hour
// (cacheRecent), so an entry's time is no finer than that. A missing entry is a cache miss
// that Go rebuilds, so removing an unused one costs at most a rebuild.
func (t *cacheTrim) round(dir string, now time.Time, b cacheBounds) (c lazyCount) {
	cutoff := int64(math.MinInt64) // nothing is old enough until the cache is measured and over
	if t.measured >= cacheSubdirs {
		total := t.total()
		switch {
		case total > b.limit:
			t.over = true
		case total <= b.limit-b.slack:
			t.over = false
		}
		if t.over {
			cutoff = t.cutoff(total - (b.limit - b.slack))
		}
	}
	for range b.dirs {
		i := t.next
		t.next = (t.next + 1) % cacheSubdirs
		size, hours := int64(0), map[int64]int64{}
		sub := filepath.Join(dir, fmt.Sprintf("%02x", i))
		entries, err := os.ReadDir(sub)
		if err != nil && !os.IsNotExist(err) && !t.failed[sub] {
			t.noteFailed(sub)
			c.fail(sub, err)
		}
		for _, d := range entries {
			if !d.Type().IsRegular() || !cacheEntryRE.MatchString(d.Name()) {
				continue
			}
			fi, err := d.Info()
			if err != nil {
				continue // gone since the listing (Go's own trim, or a rename into place)
			}
			at := fi.ModTime()
			hour := at.Unix() / 3600
			path := filepath.Join(sub, d.Name())
			if hour < cutoff && now.Sub(at) >= cacheRecent && c.removed < b.remove && !t.failed[path] {
				if err := safepath.RemoveUnder(dir, path); err != nil {
					t.noteFailed(path)
					c.fail(path, err)
				} else {
					c.removed++
					c.freed += fi.Size()
					continue
				}
			}
			size += fi.Size()
			hours[hour] += fi.Size()
		}
		t.sizes[i], t.hours[i] = size, hours
		if t.measured < cacheSubdirs {
			t.measured++
		}
	}
	c.size = t.total()
	return c
}

// noteFailed marks a path a read or a removal failed on.
func (t *cacheTrim) noteFailed(path string) {
	if t.failed == nil {
		t.failed = map[string]bool{}
	}
	t.failed[path] = true
}

// total is the cache's measured size: the sum of its subdirectories' last reads.
func (t *cacheTrim) total() (n int64) {
	for _, s := range t.sizes {
		n += s
	}
	return n
}

// cutoff is the hour before which every entry goes to free need bytes: the oldest hours'
// bytes summed until they reach need.
func (t *cacheTrim) cutoff(need int64) int64 {
	all := map[int64]int64{}
	for _, h := range t.hours {
		for hour, n := range h {
			all[hour] += n
		}
	}
	hours := make([]int64, 0, len(all))
	for hour := range all {
		hours = append(hours, hour)
	}
	sort.Slice(hours, func(i, j int) bool { return hours[i] < hours[j] })
	var sum int64
	for _, hour := range hours {
		sum += all[hour]
		if sum >= need {
			return hour + 1
		}
	}
	if len(hours) == 0 {
		return math.MinInt64
	}
	return hours[len(hours)-1] + 1
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
