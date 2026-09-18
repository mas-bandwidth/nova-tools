package main

// The fill verb: fill-loop.sh's tick body, once per tick, for every bench. --machines names
// the machines registry, and a --bench whose roles lack `bench` is refused before any ssh:
// runner hosts are CI-only (Glenn 2026-09-18), and the fill is the path a CARD takes. It reads the
// bench's capacity over ssh, pops that many ready cards, moves them to launched and starts
// `nova-swarm native` for each on the bench. A card's `LANE: <name>` line serializes its
// area: at most one live card per lane, the rest held in order, named by --lanes (default
// queue/control/lanes.tsv). One FILL line per tick.
//
// EVERYTHING THIS FILE DOES TO A MACHINE COMES FROM THE MACHINE'S REGISTRY ROW. The ssh
// target is the row's ssh column -- `air` is a name in a file and `glenn@100.117.59.68` is
// the host -- and the capacity formula is chosen by the row's os, never by probing the
// machine to ask what it is. The schema dogfood on the M2 Air, 2026-09-18, found `fill`
// could not serve a darwin bench BY CONSTRUCTION: it sshed the bench NAME and sent a /proc
// script to a Mac, and both failures came back as `exit status 255` with the reason thrown
// away.
//
// Two flags make the tick runnable without a bench: --capacity <n> is a fixed capacity and
// no ssh at all, and --launcher <path> is the program each card is handed to. Together they
// are a dry run over a directory of cards -- the way the lane logic (FILL HELD) is
// exercised by a hand, not only by a test's injected seam. --dry-run is the other one: it
// reads capacity over ssh for real and launches nothing, which is the one probe a bench
// just added to the registry is worth.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
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
	dryRun := f.fs.Bool("dry-run", false, "")
	swarmRoot := f.fs.String("swarm-root", "", "")
	harness := f.fs.String("harness", "", "")
	model := f.fs.String("model", "", "")
	worker := f.fs.String("worker", "", "")
	sshProgram := f.fs.String("ssh", "", "")
	deadline := f.fs.Int("deadline", defaultCardDeadline, "")
	grace := f.fs.String("launch-grace", defaultLaunchGrace.String(), "")
	var benches benchFlag
	var only benchFlag
	f.fs.Var(&benches, "bench", "")
	f.fs.Var(&only, "only", "")

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
	if *dryRun && *capacity >= 0 {
		f.add("--dry-run and --capacity together: --dry-run exists to read the REAL capacity over ssh, and --capacity is the fixed number that opens no connection; pass one")
	}
	if f.refused(stderr) {
		return 2
	}
	if len(benches) == 0 {
		benches = fillBenches
	}
	var reader pulse.Capacity = sshCapacity{ssh: *sshProgram}
	if *capacity >= 0 {
		reader = fixedCapacity(*capacity)
	}
	return pulse.Fill(pulse.FillInput{
		Ready:    *ready,
		Launched: *launched,
		Lanes:    *lanes,
		Machines: *machines,
		Session:  *session,
		Benches:  []string(benches),
		Only:     []string(only),
		Once:     *once,
		DryRun:   *dryRun,
		Stdout:   stdout,
		Stderr:   stderr,
		Now:      func() time.Time { return now },
		Capacity: reader,
		Launcher: benchLauncher{
			bin: *launcher, ssh: *sshProgram, root: *swarmRoot,
			harness: *harness, model: *model, worker: *worker,
			deadline: *deadline, grace: wait,
		},
	})
}

// linuxCapacityScript is fill-loop.sh's formula, run on the bench: the min of core headroom
// (cores*1.5 - load1 - cores/8, a CI reserve), disk headroom ((free_gb-25)/2) and memory
// headroom (memfree_gb/2).
const linuxCapacityScript = `c=$(nproc); l=$(cut -d. -f1 /proc/loadavg); f=$(df -BG "$HOME" | awk 'NR==2{gsub("G","",$4); print $4}'); m=$(awk '/MemAvailable/{printf "%d", $2/1048576}' /proc/meminfo); a1=$(( c*3/2 - l - c/8 )); a2=$(( (f-25)/2 )); a3=$(( m/2 )); a=$a1; [ $a2 -lt $a ] && a=$a2; [ $a3 -lt $a ] && a=$a3; echo "$a"`

// darwinCapacityScript is the SAME formula over the numbers a Mac actually publishes.
// There is no /proc and no nproc: cores are `sysctl -n hw.ncpu`, load is the second field
// of `sysctl -n vm.loadavg` (which prints `{ 1.86 2.03 2.11 }`), disk is `df -g` (whole
// gigabytes, already a number) and memory is `sysctl -n hw.memsize` in bytes.
//
// darwin has no MemAvailable, so the memory term is the machine's TOTAL halved rather than
// what is free. It is the conservative direction on the machines we have -- an 8 GB Air
// yields 4, under its core term -- and it is written down here rather than left as a
// surprise in the number.
const darwinCapacityScript = `c=$(sysctl -n hw.ncpu); l=$(sysctl -n vm.loadavg | awk '{print $2}' | cut -d. -f1); f=$(df -g "$HOME" | awk 'NR==2{print $4}'); m=$(( $(sysctl -n hw.memsize) / 1073741824 )); a1=$(( c*3/2 - l - c/8 )); a2=$(( (f-25)/2 )); a3=$(( m/2 )); a=$a1; [ $a2 -lt $a ] && a=$a2; [ $a3 -lt $a ] && a=$a3; echo "$a"`

// capacityScript is the formula for one os, chosen by the REGISTRY ROW and never by probing
// the machine: the row is what says what the machine is, and a tool that asks the machine
// has already opened the connection it was deciding about. An os with no formula is a named
// refusal, so a row nobody wrote a formula for fails where a person can read it rather than
// answering `exit status 255` forever.
func capacityScript(goos string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "linux":
		return linuxCapacityScript, nil
	case "darwin":
		return darwinCapacityScript, nil
	}
	return "", fmt.Errorf("no capacity formula for os %s; the machines registry's os/arch column says what the machine is, and fill has a formula for linux and darwin", oneline.Field(dashOr(goos)))
}

// capacityArgv is the whole ssh command the probe runs, built from the row: `-n` so the
// probe never eats a caller's stdin, BatchMode so it fails instead of prompting, the row's
// OWN ssh target, and the formula for the row's os.
func capacityArgv(m fleet.Machine) ([]string, error) {
	script, err := capacityScript(m.OS)
	if err != nil {
		return nil, err
	}
	target := strings.TrimSpace(m.SSH)
	if target == "" {
		return nil, fmt.Errorf("%s has no ssh target in the machines registry; the ssh column is the host, and the bench name is not one", oneline.Field(dashOr(m.Name)))
	}
	return []string{"-n", "-o", "BatchMode=yes", target, script}, nil
}

// dashOr is an empty field printed as `-`, so a refusal never has a hole in it.
func dashOr(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// fixedCapacity is --capacity: the same number for every bench, and no child at all. It is
// what makes a dry run of the tick possible on a bench-less machine.
type fixedCapacity int

func (c fixedCapacity) Capacity(fleet.Machine) (int, error) { return int(c), nil }

// sshCapacity reads one bench's capacity over ssh, at the row's own target and with the
// row's own formula. Its stderr is kept as a bounded tail: `exit status 255` with the reason
// thrown away was the whole dogfood edge, and one NOTE now says both what happened and why.
type sshCapacity struct{ ssh string }

func (c sshCapacity) Capacity(m fleet.Machine) (int, error) {
	argv, err := capacityArgv(m)
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(sshProgramOr(c.ssh), argv...)
	var out bytes.Buffer
	said := &tail{}
	cmd.Stdout, cmd.Stderr = &out, said
	if err := cmd.Run(); err != nil {
		return 0, said.wrap(err)
	}
	s := strings.TrimSpace(out.String())
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("capacity on %s answered %q", oneline.Field(m.Name), oneline.Cap(s, 200))
	}
	return n, nil
}

// sshProgramOr is --ssh, or ssh.
func sshProgramOr(s string) string {
	if strings.TrimSpace(s) == "" {
		return "ssh"
	}
	return s
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

// benchLauncher starts one moved card on its bench.
//
// The default road is `nova-swarm native` ON the bench, over the same ssh the capacity
// probe uses and at the same target the row names. It used to be `flash-native-bench.sh`,
// a name with no path behind it: the script was retired on 2026-09-18 and is on no machine,
// so every launch was a `FILL NOTE ... exec: no such file or directory` (the schema dogfood
// on the M2 Air). `--launcher <path>` is the override and keeps the hand loop's own five
// arguments, for a deployment that still has a script of its own.
//
// It starts the child and waits only the grace: a launcher that is still running when the
// grace is up has launched the card, and the bench owns it from there. The child is waited
// on in a goroutine, so it is reaped rather than left a zombie, and it is never killed.
type benchLauncher struct {
	bin      string // --launcher: a local program, the override road
	ssh      string // --ssh: the ssh program; "" is ssh
	root     string // --swarm-root: the swarm root ON the bench; "" is $HOME/<the row's seat>
	harness  string // --harness: the harness binary on the bench
	model    string // --model: provider/model
	worker   string // --worker: a worker description on the bench, instead of --model
	deadline int
	grace    time.Duration
}

func (l benchLauncher) Launch(m fleet.Machine, card string) error {
	var name string
	var argv []string
	if strings.TrimSpace(l.bin) != "" {
		name, argv = l.overrideArgv(m, card)
	} else {
		// The card lives on the coordinator; the bench runs it. It is copied first,
		// through its own connection, because a copy that fails must fail as a copy and
		// not as a card that ran and vanished.
		remote, err := l.copyCard(m, card)
		if err != nil {
			return err
		}
		argv, err = l.nativeArgv(m, remote)
		if err != nil {
			return err
		}
		name = sshProgramOr(l.ssh)
	}
	cmd := exec.Command(name, argv...)
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

// cardDeadline is --deadline, or the hand loop's 2400.
func (l benchLauncher) cardDeadline() int {
	if l.deadline > 0 {
		return l.deadline
	}
	return defaultCardDeadline
}

// overrideArgv is `--launcher <path>`: the hand loop's own argv, unchanged, with the bench
// name and the seat taken from the registry row rather than built out of the name.
func (l benchLauncher) overrideArgv(m fleet.Machine, card string) (string, []string) {
	label := strings.TrimSuffix(filepath.Base(card), ".md")
	seat := m.Seat
	if strings.TrimSpace(seat) == "" {
		seat = "swarm-" + m.Name
	}
	return l.bin, []string{m.Name, seat, card, label, strconv.Itoa(l.cardDeadline())}
}

// swarmRoot is the swarm root ON the bench: --swarm-root, else `$HOME/<the row's seat>`,
// which is what the seat column has meant since the hand loop passed `swarm-<bench>` as its
// second argument. A row with no seat and no flag is refused rather than guessed at.
func (l benchLauncher) swarmRoot(m fleet.Machine) (string, error) {
	root := strings.TrimSpace(l.root)
	if root == "" {
		if strings.TrimSpace(m.Seat) == "" {
			return "", fmt.Errorf("no swarm root for %s: the registry row names no seat, so there is nothing to derive one from; pass --swarm-root <dir on the bench>", oneline.Field(dashOr(m.Name)))
		}
		root = "$HOME/" + m.Seat
	}
	if strings.ContainsAny(root, " \t'\"$`\\") && !strings.HasPrefix(root, "$HOME/") {
		return "", fmt.Errorf("--swarm-root %s cannot be sent to a shell safely; name a plain absolute path on the bench", oneline.Field(root))
	}
	return root, nil
}

// nativeArgv is the whole ssh command that starts one card: `nova-swarm native` on the
// bench, in the bench's own swarm root, with the card's own slot under it so `harvest
// --bench`'s `<root>/*/jobs/*/` glob finds the job afterwards.
//
// Nothing here is guessed. A missing harness or model is a refusal that names the flag,
// because a launcher that invents them starts a card that cannot run and reports it
// launched.
func (l benchLauncher) nativeArgv(m fleet.Machine, remoteCard string) ([]string, error) {
	harness := strings.TrimSpace(l.harness)
	if harness == "" {
		return nil, fmt.Errorf("no harness for %s: pass --harness <path on the bench>, the binary nova-swarm native runs", oneline.Field(dashOr(m.Name)))
	}
	model, worker := strings.TrimSpace(l.model), strings.TrimSpace(l.worker)
	if model == "" && worker == "" {
		return nil, fmt.Errorf("no model for %s: pass --model <provider/model>, or --worker <description on the bench> which pins one", oneline.Field(dashOr(m.Name)))
	}
	target := strings.TrimSpace(m.SSH)
	if target == "" {
		return nil, fmt.Errorf("%s has no ssh target in the machines registry; the ssh column is the host, and the bench name is not one", oneline.Field(dashOr(m.Name)))
	}
	root, err := l.swarmRoot(m)
	if err != nil {
		return nil, err
	}
	label := strings.TrimSuffix(filepath.Base(remoteCard), ".md")
	script := strings.Join([]string{
		"nova-swarm native",
		"--harness " + shellArg(harness),
		modelFlag(model, worker),
		"--card " + shellArg(remoteCard),
		"--slot " + root + "/" + shellArg(label),
		"--root " + root,
		"--label " + shellArg(label),
		"--deadline " + strconv.Itoa(l.cardDeadline()) + "s",
	}, " ")
	return []string{"-n", "-o", "BatchMode=yes", target, script}, nil
}

// modelFlag is --model, or --worker when a description pins the model instead (issue #881).
func modelFlag(model, worker string) string {
	if worker != "" {
		return "--worker " + shellArg(worker)
	}
	return "--model " + shellArg(model)
}

// copyCard puts the card on the bench and answers where it landed. The card is written
// under the swarm root's own cards directory, by name, so the bench's copy and the
// coordinator's `--launched` copy carry the same filename and a person can match them.
func (l benchLauncher) copyCard(m fleet.Machine, card string) (string, error) {
	root, err := l.swarmRoot(m)
	if err != nil {
		return "", err
	}
	target := strings.TrimSpace(m.SSH)
	if target == "" {
		return "", fmt.Errorf("%s has no ssh target in the machines registry", oneline.Field(dashOr(m.Name)))
	}
	body, err := os.ReadFile(card)
	if err != nil {
		return "", fmt.Errorf("cannot read the card %s: %s", oneline.Field(card), oneline.Err(err))
	}
	base := filepath.Base(card)
	remote := root + "/cards/" + base
	// No `-n` here: the card itself is the stdin, which is the one place in this file
	// where a child is meant to read one.
	cmd := exec.Command(sshProgramOr(l.ssh), "-o", "BatchMode=yes", target,
		"mkdir -p "+root+"/cards && cat > "+root+"/cards/"+shellArg(base))
	cmd.Stdin = bytes.NewReader(body)
	said := &tail{}
	cmd.Stdout, cmd.Stderr = io.Discard, said
	if err := cmd.Run(); err != nil {
		return "", said.wrap(fmt.Errorf("copying %s to %s: %s", oneline.Field(base), oneline.Field(target), oneline.Err(err)))
	}
	return remote, nil
}

// shellArg makes one argument safe for the bench's shell: a plain token is left alone, so
// the command a person reads in a FILL NOTE is the command they would type, and anything
// else is single-quoted. The swarm root is the one thing never passed through here, because
// `$HOME/<seat>` is MEANT to be expanded on the bench; it is validated in swarmRoot instead.
func shellArg(s string) string {
	if s != "" && strings.IndexFunc(s, func(r rune) bool {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return false
		}
		return !strings.ContainsRune("._/@:+=-", r)
	}) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
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
