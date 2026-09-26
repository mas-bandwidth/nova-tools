package ci

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/webhook"
	"github.com/redis/go-redis/v9"
)

// cost_test.go is the red-test contract of the COST line (card ci-cost-line,
// nova-tools#4328). testdata/cost/jobs.json is one run's job listing in the
// forge's shape: lint 15 s green, "test (hulk)" 42 s FAILED, e2e 33 s green
// in attempt 2 (a RERUN), lisp skipped at 0 s, test-hosted 60 s green. No
// test here reads the clock or a socket; the stream write goes to a fake.

const costFixtureSHA = "0123456789abcdef0123456789abcdef01234567"

func costFixture(t *testing.T) []FailedJob {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "cost", "jobs.json"))
	if err != nil {
		t.Fatal(err)
	}
	total, jobs, err := ParseJobsPage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(jobs) != 5 {
		t.Fatalf("fixture: total_count %d, %d jobs, want 5 and 5", total, len(jobs))
	}
	return jobs
}

func costReceipt() webhook.Receipt {
	return webhook.Receipt{
		Repo: "mas-bandwidth/nova-tools", SHA: costFixtureSHA, RunID: "777", Event: "pull_request",
		HeadBranch: "rowan/x", BaseBranch: "dev", PR: "4328", Workflow: "ci", Conclusion: "failure",
		At: "2026-09-26T20:06:00Z",
	}
}

func costJob(t *testing.T, c Cost, name string) CostJob {
	t.Helper()
	for _, j := range c.Jobs {
		if j.Name == name {
			return j
		}
	}
	t.Fatalf("no job %q in %+v", name, c.Jobs)
	return CostJob{}
}

// TestCostLineFromJobs is the card's DONE-WHEN: the fixture listing with one
// failed job and one rerun job yields a COST line whose spin equals those two
// jobs' seconds, 42 + 33, and whose total is every job's, 150.
func TestCostLineFromJobs(t *testing.T) {
	t.Parallel()

	c := CostFromJobs(costFixture(t))
	if c.Spin != 42+33 {
		t.Errorf("spin = %d, want 75: the failed job's 42 s and the rerun job's 33 s", c.Spin)
	}
	if c.Total != 15+42+33+0+60 {
		t.Errorf("total = %d, want 150", c.Total)
	}
	if c.Unknown != 0 || len(c.Jobs) != 5 {
		t.Errorf("unknown = %d, jobs = %d, want 0 and 5", c.Unknown, len(c.Jobs))
	}
	if j := costJob(t, c, "test (hulk)"); j.Spin != SpinFailed || j.Seconds != 42 {
		t.Errorf("the failed job = %+v, want spin=failed seconds=42", j)
	}
	if j := costJob(t, c, "e2e"); j.Spin != SpinRerun || j.Seconds != 33 || j.Attempt != 2 {
		t.Errorf("the rerun job = %+v, want spin=rerun seconds=33 attempt=2", j)
	}
	for _, name := range []string{"lint", "lisp", "test-hosted"} {
		if j := costJob(t, c, name); j.Spin != "" {
			t.Errorf("%s is spin (%s); a green first-attempt job is not", name, j.Spin)
		}
	}

	line := c.Line(costReceipt(), "")
	want := "COST repo=mas-bandwidth/nova-tools sha=" + costFixtureSHA + " run=777 event=pull_request workflow=ci conclusion=failure pr=4328" +
		" jobs=5 total=150 spin=75 unknown=0" +
		` job=lint:15:success:1:ok job=test\x20(hulk):42:failure:1:failed job=e2e:33:success:2:rerun job=lisp:0:skipped:1:ok job=test-hosted:60:success:1:ok ev=-`
	if line != want {
		t.Errorf("line =\n%s\nwant\n%s", line, want)
	}
	if strings.Count(line, "\n") != 0 || strings.Count(line, "COST ") != 1 {
		t.Errorf("the COST line is not one line: %q", line)
	}
}

// A cancelled job's seconds are spin, named cancelled; the forge's other
// spelling reads the same.
func TestCostCancelledJobIsSpin(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 26, 20, 0, 0, 0, time.UTC)
	for _, spelling := range []string{"cancelled", "canceled"} {
		c := CostFromJobs([]FailedJob{
			{Name: "test", Conclusion: spelling, Attempt: 1, Started: at, Completed: at.Add(50 * time.Second)},
			{Name: "lint", Conclusion: "success", Attempt: 1, Started: at, Completed: at.Add(10 * time.Second)},
		})
		if c.Spin != 50 || c.Total != 60 {
			t.Errorf("%s: spin=%d total=%d, want 50 and 60", spelling, c.Spin, c.Total)
		}
		if j := costJob(t, c, "test"); j.Spin != SpinCancelled {
			t.Errorf("%s: spin reason = %q, want cancelled", spelling, j.Spin)
		}
	}
}

// Under filter=all the listing holds every attempt: the first attempt of a
// rerun job is superseded and its seconds are spin too, as is the rerun; a
// red first attempt is named failed, not superseded, because a reader chases
// the red.
func TestCostEveryAttemptIsPriced(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 26, 20, 0, 0, 0, time.UTC)
	c := CostFromJobs([]FailedJob{
		{Name: "test", Conclusion: "failure", Attempt: 1, Started: at, Completed: at.Add(42 * time.Second)},
		{Name: "e2e", Conclusion: "success", Attempt: 1, Started: at, Completed: at.Add(20 * time.Second)},
		{Name: "test", Conclusion: "success", Attempt: 2, Started: at.Add(time.Minute), Completed: at.Add(100 * time.Second)},
		{Name: "e2e", Conclusion: "success", Attempt: 2, Started: at.Add(time.Minute), Completed: at.Add(80 * time.Second)},
	})
	if c.Total != 42+20+40+20 || c.Spin != 42+20+40+20 {
		t.Errorf("total=%d spin=%d, want 122 and 122: every attempt of a rerun run is spin", c.Total, c.Spin)
	}
	want := []string{SpinFailed, SpinSuperseded, SpinRerun, SpinRerun}
	for i, w := range want {
		if c.Jobs[i].Spin != w {
			t.Errorf("job %d (%s attempt %d) spin = %q, want %q", i, c.Jobs[i].Name, c.Jobs[i].Attempt, c.Jobs[i].Spin, w)
		}
	}
}

// A job the forge has no completed_at for is still running: its seconds are
// unknown, printed "-", counted in unknown= and never summed as zero. A stamp
// out of order (completed before started) is unknown too, not negative.
func TestCostUnknownSecondsAreNotZero(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 26, 20, 0, 0, 0, time.UTC)
	c := CostFromJobs([]FailedJob{
		{Name: "lint", Conclusion: "success", Attempt: 1, Started: at, Completed: at.Add(10 * time.Second)},
		{Name: "test", Conclusion: "", Attempt: 1, Started: at},
		{Name: "e2e", Conclusion: "failure", Attempt: 1, Started: at.Add(time.Minute), Completed: at},
	})
	if c.Total != 10 || c.Spin != 0 || c.Unknown != 2 {
		t.Errorf("total=%d spin=%d unknown=%d, want 10, 0 and 2", c.Total, c.Spin, c.Unknown)
	}
	line := c.Line(costReceipt(), "")
	for _, want := range []string{"unknown=2", "job=test:-:-:1:ok", "job=e2e:-:failure:1:failed"} {
		if !strings.Contains(line, want) {
			t.Errorf("line lacks %q:\n%s", want, line)
		}
	}
}

// A listing that is not the forge's JSON is refused, never read as an empty run.
func TestCostRefusesAListingThatIsNotJSON(t *testing.T) {
	t.Parallel()

	if _, _, err := ParseJobsPage([]byte("<html>not a listing")); err == nil {
		t.Error("a non-JSON listing parsed; want an error")
	}
	total, jobs, err := ParseJobsPage([]byte(`{"total_count":0,"jobs":[]}`))
	if err != nil || total != 0 || len(jobs) != 0 {
		t.Errorf("an empty listing: total=%d jobs=%d err=%v, want 0, 0, nil", total, len(jobs), err)
	}
}

// fakeCostWriter records the one XADD the write makes.
type fakeCostWriter struct {
	stream string
	values []string
	err    error
}

func (f *fakeCostWriter) XAdd(_ context.Context, a *redis.XAddArgs) *redis.StringCmd {
	f.stream = a.Stream
	f.values, _ = a.Values.([]string)
	if f.err != nil {
		return redis.NewStringResult("", f.err)
	}
	return redis.NewStringResult("1700000000000-0", nil)
}

// The ci:cost entry carries the receipt's identity (so it joins
// ci:<repo>:<sha>:gh), the totals, and one job:<name>:<attempt> field per
// job; the write goes to ci:cost and nowhere else, and the id comes back on
// the line.
func TestCostEntryCarriesTheReceiptAndEveryJob(t *testing.T) {
	t.Parallel()

	c := CostFromJobs(costFixture(t))
	r := costReceipt()
	w := &fakeCostWriter{}
	id, err := WriteCost(context.Background(), w, &r, c)
	if err != nil {
		t.Fatal(err)
	}
	if id != "1700000000000-0" || w.stream != CostStream {
		t.Errorf("wrote %q to %q, want the id and %s", id, w.stream, CostStream)
	}
	got := map[string]string{}
	for i := 0; i+1 < len(w.values); i += 2 {
		got[w.values[i]] = w.values[i+1]
	}
	want := map[string]string{
		"repo": "mas-bandwidth/nova-tools", "sha": costFixtureSHA, "run_id": "777", "event": "pull_request",
		"workflow": "ci", "conclusion": "failure", "pr": "4328", "head_branch": "rowan/x", "base_branch": "dev",
		"at": "2026-09-26T20:06:00Z", "jobs": "5", "total": "150", "spin": "75", "unknown": "0",
		"job:lint:1": "lint:15:success:1:ok", "job:test (hulk):1": `test\x20(hulk):42:failure:1:failed`,
		"job:e2e:2": "e2e:33:success:2:rerun", "job:lisp:1": "lisp:0:skipped:1:ok", "job:test-hosted:1": "test-hosted:60:success:1:ok",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("field %s = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("the entry has %d fields, want %d: %v", len(got), len(want), w.values)
	}
	if !strings.HasSuffix(c.Line(r, id), " ev=1700000000000-0") {
		t.Errorf("the line does not end in the entry id: %s", c.Line(r, id))
	}
}

// The write goes through the receipt's own Validate: a receipt the runner
// could not write is a cost that is not written either, and the refusal
// names the flag; a failed XADD is an error, never a silent line.
func TestCostWriteRefusesWhatTheReceiptRefuses(t *testing.T) {
	t.Parallel()

	c := CostFromJobs(costFixture(t))
	bad := costReceipt()
	bad.SHA = "abc"
	w := &fakeCostWriter{}
	if _, err := WriteCost(context.Background(), w, &bad, c); err == nil || !strings.Contains(err.Error(), "--sha") {
		t.Errorf("a short sha: err = %v, want the --sha refusal", err)
	}
	if w.stream != "" {
		t.Errorf("a refused receipt still wrote to %s", w.stream)
	}
	r := costReceipt()
	if _, err := WriteCost(context.Background(), &fakeCostWriter{err: context.DeadlineExceeded}, &r, c); err == nil || !strings.Contains(err.Error(), "XADD ci:cost") {
		t.Errorf("a failed XADD: err = %v, want it named", err)
	}
	if _, err := WriteCost(context.Background(), nil, &r, c); err == nil {
		t.Error("a nil writer: no error")
	}
}
