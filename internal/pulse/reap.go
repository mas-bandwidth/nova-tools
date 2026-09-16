package pulse

// The reaper.
//
// Pit stop 3, bug 10 (issue #828, class E): six `nova-swarm-swarmtest supervise` processes
// leaked by a test lived up to fourteen hours on Space, a twenty-three-hour-old card still
// ran on the Studio, three stale cards sat in launched/ with no job behind them, and test
// binaries under /tmp piled up. Nothing collected any of it, because nothing was asked to.
// The rule that retires the class: a `reap` step each tick, with counts on its one line.
//
//   a process under a swarm root older than the deadline  -> killed
//   a slot lock <root>/<slot>/BATCH whose pid is dead     -> removed
//   a launched card whose job dir is gone, past deadline  -> disposed BY ITS CAUSE (cause.go)
//   a temp dir matching the swarm test glob, past 30 min  -> removed
//
// The fourth line was `requeued once, then failed`, and that blind requeue is class I of the
// same issue: 307 of 834 launches were a card rerun with the same text after a failure
// nothing had read. It now reads the harness log first (cause.go) and the cause chooses:
// a re-cut under a new number carrying the cause line, a bench probe when the bench and not
// the card was wrong, or a failure with a triage packet when the cause has already had its
// remedy. The text it replaces can never launch again (RECUT.tsv, admission.go).
//
// --dry-run changes nothing and prints the same counts, so the reaper can be read before it
// is trusted.
//
// The process table is an interface: the real one runs `ps -eo pid,etime,args` once and the
// tests drive a fake, so no test of this package signals any pid. The reaper never matches
// ITSELF: its own pid, its parent's, and anything whose argv names this tool are skipped —
// on 2026-09-10 a loop that matched its own command line orphaned nineteen shells, and the
// guard here is the memory of it.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/safepath"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// defaultTempGlob is where the swarm's own tests leave their binaries behind.
const defaultTempGlob = "/tmp/*swarmtest*"

// defaultTempAge is how long one of those may live.
const defaultTempAge = 30 * time.Minute

// Proc is one row of the process table: a pid, its age, and the argv as one string.
type Proc struct {
	PID  int
	Age  time.Duration
	Args string
}

// ProcessTable is the machine's processes, and the two things the reaper does with them.
type ProcessTable interface {
	List() ([]Proc, error)
	Alive(pid int) bool
	Kill(pid int) error
}

// ReapInput is the reap verb's input, held apart from flag parsing.
type ReapInput struct {
	Roots    string // comma-separated swarm roots
	Queue    string // the queue directory holding pending, launched and failed
	Deadline time.Duration
	DryRun   bool
	Procs    ProcessTable
	TempGlob string        // default "/tmp/*swarmtest*"; a test points it at its own directory
	TempRoot string        // the directory the temp glob must sit under; empty derives it from the glob's literal prefix
	TempAge  time.Duration // default 30 minutes

	// Rule E2 (runners.go): a runner busy with no in-progress run on Repo for longer than
	// RunnerIdle is restarted through its service. With no Repo and no Runners table the
	// rule does not run and the count is zero: a bench with no self-hosted runner has no
	// jam to clear.
	Repo       string
	Runners    RunnerTable
	Restarter  RunnerRestarter
	RunnerIdle time.Duration // default DefaultRunnerIdle (5 minutes)
	Version    string        // this build's identity, for the CUT stamp a re-cut card carries
	Now        func() time.Time
	Stdout     io.Writer
	Stderr     io.Writer
}

// Reap runs one collection over the roots and the queue and prints one REAP line. It
// returns 0 when it ran, 2 when it could not.
func Reap(in ReapInput) int {
	roots := splitRoots(in.Roots)
	if len(roots) == 0 {
		fmt.Fprintf(in.Stderr, "REAP REFUSED: --roots is required (pass the swarm roots, comma separated)\n")
		return 2
	}
	if info, err := os.Stat(in.Queue); err != nil || !info.IsDir() {
		fmt.Fprintf(in.Stderr, "REAP REFUSED: --queue %s is not a directory (pass the queue directory the cards live in)\n", oneline.Field(in.Queue))
		return 2
	}
	if in.Deadline <= 0 {
		fmt.Fprintf(in.Stderr, "REAP REFUSED: --deadline wants a whole number of seconds (pass the batch deadline a card may not outlive)\n")
		return 2
	}
	now := in.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	tempGlob, tempAge := in.TempGlob, in.TempAge
	if tempGlob == "" {
		tempGlob = defaultTempGlob
	}
	if tempAge <= 0 {
		tempAge = defaultTempAge
	}

	killed := reapProcesses(in, roots)
	locks := reapSlotLocks(in, roots)
	d := reapLaunchedCards(in, roots, now())
	temp := reapTempDirs(in, tempGlob, tempAge, now())
	restarted := reapStuckRunners(in, now())

	fmt.Fprintf(in.Stdout, "REAP roots=%d killed=%d locks=%d requeued=%d recut=%d probe=%d failed=%d triaged=%d temp=%d restarted=%d dry-run=%t\n",
		len(roots), killed, locks, d.requeued, d.recut, d.probe, d.failed, d.triaged, temp, restarted, in.DryRun)
	return 0
}

// disposal is what the reaper did with the launched cards this tick. `requeued` is every
// card that went back to pending, by any route, so the line still answers the question the
// old one answered; `recut` and `probe` say by WHICH route, which is the whole of class I.
type disposal struct{ requeued, recut, probe, failed, triaged int }

// reapProcesses kills every process under a root that has outlived the deadline.
func reapProcesses(in ReapInput, roots []string) int {
	procs, err := in.Procs.List()
	if err != nil {
		fmt.Fprintf(in.Stderr, "REAP NOTE the process table could not be read: %s\n", oneline.Err(err))
		return 0
	}
	self, parent := os.Getpid(), os.Getppid()
	killed := 0
	for _, p := range procs {
		if p.PID <= 0 || p.PID == self || p.PID == parent {
			continue // a loop that finds itself is the bug, not the fix
		}
		if strings.Contains(p.Args, "nova-pulse") {
			continue
		}
		if p.Age < in.Deadline || !underAnyRoot(p.Args, roots) {
			continue
		}
		killed++
		if in.DryRun {
			continue
		}
		if err := in.Procs.Kill(p.PID); err != nil {
			fmt.Fprintf(in.Stderr, "REAP NOTE pid=%d could not be killed: %s\n", p.PID, oneline.Err(err))
		}
	}
	return killed
}

// reapSlotLocks removes every <root>/<slot>/BATCH lock whose pid is dead: the swarm's own
// lock file is the one authority on a busy slot, and a dead lock holds a free slot shut.
func reapSlotLocks(in ReapInput, roots []string) int {
	removed := 0
	for _, root := range roots {
		locks, _ := filepath.Glob(filepath.Join(root, "*", "BATCH"))
		for _, path := range locks {
			pid := lockPID(path)
			if pid <= 0 || in.Procs.Alive(pid) {
				continue
			}
			removed++
			if in.DryRun {
				continue
			}
			if err := os.Remove(path); err != nil {
				fmt.Fprintf(in.Stderr, "REAP NOTE the lock %s could not be removed: %s\n", oneline.Field(path), oneline.Err(err))
			}
		}
	}
	return removed
}

// lockPID reads the pid off a `id=<batch> pid=<n> at=<stamp>` lock line.
func lockPID(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for _, f := range strings.Fields(string(raw)) {
		if v, ok := strings.CutPrefix(f, "pid="); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return 0
			}
			return n
		}
	}
	return 0
}

// reapLaunchedCards disposes every launched card whose job directory is gone and whose
// launch is older than the deadline -- BY ITS CAUSE, never blindly (class I, cause.go).
func reapLaunchedCards(in ReapInput, roots []string, now time.Time) disposal {
	var d disposal
	launched := filepath.Join(in.Queue, "launched")
	cards, _ := filepath.Glob(filepath.Join(launched, "card-*.md"))
	for _, path := range cards {
		name := filepath.Base(path)
		info, err := os.Stat(path)
		if err != nil || now.Sub(info.ModTime()) < in.Deadline {
			continue // still inside the deadline: the batch owns it, not the reaper
		}
		if jobExists(roots, strings.TrimSuffix(name, ".md")) {
			continue
		}
		log, resultPresent := HarnessEvidence(in.Queue, name)
		sig := Read(log, resultPresent)
		action, why := Decide(sig, PriorKinds(in.Queue, name))
		switch action {
		case ActionProbe:
			// The bench, not the card. The text is untouched and goes back to pending: a
			// card re-cut because go was missing from a worker's PATH is a card changed for
			// no reason, and that is how 174 of them failed a second time.
			d.requeued++
			d.probe++
			if in.DryRun {
				continue
			}
			recordProbe(in, name, sig, now)
			recordAttempt(in, name, sig, now)
			moveCard(in, path, filepath.Join(in.Queue, "pending", name))
		case ActionRecut:
			d.requeued++
			d.recut++
			if in.DryRun {
				continue
			}
			out, err := Recut(RecutInput{Queue: in.Queue, CardPath: path, Sig: sig, Version: in.Version, Now: func() time.Time { return now }})
			if err != nil {
				fmt.Fprintf(in.Stderr, "REAP NOTE %s could not be re-cut: %s\n", oneline.Field(name), oneline.Err(err))
				continue
			}
			_ = out // the re-cut is recorded in RECUT.tsv and counted on the REAP line; the
			// reaper prints ONE line whatever it collected, which is the whole point of it.
			moveCard(in, path, filepath.Join(in.Queue, "recut", name))
		default:
			// ActionFail and ActionTriage: the cause has had its remedy, or no rule reads
			// this signature. Either way the decision leaves the loop as a packet on the
			// text route and never as another launch.
			d.failed++
			if in.DryRun {
				d.triaged++
				continue
			}
			recordAttempt(in, name, sig, now)
			if cutTriagePacket(in, name, sig, log, why) {
				d.triaged++
			}
			moveCard(in, path, filepath.Join(in.Queue, "failed", name))
		}
	}
	return d
}

// recordAttempt writes one row of a card's own failure history, which is what makes the
// SECOND failure of a cause visible to the next tick.
func recordAttempt(in ReapInput, name string, sig Signature, now time.Time) {
	row := AttemptRow{At: now.UTC().Format(time.RFC3339), Kind: sig.Kind, Line: sig.Line}
	if err := AppendAttempt(in.Queue, name, row); err != nil {
		fmt.Fprintf(in.Stderr, "REAP NOTE the attempt row for %s could not be written: %s\n", oneline.Field(name), oneline.Err(err))
	}
}

// recordProbe appends one row to <queue>/PROBE.tsv: a bench or a route to probe, named by
// the card that found it. It is a row and not a note, because a person reading a note about
// a missing toolchain is a person doing a test's job.
func recordProbe(in ReapInput, name string, sig Signature, now time.Time) {
	if err := os.MkdirAll(in.Queue, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(in.Queue, "PROBE.tsv"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(in.Stderr, "REAP NOTE the probe row for %s could not be written: %s\n", oneline.Field(name), oneline.Err(err))
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\t%s\t%s\n", oneline.Field(now.UTC().Format(time.RFC3339)), oneline.Field(name), oneline.Field(sig.Kind))
}

// evidenceTail is how much of a harness log a triage packet is given. The packet's own
// ceiling is PacketMax; this is the ceiling on what is offered to it, so a ten-megabyte log
// is never read into a card's memory to be thrown away a line later.
const evidenceTail = 4000

// cutTriagePacket writes the case's evidence and cuts the packet the text route decides on
// (triage.go). It reports whether a packet was written.
func cutTriagePacket(in ReapInput, name string, sig Signature, log []byte, why string) bool {
	kind := TriageCase(sig.Kind)
	dir := filepath.Join(in.Queue, "UNDECIDED")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintf(in.Stderr, "REAP NOTE the evidence for %s could not be written: %s\n", oneline.Field(name), oneline.Err(err))
		return false
	}
	tail := string(log)
	if len(tail) > evidenceTail {
		tail = tail[len(tail)-evidenceTail:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "card %s reaped past the deadline with no job directory\n", name)
	if why != "" {
		fmt.Fprintf(&b, "REFUSED: %s\n", why)
	}
	b.WriteString(tail)
	if err := os.WriteFile(filepath.Join(dir, kind+".txt"), []byte(b.String()), 0o644); err != nil {
		fmt.Fprintf(in.Stderr, "REAP NOTE the evidence for %s could not be written: %s\n", oneline.Field(name), oneline.Err(err))
		return false
	}
	out := filepath.Join(in.Queue, "triage", "triage-"+kind+"-"+strings.TrimSuffix(name, ".md")+".md")
	var quiet strings.Builder
	if code := Triage(TriageInput{Case: kind, Queue: in.Queue, Out: out, Ref: name, Stdout: &quiet, Stderr: in.Stderr}); code != 0 {
		return false
	}
	return true
}

// jobExists says whether any bench still holds this card's job directory.
func jobExists(roots []string, label string) bool {
	for _, root := range roots {
		if matches, _ := filepath.Glob(filepath.Join(root, "*", "jobs", label)); len(matches) > 0 {
			return true
		}
	}
	return false
}

func moveCard(in ReapInput, from, to string) {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		fmt.Fprintf(in.Stderr, "REAP NOTE %s could not be moved: %s\n", oneline.Field(filepath.Base(from)), oneline.Err(err))
		return
	}
	if err := os.Rename(from, to); err != nil {
		fmt.Fprintf(in.Stderr, "REAP NOTE %s could not be moved: %s\n", oneline.Field(filepath.Base(from)), oneline.Err(err))
	}
}

// reapTempDirs removes the swarm test directories older than the temp age. The glob
// comes from a flag, so it is the one match the reaper cannot trust to name a path
// under anything: the root the matches must sit under is named by --temp-root, or
// derived from the glob's own literal prefix, and a glob whose prefix escapes that root
// is refused whole. Each match is removed through safepath.RemoveUnder, which refuses a
// path that is the root, outside it, or a symlink.
func reapTempDirs(in ReapInput, glob string, age time.Duration, now time.Time) int {
	matches, err := filepath.Glob(glob)
	if err != nil {
		fmt.Fprintf(in.Stderr, "REAP NOTE the temp glob %s does not parse: %s\n", oneline.Field(glob), oneline.Err(err))
		return 0
	}
	root := in.TempRoot
	if strings.TrimSpace(root) == "" {
		root = globRoot(glob)
	} else if !dirUnder(root, globRoot(glob)) {
		fmt.Fprintf(in.Stderr, "REAP REFUSED the temp glob %s is not under its root %s; pass --temp-root naming the directory the glob must sit under\n",
			oneline.Field(glob), oneline.Field(root))
		return 0
	}
	removed := 0
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil || now.Sub(info.ModTime()) < age {
			continue
		}
		removed++
		if in.DryRun {
			continue
		}
		if err := safepath.RemoveUnder(root, path); err != nil {
			fmt.Fprintf(in.Stderr, "REAP NOTE %s could not be removed: %s\n", oneline.Field(path), oneline.Err(err))
		}
	}
	return removed
}

// globRoot is the directory a glob's matches must sit under: the literal text before the
// first metacharacter, with a trailing partial element removed. `/tmp/*swarmtest*` roots
// at `/tmp`; `/*` roots at `/`, which RemoveUnder then refuses.
func globRoot(pattern string) string {
	cut := strings.IndexAny(pattern, "*?[")
	if cut < 0 {
		return filepath.Dir(pattern)
	}
	lit := pattern[:cut]
	if lit == "" {
		return "."
	}
	if strings.HasSuffix(lit, string(os.PathSeparator)) {
		return filepath.Clean(lit)
	}
	return filepath.Dir(lit)
}

// dirUnder reports whether dir is root or strictly below it.
func dirUnder(root, dir string) bool {
	r, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	d, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	r, d = filepath.Clean(r), filepath.Clean(d)
	return d == r || strings.HasPrefix(d, r+string(os.PathSeparator))
}

func underAnyRoot(args string, roots []string) bool {
	for _, root := range roots {
		if root != "" && strings.Contains(args, root) {
			return true
		}
	}
	return false
}

func splitRoots(s string) []string {
	var out []string
	for _, r := range strings.Split(s, ",") {
		if r = strings.TrimSpace(r); r != "" {
			out = append(out, r)
		}
	}
	return out
}

// OSProcs is the real process table: one bounded listing, and a signal per overdue pid.
type OSProcs struct{ Timeout time.Duration }

func (o OSProcs) List() ([]Proc, error) {
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-eo", "pid,etime,args").Output()
	if err != nil {
		return nil, fmt.Errorf("the process table could not be listed: %w", err)
	}
	var procs []Proc
	for i, line := range strings.Split(string(out), "\n") {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		age, ok := parseETime(fields[1])
		if !ok {
			continue
		}
		procs = append(procs, Proc{PID: pid, Age: age, Args: strings.Join(fields[2:], " ")})
	}
	return procs, nil
}

func (o OSProcs) Alive(pid int) bool { return swarm.Alive(pid, "") }

func (o OSProcs) Kill(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// parseETime reads ps's elapsed time: [[dd-]hh:]mm:ss.
func parseETime(s string) (time.Duration, bool) {
	days := 0
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil {
			return 0, false
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, false
	}
	var nums []int
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0, false
		}
		nums = append(nums, n)
	}
	hours, minutes, seconds := 0, nums[0], nums[1]
	if len(nums) == 3 {
		hours, minutes, seconds = nums[0], nums[1], nums[2]
	}
	return time.Duration(days)*24*time.Hour + time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute + time.Duration(seconds)*time.Second, true
}
