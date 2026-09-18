package main

// The fill verb: fill-loop.sh's tick body, once per tick, for every bench. --machines names
// the machines registry, and a --bench whose roles lack `bench` is refused before any ssh:
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
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	novalog "github.com/mas-bandwidth/nova-tools/internal/log"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// fillBenches is the fleet fill-loop.sh fills when the caller names none.
var fillBenches = []string{"hulk", "vision", "space"}

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
	session := f.fs.String("session", "", "")
	once := f.fs.Bool("once", false, "")
	capacity := f.fs.Int("capacity", -1, "")
	launcher := f.fs.String("launcher", "", "")
	deadline := f.fs.Int("deadline", defaultCardDeadline, "")
	grace := f.fs.String("launch-grace", defaultLaunchGrace.String(), "")
	var benches benchFlag
	var only benchFlag
	f.fs.Var(&benches, "bench", "")
	f.fs.Var(&only, "only", "")
	// The structured sink of SPEC-LOGS.md Part 2: stderr by default -- which under systemd
	// is the unit's journal and so a source Alloy already reads -- or the file --log names,
	// which Alloy tails. --label is the fleet name of THIS machine, the bench label every
	// line carries; the bench each tick FILLS is a field of the message, because one fill
	// loop fills many benches from one machine. A --log that cannot be opened is a refusal
	// before a single ssh, never a silent run with no log.
	logPath := f.fs.String("log", "", "")
	label := f.fs.String("label", "", "")

	if !f.parse(args, stderr) {
		return 2
	}
	wait, err := parseGrace(*grace)
	if err != nil {
		f.add(err.Error())
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
	if len(benches) == 0 {
		benches = fillBenches
	}
	var reader pulse.Capacity = sshCapacity{}
	if *capacity >= 0 {
		reader = fixedCapacity(*capacity)
	}
	events, closer, err := novalog.Sink(*logPath, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "FILL REFUSED: --log %s cannot be opened for append: %s\n",
			oneline.Field(*logPath), oneline.Err(err))
		return 2
	}
	if closer != nil {
		defer closer.Close()
	}
	// The emitter's clock is the REAL one and not this run's `now`: a fill loop ticks
	// every five minutes for hours, and a fixed clock would stamp every tick of a night
	// with the minute the process started -- which is a log a query cannot order.
	em := novalog.NewEmitter(events, "nova-pulse", "fill", novalog.BenchName(*label))
	return pulse.Fill(pulse.FillInput{
		Ready:    *ready,
		Launched: *launched,
		Lanes:    *lanes,
		Machines: *machines,
		Session:  *session,
		Benches:  []string(benches),
		Only:     []string(only),
		Once:     *once,
		Stdout:   stdout,
		Stderr:   stderr,
		Now:      func() time.Time { return now },
		Capacity: reader,
		Launcher: flashLauncher{bin: *launcher, deadline: *deadline, grace: wait},
		Events:   em,
	})
}

// capacityScript is fill-loop.sh's formula, run on the bench: the min of core headroom
// (cores*1.5 - load1 - cores/8, a CI reserve), disk headroom ((free_gb-25)/2) and memory
// headroom (memfree_gb/2).
const capacityScript = `c=$(nproc); l=$(cut -d. -f1 /proc/loadavg); f=$(df -BG "$HOME" | awk 'NR==2{gsub("G","",$4); print $4}'); m=$(awk '/MemAvailable/{printf "%d", $2/1048576}' /proc/meminfo); a1=$(( c*3/2 - l - c/8 )); a2=$(( (f-25)/2 )); a3=$(( m/2 )); a=$a1; [ $a2 -lt $a ] && a=$a2; [ $a3 -lt $a ] && a=$a3; echo "$a"`

// fixedCapacity is --capacity: the same number for every bench, and no child at all. It is
// what makes a dry run of the tick possible on a bench-less machine.
type fixedCapacity int

func (c fixedCapacity) Capacity(string) (int, error) { return int(c), nil }

// sshCapacity reads one bench's capacity over the same ssh the hand loop used.
type sshCapacity struct{ ssh string }

func (c sshCapacity) Capacity(bench string) (int, error) {
	ssh := c.ssh
	if ssh == "" {
		ssh = "ssh"
	}
	cmd := exec.Command(ssh, "-n", "-o", "BatchMode=yes", bench, capacityScript)
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
