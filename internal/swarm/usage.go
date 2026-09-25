package swarm

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// USAGE OUTLIVES THE JOB (rule 12).
//
// DeepSeek's usage for two batches on 2026-09-11 lived in per-worker data directories that
// were reclaimed with the jobs, and nothing survived. So the usage file is written OUTSIDE
// the reclaimable subtree, by finalize, BEFORE anything else happens to the job, and
// reclaim refuses without it.
//
// A field the provider did not report is the literal "-", never 0: a zero is a
// measurement and a dash is an absence, and nova-tokens reads this file and reads "-" as
// unknown (SPEC-TOKENS rule 14).

// UsageColumns are the sixteen columns, in this order. The order is the contract.
var UsageColumns = []string{
	"job", "attempt", "from", "started", "ended", "end", "rc", "provider", "model", "repo",
	"tokens_in", "tokens_out", "cache_write", "cache_read", "reasoning", "usd",
}

// The ways a job ends, as the `end` column spells them.
const (
	EndDone         = "done"
	EndKilled       = "killed"
	EndBudget       = "budget"
	EndUnverifiable = "budget-unverifiable"
	EndViolation    = "violation"
	EndFailed       = "failed"
	EndUnknown      = "unknown"
	EndLaunchFailed = "launch-failed"
	EndInputLimit   = "input-limit"
	// EndProvider is a launch that did not take: the harness died inside the launch
	// grace with a provider server error in its tail. It is retried with backoff and,
	// after the third fast failure, is filed with the provider's own ref (issue #900).
	EndProvider = "provider"
	// EndWall is a job the harness's own fence stopped at a path outside it, with no
	// RESULT.md: the death names the path and the commits ./repo kept (issue #918). It is
	// also a card the harness's own fence or the OS wall stopped before it could publish:
	// the machinery ended the task, not the model (wall.go, issue #644's follow-up).
	EndWall = "wall"
)

// Dash is the absence this file writes, and never a zero.
const Dash = "-"

// UsageRow is one job's row.
type UsageRow map[string]string

// UsagePath is <pool>/usage/<job>.tsv, one per job id, outside everything reclaim removes.
func (p *Pool) UsagePath(id string) string { return p.Path(Usage, id+".tsv") }

// WriteUsage writes a job's usage file, once and never again: a job's second attempt is a
// new job id with its own file, so cost sums each attempt once and a retry never
// double-counts. It reports whether the file already existed.
func (p *Pool) WriteUsage(id string, row UsageRow) (string, bool, error) {
	path := p.UsagePath(id)
	if _, err := os.Stat(path); err == nil {
		return path, true, nil
	}
	var head, values []string
	for _, c := range UsageColumns {
		v := strings.TrimSpace(row[c])
		if v == "" {
			v = Dash
		}
		// A tab or a newline in a value would make one row read as two fields or two rows;
		// the file is tab-separated and this is where that is kept true.
		v = strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(v)
		head = append(head, c)
		values = append(values, v)
	}
	body := strings.Join(head, "\t") + "\n" + strings.Join(values, "\t") + "\n"
	return path, false, writeAtomic(path, []byte(body), 0o644)
}

// ReadUsage reads every usage row in the pool, in job-id order, which is time order.
func (p *Pool) ReadUsage() ([]UsageRow, error) {
	entries, err := os.ReadDir(p.Path(Usage))
	if err != nil {
		return nil, err
	}
	var out []UsageRow
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tsv") {
			continue
		}
		row, err := readUsageFile(filepath.Join(p.Path(Usage), e.Name()))
		if err != nil {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

func readUsageFile(path string) (UsageRow, error) {
	// The usage row is written by writeAtomic (WriteUsage) and read back by `report` and
	// `reclaim` while a run is still finalizing other jobs: same rename, same collision.
	raw, err := readFileSteady(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("%s holds no row", path)
	}
	head := strings.Split(lines[0], "\t")
	// ONE FILE, ONE ROW is the pool's shape; ONE FILE, MANY ROWS is a retried native card
	// (issue #900), where each launch appended its own row. The last row names the card; the
	// numeric columns are summed across every row, and a column any attempt left a dash
	// stays a dash rather than becoming a zero. A single-row file reads exactly as before.
	if len(lines) > 2 {
		return foldUsageRows(head, lines[1:]), nil
	}
	values := strings.Split(lines[1], "\t")
	row := UsageRow{}
	for i, name := range head {
		if i < len(values) {
			row[name] = values[i]
		}
	}
	return row, nil
}

// foldUsageRows sums the numeric columns of a multi-row usage file and keeps the last row's
// naming columns. A dash in any attempt for a column keeps that column a dash: a sum that
// counted a missing attempt as zero would be a measurement the provider never made.
func foldUsageRows(head []string, lines []string) UsageRow {
	split := func(line string) map[string]string {
		out := map[string]string{}
		values := strings.Split(line, "\t")
		for i, name := range head {
			if i < len(values) {
				out[name] = values[i]
			}
		}
		return out
	}
	row := split(lines[len(lines)-1])
	numeric := append(append([]string{}, TokenColumns...), "usd")
	for _, name := range numeric {
		sum := 0.0
		complete := true
		for _, line := range lines {
			f, err := strconv.ParseFloat(strings.TrimSpace(split(line)[name]), 64)
			if err != nil {
				complete = false
				break
			}
			sum += f
		}
		switch {
		case !complete:
			row[name] = Dash
		case name == "usd":
			row[name] = strconv.FormatFloat(sum, 'f', 4, 64)
		default:
			row[name] = strconv.FormatInt(int64(sum), 10)
		}
	}
	return row
}

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

// BudgetColumns are the columns rule 13's budget counts: what the job SENT, what it
// GENERATED, and what it REASONED. Cache reads are the provider re-reading context it
// already holds, and cache writes are that context being laid down once; neither is work
// this job asked for, and counting them ended a real four-million-token job at 4.18M in
// under three minutes with zero findings (dogfood D6, 2026-09-11). The usage ROW keeps all
// five columns -- `cost` reads what it always read -- the BUDGET counts these three.
var BudgetColumns = []string{"tokens_in", "tokens_out", "reasoning"}

// Budget is rule 13's observed spend: the same arithmetic as Sum over BudgetColumns.
func (u ProviderUsage) Budget() (sum int, seen int, partial bool) { return u.add(BudgetColumns) }

// BudgetWord is rule 13's ceiling and what was observed under it, in the four spellings
// the document names, and it is the ONE place either route renders them:
//
//	unmetered   the caller said this provider has no live accounting
//	-/<n>       nothing was observed, so the run is never reported as under budget
//	<s>+/<n>    a PARTIAL observation: some columns were dashes, so it can show that the
//	            budget was reached and can never show that the job stayed under it
//	<s>/<n>     a whole observation
//
// THE POOL'S `RUN DONE`/`RUN KILLED` AND `native`'s `NATIVE OK` BOTH COME THROUGH HERE
// (rule 13d: "Everything rule 13 says about the word, the sum, the stop, the record and a
// source that cannot be read holds for a native card"). Two renderings of one field is how
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
// worker description names (rule 13). There are two: `opencode`, the job's own database in
// the data home this tool exported for it, and `none`, which reports nothing and under
// which only `--tokens unmetered` tasks may run.
//
// A SOURCE THAT REPORTS NOTHING AND A SOURCE THAT FAILS TO READ ARE NOT THE SAME THING,
// and rule 13 rests on the difference: the first leaves the budget unable to fire and the
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

// Stamp is the one time format this tool writes: a UTC instant, seconds resolution.
func Stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }
