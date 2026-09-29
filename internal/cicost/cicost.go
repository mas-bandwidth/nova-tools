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
	"os"
	"sort"
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

// Entry is one run's record on the ci:cost stream.
type Entry struct {
	ID      string
	Receipt cireceipt.Receipt
	Cost    Cost
}

// Line returns the formatted COST line for this entry, ending with ev=<ID>.
func (e Entry) Line() string {
	return e.Cost.Line(e.Receipt, e.ID)
}

// unescapeField unescapes hex and unicode sequences produced by oneline.Field.
func unescapeField(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+3 < len(s) && s[i+1] == 'x' {
			if bVal, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
				b.WriteByte(byte(bVal))
				i += 4
				continue
			}
		} else if s[i] == '\\' && i+5 < len(s) && s[i+1] == 'u' {
			if rVal, err := strconv.ParseUint(s[i+2:i+6], 16, 32); err == nil {
				b.WriteRune(rune(rVal))
				i += 6
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// ParseJobValue parses one job as stored in the ci:cost stream:
// <name>:<seconds>:<conclusion>:<attempt>:<spin|ok>.
func ParseJobValue(s string) (CostJob, error) {
	parts := strings.Split(s, ":")
	if len(parts) < 5 {
		return CostJob{}, fmt.Errorf("malformed job value %q", s)
	}
	namePart := strings.Join(parts[:len(parts)-4], ":")
	name := unescapeField(namePart)
	secsPart := parts[len(parts)-4]
	conclPart := unescapeField(parts[len(parts)-3])
	attemptPart := parts[len(parts)-2]
	spinPart := parts[len(parts)-1]

	attempt, err := strconv.Atoi(attemptPart)
	if err != nil {
		return CostJob{}, fmt.Errorf("malformed attempt in job value %q: %w", s, err)
	}
	cj := CostJob{
		Name:       name,
		Conclusion: strings.ToLower(strings.TrimSpace(conclPart)),
		Attempt:    attempt,
	}
	if secsPart != "-" {
		secs, err := strconv.ParseInt(secsPart, 10, 64)
		if err != nil {
			return CostJob{}, fmt.Errorf("malformed seconds in job value %q: %w", s, err)
		}
		cj.Known = true
		cj.Seconds = secs
	}
	if spinPart != "ok" && spinPart != "" {
		cj.Spin = spinPart
	}
	return cj, nil
}

// ParseEntry reconstructs an Entry from a stream entry ID and field-value map.
func ParseEntry(id string, values map[string]string) (Entry, error) {
	r := cireceipt.Receipt{
		Repo:       values["repo"],
		SHA:        values["sha"],
		RunID:      values["run_id"],
		Workflow:   values["workflow"],
		Conclusion: values["conclusion"],
		PR:         values["pr"],
		At:         values["at"],
	}
	total, _ := strconv.ParseInt(values["total"], 10, 64)
	spin, _ := strconv.ParseInt(values["spin"], 10, 64)
	unknown, _ := strconv.Atoi(values["unknown"])

	var jobs []CostJob
	for k, v := range values {
		if strings.HasPrefix(k, "job:") {
			cj, err := ParseJobValue(v)
			if err != nil {
				return Entry{}, err
			}
			jobs = append(jobs, cj)
		}
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].Name == jobs[j].Name {
			return jobs[i].Attempt < jobs[j].Attempt
		}
		return jobs[i].Name < jobs[j].Name
	})

	return Entry{
		ID:      id,
		Receipt: r,
		Cost: Cost{
			Jobs:    jobs,
			Total:   total,
			Spin:    spin,
			Unknown: unknown,
		},
	}, nil
}

// ParseValues reconstructs an Entry from a stream entry ID and map[string]any.
func ParseValues(id string, values map[string]any) (Entry, error) {
	m := make(map[string]string, len(values))
	for k, v := range values {
		m[k] = fmt.Sprint(v)
	}
	return ParseEntry(id, m)
}

// Reader is the Redis interface needed to read entries from ci:cost stream.
type Reader interface {
	XRevRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd
}

// ReadRecent reads up to count entries from ci:cost stream, newest first.
func ReadRecent(ctx context.Context, r Reader, count int) ([]Entry, error) {
	if r == nil {
		return nil, errors.New("no redis reader")
	}
	if count <= 0 {
		count = 50
	}
	msgs, err := r.XRevRangeN(ctx, Stream, "+", "-", int64(count)).Result()
	if err != nil {
		return nil, fmt.Errorf("XREVRANGE %s: %w", Stream, err)
	}
	var entries []Entry
	for _, m := range msgs {
		e, err := ParseValues(m.ID, m.Values)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// ReadHead scans recent ci:cost entries for a given repo and commit SHA.
func ReadHead(ctx context.Context, r Reader, repo, sha string) (*Entry, error) {
	entries, err := ReadRecent(ctx, r, 200)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if (repo == "" || e.Receipt.Repo == repo) && strings.HasPrefix(e.Receipt.SHA, sha) {
			return &e, nil
		}
	}
	return nil, nil
}

// ReadPR scans recent ci:cost entries for a given repo and PR number.
func ReadPR(ctx context.Context, r Reader, repo string, pr int) (*Entry, error) {
	prStr := strconv.Itoa(pr)
	entries, err := ReadRecent(ctx, r, 200)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if (repo == "" || e.Receipt.Repo == repo) && (e.Receipt.PR == prStr || e.Receipt.PR == "#"+prStr) {
			return &e, nil
		}
	}
	return nil, nil
}

// ReadRun scans recent ci:cost entries for a given repo and run ID.
func ReadRun(ctx context.Context, r Reader, repo, runID string) (*Entry, error) {
	entries, err := ReadRecent(ctx, r, 200)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if (repo == "" || e.Receipt.Repo == repo) && e.Receipt.RunID == runID {
			return &e, nil
		}
	}
	return nil, nil
}

// LoadSpinCeiling reads the spin ceiling (maximum allowable spin seconds) from path.
func LoadSpinCeiling(path string) (int64, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	lines := strings.Split(string(raw), "\n")
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		fields := strings.Fields(l)
		valStr := fields[len(fields)-1]
		val, err := strconv.ParseInt(valStr, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse spin ceiling %q in %s: %w", l, path, err)
		}
		return val, nil
	}
	return 0, fmt.Errorf("no spin ceiling value found in %s", path)
}

// CheckSpinCeiling verifies that no entry has spin exceeding the ceiling.
func CheckSpinCeiling(entries []Entry, ceiling int64) error {
	for _, e := range entries {
		if e.Cost.Spin > ceiling {
			return fmt.Errorf("run %s (%s@%s) spin %ds exceeds ceiling %ds",
				e.Receipt.RunID, e.Receipt.Repo, e.Receipt.SHA, e.Cost.Spin, ceiling)
		}
	}
	return nil
}

// CheckDevSpin verifies that the most recent dev runs (PR == "" or "-") do not exceed ceiling.
func CheckDevSpin(entries []Entry, ceiling int64, devRunsCount int) error {
	checked := 0
	for _, e := range entries {
		if e.Receipt.PR != "" && e.Receipt.PR != "-" {
			continue
		}
		if e.Cost.Spin > ceiling {
			return fmt.Errorf("dev run %s (%s@%s) spin %ds exceeds ceiling %ds",
				e.Receipt.RunID, e.Receipt.Repo, e.Receipt.SHA, e.Cost.Spin, ceiling)
		}
		checked++
		if devRunsCount > 0 && checked >= devRunsCount {
			break
		}
	}
	return nil
}
