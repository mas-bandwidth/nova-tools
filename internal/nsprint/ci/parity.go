package ci

// parity.go is `ci parity` (nova-tools #3041, #2756 10.8.1): Redis-verdict
// parity with Actions over one sprint, the measurement that lets the Linux and
// darwin rows leave .github/workflows. It reads Redis alone: the sprint's
// window from s:<S> (opened_at, closed_at), the Actions side from the
// completed workflow_run entries on ev:github (the webhook receiver's stream,
// #2657), and our side from the request record ci:<repo>:<sha> (run.go, the
// word the lander waits on). THE BOUNDARY: nothing here polls GitHub; the one
// budgeted GitHub read is compare.go and parity never calls it
// (TestParityNeverPollsGitHub holds the line).

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// ExitParity is the parity verb's exit when a head Actions passed is not OK
// on its record, or the sample is under the minimum.
const ExitParity = 1

// parityPage is the XRANGE page over ev:github.
const parityPage = 1000

// ParityRequest measures one sprint. Min is the heads Actions passed the gate
// needs (the verb's default is 20); 0 for none.
type ParityRequest struct {
	Sprint string
	Min    int
}

// ParityHead is one head Actions passed, with our record's word: OK (ci
// green), FAIL (red), PENDING, or MISSING when ci:<repo>:<sha> is absent.
type ParityHead struct {
	Repo string
	PR   int
	Head string
	Key  string
}

// Parity is the measurement: the sprint's window, every head Actions passed
// in it, and the ones whose record is not OK.
type Parity struct {
	Sprint   string
	From, To int64 // the window in ms; To 0 while the sprint is open
	Passed   []ParityHead
	Fail     []ParityHead
	Min      int
}

// actionsPassing are the workflow_run conclusions that do not fail a head.
var actionsPassing = map[string]bool{"success": true, "skipped": true, "neutral": true}

// ReadParity reads the window of s:<S>, pages the completed workflow_run
// entries on ev:github in that window, and reads the record of every head
// they name in one pipeline. A head Actions passed is one where the latest
// completed run of every workflow concluded success, skipped or neutral and at
// least one concluded success. A PR is in the sprint when our CI holds a
// record for any of its heads in the window, so a later head of that PR that
// was never requested counts, MISSING, and a PR our CI never saw does not.
func ReadParity(ctx context.Context, st *store.Store, req ParityRequest) (Parity, error) {
	if !sprintRx.MatchString(req.Sprint) {
		return Parity{}, fmt.Errorf("sprint %q is not [a-z0-9-]{1,40}", req.Sprint)
	}
	p := Parity{Sprint: req.Sprint, Min: req.Min}
	win, err := st.Client().HMGet(ctx, "s:"+req.Sprint, "opened_at", "closed_at").Result()
	if err != nil {
		return p, fmt.Errorf("HMGET s:%s: %w", req.Sprint, err)
	}
	p.From, _ = strconv.ParseInt(str(win[0]), 10, 64)
	p.To, _ = strconv.ParseInt(str(win[1]), 10, 64)
	if p.From <= 0 {
		return p, fmt.Errorf("sprint %s has no opened_at on s:%s; nova-sprint sprint open writes it", req.Sprint, req.Sprint)
	}
	runs, err := completedRuns(ctx, st, p.From, p.To)
	if err != nil {
		return p, err
	}
	keys := make([]string, 0, len(runs))
	for k := range runs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	reads := make([]store.HashRead, len(keys))
	for i, k := range keys {
		reads[i] = store.HashRead{Key: RecordKey(runs[k].repo, runs[k].head), Fields: []string{"ci"}}
	}
	vals, err := st.PipelineHMGet(ctx, reads)
	if err != nil {
		return p, err
	}
	requested := map[string]bool{} // "<repo>#<pr>"
	for i, k := range keys {
		h := runs[k]
		h.key = recordWord(vals[i][0])
		if h.key != Missing {
			requested[h.pull()] = true
		}
	}
	for _, k := range keys {
		h := runs[k]
		if !h.passed() || !requested[h.pull()] {
			continue
		}
		ph := ParityHead{Repo: h.repo, PR: h.pr, Head: h.head, Key: h.key}
		p.Passed = append(p.Passed, ph)
		if h.key != OK {
			p.Fail = append(p.Fail, ph)
		}
	}
	return p, nil
}

// recordWord maps the record's ci field to the verdict words: an absent
// record (or one with no ci field) is MISSING.
func recordWord(v any) string {
	switch str(v) {
	case SummaryGreen:
		return OK
	case SummaryRed:
		return Fail
	case SummaryPending:
		return Pending
	}
	return Missing
}

// actionsHead is one head's completed runs: the latest conclusion per
// workflow, and our record's word once read.
type actionsHead struct {
	repo, head string
	pr         int
	latest     map[string]string
	key        string
}

func (h *actionsHead) pull() string { return h.repo + "#" + strconv.Itoa(h.pr) }

func (h *actionsHead) passed() bool {
	success := false
	for _, c := range h.latest {
		if !actionsPassing[c] {
			return false
		}
		if c == "success" {
			success = true
		}
	}
	return success
}

// completedRuns pages ev:github over [from, to] (to 0: to the end), keeping
// the completed workflow_run entries that name a pull request, keyed
// "<repo> <head>". The stream is in arrival order, so a later run of a
// workflow replaces an earlier one.
func completedRuns(ctx context.Context, st *store.Store, from, to int64) (map[string]*actionsHead, error) {
	client := st.Client()
	start, end := strconv.FormatInt(from, 10), "+"
	if to > 0 {
		end = strconv.FormatInt(to, 10)
	}
	out := map[string]*actionsHead{}
	for {
		msgs, err := client.XRangeN(ctx, ghevent.Stream, start, end, parityPage).Result()
		if err != nil {
			return nil, fmt.Errorf("XRANGE %s: %w", ghevent.Stream, err)
		}
		for _, m := range msgs {
			v := func(f string) string { s, _ := m.Values[f].(string); return strings.TrimSpace(s) }
			if v("kind") != "workflow_run" || v("action") != "completed" {
				continue
			}
			repo := v("repo")
			if i := strings.LastIndex(repo, "/"); i >= 0 {
				repo = repo[i+1:]
			}
			head, workflow := v("head"), v("workflow")
			n, err := strconv.Atoi(v("number"))
			if err != nil || n <= 0 || !repoRx.MatchString(repo) || !shaRx.MatchString(head) {
				continue
			}
			k := repo + " " + head
			h := out[k]
			if h == nil {
				h = &actionsHead{repo: repo, head: head, pr: n, latest: map[string]string{}}
				out[k] = h
			}
			h.latest[workflow] = v("conclusion")
		}
		if len(msgs) < parityPage {
			return out, nil
		}
		start = "(" + msgs[len(msgs)-1].ID
	}
}

// parityRemedy names the verb that moves one miss toward parity.
func parityRemedy(h ParityHead) string {
	switch h.Key {
	case Missing:
		return fmt.Sprintf("nova-sprint ci request --repo %s --sha %s --pr %d", h.Repo, h.Head, h.PR)
	case Pending:
		return "nova-sprint ci run --bench <b>"
	}
	return fmt.Sprintf("nova-sprint ci status --repo %s --sha %s", h.Repo, h.Head)
}

// WriteParity prints one `PARITY FAIL <head> repo= pr= key= remedy:` line per
// head Actions passed whose record is not OK, then the receipt
// `PARITY n/m sprint=<S>`, and returns 0 at parity over at least Min heads,
// ExitParity otherwise.
func WriteParity(w io.Writer, p Parity) int {
	for _, h := range p.Fail {
		fmt.Fprintf(w, "PARITY FAIL %s repo=%s pr=%d key=%s remedy: %s\n", h.Head, h.Repo, h.PR, h.Key, parityRemedy(h))
	}
	n, m := len(p.Passed)-len(p.Fail), len(p.Passed)
	line := fmt.Sprintf("PARITY %d/%d sprint=%s", n, m, p.Sprint)
	code := ExitOK
	if len(p.Fail) > 0 {
		code = ExitParity
	}
	if m < p.Min {
		line += fmt.Sprintf(" short: %d < %d remedy: measure again once Actions has passed %d heads of the sprint", m, p.Min, p.Min)
		code = ExitParity
	}
	fmt.Fprintln(w, line)
	return code
}
