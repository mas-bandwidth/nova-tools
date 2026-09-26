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
//     is live;
//   - pr:<repo>:<n>'s head, base and stream when the run names a pull
//     request, with plain HSET: the bench seat may not EVAL land_stream.lua,
//     and the fields a new record needs are written the way that script
//     writes them.
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
	At         string // RFC3339; "" means now
	Jobs       []Job
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
		r.At = time.Now().UTC().Format(time.RFC3339)
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

// Stream is the work stream a PR belongs to, from its branches: the head
// branch stream/<s>, else the base branch stream/<s>, else the base branch.
func Stream(headBranch, baseBranch string) string {
	head, base := branch(headBranch), branch(baseBranch)
	if s, ok := strings.CutPrefix(head, "stream/"); ok && s != "" {
		return s
	}
	if s, ok := strings.CutPrefix(base, "stream/"); ok && s != "" {
		return s
	}
	return base
}

// Written is the receipt as it landed.
type Written struct {
	Key     string // ci:<repo>:<sha>:gh
	EntryID string // the ev:github row
	Word    string // gh after the write
	Fail    string // gh_fail after the write
	Runs    int    // fields written: the workflow and its jobs
	Applied int    // of them, how many the function applied (the rest were older than the hash held)
	PRKey   string // pr:<repo>:<n> refreshed, "" when the run names no PR
	Stream  string // the stream written on that record
}

// Line is the receipt's one line.
func (w Written) Line() string {
	dash := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	return fmt.Sprintf("CIGH RUNNER %s gh=%s fail=%s runs=%d applied=%d ev=%s pr=%s stream=%s",
		w.Key, dash(w.Word), dash(w.Fail), w.Runs, w.Applied, dash(w.EntryID), dash(w.PRKey), dash(w.Stream))
}

// Write lands one receipt: the ev:github row, then in one pipeline the
// function call per run, the source stamp and the PR record refresh. It
// needs the nova_sprint function library on the store and a seat with
// XADD on ev:github, FCALL, and HSET on ci:* and pr:*.
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

	var pk, oldHead string
	if r.PR != "" {
		pk = prkey.KeyText(r.Repo, r.PR)
		old, err := rdb.HMGet(ctx, pk, "head", "created_at").Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return w, fmt.Errorf("HMGET %s: %w", pk, err)
		}
		if len(old) > 0 {
			oldHead, _ = old[0].(string)
		}
		w.PRKey = pk
		w.Stream = Stream(r.HeadBranch, r.BaseBranch)
	}

	pipe := rdb.Pipeline()
	var calls []*redis.Cmd
	calls = append(calls, pipe.FCall(ctx, Function, []string{w.Key, ghevent.Stream},
		Group, id, "workflow_run", r.Workflow, r.RunID, "completed", r.Conclusion, r.At, r.PR))
	for _, j := range r.Jobs {
		calls = append(calls, pipe.FCall(ctx, Function, []string{w.Key, ghevent.Stream},
			Group, id, "check_run", j.Name, r.RunID, "completed", j.Result, r.At, r.PR))
	}
	pipe.HSet(ctx, w.Key, "source", SourceRunner, "run_id", r.RunID, "event", r.Event)
	if pk != "" {
		now := strconv.FormatInt(time.Now().UnixMilli(), 10)
		fields := []any{"head", r.SHA, "base", r.BaseBranch, "stream", w.Stream, "updated_at", now}
		if oldHead == "" {
			// a new record, the fields land_stream.lua's record op gives one
			fields = append(fields, "repo", r.Repo, "n", r.PR, "state", "open", "ci", "pending", "mergeable", "", "created_at", now)
			pipe.HSetNX(ctx, pk, "reads", "")
		} else if oldHead != r.SHA {
			// a new head: the record's own CI and mergeable were for the old one
			fields = append(fields, "ci", "pending", "mergeable", "")
		}
		pipe.HSet(ctx, pk, fields...)
	}
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
	return w, nil
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
