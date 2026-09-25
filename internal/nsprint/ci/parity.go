package ci

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// DefaultLookback is how far before the sprint's first ci cut the Actions
// side is read: a workflow_run for a cut head can complete just before its
// card is cut.
const DefaultLookback = time.Hour

// ExitParity is the parity verb's exit when a head Actions passed is not OK
// on its key, or the sample is under the minimum.
const ExitParity = 1

// ParityRequest measures Redis-verdict parity with Actions over one sprint
// (#2756 10.8.1, nova-tools #3041).
type ParityRequest struct {
	Sprint   string
	Min      int           // heads Actions passed needed for the gate; 0 for none
	Lookback time.Duration // default DefaultLookback
}

// ParityHead is one head Actions passed, with its key's verdict.
type ParityHead struct {
	Repo    string
	PR      int
	Head    string
	Verdict string // the ci:<repo>:<head>:<gid> verdict, MISSING when absent
}

// Parity is the measurement: every head of a sprint PR that Actions passed,
// and the ones whose key is not OK.
type Parity struct {
	Passed []ParityHead
	Fail   []ParityHead
	Min    int
}

// actionsPassing are the workflow_run conclusions that do not fail a head.
var actionsPassing = map[string]bool{"success": true, "skipped": true, "neutral": true}

// ReadParity reads the sprint's ci cards, the completed workflow_run entries
// on ev:github for the sprint's PRs, and the key of every head Actions
// passed. A head Actions passed is one where the latest completed run of
// every workflow concluded success, skipped or neutral and at least one
// concluded success. The PRs are the ones the sprint cut a ci card for, so a
// new head of a sprint PR that was never cut is counted, MISSING.
func ReadParity(ctx context.Context, st *store.Store, req ParityRequest) (Parity, error) {
	if !sprintRx.MatchString(req.Sprint) {
		return Parity{}, fmt.Errorf("sprint %q is not [a-z0-9-]{1,40}", req.Sprint)
	}
	if req.Lookback <= 0 {
		req.Lookback = DefaultLookback
	}
	p := Parity{Min: req.Min}
	cards, err := Cards(ctx, st, req.Sprint)
	if err != nil {
		return p, err
	}
	prs := map[string]bool{} // "<repo>#<pr>"
	var firstCut int64
	for _, c := range cards {
		n, ok := labelPR(c.Label)
		if !ok || c.Repo == "" {
			continue
		}
		prs[c.Repo+"#"+strconv.Itoa(n)] = true
		if c.CutAt > 0 && (firstCut == 0 || c.CutAt < firstCut) {
			firstCut = c.CutAt
		}
	}
	if len(prs) == 0 {
		return p, nil
	}
	start := "-"
	if from := firstCut - req.Lookback.Milliseconds(); firstCut > 0 && from > 0 {
		start = strconv.FormatInt(from, 10)
	}
	runs, err := completedRuns(ctx, st, start, prs)
	if err != nil {
		return p, err
	}
	keys := make([]string, 0, len(runs))
	for k, h := range runs {
		if h.passed() {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		h := runs[k]
		fields, err := civerdict.ReadHead(ctx, st.Client(), h.repo, h.head)
		if err != nil {
			return p, err
		}
		v := fields["verdict"]
		if v == "" {
			v = Missing
		}
		ph := ParityHead{Repo: h.repo, PR: h.pr, Head: h.head, Verdict: v}
		p.Passed = append(p.Passed, ph)
		if v != OK {
			p.Fail = append(p.Fail, ph)
		}
	}
	return p, nil
}

// actionsHead is one head's completed runs: the latest conclusion per workflow.
type actionsHead struct {
	repo, head string
	pr         int
	latest     map[string]string
}

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

// completedRuns pages ev:github from start, keeping completed workflow_run
// entries for the sprint's PRs, keyed "<repo> <head>". The stream is in
// arrival order, so a later run of a workflow replaces an earlier one.
func completedRuns(ctx context.Context, st *store.Store, start string, prs map[string]bool) (map[string]*actionsHead, error) {
	client := st.Client()
	out := map[string]*actionsHead{}
	for {
		msgs, err := client.XRangeN(ctx, ghevent.Stream, start, "+", 1000).Result()
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
			if err != nil || !prs[repo+"#"+strconv.Itoa(n)] || !shaRx.MatchString(head) {
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
		if len(msgs) < 1000 {
			return out, nil
		}
		start = "(" + msgs[len(msgs)-1].ID
	}
}

// labelPR reads the PR number out of ci-<pr>-<head8>-<base8>.
func labelPR(label string) (int, bool) {
	rest, ok := strings.CutPrefix(label, "ci-")
	if !ok {
		return 0, false
	}
	i := strings.Index(rest, "-")
	if i <= 0 {
		return 0, false
	}
	n, err := strconv.Atoi(rest[:i])
	return n, err == nil && n > 0
}

// WriteParity prints one `PARITY FAIL <head> repo= pr= key=` line per head
// Actions passed whose key is not OK, then `PARITY n/m`, and returns 0 at
// parity over at least Min heads, 1 otherwise.
func WriteParity(w io.Writer, p Parity) int {
	for _, h := range p.Fail {
		fmt.Fprintf(w, "PARITY FAIL %s repo=%s pr=%d key=%s\n", h.Head, h.Repo, h.PR, h.Verdict)
	}
	n, m := len(p.Passed)-len(p.Fail), len(p.Passed)
	line := fmt.Sprintf("PARITY %d/%d", n, m)
	code := ExitOK
	if len(p.Fail) > 0 {
		code = ExitParity
	}
	if m < p.Min {
		line += fmt.Sprintf(" short: %d < %d", m, p.Min)
		code = ExitParity
	}
	fmt.Fprintln(w, line)
	return code
}
