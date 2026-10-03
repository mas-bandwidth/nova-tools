// Package gocache holds a Go build cache under a size: least recently used entries go
// first, never one used in the last Recent, never anything that is not a cache entry.
//
// Go itself removes an entry only after five days unused, with no bound on size; a day of
// sprint cards grew one shared cache past 50 GiB, and a friend's per-job caches reached
// 9.3 GiB (ideas#833). Two callers hold a cache with it: the member's lazy round
// (cmd/nova-swarm/lazyclean.go), a few subdirectories at a time on its own clock, and
// nova-sprint friend clean (Hold), once a night over a friend's whole cache.
package gocache

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

const gib = 1 << 30

// Limit is the size a cache is held under (the owner's order of magnitude, 10 GiB); once
// over it, a trim removes until it is Slack under it, so it does not trim again at once.
const (
	Limit int64 = 10 * gib
	Slack int64 = 2 * gib
)

// Recent is how recently used an entry is never removed. Go marks an entry used by setting
// its modification time, and only when that is over an hour old (the go command's cache
// package, its mtimeInterval), so an entry whose time is two hours old has not been used in
// the last hour: a build that just looked it up never finds it gone.
const Recent = 2 * time.Hour

// Subdirs is the number of subdirectories of a Go build cache: two hex digits.
const Subdirs = 256

// entryRE is an entry of a Go build cache: an action (-a) or an output (-d), named by its
// hash (the go command's cache package). Anything else in the cache is never removed or
// counted.
var entryRE = regexp.MustCompile(`^[0-9a-f]{64}-[ad]$`)

// Bounds are a trim's limits. Dirs is how many subdirectories a round reads, Remove the
// most entries it removes; Dry counts what a round would remove and removes nothing.
type Bounds struct {
	Limit, Slack int64
	Dirs, Remove int
	Dry          bool
}

// Count is what one round did: entries removed (or, dry, that would be), removals failed
// (Why the first one's path and reason), the bytes freed and the cache's measured size.
type Count struct {
	Removed, Failed int
	Freed, Size     int64
	Why             string
}

func (c *Count) fail(path string, err error) {
	c.Failed++
	if c.Why == "" {
		c.Why = path + ": " + err.Error()
	}
}

// Trim is the running measure of a cache and its trim. sizes and hours are each
// subdirectory's bytes, in all and by the hour (unix) of each entry's last use, as last
// read; measured counts the subdirectories read at least once (they are read in order from
// 00, so the first 256 reads measure them all); over is set when the measured size passed
// the limit and cleared when it fell to the limit less the slack.
type Trim struct {
	sizes    [Subdirs]int64
	hours    [Subdirs]map[int64]int64
	measured int
	next     int
	over     bool
	failed   map[string]bool // paths a read or a removal failed on: said once, not tried again
}

// Hold measures the whole cache in one round and trims it in a second: a one-shot caller's
// trim, Dirs ignored.
func Hold(dir string, now time.Time, b Bounds) Count {
	var t Trim
	b.Dirs = Subdirs
	t.Round(dir, now, b)
	return t.Round(dir, now, b)
}

// Round reads b.Dirs of the cache's subdirectories, the next ones in turn, and, once the
// whole cache has been measured and its size is over the limit, removes from those
// subdirectories the entries last used before the cutoff (cutoff) and over Recent ago, at
// most b.Remove a round. Oldest first, to the hour: Go records a use to the hour (Recent),
// so an entry's time is no finer than that. A missing entry is a cache miss that Go
// rebuilds, so removing an unused one costs at most a rebuild.
func (t *Trim) Round(dir string, now time.Time, b Bounds) (c Count) {
	cutoff := int64(math.MinInt64) // nothing is old enough until the cache is measured and over
	if t.measured >= Subdirs {
		total := t.Total()
		switch {
		case total > b.Limit:
			t.over = true
		case total <= b.Limit-b.Slack:
			t.over = false
		}
		if t.over {
			cutoff = t.cutoff(total - (b.Limit - b.Slack))
		}
	}
	for range b.Dirs {
		i := t.next
		t.next = (t.next + 1) % Subdirs
		size, hours := int64(0), map[int64]int64{}
		sub := filepath.Join(dir, fmt.Sprintf("%02x", i))
		entries, err := os.ReadDir(sub)
		if err != nil && !os.IsNotExist(err) && !t.failed[sub] {
			t.noteFailed(sub)
			c.fail(sub, err)
		}
		for _, d := range entries {
			if !d.Type().IsRegular() || !entryRE.MatchString(d.Name()) {
				continue
			}
			fi, err := d.Info()
			if err != nil {
				continue // gone since the listing (Go's own trim, or a rename into place)
			}
			at := fi.ModTime()
			hour := at.Unix() / 3600
			path := filepath.Join(sub, d.Name())
			if hour < cutoff && now.Sub(at) >= Recent && c.Removed < b.Remove && !t.failed[path] {
				if b.Dry {
					c.Removed++
					c.Freed += fi.Size()
					continue
				}
				if err := safepath.RemoveUnder(dir, path); err != nil {
					t.noteFailed(path)
					c.fail(path, err)
				} else {
					c.Removed++
					c.Freed += fi.Size()
					continue
				}
			}
			size += fi.Size()
			hours[hour] += fi.Size()
		}
		t.sizes[i], t.hours[i] = size, hours
		if t.measured < Subdirs {
			t.measured++
		}
	}
	c.Size = t.Total()
	return c
}

// noteFailed marks a path a read or a removal failed on.
func (t *Trim) noteFailed(path string) {
	if t.failed == nil {
		t.failed = map[string]bool{}
	}
	t.failed[path] = true
}

// Total is the cache's measured size: the sum of its subdirectories' last reads.
func (t *Trim) Total() (n int64) {
	for _, s := range t.sizes {
		n += s
	}
	return n
}

// cutoff is the hour before which every entry goes to free need bytes: the oldest hours'
// bytes summed until they reach need.
func (t *Trim) cutoff(need int64) int64 {
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
