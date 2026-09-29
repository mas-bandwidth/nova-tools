package cicost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cireceipt"
	"github.com/redis/go-redis/v9"
)

// cicost_test.go is the red-test contract of the COST line. testdata/jobs.json
// is one run's job listing in the forge's shape: lint 15 s green, "test
// (linux)" 42 s FAILED, functional 33 s green in attempt 2 (a RERUN), docs
// skipped at 0 s, test-hosted 60 s green. No test here reads the clock or a
// socket; the stream write goes to a fake.

const fixtureSHA = "0123456789abcdef0123456789abcdef01234567"

func fixture(t *testing.T) []Job {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "jobs.json"))
	if err != nil {
		t.Fatal(err)
	}
	total, jobs, err := ParseJobs(raw)
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || len(jobs) != 5 {
		t.Fatalf("fixture: total_count %d, %d jobs, want 5 and 5", total, len(jobs))
	}
	return jobs
}

func receipt() cireceipt.Receipt {
	return cireceipt.Receipt{
		Repo: "mas-bandwidth/nova-tools", SHA: fixtureSHA, RunID: "777", PR: "4328",
		Workflow: "ci", Conclusion: "failure", At: "2026-09-26T20:06:00Z",
	}
}

func job(t *testing.T, c Cost, name string) CostJob {
	t.Helper()
	for _, j := range c.Jobs {
		if j.Name == name {
			return j
		}
	}
	t.Fatalf("no job %q in %+v", name, c.Jobs)
	return CostJob{}
}

// The fixture listing with one failed job and one rerun job yields a COST
// line whose spin equals those two jobs' seconds, 42 + 33, and whose total is
// every job's, 150.
func TestCostLineFromJobs(t *testing.T) {
	t.Parallel()

	c := FromJobs(fixture(t))
	if c.Spin != 42+33 {
		t.Errorf("spin = %d, want 75: the failed job's 42 s and the rerun job's 33 s", c.Spin)
	}
	if c.Total != 15+42+33+0+60 {
		t.Errorf("total = %d, want 150", c.Total)
	}
	if c.Unknown != 0 || len(c.Jobs) != 5 {
		t.Errorf("unknown = %d, jobs = %d, want 0 and 5", c.Unknown, len(c.Jobs))
	}
	if j := job(t, c, "test (linux)"); j.Spin != SpinFailed || j.Seconds != 42 {
		t.Errorf("the failed job = %+v, want spin=failed seconds=42", j)
	}
	if j := job(t, c, "functional"); j.Spin != SpinRerun || j.Seconds != 33 || j.Attempt != 2 {
		t.Errorf("the rerun job = %+v, want spin=rerun seconds=33 attempt=2", j)
	}
	for _, name := range []string{"lint", "docs", "test-hosted"} {
		if j := job(t, c, name); j.Spin != "" {
			t.Errorf("%s is spin (%s); a green first-attempt job is not", name, j.Spin)
		}
	}

	line := c.Line(receipt(), "")
	want := "COST repo=mas-bandwidth/nova-tools sha=" + fixtureSHA + " run=777 workflow=ci conclusion=failure pr=4328" +
		" jobs=5 total=150 spin=75 unknown=0" +
		` job=lint:15:success:1:ok job=test\x20(linux):42:failure:1:failed job=functional:33:success:2:rerun job=docs:0:skipped:1:ok job=test-hosted:60:success:1:ok ev=-`
	if line != want {
		t.Errorf("line =\n%s\nwant\n%s", line, want)
	}
	if strings.Count(line, "\n") != 0 || strings.Count(line, "COST ") != 1 {
		t.Errorf("the COST line is not one line: %q", line)
	}
}

// A receipt with no pull request prints pr=-; an empty receipt prints a dash
// in every slot rather than an empty field a scanner would misread.
func TestCostLineDashesTheEmptySlots(t *testing.T) {
	t.Parallel()

	line := Cost{}.Line(cireceipt.Receipt{Repo: "o/n"}, "")
	if line != "COST repo=o/n sha=- run=- workflow=- conclusion=- pr=- jobs=0 total=0 spin=0 unknown=0 ev=-" {
		t.Errorf("line = %q", line)
	}
}

// A cancelled job's seconds are spin, named cancelled; the forge's other
// spelling reads the same.
func TestCostCancelledJobIsSpin(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 26, 20, 0, 0, 0, time.UTC)
	for _, spelling := range []string{"cancelled", "canceled"} {
		c := FromJobs([]Job{
			{Name: "test", Conclusion: spelling, Attempt: 1, Started: at, Completed: at.Add(50 * time.Second)},
			{Name: "lint", Conclusion: "success", Attempt: 1, Started: at, Completed: at.Add(20 * time.Second)},
		})
		if c.Spin != 50 || c.Total != 70 {
			t.Errorf("%s: spin=%d total=%d, want 50 and 70", spelling, c.Spin, c.Total)
		}
		if j := job(t, c, "test"); j.Spin != SpinCancelled {
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
	c := FromJobs([]Job{
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
	c := FromJobs([]Job{
		{Name: "lint", Conclusion: "success", Attempt: 1, Started: at, Completed: at.Add(20 * time.Second)},
		{Name: "test", Conclusion: "", Attempt: 1, Started: at},
		{Name: "e2e", Conclusion: "failure", Attempt: 1, Started: at.Add(time.Minute), Completed: at},
	})
	if c.Total != 20 || c.Spin != 0 || c.Unknown != 2 {
		t.Errorf("total=%d spin=%d unknown=%d, want 20, 0 and 2", c.Total, c.Spin, c.Unknown)
	}
	line := c.Line(receipt(), "")
	for _, want := range []string{"unknown=2", "job=test:-:-:1:ok", "job=e2e:-:failure:1:failed"} {
		if !strings.Contains(line, want) {
			t.Errorf("line lacks %q:\n%s", want, line)
		}
	}
}

// A listing that is not the forge's JSON is refused, never read as an empty
// run; an empty listing is an empty run.
func TestCostRefusesAListingThatIsNotJSON(t *testing.T) {
	t.Parallel()

	if _, _, err := ParseJobs([]byte("<html>not a listing")); err == nil {
		t.Error("a non-JSON listing parsed; want an error")
	}
	total, jobs, err := ParseJobs([]byte(`{"total_count":0,"jobs":[]}`))
	if err != nil || total != 0 || len(jobs) != 0 {
		t.Errorf("an empty listing: total=%d jobs=%d err=%v, want 0, 0, nil", total, len(jobs), err)
	}
}

// fakeWriter records the one XADD the write makes.
type fakeWriter struct {
	stream string
	values []string
	err    error
}

func (f *fakeWriter) XAdd(_ context.Context, a *redis.XAddArgs) *redis.StringCmd {
	f.stream = a.Stream
	f.values, _ = a.Values.([]string)
	if f.err != nil {
		return redis.NewStringResult("", f.err)
	}
	return redis.NewStringResult("1700000000000-0", nil)
}

// The ci:cost entry carries the receipt's identity (so it joins the run's
// ev:github row), the totals, and one job:<name>:<attempt> field per job; the
// write goes to ci:cost and nowhere else, and the id comes back on the line.
func TestCostEntryCarriesTheReceiptAndEveryJob(t *testing.T) {
	t.Parallel()

	c := FromJobs(fixture(t))
	r := receipt()
	w := &fakeWriter{}
	id, err := Write(context.Background(), w, &r, c)
	if err != nil {
		t.Fatal(err)
	}
	if id != "1700000000000-0" || w.stream != Stream {
		t.Errorf("wrote %q to %q, want the id and %s", id, w.stream, Stream)
	}
	got := map[string]string{}
	for i := 0; i+1 < len(w.values); i += 2 {
		got[w.values[i]] = w.values[i+1]
	}
	want := map[string]string{
		"repo": "mas-bandwidth/nova-tools", "sha": fixtureSHA, "run_id": "777", "workflow": "ci",
		"conclusion": "failure", "pr": "4328", "at": "2026-09-26T20:06:00Z",
		"jobs": "5", "total": "150", "spin": "75", "unknown": "0",
		"job:lint:1": "lint:15:success:1:ok", "job:test (linux):1": `test\x20(linux):42:failure:1:failed`,
		"job:functional:2": "functional:33:success:2:rerun", "job:docs:1": "docs:0:skipped:1:ok", "job:test-hosted:1": "test-hosted:60:success:1:ok",
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
// could not write is a cost that is not written either, and the refusal names
// the flag; a failed XADD is an error naming the stream, never a silent line.
func TestCostWriteRefusesWhatTheReceiptRefuses(t *testing.T) {
	t.Parallel()

	c := FromJobs(fixture(t))
	bad := receipt()
	bad.SHA = "abc"
	w := &fakeWriter{}
	if _, err := Write(context.Background(), w, &bad, c); err == nil || !strings.Contains(err.Error(), "--sha") {
		t.Errorf("a short sha: err = %v, want the --sha refusal", err)
	}
	if w.stream != "" {
		t.Errorf("a refused receipt still wrote to %s", w.stream)
	}
	r := receipt()
	if _, err := Write(context.Background(), &fakeWriter{err: context.DeadlineExceeded}, &r, c); err == nil || !strings.Contains(err.Error(), "XADD ci:cost") {
		t.Errorf("a failed XADD: err = %v, want it named", err)
	}
	if _, err := Write(context.Background(), nil, &r, c); err == nil {
		t.Error("a nil writer: no error")
	}
}

func TestParseJobValue(t *testing.T) {
	t.Parallel()

	cj, err := ParseJobValue("lint:15:success:1:ok")
	if err != nil {
		t.Fatal(err)
	}
	if cj.Name != "lint" || cj.Seconds != 15 || !cj.Known || cj.Conclusion != "success" || cj.Attempt != 1 || cj.Spin != "" {
		t.Errorf("got %+v, want lint 15s success attempt 1 ok", cj)
	}

	cjFailed, err := ParseJobValue(`test\x20(linux):42:failure:1:failed`)
	if err != nil {
		t.Fatal(err)
	}
	if cjFailed.Name != "test (linux)" || cjFailed.Seconds != 42 || !cjFailed.Known || cjFailed.Conclusion != "failure" || cjFailed.Spin != "failed" {
		t.Errorf("got %+v, want test (linux) 42s failed", cjFailed)
	}

	cjUnknown, err := ParseJobValue("ci-ok:-:success:1:ok")
	if err != nil {
		t.Fatal(err)
	}
	if cjUnknown.Known || cjUnknown.Seconds != 0 {
		t.Errorf("cjUnknown should not be known: %+v", cjUnknown)
	}

	if _, err := ParseJobValue("bad"); err == nil {
		t.Error("expected error parsing malformed job value")
	}
}

func TestParseEntryAndRoundtrip(t *testing.T) {
	t.Parallel()

	c := FromJobs(fixture(t))
	r := receipt()
	fields := Fields(r, c)
	m := make(map[string]string)
	for i := 0; i < len(fields); i += 2 {
		m[fields[i]] = fields[i+1]
	}

	entry, err := ParseEntry("1700000000000-0", m)
	if err != nil {
		t.Fatal(err)
	}
	if entry.ID != "1700000000000-0" {
		t.Errorf("ID = %q, want 1700000000000-0", entry.ID)
	}
	if entry.Receipt.Repo != r.Repo || entry.Receipt.SHA != r.SHA || entry.Receipt.RunID != r.RunID {
		t.Errorf("receipt mismatch: got %+v, want %+v", entry.Receipt, r)
	}
	if entry.Cost.Total != c.Total || entry.Cost.Spin != c.Spin {
		t.Errorf("cost mismatch: got total=%d spin=%d, want total=%d spin=%d", entry.Cost.Total, entry.Cost.Spin, c.Total, c.Spin)
	}
	if len(entry.Cost.Jobs) != len(c.Jobs) {
		t.Errorf("got %d jobs, want %d", len(entry.Cost.Jobs), len(c.Jobs))
	}
	line := entry.Line()
	if !strings.HasPrefix(line, "COST repo=mas-bandwidth/nova-tools") || !strings.HasSuffix(line, "ev=1700000000000-0") {
		t.Errorf("entry.Line() = %q", line)
	}
}

type fakeReader struct {
	msgs []redis.XMessage
	err  error
}

func (f *fakeReader) XRevRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd {
	cmd := redis.NewXMessageSliceCmd(ctx)
	if f.err != nil {
		cmd.SetErr(f.err)
		return cmd
	}
	n := int(count)
	if n > len(f.msgs) {
		n = len(f.msgs)
	}
	cmd.SetVal(f.msgs[:n])
	return cmd
}

func makeFakeMessage(id, repo, sha, runID, pr string, spin, total int64) redis.XMessage {
	return redis.XMessage{
		ID: id,
		Values: map[string]any{
			"repo":       repo,
			"sha":        sha,
			"run_id":     runID,
			"workflow":   "ci",
			"conclusion": "success",
			"pr":         pr,
			"at":         "2026-09-29T10:00:00Z",
			"jobs":       "1",
			"total":      fmt.Sprint(total),
			"spin":       fmt.Sprint(spin),
			"unknown":    "0",
			"job:test:1": "test:50:success:1:ok",
		},
	}
}

func TestReadRecentAndLookupHelpers(t *testing.T) {
	t.Parallel()

	rdb := &fakeReader{
		msgs: []redis.XMessage{
			makeFakeMessage("1002-0", "mas-bandwidth/nova-tools", "head2", "2002", "4328", 0, 100),
			makeFakeMessage("1001-0", "mas-bandwidth/nova-tools", "head1", "2001", "", 10, 80),
		},
	}

	entries, err := ReadRecent(context.Background(), rdb, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}

	head, err := ReadHead(context.Background(), rdb, "mas-bandwidth/nova-tools", "head1")
	if err != nil || head == nil {
		t.Fatalf("ReadHead failed: %v, %v", head, err)
	}
	if head.Receipt.RunID != "2001" {
		t.Errorf("ReadHead run_id = %s, want 2001", head.Receipt.RunID)
	}

	pr, err := ReadPR(context.Background(), rdb, "mas-bandwidth/nova-tools", 4328)
	if err != nil || pr == nil {
		t.Fatalf("ReadPR failed: %v, %v", pr, err)
	}
	if pr.Receipt.RunID != "2002" {
		t.Errorf("ReadPR run_id = %s, want 2002", pr.Receipt.RunID)
	}

	run, err := ReadRun(context.Background(), rdb, "mas-bandwidth/nova-tools", "2002")
	if err != nil || run == nil {
		t.Fatalf("ReadRun failed: %v, %v", run, err)
	}
	if run.Receipt.SHA != "head2" {
		t.Errorf("ReadRun sha = %s, want head2", run.Receipt.SHA)
	}
}

func TestSpinCeilingChecks(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	path := filepath.Join(tmp, "ci_spin.txt")
	if err := os.WriteFile(path, []byte("# CI spin ceiling\n0\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ceiling, err := LoadSpinCeiling(path)
	if err != nil {
		t.Fatal(err)
	}
	if ceiling != 0 {
		t.Errorf("ceiling = %d, want 0", ceiling)
	}

	cleanEntries := []Entry{
		{ID: "1", Receipt: cireceipt.Receipt{RunID: "1", Repo: "repo", SHA: "sha1"}, Cost: Cost{Spin: 0}},
	}
	if err := CheckSpinCeiling(cleanEntries, 0); err != nil {
		t.Errorf("CheckSpinCeiling(0) with spin 0 returned error: %v", err)
	}

	spinningEntries := []Entry{
		{ID: "2", Receipt: cireceipt.Receipt{RunID: "2", Repo: "repo", SHA: "sha2"}, Cost: Cost{Spin: 15}},
	}
	if err := CheckSpinCeiling(spinningEntries, 0); err == nil {
		t.Error("CheckSpinCeiling(0) with spin 15 should have failed")
	}

	devEntries := []Entry{
		{ID: "1", Receipt: cireceipt.Receipt{RunID: "1", SHA: "s1", PR: ""}, Cost: Cost{Spin: 0}},
		{ID: "2", Receipt: cireceipt.Receipt{RunID: "2", SHA: "s2", PR: "100"}, Cost: Cost{Spin: 50}}, // PR run ignored by CheckDevSpin
	}
	if err := CheckDevSpin(devEntries, 0, 5); err != nil {
		t.Errorf("CheckDevSpin failed unexpectedly: %v", err)
	}

	badDevEntries := []Entry{
		{ID: "1", Receipt: cireceipt.Receipt{RunID: "1", SHA: "s1", PR: ""}, Cost: Cost{Spin: 20}},
	}
	if err := CheckDevSpin(badDevEntries, 0, 5); err == nil {
		t.Error("CheckDevSpin should fail when dev run spin > ceiling")
	}
}

func TestQueryFiltering(t *testing.T) {
	t.Parallel()

	rdb := &fakeReader{
		msgs: []redis.XMessage{
			makeFakeMessage("1003-0", "mas-bandwidth/nova-tools", "head33333333", "3003", "4328", 0, 120),
			makeFakeMessage("1002-0", "mas-bandwidth/nova-tools", "head22222222", "3002", "4300", 15, 80),
			makeFakeMessage("1001-0", "other/repo", "head11111111", "3001", "", 0, 50),
		},
	}

	// 1. Filter by repo
	entries, err := Query(context.Background(), rdb, QueryFilter{Repo: "other/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Receipt.RunID != "3001" {
		t.Fatalf("Query by repo got %d entries, want run 3001", len(entries))
	}

	// 2. Filter by PR
	entries, err = Query(context.Background(), rdb, QueryFilter{PR: 4328})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Receipt.RunID != "3003" {
		t.Fatalf("Query by PR got %d entries, want run 3003", len(entries))
	}

	// 3. Filter by Head
	entries, err = Query(context.Background(), rdb, QueryFilter{Head: "head2222"})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Receipt.RunID != "3002" {
		t.Fatalf("Query by head got %d entries, want run 3002", len(entries))
	}

	// 4. Limit
	entries, err = Query(context.Background(), rdb, QueryFilter{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("Query with limit 2 got %d entries", len(entries))
	}
}

func TestRenderTable(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		{
			ID: "100-0",
			Receipt: cireceipt.Receipt{
				RunID: "777",
				PR:    "4328",
				SHA:   "0123456789abcdef",
			},
			Cost: Cost{
				Total: 150,
				Spin:  75,
				Jobs: []CostJob{
					{Name: "lint", Seconds: 15, Known: true, Conclusion: "success", Attempt: 1},
					{Name: "test (linux)", Seconds: 42, Known: true, Conclusion: "failure", Attempt: 1, Spin: "failed"},
					{Name: "functional", Seconds: 33, Known: true, Conclusion: "success", Attempt: 2, Spin: "rerun"},
					{Name: "docs", Seconds: 0, Known: true, Conclusion: "skipped", Attempt: 1},
					{Name: "pending", Known: false},
				},
			},
		},
		{
			ID: "101-0",
			Receipt: cireceipt.Receipt{
				RunID: "778",
				SHA:   "abcdef12",
			},
			Cost: Cost{
				Total: 60,
				Spin:  0,
			},
		},
	}

	var buf bytes.Buffer
	if err := RenderTable(&buf, entries); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3:\n%s", len(lines), out)
	}
	header := lines[0]
	for _, col := range []string{"RUN", "PR", "HEAD", "JOBS", "TOTAL_S", "SPIN_S", "BREAKDOWN"} {
		if !strings.Contains(header, col) {
			t.Errorf("header missing column %q: %s", col, header)
		}
	}
	row1 := lines[1]
	if !strings.Contains(row1, "777") || !strings.Contains(row1, "4328") || !strings.Contains(row1, "01234567") || !strings.Contains(row1, "150") || !strings.Contains(row1, "75") {
		t.Errorf("row1 unexpected: %s", row1)
	}
	if !strings.Contains(row1, "lint:15s") || !strings.Contains(row1, "test (linux):42s(failed)") || !strings.Contains(row1, "functional:33s(rerun)") || !strings.Contains(row1, "pending:-") {
		t.Errorf("row1 breakdown unexpected: %s", row1)
	}
	row2 := lines[2]
	if !strings.Contains(row2, "778") || !strings.Contains(row2, "-") || !strings.Contains(row2, "60") || !strings.Contains(row2, "0") {
		t.Errorf("row2 unexpected: %s", row2)
	}

	// Empty table
	var emptyBuf bytes.Buffer
	if err := RenderTable(&emptyBuf, nil); err != nil {
		t.Fatal(err)
	}
	emptyLines := strings.Split(strings.TrimSpace(emptyBuf.String()), "\n")
	if len(emptyLines) != 1 {
		t.Fatalf("empty table should only have header line, got: %v", emptyLines)
	}
}

func TestRenderJSON(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		{
			ID: "100-0",
			Receipt: cireceipt.Receipt{
				RunID:      "777",
				PR:         "4328",
				SHA:        "0123456789abcdef",
				Repo:       "mas-bandwidth/nova-tools",
				Workflow:   "ci",
				Conclusion: "failure",
			},
			Cost: Cost{
				Total: 150,
				Spin:  75,
				Jobs: []CostJob{
					{Name: "lint", Seconds: 15, Known: true, Conclusion: "success", Attempt: 1},
					{Name: "test", Seconds: 42, Known: true, Conclusion: "failure", Attempt: 1, Spin: "failed"},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := RenderJSON(&buf, entries); err != nil {
		t.Fatal(err)
	}

	var parsed []JSONEntry
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v\noutput:\n%s", err, buf.String())
	}
	if len(parsed) != 1 {
		t.Fatalf("parsed %d entries, want 1", len(parsed))
	}
	p := parsed[0]
	if p.RunID != "777" || p.PR != "4328" || p.TotalSeconds != 150 || p.SpinSeconds != 75 || p.TotalS != 150 || p.SpinS != 75 {
		t.Errorf("parsed mismatch: %+v", p)
	}
	if len(p.Jobs) != 2 || p.Jobs[0].Name != "lint" || p.Jobs[1].Spin != "failed" {
		t.Errorf("jobs mismatch: %+v", p.Jobs)
	}

	// Empty entries produces []
	var emptyBuf bytes.Buffer
	if err := RenderJSON(&emptyBuf, nil); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(emptyBuf.String()) != "[]" {
		t.Errorf("empty JSON output = %q, want []", emptyBuf.String())
	}
}

func TestSumCost(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		{Cost: Cost{Total: 100, Spin: 10}},
		{Cost: Cost{Total: 250, Spin: 50}},
	}
	tot, spin := SumCost(entries)
	if tot != 350 || spin != 60 {
		t.Errorf("SumCost = (%d, %d), want (350, 60)", tot, spin)
	}

	emptyTot, emptySpin := SumCost(nil)
	if emptyTot != 0 || emptySpin != 0 {
		t.Errorf("SumCost(nil) = (%d, %d), want (0, 0)", emptyTot, emptySpin)
	}
}
