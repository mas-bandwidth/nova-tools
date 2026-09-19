package swarm

import (
	"fmt"
	"os/exec"
	"time"
)

// A BUDGET NEEDS A SOURCE THE TOOL CAN READ, AND THAT IS CHECKED BEFORE ANYTHING IS MADE
// (SPEC-SWARM rule 13d, issue #1545).
//
// Rule 13 already refuses, at exit 2 before the first worker, a pool whose description says
// `usage: none` while a pending task carries a numeric budget, "because a budget nothing can
// observe is a promise the tool cannot keep". Rule 13d holds the native route to the same
// sentence, and adds the second way a source can be unreadable: a bench with no `sqlite3` on
// `PATH`, which is how `usage: opencode` is read at all.
//
// AND IT ADDS THE CARELESS CALLER (PR #1566 decision 19). A description that sets
// `max_cache_read` or `max_turns` is refused under either condition WHATEVER `--tokens`
// says, `unmetered` included, because the card's own budget is read from the same source --
// so `--tokens unmetered` beside a `max_turns` on a `usage: none` bench would otherwise be a
// cap the caller believes is enforced and nothing enforces. `unmetered` with no such
// description runs under both, as it does today.

// NativeUsageSource is the source a native launch reads its budget from: the worker
// description's `usage` when `--worker` names one, and `opencode` when there is none.
// It is one function because "and `opencode` when there is no `--worker`" is a sentence of
// rule 13d's and not a default anybody should retype.
func NativeUsageSource(worker *Worker) string {
	if worker == nil {
		return UsageOpenCode
	}
	return worker.Usage
}

// NativeBudgetSourceRefusal is the reason a native launch is refused for a budget nothing
// can observe, or "" when the launch may proceed. The caller prints it as a NATIVE REFUSED
// line and exits 2, BEFORE any directory is made.
//
// `tokens` and `unmetered` are the budget word as read; `cardBudget` is whether a worker
// description set `max_turns` or `max_cache_read`, and `cardBudgetNames` names which, so the
// refusal tells a caller which field of their description put them here rather than making
// them guess.
func NativeBudgetSourceRefusal(source string, tokens int, unmetered bool, worker *Worker) string {
	numeric := !unmetered && tokens > 0
	card := worker != nil && worker.HasCardBudget()
	if !numeric && !card {
		// `--tokens unmetered` with no card budget: there is no number to observe, the
		// deadline is the only stop, and rule 13d says such a launch runs under both
		// conditions.
		return ""
	}
	why := "a numeric --tokens"
	if !numeric {
		why = "this worker description's " + cardBudgetFields(worker)
	} else if card {
		why = "a numeric --tokens and this worker description's " + cardBudgetFields(worker)
	}
	switch source {
	case UsageNone:
		return fmt.Sprintf("%s wants a usage source this tool can read, and the worker description says `usage: none`, which reports nothing; a budget nothing can observe is a promise the tool cannot keep, so this launch is refused rather than run under a cap that would never fire. Give the description `usage: opencode`, or launch with --tokens unmetered and no max_turns or max_cache_read", why)
	case UsageOpenCode, "":
		if !SQLiteOnPath() {
			return fmt.Sprintf("%s is read from the harness's own database with `%s -readonly`, and %s is on no PATH entry of this bench; a budget nothing can observe is a promise the tool cannot keep, so this launch is refused rather than run under a cap that would never fire. Install %s on this bench, or launch with --tokens unmetered and no max_turns or max_cache_read", why, SQLiteBinary, SQLiteBinary, SQLiteBinary)
		}
		return ""
	default:
		return fmt.Sprintf("%s wants a usage source this tool can read, and the worker description's usage is %q, which is not one this tool reads; it wants `%s` or `%s`", why, source, UsageOpenCode, UsageNone)
	}
}

// cardBudgetFields names the card-budget fields a description actually set, so the refusal
// points at the line the caller wrote.
func cardBudgetFields(worker *Worker) string {
	switch {
	case worker == nil:
		return "card budget"
	case worker.MaxTurns > 0 && worker.MaxCacheRead > 0:
		return "max_turns and max_cache_read"
	case worker.MaxTurns > 0:
		return "max_turns"
	case worker.MaxCacheRead > 0:
		return "max_cache_read"
	}
	return "card budget"
}

// SQLiteOnPath says whether the one program a usage source needs can be resolved. It is
// asked BEFORE a launch by rule 13d's refusal and AFTER one by the final read, and both ask
// it the same way.
func SQLiteOnPath() bool {
	_, err := exec.LookPath(SQLiteBinary)
	return err == nil
}

// DefaultUsageInterval is how often a live sample reads the source. It is a tool property,
// like a timeout, and never a fact about anybody's job. Rule 13 names it for `run` and rule
// 13d gives `native` the same flag and the same default.
const DefaultUsageInterval = 5 * time.Second

// UsageIntervalFloor is the shortest interval `native` accepts. Rule 13d: an interval under
// one second "could end an honest card on three quick reads" -- three failed reads in a row
// end a card `budget-unverifiable`, and at a tenth of a second that is a third of a second
// of bad luck rather than a source that has really stopped answering.
const UsageIntervalFloor = time.Second

// NativeUsageIntervalRefusal is the reason `native` refuses an interval, or "" when it takes
// it. The ceiling is the deadline and it is EXCLUSIVE: an interval equal to the deadline
// would let no sample run at all, because the deadline ends the card at the instant the
// first sample would have been due.
func NativeUsageIntervalRefusal(interval, deadline time.Duration) string {
	switch {
	case interval < UsageIntervalFloor:
		return fmt.Sprintf("--usage-interval is at least %s, got %s; three failed reads in a row end a card budget-unverifiable, and under a second that is a moment's bad luck rather than a source that has stopped answering", UsageIntervalFloor, interval)
	case deadline > 0 && interval >= deadline:
		return fmt.Sprintf("--usage-interval is shorter than --deadline, got %s against a deadline of %s; at or past the deadline no sample would ever run and the budget could not fire", interval, deadline)
	}
	return ""
}
