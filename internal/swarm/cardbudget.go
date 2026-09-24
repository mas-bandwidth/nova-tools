package swarm

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
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
	return CountCardTurnsIn(filepath.Join(jobDir, "harness.log"), u)
}

// CountCardTurnsIn is the same count against a NAMED log, because the two routes keep the
// harness's words in two different files and rule 13d says which is which: "Turns are
// counted as rule 13b counts them, with `<job>/harness-output.log` as the log, since
// `native` never writes `harness.log`."
//
// THE NAMES ARE NOT INTERCHANGEABLE. `harness.log` has two owners on the native route
// already -- a batch pins its runner's stdout to it, which is where the NATIVE OK line
// lands -- so counting "assistant" in it would count the machinery's own lines and not the
// model's turns. `harness-output.log` is the capture with one writer (issue #608).
func CountCardTurnsIn(logPath string, u ProviderUsage) int {
	n := countAssistantLines(logPath)
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

// THE MEASURED STARTUP COST (CARD-8349). Stella's read: a 2k cap was smaller
// than the harness's own first context, so every card carrying it would have
// died at once, at load, before doing any work. A card budget is only a budget
// if it is above what the harness spends before the card's first turn; a
// known-answer probe writes the measurement to `<root>/startup-cost.tsv`
// (columns `harness_sha256`, `first_usage_cache_read`, `first_usage_turns`),
// and a description's budget below it is refused BY NAME at load, before any
// launch, and never applied.

// StartupCostFile is the measurement's name under a pool's or a native run's root.
const StartupCostFile = "startup-cost.tsv"

// StartupCost is the harness's measured first turn: what the harness spends
// before the card says anything. Present is false when no file has been
// written yet, which accepts every budget.
type StartupCost struct {
	HarnessSHA256       string
	FirstUsageCacheRead int
	FirstUsageTurns     int
	Present             bool
}

// ReadStartupCost reads `<root>/startup-cost.tsv`. An absent file is not an
// error and answers Present=false: no measurement accepts every budget. A
// header line naming the columns is skipped.
func ReadStartupCost(root string) (StartupCost, error) {
	var s StartupCost
	path := filepath.Join(root, StartupCostFile)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, err
	}
	f, err := openRegularRead(path)
	if err != nil {
		return s, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			fields = strings.Fields(line)
		}
		if len(fields) < 3 {
			continue
		}
		if strings.Contains(fields[0], "harness_sha256") || strings.Contains(fields[1], "first_usage") {
			continue
		}
		cacheRead, err := strconv.Atoi(strings.TrimSpace(fields[1]))
		if err != nil {
			return s, nil
		}
		turns, err := strconv.Atoi(strings.TrimSpace(fields[2]))
		if err != nil {
			return s, nil
		}
		s.HarnessSHA256 = strings.TrimSpace(fields[0])
		s.FirstUsageCacheRead = cacheRead
		s.FirstUsageTurns = turns
		s.Present = true
		return s, sc.Err()
	}
	return s, sc.Err()
}

// RecordStartupCost writes the known-answer measurement at
// `<root>/startup-cost.tsv`, a header and one row.
func RecordStartupCost(root, harnessSHA256 string, cacheRead, turns int) error {
	body := fmt.Sprintf("harness_sha256\tfirst_usage_cache_read\tfirst_usage_turns\n%s\t%d\t%d\n",
		harnessSHA256, cacheRead, turns)
	return writeAtomic(filepath.Join(root, StartupCostFile), []byte(body), 0o644)
}

// RecordStartupCostIfAbsent writes the measurement from the first finished
// task, and only when no measurement is there yet: the first run has nothing
// to compare against, so it accepts its budget and leaves the next run a fact.
func RecordStartupCostIfAbsent(root, harnessSHA256 string, cacheRead, turns int) error {
	if _, err := os.Stat(filepath.Join(root, StartupCostFile)); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return RecordStartupCost(root, harnessSHA256, cacheRead, turns)
}

// HarnessSHA256 is the lowercase hex sha256 of the harness binary, resolved on
// PATH when the description names a bare command. An unreadable harness answers
// the empty string, and the measurement is still written.
func HarnessSHA256(harness string) string {
	path := harness
	if !strings.ContainsRune(path, filepath.Separator) {
		if found, err := exec.LookPath(harness); err == nil {
			path = found
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// BudgetRefusal is the one line a card budget below the measured startup cost
// is refused with, or "" when the budget stands or no measurement exists. A
// max_cache_read below twice the measured first cache read cannot leave room
// for the card; a max_turns below the measured first turns plus two stops the
// card before its first real turn. The refusal names both numbers.
func (w Worker) BudgetRefusal(s StartupCost) string {
	if !s.Present {
		return ""
	}
	if w.MaxCacheRead > 0 && w.MaxCacheRead < 2*s.FirstUsageCacheRead {
		return fmt.Sprintf("BUDGET REFUSED max_cache_read=%d startup=%d (want >= 2x)",
			w.MaxCacheRead, s.FirstUsageCacheRead)
	}
	if w.MaxTurns > 0 && w.MaxTurns < s.FirstUsageTurns+2 {
		return fmt.Sprintf("BUDGET REFUSED max_turns=%d startup=%d (want >= startup+2)",
			w.MaxTurns, s.FirstUsageTurns)
	}
	return ""
}
