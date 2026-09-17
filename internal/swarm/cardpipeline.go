package swarm

import (
	"strconv"
	"strings"
)

// SPEC-SWARM, issue #856: a card is a pipeline of stateless model calls, not an
// agent loop. A pipeline card names at most pipelineModelSteps model calls. A
// fourth call is the agentic loop, and the loop is admitted only by `MODE:
// explore`, the one read whose next input is not known when the card is written.

// pipelineModelSteps is the number of model calls a card may name with no mode
// word: the fix-card shape of P7 -- red test, fix, RESULT.
const pipelineModelSteps = 3

// exploreRemedy is the line a card that asks for a fourth call without the mode
// word is refused with. It names the keyword and the rule so the caller can act.
const exploreRemedy = "a fourth model call needs `MODE: explore`: a card is a pipeline of one call per step (SPEC-SWARM the card is a pipeline, issue #856)"

// cardExploreMode reports whether the card's own lines declare `MODE: explore`.
func cardExploreMode(raw string) bool {
	for _, ln := range strings.Split(raw, "\n") {
		if strings.EqualFold(strings.TrimSpace(ln), "MODE: explore") {
			return true
		}
	}
	return false
}

// highestModelStep returns the largest n in a card line that begins `STEP n`, or
// zero when the card names no step. It is the count of the card's model calls.
func highestModelStep(raw string) int {
	highest := 0
	for _, ln := range strings.Split(raw, "\n") {
		line := strings.TrimSpace(ln)
		if !strings.HasPrefix(line, "STEP ") {
			continue
		}
		fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, "STEP ")))
		if len(fields) == 0 {
			continue
		}
		n, err := strconv.Atoi(strings.TrimRight(fields[0], ".:"))
		if err != nil {
			continue
		}
		if n > highest {
			highest = n
		}
	}
	return highest
}

// exploreTurnBudget reads the turn budget a `MODE: explore` card carries on its
// `TURNS: <n>` line, and whether the card carried one. A card that is not an
// explore card has no budget: it has no loop to stop.
func exploreTurnBudget(raw string) (int, bool) {
	if !cardExploreMode(raw) {
		return 0, false
	}
	for _, ln := range strings.Split(raw, "\n") {
		line := strings.TrimSpace(ln)
		if !strings.HasPrefix(line, "TURNS:") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "TURNS:")))
		if err != nil || n < 1 {
			continue
		}
		return n, true
	}
	return 0, false
}

// cardPipelineFailure returns the remedy line for a card that asks for a fourth
// model call without `MODE: explore`, or "" when the card is a pipeline.
func cardPipelineFailure(raw string) string {
	if cardExploreMode(raw) {
		return ""
	}
	if highestModelStep(raw) > pipelineModelSteps {
		return exploreRemedy
	}
	return ""
}
