package swarm

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// THE CARD BUDGET (CARD-8317): a runaway card is a prompt defect, not a model
// one, and the pool says so.
//
// A worker description optionally carries `max_turns` and `max_cache_read`.
// The supervisor samples usage every --usage-interval beside the token
// budget (rule 13); when a running task's cache_read exceeds max_cache_read,
// or its turn count exceeds max_turns, the supervisor stops the task on the
// existing stop path used for the deadline, the job moves to failed/ with
// end=budget in usage.tsv, and the task's report carries
// `PROMPT-DEFECT task=<id> reason=budget cache_read=<n> max=<m> turns=<t>`.

// HasCardBudget reports whether the description carries any card budget.
func (w Worker) HasCardBudget() bool { return w.MaxTurns > 0 || w.MaxCacheRead > 0 }

// CardCacheRead is the observed cache_read as a number, and whether it was one.
func CardCacheRead(u ProviderUsage) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(u.Values["cache_read"]))
	if err != nil {
		return 0, false
	}
	return n, true
}

// CountCardTurns counts a job's assistant turns: the harness output log's
// assistant turns, counted the way usage already counts them (assistant
// rows/lines), or the usage row count where the log has fewer. A missing log
// answers the usage row count; both missing is zero turns.
func CountCardTurns(jobDir string, u ProviderUsage) int {
	n := countAssistantLines(filepath.Join(jobDir, "harness.log"))
	if u.Turns > n {
		n = u.Turns
	}
	return n
}

// countAssistantLines counts the harness log lines that carry an assistant
// turn, the way the usage source counts assistant rows of the message table.
func countAssistantLines(path string) int {
	f, err := openRegularRead(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	n := 0
	for sc.Scan() {
		if strings.Contains(strings.ToLower(sc.Text()), "assistant") {
			n++
		}
	}
	return n
}

// OverCardBudget reports whether the observed usage and turn count exceed the
// description's card budget. It returns the observed cache_read (0 for an
// absence, which never fires) and the budget that fired: max_cache_read where
// the cache fired, else max_turns.
func (w Worker) OverCardBudget(u ProviderUsage, turns int) (over bool, cacheRead, max int) {
	if n, ok := CardCacheRead(u); ok {
		cacheRead = n
	}
	if w.MaxCacheRead > 0 && cacheRead > w.MaxCacheRead {
		return true, cacheRead, w.MaxCacheRead
	}
	if w.MaxTurns > 0 && turns > w.MaxTurns {
		return true, cacheRead, w.MaxTurns
	}
	return false, cacheRead, 0
}

// PromptDefectLine is the report line a budget stop writes into the task's
// report. One line, the one-line output grammar's fields, no payload.
func PromptDefectLine(id string, cacheRead, max, turns int) string {
	return fmt.Sprintf("PROMPT-DEFECT task=%s reason=budget cache_read=%d max=%d turns=%d",
		id, cacheRead, max, turns)
}

// AppendPromptDefect writes the PROMPT-DEFECT line into the task's report,
// creating the report where the worker published none. A line for this task
// is written once: a report that already carries one is left alone.
func AppendPromptDefect(jobDir, id string, cacheRead, max, turns int) error {
	line := PromptDefectLine(id, cacheRead, max, turns)
	path := ResultPath(jobDir)
	if raw, err := readFileSteady(path); err == nil && strings.Contains(string(raw), "PROMPT-DEFECT task="+id+" ") {
		return nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.Size() > 0 {
		if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 && raw[len(raw)-1] != '\n' {
			if _, err := f.WriteString("\n"); err != nil {
				return err
			}
		}
	}
	_, err = f.WriteString(line + "\n")
	return err
}

// cardBudgetMaxWord renders the budget that fired for messages that carry it.
func cardBudgetMaxWord(max int) string { return strconv.Itoa(max) }
