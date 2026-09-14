package wake

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Check runs on a named head -- --run, the amendment of 2026-09-13.
//
// It exists beside --entry because a run is not always attached to a pull
// request -- a push to a branch under a merge lane, a tag, a nightly, a commit
// another line is landing -- and because the thing a window waits for is THIS
// HEAD IS GREEN and not THIS NUMBER IS GREEN: an entry whose head moves is a
// different run with the same number.
//
// The state value is the entry value with NO ENTRY STATE, computed by the same
// bucket rule (entry.go, not re-spelled here) over the head's check runs and
// its status contexts, and FINAL has the entry definition with one clause fewer
// -- pending=0 with at least one check; there is no MERGED or CLOSED to be
// final by.
//
// A QUEUED RUN IS NOT A RUNNING ONE, AND THIS SOURCE CANNOT TELL THEM APART.
// pending= counts both. What the window can do is set --entry-interval to the
// run's expected length and read pending= unchanged across two ticks as the
// tell; what this tool does not do is model a runner pool.

// RunCheckLimit is the page this source does not turn. A head with more than
// 100 check runs is unreadable, which is VISIBLE, rather than a count that
// quietly stops at a page.
const RunCheckLimit = 100

// RunIntervalFloor is --entry-interval's floor when any --run is given. Two
// REST calls per head every 5 seconds is 1,440 calls an hour per head, so four
// watched heads spent a whole REST pool.
const RunIntervalFloor = 30 * time.Second

// Runs is the --run source.
type Runs struct {
	Names   []string // <owner/repo>@<sha>
	Every_  time.Duration
	Timeout time.Duration
	Final   bool
	// Prev reads the stored value, so changed= on the WAKE SOURCE line counts
	// what MOVED rather than standing at zero for ever (the Fable read, F5): a
	// count nothing increments is a field that cannot go wrong, which is the
	// same thing as a field nobody can read.
	Prev func(key string) (string, bool)

	calls
	read, changed, unreadable int
}

func (r *Runs) Name() string         { return "runs" }
func (r *Runs) Every() time.Duration { return r.Every_ }

func (r *Runs) Counts() (read, changed, unreadable, calls int) {
	return r.read, r.changed, r.unreadable, r.Calls()
}

func (r *Runs) Poll(ctx context.Context, now time.Time) (Result, error) {
	type answer struct {
		i     int
		value string
		bad   bool
	}
	out := make([]answer, len(r.Names))
	sem := make(chan struct{}, ForgeBatch)
	done := make(chan answer, len(r.Names))
	for i, name := range r.Names {
		go func(i int, name string) {
			sem <- struct{}{}
			defer func() { <-sem }()
			value, err := r.one(ctx, name)
			if err != nil {
				done <- answer{i: i, value: Compose("unreadable", oneLineOf(err.Error())), bad: true}
				return
			}
			done <- answer{i: i, value: value}
		}(i, name)
	}
	for range r.Names {
		a := <-done
		out[a.i] = a
	}
	res := Result{}
	bad := 0
	for i, a := range out {
		r.read++
		if a.bad {
			bad++
			r.unreadable++
		}
		key := "run:" + r.Names[i]
		if r.Prev != nil {
			if old, had := r.Prev(key); !had || old != a.value {
				r.changed++
			}
		}
		res.Items = append(res.Items, Item{Kind: KindRun, Key: key, Value: a.value})
	}
	if len(r.Names) > 0 && bad == len(r.Names) {
		_, reason := Unreadable(out[0].value)
		return res, fmt.Errorf("every watched head is unreadable: %s", reason)
	}
	return res, nil
}

// one is the two REST calls per head and the bucket rule over what they answer.
func (r *Runs) one(ctx context.Context, name string) (string, error) {
	repo, sha, err := SplitHead(name)
	if err != nil {
		return "", err
	}
	owner, rname := ownerRepo(repo)
	rawRuns, err := gh(ctx, r.Timeout, &r.calls, "api",
		fmt.Sprintf("repos/%s/%s/commits/%s/check-runs?per_page=100", owner, rname, sha))
	if err != nil {
		return "", err
	}
	var runs struct {
		Total     int `json:"total_count"`
		CheckRuns []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	if uerr := json.Unmarshal(rawRuns, &runs); uerr != nil {
		return "", fmt.Errorf("gh answered something this tool cannot parse: %s", oneLineOf(uerr.Error()))
	}
	if runs.Total > RunCheckLimit {
		return "", fmt.Errorf("more than %d check runs on this head", RunCheckLimit)
	}
	rawStatus, err := gh(ctx, r.Timeout, &r.calls, "api",
		fmt.Sprintf("repos/%s/%s/commits/%s/status", owner, rname, sha))
	if err != nil {
		return "", err
	}
	var status struct {
		Statuses []struct {
			Context string `json:"context"`
			State   string `json:"state"`
		} `json:"statuses"`
	}
	if uerr := json.Unmarshal(rawStatus, &status); uerr != nil {
		return "", fmt.Errorf("gh answered something this tool cannot parse: %s", oneLineOf(uerr.Error()))
	}

	var fail, pending, pass int
	var failing []string
	count := func(name, b string) {
		if name == "" {
			name = "?"
		}
		switch b {
		case "pending":
			pending++
		case "pass":
			pass++
		default:
			fail++
			failing = append(failing, name)
		}
	}
	for _, c := range runs.CheckRuns {
		count(c.Name, bucket("CheckRun", c.Status, c.Conclusion, ""))
	}
	for _, s := range status.Statuses {
		count(s.Context, bucket("StatusContext", "", "", s.State))
	}
	sort.Strings(failing)
	return Compose(strconv.Itoa(fail), strconv.Itoa(pending), strconv.Itoa(pass),
		strings.Join(failing, ",")), nil
}

// IsFinalRun is --final-only's predicate for a head: the entry definition with
// one clause fewer. A head with NO checks at all is never final, for the reason
// an entry with none is not -- pending=0 pass=0 fail=0 is the state of checks
// that have not been created yet, and calling that green is the one arithmetic
// mistake here that would merge something red.
func IsFinalRun(value string) bool {
	p := Decompose(value)
	if len(p) == 2 && p[0] == "unreadable" {
		return false
	}
	p = fields(p, 4)
	fail, _ := strconv.Atoi(p[0])
	pending, _ := strconv.Atoi(p[1])
	pass, _ := strconv.Atoi(p[2])
	return pending == 0 && fail+pass > 0
}
