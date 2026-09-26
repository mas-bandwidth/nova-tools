package webhook

// The runner is the event source (card gh-ci-receipts, stream github).
// Measured 2026-09-26 12:38 PM ET: ev:github was empty (XLEN 0) and no
// ci:*:gh key existed, because the signed receiver sits behind a tailscale
// funnel that is kept off by design, so `land pr` could only print WAITING.
// Our CI runners are self-hosted on the tailnet and run as the bench seat, so
// the ci-ok job itself reports the run at its end, from inside the run: no
// funnel, no webhook, no poll. It writes exactly what the receiver path
// would have produced, so every reader (land pr, ci status, parity, the
// dev-red duty) keeps working unchanged:
//
//   - one ev:github row of the workflow_run shape (ghevent.Fields), sender
//     "runner", so the parity read sees the completed run;
//   - ci:<repo>:<sha>:gh through the same ns_ci_github function the consumer
//     calls (one writer, the same fold), one wf:<workflow> field for the run
//     and one check:<job> field per job of the run; the job's id is the run
//     id (the runner has no check-run ids without asking GitHub, and a rerun
//     keeps the id with a later time, which the function orders on);
//   - the source of the record (source=runner), so Source says which path
//     is live.
//
// The PR record (card pr-record-follows-github; measured 2026-09-26: #4377
// and #4388 kept a head two pushes old and #4371 read ci=pending under a
// green receipt, because ev:github carries no pull_request delivery while
// the funnel is off, so the runner is the one source that sees each head):
// a receipt of a pull_request run that was not cancelled is a claim of the
// PR's head through land.RecordPRHead (plain HSET/SADD/SREM, which the bench
// seat may do; it never touches an existing record's stream, which is the
// lander's, and orders runner claims by run id, so an older run's receipt
// never rewinds a head a newer run named). Then a final word (green or red)
// is folded onto every open record whose head is the receipt's sha, through
// the head index pr:<name>:head:<sha> (land.FoldCI), so ci on the record is
// the receipt, not a pending that never moves. A cancelled run writes
// neither: a newer push cancelled it. ns_ci_github orders a field by run id
// then time, so an older run's receipt is KEPT, never applied over a newer
// one.
//
// A refused receipt names the field and the remedy in one line; a failed
// write is an error the verb turns into a red ci-ok, because a landing must
// never wait on a receipt that silently did not happen.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/redis/go-redis/v9"
)

// SourceRunner, SourceHook and SourceNone are Source's three answers.
const (
	SourceRunner = "runner"
	SourceHook   = "hook"
	SourceNone   = "none"
)

// Sender is the ev:github sender of a row the runner appended.
const Sender = "runner"

// Job is one job of the run as ci-ok saw it: needs.<name>.result.
type Job struct {
	Name   string
	Result string // success, failure, cancelled or skipped
}

// ParseJob reads one --job value, name=result.
func ParseJob(v string) (Job, error) {
	name, result, ok := strings.Cut(strings.TrimSpace(v), "=")
	j := Job{Name: strings.TrimSpace(name), Result: strings.TrimSpace(result)}
	if !ok || j.Name == "" || strings.ContainsAny(j.Name, " \t:=") {
		return j, fmt.Errorf("--job wants <name>=<result>, got %q", v)
	}
	switch j.Result {
	case "success", "failure", "cancelled", "skipped":
		return j, nil
	}
	return j, fmt.Errorf("--job %s: result %q is not success, failure, cancelled or skipped", j.Name, j.Result)
}

// Receipt is one run's result as the runner reports it.
type Receipt struct {
	Repo       string // owner/name
	SHA        string // the head the run tested: the PR head, the group head or the pushed commit
	RunID      string // decimal
	Event      string // pull_request, merge_group, push or workflow_dispatch
	HeadBranch string // github.head_ref (a ref prefix is stripped)
	BaseBranch string // github.base_ref
	PR         string // the pull request number, "" when the event names none
	Workflow   string // github.workflow
	Conclusion string // job.status of ci-ok: success, failure or cancelled
	At         string // RFC3339; "" means Now()
	Jobs       []Job
	// Now is the clock an empty At is stamped from; nil means time.Now.
	Now func() time.Time
}

// Validate names the first field a receipt cannot be written from, with the
// remedy in the message.
func (r *Receipt) Validate() error {
	if _, _, err := prkey.Split(r.Repo); err != nil || !strings.Contains(r.Repo, "/") {
		return fmt.Errorf("--repo wants owner/name, got %q", r.Repo)
	}
	r.SHA = strings.ToLower(strings.TrimSpace(r.SHA))
	if !shaRx.MatchString(r.SHA) {
		return fmt.Errorf("--sha wants the 40-hex head the run tested, got %q", r.SHA)
	}
	r.RunID = strings.TrimSpace(r.RunID)
	if !decimal(r.RunID) {
		return fmt.Errorf("--run-id wants the decimal github.run_id, got %q", r.RunID)
	}
	r.Event = strings.TrimSpace(r.Event)
	switch r.Event {
	case "pull_request", "merge_group", "push", "workflow_dispatch":
	default:
		return fmt.Errorf("--event wants pull_request, merge_group, push or workflow_dispatch, got %q", r.Event)
	}
	r.Workflow = strings.Join(strings.Fields(r.Workflow), "-")
	if r.Workflow == "" {
		return errors.New("--workflow wants the workflow's name (github.workflow)")
	}
	r.Conclusion = strings.TrimSpace(r.Conclusion)
	switch r.Conclusion {
	case "success", "failure", "cancelled":
	default:
		return fmt.Errorf("--conclusion wants success, failure or cancelled (job.status), got %q", r.Conclusion)
	}
	r.PR = strings.TrimSpace(r.PR)
	if r.PR != "" && !decimal(r.PR) {
		return fmt.Errorf("--pr wants the pull request number or nothing, got %q", r.PR)
	}
	r.HeadBranch = branch(r.HeadBranch)
	r.BaseBranch = branch(r.BaseBranch)
	if r.At == "" {
		now := r.Now
		if now == nil {
			now = time.Now
		}
		r.At = now().UTC().Format(time.RFC3339)
	}
	if _, err := time.Parse(time.RFC3339, r.At); err != nil {
		return fmt.Errorf("--at wants RFC3339, got %q", r.At)
	}
	seen := map[string]bool{}
	for _, j := range r.Jobs {
		if _, err := ParseJob(j.Name + "=" + j.Result); err != nil {
			return err
		}
		if seen[j.Name] {
			return fmt.Errorf("--job %s given twice", j.Name)
		}
		seen[j.Name] = true
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

func branch(ref string) string {
	ref = strings.TrimSpace(ref)
	ref = strings.TrimPrefix(ref, "refs/heads/")
	return ref
}

// Written is the receipt as it landed.
type Written struct {
	Key     string // ci:<repo>:<sha>:gh
	EntryID string // the ev:github row
	Word    string // gh after the write
	Fail    string // gh_fail after the write
	Runs    int    // fields written: the workflow and its jobs
	Applied int    // of them, how many the function applied (the rest were older than the hash held)
	// PR is the claim on the PR record (Key "" when the run is not a
	// pull_request run or was cancelled); Folded the PR numbers whose record
	// took the word at this head.
	PR     land.PRHeadResult
	Folded []string
}

// FoldLine is the receipt's second line when the word reached a PR record.
func (w Written) FoldLine() string {
	if len(w.Folded) == 0 {
		return ""
	}
	return fmt.Sprintf("CIGH FOLD %s ci=%s prs=%s", w.Key, w.Word, strings.Join(w.Folded, ","))
}

// Line is the receipt's one line.
func (w Written) Line() string {
	dash := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	return fmt.Sprintf("CIGH RUNNER %s gh=%s fail=%s runs=%d applied=%d ev=%s",
		w.Key, dash(w.Word), dash(w.Fail), w.Runs, w.Applied, dash(w.EntryID))
}

// Write lands one receipt: the ev:github row, then in one pipeline the
// function call per run and the source stamp; then, unless the run was
// cancelled, the PR record's claim (a pull_request run) and the fold of a
// final word onto the records at the sha. It needs the nova_sprint function
// library on the store and a seat with XADD on ev:github, FCALL and HSET on
// ci:*, and HMGET, HSET, SADD, SREM and SMEMBERS on pr:*. It touches no
// other key.
func Write(ctx context.Context, rdb *redis.Client, r Receipt) (Written, error) {
	var w Written
	if rdb == nil {
		return w, errors.New("ci github --from-runner: no redis client")
	}
	if err := r.Validate(); err != nil {
		return w, err
	}
	w.Key = Key(r.Repo, r.SHA)
	w.Runs = 1 + len(r.Jobs)

	id, err := ghevent.Publish(ctx, rdb, ghevent.Entry{
		Repo: r.Repo, Kind: "workflow_run", Number: r.PR, Head: r.SHA, Action: "completed", At: r.At,
		Sender: Sender, RunID: r.RunID, Workflow: r.Workflow, Status: "completed", Conclusion: r.Conclusion,
	})
	if err != nil {
		return w, fmt.Errorf("XADD %s: %w", ghevent.Stream, err)
	}
	w.EntryID = id

	pipe := rdb.Pipeline()
	var calls []*redis.Cmd
	calls = append(calls, pipe.FCall(ctx, Function, []string{w.Key, ghevent.Stream},
		Group, id, "workflow_run", r.Workflow, r.RunID, "completed", r.Conclusion, r.At, r.PR))
	for _, j := range r.Jobs {
		calls = append(calls, pipe.FCall(ctx, Function, []string{w.Key, ghevent.Stream},
			Group, id, "check_run", j.Name, r.RunID, "completed", j.Result, r.At, r.PR))
	}
	pipe.HSet(ctx, w.Key, "source", SourceRunner, "run_id", r.RunID, "event", r.Event)
	if _, err := pipe.Exec(ctx); err != nil {
		return w, fmt.Errorf("write %s: %w", w.Key, err)
	}
	for _, c := range calls {
		reply, _ := c.Slice()
		if len(reply) > 0 && reply[0] == "APPLIED" {
			w.Applied++
		}
		if len(reply) >= 3 {
			w.Word, _ = reply[1].(string)
			w.Fail, _ = reply[2].(string)
		}
	}
	if r.Conclusion == "cancelled" {
		return w, nil
	}
	var extra []string
	if r.Event == "pull_request" && r.PR != "" {
		n, _ := strconv.Atoi(r.PR)
		claim := land.PRClaim{Repo: r.Repo, N: n, Head: r.SHA, Action: "run", Branch: r.HeadBranch, Base: r.BaseBranch,
			Source: land.ClaimRunner, EvID: id, RunID: r.RunID}
		if t, err := time.Parse(time.RFC3339, r.At); err == nil {
			claim.At = t.UnixMilli()
		}
		if w.PR, err = land.RecordPRHead(ctx, rdb, claim, r.Now); err != nil {
			return w, err
		}
		extra = []string{r.PR}
	}
	why := "gh " + w.Word + " run " + r.RunID
	if w.Fail != "" {
		why += " " + w.Fail
	}
	if w.Folded, err = land.FoldCI(ctx, rdb, r.Repo, r.SHA, w.Word, why, extra, r.Now); err != nil {
		return w, err
	}
	return w, nil
}

// SourceOf says which path appended an ev:github row from its sender: the
// runner names itself, the receiver carries the GitHub login, and no row is
// none. doctor's ingest line reads it off the newest row it already holds.
func SourceOf(sender string) string {
	switch strings.TrimSpace(sender) {
	case "":
		return SourceNone
	case Sender:
		return SourceRunner
	}
	return SourceHook
}

// Source says which path wrote a record: runner when the runner stamped it,
// hook when the consumer applied an ev:github entry the receiver appended,
// none when nothing is recorded at that head.
func Source(r Record) string {
	switch {
	case !r.Found:
		return SourceNone
	case r.Source == SourceRunner:
		return SourceRunner
	case r.EvID != "":
		return SourceHook
	}
	return SourceNone
}
