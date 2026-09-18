package pulse

// Fill is fill-loop.sh's tick body as one verb (#1142): for each bench, read the bench's
// capacity, cap it at FillCap, pop that many card-*.md from --ready in filename order, move
// each to --launched and hand it to the launcher. One FILL line per tick, no model call.
//
// A card may name a LANE (`LANE: <name>`), and a lane is a serial queue over one area of
// the codebase: at most one live card per lane at a time. A ready card whose lane already
// has a live card -- one under --launched, or one launched earlier in this tick -- is held
// in order with a FILL HELD line and stays ready. A LANE the lanes file does not name is
// refused with the remedy. A card with no LANE is launched exactly as before.
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

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
// card. The real one shells flash-native-bench.sh on the bench; tests inject a recorder.
type CardLauncher interface {
	Launch(bench, card string) error
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

func (g guardedLauncher) Launch(bench, card string) error {
	if err := g.reg.RequireBench(bench); err != nil {
		return err
	}
	return g.next.Launch(bench, card)
}

// FillInput is the fill verb apart from flag parsing, so a test drives one tick with fake
// directories and stub seams.
type FillInput struct {
	Ready    string        // the queue/ready directory the card-*.md are popped from
	Launched string        // the queue/launched directory they are moved into; its cards are live
	Lanes    string        // the lanes file: <name>\t<path prefixes> per line; empty names no lane
	Machines string        // the machines registry; a bench whose roles lack `bench` is refused
	Benches  []string      // the benches to fill, in order
	Once     bool          // true runs exactly one tick and returns
	Interval time.Duration // how long between ticks; 0 takes FillInterval
	Stdout   io.Writer
	Stderr   io.Writer
	Now      func() time.Time
	Sleep    func(time.Duration)
	Launcher CardLauncher
	Capacity Capacity
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
// or exactly one tick when --once is set. It returns 0, or 2 on a refusal that never
// started.
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
		{in.Ready, "ready", "the directory holding the card-*.md ready to launch"},
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
	for _, dir := range []string{in.Ready, in.Launched} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return refusal(in.Stderr, "FILL", fmt.Errorf("cannot open %s: %s (name a writable directory)", oneline.Field(dir), oneline.Err(err)))
		}
	}

	for tick := 1; ; tick++ {
		lines, err := fillTick(in, tick)
		for _, line := range lines {
			fmt.Fprintln(in.Stdout, line)
		}
		if err != nil {
			fmt.Fprintf(in.Stderr, "FILL NOTE tick=%d: %s\n", tick, oneline.Err(err))
		}
		if in.Once {
			break
		}
		in.Sleep(in.Interval)
	}
	return 0
}

// fillTick is one turn: list ready once in filename order, then for each bench take up to
// min(capacity, FillCap) cards and launch them. A LANE card is launched only when its lane
// has no live card; otherwise it is held, and the live card it is held behind is named. A
// LANE the lanes file does not name is refused. The move out of ready is the claim, so a
// card another hand already took is skipped and never launched twice. It returns the FILL
// line first and then one FILL HELD line per held card, plus the first launcher error.
func fillTick(in FillInput, tick int) ([]string, error) {
	cards := readyCards(in.Ready)
	lanes := laneTable(in.Lanes)
	live := liveLanes(in.Launched)
	gate := newCertifyGate(in)
	idx := 0
	var firstErr error
	var held []string

	parts := make([]string, 0, len(in.Benches))
	for _, bench := range in.Benches {
		want := 0
		if n, err := in.Capacity.Capacity(bench); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("capacity on %s: %w", field(bench), err)
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

		launched := 0
		for want > 0 && idx < len(cards) {
			card := cards[idx]
			idx++
			lane := cardLane(card)
			if lane != "" {
				if _, known := lanes[lane]; !known {
					fmt.Fprintf(in.Stderr, "FILL REFUSED card=%d lane=%s remedy=%q\n",
						cardNumber(card), oneline.Field(lane),
						fmt.Sprintf("add the lane to %s or drop the LANE line", in.Lanes))
					continue
				}
				if holder, isLive := live[lane]; isLive {
					held = append(held, fmt.Sprintf("FILL HELD card=%d lane=%s live=%s",
						cardNumber(card), oneline.Field(lane), oneline.Field(holder)))
					continue
				}
			}
			// A card goes to a machine that has PROVED it can do that kind of work, or it
			// does not go. The card stays READY, so the remedy is run and the same card is
			// dealt again rather than lost.
			if !gate.allows(in.Stderr, bench, card) {
				continue
			}
			moved := filepath.Join(in.Launched, filepath.Base(card))
			if err := os.Rename(card, moved); err != nil {
				// Another tick or another hand took it first: the card is in exactly
				// one place at every moment, and a card is never launched twice.
				continue
			}
			if lane != "" {
				live[lane] = filepath.Base(moved)
			}
			if err := in.Launcher.Launch(bench, moved); err != nil && firstErr == nil {
				firstErr = fmt.Errorf("launch %s on %s: %w", field(filepath.Base(moved)), field(bench), err)
			}
			launched++
			want--
		}
		parts = append(parts, fmt.Sprintf("%s:launched=%d", oneline.Field(bench), launched))
	}

	var b strings.Builder
	b.WriteString("FILL tick=")
	b.WriteString(strconv.Itoa(tick))
	for _, p := range parts {
		b.WriteByte(' ')
		b.WriteString(p)
	}
	b.WriteString(" ready=")
	b.WriteString(strconv.Itoa(len(readyCards(in.Ready))))
	lines := append([]string{b.String()}, held...)
	return lines, firstErr
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
// is the live set, and a live card's lane is read from its own card file.
func liveLanes(launched string) map[string]string {
	out := map[string]string{}
	for _, card := range readyCards(launched) {
		if lane := cardLane(card); lane != "" {
			out[lane] = filepath.Base(card)
		}
	}
	return out
}

// readyCards lists the ready card files in filename order, which is the order ls handed
// fill-loop.sh. The move out of ready is the queue's claim; the glob is a snapshot.
func readyCards(dir string) []string {
	cards, _ := filepath.Glob(filepath.Join(dir, "card-*.md"))
	return cards // filepath.Glob returns lexical order
}
