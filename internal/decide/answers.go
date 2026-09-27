// An answer is checked against the question that asked it (edges 22 and 23 of
// the non-author's read of #1327).
//
// A typed decision is typed at BOTH ends. The question carries a closed set of
// criteria, and until this file existed nothing ever compared the answer to it:
// an answer of "deepseek-flash" to a question offering continue, ask-all-
// friends and ask-glenn printed `HELP help=deepseek-flash conf=0.94 below=-` at
// exit 0, and `{"type":"choice","confidence":0.99}` -- an answer naming nothing
// at all -- printed an empty value ABOVE the floor at exit 0. Both are the
// provider failing to answer the question, and a decision that was never made
// cannot be authorized by the confidence attached to it.
//
// So: a choice answer must name one of the options its question offered, and
// the refusal names the answer and the offered set, because a reader has to see
// the gap to act on it.
package decide

import (
	"fmt"
	"sort"
	"strings"
)

// ValidateAnswers checks each typed answer against the question it answers. It
// is the last gate before an answer becomes a decision: a choice outside the
// criteria, a choice naming nothing, an answer of the wrong type, and an answer
// to a question nobody asked are all PROVIDER ERRORS rather than decisions.
//
// The check is deterministic: questions are walked in name order, so the same
// bad response always names the same first offence.
func ValidateAnswers(qs map[string]Question, answers map[string]Answer) error {
	names := make([]string, 0, len(answers))
	for name := range answers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		q, ok := qs[name]
		if !ok {
			return fmt.Errorf("decide: the provider answered question %q, which was never asked; the questions asked are %s",
				name, offeredList(questionNames(qs)))
		}
		if err := validateAnswer(name, q, answers[name]); err != nil {
			return err
		}
	}
	return nil
}

// validateAnswer is one answer against one question.
func validateAnswer(name string, q Question, a Answer) error {
	switch {
	case q.Choice != nil:
		offered := offeredList(choiceOptions(q))
		if a.Type != "" && a.Type != "choice" {
			return fmt.Errorf("decide: question %q is a choice among %s, and the provider answered with a %s",
				name, offered, a.Type)
		}
		if strings.TrimSpace(a.Choice) == "" {
			return fmt.Errorf("decide: question %q was answered with no choice at all; it offers %s", name, offered)
		}
		if _, ok := q.Choice[a.Choice]; !ok {
			return fmt.Errorf("decide: question %q was answered %q, which it does not offer; it offers %s",
				name, a.Choice, offered)
		}
	case q.Score != nil:
		if a.Type != "" && a.Type != "score" {
			return fmt.Errorf("decide: question %q is a score over %d levels, and the provider answered with a %s",
				name, len(q.Score), a.Type)
		}
	case q.Noul:
		if a.Type != "" && a.Type != "noul" {
			return fmt.Errorf("decide: question %q is a noul, and the provider answered with a %s", name, a.Type)
		}
	}
	return nil
}

// choiceOptions is a choice question's options, in name order.
func choiceOptions(q Question) []string {
	out := make([]string, 0, len(q.Choice))
	for option := range q.Choice {
		out = append(out, option)
	}
	sort.Strings(out)
	return out
}

// questionNames is the names a request asked about, in order.
func questionNames(qs map[string]Question) []string {
	out := make([]string, 0, len(qs))
	for name := range qs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// offeredList renders a closed set the way the criteria are written, so the
// refusal a person reads and the question the provider was sent look the same.
func offeredList(options []string) string {
	if len(options) == 0 {
		return "nothing"
	}
	return strings.Join(options, "|")
}
