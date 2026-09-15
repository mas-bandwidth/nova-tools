package swarm

// Batch: scatter, wait, gather (SPEC-SWARM.md, "Batch: scatter, wait, gather").
//
// A batch is one id and one deadline held across n cards. Scatter starts one runner
// process per card; wait ends every card or the deadline, whatever comes first, and the
// machinery kills the stragglers rather than waiting for them; gather folds the batch into
// one bounded packet with no report body in it.
//
// A card's result is RESULT.md under <root>/<slot>/jobs/<label>/RESULT.md. Line 1 must be
// the contract line the card was admitted under -- line 1 of the card's own text file --
// or the card is an ABSTAIN row, never folded. A missing result is an ABSTAIN row too.
// Line 2 is the card's disposition and is the only finding-adjacent text the packet ever
// carries: one line, capped, one per card.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// admitError is a card-shape refusal found while admitting a card. It is distinct from a
// plain read error because it carries the label and the reason and prints its own line:
// ADMIT REFUSED <label> card-shape: <reason>, citing docs/WORKER-CARDS.md practice 17.
type admitError struct {
	label  string
	reason string
}

func (e *admitError) Error() string {
	return fmt.Sprintf("ADMIT REFUSED %s card-shape: %s", oneline.Field(e.label), e.reason)
}

// BatchInput is everything the batch scatter/wait/gather needs, held apart from the
// command-line parsing so a test can drive it with a fake runner script.
type BatchInput struct {
	ID       string        // the batch id, printed on the BATCH line
	Deadline time.Duration // the whole batch's own deadline
	Idle     time.Duration // per-card idle timeout: a card's log not growing this long is killed
	Cards    string        // path to the TSV: label \t slot \t model \t card-path
	Root     string        // the root a card's RESULT.md hangs under
	Runner   string        // the command, one process per card
	Benches  string        // path to the benches table; empty means no table is read
	Bench    string        // comma-separated bench names to allocate the cards across; empty means local only
	Stdout   io.Writer
	Stderr   io.Writer
}

// batchCard is one admitted card, in TSV order. slot is zero while the card asked for a
// slot allocation (its slot column was empty or '-'), and a positive number once assignSlots
// has either kept the name the card asked for or allocated the lowest free one.
type batchCard struct {
	label    string
	slot     int
	bench    string // the bench this card runs on; empty names the local machine
	model    string
	cardPath string
	contract string // line 1 of the card's text, the line by which it was admitted
}

// maxHoldLines is the HOLD ceiling. A packet is BATCH + n card lines + HOLD lines, and the
// spec bounds the packet at n + 12 lines: one BATCH line and at most eleven HOLD lines.
const maxHoldLines = 11

// idlePollInterval is how often the batch re-reads a card's log size while waiting, in
// search of a card whose log has stopped growing. It is short enough that an idle kill lands
// close to the timeout and long enough that it does not busy-spin over n files.
const idlePollInterval = 100 * time.Millisecond

// Batch runs one batch through scatter, wait and gather and returns the process exit code:
// 0 only when every card was done and none held, 1 otherwise, 2 when the admission could
// not even be read.
func Batch(in BatchInput) int {
	absroot, err := filepath.Abs(in.Root)
	if err != nil {
		fmt.Fprintf(in.Stderr, "nova-swarm batch: %s\n", oneline.Err(err))
		return 2
	}
	in.Root = absroot
	cards, err := readCards(in.Cards)
	if err != nil {
		var ae *admitError
		var ar *admitRefusal
		if errors.As(err, &ae) {
			fmt.Fprintln(in.Stderr, ae.Error())
		} else if errors.As(err, &ar) {
			fmt.Fprintln(in.Stderr, ar.Error())
		} else {
			fmt.Fprintf(in.Stderr, "nova-swarm batch: %s\n", oneline.Err(err))
		}
		return 2
	}
	if len(cards) == 0 {
		fmt.Fprintf(in.Stderr, "BATCH REFUSED: %s holds no card; a batch of no cards is a typo\n", oneline.Field(in.Cards))
		return 1
	}
	if in.Runner == "" && in.Bench == "" && !anyCardNamesBench(cards) {
		fmt.Fprintln(in.Stderr, "nova-swarm batch: --runner is required; it wants the command one process per card runs")
		return 2
	}
	benches := map[string]Bench{}
	if in.Bench != "" || anyCardNamesBench(cards) {
		var err error
		benches, err = allocateBenches(cards, in.Benches, in.Bench)
		if err != nil {
			fmt.Fprintln(in.Stderr, err)
			return 2
		}
	} else if err := assignSlots(cards, in.Root); err != nil {
		fmt.Fprintln(in.Stderr, err)
		return 1
	}

	// scatter: one runner process per card, in TSV order. The job directory is made before
	// the process starts so a runner can write RESULT.md straight into place.
	type proc struct {
		cmd        *exec.Cmd
		slot       int
		label      string
		idleLog    string // the file the idle monitor watched; set only on an idle kill
		done       bool   // guarded by doneMu
		idleKilled bool   // guarded by doneMu
		rc         int    // the child's exit code; guarded by doneMu
		lastGrow   time.Time
	}
	var doneMu sync.Mutex
	procs := make([]proc, len(cards))
	for i, c := range cards {
		// A remote card's job directory sits under <root>/<bench>-<n>/jobs/<label>; a local
		// card's under <root>/<n>/jobs/<label>, as today.
		job := filepath.Join(in.Root, scratchName(c), "jobs", c.label)
		if err := os.MkdirAll(job, 0o755); err != nil {
			fmt.Fprintf(in.Stderr, "nova-swarm batch: %s\n", oneline.Err(err))
			return 2
		}
		// A card's log is the runner's own stdout pinned to a regular file under the job, the
		// way the spec records a job: harness.log. Idle means this file stopped growing.
		logPath := filepath.Join(job, "harness.log")
		logFile, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			fmt.Fprintf(in.Stderr, "nova-swarm batch: %s\n", oneline.Err(err))
			return 2
		}
		var cmd *exec.Cmd
		if c.bench != "" {
			// On a remote bench the batch builds the native command itself: ssh <host>
			// [taskset -c <core>] <root>/bin/nova-swarm native ..., with the card copied first.
			cmd, err = remoteRun(c, benches[c.bench], in.Root, int(in.Deadline.Seconds()), logFile)
			if err != nil {
				_ = logFile.Close()
				fmt.Fprintln(in.Stderr, err)
				return 2
			}
		} else {
			cmd = exec.Command(in.Runner, c.label, strconv.Itoa(c.slot), c.model, c.cardPath, in.Root)
			cmd.Env = append(os.Environ(), "NOVA_SWARM_ROOT="+in.Root, "NOVA_SWARM_JOB="+job)
			cmd.Stdout = logFile
			cmd.Stderr = logFile
		}
		if err := cmd.Start(); err != nil {
			_ = logFile.Close()
			fmt.Fprintf(in.Stderr, "nova-swarm batch: runner %s could not start for %s: %s\n",
				oneline.Field(in.Runner), oneline.Field(c.label), oneline.Err(err))
			return 2
		}
		_ = logFile.Close()
		procs[i] = proc{cmd: cmd, slot: c.slot, label: c.label, lastGrow: time.Now()}
	}

	// wait: every card ends, or the deadline. The wait is one select over one "all done"
	// signal and one timer; it never waits for a card past the deadline. Alongside it, when
	// --idle is set, one monitor re-reads each running card's own log -- <slot>/native.log
	// when the child wrote one, else the job's harness.log -- and kills a card whose log has
	// not grown for the idle window: a dead card is removed from the wait, so the batch
	// returns on its slowest still-working card rather than burning the whole deadline.
	var wg sync.WaitGroup
	allDone := make(chan struct{})
	for i := range procs {
		wg.Add(1)
		go func(p *proc) {
			defer wg.Done()
			err := p.cmd.Wait()
			doneMu.Lock()
			p.done = true
			if ee, ok := err.(*exec.ExitError); ok {
				p.rc = ee.ExitCode()
			} else if err == nil {
				p.rc = 0
			} else {
				p.rc = -1
			}
			doneMu.Unlock()
		}(&procs[i])
	}
	go func() { wg.Wait(); close(allDone) }()

	stopMonitor := make(chan struct{})
	var monitorWG sync.WaitGroup
	if in.Idle > 0 {
		monitorWG.Add(1)
		go func() {
			defer monitorWG.Done()
			ticker := time.NewTicker(idlePollInterval)
			defer ticker.Stop()
			lastSize := make([]int64, len(procs))
			for {
				select {
				case <-stopMonitor:
					return
				case <-allDone:
					return
				case now := <-ticker.C:
					doneMu.Lock()
					for i := range procs {
						if procs[i].done || procs[i].idleKilled {
							continue
						}
						size := logSize(cardLogPath(in.Root, procs[i].slot, procs[i].label))
						if size != lastSize[i] {
							lastSize[i] = size
							procs[i].lastGrow = now
							continue
						}
						if now.Sub(procs[i].lastGrow) >= in.Idle {
							procs[i].idleKilled = true
							procs[i].idleLog = cardLogPath(in.Root, procs[i].slot, procs[i].label)
							if procs[i].cmd.Process != nil {
								_ = procs[i].cmd.Process.Kill()
							}
						}
					}
					doneMu.Unlock()
				}
			}
		}()
	}

	select {
	case <-allDone:
	case <-time.After(in.Deadline):
		doneMu.Lock()
		for i := range procs {
			if !procs[i].done && procs[i].cmd.Process != nil {
				_ = procs[i].cmd.Process.Kill()
			}
		}
		doneMu.Unlock()
	}
	close(stopMonitor)
	monitorWG.Wait()
	// Reap every child before gather reads their exit codes: a killed card's rc must be
	// settled before the gather decides whether a missing RESULT.md is a clean exit.
	wg.Wait()

	// gather: fold every card into one bounded packet. A missing or wrong-line-1 result is
	// an ABSTAIN row; done is decided by the contract alone, never by the process's timing.
	idleSeconds := int(in.Idle.Seconds())
	var (
		done, abstain, idle, stalled int
		holds                        []string
		totalIn, totalOut            int
		total                        float64
	)
	type row struct {
		label    string
		slot     int
		state    string
		line2    string
		in       int
		out      int
		usd      float64
		hold     bool
		idle     bool
		idleLog  string // the file the idle monitor watched, for an idle-killed card
		logLines int
		stalled  bool
		missing  bool   // RESULT.md was not there at all
		jobDir   string // the job directory, for the missing-result reason
		noResult bool   // missing RESULT.md on a clean exit (rc==0)
		inLimit  bool   // the card's own usage row named end=input-limit
	}
	rows := make([]row, len(cards))
	for i, c := range cards {
		rows[i].label = c.label
		rows[i].slot = c.slot
		rows[i].in, rows[i].out, rows[i].usd = readCardUsage(cardUsagePath(in.Root, c.slot, c.label))
		totalIn += rows[i].in
		totalOut += rows[i].out
		total += rows[i].usd
		rows[i].logLines = logOutputLines(cardLogPath(in.Root, c.slot, c.label))
		// A card whose job was refused for size is its own score, `reason=input-limit`: the
		// class is the provider's own structured signal, and the batch names it rather than
		// reading a missing result as a plain abstain (issue #163).
		rows[i].inLimit = cardEndsInputLimit(cardLogPath(in.Root, c.slot, c.label))
		// A card the idle monitor killed is its own score, an ABSTAIN that names its reason,
		// not a missing-result abstain: the card was not hung by its work but stopped growing.
		if procs[i].idleKilled {
			rows[i].state = "abstain"
			rows[i].idle = true
			rows[i].idleLog = procs[i].idleLog
			abstain++
			idle++
			continue
		}
		if rows[i].inLimit {
			rows[i].state = "abstain"
			abstain++
			continue
		}
		path := filepath.Join(in.Root, strconv.Itoa(c.slot), "jobs", c.label, "RESULT.md")
		raw, err := os.ReadFile(path)
		if err != nil {
			rows[i].state = "abstain"
			rows[i].missing = true
			rows[i].jobDir = filepath.Dir(path)
			abstain++
			continue
		}
		lines := strings.Split(string(raw), "\n")
		if !strings.EqualFold(strings.TrimSpace(first(lines)), strings.TrimSpace(c.contract)) {
			rows[i].state = "abstain"
			abstain++
			continue
		}
		rows[i].state = "done"
		done++
		rows[i].line2 = strings.TrimRight(second(lines), "\r\n")
		if strings.Contains(rows[i].line2, "HOLD") {
			rows[i].hold = true
			holds = append(holds, rows[i].line2)
		}
	}

	// A card that ended -- killed, abstained or refused -- with no output after the wall
	// opened is a prompt or harness defect, not a slow model: it is named stalled. But a
	// card that ran to a clean exit (rc 0) and still has no RESULT.md is not a stall: the
	// model finished its run and named its own reason, so the abstain says so rather than
	// blaming a wall that opened on nothing.
	for i := range rows {
		if rows[i].state == "done" {
			continue
		}
		if rows[i].inLimit {
			continue
		}
		if rows[i].logLines == 0 {
			rows[i].stalled = true
			stalled++
			continue
		}
		if rows[i].missing && procs[i].rc == 0 {
			rows[i].noResult = true
		}
	}

	// The packet's grammar. The BATCH line first, then one line per card in admission
	// order (label, its resolved slot, then line 2 verbatim), then HOLD lines -- at most
	// maxHoldLines -- so the whole packet never grows past n + 12 lines whatever the batch
	// holds.
	fmt.Fprintf(in.Stdout, "BATCH %s n=%d done=%d abstain=%d in=%d out=%d usd=%s idle=%d stalled=%d\n",
		oneline.Field(in.ID), len(cards), done, abstain, totalIn, totalOut, formatUSD(total), idle, stalled)
	for _, r := range rows {
		switch {
		case r.inLimit:
			fmt.Fprintf(in.Stdout, "%s slot=%d: ABSTAIN reason=input-limit\n", oneline.Field(r.label), r.slot)
		case r.idle:
			fmt.Fprintf(in.Stdout, "%s slot=%d: ABSTAIN -- idle %ds (%s)\n", oneline.Field(r.label), r.slot, idleSeconds, r.idleLog)
		case r.stalled:
			fmt.Fprintf(in.Stdout, "%s slot=%d: ABSTAIN -- stalled (no output after the wall opened)\n", oneline.Field(r.label), r.slot)
		case r.noResult:
			fmt.Fprintf(in.Stdout, "%s slot=%d: ABSTAIN -- no RESULT.md in %s (rc=0)\n", oneline.Field(r.label), r.slot, r.jobDir)
		case r.state == "abstain":
			fmt.Fprintf(in.Stdout, "%s slot=%d abstain log=%d\n", oneline.Field(r.label), r.slot, r.logLines)
		default:
			fmt.Fprintf(in.Stdout, "%s slot=%d: %s log=%d\n", oneline.Field(r.label), r.slot, r.line2, r.logLines)
		}
	}
	for i := 0; i < len(holds) && i < maxHoldLines; i++ {
		fmt.Fprintf(in.Stdout, "HOLD: %s\n", oneline.Escape(oneline.Cap(holds[i], oneline.TailBytes)))
	}

	if abstain == 0 && len(holds) == 0 {
		return 0
	}
	return 1
}

// readCards reads the TSV and admits every card or none: one line that does not parse
// queues nothing at all, because a batch is all of its cards or none.
func readCards(path string) ([]batchCard, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--cards wants a readable TSV of label, slot, model, card-path: %w", err)
	}
	var cards []batchCard
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 4 {
			return nil, fmt.Errorf("--cards line %d wants label<TAB>slot<TAB>model<TAB>card-path, got %d fields", i+1, len(parts))
		}
		slot := 0
		bench := ""
		if s := strings.TrimSpace(parts[1]); s != "" && s != "-" {
			if b, n, ok := strings.Cut(s, ":"); ok {
				// A bench slot is bench:<n>: slot n on that bench.
				v, err := strconv.Atoi(n)
				if err != nil || v < 1 || b == "" {
					return nil, fmt.Errorf("--cards line %d wants a bench slot bench:<n>, got %q", i+1, parts[1])
				}
				slot = v
				bench = b
			} else {
				n, err := strconv.Atoi(s)
				if err != nil || n < 1 {
					return nil, fmt.Errorf("--cards line %d wants a positive slot number, got %q", i+1, parts[1])
				}
				slot = n
			}
		}
		cardPath := parts[3]
		cardRaw, err := os.ReadFile(cardPath)
		if err != nil {
			return nil, fmt.Errorf("--cards line %d: %w", i+1, err)
		}
		contract := strings.TrimSpace(first(strings.Split(string(cardRaw), "\n")))
		if contract == "" {
			return nil, fmt.Errorf("--cards line %d: %s is empty; a card admits under line 1 of its text", i+1, cardPath)
		}
		if reason := cardShapeFailure(parts[2], string(cardRaw)); reason != "" {
			return nil, &admitError{label: parts[0], reason: reason}
		}
		// A card whose repositories are not reachable without credentials is refused at
		// admission, before any runner starts: lesson 7 (private repositories).
		if err := checkRepos(parts[0], string(cardRaw)); err != nil {
			return nil, err
		}
		cards = append(cards, batchCard{
			label:    parts[0],
			slot:     slot,
			bench:    bench,
			model:    parts[2],
			cardPath: cardPath,
			contract: contract,
		})
	}
	return cards, nil
}

// logSize is the byte length of a card's log file, or zero when the file is not there yet.
// Growth is the only signal the idle monitor trusts: a card that has written nothing, or has
// stopped writing, reads the same size twice and is on the clock.
func logSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// cardUsagePath resolves one card's usage.tsv: the job directory beside RESULT.md first,
// then the slot directory fallback (lesson 24: the BATCH line sums in/out/usd from the usage rows).
func cardUsagePath(root string, slot int, label string) string {
	jobPath := filepath.Join(root, strconv.Itoa(slot), "jobs", label, "usage.tsv")
	if _, err := os.Stat(jobPath); err == nil {
		return jobPath
	}
	return filepath.Join(root, strconv.Itoa(slot), "usage.tsv")
}

// readCardUsage reads one card's usage.tsv -- the native run's header-plus-row -- and
// returns its tokens_in, tokens_out and usd, or zeroes when the file is absent.
func readCardUsage(path string) (in, out int, usd float64) {
	row, err := readUsageFile(path)
	if err != nil {
		return 0, 0, 0
	}
	in, _ = row.Int("tokens_in")
	out, _ = row.Int("tokens_out")
	usd, _ = strconv.ParseFloat(strings.TrimSpace(row["usd"]), 64)
	return in, out, usd
}

// cardEndsInputLimit reports whether a card's own log carries the structured signal the
// supervisor recorded when the job was refused for size -- `INPUT LIMIT class=… value=…
// limit=…` -- the FIELD the batch reads to score the card `reason=input-limit` rather than a
// plain abstain (issue #163). No prose rule is asked to decide it.
func cardEndsInputLimit(logPath string) bool {
	raw, err := os.ReadFile(logPath)
	if err != nil {
		return false
	}
	_, ok := ReadInputLimitSignal(raw)
	return ok
}

func formatUSD(n float64) string { return strconv.FormatFloat(n, 'f', 4, 64) }

// cardLogPath is the child's own log the gather counts: <slot>/native.log when the native
// run wrote one, else the job's harness.log (which carries only the runner's own stdout, the
// NATIVE OK line). Run 10's defect was counting a log the child never wrote: a card with a
// valid RESULT.md looked stalled because the gather counted a non-existent native.log under
// the job directory instead of the child's real log under the slot.
func cardLogPath(root string, slot int, label string) string {
	native := filepath.Join(root, strconv.Itoa(slot), "native.log")
	if _, err := os.Stat(native); err == nil {
		return native
	}
	return filepath.Join(root, strconv.Itoa(slot), "jobs", label, "harness.log")
}

// logOutputLines counts a card's own output lines: everything in the run log after the
// sandbox's own header lines, each beginning "SANDBOX ", is what the model wrote once the
// wall opened. A card that ends with none of them is a stall, so a missing log is zero too.
func logOutputLines(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "SANDBOX ") {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		n++
	}
	return n
}

// slotDirName is the on-disk name of slot <n> under the root.
func slotDirName(n int) string { return strconv.Itoa(n) }

// slotDir is the directory of slot <n> under the root.
func slotDir(root string, n int) string { return filepath.Join(root, slotDirName(n)) }

// slotJobDir is a card's job directory under slot <n>: <root>/<n>/jobs/<label>.
func slotJobDir(root string, n int, label string) string {
	return filepath.Join(slotDir(root, n), "jobs", label)
}

// slotLocked reports whether slot <n> is busy: any lock under
// <root>/<n>/jobs/<any>/lock that carries a live pid takes the slot.
func slotLocked(root string, n int) bool {
	entries, err := os.ReadDir(filepath.Join(slotDir(root, n), "jobs"))
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(slotDir(root, n), "jobs", e.Name(), "lock"))
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil {
			continue
		}
		if Alive(pid, "") {
			return true
		}
	}
	return false
}

// busySlots collects the slot numbers that are busy on disk under the root.
func busySlots(root string) map[int]bool {
	out := map[int]bool{}
	entries, err := os.ReadDir(root)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n, err := strconv.Atoi(e.Name())
		if err != nil || n < 1 {
			continue
		}
		if slotLocked(root, n) {
			out[n] = true
		}
	}
	return out
}

// assignSlots resolves every card's slot before any launch. A card that named a slot keeps
// it; a slot named by two cards is refused outright. A card that asked for allocation (slot
// zero) takes the lowest free slot under the root -- free means neither busy on disk (a live
// lock) nor already assigned to another card in this batch.
func assignSlots(cards []batchCard, root string) error {
	busy := busySlots(root)
	assigned := map[int]string{}
	for i := range cards {
		label := cards[i].label
		if cards[i].slot != 0 {
			if prev, ok := assigned[cards[i].slot]; ok {
				return fmt.Errorf("BATCH REFUSED slot %d named twice (%s, %s)", cards[i].slot, prev, label)
			}
			assigned[cards[i].slot] = label
			continue
		}
		n := 1
		for {
			if busy[n] {
				n++
				continue
			}
			if _, ok := assigned[n]; ok {
				n++
				continue
			}
			break
		}
		cards[i].slot = n
		assigned[n] = label
	}
	return nil
}

func first(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

func second(lines []string) string {
	if len(lines) < 2 {
		return ""
	}
	return lines[1]
}

// anyCardNamesBench reports whether any card's slot column named a bench, so a bench column
// alone takes the batch onto the bench path even with no --bench (which then refuses the
// unnamed bench).
func anyCardNamesBench(cards []batchCard) bool {
	for _, c := range cards {
		if c.bench != "" {
			return true
		}
	}
	return false
}

// splitBenchNames splits --bench's comma-separated list into names in deal order, dropping
// empty entries.
func splitBenchNames(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// allocateBenches resolves every card's bench and slot across the named benches, and checks
// each pinning bench's slot count against its cores, returning the ADMIT REFUSED message when
// a bench is over-subscribed. Local cards keep their bare slot numbers; a card naming a bench
// not in the table, or not in --bench, is refused. Unassigned cards are dealt round robin,
// each bench giving its lowest free slot; the local machine is never a pinning bench.
func allocateBenches(cards []batchCard, benchesPath, benchNames string) (map[string]Bench, error) {
	table, err := ReadBenchTable(benchesPath)
	if err != nil {
		return nil, err
	}
	names := splitBenchNames(benchNames)
	named := map[string]bool{}
	for _, n := range names {
		named[n] = true
	}
	type benchUse struct {
		slots int
		used  map[int]bool
	}
	use := map[string]*benchUse{}
	for _, n := range names {
		use[n] = &benchUse{used: map[int]bool{}}
	}
	localUsed := map[int]bool{}
	var unassigned []*batchCard
	for i := range cards {
		c := &cards[i]
		if c.bench != "" {
			if _, ok := table[c.bench]; !ok || !named[c.bench] {
				return nil, fmt.Errorf("BATCH REFUSED card %s names bench %s not in --bench", oneline.Field(c.label), oneline.Field(c.bench))
			}
			if c.slot != 0 {
				u := use[c.bench]
				if u.used[c.slot] {
					return nil, fmt.Errorf("BATCH REFUSED bench %s slot %d named twice", c.bench, c.slot)
				}
				u.used[c.slot] = true
				u.slots++
				continue
			}
			unassigned = append(unassigned, c)
			continue
		}
		if c.slot != 0 {
			if localUsed[c.slot] {
				return nil, fmt.Errorf("BATCH REFUSED slot %d named twice", c.slot)
			}
			localUsed[c.slot] = true
		} else {
			unassigned = append(unassigned, c)
		}
	}
	idx := 0
	for _, c := range unassigned {
		for tries := 0; tries < len(names); tries++ {
			n := names[idx%len(names)]
			idx++
			slot := 1
			if n == "local" {
				for localUsed[slot] {
					slot++
				}
				localUsed[slot] = true
				c.bench = ""
				c.slot = slot
				break
			}
			u := use[n]
			for u.used[slot] {
				slot++
			}
			u.used[slot] = true
			u.slots++
			c.bench = n
			c.slot = slot
			break
		}
	}
	for _, n := range names {
		if n == "local" {
			continue
		}
		b := table[n]
		count := coreCount(b.Cores)
		if count < 0 {
			continue
		}
		if use[n].slots > count {
			return nil, fmt.Errorf("ADMIT REFUSED bench=%s slots=%d cores=%d", b.Name, use[n].slots, count)
		}
	}
	return table, nil
}

// cardShapeFailure checks a card's shape at admission, and only for a DeepSeek model whose
// provider prefix is opencode/ or deepseek/. Mercury (inception/) cards are not checked.
// It returns the reason if the card is refused, or "" if the card's shape is acceptable.
// The refusal cites docs/WORKER-CARDS.md practice 17: a DeepSeek card wants a working
// directory and the clone as step 1, one command per line, numbered steps, the verdict
// vocabulary inside the step, the RESULT shape last and short, no capitalised contract
// block and no launcher text.
func cardShapeFailure(model, raw string) string {
	if !isDeepSeekModel(model) {
		return ""
	}
	lines := strings.Split(raw, "\n")
	step := "docs/WORKER-CARDS.md practice 17"
	if firstNonEmpty := firstNonEmptyLine(lines); lineIsCapitalsOnly(firstNonEmpty) {
		return "capitalised contract block (" + step + ")"
	}
	if !hasStep1(lines) {
		return "no 'STEP 1' line in the first 15 lines (" + step + ")"
	}
	if mentionsLauncher(lines) {
		return "'launcher' in the first 10 lines (" + step + ")"
	}
	return ""
}

// isDeepSeekModel reports whether a model's provider prefix is opencode/ or deepseek/.
func isDeepSeekModel(model string) bool {
	prefix, _, ok := strings.Cut(model, "/")
	if !ok {
		return false
	}
	return prefix == "opencode" || prefix == "deepseek"
}

// firstNonEmptyLine is the first line whose trimmed form is not empty, or "" when every
// line is empty.
func firstNonEmptyLine(lines []string) string {
	for _, ln := range lines {
		if strings.TrimSpace(ln) != "" {
			return ln
		}
	}
	return ""
}

// lineIsCapitalsOnly reports whether a line holds at least one letter and no lowercase one:
// a capitalised contract block.
func lineIsCapitalsOnly(s string) bool {
	hasLetter := false
	for _, r := range s {
		if unicode.IsLetter(r) {
			hasLetter = true
			if unicode.IsLower(r) {
				return false
			}
		}
	}
	return hasLetter
}

// hasStep1 reports whether any of the first 15 lines begins "STEP 1".
func hasStep1(lines []string) bool {
	n := len(lines)
	if n > 15 {
		n = 15
	}
	for i := 0; i < n; i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "STEP 1") {
			return true
		}
	}
	return false
}

// mentionsLauncher reports whether any of the first 10 lines mentions "launcher".
func mentionsLauncher(lines []string) bool {
	n := len(lines)
	if n > 10 {
		n = 10
	}
	for i := 0; i < n; i++ {
		if strings.Contains(strings.ToLower(lines[i]), "launcher") {
			return true
		}
	}
	return false
}
