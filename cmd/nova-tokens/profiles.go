package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// SWARM PROFILES MEASUREMENT (the overshoot ledger from every job's usage.tsv).
//
// A native run writes one card's usage.tsv beside its RESULT.md and one PROMPT.md -- the
// card -- in the same job directory. The card's own budget line is "YOUR TOKEN BUDGET IS
// <n>" (or the word `unmetered`, which is no number). This verb walks every card under a
// swarm root and folds, per model, the card count, the median `tokens_out`, and the count
// of cards whose output exceeded their own budget line: the overshoot the bounded-contract
// review observed before its next checkpoint. It makes no policy and writes nothing; it is a
// measurement, so a budget the card does not carry is an absence, never an overshoot.

// budgetLineMarker is the one card sentence this verb reads, transcribed from worker.go's
// Prompt: "YOUR TOKEN BUDGET IS <n>. The machinery ends the job at the budget it can see."
const budgetLineMarker = "YOUR TOKEN BUDGET IS "

// profileModel is one model's folded measurement over the root.
type profileModel struct {
	cards     int
	outs      []int64
	overshoot int
}

// parseCardBudget reads the card's budget line out of the PROMPT.md beside its usage.tsv.
// No card, no line, or a non-numeric budget (unmetered) is an absence, never a zero budget.
func parseCardBudget(promptPath string) (int64, bool) {
	raw, err := os.ReadFile(promptPath)
	if err != nil {
		return 0, false
	}
	i := strings.Index(string(raw), budgetLineMarker)
	if i < 0 {
		return 0, false
	}
	rest := string(raw)[i+len(budgetLineMarker):]
	j := 0
	for j < len(rest) && rest[j] >= '0' && rest[j] <= '9' {
		j++
	}
	if j == 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(rest[:j], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// profileSwarmRoot is the `profiles` verb: it walks the card usage files under a root and
// prints one line per model, then the total. The budget is read from each card's own prompt,
// so an overshoot is one card's own ceiling, not a guess from the fold.
func profileSwarmRoot(root string, stdout, stderr io.Writer, r *refusals) int {
	if root == "" {
		r.add("--swarm-root is required; it wants the directory the swarm batches live under; refusing to guess")
		return r.print(stderr)
	}
	paths, err := filepath.Glob(filepath.Join(root, "*", "jobs", "*", "usage.tsv"))
	if err != nil {
		r.add("--swarm-root " + root + ": " + err.Error() + "; it wants the directory the swarm batches live under")
		return r.print(stderr)
	}
	sort.Strings(paths)

	models := map[string]*profileModel{}
	for _, p := range paths {
		_, model, _, out, _, _, outKnown, _, ok := readCardFile(p)
		if !ok || model == "" {
			continue
		}
		m, seen := models[model]
		if !seen {
			m = &profileModel{}
			models[model] = m
		}
		m.cards++
		if outKnown {
			m.outs = append(m.outs, out)
			if budget, have := parseCardBudget(filepath.Join(filepath.Dir(p), "PROMPT.md")); have && out > budget {
				m.overshoot++
			}
		}
	}

	names := make([]string, 0, len(models))
	for name := range models {
		names = append(names, name)
	}
	sort.Strings(names)

	totalCards, totalOver := 0, 0
	for _, name := range names {
		m := models[name]
		totalCards += m.cards
		totalOver += m.overshoot
		fmt.Fprintf(stdout, "PROFILES MODEL model=%s cards=%d median_out=%s overshoot=%d\n",
			oneline.Field(name), m.cards, oneline.Field(medianOut(m.outs)), m.overshoot)
	}
	fmt.Fprintf(stdout, "PROFILES OK models=%d cards=%d overshoot=%d\n",
		len(names), totalCards, totalOver)
	return 0
}

// medianOut is the median of the known output-token counts, or `-` when none reported one.
// The middle value for an odd count, the floor of the two middle for an even one; a card
// that left `tokens_out` a dash is an absence, never a zero that would pull the median down.
func medianOut(outs []int64) string {
	if len(outs) == 0 {
		return "-"
	}
	sort.Slice(outs, func(i, j int) bool { return outs[i] < outs[j] })
	n := len(outs)
	if n%2 == 1 {
		return strconv.FormatInt(outs[n/2], 10)
	}
	return strconv.FormatInt((outs[n/2-1]+outs[n/2])/2, 10)
}

// cmdProfiles parses the `profiles` verb's one flag and folds the root.
func cmdProfiles(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := newFlagSet("profiles")
	swarmRoot := fs.String("swarm-root", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " profiles", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if code, refused := noPositional(fs, stderr, "profiles"); refused {
		return code
	}
	r := &refusals{token: "PROFILES"}
	return profileSwarmRoot(*swarmRoot, stdout, stderr, r)
}
