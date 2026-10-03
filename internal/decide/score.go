package decide

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"
)

// The score decision (SPEC-NOVA-DECIDE section 9): every landed diff scored against the
// card that asked for it, after the merge. Its questions are the read's five and one noul
// per escalation class the reviews of landed work found (the two cold reviews of
// 2026-10-02, E1 to E16), each the probability that the diff carries that class of
// defect. A class seen often across the record (Findings) is the material for a new
// finder rule or class test.

// ScoreName is the score decision's name in the record.
const ScoreName = "score"

// Class is one escalation class: its name in the record, the review escalations it
// stands for, and the statement asked (a noul; yes is the defect present).
type Class struct {
	Name, Escalations, Statement string
}

// OutsidePaths is the one class not asked on its own: its p is 1 - p(inside_paths),
// the read's own question, so the same evidence is never asked twice.
const OutsidePaths = "outside_paths"

// Unnamed is the findings row of the decisions whose p(defect) meets the bar while no
// class does: a defect no class names yet, the material for a new class.
const Unnamed = "unnamed"

// Classes is every escalation class, in the order the score asks and prints them.
func Classes() []Class {
	return []Class{
		{"stranded_fragment", "E4", "A comment or paragraph the DIFF changes, read whole from its first line to its full stop, " +
			"is broken English: a fragment left by editing one line of a sentence that wraps over several, a line opening " +
			"with a comma or \"and\", the tail of a deleted sentence left attached to a new one, an unmatched backquote, or a " +
			"comment turned into a preformatted block by its indentation."},
		{"cut_citation", "E1, E14", "The DIFF deletes a live cross-reference: a rule number of a list the document still " +
			"numbers, a table cell that named a rule, a test name, an identifier, a docs/ or tla/ path or a model citation; " +
			"or it collapses a comment that explained a mechanism into a summary that no longer names it."},
		{"renamed_file_assumed", "E3", "The DIFF rewrites a reference to a renamed file by its bare name where the " +
			"reference named another file of the same name in another directory, so it now points at a file that is not " +
			"in its package."},
		{OutsidePaths, "E12", "The DIFF changes a file outside the CARD's PATHS that the card does not name to update."},
		{"ledger_ceiling", "E6, E15", "The DIFF leaves a ledger under internal/ci/testdata whose ceiling is not the " +
			"number of rows it holds (a ceiling lowered by other than the rows removed), or leaves a blank line in one."},
		{"comment_contradicts_code", "E7", "A comment the DIFF changes now states what the code beside it contradicts: " +
			"it says the code does what the code prevents, or that a case passes which the code refuses."},
		{"test_weakened", "E2", "An assertion the DIFF rewrites no longer fails exactly when the old one failed: the " +
			"condition under which the new one fails is not the same expression as the old (a negation moved from one " +
			"operand of a conjunction onto the whole, an operand or a message dropped)."},
		{"record_made_claim", "E5", "The DIFF turns a measured record into a standing claim: a number from a run, a " +
			"dated incident or its provenance (tool versions, workers) rewritten into the present tense or cut, or a " +
			"behaviour the code replaced described as the present one, so evidence of one run reads as a promise."},
		{"invented_reason", "E10", "The DIFF puts a reason in place of the one it removes that nothing in the code or " +
			"the removed text supports: a generic sentence (to keep builds responsive, so boundaries stay explicit) where " +
			"the removed text gave a specific reason or none."},
		{"fenced_block_edit", "E8", "The DIFF changes a line inside a fenced code block, a transcript or an example of a " +
			"tool's output in a document."},
		{"asserted_data_cut", "E11, E13", "The DIFF changes text a test, a format or a tool's printed output asserts: a " +
			"heading a test matches, a fixture file name that holds a date, a format field (an issue number in a grammar " +
			"example), or a printed field (pr=#<n>, rule=<n>) cut or turned into a placeholder."},
		{"load_bearing_word_cut", "E9", "The DIFF drops or swaps a word that carried the meaning (\"the old\" version " +
			"directories, \"had to be\", \"used to stand in for\"), so the sentence now says something else, often the " +
			"opposite."},
	}
}

// ScoreSchema is the read's five questions and one noul per class but outside_paths.
func ScoreSchema() Schema {
	s := Schema{Name: ScoreName, Questions: maps.Clone(ReadSchema().Questions)}
	for _, c := range Classes() {
		if c.Name != OutsidePaths {
			s.Questions[c.Name] = Question{Type: Noul, Instructions: c.Statement}
		}
	}
	return s
}

// ClassP is the p of every class a score decision gives, outside_paths from inside_paths.
func ClassP(d Decision) map[string]float64 {
	out := map[string]float64{}
	for _, c := range Classes() {
		if c.Name == OutsidePaths {
			out[c.Name] = 1 - d.Answers["inside_paths"].Prob("yes")
		} else {
			out[c.Name] = d.Answers[c.Name].Prob("yes")
		}
	}
	return out
}

// Top is a score decision's highest class and its p, the first in class order on a tie.
func Top(d Decision) (string, float64) {
	ps, top, best := ClassP(d), "", -1.0
	for _, c := range Classes() {
		if ps[c.Name] > best {
			top, best = c.Name, ps[c.Name]
		}
	}
	return top, best
}

// ScoreOp is a landed diff's id in the record: the card at the head that landed.
func ScoreOp(card, head string) string { return card + "@landed@" + head[:min(12, len(head))] }

// CardOf is the card a score's id names: the part before @landed@, else the whole id.
func CardOf(id string) string {
	card, _, _ := strings.Cut(id, "@landed@")
	return card
}

// Score asks the score decision over a landed card and its diff and records it under id
// (Make: an id already recorded over the same card and diff is answered from the record).
func Score(ctx context.Context, b Backend, card, diff, record, id string, at time.Time) (Decision, error) {
	inputs := map[string]string{"card_sha256": Sum([]byte(card)), "diff_sha256": Sum([]byte(diff))}
	d, _, err := Make(ctx, b, ScoreSchema(), ReadState(card, diff, ""), record, id, inputs, at)
	return d, err
}

// Cluster is one class of the findings: how many score decisions gave it a p at or above
// the bar, and their cards in id order.
type Cluster struct {
	Class string   `json:"class"`
	Count int      `json:"count"`
	Cards []string `json:"cards"`
}

// Findings clusters the score decisions made at or after since by every class each gives a p
// at or above bar, plus Unnamed for p(defect) at or above bar with no class there; most
// cards first, then class order. scored is the decisions in the window.
func Findings(ds []Decision, since time.Time, bar float64) (clusters []Cluster, scored int) {
	cards := map[string][]string{}
	for _, d := range ds {
		at, err := time.Parse(time.RFC3339, d.At)
		if d.Decision != ScoreName || err != nil || at.Before(since) {
			continue
		}
		scored++
		named := false
		for class, p := range ClassP(d) {
			if p >= bar {
				cards[class], named = append(cards[class], CardOf(d.ID)), true
			}
		}
		if !named && PDefect(d) >= bar {
			cards[Unnamed] = append(cards[Unnamed], CardOf(d.ID))
		}
	}
	order := map[string]int{Unnamed: len(Classes())}
	for i, c := range Classes() {
		order[c.Name] = i
	}
	for class, cs := range cards {
		slices.Sort(cs)
		clusters = append(clusters, Cluster{Class: class, Count: len(cs), Cards: cs})
	}
	slices.SortFunc(clusters, func(a, b Cluster) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return order[a.Class] - order[b.Class]
	})
	return clusters, scored
}
