package main

// The fill verb: fill-loop.sh's tick body, once per tick, for every bench. --machines names
// the machines registry, and with no --bench the pool IS the registry: every row whose roles
// carry `bench` and whose notes carry `certified=<YYYY-MM-DD>` (#1476). No fleet name is
// written down here. A --bench whose roles lack `bench` is refused before any ssh:
// runner hosts are CI-only (Glenn 2026-09-18), and the fill is the path a CARD takes. It reads the
// bench's capacity over ssh, pops that many ready cards, moves them to launched and hands
// each to flash-native-bench.sh. A card's `LANE: <name>` line serializes its area: at most
// one live card per lane, the rest held in order, named by --lanes (default
// queue/control/lanes.tsv). One FILL line per tick.
//
// Two flags make the tick runnable without a bench: --capacity <n> is a fixed capacity and
// no ssh at all, and --launcher <path> is the program each card is handed to. Together they
// are a dry run over a directory of cards -- the way the lane logic (FILL HELD) is
// exercised by a hand, not only by a test's injected seam.

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// defaultSwarmRoot is where a bench keeps the swarm root unless --swarm-root says
// otherwise. It is expanded ON THE BENCH, by the bench's own shell, and it is a path and
// not a machine name: this package names no machine in the fleet.
const defaultSwarmRoot = "$HOME/rowan-swarm-root"

// benchFlag collects a repeatable --bench, split on commas.
type benchFlag []string

func (b *benchFlag) String() string { return strings.Join(*b, ",") }

func (b *benchFlag) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if s := strings.TrimSpace(part); s != "" {
			*b = append(*b, s)
		}
	}
	return nil
}

func cmdFill(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("fill")
	ready := f.fs.String("ready", "", "")
	launched := f.fs.String("launched", "", "")
	lanes := f.fs.String("lanes", "queue/control/lanes.tsv", "")
	machines := f.fs.String("machines", "queue/control/machines.tsv", "")
	providers := f.fs.String("providers", "", "")
	session := f.fs.String("session", "", "")
	once := f.fs.Bool("once", false, "")
	capacity := f.fs.Int("capacity", -1, "")
	launcher := f.fs.String("launcher", "", "")
	swarmRoot := f.fs.String("swarm-root", defaultSwarmRoot, "")
	deadline := f.fs.Int("deadline", defaultCardDeadline, "")
	grace := f.fs.String("launch-grace", defaultLaunchGrace.String(), "")
	interval := f.fs.String("interval", FillIntervalDefault.String(), "")
	stop := f.fs.String("stop", "", "")
	slotsStore := f.fs.String("slots-store", defaultSlotsStore, "")
	slotsOwner := f.fs.String("slots-owner", "", "")
	slotsBin := f.fs.String("slots-bin", defaultSlotsBin, "")
	maxLoad := f.fs.Float64("max-load-per-core", defaultMaxLoadPerCore, "")
	var benches benchFlag
	var only benchFlag
	var localBenches benchFlag
	f.fs.Var(&benches, "bench", "")
	f.fs.Var(&only, "only", "")
	f.fs.Var(&localBenches, "local-bench", "")

	if !f.parse(args, stderr) {
		return 2
	}
	wait, err := parseGrace(*grace)
	if err != nil {
		f.add(err.Error())
	}
	tick, err := parseInterval(*interval)
	if err != nil {
		f.add(err.Error())
	}
	local, err := localBenchSet([]string(localBenches), []string(benches))
	if err != nil {
		f.add(err.Error())
	}
	// NaN is not less than zero, so `< 0` alone let `--max-load-per-core NaN` through, and a
	// NaN threshold compares false against every bench: the brake the caller asked for was
	// off and nothing said so. Infinity is the same silence, spelled differently.
	if math.IsNaN(*maxLoad) || math.IsInf(*maxLoad, 0) || *maxLoad < 0 {
		f.add(fmt.Sprintf(
			"--max-load-per-core is a finite number of load units per core, 0 or more (0 is the documented no-brake opt-out), got %v; a threshold nothing can exceed is a brake that is silently off",
			*maxLoad))
	}
	if *deadline <= 0 {
		f.add(fmt.Sprintf("--deadline is the card's deadline in whole seconds, 1 or more, got %d", *deadline))
	}
	f.want(*ready, "ready", "the directory holding the card-<n>.md ready to launch")
	f.want(*launched, "launched", "the directory the launched cards are moved into")
	f.want(*machines, "machines", "the machines registry: which hosts are benches and which serve the merge group's shards")
	if *capacity < -1 {
		f.add(fmt.Sprintf("--capacity is 0 or more, got %d; leave it out to read each bench's capacity over ssh", *capacity))
	}
	if f.refused(stderr) {
		return 2
	}
	// THE STORE LEADS, THE LOAD BRAKES (#1914). With a slot store named -- and one is
	// named by default -- the capacity is the bench's own free count and the load is only a
	// guard. `--slots-store ""` asks for the old load formula and nothing else; `--capacity`
	// is a fixed number for every bench and no probe at all.
	var reader pulse.Capacity
	switch {
	case *capacity >= 0:
		reader = fixedCapacity(*capacity)
	case strings.TrimSpace(*slotsStore) == "":
		reader = sshCapacity{root: *swarmRoot}
	default:
		reader = storeProbeCapacity(storeProbeConfig{
			Store:    *slotsStore,
			Owner:    *slotsOwner,
			SlotsBin: *slotsBin,
			Root:     *swarmRoot,
			Local:    local,
			MaxLoad:  *maxLoad,
			Stderr:   stderr,
		})
	}
	return pulse.Fill(pulse.FillInput{
		Ready:     *ready,
		Launched:  *launched,
		Lanes:     *lanes,
		Machines:  *machines,
		Providers: *providers,
		Session:   *session,
		Benches:   []string(benches),
		Only:      []string(only),
		Once:      *once,
		Interval:  tick,
		Stop:      *stop,
		Stdout:    stdout,
		Stderr:    stderr,
		Now:       func() time.Time { return now },
		Capacity:  reader,
		Launcher:  flashLauncher{bin: *launcher, deadline: *deadline, grace: wait},
	})
}

// swarmRootScript resolves the volume a card's I/O actually lands on and leaves it in $r.
// It is a prelude and not a whole script so a test can run it alone over tmp directories.
//
// The root is resolved THROUGH its symlinks (`cd` then `pwd -P`), because on antman
// `~/rowan-swarm-root` is a link to `/data/swarm` on a second disk: `df $HOME` there
// measures the root LV, which is not the volume the cards fill. A root that is not
// configured, or is configured and not there, falls back to $HOME -- the number every
// bench answered before this, and never a refusal in the middle of a tick.
//
// The root is expanded by the bench's own shell, so `$HOME` in it means the bench's home.
func swarmRootScript(root string) string {
	home := `r=$(cd "$HOME" 2>/dev/null && pwd -P); [ -n "$r" ] || r="$HOME"`
	if strings.TrimSpace(root) == "" {
		return home
	}
	return `r=$(cd "` + shellDoubleQuoted(root) + `" 2>/dev/null && pwd -P); [ -n "$r" ] || ` + home
}

// shellDoubleQuoted makes a path safe inside the double quotes above while leaving `$`
// alone, because the whole point of the default is that the BENCH expands `$HOME`.
func shellDoubleQuoted(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`")
	return r.Replace(s)
}

// capacityBody is fill-loop.sh's formula, run on the bench: the min of core headroom
// (cores*1.5 - load1 - cores/8, a CI reserve), disk headroom ((free_gb-25)/2) and memory
// headroom (memfree_gb/2). The disk term reads the swarm root's volume, not $HOME's. It
// wants $c (cores) and $li (load1 as a whole number) already set, and leaves the number in
// $a -- so the store probe can fall back to it for a bench that has no slot store without
// writing the formula down twice.
//
// It is the number `fill` answered before the slot store, and it is NOT the capacity any
// more: a bench earns load by running the cards it was dealt, so this closed the fleet
// exactly when the fleet was working (#1914).
func capacityBody(root string) string {
	return capacityReadings(root) + `; ` + capacityArithmetic()
}

// capacityReadings takes the formula's two other measurements and leaves them in $f (free
// GB on the swarm root's volume) and $m (available GB of memory). They are SEPARATE from
// the arithmetic so the store probe can check that each one was actually read before using
// it: an unread measurement that arrives as an empty string is a zero to shell arithmetic,
// and a zero is a number somebody could act on (Stella, R2 of #1945).
func capacityReadings(root string) string {
	return swarmRootScript(root) +
		`; f=$(df -BG "$r" 2>/dev/null | awk 'NR==2{gsub("G","",$4); print $4}')` +
		`; m=$(awk '/MemAvailable/{printf "%d", $2/1048576}' /proc/meminfo 2>/dev/null)`
}

// capacityArithmetic is the formula itself over $c, $li, $f and $m, leaving the number in
// $a. It measures nothing; every value it reads was taken by capacityReadings or by the
// caller.
func capacityArithmetic() string {
	return `a1=$(( c*3/2 - li - c/8 )); a2=$(( (f-25)/2 )); a3=$(( m/2 )); a=$a1; [ $a2 -lt $a ] && a=$a2; [ $a3 -lt $a ] && a=$a3`
}

// capacityScript is capacityBody with the two readings it wants in front of it and the
// number printed: the whole legacy probe, which `--slots-store ""` still asks for.
func capacityScript(root string) string {
	return `c=$(nproc); li=$(cut -d. -f1 /proc/loadavg); ` + capacityBody(root) + `; echo "$a"`
}

// fixedCapacity is --capacity: the same number for every bench, and no child at all. It is
// what makes a dry run of the tick possible on a bench-less machine.
type fixedCapacity int

func (c fixedCapacity) Capacity(string) (int, error) { return int(c), nil }

// sshCapacity reads one bench's capacity over the same ssh the hand loop used. root is the
// swarm root on the BENCH, whose volume the disk term measures.
type sshCapacity struct{ ssh, root string }

func (c sshCapacity) Capacity(bench string) (int, error) {
	ssh := c.ssh
	if ssh == "" {
		ssh = "ssh"
	}
	testguard.RefuseHosts(ssh, "-n", "-o", "BatchMode=yes", bench, capacityScript(c.root))
	cmd := exec.Command(ssh, "-n", "-o", "BatchMode=yes", bench, capacityScript(c.root))
	var out bytes.Buffer
	said := &tail{}
	cmd.Stdout, cmd.Stderr = &out, said
	if err := cmd.Run(); err != nil {
		return 0, said.wrap(err)
	}
	s := strings.TrimSpace(out.String())
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("capacity on %s answered %q", bench, s)
	}
	return n, nil
}

// defaultCardDeadline is the whole seconds a launched card gets, fill-loop.sh's hardcoded
// 2400. It is --deadline now: one number in the script was the deadline of every card on
// every bench, and a card's deadline is the caller's to set.
const defaultCardDeadline = 2400

// defaultLaunchGrace is how long fill waits on a launcher before it takes the card as
// launched and moves on. Launching used to be cmd.Run(): one tick launched three cards one
// after another and blocked for nine minutes, because the launcher runs the card, not just
// the start of it (dogfood, 2026-09-18). A launcher that fails, fails at once -- a missing
// binary, a refused ssh, a bad argument -- so the grace catches the failure without waiting
// for the work.
const defaultLaunchGrace = 10 * time.Second

// FillIntervalDefault is what a resident fill ticks at unless --interval says otherwise.
// There was no flag at all until #1915, so a freed slot waited up to five minutes for the
// next tick -- the floor on "a machine replaces a finished card right away". The default is
// unchanged; the flag is what makes ten seconds sayable.
const FillIntervalDefault = pulse.FillInterval

// parseInterval reads --interval: how long a resident loop waits between ticks. A value it
// cannot read is a refusal that names the flag, never a silent five minutes.
func parseInterval(s string) (time.Duration, error) {
	if strings.TrimSpace(s) == "" {
		return FillIntervalDefault, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("--interval wants a duration like 10s or 5m, got %q", s)
	}
	if d <= 0 {
		return 0, fmt.Errorf("--interval is more than zero, got %s (a loop that never waits is a fleet nobody can read)", d)
	}
	return d, nil
}

// parseGrace reads --launch-grace: a duration, or 0 to wait for the launcher to finish (the
// old behaviour, which is what a test with an instant launcher wants).
func parseGrace(s string) (time.Duration, error) {
	if strings.TrimSpace(s) == "" {
		return defaultLaunchGrace, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("--launch-grace wants a duration like 10s or 0, got %q", s)
	}
	if d < 0 {
		return 0, fmt.Errorf("--launch-grace is 0 or more, got %s", d)
	}
	return d, nil
}

// flashLauncher hands one moved card to flash-native-bench.sh, fill-loop.sh's per-card
// launcher, or to the program --launcher names.
//
// It starts the child and waits only the grace: a launcher that is still running when the
// grace is up has launched the card, and the bench owns it from there. The child is waited
// on in a goroutine, so it is reaped rather than left a zombie, and it is never killed.
type flashLauncher struct {
	bin      string
	deadline int
	grace    time.Duration
}

func (l flashLauncher) Launch(bench, card string) error {
	bin := l.bin
	if bin == "" {
		bin = "flash-native-bench.sh"
	}
	deadline := l.deadline
	if deadline <= 0 {
		deadline = defaultCardDeadline
	}
	label := strings.TrimSuffix(filepath.Base(card), ".md")
	cmd := exec.Command(bin, bench, "swarm-"+bench, card, label, strconv.Itoa(deadline))
	said := &tail{}
	cmd.Stdout, cmd.Stderr = io.Discard, said
	if err := cmd.Start(); err != nil {
		return said.wrap(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if l.grace <= 0 {
		if err := <-done; err != nil {
			return said.wrap(err)
		}
		return nil
	}
	timer := time.NewTimer(l.grace)
	defer timer.Stop()
	select {
	case err := <-done:
		if err != nil {
			return said.wrap(err)
		}
		return nil
	case <-timer.C:
		return nil
	}
}

// tailBytes is how much of a child's stderr is kept: the last words of a failure are the
// ones that say why, and a child that prints a megabyte still costs this much and no more.
const tailBytes = 4096

// tail keeps the last tailBytes of what is written to it and nothing else. io.Discard was
// there before, and `exit status 255` with the reason thrown away is the whole dogfood
// edge: the FILL NOTE named the code and never the cause.
type tail struct{ buf []byte }

func (t *tail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailBytes {
		t.buf = t.buf[len(t.buf)-tailBytes:]
	}
	return len(p), nil
}

// lastLine is the last non-empty line the child printed, which is where a tool puts its
// reason.
func (t *tail) lastLine() string {
	lines := strings.Split(strings.ReplaceAll(string(t.buf), "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}

// wrap joins the child's exit status to its last line, bounded, so one FILL NOTE says both
// what happened and why.
func (t *tail) wrap(err error) error {
	if line := t.lastLine(); line != "" {
		return fmt.Errorf("%w: %s", err, oneline.Cap(line, oneline.TailBytes))
	}
	return err
}
