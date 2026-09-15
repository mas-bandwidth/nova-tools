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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// BatchInput is everything the batch scatter/wait/gather needs, held apart from the
// command-line parsing so a test can drive it with a fake runner script.
type BatchInput struct {
	ID       string        // the batch id, printed on the BATCH line
	Deadline time.Duration // the whole batch's own deadline
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

// Batch runs one batch through scatter, wait and gather and returns the process exit code:
// 0 only when every card was done and none held, 1 otherwise, 2 when the admission could
// not even be read.
func Batch(in BatchInput) int {
	cards, err := readCards(in.Cards)
	if err != nil {
		fmt.Fprintf(in.Stderr, "nova-swarm batch: %s\n", oneline.Err(err))
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
		cmd  *exec.Cmd
		done bool // guarded by doneMu
	}
	var doneMu sync.Mutex
	procs := make([]proc, len(cards))
	for i, c := range cards {
		job := filepath.Join(in.Root, strconv.Itoa(c.slot), "jobs", c.label)
		if err := os.MkdirAll(job, 0o755); err != nil {
			fmt.Fprintf(in.Stderr, "nova-swarm batch: %s\n", oneline.Err(err))
			return 2
		}
		cmd := exec.Command(in.Runner, c.label, strconv.Itoa(c.slot), c.model, c.cardPath, in.Root)
		cmd.Env = append(os.Environ(), "NOVA_SWARM_ROOT="+in.Root, "NOVA_SWARM_JOB="+job)
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(in.Stderr, "nova-swarm batch: runner %s could not start for %s: %s\n",
				oneline.Field(in.Runner), oneline.Field(c.label), oneline.Err(err))
			return 2
		}
		procs[i] = proc{cmd: cmd}
	}

	// wait: every card ends, or the deadline. The wait is one select over one "all done"
	// signal and one timer; it never waits for a card past the deadline.
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

	// gather: fold every card into one bounded packet. A missing or wrong-line-1 result is
	// an ABSTAIN row; done is decided by the contract alone, never by the process's timing.
	var (
		done, abstain int
		holds         []string
		total         float64
	)
	type row struct {
		label string
		state string
		line2 string
		usd   float64
		hold  bool
	}
	rows := make([]row, len(cards))
	for i, c := range cards {
		rows[i].label = c.label
		rows[i].usd = readUSD(filepath.Join(in.Root, strconv.Itoa(c.slot), "jobs", c.label, "usage"))
		total += rows[i].usd
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

	// The packet's grammar. The BATCH line first, then one line per card in admission
	// order (label, then line 2 verbatim), then HOLD lines -- at most maxHoldLines -- so
	// the whole packet never grows past n + 12 lines whatever the batch holds.
	fmt.Fprintf(in.Stdout, "BATCH %s n=%d done=%d abstain=%d usd=%s\n",
		oneline.Field(in.ID), len(cards), done, abstain, formatUSD(total))
	for _, r := range rows {
		if r.state == "abstain" {
			fmt.Fprintf(in.Stdout, "%s abstain\n", oneline.Field(r.label))
			continue
		}
		fmt.Fprintf(in.Stdout, "%s %s\n", oneline.Field(r.label), oneline.Escape(oneline.Cap(r.line2, oneline.TailBytes)))
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

// readUSD reads a card's usage file -- one number, dollars -- or zero when it is absent.
func readUSD(path string) float64 {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 64)
	if err != nil {
		return 0
	}
	return n
}

func formatUSD(n float64) string { return strconv.FormatFloat(n, 'f', 4, 64) }

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
