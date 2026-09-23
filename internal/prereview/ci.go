package prereview

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ciOK is the one required check. A red or absent one at the exact head is a
// bounce (nova-tools #2704): the score above pass_above would otherwise PASS,
// and a missing answer is neutral.
const ciOK = "ci-ok"

// CheckRun is one check run from the commit's rollup. HeadSHA is the commit
// the run judged; a run for any other sha is not this head's evidence. ID is
// the attempt order GitHub assigns: a queued rerun has a higher id and empty
// timestamps.
type CheckRun struct {
	ID          int64
	Name        string
	Status      string
	Conclusion  string
	HeadSHA     string
	StartedAt   string
	CompletedAt string
}

// ValidHeadSHA reports whether sha is a full commit id. The rollup is read at
// that exact id, so a short or dirty value is not a head.
func ValidHeadSHA(sha string) bool {
	if len(sha) != 40 {
		return false
	}
	for _, c := range sha {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// ParseCheckRollup reads one page of the commit check-runs document. total is
// the document's total_count, or the page length when the document omits it.
func ParseCheckRollup(raw []byte) (runs []CheckRun, total int, err error) {
	var wire struct {
		TotalCount int `json:"total_count"`
		CheckRuns  []struct {
			ID          int64   `json:"id"`
			Name        string  `json:"name"`
			Status      string  `json:"status"`
			Conclusion  *string `json:"conclusion"`
			HeadSHA     string  `json:"head_sha"`
			StartedAt   *string `json:"started_at"`
			CompletedAt *string `json:"completed_at"`
		} `json:"check_runs"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, 0, fmt.Errorf("prereview: check rollup is not JSON: %w", err)
	}
	runs = make([]CheckRun, 0, len(wire.CheckRuns))
	for _, w := range wire.CheckRuns {
		r := CheckRun{ID: w.ID, Name: w.Name, Status: w.Status, HeadSHA: w.HeadSHA}
		if w.Conclusion != nil {
			r.Conclusion = *w.Conclusion
		}
		if w.StartedAt != nil {
			r.StartedAt = *w.StartedAt
		}
		if w.CompletedAt != nil {
			r.CompletedAt = *w.CompletedAt
		}
		runs = append(runs, r)
	}
	total = wire.TotalCount
	if total == 0 {
		total = len(runs)
	}
	return runs, total, nil
}

// ciCheck reads the rollup at pr.Head. Success of ci-ok on that sha is yes.
// Red, skipped, still running, or no ci-ok on that sha is no: a missing answer
// is neutral under pass_above, and that is how #2519 at 907546af scored PASS
// while ci-ok was red. A run whose head_sha is not pr.Head is ignored, including
// a newer red ci-ok left behind on a sha the head has moved past.
func ciCheck(pr PR) Check {
	head := strings.TrimSpace(pr.Head)
	if !ValidHeadSHA(head) {
		return Check{Missing, "no commit sha, so the rollup cannot be read at the exact head"}
	}
	latest := latestAtHead(pr.Checks, head)
	reds := redJobNames(latest)
	ci, ok := latest[ciOK]
	if !ok {
		return Check{No, bounceMissing(head, reds)}
	}
	if ciGreen(ci) {
		return Check{Yes, fmt.Sprintf("PASS ci-ok success at %s", head)}
	}
	if !ciSettled(ci) {
		st := strings.ToLower(strings.TrimSpace(ci.Status))
		if st == "" {
			st = "pending"
		}
		return Check{No, withJobs(fmt.Sprintf("BOUNCE ci-ok still %s at %s", st, head), reds)}
	}
	if redConclusion(ci.Conclusion) {
		return Check{No, fmt.Sprintf("BOUNCE ci-ok red at %s; jobs: %s", head, strings.Join(reds, ", "))}
	}
	word := strings.ToLower(strings.TrimSpace(ci.Conclusion))
	if word == "" {
		word = "not success"
	}
	return Check{No, withJobs(fmt.Sprintf("BOUNCE ci-ok %s at %s", word, head), reds)}
}

func bounceMissing(head string, reds []string) string {
	return withJobs(fmt.Sprintf("BOUNCE ci-ok missing at %s", head), reds)
}

func withJobs(msg string, reds []string) string {
	if len(reds) == 0 {
		return msg
	}
	return msg + "; jobs: " + strings.Join(reds, ", ")
}

func latestAtHead(runs []CheckRun, head string) map[string]CheckRun {
	out := map[string]CheckRun{}
	for _, r := range runs {
		if strings.TrimSpace(r.HeadSHA) != head {
			continue
		}
		name := strings.TrimSpace(r.Name)
		if name == "" {
			continue
		}
		if prev, ok := out[name]; ok && !runAfter(r, prev) {
			continue
		}
		out[name] = r
	}
	return out
}

// runAfter reports whether a is a newer attempt than b. The check-run id is
// the attempt order. A queued rerun has empty started_at and completed_at, so
// ordering by those timestamps would keep the older success. A fixture that
// recorded no id still orders by timestamp.
func runAfter(a, b CheckRun) bool {
	if a.ID != b.ID {
		return a.ID > b.ID
	}
	return runWhen(a) > runWhen(b)
}

func runWhen(r CheckRun) string {
	if t := strings.TrimSpace(r.CompletedAt); t != "" {
		return t
	}
	return strings.TrimSpace(r.StartedAt)
}

func redJobNames(latest map[string]CheckRun) []string {
	names := make([]string, 0)
	for name, r := range latest {
		if redConclusion(r.Conclusion) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func ciGreen(r CheckRun) bool {
	switch strings.ToLower(strings.TrimSpace(r.Conclusion)) {
	case "success", "pass":
	default:
		return false
	}
	s := strings.ToLower(strings.TrimSpace(r.Status))
	return s == "" || s == "completed"
}

func ciSettled(r CheckRun) bool {
	s := strings.ToLower(strings.TrimSpace(r.Status))
	if s == "completed" {
		return true
	}
	return s == "" && strings.TrimSpace(r.Conclusion) != ""
}

// redConclusion is the failure family the merge gate counts as red, including
// a cancelled run: a run nobody read is not a green one.
func redConclusion(state string) bool {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "fail", "failure", "cancel", "cancelled", "canceled", "timed_out", "action_required", "startup_failure":
		return true
	default:
		return false
	}
}
