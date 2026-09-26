package ci

// compare.go is `ci compare` (#3597): the one budgeted REST read that puts
// our receipts beside the GitHub check conclusions for the same sha, so
// Actions can be retired once twenty heads agree. It is the only path in
// this package that talks to GitHub, it makes exactly one request, and the
// lander never calls it.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/gh"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/redis/go-redis/v9"
)

// CompareRequest is one parity read.
type CompareRequest struct {
	Repo    string
	SHA     string
	Owner   string // GitHub owner; empty: mas-bandwidth
	BaseURL string // empty: the client's root
	Token   string
	HTTP    *http.Client
	Redis   redis.Cmdable // counts the call under ci-compare when set
}

// Comparison is one check beside its GitHub conclusion.
type Comparison struct {
	Check   string
	Ours    string // green, red, pending
	Actions string // green, red, pending
	Via     string // the check_run name matched, or "overall"
	Agree   bool
}

// Compared is what `ci compare` prints and writes to the record.
type Compared struct {
	Repo, SHA string
	Ours      string // the record's ci word
	Actions   string // the overall Actions word for the sha
	Rows      []Comparison
	Agree     int
	Total     int
}

// AllAgree is true when every row agrees and ours is not pending.
func (p Compared) AllAgree() bool {
	return p.Total > 0 && p.Agree == p.Total && p.Ours != SummaryPending
}

type checkRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// FetchCheckRuns is the one REST call, through the one GitHub client
// (internal/gh, #4343): GET /repos/<owner>/<repo>/commits/<sha>/check-runs,
// first page of 100. It never follows a link header and never calls the
// reruns or the workflow-runs endpoints.
func FetchCheckRuns(ctx context.Context, req CompareRequest) ([]checkRun, error) {
	owner := req.Owner
	if owner == "" {
		owner = "mas-bandwidth"
	}
	c := &gh.Client{API: req.BaseURL, Token: req.Token, HTTP: req.HTTP, Verb: "ci compare", Redis: req.Redis}
	var v struct {
		CheckRuns []checkRun `json:"check_runs"`
	}
	_, err := c.Do(ctx, http.MethodGet, "/repos/"+owner+"/"+req.Repo+"/commits/"+req.SHA+"/check-runs?per_page=100", nil, &v)
	if err != nil {
		var herr *gh.HTTPError
		if errors.As(err, &herr) {
			return nil, fmt.Errorf("check-runs %s@%s: HTTP %d: %s", req.Repo, req.SHA[:8], herr.Status, herr.Body)
		}
		return nil, fmt.Errorf("check-runs %s@%s: %w", req.Repo, req.SHA[:8], err)
	}
	return v.CheckRuns, nil
}

// wordOf maps one check_run to our three words.
func wordOf(r checkRun) string {
	if r.Status != "completed" {
		return SummaryPending
	}
	switch r.Conclusion {
	case "success", "skipped", "neutral":
		return SummaryGreen
	case "failure", "timed_out", "cancelled", "action_required", "startup_failure":
		return SummaryRed
	}
	return SummaryPending
}

// overall folds every check_run: red if any is red, pending if any is
// pending, green otherwise; pending when there are none.
func overall(runs []checkRun) string {
	if len(runs) == 0 {
		return SummaryPending
	}
	word := SummaryGreen
	for _, r := range runs {
		switch wordOf(r) {
		case SummaryRed:
			return SummaryRed
		case SummaryPending:
			word = SummaryPending
		}
	}
	return word
}

// Compare reads the record and its receipts, fetches the check runs once and
// lines them up: a check_run with our check's name is compared by name,
// otherwise against the overall Actions word. ErrNoRecord when we have no
// request for the sha.
func Compare(ctx context.Context, st *store.Store, req CompareRequest) (Compared, error) {
	if !repoRx.MatchString(req.Repo) || !shaRx.MatchString(req.SHA) {
		return Compared{}, errors.New("compare needs a repo name and a full 40-hex sha")
	}
	rows, err := ReadRows(ctx, st, req.Repo, req.SHA)
	if err != nil {
		return Compared{}, err
	}
	if !rows.Found {
		return Compared{}, ErrNoRecord
	}
	runs, err := FetchCheckRuns(ctx, req)
	if err != nil {
		return Compared{}, err
	}
	byName := map[string]checkRun{}
	for _, r := range runs {
		byName[r.Name] = r
	}
	p := Compared{Repo: req.Repo, SHA: req.SHA, Ours: rows.Summary(), Actions: overall(runs)}
	for _, row := range rows.Rows {
		c := Comparison{Check: row.Check, Ours: row.State, Via: "overall", Actions: p.Actions}
		if r, ok := byName[row.Check]; ok {
			c.Via, c.Actions = r.Name, wordOf(r)
		}
		c.Agree = c.Ours == c.Actions
		if c.Agree {
			p.Agree++
		}
		p.Total++
		p.Rows = append(p.Rows, c)
	}
	word := "differ"
	if p.AllAgree() {
		word = "agree"
	}
	if err := st.Client().HSet(ctx, RecordKey(req.Repo, req.SHA), "parity", word, "parity_actions", p.Actions,
		"parity_at", time.Now().UTC().Format(time.RFC3339)).Err(); err != nil {
		return p, fmt.Errorf("HSET parity: %w", err)
	}
	return p, nil
}

// ErrNoRecord is Compare's answer for a sha nobody requested.
var ErrNoRecord = errors.New("no ci request record for that sha")

// WriteCompare prints one AGREE/DIFFER line per check and the PARITY line,
// and returns 0 when every check agrees, 1 otherwise (the remedy is on the
// line: read the named log, or run ci run when ours is pending).
func WriteCompare(w io.Writer, p Compared) int {
	for _, c := range p.Rows {
		word := "DIFFER"
		if c.Agree {
			word = "AGREE"
		}
		fmt.Fprintf(w, "%s %s ours=%s actions=%s via=%s\n", word, c.Check, c.Ours, c.Actions, c.Via)
	}
	fmt.Fprintf(w, "PARITY %s@%s agree=%d/%d ours=%s actions=%s\n", p.Repo, p.SHA[:8], p.Agree, p.Total, p.Ours, p.Actions)
	if p.AllAgree() {
		return ExitOK
	}
	if p.Ours == SummaryPending {
		fmt.Fprintf(w, "remedy: ours is pending; nova-sprint ci run --bench <b> first\n")
	} else {
		fmt.Fprintf(w, "remedy: read the log of each DIFFER check (nova-sprint ci status --repo %s --sha %s)\n", p.Repo, p.SHA)
	}
	return 1
}
