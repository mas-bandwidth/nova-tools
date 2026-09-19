package swarm

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// COST READS <pool>/usage/ AND NOTHING ELSE, which is why it answers after a reclaim.
//
// A fleet can burn a session: one fleet ran about 9M tokens and three parallel workflows hit
// the limit in 20 minutes (Glenn, 2026-09-10). A pool whose cost is invisible is a pool that
// is discovered to be expensive by being cut off.
//
// A ZERO IS A MEASUREMENT AND A DASH IS AN ABSENCE. COST OK carries `dashes=` so a total
// with an absence in it is never read as complete.
func Cost(p *Pool, since string, max int, by string, summaryOnly bool, stdout, stderr io.Writer) int {
	switch by {
	case "", "model", "day", "repo":
	default:
		fmt.Fprintf(stderr, "COST REFUSED: --by wants model, day or repo, got %s\n", oneline.Field(by))
		return 2
	}
	rows, err := p.ReadUsage()
	if err != nil {
		fmt.Fprintf(stderr, "COST REFUSED: the usage directory could not be read: %s\n", oneline.Escape(redactedReason(err)))
		return 2
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i]["job"] < rows[j]["job"] })
	list := bounded.Capped(stdout, max, "COST", "task", "nova-swarm cost --pool "+p.Dir+" --max 0")
	totals := map[string]int{}
	dashes := map[string]int{}
	groups := map[string]*costGroup{}
	knownUSD := 0.0
	knownUSDCount := 0
	usdMissing := 0
	first, last := "", ""
	tasks := 0
	for _, row := range rows {
		if since != "" && row["job"] < since {
			continue
		}
		tasks++
		if first == "" || row["started"] < first {
			first = row["started"]
		}
		if row["ended"] > last {
			last = row["ended"]
		}
		for _, c := range TokenColumns {
			if n, ok := row.Int(c); ok {
				totals[c] += n
			} else {
				dashes[c]++
			}
		}
		if f, err := strconv.ParseFloat(strings.TrimSpace(row["usd"]), 64); err == nil {
			knownUSD += f
			knownUSDCount++
		} else {
			usdMissing++
		}
		if by != "" {
			addCostGroup(groups, row, by)
		}
		if summaryOnly {
			continue
		}
		list.Line(fmt.Sprintf("COST TASK id=%s attempt=%s end=%s in=%s out=%s cache_write=%s cache_read=%s reasoning=%s usd=%s model=%s repo=%s",
			oneline.Field(row["job"]), oneline.Field(dashOr(row["attempt"])), oneline.Field(dashOr(row["end"])),
			oneline.Field(dashOr(row["tokens_in"])), oneline.Field(dashOr(row["tokens_out"])),
			oneline.Field(dashOr(row["cache_write"])), oneline.Field(dashOr(row["cache_read"])),
			oneline.Field(dashOr(row["reasoning"])), oneline.Field(dashOr(row["usd"])),
			oneline.Field(dashOr(row["model"])), oneline.Field(dashOr(row["repo"]))))
	}
	list.More()
	if by != "" {
		printCostGroups(stdout, by, groups)
	}
	// A zero is a measured value, while an all-unknown pool has no USD total. In a
	// mixed pool `usd` remains the known subtotal for compatibility; `usd_missing`
	// makes that partial coverage explicit and `known_usd` gives machine readers a
	// stable name for the subtotal.
	usd := "-"
	if tasks == 0 || knownUSDCount > 0 {
		usd = fmt.Sprintf("%.4f", knownUSD)
	}
	fmt.Fprintf(stdout, "COST OK tasks=%d in=%d out=%d cache_write=%d cache_read=%d reasoning=%d dashes=%d,%d,%d,%d,%d usd=%s window=%s..%s known_usd=%.4f usd_missing=%d\n",
		tasks, totals["tokens_in"], totals["tokens_out"], totals["cache_write"], totals["cache_read"], totals["reasoning"],
		dashes["tokens_in"], dashes["tokens_out"], dashes["cache_write"], dashes["cache_read"], dashes["reasoning"],
		usd, oneline.Field(dashOr(first)), oneline.Field(dashOr(last)), knownUSD, usdMissing)
	return 0
}

// RATE LIMITS ARE THE DISPATCHER'S BUSINESS (SPEC-SWARM, "cost per task, and rate limits").
//
// A provider's 429 is not a failed task: the dispatcher holds the slot, waits the interval
// the provider names, or this tool's own --backoff, and retries the SAME task once. A second
// 429 fails it with rc=429 in its sidecar and its usage row, so `triage` can see that a
// batch's silence was a limit rather than a set of bad tasks.
const (
	// DefaultBackoff is the wait before the first retry when the provider names none.
	DefaultBackoff = 30 * time.Second
	// MaxBackoff is the cap the doubling never passes: the machinery never waits forever.
	MaxBackoff = 5 * time.Minute
)

// Backoff is the wait before retry n, where 1 is the first retry: the base doubled n-1
// times and capped at five minutes. A base of zero or less takes the 30-second default, so
// a caller that names no backoff still gets the spec's own.
func Backoff(retry int, base time.Duration) time.Duration {
	if base <= 0 {
		base = DefaultBackoff
	}
	d := base
	for i := 1; i < retry; i++ {
		if d >= MaxBackoff {
			return MaxBackoff
		}
		d *= 2
	}
	if d > MaxBackoff {
		return MaxBackoff
	}
	return d
}

// ProviderRetryAfter reads the interval a provider named in its own output: the token
// `retry-after`, then a whole number of seconds. A log that names none returns false, and
// the caller falls back to --backoff.
func ProviderRetryAfter(log []byte) (time.Duration, bool) {
	for _, line := range strings.Split(string(log), "\n") {
		i := strings.Index(strings.ToLower(line), "retry-after")
		if i < 0 {
			continue
		}
		rest := strings.TrimLeft(line[i+len("retry-after"):], ": =")
		var n int
		if _, err := fmt.Sscanf(rest, "%d", &n); err == nil && n >= 0 {
			return time.Duration(n) * time.Second, true
		}
	}
	return 0, false
}

// RateLimited reports whether the harness's own output says the provider answered 429.
//
// The status is read from TEXT rather than the exit code, because POSIX truncates 429 to
// 173: a harness cannot hand the number back through its exit status. The bare number must
// sit beside a status word (http, status, code, error) or be one of the phrases a provider
// uses, so a token count of 429 is not read as a rate limit.
func RateLimited(log []byte) bool {
	text := strings.ToLower(string(log))
	if strings.Contains(text, "rate limit") || strings.Contains(text, "too many requests") {
		return true
	}
	for i := 0; i+3 <= len(text); i++ {
		if text[i:i+3] != "429" {
			continue
		}
		if i > 0 && isDigit(text[i-1]) {
			continue
		}
		if i+3 < len(text) && isDigit(text[i+3]) {
			continue
		}
		start := i - 20
		if start < 0 {
			start = 0
		}
		before := text[start:i]
		if strings.Contains(before, "http") || strings.Contains(before, "status") ||
			strings.Contains(before, "code") || strings.Contains(before, "error") {
			return true
		}
	}
	return false
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// costGroup is one `--by` bucket: the pooled sums of every task that shares its key.
type costGroup struct {
	tasks      int
	sums       map[string]int
	knownUSD   float64
	usdMissing bool
}

// costGroupKey is the value a row contributes to the group: the model, the repo, or the
// calendar day of the row's start. A field the row did not report is the dash, never an
// empty key that would silently merge two absences.
func costGroupKey(row UsageRow, by string) string {
	switch by {
	case "model":
		return dashOr(row["model"])
	case "repo":
		return dashOr(row["repo"])
	case "day":
		started := strings.TrimSpace(row["started"])
		if len(started) >= 10 {
			return started[:10]
		}
		return dashOr(started)
	}
	return ""
}

// addCostGroup folds one row into its group. A token column the row did not report is an
// absence and contributes nothing to the sum; a USD of `-` marks the whole group unknown.
func addCostGroup(groups map[string]*costGroup, row UsageRow, by string) {
	key := costGroupKey(row, by)
	g := groups[key]
	if g == nil {
		g = &costGroup{sums: map[string]int{}}
		groups[key] = g
	}
	g.tasks++
	for _, c := range TokenColumns {
		if n, ok := row.Int(c); ok {
			g.sums[c] += n
		}
	}
	if f, err := strconv.ParseFloat(strings.TrimSpace(row["usd"]), 64); err == nil {
		g.knownUSD += f
	} else {
		g.usdMissing = true
	}
}

// printCostGroups writes one `COST BY` line per group, most cache reads first, and the
// value breaks ties so two runs over one pool print the same page.
func printCostGroups(stdout io.Writer, by string, groups map[string]*costGroup) {
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		ci, cj := groups[keys[i]].sums["cache_read"], groups[keys[j]].sums["cache_read"]
		if ci != cj {
			return ci > cj
		}
		return keys[i] < keys[j]
	})
	for _, key := range keys {
		g := groups[key]
		usd := "-"
		if !g.usdMissing {
			usd = fmt.Sprintf("%.4f", g.knownUSD)
		}
		fmt.Fprintf(stdout, "COST BY %s=%s tasks=%d in=%d out=%d cache_write=%d cache_read=%d reasoning=%d usd=%s\n",
			oneline.Field(by), oneline.Field(key),
			g.tasks, g.sums["tokens_in"], g.sums["tokens_out"], g.sums["cache_write"],
			g.sums["cache_read"], g.sums["reasoning"], usd)
	}
}
