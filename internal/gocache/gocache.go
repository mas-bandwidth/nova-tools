// Package gocache holds a Go build cache under a size: least recently used entries go
// first, never one used in the last Recent, never anything that is not a cache entry.
//
// Go itself removes an entry only after five days unused, with no bound on size; a day of
// sprint cards grew one shared cache past 50 GiB, and a friend's per-job caches reached
// 9.3 GiB (ideas#833). Two callers hold a cache with it: the member's lazy round
// (cmd/nova-worker/lazyclean.go), a few subdirectories at a time on its own clock, and
// nova-sprint friend clean (Hold), once a night over a friend's whole cache.
package gocache

import (
	"fmt"
	"maps"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

const gib = 1 << 30

// Limit is the size a cache is held under by default; once over it, a trim removes, oldest
// first, down to the low-water mark Slack under it (a fifth: 80% of the limit), and then
// removes nothing until the size passes the limit again. It was 10 GiB until 2026-10-04,
// when a busy 24-slot member wrote 13-14 GiB in three hours: every entry was under three
// hours old and the trim removed entries running builds still read, every round, and those
// builds failed (could not import ... go-build/...-d: no such file or directory). A busy
// machine names its own (nova-worker member --gocache-limit, disk-guard --cache-max-gb).
const (
	Limit int64 = 20 * gib
	Slack int64 = Limit / 5
)

// SlackOf is the default slack of a limit: a fifth, so a trim stops at 80% of it.
func SlackOf(limit int64) int64 { return limit / 5 }

// Recent is the default floor (Bounds.Floor): how recently used an entry is never removed,
// whatever the size. Go marks an entry used by setting
// its modification time, and only when that is over an hour old (the go command's cache
// package, its mtimeInterval), so an entry whose time is two hours old has not been used in
// the last hour: a build that just looked it up never finds it gone.
const Recent = 2 * time.Hour

// SayEvery is how often a cache over its limit with every entry younger than the floor is
// said (Count.InUse): once an hour, never once a round.
const SayEvery = time.Hour

// Subdirs is the number of subdirectories of a Go build cache: two hex digits.
const Subdirs = 256

// entryRE is an entry of a Go build cache: an action (-a) or an output (-d), named by its
// hash (the go command's cache package). Anything else in the cache is never removed or
// counted.
var entryRE = regexp.MustCompile(`^[0-9a-f]{64}-[ad]$`)

// Bounds are a trim's limits. Floor is how recently used an entry is never removed (0:
// Recent); Dirs is how many subdirectories a round reads, Remove the most entries it
// removes; Dry counts what a round would remove and removes nothing.
type Bounds struct {
	Limit, Slack int64
	Floor        time.Duration
	Dirs, Remove int
	Dry          bool
}

// floor is the bounds' floor, Recent when none is named.
func (b Bounds) floor() time.Duration {
	if b.Floor > 0 {
		return b.Floor
	}
	return Recent
}

// Count is what one round did: entries removed (or, dry, that would be), removals failed
// (Why the first one's path and reason), the bytes freed and the cache's measured size.
// InUse says the cache is over its limit and every entry is younger than the floor, so
// nothing was removed: the limit is under the machine's working set. It is set at most once
// every SayEvery, so its caller says it then and never once a round.
type Count struct {
	Removed, Failed int
	Freed, Size     int64
	Why             string
	InUse           bool
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
	saidUse  time.Time       // when a round last set InUse: once every SayEvery
}

// Hold measures the whole cache in one round and trims it in a second: a one-shot caller's
// trim, Dirs ignored. The measurement round runs first, over every subdirectory; the trim round
// re-reads them and suppresses re-reporting a failure the measurement already recorded
// (`t.failed[sub]`). That would swallow a shard that could not be read, so Hold carries the
// measurement round's failure count and first diagnostic into the returned Count. Removed and
// Freed still come from the trim round alone (the measurement round removes nothing: cutoff is
// math.MinInt64 until the cache is measured), and Size is the final measurement (docs/STANDARD.md,
// the silent rule; SPEC-CI.md, `silent`).
func Hold(dir string, now time.Time, b Bounds) Count {
	var t Trim
	b.Dirs = Subdirs
	measured := t.Round(dir, now, b)
	trim := t.Round(dir, now, b)
	trim.Failed += measured.Failed
	if measured.Why != "" {
		trim.Why = measured.Why
	}
	return trim
}

// Round reads b.Dirs of the cache's subdirectories, the next ones in turn, and, once the
// whole cache has been measured and its size is over the limit, removes from those
// subdirectories the entries last used before the cutoff (cutoff) and at least the floor
// ago (b.Floor, else Recent), at most b.Remove a round, until the size is down to the
// low-water mark (the limit less the slack); then nothing until it passes the limit again.
// Oldest first, to the hour: Go records a use to the hour (Recent), so an entry's time is no
// finer than that. A missing entry is a cache miss that Go rebuilds, so removing an unused
// one costs at most a rebuild; removing one a running build still reads fails that build,
// so no entry younger than the floor is ever removed, whatever the size. A cache over its
// limit with every entry younger than the floor loses nothing, and the round says so
// (InUse) at most once every SayEvery.
func (t *Trim) Round(dir string, now time.Time, b Bounds) (c Count) {
	floor := b.floor()
	cutoff := int64(math.MinInt64) // nothing is old enough until the cache is measured and over
	if t.measured >= Subdirs {
		total := t.Total()
		switch {
		case total > b.Limit:
			t.over = true
		case total <= b.Limit-b.Slack:
			t.over = false
		}
		switch {
		case !t.over:
		case !t.anyPast(now.Add(-floor)):
			if t.saidUse.IsZero() || now.Sub(t.saidUse) >= SayEvery {
				t.saidUse = now
				c.InUse = true
			}
		default:
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
			if hour < cutoff && now.Sub(at) >= floor && c.Removed < b.Remove && !t.failed[path] {
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

// anyPast says some measured entry may have been last used at or before at: an hour of the
// measure that begins no later than at. None means every entry is younger than the floor.
func (t *Trim) anyPast(at time.Time) bool {
	for _, h := range t.hours {
		for hour, n := range h {
			if n > 0 && hour*3600 <= at.Unix() {
				return true
			}
		}
	}
	return false
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
	hours := slices.Sorted(maps.Keys(all))
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
