package pulse

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// LAUNCH IS THE HAND LOOP, IN THE TOOL (bin/pulse-loop.sh, the shim this verb replaces).
//
// It hands the cards to `nova-swarm batch` in its CARD form -- --id --cards --deadline
// --root [--runner|--harness] [--benches --bench] [--slots] -- which is the only form that
// runs a card; the pool form it used to call wants --files and --tokens and has no runner,
// so no card ever started (issue #630).
//
// A SLOT IS FREE WHEN THE BATCH LOCK SAYS SO, NEVER WHEN A PROCESS PROBE SAYS SO. Between a
// card's clone and its first harness line there is no process and the slot is locked; a
// launcher that probed processes handed that slot out and the batch refused the card at
// admission -- ABSTAIN reason=admission slot=<n> held-by=<batch> -- which cost half of one
// shift's cards on 2026-09-16. The lock is <root>/<slot>/BATCH and the predicate is
// swarm.FreeSlots, shared with the batch that writes it. Better still, the allocation is the
// batch's own: launch hands it the range and every card's slot column is "-", so nothing can
// change between the check and the lock.

// LaunchInput is everything the launch verb needs, held apart from the command-line parsing
// so a test can drive it with fake directories and a real nova-swarm binary on PATH.
type LaunchInput struct {
	Cards    string // path to cards.tsv: label<TAB>slot<TAB>model<TAB>card
	Root     string // the swarm root: <root>/<slot>/jobs/<label>/RESULT.md
	Deadline string // the whole pulse's deadline, whole seconds
	Slots    string // the slot range batch allocates from: "<n>" or "<lo>-<hi>"
	Benches  string // the benches table passed straight through to batch
	Bench    string // comma-separated bench names, passed straight through to batch
	ID       string // the pulse id; empty mints one
	Runner   string // an optional runner script; empty runs the batch's own native
	Harness  string // the harness a runnerless batch runs native with
	Auth     string // the harness auth file
	Idle     int    // per-card idle seconds, passed through to batch
	Check    int    // seconds after which a detached `check` counts the started cards; 0 none
	Max      int    // bounded output cap on SKIPPED lines
	Swarm    string // the nova-swarm binary; empty resolves "nova-swarm" on PATH
	Stdout   io.Writer
	Stderr   io.Writer
	Now      func() time.Time
}

// cardMinAge is rule 8's youngest card: a card file written less than this ago may still be
// half-written by the cutter, and a half card admits under the wrong line 1.
const cardMinAge = 5 * time.Second

// defaultCheckAfter is when the launch check counts started cards. A job directory is made
// before the harness's first line, so 90 s is long enough that a slow clone is not dead and
// short enough that a dead batch is caught inside one tick.
const defaultCheckAfter = 90

// sshDeadline bounds the ONE ssh a bench probe costs. A bench that has not answered in this
// long is a bench with no free slots: nothing is assumed free when the probe fails.
const sshDeadline = 25 * time.Second

// Launch fills the free slots of this root with the cards that are ready and returns the
// process exit code: 0 when a batch was admitted, 2 when it was refused.
func Launch(in LaunchInput) int {
	if in.Now == nil {
		in.Now = time.Now
	}
	if in.Max < 0 {
		in.Max = 0
	}
	if err := os.MkdirAll(in.Root, 0o755); err != nil {
		return launchRefused(in.Stderr, "root", oneline.Err(err))
	}
	// ONE LAUNCH PER ROOT. Two launches racing over one root's free slots is the collision
	// the lock predicate exists to prevent, and it is cheap to say so by pid.
	release, pid, err := takePID(filepath.Join(in.Root, "pulse.pid"))
	if err != nil {
		return launchRefused(in.Stderr, "root", oneline.Err(err))
	}
	if release == nil {
		fmt.Fprintf(in.Stderr, "LAUNCH REFUSED reason=running pid=%d (one launch per root; wait for it, or kill that pid)\n", pid)
		return 2
	}
	defer release()

	cards, err := readCards(in.Cards)
	if err != nil {
		return launchRefused(in.Stderr, "cards", oneline.Err(err))
	}
	if len(cards) == 0 {
		fmt.Fprintf(in.Stderr, "LAUNCH REFUSED reason=no-card cards=%s (a pulse of no cards is a typo)\n", oneline.Field(in.Cards))
		return 2
	}

	// A card that is empty, or younger than five seconds, is skipped by name: it is a card
	// the cutter may still be writing, and a half card admits under the wrong line 1.
	skipped := bounded.Capped(in.Stderr, in.Max, "LAUNCH", "skipped", "use --max 0 to show all")
	var ready []CardRow
	for _, c := range cards {
		if why := cardNotReady(c.Card, in.Now()); why != "" {
			skipped.Line(fmt.Sprintf("LAUNCH SKIPPED label=%s reason=%s", field(c.Label), why))
			continue
		}
		ready = append(ready, c)
	}
	skipped.More()
	if len(ready) == 0 {
		fmt.Fprintf(in.Stderr, "LAUNCH REFUSED reason=no-card-ready cards=%s (every card was empty or younger than %s)\n",
			oneline.Field(in.Cards), cardMinAge)
		return 2
	}

	lo, hi, err := swarm.ParseSlotRange(in.Slots)
	if err != nil {
		return launchRefused(in.Stderr, "slots", oneline.Err(err))
	}
	if hi == 0 {
		hi = lo + len(ready) - 1
	}
	free, why := freeSlotCount(in, lo, hi)
	if why != "" {
		fmt.Fprintf(in.Stderr, "LAUNCH REFUSED reason=%s slots=%d-%d (the bench did not answer in %s; nothing is assumed free)\n",
			why, lo, hi, sshDeadline)
		return 2
	}
	if free == 0 {
		fmt.Fprintf(in.Stderr, "LAUNCH REFUSED reason=no-free-slot slots=%d-%d cards=%d (every slot in the range holds a live BATCH lock)\n",
			lo, hi, len(ready))
		return 2
	}
	if len(ready) > free {
		ready = ready[:free]
	}

	id := strings.TrimSpace(in.ID)
	if id == "" {
		id = swarm.NewID(in.Now(), "pulse")
	}

	// The cards handed to the batch carry "-" in the slot column: the batch allocates under
	// its own lock, so no slot can be taken between the count above and the lock below.
	tsv := filepath.Join(in.Root, "launch-"+id+".tsv")
	if err := writeBatchCards(tsv, ready); err != nil {
		return launchRefused(in.Stderr, "cards", oneline.Err(err))
	}
	if err := recordLaunch(in.Root, id, in.Bench, ready, in.Now()); err != nil {
		return launchRefused(in.Stderr, "root", oneline.Err(err))
	}

	if rc := startBatch(in, id, tsv, lo, hi); rc != 0 {
		return rc
	}

	fmt.Fprintf(in.Stdout, "LAUNCH OK id=%s bench=%s cards=%d slots=%d-%d free=%d deadline=%s\n",
		field(id), field(in.Bench), len(ready), lo, hi, free, field(in.Deadline))

	if in.Check > 0 {
		startCheck(in, id)
	}
	return 0
}

func launchRefused(w io.Writer, what, why string) int {
	fmt.Fprintf(w, "LAUNCH REFUSED reason=%s %s\n", what, why)
	return 2
}

// cardNotReady names why a card may not be admitted yet, or "" when it may.
func cardNotReady(path string, now time.Time) string {
	fi, err := os.Stat(path)
	if err != nil {
		return "card-unreadable"
	}
	if fi.Size() == 0 {
		return "card-empty"
	}
	if now.Sub(fi.ModTime()) < cardMinAge {
		return "card-young"
	}
	return ""
}

// freeSlotCount counts the free slots in the range: locally by the batch's own lock, and on
// a bench by ONE ssh with a deadline. A probe that does not answer returns a reason, and
// nothing is assumed free.
func freeSlotCount(in LaunchInput, lo, hi int) (int, string) {
	if strings.TrimSpace(in.Bench) == "" {
		return len(swarm.FreeSlots(in.Root, lo, hi)), ""
	}
	host, root, err := benchHostRoot(in.Benches, in.Bench)
	if err != nil {
		return 0, "bench-unknown"
	}
	held, ok := benchHeldSlots(host, root, lo, hi)
	if !ok {
		return 0, "bench-silent"
	}
	return (hi - lo + 1) - held, ""
}

// benchHostRoot reads the benches table for the first named bench's host and root.
func benchHostRoot(benchesPath, benchNames string) (string, string, error) {
	name := strings.TrimSpace(strings.SplitN(benchNames, ",", 2)[0])
	table, err := swarm.LoadBenchTable(benchesPath)
	if err != nil {
		return "", "", err
	}
	for _, b := range table {
		if b.Name == name {
			return b.Host, b.Root, nil
		}
	}
	return "", "", fmt.Errorf("no bench %s in %s", name, benchesPath)
}

// benchHeldSlots asks the bench, in ONE ssh, how many slots in the range hold a live BATCH
// lock. The remote script prints one line per held slot and then END; no END is no answer.
func benchHeldSlots(host, root string, lo, hi int) (int, bool) {
	script := fmt.Sprintf(
		`for n in $(seq %d %d); do f=%s/$n/BATCH; [ -f "$f" ] || continue; p=$(tr ' ' '\n' < "$f" | sed -n 's/^pid=//p' | head -1); [ -n "$p" ] && kill -0 "$p" 2>/dev/null && echo HELD; done; echo END`,
		lo, hi, root)
	cmd := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", host, script)
	out, err := runWithDeadline(cmd, sshDeadline)
	if err != nil && !strings.Contains(string(out), "END") {
		return 0, false
	}
	text := string(out)
	if !strings.Contains(text, "END") {
		return 0, false
	}
	held := 0
	for _, l := range strings.Split(text, "\n") {
		if strings.TrimSpace(l) == "HELD" {
			held++
		}
	}
	return held, true
}

// runWithDeadline runs a command and kills it at the deadline, returning what it printed.
func runWithDeadline(cmd *exec.Cmd, d time.Duration) ([]byte, error) {
	type res struct {
		out []byte
		err error
	}
	ch := make(chan res, 1)
	go func() {
		out, err := cmd.CombinedOutput()
		ch <- res{out, err}
	}()
	select {
	case r := <-ch:
		return r.out, r.err
	case <-time.After(d):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil, fmt.Errorf("the probe did not answer in %s", d)
	}
}

// writeBatchCards writes the TSV the batch reads: label, slot, model, card. The slot column
// is "-" for every card, so the batch allocates under its own lock.
func writeBatchCards(path string, cards []CardRow) error {
	var b strings.Builder
	for _, c := range cards {
		b.WriteString(fmt.Sprintf("%s\t%s\t%s\t%s\n", c.Label, SlotDash, c.Model, c.Card))
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// recordLaunch appends one row per card to <root>/launch.tsv:
// id, label, slot, bench, model, card sha12, stamp. The slot is "-": the batch allocates it,
// and `check` and `harvest` find the job by label under any slot.
func recordLaunch(root, id, bench string, cards []CardRow, now time.Time) error {
	f, err := os.OpenFile(filepath.Join(root, "launch.tsv"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	stamp := now.UTC().Format("2006-01-02T15:04:05Z")
	for _, c := range cards {
		if _, err := fmt.Fprintf(f, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			id, c.Label, SlotDash, dashField(bench), c.Model, cardSHA(c.Card), stamp); err != nil {
			return err
		}
	}
	return nil
}

func dashField(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// cardSHA is the sha12 of a card's bytes, the same digest its contract line carries.
func cardSHA(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "-"
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:12]
}

// startBatch starts `nova-swarm batch` in its card form, detached, with its output in
// <root>/batch-<id>.out. It does not wait: the batch holds the deadline, and a launch that
// waited for it would hold the window for the whole pulse.
func startBatch(in LaunchInput, id, tsv string, lo, hi int) int {
	bin := in.Swarm
	if bin == "" {
		bin = "nova-swarm"
	}
	argv := []string{"batch",
		"--id", id,
		"--cards", tsv,
		"--deadline", in.Deadline,
		"--root", in.Root,
		"--slots", fmt.Sprintf("%d-%d", lo, hi),
	}
	if in.Runner != "" {
		argv = append(argv, "--runner", in.Runner)
	}
	if in.Harness != "" {
		argv = append(argv, "--harness", in.Harness)
	}
	if in.Auth != "" {
		argv = append(argv, "--auth", in.Auth)
	}
	if in.Benches != "" {
		argv = append(argv, "--benches", in.Benches)
	}
	if in.Bench != "" {
		argv = append(argv, "--bench", in.Bench)
	}
	if in.Idle > 0 {
		argv = append(argv, "--idle", strconv.Itoa(in.Idle))
	}
	outPath := filepath.Join(in.Root, "batch-"+id+".out")
	logFile, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return launchRefused(in.Stderr, "root", oneline.Err(err))
	}
	defer logFile.Close()
	cmd := exec.Command(bin, argv...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return launchRefused(in.Stderr, "swarm", oneline.Err(err))
	}
	// The child is released, not waited on: its own deadline ends it, and `check` reads
	// what it did. Reaping it here would hold this verb for the whole pulse.
	go func() { _ = cmd.Wait() }()
	return 0
}

// startCheck starts this binary's own `check` verb, detached, so the launch check lands
// <in.Check> seconds later without holding the caller.
func startCheck(in LaunchInput, id string) {
	self, err := os.Executable()
	if err != nil {
		return
	}
	logPath := filepath.Join(in.Root, "pulse.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer logFile.Close()
	cmd := exec.Command(self, "check", "--id", id, "--root", in.Root,
		"--after", strconv.Itoa(in.Check))
	if in.Benches != "" {
		cmd.Args = append(cmd.Args, "--benches", in.Benches)
	}
	if in.Bench != "" {
		cmd.Args = append(cmd.Args, "--bench", in.Bench)
	}
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return
	}
	go func() { _ = cmd.Wait() }()
}

// takePID takes the one-launch-per-root pid file. It returns a release when this process
// holds it, and the live holder's pid when another one does.
func takePID(path string) (func(), int, error) {
	if raw, err := os.ReadFile(path); err == nil {
		if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 0 && swarm.Alive(pid, "") {
			return nil, pid, nil
		}
	}
	if err := os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return nil, 0, err
	}
	return func() { _ = os.Remove(path) }, 0, nil
}

// CheckInput is the launch check: <After> seconds after a launch, every card of the pulse
// must have a job directory, which the runner makes before the harness's first line.
type CheckInput struct {
	ID      string
	Root    string
	After   int
	Benches string
	Bench   string
	Stdout  io.Writer
	Stderr  io.Writer
	Sleep   func(time.Duration)
}

// Check counts the cards of a pulse that started and prints exactly one line:
// LAUNCH-OK, LAUNCH-DEAD (no card started: the runner or admission refused them all), or
// LAUNCH-UNKNOWN (a bench that did not answer is not declared dead).
func Check(in CheckInput) int {
	if in.Sleep == nil {
		in.Sleep = time.Sleep
	}
	rows, err := readLaunchTSV(filepath.Join(in.Root, "launch.tsv"), in.ID)
	if err != nil {
		return refusal(in.Stderr, "CHECK", err)
	}
	if len(rows) == 0 {
		fmt.Fprintf(in.Stderr, "CHECK REFUSED reason=no-row id=%s (launch.tsv holds no card for this pulse)\n", field(in.ID))
		return 2
	}
	if in.After > 0 {
		in.Sleep(time.Duration(in.After) * time.Second)
	}
	bench := strings.TrimSpace(in.Bench)
	total := len(rows)
	started := 0
	if bench == "" {
		for _, r := range rows {
			if jobStarted(in.Root, r.Label) {
				started++
			}
		}
	} else {
		host, root, err := benchHostRoot(in.Benches, bench)
		if err != nil {
			fmt.Fprintf(in.Stdout, "LAUNCH-UNKNOWN id=%s bench=%s cards=%d: the bench is not in the table; not declared dead\n",
				field(in.ID), field(bench), total)
			return 0
		}
		n, ok := benchStarted(host, root, rows)
		if !ok {
			fmt.Fprintf(in.Stdout, "LAUNCH-UNKNOWN id=%s bench=%s cards=%d: the %s probe over ssh did not answer; not declared dead\n",
				field(in.ID), field(bench), total, sshDeadline)
			return 0
		}
		started = n
	}
	if started == 0 {
		fmt.Fprintf(in.Stdout, "LAUNCH-DEAD id=%s bench=%s cards=%d started=0: no card started; read %s\n",
			field(in.ID), field(dashField(bench)), total, oneline.Field(filepath.Join(in.Root, "batch-"+in.ID+".out")))
		return 1
	}
	fmt.Fprintf(in.Stdout, "LAUNCH-OK id=%s bench=%s started=%d/%d\n",
		field(in.ID), field(dashField(bench)), started, total)
	return 0
}

// jobStarted reports whether a card's job directory exists under any slot of the root. The
// runner makes it before the harness's first line, so its absence -- not a missing NATIVE
// line, which only comes at the end -- is what a dead launch looks like.
func jobStarted(root, label string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if fi, err := os.Stat(filepath.Join(root, e.Name(), "jobs", label)); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

// benchStarted asks the bench, in ONE ssh, which of the pulse's labels have a job directory.
func benchStarted(host, root string, rows []LaunchRow) (int, bool) {
	var b strings.Builder
	for _, r := range rows {
		fmt.Fprintf(&b, "ls -d %s/*/jobs/%s >/dev/null 2>&1 && echo STARTED; ", root, r.Label)
	}
	b.WriteString("echo END")
	cmd := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", host, b.String())
	out, _ := runWithDeadline(cmd, sshDeadline)
	if !strings.Contains(string(out), "END") {
		return 0, false
	}
	n := 0
	for _, l := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(l) == "STARTED" {
			n++
		}
	}
	return n, true
}

// LaunchRow is one row of launch.tsv: the pulse id, the card's label, the slot ("-" when the
// batch allocated it), the bench, the model, the card's sha12 and the launch stamp.
type LaunchRow struct {
	ID    string
	Label string
	Slot  string
	Bench string
	Model string
	SHA   string
	Stamp string
}

// readLaunchTSV reads <root>/launch.tsv, keeping the rows of one pulse when id is not empty.
func readLaunchTSV(path, id string) ([]LaunchRow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s (launch writes it; run: nova-pulse launch)", path)
	}
	var out []LaunchRow
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		p := strings.Split(line, "\t")
		if len(p) != 7 {
			return nil, fmt.Errorf("%s line %d wants id, label, slot, bench, model, sha, stamp, got %d fields", path, i+1, len(p))
		}
		row := LaunchRow{ID: p[0], Label: p[1], Slot: p[2], Bench: p[3], Model: p[4], SHA: p[5], Stamp: p[6]}
		if id != "" && row.ID != id {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}
