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
func Cost(p *Pool, since string, max int, stdout, stderr io.Writer) int {
	rows, err := p.ReadUsage()
	if err != nil {
		fmt.Fprintf(stderr, "COST REFUSED: the usage directory could not be read: %s\n", oneline.Escape(redactedReason(err)))
		return 2
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i]["job"] < rows[j]["job"] })
	list := bounded.Capped(stdout, max, "COST", "task", "nova-swarm cost --pool "+p.Dir+" --max 0")
	totals := map[string]int{}
	dashes := map[string]int{}
	usd := 0.0
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
			usd += f
		}
		list.Line(fmt.Sprintf("COST TASK id=%s attempt=%s end=%s in=%s out=%s cache_write=%s cache_read=%s reasoning=%s usd=%s model=%s repo=%s",
			oneline.Field(row["job"]), oneline.Field(dashOr(row["attempt"])), oneline.Field(dashOr(row["end"])),
			oneline.Field(dashOr(row["tokens_in"])), oneline.Field(dashOr(row["tokens_out"])),
			oneline.Field(dashOr(row["cache_write"])), oneline.Field(dashOr(row["cache_read"])),
			oneline.Field(dashOr(row["reasoning"])), oneline.Field(dashOr(row["usd"])),
			oneline.Field(dashOr(row["model"])), oneline.Field(dashOr(row["repo"]))))
	}
	list.More()
	fmt.Fprintf(stdout, "COST OK tasks=%d in=%d out=%d cache_write=%d cache_read=%d reasoning=%d dashes=%d,%d,%d,%d,%d usd=%.4f window=%s..%s\n",
		tasks, totals["tokens_in"], totals["tokens_out"], totals["cache_write"], totals["cache_read"], totals["reasoning"],
		dashes["tokens_in"], dashes["tokens_out"], dashes["cache_write"], dashes["cache_read"], dashes["reasoning"],
		usd, oneline.Field(dashOr(first)), oneline.Field(dashOr(last)))
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
