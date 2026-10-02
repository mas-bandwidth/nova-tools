package swarm

import (
	"fmt"
	"strconv"
	"strings"
)

// A field the provider did not report is the literal "-", never 0: a zero is a
// measurement and a dash is an absence, and nova-tokens reads this file and reads "-" as
// unknown, preserving the distinction between an absent value and a measured zero.

// The ways a job ends, as the `end` column spells them.
const (
	EndDone         = "done"
	EndBudget       = "budget"
	EndUnverifiable = "budget-unverifiable"
	EndFailed       = "failed"
	EndUnknown      = "unknown"
	// EndProvider is a launch that did not take: the harness died inside the launch
	// grace with a provider server error in its tail. It is retried with backoff and,
	// after repeated fast failures, records the provider's reference for diagnosis.
	EndProvider = "provider"
	// EndWall is a job the harness's own fence stopped at a path outside it, with no
	// RESULT.md: the result names the path and the commits the repository kept. It is
	// also a card the harness's own fence or the OS wall stopped before it could publish:
	// the harness or operating-system wall ended the task before the worker could publish it.
	EndWall = "wall"
)

// Dash is the absence this file writes, and never a zero.
const Dash = "-"

// UsageRow is one job's row.
type UsageRow map[string]string

// Int reads a numeric column, reporting whether it is a number at all -- a dash is not.
func (r UsageRow) Int(name string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(r[name]))
	if err != nil {
		return 0, false
	}
	return n, true
}

// ProviderUsage is what the harness's own accounting said, read from the data home the
// dispatcher exported for THIS job -- one job, one database, which is why slots exist.
type ProviderUsage struct {
	Values   map[string]string
	Observed bool // the source answered with a row
	// Turns is the usage row count: the message rows folded into Values, the
	// same assistant-turn count the card budget reads where the harness log
	// has fewer. Zero where nothing was observed.
	Turns int
}

// TokenColumns are nova-tokens's five types, which are the five this tool sums.
var TokenColumns = []string{"tokens_in", "tokens_out", "cache_write", "cache_read", "reasoning"}

// BudgetColumns are the columns the budget counts: what the job SENT, what it
// GENERATED, and what it REASONED. Cache reads are the provider re-reading context it
// already holds, and cache writes are that context being laid down once; neither is work
// this job asked for, and counting them ended a real four-million-token job at 4.18M in
// under three minutes with zero findings. The usage ROW keeps all
// five columns -- `cost` reads what it always read -- the BUDGET counts these three.
var BudgetColumns = []string{"tokens_in", "tokens_out", "reasoning"}

// Budget is the observed spend: the same arithmetic as Sum over BudgetColumns.
func (u ProviderUsage) Budget() (sum int, seen int, partial bool) { return u.add(BudgetColumns) }

// BudgetWord is the ceiling and what was observed under it, in the four spellings
// the document names, and it is the ONE place either route renders them:
//
//	unmetered   the caller said this provider has no live accounting
//	-/<n>       nothing was observed, so the run is never reported as under budget
//	<s>+/<n>    a PARTIAL observation: some columns were dashes, so it can show that the
//	            budget was reached and can never show that the job stayed under it
//	<s>/<n>     a whole observation
//
// THE POOL'S `RUN DONE`/`RUN KILLED` AND `native`'s `NATIVE OK` BOTH COME THROUGH HERE
// This one path keeps the word, sum, stop, record, and a
// source that cannot be read holds for a native card). Two renderings of one field is how
// one of them drifts, and a reader who learned the field on one line would misread it on
// the other.
func BudgetWord(unmetered bool, tokens, spent int, observed, partial bool) string {
	if unmetered {
		return "unmetered"
	}
	switch {
	case !observed:
		return fmt.Sprintf("%s/%d", Dash, tokens)
	case partial:
		return fmt.Sprintf("%d+/%d", spent, tokens)
	}
	return fmt.Sprintf("%d/%d", spent, tokens)
}

// Sum adds the token columns that are present, and says whether any were missing, so that a
// partial observation prints `budget=<n>+/<n>` with the plus rather than passing for a
// whole one.
func (u ProviderUsage) Sum() (sum int, seen int, partial bool) { return u.add(TokenColumns) }

func (u ProviderUsage) add(columns []string) (sum int, seen int, partial bool) {
	for _, c := range columns {
		n, err := strconv.Atoi(strings.TrimSpace(u.Values[c]))
		if err != nil {
			partial = true
			continue
		}
		sum += n
		seen++
	}
	return sum, seen, partial && seen > 0
}

// ReadProviderUsage reads the provider's own accounting for one job, from the source the
// worker description names. There are two: `opencode`, the job's own database in
// the data home this tool exported for it, and `none`, which reports nothing and under
// which only `--tokens unmetered` tasks may run.
//
// A SOURCE THAT REPORTS NOTHING AND A SOURCE THAT FAILS TO READ ARE NOT THE SAME THING,
// and the budget rests on the difference: the first leaves the budget unable to fire and the
// deadline to end the job, and the second, three samples running, ends the job RUN
// BUDGET-UNVERIFIABLE, because a numeric budget the tool has stopped being able to see is a
// budget the caller believes is enforced and is not.
func ReadProviderUsage(source, dataHome string) (ProviderUsage, error) {
	switch source {
	case UsageOpenCode:
		return readOpenCodeUsage(dataHome)
	case UsageNone, "":
		return ProviderUsage{Values: map[string]string{}}, nil
	default:
		return ProviderUsage{}, fmt.Errorf("the usage source %q is not one this tool reads; it wants `%s` or `%s`", source, UsageOpenCode, UsageNone)
	}
}
