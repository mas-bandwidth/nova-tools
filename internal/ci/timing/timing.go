// Package timing is the machine behind the committed script cmd/nova-ci/timing.go,
// which prints one pull request's time from open to all-green split into queue,
// setup and test per job, for the last 200 pull requests of
// mas-bandwidth/schema and mas-bandwidth/nova-tools (ideas #791).
//
// The measurement reads a harvested events log: one JSON object per line, one
// line per CI job of one pull request, carrying the moments the forge reported
// -- when the PR opened, when the job was queued, when it left the queue, when
// setup finished and testing began, and when it finished, plus whether it was
// green. The three spans of a job are the job's own: queue is queued to
// started, the wait for a runner; setup is started to setup-done; test is
// setup-done to done. A PR's open-to-all-green is the envelope from its open
// to the latest done among its jobs, and it exists only when every one of the
// PR's jobs is green -- a PR still red or still open has no all-green, and its
// rows print "-" for it, the same reading the measured size tables give a size
// they refuse to guess downward.
//
// The package is deterministic on purpose: no clock, no network, no map order
// in the output. The same log renders byte-for-byte the same table, and
// re-running the script reproduces the committed table -- that is the whole
// point, and TestTimingTableReproduces holds the script's own output against
// the table committed beside it.
package timing

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DefaultRepos are the repositories the script measures when --repos is not
// given: the two the card names, schema and nova-tools.
var DefaultRepos = []string{"mas-bandwidth/nova-tools", "mas-bandwidth/schema"}

// DefaultLast is how many pull requests per repository the script measures
// when --last is not given.
const DefaultLast = 200

// Event is one line of the harvested log: one CI job of one pull request,
// with the moments the forge reported. Times are RFC 3339 in UTC; Done and
// Green must be stated -- a job still running is not in the harvest yet, and
// a line that does not say whether its job was green is a line the table
// cannot use.
type Event struct {
	Repo      string `json:"repo"`       // <owner>/<name>
	PR        int    `json:"pr"`         // the pull request's number
	Job       string `json:"job"`        // the job's name, as the forge prints it
	Opened    string `json:"opened"`     // when the PR opened
	Queued    string `json:"queued"`     // when the job entered the queue
	Started   string `json:"started"`    // when the job left the queue
	SetupDone string `json:"setup_done"` // when setup finished and testing began
	Done      string `json:"done"`       // when the job finished
	Green     *bool  `json:"green"`      // whether the job finished green
}

// Row is one line of the rendered table. Queue, Setup and Test are one job's
// own spans; Opened, AllGreen and AllGreenAt are its pull request's: AllGreen
// is true only when every job of the PR was green, and AllGreenAt is the
// moment it went all-green, the latest Done among its jobs.
type Row struct {
	Repo       string
	PR         int
	Job        string
	Opened     time.Time
	Queue      time.Duration
	Setup      time.Duration
	Test       time.Duration
	AllGreen   bool
	AllGreenAt time.Time
}

// Load reads the harvested events log, one JSON object per line, and refuses
// what a table cannot use: a line that is not JSON, a field missing or a time
// unparseable, a clock that runs backwards, and a job named twice. A blank
// line is skipped. Every refusal names its line, so a bad harvest is a
// one-line fix and not a hunt.
func Load(r io.Reader) ([]Event, error) {
	var events []Event
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, fmt.Errorf("line %d is not one JSON object this package can read: %w", n, err)
		}
		if e.Repo == "" || !strings.Contains(e.Repo, "/") {
			return nil, fmt.Errorf("line %d: repo wants the repository as <owner>/<name> (got %q)", n, e.Repo)
		}
		if e.PR <= 0 {
			return nil, fmt.Errorf("line %d: pr wants the pull request's number, a whole number above zero (got %d)", n, e.PR)
		}
		if e.Job == "" {
			return nil, fmt.Errorf("line %d: job wants the job's name as the forge prints it", n)
		}
		if e.Green == nil {
			return nil, fmt.Errorf("line %d: green must be stated, true or false; a line that does not say cannot be measured", n)
		}
		opened, err := stamp(n, "opened", e.Opened)
		if err != nil {
			return nil, err
		}
		queued, err := stamp(n, "queued", e.Queued)
		if err != nil {
			return nil, err
		}
		started, err := stamp(n, "started", e.Started)
		if err != nil {
			return nil, err
		}
		setupDone, err := stamp(n, "setup_done", e.SetupDone)
		if err != nil {
			return nil, err
		}
		done, err := stamp(n, "done", e.Done)
		if err != nil {
			return nil, err
		}
		for _, bad := range []struct {
			what       string
			have, want time.Time // have must not sit before want
		}{
			{"queued before the PR opened", queued, opened},
			{"started before it was queued", started, queued},
			{"setup_done before it started", setupDone, started},
			{"done before setup finished", done, setupDone},
		} {
			if bad.have.Before(bad.want) {
				return nil, fmt.Errorf("line %d: the clock runs backwards: %s", n, bad.what)
			}
		}
		key := e.Repo + "#" + strconv.Itoa(e.PR) + "#" + e.Job
		if seen[key] {
			return nil, fmt.Errorf("line %d: job %q of %s PR %d appears twice; a harvest that repeats a job is a harvest the table cannot read", n, e.Job, e.Repo, e.PR)
		}
		seen[key] = true
		events = append(events, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading the events log: %w", err)
	}
	return events, nil
}

// stamp reads one RFC 3339 timestamp in UTC and refuses anything else.
func stamp(n int, name, raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("line %d: %s wants an RFC 3339 time in UTC (got %q)", n, name, raw)
	}
	return t.UTC(), nil
}

// Select keeps the events of the named repositories and, per repository, of
// the last `last` pull requests the log holds -- the `last` greatest distinct
// PR numbers, the log's own ordering of recency (numbers may have gaps). A pull request is selected WHOLE, every line of it or
// none, because a table that dropped one job of a PR it kept would invent an
// all-green the forge never reported. A `last` of zero or less keeps
// everything.
func Select(events []Event, repos []string, last int) []Event {
	want := map[string]bool{}
	for _, r := range repos {
		want[r] = true
	}
	// The N greatest DISTINCT PR numbers per repository, not a numeric window
	// below the top: issues and pull requests share one counter on GitHub, so
	// PR numbers have gaps and `top-last+1` would select fewer than `last`.
	prs := map[string]map[int]bool{}
	for _, e := range events {
		if !want[e.Repo] {
			continue
		}
		if prs[e.Repo] == nil {
			prs[e.Repo] = map[int]bool{}
		}
		prs[e.Repo][e.PR] = true
	}
	keep := map[string]map[int]bool{}
	for repo, set := range prs {
		nums := make([]int, 0, len(set))
		for pr := range set {
			nums = append(nums, pr)
		}
		sort.Sort(sort.Reverse(sort.IntSlice(nums)))
		if last > 0 && len(nums) > last {
			nums = nums[:last]
		}
		keep[repo] = map[int]bool{}
		for _, pr := range nums {
			keep[repo][pr] = true
		}
	}
	var kept []Event
	for _, e := range events {
		if want[e.Repo] && keep[e.Repo][e.PR] {
			kept = append(kept, e)
		}
	}
	return kept
}

// Rows converts the selected events to the rows the table renders. Load has
// already refused a log a table cannot use; this re-checks what a hand-built
// Event could still get wrong, so the exported API has no silent path.
func Rows(events []Event) ([]Row, error) {
	openedOf := map[string]time.Time{}
	allGreenAtOf := map[string]time.Time{}
	allGreenOf := map[string]bool{}
	for _, e := range events {
		if e.Green == nil {
			return nil, fmt.Errorf("%s PR %d job %q does not say whether it was green", e.Repo, e.PR, e.Job)
		}
		key := e.Repo + "#" + strconv.Itoa(e.PR)
		opened, err := parse(e.Opened)
		if err != nil {
			return nil, fmt.Errorf("%s PR %d job %q: %s", e.Repo, e.PR, e.Job, err)
		}
		done, err := parse(e.Done)
		if err != nil {
			return nil, fmt.Errorf("%s PR %d job %q: %s", e.Repo, e.PR, e.Job, err)
		}
		if prev, ok := openedOf[key]; !ok || opened.Before(prev) {
			openedOf[key] = opened
		}
		if prev, ok := allGreenAtOf[key]; !ok || done.After(prev) {
			allGreenAtOf[key] = done
		}
		if !*e.Green {
			allGreenOf[key] = false
		} else if _, ok := allGreenOf[key]; !ok {
			allGreenOf[key] = true
		}
	}
	var rows []Row
	for _, e := range events {
		queued, err := parse(e.Queued)
		if err != nil {
			return nil, fmt.Errorf("%s PR %d job %q: %s", e.Repo, e.PR, e.Job, err)
		}
		started, err := parse(e.Started)
		if err != nil {
			return nil, fmt.Errorf("%s PR %d job %q: %s", e.Repo, e.PR, e.Job, err)
		}
		setupDone, err := parse(e.SetupDone)
		if err != nil {
			return nil, fmt.Errorf("%s PR %d job %q: %s", e.Repo, e.PR, e.Job, err)
		}
		done, err := parse(e.Done)
		if err != nil {
			return nil, fmt.Errorf("%s PR %d job %q: %s", e.Repo, e.PR, e.Job, err)
		}
		key := e.Repo + "#" + strconv.Itoa(e.PR)
		rows = append(rows, Row{
			Repo: e.Repo, PR: e.PR, Job: e.Job,
			Opened:     openedOf[key],
			Queue:      started.Sub(queued),
			Setup:      setupDone.Sub(started),
			Test:       done.Sub(setupDone),
			AllGreen:   allGreenOf[key],
			AllGreenAt: allGreenAtOf[key],
		})
	}
	return rows, nil
}

// parse reads one RFC 3339 timestamp in UTC and refuses anything else.
func parse(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s wants an RFC 3339 time in UTC", raw)
	}
	return t.UTC(), nil
}

// Render sorts the rows -- repo, then PR, then job -- and renders them as the
// TSV the script prints: a header line, then one row per job, its three spans
// in whole seconds and its PR's open-to-all-green, or "-" when the PR never
// went all-green.
func Render(rows []Row) string {
	sorted := append([]Row(nil), rows...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Repo != sorted[j].Repo {
			return sorted[i].Repo < sorted[j].Repo
		}
		if sorted[i].PR != sorted[j].PR {
			return sorted[i].PR < sorted[j].PR
		}
		return sorted[i].Job < sorted[j].Job
	})
	var b strings.Builder
	b.WriteString("repo\tpr\tjob\topened\tqueue_s\tsetup_s\ttest_s\tpr_open_to_green_s\n")
	for _, r := range sorted {
		envelope := "-"
		if r.AllGreen {
			envelope = seconds(r.AllGreenAt.Sub(r.Opened))
		}
		fmt.Fprintf(&b, "%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Repo, r.PR, r.Job, r.Opened.UTC().Format(time.RFC3339),
			seconds(r.Queue), seconds(r.Setup), seconds(r.Test), envelope)
	}
	return b.String()
}

// seconds renders a duration as the whole seconds it holds, floored: the
// harvest's own resolution is whole seconds.
func seconds(d time.Duration) string { return strconv.FormatInt(int64(d/time.Second), 10) }
