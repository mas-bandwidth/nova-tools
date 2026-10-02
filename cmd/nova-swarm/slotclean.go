package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
)

// THE SLOT'S LIFE (docs/SPEC-SWARM.md, `member`; tla/MemberSlot.tla).
//
// Every card and every read stages a whole job directory under <slots>/<launch> (the staged
// clone, its tmp and its data home, a few hundred MB), and a day of cards left about 100 GB of
// them across the fleet (the owner, 2026-10-02: "cleanup must be auto! Asynchronously."). So
// the member removes each slot itself, apart from its pass, as soon as the sprint has taken
// the launch's word. A slot is in one of these states:
//
//   working        Start claimed the name (live) and the child runs; nothing removes it
//   accepted       the sprint took the finish (ok or failed) or the return, or its queue let
//                  the card go (reaped): the member tags the launch (Ended) and the cleaner
//                  retires it
//   refused        the sprint refused the report: the slot is kept, said once, and the sweep
//                  retires it once the queue no longer holds its card
//   kept           retired: the checkout, tmp, data, pid file and results are gone; what a
//                  later look needs (native.log, card.md, frame.json, RESULT.md: a few KB) sits
//                  under <slots>/done/<launch>/ for doneKeep
//   gone           the done entry went: after doneKeep, or first under the cap
//
// The sweep (sweep) is the cleaner's own: every pass hands the runner the cards the queue
// holds for this member (member.Holder), and a launch directory of this loop's kind the queue
// does not hold, that nothing runs (no claim of this process, no live pid) and that has been
// still for leftoverIdle, is retired the same way: a crash or a kill left it, or its report
// was refused. The cap (capRound): once a capEvery the cleaner measures the slots directory
// and, over --slots-max-gb, removes done entries oldest first until under; a working slot is
// never removed, and while working slots alone are over the cap the member takes no card
// (slotsRoom) until they end and are retired. The shared Go caches under <root>/cache are not
// a slot's and are never removed here (the build cache is trimmed, lazyclean.go).
//
// Removal is by name, one launch name directly under the slots directory, never through a
// link (safepath.RemoveUnderRoots, which, once the path is placed under the slots, makes the
// tree writable without following a link and then removes it: a Go module cache in a job holds
// directories 0555 and files 0444, which a plain recursive remove cannot delete inside).

// doneDir is the directory under the slots that holds what a retired launch leaves.
const doneDir = "done"

// doneKeep is how long a retired launch's done entry is kept before the cleaner removes it.
const doneKeep = 24 * time.Hour

// leftoverIdle is how long a directory this process did not end must have been still before
// the sweep retires it: a launch another member process runs, or one still being started,
// is newer than that.
const leftoverIdle = 10 * time.Minute

// capEvery is how often the cleaner measures the slots directory against the cap: the
// measure walks every working slot, so it is not made every round.
const capEvery = time.Minute

// launchDirRE is a launch's name (launchName): the card, then its generation (a work card) or
// its attempt (a read), then its epoch. workLaunchRE and readLaunchRE are each kind's own.
var (
	launchDirRE  = regexp.MustCompile(`^[A-Za-z0-9._-]+\.[ga][0-9]+\.e[0-9]+$`)
	workLaunchRE = regexp.MustCompile(`^[A-Za-z0-9._-]+\.g[0-9]+\.e[0-9]+$`)
	readLaunchRE = regexp.MustCompile(`^[A-Za-z0-9._-]+\.a[0-9]+\.e[0-9]+$`)
)

// launchFiles are what a launch writes beside its directory in the slots (Start): its log,
// its card, its frame and its pid file. The first three are kept under done/, without the dot.
var launchFiles = []string{".native.log", ".card.md", cardcontract.FrameName, ".pid"}

// Ended is the member done with a launch (member.Ender). Accepted, it is tagged, and the
// cleaner retires it; retiring a slot is long (a staged clone and its caches, made writable
// and then removed), so a runner with a cleaner (cleaner) only tags here, and the member's
// pass never waits on a removal (the owner, 2026-10-01: "You can tag for cleanup, but don't
// do that cleanup inline."); a runner with none (a test's) retires here. Not accepted (the
// sprint refused the report), the slot is kept and said once; the sweep retires it.
func (r *nativeRunner) Ended(p member.Packet, accepted bool) {
	name := launchName(p)
	r.mu.Lock()
	delete(r.live, name)
	r.mu.Unlock()
	if !accepted {
		fmt.Fprintf(r.stderr, "nova-swarm member: NOTE launch %s kept: the sprint refused its report; the sweep retires it once the queue no longer holds its card; run: nova-sprint card %s\n", oneline.Field(name), oneline.Field(p.Primary))
		return
	}
	if r.tagged == nil {
		r.retire(name, time.Now())
		return
	}
	select {
	case r.tagged <- name:
	default:
		// the queue is full: this one is left for the sweep, which retires every launch the
		// queue no longer holds once it has been still for leftoverIdle
	}
}

// Holds records the cards the sprint's queue holds for this member (member.Holder), by launch
// name, for the sweep: a store, never a wait.
func (r *nativeRunner) Holds(cards []member.Packet) {
	held := make(map[string]bool, len(cards))
	for _, p := range cards {
		held[launchName(p)] = true
	}
	r.held.Store(&held)
}

// retire retires one launch unless a launch of that name is live: the member starts a card
// again under the same name when its report was refused and the sprint still has it working,
// and that launch's checkout is never removed under it. The removal and a start's claim of the
// name exclude each other (removing), so one always sees the other.
func (r *nativeRunner) retire(name string, now time.Time) {
	r.removing.Lock()
	defer r.removing.Unlock()
	r.mu.Lock()
	live := r.live[name]
	r.mu.Unlock()
	if live {
		return
	}
	freed, err := r.retireLaunch(name, now)
	if err != nil {
		fmt.Fprintf(r.stderr, "nova-swarm member: NOTE launch %s was not retired: %s\n", oneline.Field(name), oneline.Err(err))
		return
	}
	fmt.Fprintf(r.stderr, "CLEAN retired %s: %s freed; %s/%s kept %s\n", oneline.Field(name), oneline.Escape(sizeWord(freed)), doneDir, oneline.Field(name), doneKeep)
}

// retireLaunch moves what a later look needs under done/<name> (the log, card and frame from
// beside the directory, the newest RESULT.md of its results) and removes the rest: the
// directory, the pid file and the results. The done entry's time is its retirement, which
// the day it is kept runs from. A name that is not a launch's is refused and nothing moves.
func (r *nativeRunner) retireLaunch(name string, now time.Time) (freed int64, err error) {
	if !safepath.NameOK(name) || !launchDirRE.MatchString(name) {
		return 0, fmt.Errorf("%q is not a launch's name (<card>.g<n>.e<n> or <card>.a<n>.e<n>)", name)
	}
	done := filepath.Join(r.slots, doneDir, name)
	if err := os.MkdirAll(done, 0o755); err != nil {
		return 0, err
	}
	for _, suffix := range launchFiles[:3] {
		from := filepath.Join(r.slots, name+suffix)
		if fi, err := os.Lstat(from); err != nil || !fi.Mode().IsRegular() {
			continue // never written, already moved, or not a file (a link is left where it is)
		}
		if err := os.Rename(from, filepath.Join(done, strings.TrimPrefix(suffix, "."))); err != nil {
			return 0, err
		}
	}
	freed = treeSize(filepath.Join(r.slots, name))
	if r.resultsRoot != "" {
		results := filepath.Join(r.resultsRoot, name)
		if from := newestResult(results); from != "" {
			if err := copyRegularFile(from, filepath.Join(done, "RESULT.md")); err != nil {
				return 0, err
			}
		}
		if _, err := os.Lstat(results); err == nil {
			freed += treeSize(results)
			if err := safepath.RemoveUnderRoots(results, r.resultsRoot); err != nil {
				return 0, err
			}
		}
	}
	if err := r.removeLaunch(name); err != nil {
		return 0, err
	}
	// ignored: a pid file names a process that has ended; one left names a dead pid
	_ = safepath.RemoveUnder(r.slots, filepath.Join(r.slots, name+".pid"))
	_ = os.Chtimes(done, now, now) // ignored: the entry is then kept from the file's own time, which is earlier
	return freed, nil
}

// ownRE is the launch names of this loop's kind: a reader judges only reads, a member only
// work cards, so the two sharing one slots directory never sweep each other's launches.
func (r *nativeRunner) ownRE() *regexp.Regexp {
	if r.reader {
		return readLaunchRE
	}
	return workLaunchRE
}

// sweep retires, up to lazyRound a round, every launch of this loop's kind under the slots
// that the queue does not hold (Holds: the card landed, was dropped or was dealt elsewhere; a
// crash or a kill left the slot, or the sprint refused its report), that nothing runs (no
// claim of this process, no live pid) and that has been still for leftoverIdle. Nothing is
// judged before a pass has read the queue; a name that is not a launch's of this kind, and a
// link, are never touched.
func (r *nativeRunner) sweep(now time.Time) (retired int) {
	held := r.held.Load()
	if held == nil {
		return 0
	}
	entries, err := os.ReadDir(r.slots)
	if err != nil {
		r.failOnce(r.slots, err, "the slots directory could not be listed for the sweep")
		return 0
	}
	seen := map[string]bool{}
	for _, d := range entries {
		if retired >= lazyRound || r.urgent() {
			break
		}
		name, ok := r.launchOf(d)
		if !ok || seen[name] || (*held)[name] {
			continue
		}
		seen[name] = true
		r.mu.Lock()
		live := r.live[name]
		r.mu.Unlock()
		if live || livePID(filepath.Join(r.slots, name+".pid")) > 0 || now.Sub(lastActivity(r.slots, name)) < leftoverIdle {
			continue
		}
		fmt.Fprintf(r.stderr, "CLEAN sweep: the queue no longer holds %s and nothing runs it\n", oneline.Field(name))
		r.retire(name, now)
		retired++
	}
	return retired
}

// launchOf is the launch an entry of the slots belongs to, of this loop's kind: a directory
// named as a launch, or a regular file named as a launch followed by one of launchFiles.
// Anything else (a link, another name, another kind) is nobody's launch.
func (r *nativeRunner) launchOf(d os.DirEntry) (string, bool) {
	name := d.Name()
	switch {
	case d.IsDir():
	case d.Type().IsRegular():
		launch := ""
		for _, suffix := range launchFiles {
			if n, ok := strings.CutSuffix(name, suffix); ok {
				launch = n
				break
			}
		}
		name = launch
	default:
		return "", false
	}
	return name, name != "" && safepath.NameOK(name) && r.ownRE().MatchString(name)
}

// expire removes, up to lazyRound a round, every done entry retired doneKeep ago, and says
// so in one line when it removed something. Anything under done/ that is not a launch's
// entry is never touched.
func (r *nativeRunner) expire(now time.Time) {
	var c lazyCount
	for _, e := range r.doneEntries() {
		if now.Sub(e.at) < doneKeep {
			continue
		}
		if c.removed+c.failed >= lazyRound || r.urgent() {
			break
		}
		c.remove(r, e)
	}
	if c.removed > 0 || c.failed > 0 {
		fmt.Fprintf(r.stderr, "CLEAN done: removed %d entries kept past %s, %s freed%s\n", c.removed, doneKeep, oneline.Escape(sizeWord(c.freed)), oneline.Escape(c.failures()))
	}
}

// doneEntry is one retired launch's entry under done/, with its retirement time.
type doneEntry struct {
	name string
	at   time.Time
}

// doneEntries is every launch's entry under done/, oldest first; none when there is no done
// directory yet.
func (r *nativeRunner) doneEntries() []doneEntry {
	entries, err := os.ReadDir(filepath.Join(r.slots, doneDir))
	if err != nil {
		return nil
	}
	var all []doneEntry
	for _, d := range entries {
		if !d.IsDir() || !launchDirRE.MatchString(d.Name()) {
			continue
		}
		if fi, err := d.Info(); err == nil {
			all = append(all, doneEntry{name: d.Name(), at: fi.ModTime()})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
	return all
}

// remove removes one done entry and counts it.
func (c *lazyCount) remove(r *nativeRunner, e doneEntry) {
	path := filepath.Join(r.slots, doneDir, e.name)
	n := treeSize(path)
	if err := safepath.RemoveUnderRoots(path, r.slots); err != nil {
		c.fail(path, err)
		return
	}
	c.removed++
	c.freed += n
}

// capRound measures the slots directory once a capEvery and holds it under the cap (cap; 0
// is none): over it, done entries go oldest first until under. What is left over the cap is
// working slots, which are never removed: full is set, and the member's room (slotsRoom)
// says no until they end and are retired. One line says what the round found when it
// removed something or the answer changed.
func (r *nativeRunner) capRound(now time.Time) {
	if r.cap <= 0 || (!r.measuredAt.IsZero() && now.Sub(r.measuredAt) < capEvery) {
		return
	}
	r.measuredAt = now
	size := treeSize(r.slots)
	var c lazyCount
	for _, e := range r.doneEntries() {
		if size <= r.cap {
			break
		}
		was := c.freed
		c.remove(r, e)
		size -= c.freed - was
	}
	full := size > r.cap
	wasFull := r.full.Swap(full)
	r.size.Store(size)
	if c.removed > 0 || c.failed > 0 || wasFull != full {
		state := "under"
		if full {
			state = "full"
		}
		fmt.Fprintf(r.stderr, "CLEAN cap: the slots directory holds %s against its cap of %s; removed %d done entries, %s freed%s; %s\n",
			oneline.Escape(sizeWord(size)), oneline.Escape(sizeWord(r.cap)), c.removed, oneline.Escape(sizeWord(c.freed)), oneline.Escape(c.failures()), oneline.Escape(state))
	}
}

// slotsRoom is the member's room under the cap (member.Config.Room): no while the cleaner's
// last measure found the slots directory over the cap with no done entry left to remove.
func (r *nativeRunner) slotsRoom() (bool, string) {
	if r.full.Load() {
		return false, fmt.Sprintf("the slots directory %s holds %s, over its cap of %s (--slots-max-gb): the oldest done entries went first, and what is left is working slots, which are never removed; no card is started until they end and are retired",
			oneline.Field(r.slots), oneline.Escape(sizeWord(r.size.Load())), oneline.Escape(sizeWord(r.cap)))
	}
	return true, fmt.Sprintf("the slots directory %s holds %s, under its cap of %s", oneline.Field(r.slots), oneline.Escape(sizeWord(r.size.Load())), oneline.Escape(sizeWord(r.cap)))
}

// slotsCap is the cap in bytes: --slots-max-gb's GiB, or, at 0, a tenth of the volume the
// slots sit on (the owner, 2026-10-02: never more than a tenth of the volume by default).
func slotsCap(flagGiB int, volume uint64) int64 {
	if flagGiB > 0 {
		return int64(flagGiB) * gib
	}
	return int64(volume / 10)
}

// rooms is one Room of several, asked in turn: the first no is the answer, else the last yes.
func rooms(each ...func() (bool, string)) func() (bool, string) {
	return func() (ok bool, why string) {
		for _, room := range each {
			if ok, why = room(); !ok {
				return false, why
			}
		}
		return true, why
	}
}

// failOnce says a failure of the cleaner's once, and not again until the member restarts, so
// a round does not repeat the same line every lazyEvery.
func (r *nativeRunner) failOnce(path string, err error, what string) {
	if r.failed == nil {
		r.failed = map[string]bool{}
	}
	if r.failed[path] {
		return
	}
	r.failed[path] = true
	fmt.Fprintf(r.stderr, "nova-swarm member: NOTE %s: %s\n", oneline.Escape(what), oneline.Err(err))
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

// gib is one GiB, the unit of --disk-floor and --slots-max-gb.
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
