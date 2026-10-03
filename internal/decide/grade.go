package decide

import "strings"

// The grade decision (SPEC-NOVA-DECIDE section 10): before a card's first deal, how
// confident its convergence is, read from its brief alone: a script (no model: the answer is
// known), flash (a cheap model converges) or pro (a strong model is needed). The sprint
// writes it on the card as a hint, and starts a card graded pro at or above the sprint row's
// decide_grade bar on pro instead of flash (docs/SPEC-SPRINT.md section 5, flash first).

// GradeName is the grade decision's name in the record.
const GradeName = "grade"

// GradeQuestion is the grade decision's one question.
const GradeQuestion = "grade"

// The grades, named as the sprint's tiers are (a script card is the sprint's KIND: script).
const (
	GradeScript = "script"
	GradeFlash  = "flash"
	GradePro    = "pro"
)

// GradeSchema is the grade's question: the convergence grade of the card.
func GradeSchema() Schema {
	return Schema{Name: GradeName, Questions: map[string]Question{
		GradeQuestion: {Type: Choice, Instructions: "The least capable worker that converges on this CARD's task, first time, as its gate judges it.",
			Criteria: map[string]string{
				GradeScript: "the card's answer is known exactly: a mechanical replacement a program (a regex, a short Go or Lisp program) makes with no model",
				GradeFlash:  "a fast, cheap model converges: a bounded edit with the lines or files named, little reasoning, and a gate that says when it is done",
				GradePro:    "a strong model is needed to converge: reasoning across files, judgement the card cannot spell out, or work a cheap model gets wrong",
			}},
	}}
}

// GradeState is the text the grade is asked over: the card's brief alone, under its heading.
func GradeState(brief string) string {
	return "CARD (the whole task a worker will be given):\n" + strings.TrimRight(brief, "\n") + "\n"
}

// GradeOp is a card's grade decision id: `<card>@grade.<12 hex of the state>`.
func GradeOp(card, state string) string { return Op(card+"@grade", state) }

// GradeLabel is the grade's outcome: the tier that landed the card, or dropped ("" tier).
func GradeLabel(tier string) string {
	if tier == "" {
		return LabelDropped
	}
	return tier
}
