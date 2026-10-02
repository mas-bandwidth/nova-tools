package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// A FINISHED LAUNCH'S CHECKOUT IS REMOVED (docs/SPEC-SWARM.md, `member`).
//
// Every card and every read stages a whole job directory under <slots>/<launch> (the staged
// clone, its tmp and caches, about 80 MB), and nothing removed one: a 300-card pass with its
// reads wrote about 60 GB in twelve minutes and filled the machine's disk. A launch the member
// is done with (reported, returned or reaped: member.Ender) has its directory removed; the
// small files beside it (<launch>.native.log, .card.md, .frame.json) and the results stay. A
// launch that failed keeps its directory for a person to inspect, the newest keepFailed of
// the pool, oldest removed first. The same rule sweeps the pool when the member starts, for
// directories a crash or a kill left.
//
// The pool may be shared (a member and a reader on one root), so a directory is removed only
// when nothing is running in it: never this process's own running launch, never one whose
// pid file names a live process, and never one another process may still be finishing (any
// activity, the directory's or its native log's, in the last leftoverIdle). Removal is by
// name, one launch name directly under the slots directory, never through a link
// (safepath.RemoveUnderRoots, which, once the path is placed under the slots, makes the tree
// writable without following a link and then removes it: a Go module cache in a job holds
// directories 0555 and files 0444, which a plain recursive remove cannot delete inside).

// keepFailed is how many failed launches' directories a pool keeps for inspection.
const keepFailed = 5

// leftoverIdle is how long a directory this process did not end must have been still before
// it is counted as ended: a launch another member process runs, or one still being started,
// is newer than that.
const leftoverIdle = 10 * time.Minute

// launchDirRE is a launch's name (launchName): the card, then its generation (a read: its
// attempt), then its epoch.
var launchDirRE = regexp.MustCompile(`^[A-Za-z0-9._-]+\.[ga][0-9]+\.e[0-9]+$`)

// Ended is the member done with a launch (member.Ender): it is tagged, and its directory is
// removed by clean: an ok one's at once, a failed one's kept, the pool pruned to the newest
// keepFailed. Removing a job directory is long (a staged clone and its caches, made
// writable and then removed), so a runner with a cleaner (cleaner) only tags here, and the
// member's pass never waits on a removal (the owner, 2026-10-01: "You can tag for cleanup,
// but don't do that cleanup inline."); a runner with none (a test's) cleans here.
func (r *nativeRunner) Ended(p member.Packet, failed bool) {
	name := launchName(p)
	r.mu.Lock()
	delete(r.live, name)
	if failed {
		if r.kept == nil {
			r.kept = map[string]bool{}
		}
		r.kept[name] = true
	}
	r.mu.Unlock()
	if r.tagged == nil {
		r.clean(ended{name: name, failed: failed})
		return
	}
	select {
	case r.tagged <- ended{name: name, failed: failed}:
	default:
		// the queue is full: this one is left for a later prune, which removes every ended
		// directory that has been still for leftoverIdle
	}
}

// ended is one launch tagged for cleaning.
type ended struct {
	name   string
	failed bool
	at     time.Time // prune's: its last activity
}

// cleanQueue is how many tagged launches wait for the cleaner at once.
const cleanQueue = 1024

// cleaner starts the runner's queue of launches to clean and the one goroutine that works
// it, for as long as the process lives; with no tagged launch waiting, every lazyEvery it
// does a bounded round of its lazy work (lazyclean.go).
func (r *nativeRunner) cleaner() {
	r.tagged = make(chan ended, cleanQueue)
	go func() {
		lazy := time.NewTicker(lazyEvery)
		for {
			select {
			case e := <-r.tagged:
				r.clean(e)
			case now := <-lazy.C:
				r.lazy(now)
			}
		}
	}()
}

// clean removes a tagged launch's directory (an ok one's) and prunes the pool.
func (r *nativeRunner) clean(e ended) {
	if !e.failed {
		if err := r.removeEnded(e.name); err != nil {
			fmt.Fprintf(r.stderr, "nova-swarm member: NOTE the directory of launch %s was not removed: %s\n", oneline.Field(e.name), oneline.Err(err))
		}
	}
	r.prune(time.Now())
}

// removeEnded removes a launch's directory unless a launch of that name is live: the member
// starts a card again under the same name when its report was refused and the sprint still
// has it working, and that launch's checkout is never removed under it. The removal and a
// start's claim of the name exclude each other (removing), so one always sees the other.
func (r *nativeRunner) removeEnded(name string) error {
	r.removing.Lock()
	defer r.removing.Unlock()
	r.mu.Lock()
	live := r.live[name]
	r.mu.Unlock()
	if live {
		return nil
	}
	return r.removeLaunch(name)
}

// prune removes every ended launch directory under the slots but the newest keepFailed
// (by last activity), and returns how many it removed and kept.
func (r *nativeRunner) prune(now time.Time) (removed, kept int) {
	entries, err := os.ReadDir(r.slots)
	if err != nil {
		fmt.Fprintf(r.stderr, "nova-swarm member: NOTE the slots directory could not be listed to remove ended launches: %s\n", oneline.Err(err))
		return 0, 0
	}
	var all []ended
	for _, e := range entries {
		name := e.Name()
		// a link is never a launch directory: DirEntry.IsDir is false for one
		if !e.IsDir() || !launchDirRE.MatchString(name) {
			continue
		}
		r.mu.Lock()
		live, kept := r.live[name], r.kept[name]
		r.mu.Unlock()
		if live || livePID(filepath.Join(r.slots, name+".pid")) > 0 {
			continue
		}
		at := lastActivity(r.slots, name)
		if !kept && now.Sub(at) < leftoverIdle {
			continue // not this process's to judge yet: still starting, or another's still finishing
		}
		all = append(all, ended{name: name, at: at})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at.After(all[j].at) })
	for i, e := range all {
		if i < keepFailed {
			kept++
			continue
		}
		if err := r.removeEnded(e.name); err != nil {
			fmt.Fprintf(r.stderr, "nova-swarm member: NOTE the directory of ended launch %s was not removed: %s\n", oneline.Field(e.name), oneline.Err(err))
			continue
		}

		r.mu.Lock()
		delete(r.kept, e.name)
		r.mu.Unlock()
		removed++
	}
	return removed, kept
}

// lastActivity is the newer of the launch directory's own modification time and its native
// log's (native writes that log until it ends); zero when neither can be read.
func lastActivity(slots, name string) time.Time {
	var at time.Time
	for _, p := range []string{filepath.Join(slots, name), filepath.Join(slots, name+".native.log")} {
		if fi, err := os.Lstat(p); err == nil && fi.ModTime().After(at) {
			at = fi.ModTime()
		}
	}
	return at
}

// removeLaunch removes one launch's directory under the slots, by its name: a name that is
// not a launch's, or a path that is not a directory (a link, a file), is refused and nothing
// is removed. A directory already gone is nothing to remove.
func (r *nativeRunner) removeLaunch(name string) error {
	if !safepath.NameOK(name) || !launchDirRE.MatchString(name) {
		return fmt.Errorf("%q is not a launch directory's name (<card>.g<n>.e<n> or <card>.a<n>.e<n>)", name)
	}
	path := filepath.Join(r.slots, name)
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory (a link or a file); it is never removed", path)
	}
	return safepath.RemoveUnderRoots(path, r.slots)
}

// gib is one GiB, the unit of --disk-floor.
const gib = 1 << 30

// diskRoom is the member's Room (member.Config): the free bytes on the slots' volume against
// the floor, read once a tick before any child is started; a volume whose free bytes cannot
// be read refuses too. free is diskFree, a test's fake.
func diskRoom(slots string, floorGiB int, free func(string) (uint64, error)) func() (bool, string) {
	return func() (bool, string) {
		n, err := free(slots)
		if err != nil {
			return false, fmt.Sprintf("the free disk on the volume of %s could not be read (%s); no card is started until it can; run: df %s", oneline.Field(slots), oneline.Err(err), oneline.Field(slots))
		}
		if n < uint64(floorGiB)*gib {
			return false, fmt.Sprintf("free disk on the volume of %s is %.1f GiB, under the floor of %d GiB; no card is started until it is above; run: free disk on that volume, or start the member with a lower --disk-floor",
				oneline.Field(slots), float64(n)/gib, floorGiB)
		}
		return true, fmt.Sprintf("free disk on the volume of %s is %.1f GiB, above the floor of %d GiB", oneline.Field(slots), float64(n)/gib, floorGiB)
	}
}
