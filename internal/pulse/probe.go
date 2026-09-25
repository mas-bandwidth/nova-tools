package pulse

// `cut --probe`: the learned admission checklist (SPEC-PULSE "The learned admission
// checklist", nova-tools #585, Ikrima 2026-09-16).
//
// The recurring failure class is the abstain reason. Over the 2026-09-15 baseline of 60
// batches the same classes recur -- `no-result`, `wall refusal`, `idle kill`, `admission`,
// `line1-mismatch` -- and a rule written after each fault did not transfer to the next
// unfamiliar card. The probe runs the checks the history has actually earned against each
// new card before launch, for the price of a read instead of a whole run. A class the
// history does not name is not a learned check and is skipped: the checklist is cut from the
// record, never invented.

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// ProbeInput is one card under the checklist. History is the abstain history: one class per
// line, an optional tab-separated count after it. Budget is the card's byte budget; 0 means
// unbounded. The probe makes no model call and touches no network.
type ProbeInput struct {
	Label   string
	Card    string
	History string
	Budget  int
	Stdout  io.Writer
	Stderr  io.Writer
}

// probeCheck is one item of the checklist, named for the class it was learned from.
type probeCheck struct {
	Class  string
	Reason string
	Bad    func(card string, budget int) bool
}

// probeChecks are the five checks, each named for the class it was learned from.
var probeChecks = []probeCheck{
	{"admission", "the card leaves its job (a path outside the job, `../`)", func(card string, _ int) bool {
		return strings.Contains(card, "../")
	}},
	{"wall refusal", "the card quotes a forbidden word (--force or --admin)", func(card string, _ int) bool {
		return strings.Contains(card, "--force") || strings.Contains(card, "--admin")
	}},
	{"line1-mismatch", "the card names no red-test step (a `red line` then a `green line`)", func(card string, _ int) bool {
		return !strings.Contains(strings.ToLower(card), "red line")
	}},
	{"idle kill", "the card is over its file budget", func(card string, budget int) bool {
		return budget > 0 && len(card) > budget
	}},
	{"no-result", "the card never names where RESULT.md goes", func(card string, _ int) bool {
		return !strings.Contains(card, "RESULT.md")
	}},
}

// Probe runs the learned checklist against one card and returns 0 when it passes and 1 when
// a learned class refuses it; 2 when the history could not be read. A refusal names the
// class and its remedy so the per-class count stays comparable to the history.
func Probe(in ProbeInput) int {
	learned, err := learnedClasses(in.History)
	if err != nil {
		fmt.Fprintf(in.Stderr, "PROBE REFUSED card=%s: %s\n", oneline.Field(in.Label), oneline.Err(err))
		return 2
	}
	checked := 0
	for _, c := range probeChecks {
		if !learned[c.Class] {
			continue
		}
		checked++
		if c.Bad(in.Card, in.Budget) {
			fmt.Fprintf(in.Stderr, "PROBE REFUSED card=%s class=%s (%s)\n", oneline.Field(in.Label), oneline.Field(c.Class), c.Reason)
			return 1
		}
	}
	fmt.Fprintf(in.Stdout, "PROBE OK card=%s checked=%d\n", oneline.Field(in.Label), checked)
	return 0
}

// learnedClasses reads the abstain history: one class per line, an optional tab-separated
// count after it. Blank lines are skipped, so an empty history learns nothing and admits
// everything -- the behaviour every cut had before this card.
func learnedClasses(path string) (map[string]bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--history wants the abstain history (one class per line): %s", oneline.Err(err))
	}
	learned := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		class := strings.TrimSpace(strings.SplitN(line, "\t", 2)[0])
		if class != "" {
			learned[class] = true
		}
	}
	return learned, nil
}
