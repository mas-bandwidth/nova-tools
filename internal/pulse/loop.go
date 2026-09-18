package pulse

// ONE VERB IS THE LOOP.
//
// bin/pulse-loop.sh is one program. nova-pulse had its body as three: `run` (the tick),
// `fill` (capacity, the machines registry, the lanes) and `manager` (the shift). A person
// retiring the script had to start three loops, in the right order, on the right queue, with
// the right identity -- and nothing made them agree. That is gap 10 of the manager dogfood:
// the two placement roads read different tables, so a `run` tick could put a card on a
// runner host that `fill --machines` refuses by name.
//
// A tick here is the script's order and nothing else:
//
//  1. THE LOCK. One writer per queue, taken once for the whole loop, so the three verbs
//     inside a tick are one writer and a second loop refuses at exit 2 naming this one
//     (queuelock.go).
//  2. RUN, one tick: gate, harvest, sweep, reap, refill, launch, one WIDTH line.
//  3. FILL, one tick: every bench's capacity against the registry, the lanes, the gates.
//  4. MANAGER, one cycle: the bus, the verdicts, the merges, the refill, the gate release.
//  5. THE LAUNCH-DEAD PROBE: every launched card whose job never appeared is given back.
//  6. ONE LOOP TICK LINE.
//
// Every one of the three keeps its own line, and those lines go to <queue>/pulse.log, which
// is where the hand loop wrote them and where a person already looks. The console keeps the
// LOOP TICK line: six lines a tick on a console is the poll these verbs exist to end.
//
// THE THREE STEPS ARE SEAMS. A test drives the whole order with recorders and no bench, no
// bus and no forge; and a real loop wires them to the shipped verbs, which is all the
// difference there is between them.

import (
	"bytes"
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

// DefaultLoopInterval is how long a loop sleeps between ticks when the caller names none:
// the script's PULSE_TICK, ten seconds, which is SPEC-PULSE's manager tick (rule 1).
const DefaultLoopInterval = 10 * time.Second

// DefaultLaunchGrace is how long a launched card has to show a job directory before the
// probe calls the launch dead. bin/pulse-loop.sh's launch_check slept 90 s for the same
// reason: the job directory is made at the start, the NATIVE line only comes at the end, and
// a harness under load is silent for minutes in between.
const DefaultLaunchGrace = 90 * time.Second

// LoopInput is the loop verb apart from flag parsing.
type LoopInput struct {
	Queue    string // the queue: pending, launched, done, failed, ready, the state files
	Machines string // the machines registry EVERY placement is held against
	Lanes    string // the lanes table EVERY placement is held against
	Roots    string // the swarm roots, comma separated
	Repo     string // owner/name: the gate's branch, and the PR a card's AFTER line names
	Branch   string // the branch the mechanical gate watches
	Policy   string // the manager's policy file; empty runs no manager step
	Bus      string // the nova-bus clone the manager waits on
	As       string // the name the manager waits and receipts as
	Ready    string // the fill's ready directory; empty is <queue>/ready
	Launched string // the fill's launched directory; empty is <queue>/launched
	Benches  []string
	Session  string

	Once     bool          // exactly one tick
	DryRun   bool          // every step reads and nothing changes
	Deadline time.Duration // how long the loop runs; 0 with --once is one tick
	Interval time.Duration // between ticks; 0 is DefaultLoopInterval
	Grace    time.Duration // how long a launch has to show a job dir; 0 is DefaultLaunchGrace
	Max      int

	Stdout io.Writer
	Stderr io.Writer
	Now    func() time.Time
	Sleep  func(time.Duration)

	// The three steps, as seams. Nil takes the shipped verb.
	RunStep     func(RunInput) int
	FillStep    func(FillInput) int
	ManagerStep func(ManagerInput) int

	// What the shipped steps need that a test replaces: the bench capacity reader, the
	// per-card launcher and the forge the gates are asked of.
	Capacity Capacity
	Launcher CardLauncher
	Forge    PRSource
}

// Loop holds the loop. It returns 0 when it ended by itself and 2 on a refusal that never
// started -- a missing flag, an unreadable registry, or another writer on this queue.
func Loop(in LoopInput) int {
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
		in.Interval = DefaultLoopInterval
	}
	if in.Grace <= 0 {
		in.Grace = DefaultLaunchGrace
	}
	if in.Max == 0 {
		in.Max = 20
	}
	for _, r := range []struct{ v, name, wants string }{
		{in.Queue, "queue", "the queue directory holding pending, launched, done, failed and the state files"},
		{in.Machines, "machines", "the machines registry: which hosts are benches and which serve the merge group's shards"},
		{in.Lanes, "lanes", "the lanes table: one <name>\\t<path prefixes> line per serial area"},
		{in.Roots, "roots", "the swarm roots this loop runs on, comma separated"},
	} {
		if strings.TrimSpace(r.v) == "" {
			return refusal(in.Stderr, "LOOP", fmt.Errorf("missing --%s; refusing to guess (%s)", r.name, r.wants))
		}
	}
	if !in.Once && in.Deadline <= 0 {
		return refusal(in.Stderr, "LOOP", fmt.Errorf("missing --deadline; refusing to guess (how long this loop runs, such as 6h -- or --once for exactly one tick)"))
	}
	// --dry-run IS NOT HONOURED BY THE RUN STEP, so it is refused rather than half-kept.
	// `fill` and `manager` have a real read-only mode; the run tick's six seams -- gate,
	// harvest, sweep, reap, refill, launch -- have none, and RunInput has no field to carry
	// one. A loop that took --dry-run and then harvested, merged, reaped and launched is
	// worse than no flag: it is a flag a person points at a live queue BECAUSE they were
	// promised nothing would move (Stella's cold read of #1430, defect 1). A caller that
	// injects its own RunStep owns that step's behaviour and may dry-run it.
	if in.DryRun && in.RunStep == nil {
		return refusal(in.Stderr, "LOOP", fmt.Errorf(
			"--dry-run is not honoured by the run step and is refused rather than half-kept: its seams (gate, harvest, sweep, reap, refill, launch) have no read-only mode, so the tick would still harvest, merge, reap, cut and launch. Use `nova-pulse fill --dry-run` and `nova-pulse manager --dry-run`, which change nothing, or run the loop for real (nova-tools #1441)"))
	}
	if in.Ready == "" {
		in.Ready = filepath.Join(in.Queue, "ready")
	}
	if in.Launched == "" {
		in.Launched = filepath.Join(in.Queue, "launched")
	}
	// EVERY TABLE IS READ BEFORE THE FIRST TICK. A path that is merely non-empty is not a
	// registry: a typo in --machines used to become a tick that placed nothing and said so
	// only in the log, and an unparseable lanes file became "no lane by that name" and
	// refused every card in the queue one at a time (Stella's cold read, defect 3).
	if err := in.validate(); err != nil {
		return refusal(in.Stderr, "LOOP", err)
	}
	// THE LOCK, once, for the whole loop. The three verbs inside a tick each ask for it and
	// each is handed back a handle that releases nothing, because one process is one writer.
	if !in.DryRun {
		lock, err := LockQueue(in.Queue, "loop")
		if err != nil {
			return refusal(in.Stderr, "LOOP", err)
		}
		defer lock.Release()
	}

	l := &looper{in: in, roots: splitList(in.Roots)}
	start := in.Now()
	end := start.Add(in.Deadline)
	failed := 0
	for tick := 1; ; tick++ {
		c := l.tick(tick)
		fmt.Fprintln(in.Stdout, c.line(in.DryRun))
		failed += c.failed
		if in.Once || !in.Now().Before(end) {
			break
		}
		in.Sleep(in.Interval)
	}
	// A STEP THAT FAILED IS NOT A QUIET DAY. The loop used to throw every step's exit code
	// away and answer 0, so a shift whose fill could reach no bench and whose manager refused
	// its policy looked exactly like a shift with nothing to do.
	if failed > 0 {
		fmt.Fprintf(in.Stderr, "LOOP NOTE steps failed=%d: read %s\n", failed, oneline.Field(filepath.Join(in.Queue, "pulse.log")))
		return 1
	}
	return 0
}

// validate reads every table the loop places against, BEFORE the first tick. A refusal here
// costs nobody a card; the same refusal found on tick 40 has already run 39 ticks against a
// registry nobody could read.
func (in LoopInput) validate() error {
	if _, err := fleet.ReadRegistry(in.Machines); err != nil {
		return fmt.Errorf("--machines %s: %s (the registry says which hosts are benches and which serve the merge group's shards)", oneline.Field(in.Machines), oneline.Err(err))
	}
	if _, err := os.Stat(in.Lanes); err != nil {
		return fmt.Errorf("--lanes %s: %s (one <name> and its path prefixes per tab-separated line)", oneline.Field(in.Lanes), oneline.Err(err))
	}
	if len(laneTable(in.Lanes)) == 0 {
		return fmt.Errorf("--lanes %s names no lane; refusing to guess (every card carrying a LANE line would be refused one at a time)", oneline.Field(in.Lanes))
	}
	roots := splitList(in.Roots)
	if len(roots) == 0 {
		return fmt.Errorf("--roots %s names no root; refusing to guess (the swarm roots this loop runs on, comma separated)", oneline.Field(in.Roots))
	}
	for _, root := range roots {
		if !isDir(root) {
			return fmt.Errorf("--roots names %s, which is not a directory (a swarm root holds <slot>/jobs/<label>)", oneline.Field(root))
		}
	}
	if p := strings.TrimSpace(in.Policy); p != "" {
		if _, err := readPolicy(p); err != nil {
			return fmt.Errorf("--policy %s", oneline.Err(err))
		}
	}
	return nil
}

// looper is the loop's state.
type looper struct {
	in    LoopInput
	roots []string
}

// tick is one turn of the script's order, and it answers the counts the console line carries.
//
// A STEP THAT FAILED STOPS WHAT DEPENDS ON IT. The run tick is the gate: it is what asks
// whether the branch is red, folds what came back, reaps what leaked and tops the pool up, so
// a run tick that could not run is not a tick anything may place cards after. `fill` failing
// does NOT stop `manager`: a bench nobody can reach is no reason to stop merging what already
// came home. Every skipped step is named, and the failures are counted on the line.
func (l *looper) tick(n int) loopCounts {
	var c loopCounts
	c.n = n

	// 2. RUN.
	runOut, runCode := l.step("run", func(w io.Writer) int { return l.runStep(w) })
	c.ran, c.harvested = fieldInt(runOut, "launched="), fieldInt(runOut, "harvested=")
	if runCode != 0 {
		c.failed++
		l.note("LOOP SKIPPED fill, manager and the launch-dead probe: the run tick exited %d, so the gate, the harvest, the reap and the refill did not happen and nothing may place a card behind them", runCode)
		c.refused += countPrefix(runOut, "LAUNCH REFUSED")
		return c
	}

	// 3. FILL.
	fillOut, fillCode := l.step("fill", func(w io.Writer) int { return l.fillStep(w) })
	c.filled = benchLaunched(fillOut)
	c.held = countPrefix(fillOut, "FILL HELD") + countPrefix(fillOut, "FILL GATED")
	c.refused = countPrefix(fillOut, "FILL REFUSED")
	if fillCode != 0 {
		c.failed++
	}

	// 4. MANAGER.
	mgrOut := ""
	if strings.TrimSpace(l.in.Policy) != "" {
		var mgrCode int
		mgrOut, mgrCode = l.step("manager", func(w io.Writer) int { return l.managerStep(w) })
		if mgrCode != 0 {
			c.failed++
		}
	}
	// A refusal on either road into a bench is a refusal, and the loop counts both.
	c.refused += countPrefix(runOut, "LAUNCH REFUSED") + countPrefix(mgrOut, "MANAGER REFUSED")
	c.held += countPrefix(runOut, "LAUNCH HELD") + countPrefix(runOut, "LAUNCH GATED")

	// 5. THE PROBE.
	c.dead = l.launchDead()

	return c
}

// note files one line of the loop's own, in the same place every step's lines go.
func (l *looper) note(format string, a ...any) {
	appendLine(filepath.Join(l.in.Queue, "pulse.log"),
		l.in.Now().UTC().Format("15:04:05Z")+" "+fmt.Sprintf(format, a...))
}

// step runs one of the three and puts its lines where the hand loop put them:
// <queue>/pulse.log. It returns what the step printed so the tick can count it.
func (l *looper) step(name string, run func(io.Writer) int) (string, int) {
	var buf bytes.Buffer
	code := run(&buf)
	// A verb that stamps its own lines is stamped ONCE. The wired verbs write through
	// Wiring.log, which puts the time in front; the loop puts the time in front of every
	// line it files. Both ran, and the log read `19:51:51Z 19:51:51Z LAUNCH REFUSED ...`
	// with the marker no longer at the start of the line, so the LOOP TICK line above it
	// read refused=0 while the refusal sat two lines below (found dogfooding this verb,
	// 2026-09-18).
	var kept []string
	for _, line := range strings.Split(buf.String(), "\n") {
		line = unstamp(line)
		if line == "" {
			continue
		}
		kept = append(kept, line)
		appendLine(filepath.Join(l.in.Queue, "pulse.log"),
			l.in.Now().UTC().Format("15:04:05Z")+" "+line)
	}
	if code != 0 {
		l.note("LOOP STEP %s exited %d", oneline.Field(name), code)
	}
	return strings.Join(kept, "\n"), code
}

// unstamp takes one leading `HH:MM:SSZ ` off a line, so a marker is at the start of the line
// whichever verb wrote it.
func unstamp(line string) string {
	line = strings.TrimSpace(line)
	stamp, rest, ok := strings.Cut(line, " ")
	if !ok || len(stamp) != 9 || !strings.HasSuffix(stamp, "Z") {
		return line
	}
	for i, r := range stamp[:8] {
		if i == 2 || i == 5 {
			if r != ':' {
				return line
			}
			continue
		}
		if r < '0' || r > '9' {
			return line
		}
	}
	return strings.TrimSpace(rest)
}

func (l *looper) runStep(w io.Writer) int {
	in := RunInput{
		Queue: l.in.Queue, Roots: l.in.Roots, Repo: l.in.Repo, Branch: l.in.Branch,
		Hours: 0, Once: true, Locked: true, Max: l.in.Max,
		Bus: l.in.Bus, As: l.in.As,
		Stdout: w, Stderr: w, Now: l.in.Now,
		Sleep: func(time.Duration) {},
	}
	if l.in.RunStep != nil {
		return l.in.RunStep(in)
	}
	cfg := DefaultConfig()
	in.Configured = func(c Config) { cfg = c }
	// The wired verbs' own lines come HERE rather than straight to <queue>/pulse.log: the
	// loop writes them to the same file a breath later, and on the way it can COUNT them.
	// Without this a `LAUNCH REFUSED bench=studio reason=runner-host` was in the log and the
	// LOOP TICK line above it read refused=0 (found dogfooding this verb, 2026-09-18).
	Wire(&in, NewWiring(WiringInput{
		Queue: l.in.Queue, Roots: l.in.Roots, Repo: l.in.Repo, Branch: l.in.Branch,
		Machines: l.in.Machines, Lanes: l.in.Lanes,
		Max: l.in.Max, Now: l.in.Now, Log: w,
		Config: func() Config { return cfg },
		PRs:    l.in.Forge,
	}))
	return Run(in)
}

func (l *looper) fillStep(w io.Writer) int {
	in := FillInput{
		Ready: l.in.Ready, Launched: l.in.Launched, Lanes: l.in.Lanes,
		Machines: l.in.Machines, Queue: l.in.Queue, Repo: l.in.Repo,
		Session: l.in.Session, Benches: l.in.Benches, Once: true,
		DryRun: l.in.DryRun, Locked: true,
		Stdout: w, Stderr: w, Now: l.in.Now, Sleep: func(time.Duration) {},
		Capacity: l.in.Capacity, Launcher: l.in.Launcher, Forge: l.in.Forge,
	}
	if l.in.FillStep != nil {
		return l.in.FillStep(in)
	}
	return Fill(in)
}

func (l *looper) managerStep(w io.Writer) int {
	in := ManagerInput{
		Policy: l.in.Policy, Queue: l.in.Queue, Roots: l.in.Roots,
		Bus: l.in.Bus, As: l.in.As, Hours: 0, Once: true,
		DryRun: l.in.DryRun, Locked: true, Max: l.in.Max,
		Stdout: w, Stderr: w, Now: l.in.Now,
	}
	if l.in.ManagerStep != nil {
		return l.in.ManagerStep(in)
	}
	return Manager(in)
}

// launchDead is bin/pulse-loop.sh's launch_check, as a check the tick runs rather than a
// sleeping child of every launch. A card under --launched whose marker is older than the
// grace and whose job directory has never appeared on any root did not start: the runner
// refused it, the ssh died, the batch never ran. The card goes back to the pending queue and
// its marker goes with it, so the LANE IT WAS HOLDING IS RELEASED -- a lane held by a card
// that never ran is a lane nobody can use, and that is how a serial area stops for an hour.
//
// A card whose job directory IS there is alive and is never touched here: `reap --deadline`
// owns the slow ones, twenty-five minutes out. This probe owns the ones that never began.
func (l *looper) launchDead() int {
	if l.in.DryRun {
		return 0
	}
	now := l.in.Now()
	dead := 0
	for _, card := range readyCards(l.in.Launched) {
		base := filepath.Base(card)
		marker := readLaunchedMarker(l.in.Launched, base)
		at, err := time.Parse(time.RFC3339, marker["at"])
		if err != nil || now.Sub(at) < l.in.Grace {
			continue // no marker to judge, or still inside its grace
		}
		label := strings.TrimSuffix(base, ".md")
		if jobExists(l.roots, label) {
			continue
		}
		back := filepath.Join(l.in.Queue, "pending", base)
		if err := os.MkdirAll(filepath.Dir(back), 0o755); err != nil {
			continue
		}
		if err := os.Rename(card, back); err != nil {
			continue
		}
		_ = os.Remove(launchedMarker(l.in.Launched, base))
		dead++
		appendLine(filepath.Join(l.in.Queue, "pulse.log"), fmt.Sprintf("%s LOOP LAUNCH-DEAD card=%s bench=%s lane=%s age=%s: no job directory after the launch; requeued and its lane released",
			now.UTC().Format("15:04:05Z"), oneline.Field(base), oneline.Field(nonEmpty(marker["bench"], "-")),
			oneline.Field(nonEmpty(marker["lane"], "-")), now.Sub(at).Round(time.Second)))
		appendLine(filepath.Join(l.in.Queue, "ESCALATE"), fmt.Sprintf("%s LOOP LAUNCH-DEAD card=%s bench=%s: the card never started; read the batch output on that bench",
			now.UTC().Format(time.RFC3339), oneline.Field(base), oneline.Field(nonEmpty(marker["bench"], "-"))))
	}
	return dead
}

// loopCounts is what one tick did, and the LOOP TICK line is exactly these fields.
type loopCounts struct {
	n, ran, filled, harvested, held, refused, dead, failed int
}

// line is the one line the console keeps. A held card and a gated card are both held: from a
// person's chair "this card is not going out yet, and something already said why on its own
// line" is one fact, and the FILL HELD and FILL GATED lines in pulse.log carry which.
func (c loopCounts) line(dry bool) string {
	line := fmt.Sprintf("LOOP TICK n=%d ran=%d filled=%d harvested=%d held=%d refused=%d dead=%d failed=%d",
		c.n, c.ran, c.filled, c.harvested, c.held, c.refused, c.dead, c.failed)
	if dry {
		line += " dry-run=yes"
	}
	return line
}

// fieldInt reads `<name><digits>` out of a verb's own line -- the bounded one-line contract
// every verb here keeps. The loop does not reach into the verbs' internals: it reads what
// they said, which is the same thing a person reads.
func fieldInt(out, name string) int {
	i := strings.Index(out, name)
	if i < 0 {
		return 0
	}
	rest := out[i+len(name):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(rest[:end])
	return n
}

// benchLaunched adds up the `<bench>:launched=<n>,failed=<n>` pairs of a FILL line.
func benchLaunched(out string) int {
	total := 0
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "FILL tick=") {
			continue
		}
		for _, f := range strings.Fields(line) {
			_, pair, ok := strings.Cut(f, ":")
			if !ok {
				continue
			}
			if v, _, ok := strings.Cut(pair, ","); ok {
				total += fieldInt(v, "launched=")
			}
		}
	}
	return total
}

// countPrefix counts the lines of a verb's output that begin with one of its markers.
func countPrefix(out, prefix string) int {
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			n++
		}
	}
	return n
}
