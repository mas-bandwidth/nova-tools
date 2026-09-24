package pulse

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// PlanFile is the plan a pro card writes as turn one (#2590).
const PlanFile = "PLAN.md"

// PlanOKLine is the approval line a friend appends to PlanFile. It counts
// only as the last non-empty line, alone on that line, so a plan that
// mentions PLAN-OK in prose is not approved.
const PlanOKLine = "PLAN-OK"

// PlanStepReady reports whether a pro card may execute: PlanFile exists,
// ends with the PLAN-OK line, and every ask (if any) is bounded multiple
// choice. A card with no asks is not blocked by the ask rule.
func PlanStepReady(jobDir string, asks []string) (bool, string) {
	planPath := filepath.Join(jobDir, PlanFile)
	data, err := os.ReadFile(planPath)
	if err != nil {
		return false, "plan file missing"
	}
	if !planApproved(string(data)) {
		return false, "waiting for PLAN-OK"
	}
	if !boundedAsks(asks) {
		return false, "asks not bounded multiple choice (each ask must list options in parentheses like (A / B / C))"
	}
	return true, ""
}

// planApproved is true when the last non-empty line of the plan is exactly
// PlanOKLine (surrounding whitespace ignored).
func planApproved(content string) bool {
	lines := strings.Split(content, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		return l == PlanOKLine
	}
	return false
}

// boundedAsks is true when every ask carries a bounded option list. No asks
// is bounded (nothing to answer).
func boundedAsks(asks []string) bool {
	for _, a := range asks {
		if !boundedAsk(a) {
			return false
		}
	}
	return true
}

// boundedAsk is true when the ask ends with a parenthesised option list of
// at least two non-empty options separated by "/", e.g. "(A / B / C)".
func boundedAsk(a string) bool {
	s := strings.TrimSpace(a)
	if !strings.HasSuffix(s, ")") {
		return false
	}
	open := strings.LastIndex(s, "(")
	if open < 0 {
		return false
	}
	inner := s[open+1 : len(s)-1]
	if strings.ContainsAny(inner, "()") {
		return false
	}
	opts := strings.Split(inner, "/")
	if len(opts) < 2 {
		return false
	}
	for _, o := range opts {
		if strings.TrimSpace(o) == "" {
			return false
		}
	}
	return true
}

// PlanModeLine is the card line that puts a pro card in plan mode: `PLAN: PLAN.md`.
// Plan mode is the card's own declaration, so pro cards cut before it fold as they did.
const PlanModeLine = "PLAN:"

// planHold is the publication safeguard behind the admission gate (planAdmitCards): harvest
// asks it before a finished card is pushed. A pro card that declares plan mode is not
// published until its job directory's PLAN.md ends with PLAN-OK and every Q: line in it is
// bounded multiple choice; held is true with PlanStepReady's reason while it may not be.
// That is what keeps the plan-only turn's own result from being pushed. Flash cards skip
// plan mode (#2590), and so does a card with no PLAN: line.
func planHold(jobDir string, c CardRow) (why string, held bool) {
	if !planMode(c) {
		return "", false
	}
	ready, why := PlanStepReady(jobDir, planAsks(jobDir))
	return why, !ready
}

// planMode is true for a pro card whose card declares `PLAN: PLAN.md` (#2590).
func planMode(c CardRow) bool {
	return c.Model == "pro" && cardPlansFirst(readCard(c.Card))
}

// PlanTurnHead opens the instruction launch writes into a plan-mode card's first turn.
const PlanTurnHead = "PLAN-ONLY TURN"

// planTurnText is the instruction a plan-only launch carries after the card's line 1.
const planTurnText = PlanTurnHead + " (#2590 plan step): this launch writes the plan and stops. " +
	"Write " + PlanFile + " at the top of your job directory (the directory holding harness.log and RESULT.md): " +
	"the files you will touch, the steps, and every question as one `Q:` line whose choices are listed in parentheses like (A / B / C). " +
	"Do not edit the repository, commit or push in this turn. Stop once " + PlanFile + " is written: a friend appends " +
	PlanOKLine + " as its last line, and the card is launched again to execute the plan."

// planAdmitCards is the plan step at ADMISSION (PlanStepReady's production caller; Stella's
// holds on #3165 and #3182: a gate at harvest runs after the card has already executed). For
// each pro card in plan mode, before it reaches a slot:
//   - no PLAN.md in its job directory: it is admitted as a plan-only turn -- a copy of the
//     card under <root>/cards/plan/ that carries the PLAN-ONLY TURN instruction after line 1,
//     so the first turn writes PLAN.md and stops (PLAN TURN line);
//   - a PLAN.md that PlanStepReady refuses (no final PLAN-OK, an unbounded Q: line): it is
//     not launched at all (ADMIT REFUSED gate=plan-step), and stays in the caller's cards;
//   - an approved PLAN.md: the card itself is admitted to execute.
//
// Every other card passes untouched. A refusal is not a failed run: the caller fills the
// slots it can with what is left, the way the STOP admission does.
func planAdmitCards(root string, cards []CardRow, w io.Writer) ([]CardRow, int, error) {
	var admitted []CardRow
	refused := 0
	for _, c := range cards {
		if !planMode(c) {
			admitted = append(admitted, c)
			continue
		}
		jobDir, n := resolveJobDir(root, c.Slot, c.Label, cardContract(c.Card))
		if n > 1 {
			refused++
			fmt.Fprintf(w, "ADMIT REFUSED card=%s gate=plan-step more than one job directory carries this card's contract; its plan cannot be found\n", oneline.Field(c.Label))
			continue
		}
		if _, err := os.Stat(filepath.Join(jobDir, PlanFile)); err != nil {
			turn, err := writePlanTurn(root, c)
			if err != nil {
				return nil, refused, err
			}
			fmt.Fprintf(w, "PLAN TURN card=%s: plan-only launch; execution waits for %s in %s\n",
				oneline.Field(c.Label), PlanOKLine, oneline.Field(filepath.Join(jobDir, PlanFile)))
			c.Card = turn
			admitted = append(admitted, c)
			continue
		}
		if ready, why := PlanStepReady(jobDir, planAsks(jobDir)); !ready {
			refused++
			fmt.Fprintf(w, "ADMIT REFUSED card=%s gate=plan-step %s (%s)\n",
				oneline.Field(c.Label), oneline.Escape(why), oneline.Field(filepath.Join(jobDir, PlanFile)))
			continue
		}
		admitted = append(admitted, c)
	}
	return admitted, refused, nil
}

// writePlanTurn writes the plan-only copy of a card to <root>/cards/plan/<label>.md: line 1
// (the RESULT contract) unchanged, then the PLAN-ONLY TURN instruction, then the rest of the
// card, so the contract, the PLAN: line and the card's own text all still read the same.
func writePlanTurn(root string, c CardRow) (string, error) {
	raw, err := os.ReadFile(c.Card)
	if err != nil {
		return "", err
	}
	first, rest, _ := strings.Cut(string(raw), "\n")
	dir := filepath.Join(root, "cards", "plan")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, filepath.Base(c.Label)+".md")
	body := first + "\n" + planTurnText + "\n" + rest
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// cardPlansFirst is true when the card carries `PLAN: PLAN.md` before its first section.
func cardPlansFirst(card string) bool {
	for _, l := range strings.Split(card, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "## ") {
			return false
		}
		if v, ok := strings.CutPrefix(t, PlanModeLine); ok && strings.TrimSpace(v) == PlanFile {
			return true
		}
	}
	return false
}

// planAsks is the plan's questions: every PLAN.md line that starts with `Q:`.
func planAsks(jobDir string) []string {
	raw, err := os.ReadFile(filepath.Join(jobDir, PlanFile))
	if err != nil {
		return nil
	}
	var asks []string
	for _, l := range strings.Split(string(raw), "\n") {
		if t := strings.TrimSpace(l); strings.HasPrefix(t, "Q:") {
			asks = append(asks, t)
		}
	}
	return asks
}
