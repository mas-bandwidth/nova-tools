package swarm

import (
	"bufio"
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
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) < 2 {
		return nil, fmt.Errorf("%s holds no row", path)
	}
	head := strings.Split(lines[0], "\t")
	values := strings.Split(lines[1], "\t")
	row := UsageRow{}
	for i, name := range head {
		if i < len(values) {
			row[name] = values[i]
		}
	}
	return row, nil
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
}

// TokenColumns are nova-tokens's five types, which are the five this tool sums.
var TokenColumns = []string{"tokens_in", "tokens_out", "cache_write", "cache_read", "reasoning"}

// Sum adds the token columns that are present, and says whether any were missing, so that a
// partial observation prints `budget=<n>+/<n>` with the plus rather than passing for a
// whole one.
func (u ProviderUsage) Sum() (sum int, seen int, partial bool) {
	for _, c := range TokenColumns {
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

// UsageFileName is the file the harness writes its own accounting into, inside the data
// home this tool exported for the job.
//
// THE SPEC NAMES SQLITE, AND THIS TOOL CANNOT READ ONE. SPEC-SWARM rule 12 says the source
// for OpenCode is the SQLite database in the data home, read once, read-only. This repo is
// standard library only (CONTRIBUTING.md, shape before substance), and the standard library
// has no SQLite reader; a third-party driver would show up as a go.mod diff and is the one
// thing that cannot arrive here silently. So the source this tool reads is a tab-separated
// file the harness writes beside its database, one header line and one row per sample, the
// last row winning -- the same five token types, the same dash-is-absence rule. It is
// recorded as a gap rather than a decision: see RESULT.md, "What the spec did not tell me".
const UsageFileName = "usage.tsv"

// ReadProviderUsage reads the provider's own accounting for one job.
//
// A MISSING FILE IS NOT AN ERROR: it is the provider reporting nothing YET, which rule 13
// distinguishes carefully from a source that FAILS to read. The first leaves the budget
// unable to fire and the deadline to end the job; the second, three samples running, ends
// the job RUN BUDGET-UNVERIFIABLE, because a numeric budget the tool has stopped being able
// to see is a budget the caller believes is enforced and is not.
func ReadProviderUsage(dataHome string) (ProviderUsage, error) {
	path := filepath.Join(dataHome, UsageFileName)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ProviderUsage{Values: map[string]string{}}, nil
		}
		return ProviderUsage{}, fmt.Errorf("the usage source %s could not be read: %s", path, redactedReason(err))
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var head, last []string
	for sc.Scan() {
		fields := strings.Split(strings.TrimRight(sc.Text(), "\r"), "\t")
		if head == nil {
			head = fields
			continue
		}
		if len(strings.TrimSpace(strings.Join(fields, ""))) == 0 {
			continue
		}
		last = fields
	}
	if err := sc.Err(); err != nil {
		return ProviderUsage{}, fmt.Errorf("the usage source %s could not be read: %s", path, redactedReason(err))
	}
	if head == nil {
		return ProviderUsage{}, fmt.Errorf("the usage source %s has no header row", path)
	}
	if last == nil {
		return ProviderUsage{Values: map[string]string{}}, nil
	}
	values := map[string]string{}
	for i, name := range head {
		if i < len(last) {
			values[strings.TrimSpace(name)] = strings.TrimSpace(last[i])
		}
	}
	return ProviderUsage{Values: values, Observed: true}, nil
}

// Stamp is the one time format this tool writes: a UTC instant, seconds resolution.
func Stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }
