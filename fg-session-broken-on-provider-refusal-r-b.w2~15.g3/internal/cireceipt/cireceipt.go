// Package cireceipt is the run receipt the ci-ok job of
// .github/workflows/ci.yml writes at the end of every run: one ev:github row
// of the workflow_run shape, sender "runner", appended through
// internal/ghevent. The signed webhook receiver sits behind a tailscale funnel
// kept off by design, so the runner is the event source: our runners are
// self-hosted on the tailnet and run as the bench seat, and the run reports
// itself from inside, with no GitHub call.
//
// The row's reader is ghevent.Reader (internal/ghevent), which blocks on ev:github for the pull requests it names and reads repo,
// number, kind, action, head, sender and at. The fields are exactly the ones
// `nova-sprint ci github --from-runner` wrote, so that reader is unchanged. The ci:<repo>:<sha>:gh fold and
// the pr:<repo>:<n> claim that verb also wrote are not written here: their
// only readers were nova-sprint's own (land pr, read brief --pr, its Lua).
//
// A refused receipt names every refused field and what each wants in one
// error; a failed
// write is an error the verb turns into a red ci-ok, because a receipt that
// silently did not happen must never read as one that did. Repeat receipts
// from retries or reruns are acceptable wake hints for stream consumers.
package cireceipt

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/redis/go-redis/v9"
)

// Sender is the ev:github sender of a row the runner appended.
const Sender = "runner"

var shaRx = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Receipt is one run's result as the runner reports it.
type Receipt struct {
	Repo       string // owner/name
	SHA        string // the head the run tested: the PR head, else github.sha
	RunID      string // decimal github.run_id
	PR         string // the pull request number, "" when the event names none
	Workflow   string // github.workflow; runs of blanks become one dash
	Conclusion string // job.status of ci-ok: success, failure or cancelled
	At         string // RFC3339; "" means Now()
	// Now is the clock an empty At is stamped from; nil means time.Now.
	Now func() time.Time
}

// Validate normalizes r and names every field a receipt cannot be written
// from, each with what the field wants, in one error (joined with "; ") so a
// caller fixes the call once.
func (r *Receipt) Validate() error {
	var problems []string
	r.Repo = strings.TrimSpace(r.Repo)
	owner, name, ok := strings.Cut(r.Repo, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") || strings.ContainsAny(r.Repo, " :\t\r\n") {
		problems = append(problems, fmt.Sprintf("--repo wants owner/name, got %q", r.Repo))
	}
	r.SHA = strings.ToLower(strings.TrimSpace(r.SHA))
	if !shaRx.MatchString(r.SHA) {
		problems = append(problems, fmt.Sprintf("--sha wants the 40-hex head the run tested, got %q", r.SHA))
	}
	r.RunID = strings.TrimSpace(r.RunID)
	if !decimal(r.RunID) {
		problems = append(problems, fmt.Sprintf("--run-id wants the decimal github.run_id, got %q", r.RunID))
	}
	r.Workflow = strings.Join(strings.Fields(r.Workflow), "-")
	if r.Workflow == "" {
		problems = append(problems, "--workflow wants the workflow's name (github.workflow)")
	}
	r.Conclusion = strings.TrimSpace(r.Conclusion)
	switch r.Conclusion {
	case "success", "failure", "cancelled":
	default:
		problems = append(problems, fmt.Sprintf("--conclusion wants success, failure or cancelled (job.status), got %q", r.Conclusion))
	}
	r.PR = strings.TrimSpace(r.PR)
	if r.PR != "" && !decimal(r.PR) {
		problems = append(problems, fmt.Sprintf("--pr wants the pull request number or nothing, got %q", r.PR))
	}
	r.At = strings.TrimSpace(r.At)
	if r.At == "" {
		now := r.Now
		if now == nil {
			now = time.Now
		}
		r.At = now().UTC().Format(time.RFC3339)
	}
	if _, err := time.Parse(time.RFC3339, r.At); err != nil {
		problems = append(problems, fmt.Sprintf("--at wants RFC3339, got %q", r.At))
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func decimal(s string) bool {
	if s == "" {
		return false
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

// Entry is the ev:github row of a validated receipt.
func (r Receipt) Entry() ghevent.Entry {
	return ghevent.Entry{
		Repo: r.Repo, Kind: "workflow_run", Number: r.PR, Head: r.SHA, Action: "completed", At: r.At,
		Sender: Sender, RunID: r.RunID, Workflow: r.Workflow, Status: "completed", Conclusion: r.Conclusion,
	}
}

// Write validates r and appends its row in one XADD, returning the entry id.
// It needs a seat with XADD on ev:github and writes no other key.
func Write(ctx context.Context, rdb *redis.Client, r Receipt) (string, error) {
	if rdb == nil {
		return "", errors.New("no redis client")
	}
	if err := r.Validate(); err != nil {
		return "", err
	}
	id, err := ghevent.Publish(ctx, rdb, r.Entry())
	if err != nil {
		return "", fmt.Errorf("XADD %s: %w", ghevent.Stream, err)
	}
	return id, nil
}

// Line is the verb's one line for a written receipt.
func Line(r Receipt, id string) string {
	pr := r.PR
	if pr == "" {
		pr = "-"
	}
	return fmt.Sprintf("CI RECEIPT %s sha=%s run=%s workflow=%s conclusion=%s pr=%s ev=%s",
		r.Repo, r.SHA, r.RunID, r.Workflow, r.Conclusion, pr, id)
}
