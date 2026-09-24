// Package consume is the ns_pr_eval side effect that is not the PR record
// itself. The rebase card (#3094) is that effect: one script card, or one
// conflict fix task, decided from the record the eval already holds.
package consume

import "github.com/mas-bandwidth/nova-tools/internal/nsprint/rebase"

// RebaseOnEval is the ns_pr_eval step that cuts a rebase card. It does not
// read GitHub and it does not call a model. An ineligible PR returns ok
// false and leaves the book unchanged.
func RebaseOnEval(b *rebase.Book, rec rebase.Record) (rebase.Card, bool, error) {
	return rebase.OnEval(b, rec)
}
