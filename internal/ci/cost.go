package ci

// cost.go is the machine behind `nova-ci cost` (card ci-cost-line, stream ci,
// source nova-tools#4328): every CI run writes ONE COST line to its log and
// to the ci:cost stream, from the forge's own job listing for the run --
// job-seconds per job, their total, and spin, the seconds of the failed,
// cancelled and rerun jobs. Spin is the number a reader chases: a job whose
// seconds bought no verdict (it went red, it was cut down, or it ran again).
//
// The line rides on the run receipt the runner already writes (#4375,
// bed06e65): it carries the receipt's own identity -- repo, sha, run id,
// event, workflow, conclusion, pr -- so a ci:cost row joins the
// ci:<repo>:<sha>:gh record and the ev:github row of the same run, and the
// verb takes the receipt's flags, spelt the same, so the ci-ok step that
// writes the receipt can write the cost from the same context.
//
// Everything above the write is PURE: ParseJobsPage (failed_forge.go, the one
// reader of the forge's job listing) turns the bytes into jobs, CostFromJobs
// turns jobs into seconds, and Line prints them. Nothing here reads a file,
// the clock or the network; the tests hand it a fixture listing. A job's
// seconds are its completed_at minus its started_at as the forge stamped
// them; a job the forge has no completed_at for (still running) has UNKNOWN
// seconds, printed "-" and counted in unknown=, never as zero.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/webhook"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// CostStream is the Redis stream every run's COST line is appended to: one
// entry per run, never trimmed (keys do not expire).
const CostStream = "ci:cost"

// The reasons a job's seconds are spin. A red job is named by its own
// conclusion; a job that ran in a later attempt of the run is a rerun; an
// earlier attempt of a job the listing also holds a later attempt of was
// superseded by it (the forge lists every attempt under filter=all).
const (
	SpinFailed     = "failed"
	SpinCancelled  = "cancelled"
	SpinRerun      = "rerun"
	SpinSuperseded = "superseded"
)

// CostJob is one job of the run priced: its seconds and whether they were spin.
type CostJob struct {
	Name       string
	Conclusion string
	Attempt    int
	Seconds    int64  // completed_at - started_at, whole seconds; meaningful only when Known
	Known      bool   // false when the forge gave no started_at or completed_at, or they are out of order
	Spin       string // one of the Spin* reasons, or "" when the seconds bought a verdict
}

// Cost is one run priced.
type Cost struct {
	Jobs    []CostJob
	Total   int64 // the seconds of every job whose seconds are known
	Spin    int64 // of them, the seconds of the failed, cancelled and rerun jobs
	Unknown int   // jobs whose seconds are unknown (no completed_at yet)
}

// CostFromJobs prices one run's jobs, in the listing's order. A job is spin
// when its conclusion is red (FailedJob.Failed: not success, skipped or
// neutral; cancelled named as such), when its run_attempt is above one (the
// CI-red reading's own count of spent reruns), or when the listing holds a
// later attempt of the same name.
func CostFromJobs(jobs []FailedJob) Cost {
	latest := map[string]int{}
	for _, j := range jobs {
		if j.Attempt > latest[j.Name] {
			latest[j.Name] = j.Attempt
		}
	}
	var c Cost
	for _, j := range jobs {
		cj := CostJob{Name: j.Name, Conclusion: strings.ToLower(strings.TrimSpace(j.Conclusion)), Attempt: j.Attempt}
		if !j.Started.IsZero() && !j.Completed.IsZero() && !j.Completed.Before(j.Started) {
			cj.Known = true
			cj.Seconds = int64(j.Completed.Sub(j.Started) / time.Second)
		}
		switch {
		case j.Cancelled():
			cj.Spin = SpinCancelled
		case j.Failed():
			cj.Spin = SpinFailed
		case j.Attempt > 1:
			cj.Spin = SpinRerun
		case j.Attempt < latest[j.Name]:
			cj.Spin = SpinSuperseded
		}
		if cj.Known {
			c.Total += cj.Seconds
			if cj.Spin != "" {
				c.Spin += cj.Seconds
			}
		} else {
			c.Unknown++
		}
		c.Jobs = append(c.Jobs, cj)
	}
	return c
}

// Line is the one COST line: the receipt's identity, the totals, then one
// job=<name>:<seconds>:<conclusion>:<attempt>:<spin|ok> field per job, and the
// ci:cost entry id the line was written as (ev=-, when it was only logged).
// The name goes through oneline.Field so a job named "test (hulk)" stays one
// token; unknown seconds print "-".
func (c Cost) Line(r webhook.Receipt, ev string) string {
	dash := func(s string) string {
		if s == "" {
			return "-"
		}
		return s
	}
	var b strings.Builder
	fmt.Fprintf(&b, "COST repo=%s sha=%s run=%s event=%s workflow=%s conclusion=%s pr=%s jobs=%d total=%d spin=%d unknown=%d",
		oneline.Field(r.Repo), dash(r.SHA), dash(r.RunID), dash(r.Event), oneline.Field(r.Workflow), dash(r.Conclusion), dash(r.PR),
		len(c.Jobs), c.Total, c.Spin, c.Unknown)
	for _, j := range c.Jobs {
		b.WriteString(" job=" + costJobValue(j))
	}
	b.WriteString(" ev=" + dash(ev))
	return b.String()
}

// costJobValue is one job as the line and the stream spell it:
// <name>:<seconds>:<conclusion>:<attempt>:<spin|ok>.
func costJobValue(j CostJob) string {
	secs := "-"
	if j.Known {
		secs = strconv.FormatInt(j.Seconds, 10)
	}
	why := "ok"
	if j.Spin != "" {
		why = j.Spin
	}
	return fmt.Sprintf("%s:%s:%s:%d:%s", oneline.Field(j.Name), secs, oneline.Field(dashIfEmpty(j.Conclusion)), j.Attempt, why)
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// CostFields is the ci:cost entry: the receipt's identity, the totals and
// one job:<name>:<attempt> field per job, in the listing's order. It is a
// flat list so XADD writes it as given; a reader joins it to
// ci:<repo>:<sha>:gh by repo, sha and run_id.
func CostFields(r webhook.Receipt, c Cost) []string {
	fields := []string{
		"repo", r.Repo, "sha", r.SHA, "run_id", r.RunID, "event", r.Event, "workflow", r.Workflow,
		"conclusion", r.Conclusion, "pr", r.PR, "head_branch", r.HeadBranch, "base_branch", r.BaseBranch, "at", r.At,
		"jobs", strconv.Itoa(len(c.Jobs)), "total", strconv.FormatInt(c.Total, 10),
		"spin", strconv.FormatInt(c.Spin, 10), "unknown", strconv.Itoa(c.Unknown),
	}
	for _, j := range c.Jobs {
		fields = append(fields, "job:"+j.Name+":"+strconv.Itoa(j.Attempt), costJobValue(j))
	}
	return fields
}

// CostWriter is the one Redis call the write needs; *redis.Client is one, and
// the verb's tests hand in a fake that records the entry.
type CostWriter interface {
	XAdd(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd
}

// WriteCost validates the receipt (the same Validate the runner's receipt
// goes through, so a cost never names a run the receipt could not) and
// appends one entry to ci:cost. It returns the entry id. It touches no other
// key; the bench seat's ACL holds ~ci:* and +xadd.
func WriteCost(ctx context.Context, w CostWriter, r *webhook.Receipt, c Cost) (string, error) {
	if w == nil {
		return "", errors.New("nova-ci cost: no redis client")
	}
	if err := r.Validate(); err != nil {
		return "", err
	}
	id, err := w.XAdd(ctx, &redis.XAddArgs{Stream: CostStream, Values: CostFields(*r, c)}).Result()
	if err != nil {
		return "", fmt.Errorf("XADD %s: %w", CostStream, err)
	}
	return id, nil
}
