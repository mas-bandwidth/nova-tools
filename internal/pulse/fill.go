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
// beside the card, so a lane nobody has added does not reprint its refusal every five
// minutes, and editing the lanes file makes every refusal speak again. A card with no LANE
// is launched exactly as before.
//
// A launcher that fails is not a card that ran. The card goes back to --ready with a
// `.failed-<n>` marker naming the attempt and the reason, its lane is released, and the
// tick counts it under failed= rather than launched= -- the dogfood edge of 2026-09-18,
// where an exit-7 launcher left a card under --launched holding its lane forever while the
// tick read launched=1.
//
// WHERE a card may go is not the caller's opinion: --machines names the machines registry
// (internal/fleet), and a bench whose roles lack `bench` is refused BY NAME before any ssh
// is opened -- exit 2, nothing launched. That is Glenn's lock of 2026-09-18: runner hosts
// are CI-only, and a card on a machine serving the merge group's shards makes the shard
// slow, the gate red and the queue stop. The guard is in three places on purpose: the whole
// bench list is checked before the first tick, and then EVERY capacity read and EVERY launch
// goes through a wrapper that asks the registry again -- so a bench name that arrives by
// some other road later still cannot reach a runner host.
//
// The two things that touch the world -- the capacity formula on a bench and the per-card
// launch -- are injected seams (Capacity and CardLauncher), so a test drives the whole
// tick against a fake ready directory, a fake clock and a fake launcher. No test opens an
// ssh connection or spawns a process.
//
// Both seams are handed the machine's REGISTRY ROW, never its name. A bench name is not a
// hostname and does not say what the machine runs: `air` is reached at
// `glenn@100.117.59.68` and answers a darwin capacity formula, and a fill that knew only
// the name sshed nowhere and sent /proc to a Mac (the schema dogfood on the M2 Air,
// 2026-09-18). The row is resolved ONCE, here, past refuseNonBenches, and it is the row
// the seams get.
//
// A card may say which os it needs -- `os: darwin`, or the os half of a `LEG: darwin/arm64`
// line -- and it is then launched only on a row whose os matches. A card that names none
// runs anywhere, exactly as before. A card nothing this tick can run is left ready with one
// FILL WAITING line per os, so it is not stuck in silence.

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

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// FillCap is the most cards one bench may take in a tick: fill-loop.sh holds this reserve
// back so a filling bench never eats the machine its own CI needs.
const FillCap = 30

// FillInterval is how often the loop ticks when --once is absent (fill-loop.sh's sleep 300).
const FillInterval = 300 * time.Second

// CardLauncher launches one card that Fill has already moved into --launched. It is the
// per-card seam of the launch verb (internal/pulse/launch.go): one call, in order, one
// card. It is handed the machine's registry ROW -- the ssh target, the os and the seat are
// all facts of that row and none of them is knowable from the bench name. The real one runs
// `nova-swarm native` on the bench over ssh; tests inject a recorder.
type CardLauncher interface {
	Launch(m fleet.Machine, card string) error
}

// Capacity answers how many cards one bench can take this tick -- card 9316's formula, the
// min of core, disk and memory headroom. It takes the registry row for the same reason the
// launcher does: the formula itself is chosen by the row's os, never by probing the machine
// to find out what it is. The real one runs it over ssh; tests inject a fixed number.
type Capacity interface {
	Capacity(m fleet.Machine) (int, error)
}

// refuseNonBenches holds every named bench against the registry BEFORE the first tick, so
// a fill naming a runner host launches nothing at all rather than launching what it can and
// refusing the rest. Every refused name gets its own line: a person who typed two wrong
// names learns both at once.
func refuseNonBenches(stderr io.Writer, reg *fleet.Registry, benches []string) int {
	code := 0
	for _, bench := range benches {
		var r *fleet.Refusal
		if err := reg.RequireBench(bench); errors.As(err, &r) {
			fmt.Fprintln(stderr, r.Line("FILL"))
			code = 2
		}
	}
	return code
}

// benchRows resolves every named bench to its registry ROW, in the order the caller named
// them. It runs past refuseNonBenches, so every name is known and carries the bench role;
// a name that somehow is not is dropped rather than guessed at.
func benchRows(reg *fleet.Registry, benches []string) []fleet.Machine {
	out := make([]fleet.Machine, 0, len(benches))
	for _, bench := range benches {
		if m, ok := reg.Lookup(bench); ok {
			out = append(out, m)
		}
	}
	return out
}

// guardedCapacity is the capacity seam with the registry in front of it: a capacity probe
// is an ssh to the machine, which is load, which is exactly what a runner host may not take.
type guardedCapacity struct {
	reg  *fleet.Registry
	next Capacity
}

func (g guardedCapacity) Capacity(m fleet.Machine) (int, error) {
	if err := g.reg.RequireBench(m.Name); err != nil {
		return 0, err
	}
	return g.next.Capacity(m)
}

// guardedLauncher is the launch seam with the registry in front of it: the last gate a card
// passes before it lands on a machine.
type guardedLauncher struct {
	reg  *fleet.Registry
	next CardLauncher
}

func (g guardedLauncher) Launch(m fleet.Machine, card string) error {
	if err := g.reg.RequireBench(m.Name); err != nil {
		return err
	}
	return g.next.Launch(m, card)
}

// FillInput is the fill verb apart from flag parsing, so a test drives one tick with fake
// directories and stub seams.
type FillInput struct {
	Ready    string        // the queue/ready directory the card-<n>.md are popped from
	Launched string        // the queue/launched directory they are moved into; its cards are live
	Lanes    string        // the lanes file: <name>\t<path prefixes> per line; empty names no lane
	Machines string        // the machines registry; a bench whose roles lack `bench` is refused
	Session  string        // the session id stamped into every launched card's marker
	Benches  []string      // the benches to fill, in order
	Only     []string      // glob patterns over a card's filename; empty takes every ready card
	Once     bool          // true runs exactly one tick and returns
	DryRun   bool          // read each bench's capacity, launch nothing and move nothing
	Interval time.Duration // how long between ticks; 0 takes FillInterval
	Stdout   io.Writer
	Stderr   io.Writer
	Now      func() time.Time
	Sleep    func(time.Duration)
	Launcher CardLauncher
	Capacity Capacity
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
	if code := refuseNonBenches(in.Stderr, reg, in.Benches); code != 0 {
		return code
	}
	// Belt and braces: even a bench that passed the list check is asked again at the
	// moment the card, or the capacity probe, would reach the machine.
	in.Capacity = guardedCapacity{reg: reg, next: in.Capacity}
	in.Launcher = guardedLauncher{reg: reg, next: in.Launcher}
	// The rows, once. Everything below this line works from the registry's own facts --
	// the ssh target, the os, the seat -- and never from the bench name.
	rows := benchRows(reg, in.Benches)
	for _, dir := range []string{in.Ready, in.Launched} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return refusal(in.Stderr, "FILL", fmt.Errorf("cannot open %s: %s (name a writable directory)", oneline.Field(dir), oneline.Err(err)))
		}
	}

	for tick := 1; ; tick++ {
		lines, res := fillTick(in, rows, tick)
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

// fillTick is one turn: list ready once in filename order, then for each bench take up to
// min(capacity, FillCap) cards and launch them. A LANE card is launched only when its lane
// has no live card; otherwise it is held, and the live card it is held behind is named. A
// LANE the lanes file does not name is refused, once per card per lanes-file mtime. The
// move out of ready is the claim, so a card another hand already took is skipped and never
// launched twice; a launcher that fails moves its card back and releases its lane. It
// returns the FILL line first and then one FILL HELD line per held card.
func fillTick(in FillInput, rows []fleet.Machine, tick int) ([]string, tickResult) {
	cards := selectedCards(readyCards(in.Ready), in.Only)
	lanes := laneTable(in.Lanes)
	live := liveLanes(in.Launched)
	// A card is offered to every bench in turn until one takes it, so a card that names an
	// os only the third bench runs still lands. `taken` is what stops it landing twice: the
	// old shared cursor could not hold both facts at once.
	taken := make(map[string]bool, len(cards))
	res := tickResult{benches: len(rows)}
	var held []string

	if strays := strayCards(in.Ready); len(strays) > 0 {
		fmt.Fprintf(in.Stderr, "FILL REFUSED ready=%s file=%s more=%d remedy=%q\n",
			oneline.Field(in.Ready), oneline.Field(filepath.Base(strays[0])), len(strays)-1,
			"fill reads card-<n>.md and nothing else; rename it, or cut it with nova-pulse cut")
	}

	parts := make([]string, 0, len(rows))
	for _, m := range rows {
		want := 0
		capacityFailed := false
		n, err := in.Capacity.Capacity(m)
		if err != nil {
			capacityFailed = true
			if res.err == nil {
				res.err = fmt.Errorf("capacity on %s: %w", field(m.Name), err)
			}
		} else {
			want = n
		}
		if want > FillCap {
			want = FillCap
		}
		if want < 0 {
			want = 0
		}
		// --dry-run stops here: the probe is the whole errand, and nothing below this
		// line may move a card or reach a machine again.
		if in.DryRun {
			if capacityFailed {
				res.failed++
				parts = append(parts, fmt.Sprintf("%s:capacity=-,take=0,os=%s,ssh=%s",
					oneline.Field(m.Name), oneline.Field(m.OS), oneline.Field(m.SSH)))
				continue
			}
			parts = append(parts, fmt.Sprintf("%s:capacity=%d,take=%d,os=%s,arch=%s,ssh=%s",
				oneline.Field(m.Name), n, want, oneline.Field(m.OS), oneline.Field(m.Arch), oneline.Field(m.SSH)))
			continue
		}

		launched, failed := 0, 0
		for _, card := range cards {
			if want <= 0 {
				break
			}
			if taken[card] {
				continue
			}
			// The os is a requirement of the card, not a preference: a darwin card on a
			// linux bench is a wasted slot and a red run. The card is left for a bench
			// that can run it, this tick or a later one.
			if need := cardOS(card); need != "" && !strings.EqualFold(need, m.OS) {
				continue
			}
			lane := cardLane(card)
			if lane != "" {
				if _, known := lanes[lane]; !known {
					refuseLane(in, card, lane)
					taken[card] = true
					continue
				}
				if holder, isLive := live[lane]; isLive {
					held = append(held, fmt.Sprintf("FILL HELD card=%s lane=%s live=%s",
						oneline.Field(filepath.Base(card)), oneline.Field(lane), oneline.Field(holder)))
					taken[card] = true
					continue
				}
			}
			base := filepath.Base(card)
			moved := filepath.Join(in.Launched, base)
			if err := os.Rename(card, moved); err != nil {
				// Another tick or another hand took it first: the card is in exactly
				// one place at every moment, and a card is never launched twice.
				taken[card] = true
				continue
			}
			taken[card] = true
			writeLaunchedMarker(in, moved, base, lane, m)
			if lane != "" {
				live[lane] = base
			}
			want--
			if err := in.Launcher.Launch(m, moved); err != nil {
				failed++
				failLaunch(in, moved, base, lane, live, err)
				if res.err == nil {
					res.err = fmt.Errorf("launch %s on %s: %w", field(base), field(m.Name), err)
				}
				continue
			}
			launched++
		}
		if capacityFailed || (launched == 0 && failed > 0) {
			res.failed++
		}
		parts = append(parts, fmt.Sprintf("%s:launched=%d,failed=%d", oneline.Field(m.Name), launched, failed))
	}

	var b strings.Builder
	b.WriteString("FILL ")
	if in.DryRun {
		b.WriteString("DRY ")
	}
	b.WriteString("tick=")
	b.WriteString(strconv.Itoa(tick))
	for _, p := range parts {
		b.WriteByte(' ')
		b.WriteString(p)
	}
	b.WriteString(" ready=")
	b.WriteString(strconv.Itoa(len(readyCards(in.Ready))))
	lines := append([]string{b.String()}, held...)
	if !in.DryRun {
		lines = append(lines, waitingLines(cards, taken, rows)...)
	}
	return lines, res
}

// waitingLines names the cards no named bench could run because none of them runs the os
// the card asks for. One line per os, with the first card and the count, and what the named
// benches actually run -- a card that waits in silence is a card nobody knows is waiting.
func waitingLines(cards []string, taken map[string]bool, rows []fleet.Machine) []string {
	have := map[string]bool{}
	named := make([]string, 0, len(rows))
	for _, m := range rows {
		have[strings.ToLower(m.OS)] = true
		named = append(named, m.Name+"="+m.OS)
	}
	first := map[string]string{}
	count := map[string]int{}
	var order []string
	for _, card := range cards {
		if taken[card] {
			continue
		}
		need := cardOS(card)
		if need == "" || have[need] {
			continue
		}
		if _, seen := first[need]; !seen {
			first[need] = filepath.Base(card)
			order = append(order, need)
		}
		count[need]++
	}
	// The roster is QUOTED, not Field-escaped: it is a list of `<bench>=<os>` pairs, and
	// Field escapes `=` to \x3d because a field VALUE may not carry one. The quotes are
	// the delimiter instead, and what is between them reads as it would be typed.
	out := make([]string, 0, len(order))
	for _, need := range order {
		out = append(out, fmt.Sprintf("FILL WAITING os=%s cards=%d first=%s named=%s",
			oneline.Field(need), count[need], oneline.Field(first[need]),
			oneline.Quote(strings.Join(named, ","))))
	}
	return out
}

// cardOS reads the os a card must run on: an `os: <name>` line, or the os half of a
// `LEG: <os>/<arch>` line. "" is a card that runs anywhere, which is most of them. Only the
// exact field prefix counts, so a `LEGS:` line is prose -- the same rule cardLane keeps.
func cardOS(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(line)
		for _, prefix := range []string{"os:", "OS:", "LEG:", "leg:"} {
			v, ok := strings.CutPrefix(t, prefix)
			if !ok {
				continue
			}
			v = strings.TrimSpace(v)
			if goos, _, _ := strings.Cut(v, "/"); strings.TrimSpace(goos) != "" {
				return strings.ToLower(strings.TrimSpace(goos))
			}
		}
	}
	return ""
}

// failLaunch is what a launcher's failure costs: the card goes back to --ready, its lane is
// released so the lane is not held by a card that never ran, and a `.failed-<n>` marker
// beside it carries the attempt number and the reason. The card is ready again on the next
// tick, and the markers are the count of how often it has failed.
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
	n := len(failedMarkers(in.Ready, base)) + 1
	line := fmt.Sprintf("%s\tattempt=%d\t%s\n",
		in.Now().UTC().Format(time.RFC3339), n, oneline.Cap(oneline.Err(cause), oneline.TailBytes))
	_ = os.WriteFile(marker(in.Ready, base, "failed", strconv.Itoa(n)), []byte(line), 0o644)
}

// refuseLane prints one FILL REFUSED line for a lane the lanes file does not name -- once
// per card per lanes-file mtime. The marker beside the card is the memory: a second tick
// over the same unchanged lanes file says nothing, and a lanes file that was edited (a new
// mtime, so a new marker name) speaks again, because the answer may have changed.
func refuseLane(in FillInput, card, lane string) {
	base := filepath.Base(card)
	key := strconv.FormatInt(lanesStamp(in.Lanes), 10)
	path := marker(in.Ready, base, "refused", key)
	if _, err := os.Stat(path); err == nil {
		return
	}
	for _, stale := range refusedMarkers(in.Ready, base) {
		if stale != path {
			_ = os.Remove(stale)
		}
	}
	_ = os.WriteFile(path, []byte(lane+"\n"), 0o644)
	fmt.Fprintf(in.Stderr, "FILL REFUSED card=%s lane=%s remedy=%q\n",
		oneline.Field(base), oneline.Field(lane),
		fmt.Sprintf("add the lane to %s or drop the LANE line", in.Lanes))
}

// marker is the path of one marker beside a card: <card>.<kind>-<key>. It is never a
// card-<n>.md, so the queue's own glob steps over it.
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
// holds, the bench it went to and how that bench is reached, its label and the session that
// cut it. `harvest` reads this to release the lane and to know whose job it is looking at,
// and the ssh and os lines are how a person reading the marker can tell WHICH machine ran
// the card without opening the registry as it was that day.
func writeLaunchedMarker(in FillInput, moved, base, lane string, m fleet.Machine) {
	now := in.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	body := fmt.Sprintf("lane=%s\nbench=%s\nssh=%s\nos=%s\nlabel=%s\nsession=%s\ncard=%s\nat=%s\n",
		lane, m.Name, m.SSH, m.OS, strings.TrimSuffix(base, ".md"), in.Session, base,
		now().UTC().Format(time.RFC3339))
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
