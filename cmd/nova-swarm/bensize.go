// bench size: measures one bench's width, a power of two (docs/SPEC-SWARM.md,
// "Benches", #528). `nova-swarm bench size --benches <file> --bench <name> [--max
// <n>]` runs the known-answer card W times concurrently for W = 1, 2, 4, ... and
// keeps doubling while three rules hold, writes the width back to the bench row, and
// ends with one BENCH WIDTH line.
package main

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

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// sizeMaxWidth is the largest concurrency bench size will try, the same ceiling the
// --workers cap enforces: a caller asking for wider than 64 has a belief about
// throughput a note does not correct.
const sizeMaxWidth = 64

// knownAnswerCard is the card bench size runs W times: a fixed known-answer task that
// always completes, so the only reasons a round abstains are the machine's own —
// idle, deadline, refusal — which is what rule (c) is measuring.
const knownAnswerCard = "BENCH SIZE known-answer\nanswer: OK\n"

// cmdBenchSize measures one bench's width and records it.
func cmdBenchSize(args []string, stdout, stderr io.Writer) int {
	f := newFlags("bench size")
	benches := f.fs.String("benches", "", "")
	bench := f.fs.String("bench", "", "")
	max := f.fs.Int("max", sizeMaxWidth, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*benches, "benches", "the TSV file naming each bench (name host root cores harness auth wall)")
	f.want(*bench, "bench", "the name of one row to measure, as its name column")
	if *max < 1 || *max > sizeMaxWidth {
		f.add(fmt.Sprintf("--max is 1..%d, got %d", sizeMaxWidth, *max))
	}
	if f.refused(stderr) {
		return 2
	}
	table, err := swarm.LoadBenchTable(*benches)
	if err != nil {
		fmt.Fprintf(stderr, "BENCH REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	row := -1
	for i := range table {
		if table[i].Name == *bench {
			row = i
			break
		}
	}
	if row < 0 {
		fmt.Fprintf(stderr, "BENCH REFUSED: no bench %s in %s\n", oneline.Field(*bench), oneline.Field(*benches))
		return 2
	}
	cores, err := sizerCores(&table[row], stderr)
	if err != nil {
		fmt.Fprintf(stderr, "BENCH REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	s, err := newSizer(&table[row])
	if err != nil {
		fmt.Fprintf(stderr, "BENCH REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	width := swarm.SizeWidth(*max, cores, s.measure)
	table[row].Width = width
	table[row].Measured = swarm.Stamp(time.Now())
	table[row].Version = buildinfo.ShortSHA(version)
	if err := swarm.WriteBenchTable(*benches, table); err != nil {
		fmt.Fprintf(stderr, "BENCH REFUSED: %s\n", oneline.Err(err))
		return 2
	}
	fmt.Fprintf(stdout, "BENCH WIDTH bench=%s width=%d cores=%d rows=%d\n",
		oneline.Field(table[row].Name), width, cores, len(table))
	return 0
}

// sizerCores is the bench's core count: the breadth of a pinned list, or `nproc --all`
// on the bench for a "-" row.
func sizerCores(row *swarm.Bench, stderr io.Writer) (int, error) {
	if row.Pinned() {
		cores, err := swarm.CoresList(row.Cores)
		if err != nil {
			return 0, err
		}
		return len(cores), nil
	}
	out, err := benchRemote(row.Host, "nproc", "--all")
	if err != nil {
		return 0, fmt.Errorf("bench %s cores are \"-\" and nproc --all would not run: %v", row.Name, err)
	}
	n, aerr := strconv.Atoi(strings.TrimSpace(out))
	if aerr != nil || n < 1 {
		return 0, fmt.Errorf("bench %s nproc --all answered %q", row.Name, out)
	}
	return n, nil
}

// sizer runs the known-answer card W times and reads the bench's load, one round at a
// time. It reuses prober.remote so a bench is reached the same way probe reaches it —
// ssh for a remote row, an exec for the local one — and both are fakeable in a test.
type sizer struct {
	prober *prober
	card   string
}

func newSizer(row *swarm.Bench) (*sizer, error) {
	dir, err := os.MkdirTemp("", "nova-bench-size")
	if err != nil {
		return nil, err
	}
	card := filepath.Join(dir, "known-answer.md")
	if err := os.WriteFile(card, []byte(knownAnswerCard), 0o600); err != nil {
		return nil, err
	}
	return &sizer{prober: &prober{row: row}, card: card}, nil
}

// measure runs w copies of the known-answer card concurrently, times the round, and
// reads the one-minute load afterwards.
func (s *sizer) measure(w int) swarm.SizeRound {
	start := time.Now()
	var wg sync.WaitGroup
	var mu sync.Mutex
	abstain := 0
	for i := 0; i < w; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.runCard(); err != nil {
				mu.Lock()
				abstain++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	elapsed := time.Since(start)
	cpm := 0.0
	if elapsed > 0 {
		cpm = float64(w) / elapsed.Minutes()
	}
	return swarm.SizeRound{Load: s.readLoad(), CardsPerMin: cpm, Abstains: abstain}
}

// runCard runs the known-answer card once through the bench's harness. A remote bench
// gets the card copied to it first, exactly one file; the harness exit status is the
// answer — non-zero is an abstain.
func (s *sizer) runCard() error {
	b := s.prober.row
	if b.Host != "" && b.Host != "local" {
		dest := b.Root + "/cards/known-answer.md"
		if err := rsyncCard(s.card, b.Host, dest); err != nil {
			return err
		}
		_, err := s.prober.remote(b.Host, b.Harness, "run", dest)
		return err
	}
	_, err := s.prober.remote("", b.Harness, "run", s.card)
	return err
}

// readLoad is the bench's one-minute load average, the first field of `/proc/loadavg`.
func (s *sizer) readLoad() float64 {
	out, _ := s.prober.remote(s.prober.row.Host, "cat", "/proc/loadavg")
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(fields[0], 64)
	return v
}

// rsyncCard moves the known-answer card to the bench, the one file that crosses before
// a remote round runs.
func rsyncCard(local, host, dest string) error {
	out, err := exec.Command("rsync", local, host+":"+dest).CombinedOutput()
	if err != nil {
		return fmt.Errorf("rsync: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// benchRemote runs args after host through ssh, or locally for the local row.
func benchRemote(host string, args ...string) (string, error) {
	var argv []string
	if host != "" && host != "local" {
		argv = append([]string{"ssh", host}, args...)
	} else {
		argv = args
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
