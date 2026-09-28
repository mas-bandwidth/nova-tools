// Package cicost is the machine behind `nova-ci cost`: every CI run writes ONE
// COST line to its log and to the ci:cost stream, from the forge's own job
// listing for the run. The line carries job-seconds per job, their total, and
// spin, the seconds of the failed, cancelled and rerun jobs. Spin is the number
// a reader chases: a job whose seconds bought no verdict, because it went red,
// it was cut down, or it ran again.
//
// The line rides on the run receipt (internal/cireceipt): it carries the
// receipt's identity, repo, sha, run id, workflow, conclusion and pr, so a
// ci:cost entry joins the ev:github row of the same run by repo, sha and
// run_id, and the verb takes the receipt's flags, spelt the same, so the ci-ok
// step that writes the receipt writes the cost from the same context.
//
// Everything above the write is PURE. ParseJobs turns the bytes of the
// listing (the body of `repos/<owner>/<name>/actions/runs/<id>/jobs`) into
// jobs, FromJobs turns jobs into seconds, and Line prints them. Nothing here
// reads a file, the clock or the network; the tests hand it a fixture. A job's
// seconds are its completed_at minus its started_at as the forge stamped them;
// a job the forge has no completed_at for (still running) has UNKNOWN seconds,
// printed "-" and counted in unknown=, never as zero. Everything read from the
// listing is DATA from a host: it is priced, never followed.
package cicost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cireceipt"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// Stream is the Redis stream every run's COST line is appended to: one entry
// per run, never trimmed.
const Stream = "ci:cost"

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

// Job is one job of the run as the forge lists it: what pricing needs and
// nothing more.
type Job struct {
	Name       string
	Conclusion string    // success, failure, cancelled, timed_out, skipped, ...
	Attempt    int       // the forge's run_attempt; 1 is the first, 0 is absent
	Started    time.Time // started_at; zero when the forge gave none
	Completed  time.Time // completed_at; zero while the job runs, so the seconds are unknown
}

// Failed is true for a job whose conclusion is red: not success, skipped or
// neutral, and not still empty.
func (j Job) Failed() bool {
	switch strings.ToLower(strings.TrimSpace(j.Conclusion)) {
	case "", "success", "skipped", "neutral":
		return false
	}
	return true
}

// Cancelled is true for a job the run cut down rather than one that went red
// of its own. The forge spells it both ways, and the two are the same job.
func (j Job) Cancelled() bool {
	switch strings.ToLower(strings.TrimSpace(j.Conclusion)) {
	case "cancelled", "canceled":
		return true
	}
	return false
}

// ParseJobs reads the forge's job listing for a run: one page, or every
// page's jobs gathered under one `jobs` array. It returns the forge's own
// total_count and the jobs in the listing's order. Bytes that are not the
// listing's JSON are an error, never an empty run.
func ParseJobs(raw []byte) (total int, jobs []Job, err error) {
	var body struct {
		Total int `json:"total_count"`
		Jobs  []struct {
			Name        string `json:"name"`
			Conclusion  string `json:"conclusion"`
			Attempt     int    `json:"run_attempt"`
			StartedAt   string `json:"started_at"`
			CompletedAt string `json:"completed_at"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return 0, nil, err
	}
	for _, j := range body.Jobs {
		jobs = append(jobs, Job{
			Name: j.Name, Conclusion: j.Conclusion, Attempt: j.Attempt,
			Started: parseForgeTime(j.StartedAt), Completed: parseForgeTime(j.CompletedAt),
		})
	}
	return body.Total, jobs, nil
}

// parseForgeTime reads one RFC 3339 stamp, and returns the zero time for
// anything else: a missing stamp means the seconds are unknown, never zero.
func parseForgeTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}
	}
	return t
}

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

// FromJobs prices one run's jobs, in the listing's order. A job is spin when
// its conclusion is red (cancelled named as such), when its run_attempt is
// above one, or when the listing holds a later attempt of the same name.
func FromJobs(jobs []Job) Cost {
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
// Every value goes through oneline.Field, so a job named "test (linux)" stays
// one token; unknown seconds print "-".
func (c Cost) Line(r cireceipt.Receipt, ev string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "COST repo=%s sha=%s run=%s workflow=%s conclusion=%s pr=%s jobs=%d total=%d spin=%d unknown=%d",
		dash(r.Repo), dash(r.SHA), dash(r.RunID), dash(r.Workflow), dash(r.Conclusion), dash(r.PR),
		len(c.Jobs), c.Total, c.Spin, c.Unknown)
	for _, j := range c.Jobs {
		b.WriteString(" job=" + jobValue(j))
	}
	b.WriteString(" ev=" + dash(ev))
	return b.String()
}

// jobValue is one job as the line and the stream spell it:
// <name>:<seconds>:<conclusion>:<attempt>:<spin|ok>.
func jobValue(j CostJob) string {
	secs := "-"
	if j.Known {
		secs = strconv.FormatInt(j.Seconds, 10)
	}
	why := "ok"
	if j.Spin != "" {
		why = j.Spin
	}
	return fmt.Sprintf("%s:%s:%s:%d:%s", oneline.Field(j.Name), secs, oneline.Field(dash(j.Conclusion)), j.Attempt, why)
}

// dash is a value for a slot, with "-" for an empty one, escaped to one token.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return oneline.Field(s)
}

// Fields is the ci:cost entry: the receipt's identity, the totals and one
// job:<name>:<attempt> field per job, in the listing's order. It is a flat
// key, value list so XADD writes it as given; a reader joins it to the run's
// ev:github row by repo, sha and run_id.
func Fields(r cireceipt.Receipt, c Cost) []string {
	fields := []string{
		"repo", r.Repo, "sha", r.SHA, "run_id", r.RunID, "workflow", r.Workflow,
		"conclusion", r.Conclusion, "pr", r.PR, "at", r.At,
		"jobs", strconv.Itoa(len(c.Jobs)), "total", strconv.FormatInt(c.Total, 10),
		"spin", strconv.FormatInt(c.Spin, 10), "unknown", strconv.Itoa(c.Unknown),
	}
	for _, j := range c.Jobs {
		fields = append(fields, "job:"+j.Name+":"+strconv.Itoa(j.Attempt), jobValue(j))
	}
	return fields
}

// Writer is the one Redis call the write needs; *redis.Client is one, and the
// verb's tests hand in a fake that records the entry.
type Writer interface {
	XAdd(ctx context.Context, a *redis.XAddArgs) *redis.StringCmd
}

// Write validates the receipt (the same Validate the runner's receipt goes
// through, so a cost never names a run the receipt could not) and appends one
// entry to ci:cost, returning the entry id. It touches no other key: the
// bench seat needs XADD on ci:cost and nothing more.
func Write(ctx context.Context, w Writer, r *cireceipt.Receipt, c Cost) (string, error) {
	if w == nil {
		return "", errors.New("no redis client")
	}
	if err := r.Validate(); err != nil {
		return "", err
	}
	id, err := w.XAdd(ctx, &redis.XAddArgs{Stream: Stream, Values: Fields(*r, c)}).Result()
	if err != nil {
		return "", fmt.Errorf("XADD %s: %w", Stream, err)
	}
	return id, nil
}
