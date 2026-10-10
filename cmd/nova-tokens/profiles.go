package main

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tool"
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
	raw, err := boundedReadFile(promptPath, 4<<20) // 4 MiB cap for PROMPT.md
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

const wantsSwarmRoot = "the directory the swarm batches live under"

func profilesVerb(now time.Time) tool.Verb {
	return tool.Verb{
		Name:   "profiles",
		Token:  "PROFILES",
		Usage:  "profiles --swarm-root <dir>\n                      one PROFILES MODEL line per model (cards, median output, overshoot), then a PROFILES OK line with totals",
		Effect: tool.Inspection,
		Flags: func(f *tool.Flags) {
			f.Required("swarm-root", wantsSwarmRoot)
			f.Check(func(c *tool.Call) {
				root := c.Str("swarm-root")
				if root != "" {
					if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
						if err != nil && os.IsNotExist(err) {
							c.Problem("--swarm-root does not exist: " + root + "; it wants the directory the swarm batches live under")
						} else if err != nil {
							c.Problem("--swarm-root " + root + ": " + err.Error() + "; it wants the directory the swarm batches live under")
						} else {
							c.Problem("--swarm-root is not a directory: " + root + "; it wants the directory the swarm batches live under")
						}
					}
				}
			})
		},
		Run: func(c *tool.Call) *tool.Out {
			return runProfiles(c)
		},
	}
}

// runProfiles is the `profiles` verb: it walks the card usage files under a root and
// returns one line per model, then the total. The budget is read from each card's own prompt,
// so an overshoot is one card's own ceiling, not a guess from the fold.
func runProfiles(c *tool.Call) *tool.Out {
	root := c.Str("swarm-root")

	paths, err := filepath.Glob(filepath.Join(root, "*", "jobs", "*", "usage.tsv"))
	if err != nil {
		return tool.Refuse("--swarm-root " + root + ": " + err.Error() + "; it wants the directory the swarm batches live under")
	}
	sort.Strings(paths)

	models := map[string]*profileModel{}
	for _, p := range paths {
		u := readCardFile(p)
		if !u.ok || u.model == "" {
			continue
		}
		m, seen := models[u.model]
		if !seen {
			m = &profileModel{}
			models[u.model] = m
		}
		m.cards++
		if u.outKnown {
			m.outs = append(m.outs, u.out)
			if budget, have := parseCardBudget(filepath.Join(filepath.Dir(p), "PROMPT.md")); have && u.out > budget {
				m.overshoot++
			}
		}
	}

	o := tool.Done()
	totalCards, totalOver := 0, 0
	for _, name := range slices.Sorted(maps.Keys(models)) {
		m := models[name]
		totalCards += m.cards
		totalOver += m.overshoot
		o.Item("model", "model", name, "cards", m.cards, "median_out", medianOut(m.outs), "overshoot", m.overshoot)
	}
	o.Fact("models", len(models)).
		Fact("cards", totalCards).
		Fact("overshoot", totalOver)

	return o
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
	lo, hi := outs[n/2-1], outs[n/2]
	return strconv.FormatInt(lo+(hi-lo)/2, 10)
}
