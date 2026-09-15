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
	Stdout   io.Writer
	Stderr   io.Writer
}

// batchCard is one admitted card, in TSV order.
type batchCard struct {
	label    string
	slot     int
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
	if in.Runner == "" {
		fmt.Fprintln(in.Stderr, "nova-swarm batch: --runner is required; it wants the command one process per card runs")
		return 2
	}

	// scatter: one runner process per card, in TSV order. The job directory is made before
	// the process starts so a runner can write RESULT.md straight into place.
	type proc struct {
		cmd        *exec.Cmd
		logPath    string
		done       bool // guarded by doneMu
		idleKilled bool // guarded by doneMu
		lastGrow   time.Time
	}
	var doneMu sync.Mutex
	procs := make([]proc, len(cards))
	for i, c := range cards {
		job := filepath.Join(in.Root, strconv.Itoa(c.slot), "jobs", c.label)
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
		cmd := exec.Command(in.Runner, c.label, strconv.Itoa(c.slot), c.model, c.cardPath, in.Root)
		cmd.Env = append(os.Environ(), "NOVA_SWARM_ROOT="+in.Root, "NOVA_SWARM_JOB="+job)
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		if err := cmd.Start(); err != nil {
			_ = logFile.Close()
			fmt.Fprintf(in.Stderr, "nova-swarm batch: runner %s could not start for %s: %s\n",
				oneline.Field(in.Runner), oneline.Field(c.label), oneline.Err(err))
			return 2
		}
		_ = logFile.Close()
		procs[i] = proc{cmd: cmd, logPath: logPath, lastGrow: time.Now()}
	}

	// wait: every card ends, or the deadline. The wait is one select over one "all done"
	// signal and one timer; it never waits for a card past the deadline. Alongside it, when
	// --idle is set, one monitor re-reads each running card's log and kills a card whose log
	// has not grown for the idle window: a dead card is removed from the wait, so the batch
	// returns on its slowest still-working card rather than burning the whole deadline.
	var wg sync.WaitGroup
	allDone := make(chan struct{})
	for i := range procs {
		wg.Add(1)
		go func(p *proc) {
			defer wg.Done()
			_ = p.cmd.Wait()
			doneMu.Lock()
			p.done = true
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
						size := logSize(procs[i].logPath)
						if size != lastSize[i] {
							lastSize[i] = size
							procs[i].lastGrow = now
							continue
						}
						if now.Sub(procs[i].lastGrow) >= in.Idle {
							procs[i].idleKilled = true
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
		state    string
		line2    string
		in       int
		out      int
		usd      float64
		hold     bool
		idle     bool
		logLines int
		stalled  bool
	}
	rows := make([]row, len(cards))
	for i, c := range cards {
		rows[i].label = c.label
		rows[i].in, rows[i].out, rows[i].usd = readCardUsage(filepath.Join(in.Root, strconv.Itoa(c.slot), "jobs", c.label, "usage.tsv"))
		totalIn += rows[i].in
		totalOut += rows[i].out
		total += rows[i].usd
		rows[i].logLines = logOutputLines(filepath.Join(in.Root, strconv.Itoa(c.slot), "jobs", c.label, "native.log"))
		// A card the idle monitor killed is its own score, an ABSTAIN that names its reason,
		// not a missing-result abstain: the card was not hung by its work but stopped growing.
		if procs[i].idleKilled {
			rows[i].state = "abstain"
			rows[i].idle = true
			abstain++
			idle++
			continue
		}
		path := filepath.Join(in.Root, strconv.Itoa(c.slot), "jobs", c.label, "RESULT.md")
		raw, err := os.ReadFile(path)
		if err != nil {
			rows[i].state = "abstain"
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
	// opened is a prompt or harness defect, not a slow model: it is named stalled.
	for i := range rows {
		if rows[i].state != "done" && rows[i].logLines == 0 {
			rows[i].stalled = true
			stalled++
		}
	}

	// The packet's grammar. The BATCH line first, then one line per card in admission
	// order (label, then line 2 verbatim), then HOLD lines -- at most maxHoldLines -- so
	// the whole packet never grows past n + 12 lines whatever the batch holds.
	fmt.Fprintf(in.Stdout, "BATCH %s n=%d done=%d abstain=%d in=%d out=%d usd=%s idle=%d stalled=%d\n",
		oneline.Field(in.ID), len(cards), done, abstain, totalIn, totalOut, formatUSD(total), idle, stalled)
	for _, r := range rows {
		switch {
		case r.idle:
			fmt.Fprintf(in.Stdout, "%s: ABSTAIN -- idle %ds\n", oneline.Field(r.label), idleSeconds)
		case r.stalled:
			fmt.Fprintf(in.Stdout, "%s: ABSTAIN -- stalled (no output after the wall opened)\n", oneline.Field(r.label))
		case r.state == "abstain":
			fmt.Fprintf(in.Stdout, "%s abstain log=%d\n", oneline.Field(r.label), r.logLines)
		default:
			fmt.Fprintf(in.Stdout, "%s %s log=%d\n", oneline.Field(r.label), oneline.Escape(oneline.Cap(r.line2, oneline.TailBytes)), r.logLines)
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
		slot, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil || slot < 1 {
			return nil, fmt.Errorf("--cards line %d wants a positive slot number, got %q", i+1, parts[1])
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

func formatUSD(n float64) string { return strconv.FormatFloat(n, 'f', 4, 64) }

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
