package pulse

// Fill is fill-loop.sh's tick body as one verb (#1142): for each bench, read the bench's
// capacity, cap it at FillCap, pop that many card-<n>.md from --ready in filename order,
// move each to --launched and hand it to the launcher. One FILL line per tick, no model
// call.
//
// A card may name a LANE (`LANE: <name>`), and a lane is a serial queue over one area of
// the codebase: at most one live card per lane at a time. A ready card whose lane already
// has a live card -- one under --launched, or one launched earlier in this tick -- is held
// in order with a FILL HELD line and stays ready. A LANE the lanes file does not name is
// refused with the remedy, once per card per lanes-file mtime: the refusal leaves a marker
// in the markers directory, so a lane nobody has added does not reprint its refusal every
// five minutes, and editing the lanes file makes every refusal speak again. A card with no
// LANE is launched exactly as before.
//
// A launcher that fails is not a card that ran. The card goes back to --ready with a
// `.failed-<n>` marker naming the attempt and the reason, its lane is released, and the
// tick counts it under failed= rather than launched= -- the dogfood edge of 2026-09-18,
// where an exit-7 launcher left a card under --launched holding its lane forever while the
// tick read launched=1. The marker does NOT live in --ready (#2013): a ready directory
// holds cards, so everything that counts it counts cards. Markers live in their own
// directory beside it, they are taken when the card they belong to relaunches, and a
// marker whose card has left the queue is reaped at the top of the tick -- one launcher
// bug on the night of the 2026-09-20 load test left 1,275 of them lying in ready, and
// every counter in the fleet read a queue that was empty as a queue that was full.
//
// WHOM it fills is not a list in this file either: with no bench named, the pool is every
// machine in the registry that carries the `bench` role and a `certified=<YYYY-MM-DD>` note.
// A bench certified tonight is filled tonight, by its row and not by a release.
//
// WHERE a card may go is not the caller's opinion: --machines names the machines registry
// (internal/fleet), and a bench whose roles lack `bench` is refused BY NAME before any ssh
// is opened -- exit 2, nothing launched. That is Glenn's lock of 2026-09-18: runner hosts
// are CI-only, and a card on a machine serving the merge group's shards makes the shard
// slow, the gate red and the queue stop. A row that is both runner and bench without the
// dated allow-shared note is different (#2031): that bench is DISABLED with a named line
// each tick, and every other bench deals. The guard is in three places on purpose: the
// whole bench list is checked before the first tick, and then EVERY capacity read and EVERY
// launch goes through a wrapper that asks the registry again -- so a bench name that arrives
// by some other road later still cannot reach a runner host.
//
// THE SEAT COMES FROM THE ROW (#2014). The launcher's second argument is the bench's
// nova-secrets seat from the machines registry, not `swarm-<bench>`. The Studio's seat is
// `studio` and the Air's is `air`; inventing `swarm-studio` killed every card on the
// strongest bench (SECRETS EXEC FAIL, exit 125) and bounced them back. A bench whose row
// names no seat, or a seat that is not one plain name, is refused once by name at the loop
// and dropped from the pool; a fill left with no seated bench refuses with exit 2.
//
// The two things that touch the world -- the capacity formula on a bench and the per-card
// launch -- are injected seams (Capacity and CardLauncher), so a test drives the whole
// tick against a fake ready directory, a fake clock and a fake launcher. No test opens an
// ssh connection or spawns a process.

import (
	"os"
	"path/filepath"
	"strings"
)

// Capacity answers how many cards the named bench can take this tick -- card 9316's
// formula, the min of core, disk and memory headroom. The real one runs it over ssh; tests
// inject a fixed number.
type Capacity interface {
	Capacity(bench string) (int, error)
}

// marker is the path of one marker: <card>.<kind>-<key>. It is never a card-<n>.md, so a
// glob over the queue steps past it -- and since #2013 it is not in the queue at all.
func marker(dir, base, kind, key string) string {
	return filepath.Join(dir, base+"."+kind+"-"+key)
}

// launchedMarker is the path of a launched card's marker: <card>.launched, beside the card
// under --launched. It is never a card-<n>.md, so every glob over the queue steps past it.
func launchedMarker(dir, base string) string {
	return filepath.Join(dir, base+".launched")
}

// readLaunchedMarker reads one launched marker into its key=value fields. A missing or
// unreadable marker is an empty table, never a guess.
func readLaunchedMarker(dir, base string) map[string]string {
	out := map[string]string{}
	raw, err := os.ReadFile(launchedMarker(dir, base))
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		if k = strings.TrimSpace(k); k != "" {
			out[k] = strings.TrimSpace(v)
		}
	}
	return out
}

// isDir says whether a path is a directory that is there.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// readyCards lists the ready card files in filename order, which is the order ls handed
// fill-loop.sh. The move out of ready is the queue's claim; the glob is a snapshot.
//
// card-<n>.md is the one filename contract of the queue directories, and it is what every
// verb that writes a card writes: `cut` wrote `<label>.md` until 2026-09-18, and a whole
// directory of cut cards sat in --ready that this glob silently stepped over.
func readyCards(dir string) []string {
	cards, _ := filepath.Glob(filepath.Join(dir, "card-*.md"))
	return cards // filepath.Glob returns lexical order
}
