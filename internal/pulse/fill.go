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
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/metrics"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// FillCap is the most cards one bench may take in a tick: fill-loop.sh holds this reserve
// back so a filling bench never eats the machine its own CI needs. It is the one number:
// the live loop uses it (#1483), the verb's --fill-cap flag defaults to it, and an unset
// FillInput.FillCap takes it (#2908).
const FillCap = 60

// FillInterval is how often the loop ticks when --once is absent (fill-loop.sh's sleep 300).
const FillInterval = 300 * time.Second

// CardLauncher launches one card that Fill has already moved into --launched. It is the
// per-card seam of the launch verb (internal/pulse/launch.go): one call, in order, one
// card. The real one shells flash-native-bench.sh on the bench; tests inject a recorder.
//
// seat is the bench's nova-secrets seat, read from the machines registry's seat column
// (#2014). It is passed rather than derived: `swarm-`+bench was right for six benches out of
// nine and wrong for the two that mattered most.
type CardLauncher interface {
	Launch(bench, seat, card string) error
}

// Capacity answers how many cards the named bench can take this tick -- card 9316's
// formula, the min of core, disk and memory headroom. The real one runs it over ssh; tests
// inject a fixed number.
type Capacity interface {
	Capacity(bench string) (int, error)
}

// BuildReader answers what build is installed on a machine. It is half of what makes a
// certificate current -- `nova-update release adopt` changes it, and a certificate written
// before an adopt must not survive it -- so the fill READS it rather than assuming, once per
// machine per tick. The real one is one ssh; tests inject a table.
type BuildReader interface {
	Build(machine string) (string, error)
}

// refuseNonBenches holds every named bench against the registry BEFORE the first tick, so
// a fill naming a runner host launches nothing at all rather than launching what it can and
// refusing the rest. Every refused name gets its own line: a person who typed two wrong
// names learns both at once. A shared-without-note row is not this: fill disables that
// bench per tick and keeps dealing to its neighbours (#2031).
func refuseNonBenches(stderr io.Writer, reg *fleet.Registry, benches []string) int {
	code := 0
	for _, bench := range benches {
		var r *fleet.Refusal
		if err := reg.RequireBench(bench); errors.As(err, &r) {
			if r.Reason == fleet.ReasonSharedWithoutNote {
				continue
			}
			fmt.Fprintln(stderr, r.Line("FILL"))
			code = 2
		}
	}
	return code
}

// dropLockFailed takes the poisoned shared rows out of the deal list so they do not appear
// as a filling bench. The DISABLED line is how they are named.
func dropLockFailed(reg *fleet.Registry, benches []string) []string {
	if len(reg.LockFailed()) == 0 {
		return benches
	}
	failed := make(map[string]bool, len(reg.LockFailed()))
	for _, m := range reg.LockFailed() {
		failed[m.Name] = true
	}
	var keep []string
	for _, b := range benches {
		if !failed[b] {
			keep = append(keep, b)
		}
	}
	return keep
}

// printDisabledLock names every row that failed the runner/bench lock, once per tick.
func printDisabledLock(stderr io.Writer, reg *fleet.Registry) {
	for _, m := range reg.LockFailed() {
		r := reg.SharedLockRefusal(m)
		fmt.Fprintf(stderr, "FILL DISABLED bench=%s reason=%s remedy=%s\n",
			oneline.Field(r.Name), oneline.Field(r.Reason), oneline.Quote(r.Remedy))
	}
}

// guardedCapacity is the capacity seam with the registry in front of it: a capacity probe
// is an ssh to the machine, which is load, which is exactly what a runner host may not take.
type guardedCapacity struct {
	reg  *fleet.Registry
	next Capacity
}

func (g guardedCapacity) Capacity(bench string) (int, error) {
	if err := g.reg.RequireBench(bench); err != nil {
		return 0, err
	}
	return g.next.Capacity(bench)
}

// guardedLauncher is the launch seam with the registry in front of it: the last gate a card
// passes before it lands on a machine.
type guardedLauncher struct {
	reg  *fleet.Registry
	next CardLauncher
}

func (g guardedLauncher) Launch(bench, seat, card string) error {
	if err := g.reg.RequireBench(bench); err != nil {
		return err
	}
	return g.next.Launch(bench, seat, card)
}

// benchSeats resolves every named bench's seat from the registry ONCE, before the first card
// is dealt (#2014). A bench whose row names no seat is refused by name and dropped from the
// pool: its neighbours keep working, because one incomplete row is not a reason to stop a
// fleet, and the refusal is printed here -- once -- rather than once per card.
func benchSeats(stderr io.Writer, reg *fleet.Registry, benches []string) (map[string]string, []string) {
	seats := make(map[string]string, len(benches))
	kept := make([]string, 0, len(benches))
	for _, bench := range benches {
		seat := ""
		if m, ok := reg.Lookup(bench); ok {
			seat = strings.TrimSpace(m.Seat)
		}
		if seat == "" {
			fmt.Fprintf(stderr, "FILL REFUSED bench=%s reason=no-seat remedy=%q\n",
				field(bench), fmt.Sprintf(
					"write the machine's nova-secrets seat into the seat column of %s; a card runs under a seat and fill will not invent one (swarm-<bench> is a guess, and it is the guess that killed every card on the Studio)",
					reg.Path()))
			continue
		}
		// The registry validates every other column and not this one, and the seat becomes
		// a filename in the secrets store (<seat>.yaml, <seat>.key) and an argument to the
		// launcher. A row that names something else is refused here rather than passed on.
		if !plainSeat(seat) {
			fmt.Fprintf(stderr, "FILL REFUSED bench=%s reason=seat-not-a-name seat=%s remedy=%q\n",
				field(bench), field(seat), fmt.Sprintf(
					"a seat is one plain name -- no space, no path separator, not `.` or `..` -- because it names a file in the secrets store; fix the seat column of %s",
					reg.Path()))
			continue
		}
		seats[bench] = seat
		kept = append(kept, bench)
	}
	return seats, kept
}

// plainSeat says whether a seat is one plain name: the stem of a file in the secrets store,
// and nothing that could reach out of it or split into two arguments.
func plainSeat(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	if strings.ContainsAny(s, `/\`) {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	})
}

// FillInput is the fill verb apart from flag parsing, so a test drives one tick with fake
// directories and stub seams.
type FillInput struct {
	Ready      string            // the queue/ready directory the card-<n>.md are popped from
	Launched   string            // the queue/launched directory they are moved into; its cards are live
	Markers    string            // where .failed-<n>/.refused-<k> markers live; "" is <ready>-markers, never inside --ready
	Lanes      string            // the lanes file: <name>\t<path prefixes> per line; empty names no lane
	Machines   string            // the machines registry; a bench whose roles lack `bench` is refused
	Queue      string            // the queue directory whose .lock this fill takes; empty is --launched's parent
	Repo       string            // repo root for git commands (default: ".")
	Base       string            // base branch to check dependencies against (default: "dev")
	ResultsDir string            // results store root (default: ~/nova-bench/results)
	Checker    DependencyChecker // optional dependency checker seam for testing
	Session    string            // the session id stamped into every launched card's marker
	Benches    []string          // the benches to fill, in order
	Only       []string          // glob patterns over a card's filename; empty takes every ready card
	Once       bool              // true runs exactly one tick and returns
	Interval   time.Duration     // how long between ticks; 0 takes FillInterval
	Stop       string            // touch this file to stop the loop; empty names no stop file
	Stdout     io.Writer
	Stderr     io.Writer
	Now        func() time.Time
	Sleep      func(time.Duration)
	Launcher   CardLauncher
	Capacity   Capacity
	// FillCap is the most cards one bench may take in a tick; 0 takes the FillCap constant.
	// The --fill-cap flag defaults to the FillCap constant, so the verb, the loop and an
	// unset field agree on one number (#1483, #2908).
	FillCap int
	// Metrics receives the fill's queue depth, launched cards and launcher latency each
	// tick (nx-g61); nil exports nothing.
	Metrics *metrics.Set
	// Locked says this fill runs inside a caller that already holds the queue's lock (the
	// `loop` verb, so it takes none of its own.
	Locked bool
	// Force says to re-execute a card even if its content key already has a DONE result.
	// Active attempts retain their lease and fence and are never duplicated, even with Force.
	Force bool
	// BaseSHA is the base commit SHA for card content keys; empty defaults to repo HEAD or dev.
	BaseSHA string
	// Done is the queue/done directory where cached/completed cards are moved; empty is <queue>/done.
	Done string
	// KeysFile optionally specifies the path to keys.tsv directly; empty uses <queue>/keys.tsv.
	KeysFile string
	// KeyStore optionally overrides the key store; nil uses NewKeyStore(fillQueue(in)).
	KeyStore *KeyStore
	// CERTIFICATION. Certs names the certificates file `nova-pulse fleet certify` writes,
	// Hash is the standard hash the fleet is held to now, and Build reads what each machine
	// is running. With all three, a card whose workload class has no current certificate on
	// the bench it was dealt is REFUSED and stays ready. Without them the gate is off, which
	// is the documented narrowing for a loop that has not adopted certification yet -- and
	// exactly the state hulk was in when its first Go card of 2026-09-18 died inside the
	// wall on a toolchain the wall could not read.
	Certs string
	Hash  string
	Build BuildReader
}

// Fill holds the loop: one fillTick per bench set, one FILL line per tick, until killed --
// or exactly one tick when --once is set. It returns 0, 1 when every bench failed the tick
// (a fleet nobody can reach is not a quiet success), or 2 on a refusal that never started.
func Fill(in FillInput) int {
	if in.Stdout == nil {
		in.Stdout = io.Discard
	}
	if in.Stderr == nil {
		in.Stderr = io.Discard
	}
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	if in.Sleep == nil {
		in.Sleep = time.Sleep
	}
	if in.Interval <= 0 {
		in.Interval = FillInterval
	}
	for _, r := range []struct{ v, name, wants string }{
		{in.Ready, "ready", "the directory holding the card-<n>.md ready to launch"},
		{in.Launched, "launched", "the directory the launched cards are moved into"},
	} {
		if strings.TrimSpace(r.v) == "" {
			return refusal(in.Stderr, "FILL", fmt.Errorf("missing --%s; refusing to guess (%s)", r.name, r.wants))
		}
	}
	if in.Launcher == nil {
		return refusal(in.Stderr, "FILL", fmt.Errorf("missing a launcher; refusing to guess (inject a pulse.CardLauncher)"))
	}
	if in.Capacity == nil {
		return refusal(in.Stderr, "FILL", fmt.Errorf("missing a capacity reader; refusing to guess (inject a pulse.Capacity)"))
	}
	// The registry is not optional. Without it the verb cannot tell a bench from a CI
	// runner host, and the one thing it must never do is guess that.
	if strings.TrimSpace(in.Machines) == "" {
		return refusal(in.Stderr, "FILL", fmt.Errorf(
			"missing --machines; refusing to guess (the machines registry says which hosts are benches and which serve the merge group's shards: queue/control/machines.tsv)"))
	}
	reg, err := fleet.ReadRegistry(in.Machines)
	if err != nil {
		return refusal(in.Stderr, "FILL", err)
	}
	// THE POOL (#1476): with no bench named, the tick fills every certified bench the
	// registry carries. The pool was a Go literal -- `hulk, vision, space` -- so a bench
	// certified last night was not filled until someone edited a tool and cut a release.
	// Now the row IS the enrolment, and the tool holds no fleet name at all.
	named := len(in.Benches) > 0
	if !named {
		in.Benches = reg.CertifiedBenchNames()
	}
	in.Benches = dropLockFailed(reg, in.Benches)
	if code := refuseNonBenches(in.Stderr, reg, in.Benches); code != 0 {
		return code
	}
	if len(in.Benches) == 0 {
		printDisabledLock(in.Stderr, reg)
		if named {
			return refusal(in.Stderr, "FILL", fmt.Errorf(
				"every named bench is disabled by the runner/bench lock; add `allow-shared=<YYYY-MM-DD> <why>` to the shared row's notes, or name a bench that may take a card"))
		}
		return refusal(in.Stderr, "FILL", fmt.Errorf(
			"%s names no certified bench, so the fill pool is empty; certify the bench and write the day into its notes (`certified=<YYYY-MM-DD> <the report it was certified by>`), or name one with --bench",
			in.Machines))
	}
	// THE SEAT COMES FROM THE ROW (#2014), resolved once, here, before a card moves.
	seats, seated := benchSeats(in.Stderr, reg, in.Benches)
	if len(seated) == 0 {
		return refusal(in.Stderr, "FILL", fmt.Errorf(
			"no named bench carries a seat in %s, so there is nothing to fill; every bench a card may run on needs its nova-secrets seat in the registry's seat column",
			oneline.Field(in.Machines)))
	}
	in.Benches = seated
	// Belt and braces: even a bench that passed the list check is asked again at the
	// moment the card, or the capacity probe, would reach the machine.
	in.Capacity = guardedCapacity{reg: reg, next: in.Capacity}
	in.Launcher = guardedLauncher{reg: reg, next: in.Launcher}
	for _, dir := range []string{in.Ready, in.Launched} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return refusal(in.Stderr, "FILL", fmt.Errorf("cannot open %s: %s (name a writable directory)", oneline.Field(dir), oneline.Err(err)))
		}
	}
	// ONE WRITER PER QUEUE (queuelock.go). A fill moves cards and writes the markers that
	// hold a lane, so a second one on the same queue is a race over both.
	if !in.Locked {
		lock, err := LockQueue(fillQueue(in), "fill")
		if err != nil {
			return refusal(in.Stderr, "FILL", err)
		}
		defer lock.Release()
	}

	for tick := 1; ; tick++ {
		// THE STOP FILE, checked before a card is claimed and never in the middle of a
		// tick: the tick in flight finishes, no further card is claimed, and every card
		// already live on a bench keeps running. A kill would land between the move out of
		// --ready and the launcher, leaving a card under --launched that nobody started.
		if stopped(in.Stop) {
			fmt.Fprintf(in.Stdout, "FILL STOP tick=%d file=%s live=%d note=%q\n",
				tick, oneline.Field(in.Stop), len(readyCards(in.Launched)),
				"no new launches; the live cards are untouched and nothing was killed")
			return 0
		}
		printDisabledLock(in.Stderr, reg)
		lines, res := fillTick(in, seats, tick)
		for _, line := range lines {
			fmt.Fprintln(in.Stdout, line)
		}
		if res.err != nil {
			fmt.Fprintf(in.Stderr, "FILL NOTE tick=%d: %s\n", tick, oneline.Err(res.err))
		}
		if in.Once {
			if res.allBenchesFailed() {
				return 1
			}
			break
		}
		in.Sleep(in.Interval)
	}
	return 0
}

// tickResult is what one tick answers apart from its lines: how many benches it tried, how
// many of them could not be filled at all, and the first thing that went wrong.
type tickResult struct {
	benches int
	failed  int
	err     error
}

// allBenchesFailed says whether the tick reached no bench at all: every named bench either
// refused its capacity read or failed every launch it attempted. That is a red fleet, and a
// red fleet is exit 1 -- the dogfood edge where a whole tick of `exit status 255` still
// answered 0.
func (r tickResult) allBenchesFailed() bool { return r.benches > 0 && r.failed == r.benches }

// fillTick is one turn: reap the markers nobody is waiting on, list ready once in filename
// order, read each bench's capacity once, then deal one card per bench in turn until every
// bench is at its cap or the pool is empty. A LANE card is launched only when its lane has
// no live card; otherwise it is held, and the live card it is held behind is named. A
// LANE the lanes file does not name is refused, once per card per lanes-file mtime. The
// move out of ready is the claim, so a card another hand already took is skipped and never
// launched twice; a launcher that fails moves its card back and releases its lane. It
// returns the FILL line first, then one FILL HELD line per held card, then the FILL REAPED
// line when the tick took stale markers away.
func fillTick(in FillInput, seats map[string]string, tick int) ([]string, tickResult) {
	reaped := reapMarkers(in)
	// Dependency and priority order first (SortQueueCards), then fair share across
	// lanes: the interleave keeps each lane's sorted order, and CheckCardDependencies
	// still holds any card whose prerequisite has not landed.
	lastServed, hasCursor := readLaneCursor(in)
	cards := fairShareLaneCards(SortQueueCards(selectedCards(readyCards(in.Ready), in.Only)), lastServed, hasCursor)
	lanes := laneTable(in.Lanes)
	live := liveLanes(in.Launched)
	gate := newCertifyGate(in)
	idx := 0
	res := tickResult{benches: len(in.Benches)}
	var held []string

	checker := in.Checker
	if checker == nil {
		checker = NewGitAndResultsChecker(in.Repo, in.Base, in.ResultsDir)
	}

	if strays := strayCards(in.Ready); len(strays) > 0 {
		fmt.Fprintf(in.Stderr, "FILL REFUSED ready=%s file=%s more=%d remedy=%q\n",
			oneline.Field(in.Ready), oneline.Field(filepath.Base(strays[0])), len(strays)-1,
			"fill reads card-<n>.md and nothing else; rename it, or cut it with nova-pulse cut")
	}

	// ONE capacity read per bench per tick, before any card is dealt: the probe is an ssh
	// to the machine, so a read inside the round-robin would multiply the calls by the
	// pool. want[i] is bench i's remaining cards, clamped by FillCap and at zero.
	fillCap := in.FillCap
	if fillCap <= 0 {
		fillCap = FillCap
	}

	want := make([]int, len(in.Benches))
	capacityFailed := make([]bool, len(in.Benches))
	for i, bench := range in.Benches {
		if n, err := in.Capacity.Capacity(bench); err != nil {
			capacityFailed[i] = true
			// FAIL CLOSED, AND SAY SO, PER BENCH. A probe or parse failure is zero free
			// slots on THAT bench -- never a deal, never a fall-through -- and every
			// failing bench gets its own line: the tick used to keep only the first
			// reason, so a fleet nobody could read named one machine and went quiet.
			fmt.Fprintf(in.Stderr, "FILL UNREADABLE bench=%s free=0 reason=%s\n",
				field(bench), oneline.Err(err))
			if res.err == nil {
				res.err = fmt.Errorf("capacity on %s: %w", field(bench), err)
			}
		} else {
			want[i] = n
		}
		if want[i] > fillCap {
			want[i] = fillCap
		}
		if want[i] < 0 {
			want[i] = 0
		}
	}

	ks := in.KeyStore
	if ks == nil {
		if in.KeysFile != "" {
			ks = NewKeyStoreFromFile(in.KeysFile)
		} else {
			ks = NewKeyStore(fillQueue(in))
		}
	}

	// Round-robin: one card per bench in turn, passes repeat until every bench is at its
	// capacity or the pool is empty. A card skipped as unknown or held consumes the card
	// but not the bench's want, so the bench is offered the next pass rather than dropped
	// -- a run of held cards does not end the tick for a bench.
	launched := make([]int, len(in.Benches))
	failed := make([]int, len(in.Benches))
	for {
		progressed := false
		for i, bench := range in.Benches {
			if want[i] == 0 || idx >= len(cards) {
				continue
			}
			progressed = true
			card := cards[idx]
			idx++

			// Check dependencies before admitting/launching (Essential 3 from Issue #2437)
			if unmetDep, reason, ok := CheckCardDependencies(card, checker); !ok {
				held = append(held, fmt.Sprintf("FILL HELD card=%s depends-on=%s reason=%s",
					oneline.Field(filepath.Base(card)), oneline.Field(unmetDep), oneline.Field(reason)))
				continue
			}
			base := filepath.Base(card)

			// Content key check (Cards v2 #2507): resume by content key.
			// The queue refuses to deal a card whose key already has a DONE result unless --force;
			// active/live attempts retain their lease and fence and CANNOT be duplicated, including with --force.
			var currentCK CardKey
			if ks != nil {
				if raw, err := os.ReadFile(card); err == nil {
					baseSHA := in.BaseSHA
					if baseSHA == "" {
						baseSHA = ExtractCardBase(string(raw))
					}
					lane := cardLane(card)
					currentCK = ComputeCardKey(string(raw), baseSHA, lane)
					decision, rec := ks.CheckKey(currentCK, in.Force)
					switch decision {
					case DecisionActive:
						held = append(held, fmt.Sprintf("FILL ACTIVE card=%s key=%s attempt=%s note=%q",
							oneline.Field(base), oneline.Field(currentCK.String()),
							oneline.Field(rec.AttemptID), "active attempt retains lease and fence; cannot duplicate"))
						continue
					case DecisionCached:
						prStr := "-"
						if rec != nil && rec.PR > 0 {
							prStr = strconv.Itoa(rec.PR)
						}
						held = append(held, fmt.Sprintf("FILL CACHED card=%s pr=%s key=%s",
							oneline.Field(base), prStr, oneline.Field(currentCK.String())))
						doneDir := in.Done
						if doneDir == "" {
							doneDir = filepath.Join(fillQueue(in), "done")
						}
						_ = os.MkdirAll(doneDir, 0o755)
						_ = os.Rename(card, filepath.Join(doneDir, base))
						continue
					case DecisionOlderBase:
						held = append(held, fmt.Sprintf("FILL BASE-MOVED card=%s prior-base=%s current-base=%s key=%s note=%q",
							oneline.Field(base), oneline.Field(rec.Base), oneline.Field(currentCK.BaseSHA),
							oneline.Field(currentCK.String()), "done at older base; needs rebase or re-run"))
						continue
					}
				}
			}

			lane := cardLane(card)
			if lane != "" {
				if _, known := lanes[lane]; !known {
					refuseLane(in, card, lane)
					continue
				}
				if holder, isLive := live[lane]; isLive {
					held = append(held, fmt.Sprintf("FILL HELD card=%s lane=%s live=%s",
						oneline.Field(filepath.Base(card)), oneline.Field(lane), oneline.Field(holder)))
					continue
				}
			}
			// A card goes to a machine that has PROVED it can do that kind of work, or it
			// does not go. The card stays READY, so the remedy is run and the same card is
			// dealt again rather than lost.
			if !gate.allows(in.Stderr, bench, card) {
				continue
			}
			moved := filepath.Join(in.Launched, base)
			if err := os.Rename(card, moved); err != nil {
				// Another tick or another hand took it first: the card is in exactly
				// one place at every moment, and a card is never launched twice.
				continue
			}
			writeLaunchedMarker(in, moved, base, lane, bench)
			if lane != "" {
				live[lane] = base
			}
			want[i]--
			// Wall time, not in.Now: the command line pins Now to the moment it started.
			start := time.Now()
			err := in.Launcher.Launch(bench, seats[bench], moved)
			in.Metrics.ProviderLatency(metrics.Fill, bench, time.Since(start))
			if err != nil {
				failed[i]++
				failLaunch(in, moved, base, lane, live, err)
				if res.err == nil {
					res.err = fmt.Errorf("launch %s on %s: %w", field(base), field(bench), err)
				}
				continue
			}
			launched[i]++
			// The card ran: whatever it failed at before is history, not queue depth (#2013).
			reapCardMarkers(in, base)
			writeLaneCursor(in, lane)
		}
		if !progressed {
			break
		}
	}

	parts := make([]string, 0, len(in.Benches))
	for i, bench := range in.Benches {
		if capacityFailed[i] || (launched[i] == 0 && failed[i] > 0) {
			res.failed++
		}
		parts = append(parts, fmt.Sprintf("%s:launched=%d,failed=%d",
			oneline.Field(bench), launched[i], failed[i]))
	}

	var b strings.Builder
	b.WriteString("FILL tick=")
	b.WriteString(strconv.Itoa(tick))
	for _, p := range parts {
		b.WriteByte(' ')
		b.WriteString(p)
	}
	// ready= is CARDS, never files: the directory holds card-<n>.md and the markers live
	// somewhere else, so the depth a reader acts on is the depth of the queue (#2013).
	depth := len(readyCards(in.Ready))
	in.Metrics.QueueDepth(metrics.Fill, depth)
	in.Metrics.LeasesHeld(metrics.Fill, len(readyCards(in.Launched)))
	b.WriteString(" ready=")
	b.WriteString(strconv.Itoa(depth))
	lines := append([]string{b.String()}, held...)
	if reaped > 0 {
		lines = append(lines, fmt.Sprintf("FILL REAPED tick=%d markers=%d dir=%s note=%q",
			tick, reaped, oneline.Field(markersDir(in)),
			"markers whose card has left the queue; a marker is a record of a failure, not a card"))
	}
	return lines, res
}

// failLaunch is what a launcher's failure costs: the card goes back to --ready, its lane is
// released so the lane is not held by a card that never ran, and a `.failed-<n>` marker in
// the markers directory carries the attempt number and the reason. The card is ready again
// on the next tick, and the markers are the count of how often it has failed.
func failLaunch(in FillInput, moved, base, lane string, live map[string]string, cause error) {
	if lane != "" && live[lane] == base {
		delete(live, lane)
	}
	// The marker is what holds the lane, so it goes with the card: a marker left beside a
	// card that went back to --ready holds a lane nobody is running.
	_ = os.Remove(launchedMarker(in.Launched, base))
	back := filepath.Join(in.Ready, base)
	if err := os.Rename(moved, back); err != nil {
		// The card could not be put back; it stays under --launched rather than
		// vanishing, and the FILL NOTE already carries the launcher's own reason.
		return
	}
	dir := markersDir(in)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	n := len(failedMarkers(dir, base)) + 1
	line := fmt.Sprintf("%s\tattempt=%d\t%s\n",
		in.Now().UTC().Format(time.RFC3339), n, oneline.Cap(oneline.Err(cause), oneline.TailBytes))
	_ = os.WriteFile(marker(dir, base, "failed", strconv.Itoa(n)), []byte(line), 0o644)
}

// refuseLane prints one FILL REFUSED line for a lane the lanes file does not name -- once
// per card per lanes-file mtime. The marker in the markers directory is the memory: a
// second tick over the same unchanged lanes file says nothing, and a lanes file that was
// edited (a new mtime, so a new marker name) speaks again, because the answer may have
// changed.
func refuseLane(in FillInput, card, lane string) {
	base := filepath.Base(card)
	dir := markersDir(in)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	key := strconv.FormatInt(lanesStamp(in.Lanes), 10)
	path := marker(dir, base, "refused", key)
	if _, err := os.Stat(path); err == nil {
		return
	}
	for _, stale := range refusedMarkers(dir, base) {
		if stale != path {
			_ = os.Remove(stale)
		}
	}
	_ = os.WriteFile(path, []byte(lane+"\n"), 0o644)
	fmt.Fprintf(in.Stderr, "FILL REFUSED card=%s lane=%s remedy=%q\n",
		oneline.Field(base), oneline.Field(lane),
		fmt.Sprintf("add the lane to %s or drop the LANE line", in.Lanes))
}

// marker is the path of one marker: <card>.<kind>-<key>. It is never a card-<n>.md, so a
// glob over the queue steps past it -- and since #2013 it is not in the queue at all.
func marker(dir, base, kind, key string) string {
	return filepath.Join(dir, base+"."+kind+"-"+key)
}

func failedMarkers(dir, base string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, base+".failed-*"))
	return m
}

func refusedMarkers(dir, base string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, base+".refused-*"))
	return m
}

// markerKinds is every marker fill writes for a card: an attempt that failed, and a lane
// refusal it has already printed once.
var markerKinds = []string{"failed", "refused"}

// markersDir is where the markers live: FillInput.Markers, or a directory beside --ready
// named for it. Never --ready itself (#2013). A ready directory holds cards, and the whole
// fleet reads its depth: 1,275 markers in it on the night of the load test made every
// counter say the benches were full while they idled.
func markersDir(in FillInput) string {
	if s := strings.TrimSpace(in.Markers); s != "" {
		return s
	}
	clean := filepath.Clean(in.Ready)
	return filepath.Join(filepath.Dir(clean), filepath.Base(clean)+"-markers")
}

// markerCard is the card a marker belongs to, or "" when the name is not a marker.
func markerCard(name string) string {
	for _, kind := range markerKinds {
		if base, _, ok := strings.Cut(name, "."+kind+"-"); ok {
			return base
		}
	}
	return ""
}

// markersInDir lists every marker of every kind in one directory.
func markersInDir(dir string) []string {
	var out []string
	for _, kind := range markerKinds {
		m, _ := filepath.Glob(filepath.Join(dir, "*."+kind+"-*"))
		out = append(out, m...)
	}
	sort.Strings(out)
	return out
}

// reapCardMarkers takes every marker belonging to one card, wherever it lies: the card has
// relaunched, so what it failed at before is history. The ready directory is swept too,
// because every marker written before #2013 is sitting in one right now.
func reapCardMarkers(in FillInput, base string) {
	for _, dir := range []string{markersDir(in), in.Ready} {
		for _, kind := range markerKinds {
			m, _ := filepath.Glob(filepath.Join(dir, base+"."+kind+"-*"))
			for _, p := range m {
				_ = os.Remove(p)
			}
		}
	}
}

// reapMarkers is the top of every tick: markers written before this rule are moved out of
// --ready into the markers directory, keeping the attempt count they carry, and then every
// marker whose card is no longer ready is removed. It answers how many it removed.
//
// A marker is a record of a failure that the NEXT attempt will read. Once the card is gone
// -- harvested, withdrawn, relaunched, moved by a hand -- nobody will read it again, and it
// is only something for a counter to trip over.
func reapMarkers(in FillInput) int {
	dir := markersDir(in)
	if stray := markersInDir(in.Ready); len(stray) > 0 {
		if err := os.MkdirAll(dir, 0o755); err == nil {
			for _, p := range stray {
				if os.Rename(p, filepath.Join(dir, filepath.Base(p))) != nil {
					_ = os.Remove(p)
				}
			}
		}
	}
	ready := map[string]bool{}
	for _, card := range readyCards(in.Ready) {
		ready[filepath.Base(card)] = true
	}
	n := 0
	for _, p := range markersInDir(dir) {
		if card := markerCard(filepath.Base(p)); card != "" && !ready[card] {
			if os.Remove(p) == nil {
				n++
			}
		}
	}
	return n
}

// lanesStamp is the lanes file's modification time in whole seconds, or 0 when there is no
// lanes file: the key a refusal is remembered under, so an edited table is a new answer.
func lanesStamp(path string) int64 {
	if strings.TrimSpace(path) == "" {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.ModTime().Unix()
}

// certifyGate is the fill's half of Glenn's certification directive of 2026-09-18: a card
// may not be launched onto a machine that has not proved it can do that kind of work.
//
// It is built once per tick and answers per card. The machine's build is read at most once
// per tick per machine, and the certificates file at most once per tick, because the gate
// must cost one ssh and one read whatever the queue's depth is.
type certifyGate struct {
	certs  []fleet.Certificate
	hash   string
	build  BuildReader
	on     bool
	err    error
	builds map[string]string
	said   map[string]bool
}

func newCertifyGate(in FillInput) *certifyGate {
	g := &certifyGate{
		hash: in.Hash, build: in.Build,
		builds: map[string]string{}, said: map[string]bool{},
	}
	if strings.TrimSpace(in.Certs) == "" || in.Build == nil || strings.TrimSpace(in.Hash) == "" {
		return g
	}
	g.on = true
	g.certs, g.err = fleet.ReadCertificates(in.Certs)
	return g
}

// allows answers whether this card may be launched on this machine, and prints the refusal
// once per machine and class -- a queue of forty Go cards against an uncertified bench is
// one line, not forty.
func (g *certifyGate) allows(stderr io.Writer, machine, card string) bool {
	if !g.on {
		return true
	}
	class := cardWorkload(card)
	if g.certified(machine, class) {
		return true
	}
	key := machine + "\x00" + class
	if !g.said[key] {
		g.said[key] = true
		fmt.Fprintf(stderr, "FILL REFUSED bench=%s reason=uncertified workload=%s remedy=%s\n",
			oneline.Field(machine), oneline.Field(class),
			oneline.Quote("nova-pulse fleet certify --machine "+machine))
	}
	return false
}

func (g *certifyGate) certified(machine, class string) bool {
	if g.err != nil {
		return false
	}
	build, read := g.builds[machine]
	if !read {
		b, err := g.build.Build(machine)
		if err != nil {
			b = ""
		}
		g.builds[machine] = b
		build = b
	}
	if build == "" {
		return false
	}
	return fleet.Certified(g.certs, machine, class, build, g.hash)
}

// cardWorkload is the workload class a card's work belongs to. The card may say so itself
// with `workload: <class>`; otherwise it is decided by the language the card names, and a
// card that names no language at all is Go, which is what all but a handful of them are.
func cardWorkload(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return DefaultWorkload
	}
	lang := ""
	for _, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(t, "workload:"); ok {
			if class := strings.TrimSpace(v); class != "" {
				return class
			}
		}
		if lang != "" {
			continue
		}
		for _, key := range []string{"LANG:", "LEG:"} {
			if v, ok := strings.CutPrefix(t, key); ok {
				lang = strings.ToLower(strings.TrimSpace(v))
			}
		}
	}
	if class, ok := workloadByLanguage[lang]; ok {
		return class
	}
	return DefaultWorkload
}

// DefaultWorkload is the class a card with no `workload:` and no language line belongs to.
const DefaultWorkload = "go-test"

// workloadByLanguage maps a card's LANG or LEG line to the workload class that certifies a
// bench for it. A language with no entry falls back to DefaultWorkload rather than to no
// gate at all: an unknown language on an uncertified bench is still an uncertified bench.
var workloadByLanguage = map[string]string{
	"go":     "go-test",
	"golang": "go-test",
	"c":      "c-build",
	"cpp":    "cpp-build",
	"c++":    "cpp-build",
	"lisp":   "sbcl",
	"sbcl":   "sbcl",
}

// cardLane reads a card's `LANE: <name>` line, or "" when it names none. Only the exact
// field prefix counts: a `LANES:` line is prose, not a lane.
func cardLane(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "LANE:"); ok {
			if lane := strings.TrimSpace(v); lane != "" {
				return lane
			}
		}
	}
	return ""
}

// laneTable reads a lanes file: `<name>\t<path prefixes>` per line, `#` a comment and a
// blank line skipped. Only the name is needed here; the prefixes are the area the lane
// serializes. A missing file is an empty table, so a card naming a lane is then refused.
func laneTable(path string) map[string]bool {
	out := map[string]bool{}
	if strings.TrimSpace(path) == "" {
		return out
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, _, _ := strings.Cut(line, "\t")
		if name = strings.TrimSpace(name); name != "" {
			out[name] = true
		}
	}
	return out
}

// liveLanes reads the lane of every card already under --launched: the launched directory
// is the live set, and a live card's lane is the one its launched marker names. The marker
// is the record of what the card TOOK, so the lane is released by name rather than by
// parsing the card again -- a worker that rewrote its card, or a hand that edited it, does
// not move a lane that is already held. A card with no marker (one launched before markers,
// or moved in by hand) falls back to its own LANE: line, exactly as before.
func liveLanes(launched string) map[string]string {
	out := map[string]string{}
	for _, card := range readyCards(launched) {
		base := filepath.Base(card)
		lane := readLaunchedMarker(launched, base)["lane"]
		if lane == "" {
			lane = cardLane(card)
		}
		if lane != "" {
			out[lane] = base
		}
	}
	return out
}

// launchedMarker is the path of a launched card's marker: <card>.launched, beside the card
// under --launched. It is never a card-<n>.md, so every glob over the queue steps past it.
func launchedMarker(dir, base string) string {
	return filepath.Join(dir, base+".launched")
}

// writeLaunchedMarker records what the card took the moment it became live: the lane it
// holds, the bench it went to, its label and the session that cut it. `harvest` reads this
// to release the lane and to know whose job it is looking at.
func writeLaunchedMarker(in FillInput, moved, base, lane, bench string) {
	now := in.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	body := fmt.Sprintf("lane=%s\nbench=%s\nlabel=%s\nsession=%s\ncard=%s\nat=%s\n",
		lane, bench, strings.TrimSuffix(base, ".md"), in.Session, base,
		now().UTC().Format(time.RFC3339))
	if deps := CardDependencies(moved); len(deps) > 0 {
		body += fmt.Sprintf("depends-on=%s\n", strings.Join(deps, ","))
	}
	_ = os.WriteFile(launchedMarker(in.Launched, base), []byte(body), 0o644)
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

// stopped says whether the stop file is there. No --stop names no stop file, and a file
// that is not there is a loop that runs: the check is a stat and nothing else, so a
// resident loop pays one syscall a tick for a control that never needs a signal.
func stopped(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// fillQueue is the directory whose lock this fill takes: --queue when it is named, else the
// parent of --launched, which is the queue's own layout (queue/launched, queue/ready). It is
// the layout and not a guess about meaning: a fill that moves queue/ready/card-9.md into
// queue/launched/ is writing that queue, whatever the caller calls it.
func fillQueue(in FillInput) string {
	if q := strings.TrimSpace(in.Queue); q != "" {
		return q
	}
	return filepath.Dir(strings.TrimRight(in.Launched, string(os.PathSeparator)))
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

// selectedCards keeps the ready cards this run is allowed to launch. A ready directory is
// shared: another line's cards sit in it, and a fill with no filter launched them on its own
// benches (dogfood, 2026-09-18 -- card-9382 and a card on a live merge lane). --only is the
// whitelist, one or more glob patterns matched against the card's filename, with or without
// the card- prefix and the .md suffix, so `--only 96*` and `--only card-9601.md` both name
// the same card. No --only is every card, as before. A card nobody selected is left in
// ready, untouched and unrefused: it is not this run's to judge.
func selectedCards(cards, patterns []string) []string {
	if len(patterns) == 0 {
		return cards
	}
	var out []string
	for _, card := range cards {
		base := filepath.Base(card)
		name := strings.TrimSuffix(base, ".md")
		bare := strings.TrimPrefix(name, "card-")
		for _, p := range patterns {
			if matched(p, base) || matched(p, name) || matched(p, bare) {
				out = append(out, card)
				break
			}
		}
	}
	return out
}

func matched(pattern, s string) bool {
	ok, err := filepath.Match(pattern, s)
	return err == nil && ok
}

// strayCards lists the .md files in a ready directory that readyCards would step over: a
// card by any other name is not a card, and the tick says so rather than leaving it to sit
// there unread. Markers are not .md and never count.
func strayCards(dir string) []string {
	all, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	known := map[string]bool{}
	for _, c := range readyCards(dir) {
		known[c] = true
	}
	var out []string
	for _, p := range all {
		if !known[p] {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// laneCursorPath is where the multi-tick lane round-robin cursor is persisted.
// Persisting in in.Launched as a hidden file (.lane-cursor) ensures fair-share progress survives
// process restarts and single-tick invocations (--once) on constrained fleets,
// without polluting the markers directory or queue card globs.
func laneCursorPath(in FillInput) string {
	return filepath.Join(in.Launched, ".lane-cursor")
}

// writeLaneCursor records the last successfully launched lane.
func writeLaneCursor(in FillInput, lane string) {
	if strings.TrimSpace(in.Launched) == "" {
		return
	}
	_ = os.MkdirAll(in.Launched, 0o755)
	_ = os.WriteFile(laneCursorPath(in), []byte(lane+"\n"), 0o644)
}

// readLaneCursor returns the last successfully launched lane, or false if unset.
func readLaneCursor(in FillInput) (string, bool) {
	if strings.TrimSpace(in.Launched) == "" {
		return "", false
	}
	raw, err := os.ReadFile(laneCursorPath(in))
	if err != nil {
		return "", false
	}
	line := strings.TrimSuffix(string(raw), "\n")
	return line, true
}

// rotateLanes orders active lanes so that lanes after the last-served lane
// in round-robin sequence come first. This guarantees multi-tick fair progress
// under constrained capacity (e.g. 1 slot per tick) so late-alphabetical lanes
// (e.g. "zebra") are never starved by arriving unlaned cards or earlier lanes.
func rotateLanes(laneNames []string, lastServed string, hasCursor bool) []string {
	if len(laneNames) <= 1 || !hasCursor {
		return laneNames
	}
	var after, before []string
	for _, lane := range laneNames {
		if lane > lastServed {
			after = append(after, lane)
		} else {
			before = append(before, lane)
		}
	}
	return append(after, before...)
}

// fairShareLaneCards interleaves pending cards across active lanes round-robin,
// starting after the last-served lane when hasCursor is true.
// Within each lane, the original FIFO card order is preserved.
// This prevents lane starvation both within a single tick and across successive
// capacity-constrained ticks.
func fairShareLaneCards(cards []string, lastServed string, hasCursor bool) []string {
	if len(cards) <= 1 {
		return cards
	}
	laneCards := make(map[string][]string)
	var laneNames []string
	for _, card := range cards {
		lane := cardLane(card)
		if len(laneCards[lane]) == 0 {
			laneNames = append(laneNames, lane)
		}
		laneCards[lane] = append(laneCards[lane], card)
	}
	if len(laneNames) <= 1 {
		return cards
	}
	sort.Strings(laneNames)
	laneNames = rotateLanes(laneNames, lastServed, hasCursor)

	out := make([]string, 0, len(cards))
	for {
		progressed := false
		for _, lane := range laneNames {
			q := laneCards[lane]
			if len(q) > 0 {
				out = append(out, q[0])
				laneCards[lane] = q[1:]
				progressed = true
			}
		}
		if !progressed {
			break
		}
	}
	return out
}
