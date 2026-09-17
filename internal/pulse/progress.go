package pulse

// progress is SPEC-PULSE's "Progress" verb: past card rate, cost per card, effective
// parallelism and a conservative time-remaining estimate, read from every job's
// usage.tsv and the queue's rows, never from a guess. It makes no model call.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ProgressInput is the progress verb's input, held apart from flag parsing so a test
// can drive it against fake usage files and a fake gh on PATH.
type ProgressInput struct {
	Queue  string // the queue directory: pending, launched, done and the POLICY
	Roots  string // comma-separated swarm roots, the benches whose usage.tsv is read
	Day    string // YYYY-MM-DD the day window starts at; empty means today (UTC)
	Stdout io.Writer
	Stderr io.Writer
	Now    func() time.Time
}

// progressRow is one usage.tsv row reduced to the columns the progress verb reads.
type progressRow struct {
	started time.Time
	ended   time.Time
	rc      string
	usd     float64
}

// Progress prints the PROGRESS line and the ESTIMATE line and returns 0, or 2 when
// it could not run.
func Progress(in ProgressInput) int {
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	if strings.TrimSpace(in.Queue) == "" {
		return refusal(in.Stderr, "PROGRESS", fmt.Errorf("missing --queue; refusing to guess (supply the queue directory)"))
	}
	if strings.TrimSpace(in.Roots) == "" {
		return refusal(in.Stderr, "PROGRESS", fmt.Errorf("missing --roots; refusing to guess (supply the swarm roots, comma separated)"))
	}
	day := strings.TrimSpace(in.Day)
	if day == "" {
		day = in.Now().UTC().Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", day); err != nil {
		return refusal(in.Stderr, "PROGRESS", fmt.Errorf("--day wants YYYY-MM-DD, got %q (pass the day, or omit it for today)", in.Day))
	}

	rows := progressUsage(splitList(in.Roots), day)
	cards := len(rows)
	var rc0 int
	var usdSum, busy float64
	walls := make([]float64, 0, cards)
	var minStart, maxEnd time.Time
	for _, r := range rows {
		if r.rc == "0" {
			rc0++
		}
		usdSum += r.usd
		w := r.ended.Sub(r.started).Seconds()
		if w < 0 {
			w = 0
		}
		walls = append(walls, w)
		busy += w
		if minStart.IsZero() || r.started.Before(minStart) {
			minStart = r.started
		}
		if r.ended.After(maxEnd) {
			maxEnd = r.ended
		}
	}
	p50 := progressRound(progressPercentile(walls, 50))
	p90 := progressPercentile(walls, 90)
	usdPerCard := 0.0
	if cards > 0 {
		usdPerCard = usdSum / float64(cards)
	}
	spanSec := 0.0
	if cards > 0 {
		spanSec = maxEnd.Sub(minStart).Seconds()
		if spanSec < 0 {
			spanSec = 0
		}
	}
	spanH, parallelism, cardsPerHour := 0.0, 0.0, 0.0
	if spanSec > 0 {
		spanH = spanSec / 3600
		parallelism = busy / spanSec
		cardsPerHour = float64(cards) / spanH
	}

	fmt.Fprintf(in.Stdout, "PROGRESS cards=%d rc0=%d wall_p50_s=%d wall_p90_s=%d usd_per_card=%.4f span_h=%.1f effective_parallelism=%.1f cards_per_hour=%.1f\n",
		cards, rc0, p50, progressRound(p90), usdPerCard, spanH, parallelism, cardsPerHour)

	remaining := countCards(in.Queue, "pending") + countCards(in.Queue, "launched")
	repo, scope := progressScope(in.Queue)
	if repo != "" {
		remaining += progressUnreadPRs(repo, in.Queue)
		remaining += 2 * progressInScopeIssues(repo, scope)
	}
	hours := 0.0
	if parallelism > 0 {
		hours = float64(remaining) * (p90 / 3600) / parallelism * 1.5
	}
	fmt.Fprintf(in.Stdout, "ESTIMATE remaining_cards=%d hours=%.1f\n", remaining, hours)
	return 0
}

// progressUsage walks every bench for usage.tsv files and keeps the rows whose start
// falls on the day. Columns are read by header name, so column order never matters.
func progressUsage(roots []string, day string) []progressRow {
	var out []progressRow
	for _, r := range roots {
		_ = filepath.WalkDir(r, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Name() != "usage.tsv" {
				return nil
			}
			for _, row := range parseProgressUsage(path) {
				if row.started.IsZero() || row.started.UTC().Format("2006-01-02") != day {
					continue
				}
				out = append(out, row)
			}
			return nil
		})
	}
	return out
}

// parseProgressUsage reads every data row of one job's usage.tsv by header name.
func parseProgressUsage(path string) []progressRow {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) < 2 {
		return nil
	}
	head := strings.Split(lines[0], "\t")
	var out []progressRow
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		values := strings.Split(line, "\t")
		cols := map[string]string{}
		for i, name := range head {
			if i < len(values) {
				cols[strings.TrimSpace(name)] = values[i]
			}
		}
		var r progressRow
		for _, k := range []string{"started", "start"} {
			if v := strings.TrimSpace(cols[k]); v != "" && v != "-" {
				if t, err := time.Parse(time.RFC3339, v); err == nil {
					r.started = t
					break
				}
			}
		}
		for _, k := range []string{"ended", "end"} {
			if v := strings.TrimSpace(cols[k]); v != "" && v != "-" {
				if t, err := time.Parse(time.RFC3339, v); err == nil {
					r.ended = t
					break
				}
			}
		}
		if r.started.IsZero() {
			continue
		}
		r.rc = strings.TrimSpace(cols["rc"])
		if v := strings.TrimSpace(cols["usd"]); v != "" && v != "-" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				r.usd = f
			}
		}
		out = append(out, r)
	}
	return out
}

// progressPercentile interpolates between the two nearest ranks, so equal walls read
// their own value and a fractional result is carried rather than dropped.
func progressPercentile(walls []float64, p float64) float64 {
	if len(walls) == 0 {
		return 0
	}
	s := append([]float64{}, walls...)
	sort.Float64s(s)
	if len(s) == 1 {
		return s[0]
	}
	rank := (p / 100) * float64(len(s)-1)
	lo := int(rank)
	hi := lo + 1
	if hi >= len(s) {
		return s[len(s)-1]
	}
	return s[lo] + (rank-float64(lo))*(s[hi]-s[lo])
}

func progressRound(f float64) int { return int(f + 0.5) }

// progressScope derives the repo and scope for the estimate from the queue's POLICY:
// the repo from the sources' owner/repo locators and the scope from scope-regex.
// Without a POLICY there is no repo, so the PR and issue terms are zero and the
// estimate counts the queue alone.
func progressScope(queue string) (string, *regexp.Regexp) {
	pol, err := readPolicy(filepath.Join(queue, "POLICY"))
	if err != nil {
		return "", nil
	}
	for _, src := range pol.Sources {
		for _, c := range readCandidates(src) {
			if i := strings.IndexByte(c.ID, '#'); i > 0 {
				if ownerRepo := c.ID[:i]; strings.Contains(ownerRepo, "/") {
					return ownerRepo, pol.Scope
				}
			}
		}
	}
	return "", pol.Scope
}

// progressUnreadPRs counts the repo's open PRs that still need a read: the open PRs
// minus those already recorded in the queue's APPROVED file.
func progressUnreadPRs(repo, queue string) int {
	approved := map[int]bool{}
	for _, l := range readLines(filepath.Join(queue, "APPROVED")) {
		if f := strings.Fields(l); len(f) > 1 {
			if n, err := strconv.Atoi(f[1]); err == nil {
				approved[n] = true
			}
		}
	}
	var rows []struct {
		Number int `json:"number"`
	}
	if out := runGh(childTimeout, "pr", "list", "-R", repo, "--state", "open", "--json", "number"); out != "" {
		_ = json.Unmarshal([]byte(out), &rows)
	}
	n := 0
	for _, r := range rows {
		if !approved[r.Number] {
			n++
		}
	}
	return n
}

// progressInScopeIssues counts the repo's open card-labelled issues in scope. No scope
// means every such issue is in scope.
func progressInScopeIssues(repo string, scope *regexp.Regexp) int {
	var issues []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
	}
	if out := runGh(childTimeout, "issue", "list", "-R", repo, "--state", "open", "--label", "card", "--json", "number,title"); out != "" {
		_ = json.Unmarshal([]byte(out), &issues)
	}
	n := 0
	for _, it := range issues {
		if scope == nil || scope.MatchString(it.Title) {
			n++
		}
	}
	return n
}
