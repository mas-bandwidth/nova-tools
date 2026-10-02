//go:build !perf

package testverbhelp

// overBudget is the help budget's check, held only under -tags perf
// (budget_perf.go): a unit test makes no assertion on wall time.
func overBudget(Run, []string) string { return "" }
