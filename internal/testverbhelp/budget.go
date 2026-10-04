//go:build !perf

package testverbhelp

// overBudget is the help budget's check, held only under -tags perf
// (budget_perf.go): a unit test makes no assertion on wall time. The perf job
// reaches it through each tool's TestEveryVerbsHelpIsWithinTheBudget, a name only
// the perf tag adds, which is how that job selects what it runs.
func overBudget(Run, []string) string { return "" }
