package ci

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExtractFailLinesWithRealFixtures(t *testing.T) {
	t.Parallel()

	// 1. windows-sandbox.log (plain go test output)
	lines := ExtractFailLines("test-windows-pr (0)", failedFixture(t, "windows-sandbox.log"), 3)
	if len(lines) == 0 {
		t.Fatal("expected fail lines from windows-sandbox.log")
	}
	if !strings.Contains(lines[0], "TestDenialsInsideTheAllowedSetAreNotReported") {
		t.Errorf("got %q, want TestDenialsInsideTheAllowedSetAreNotReported in head", lines[0])
	}
	if len(lines) > 3 {
		t.Errorf("got %d lines, want <= 3", len(lines))
	}

	// 2. studio-review.log (go test -json frames)
	lines = ExtractFailLines("test (3/4 studio)", failedFixture(t, "studio-review.log"), 3)
	if len(lines) == 0 {
		t.Fatal("expected fail lines from studio-review.log")
	}
	if !strings.Contains(lines[0], "TestMutateRemovesItsWorktreeOnBothPaths") {
		t.Errorf("got %q, want TestMutateRemovesItsWorktreeOnBothPaths in head", lines[0])
	}

	// 3. merge-darwin-timeout.log (panic: test timed out)
	lines = ExtractFailLines("test-hosted-merge (darwin, 1)", failedFixture(t, "merge-darwin-timeout.log"), 3)
	if len(lines) == 0 {
		t.Fatal("expected fail lines from merge-darwin-timeout.log")
	}
	if !strings.Contains(lines[0], "panic: test timed out after 1m40s") {
		t.Errorf("got %q, want timeout line", lines[0])
	}

	// 4. inline-gate-werror.log (C compiler error outside go test)
	lines = ExtractFailLines("inline-gate (ubuntu-latest, go)", failedFixture(t, "inline-gate-werror.log"), 3)
	if len(lines) == 0 {
		t.Fatal("expected error lines from inline-gate-werror.log")
	}
	if !strings.Contains(lines[0], "error:") && !strings.Contains(lines[0], "-Werror") {
		t.Errorf("got %q, want compiler error", lines[0])
	}
}

type fakeQueueForge struct {
	nodes      []QueueNode
	runs       []MergeGroupRun
	jobs       map[int64][]FailedJob
	logs       map[int64]string
	err        error
	entriesErr error
	runsErr    error
	jobsErr    error
	logsErr    error
}

func (f *fakeQueueForge) QueueEntries(ctx context.Context, repo, branch string) ([]QueueNode, error) {
	if f.entriesErr != nil {
		return nil, f.entriesErr
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.nodes, nil
}

func (f *fakeQueueForge) MergeGroupRuns(ctx context.Context, repo string) ([]MergeGroupRun, error) {
	if f.runsErr != nil {
		return nil, f.runsErr
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.runs, nil
}

func (f *fakeQueueForge) Jobs(ctx context.Context, repo string, runID int64) ([]FailedJob, error) {
	if f.jobsErr != nil {
		return nil, f.jobsErr
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.jobs[runID], nil
}

func (f *fakeQueueForge) JobLog(ctx context.Context, repo string, jobID int64) (string, error) {
	if f.logsErr != nil {
		return "", f.logsErr
	}
	if f.err != nil {
		return "", f.err
	}
	return f.logs[jobID], nil
}

func TestInspectQueueForgeErrors(t *testing.T) {
	t.Parallel()

	// 1. QueueEntries error
	f1 := &fakeQueueForge{entriesErr: errors.New("graphql timeout")}
	_, err := InspectQueue(context.Background(), f1, "mas-bandwidth/nova-tools", "dev", 3)
	if err == nil {
		t.Fatal("expected error on QueueEntries failure, got nil")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "query merge queue") || !strings.Contains(errStr, "graphql timeout") ||
		!strings.Contains(errStr, "merge queue entries were not read") || !strings.Contains(errStr, "check forge access") {
		t.Errorf("QueueEntries error = %q, want operation, cause, state, and next action", errStr)
	}

	// 2. MergeGroupRuns error
	f2 := &fakeQueueForge{
		nodes:   []QueueNode{{PR: 4582, Position: 0, State: "AWAITING_CHECKS", HeadSHA: "e29673fe4"}},
		runsErr: errors.New("502 bad gateway"),
	}
	_, err = InspectQueue(context.Background(), f2, "mas-bandwidth/nova-tools", "dev", 3)
	if err == nil {
		t.Fatal("expected error on MergeGroupRuns failure, got nil")
	}
	errStr = err.Error()
	if !strings.Contains(errStr, "list merge_group runs") || !strings.Contains(errStr, "502 bad gateway") ||
		!strings.Contains(errStr, "merge-group runs were not read") || !strings.Contains(errStr, "check forge access") {
		t.Errorf("MergeGroupRuns error = %q, want operation, cause, state, and next action", errStr)
	}

	// 3. Jobs error
	f3 := &fakeQueueForge{
		nodes: []QueueNode{{PR: 4582, Position: 0, State: "AWAITING_CHECKS", HeadSHA: "e29673fe4"}},
		runs: []MergeGroupRun{{
			ID:         35375346271,
			HeadBranch: "gh-readonly-queue/dev/pr-4582-82c79a57ae0f",
			Conclusion: "failure",
		}},
		jobsErr: errors.New("rate limited"),
	}
	_, err = InspectQueue(context.Background(), f3, "mas-bandwidth/nova-tools", "dev", 3)
	if err == nil {
		t.Fatal("expected error on Jobs failure, got nil")
	}
	errStr = err.Error()
	if !strings.Contains(errStr, "list jobs for run") || !strings.Contains(errStr, "rate limited") ||
		!strings.Contains(errStr, "failed job details were not read") || !strings.Contains(errStr, "check forge access") {
		t.Errorf("Jobs error = %q, want operation, cause, state, and next action", errStr)
	}

	// 4. JobLog error
	f4 := &fakeQueueForge{
		nodes: []QueueNode{{PR: 4582, Position: 0, State: "AWAITING_CHECKS", HeadSHA: "e29673fe4"}},
		runs: []MergeGroupRun{{
			ID:         35375346271,
			HeadBranch: "gh-readonly-queue/dev/pr-4582-82c79a57ae0f",
			Conclusion: "failure",
		}},
		jobs: map[int64][]FailedJob{
			35375346271: {{ID: 101, Name: "test (linux)", Conclusion: "failure"}},
		},
		logsErr: errors.New("log 404"),
	}
	_, err = InspectQueue(context.Background(), f4, "mas-bandwidth/nova-tools", "dev", 3)
	if err == nil {
		t.Fatal("expected error on JobLog failure, got nil")
	}
	errStr = err.Error()
	if !strings.Contains(errStr, "read log for job") || !strings.Contains(errStr, "log 404") ||
		!strings.Contains(errStr, "job failure log was not read") || !strings.Contains(errStr, "check forge access") {
		t.Errorf("JobLog error = %q, want operation, cause, state, and next action", errStr)
	}
}

func TestInspectQueueEmpty(t *testing.T) {
	t.Parallel()
	f := &fakeQueueForge{}
	report, err := InspectQueue(context.Background(), f, "mas-bandwidth/nova-tools", "dev", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Entries) != 0 {
		t.Fatalf("got %d entries, want 0", len(report.Entries))
	}
	lines := report.Lines()
	if len(lines) != 1 || lines[0] != "QUEUE OK repo=mas-bandwidth/nova-tools branch=dev entries=0" {
		t.Errorf("lines = %v, want QUEUE OK line", lines)
	}
}

func TestInspectQueueEntriesWithFailures(t *testing.T) {
	t.Parallel()
	f := &fakeQueueForge{
		nodes: []QueueNode{
			{PR: 4582, Position: 0, State: "AWAITING_CHECKS", HeadSHA: "e29673fe4"},
			{PR: 4583, Position: 1, State: "QUEUED", HeadSHA: "148564976"},
		},
		runs: []MergeGroupRun{
			{
				ID:         35375346271,
				HeadBranch: "gh-readonly-queue/dev/pr-4582-82c79a57ae0f",
				HeadSHA:    "e29673fe4",
				Status:     "completed",
				Conclusion: "failure",
			},
		},
		jobs: map[int64][]FailedJob{
			35375346271: {
				{ID: 101, Name: "lint", Conclusion: "success"},
				{ID: 102, Name: "test (linux)", Conclusion: "failure"},
			},
		},
		logs: map[int64]string{
			102: "--- FAIL: TestFoo (0.01s)\n    foo_test.go:42: expected 1 got 2\nFAIL\n",
		},
	}

	report, err := InspectQueue(context.Background(), f, "mas-bandwidth/nova-tools", "dev", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(report.Entries))
	}

	e0 := report.Entries[0]
	if e0.PR != 4582 || e0.Position != 0 || e0.State != "AWAITING_CHECKS" || e0.RunID != 35375346271 || e0.Conclusion != "failure" {
		t.Errorf("entry 0 = %+v", e0)
	}
	if e0.Job != "test (linux)" {
		t.Errorf("entry 0 job = %q, want 'test (linux)'", e0.Job)
	}
	if len(e0.FailLines) != 2 {
		t.Errorf("entry 0 fail lines = %v, want 2 lines", e0.FailLines)
	}

	e1 := report.Entries[1]
	if e1.PR != 4583 || e1.Position != 1 || e1.State != "QUEUED" || e1.RunID != 0 || e1.Conclusion != "" {
		t.Errorf("entry 1 = %+v", e1)
	}

	lines := report.Lines()
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if !strings.HasPrefix(lines[0], "QUEUE repo=mas-bandwidth/nova-tools branch=dev pr=4582 pos=0 state=AWAITING_CHECKS run=35375346271 conclusion=failure job=test\\x20(linux) fail=---") {
		t.Errorf("line 0 = %q", lines[0])
	}
	if lines[1] != "QUEUE repo=mas-bandwidth/nova-tools branch=dev pr=4583 pos=1 state=QUEUED run=- conclusion=- job=- fail=-" {
		t.Errorf("line 1 = %q", lines[1])
	}

	// Table output
	tbl := report.Table()
	if !strings.Contains(tbl, "4582") || !strings.Contains(tbl, "test (linux)") || !strings.Contains(tbl, "TestFoo") {
		t.Errorf("table output = %q", tbl)
	}

	// JSON output
	data, err := report.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var back QueueReport
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Entries) != 2 || back.Entries[0].PR != 4582 {
		t.Errorf("unmarshaled json = %+v", back)
	}
}

func TestGHQueueForgeRunner(t *testing.T) {
	t.Parallel()

	runner := func(ctx context.Context, args ...string) (string, error) {
		cmdLine := strings.Join(args, " ")
		switch {
		case strings.HasPrefix(cmdLine, "api graphql"):
			return `{
				"data": {
					"repository": {
						"mergeQueue": {
							"entries": {
								"nodes": [
									{
										"position": 0,
										"state": "AWAITING_CHECKS",
										"enqueuedAt": "2026-09-28T20:00:00Z",
										"pullRequest": {
											"number": 4582,
											"headRefOid": "e29673fe4"
										}
									}
								]
							}
						}
					}
				}
			}`, nil
		case strings.Contains(cmdLine, "actions/runs?event=merge_group"):
			return `{
				"workflow_runs": [
					{
						"id": 35375346271,
						"head_branch": "gh-readonly-queue/dev/pr-4582-82c79a57ae0f",
						"head_sha": "e29673fe4",
						"status": "completed",
						"conclusion": "failure"
					}
				]
			}`, nil
		case strings.Contains(cmdLine, "/jobs?per_page=100"):
			return `{
				"jobs": [
					{
						"id": 501,
						"name": "test (linux)",
						"conclusion": "failure",
						"run_attempt": 1,
						"head_sha": "e29673fe4"
					}
				]
			}`, nil
		case strings.Contains(cmdLine, "/jobs/501/logs"):
			return "--- FAIL: TestRunner (0.02s)\n    runner_test.go:10: bad\nFAIL\n", nil
		default:
			return "", nil
		}
	}

	forge := NewGHQueueForge("mas-bandwidth/nova-tools", 5*time.Second, runner)
	report, err := InspectQueue(context.Background(), forge, "mas-bandwidth/nova-tools", "dev", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(report.Entries))
	}
	if report.Entries[0].PR != 4582 || report.Entries[0].Job != "test (linux)" {
		t.Errorf("entry = %+v", report.Entries[0])
	}
}

func TestInspectQueueForgeErrorFailsClosed(t *testing.T) {
	t.Parallel()
	errForge := &errQueueForge{err: errors.New("API rate limit exceeded")}
	_, err := InspectQueue(context.Background(), errForge, "mas-bandwidth/nova-tools", "dev", 3)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "list merge_group runs") || !strings.Contains(errStr, "API rate limit exceeded") ||
		!strings.Contains(errStr, "merge-group runs were not read") || !strings.Contains(errStr, "check forge access") {
		t.Errorf("expected error naming operation, cause, state, and next action, got: %v", err)
	}
}

type errQueueForge struct {
	err error
}

func (e *errQueueForge) QueueEntries(context.Context, string, string) ([]QueueNode, error) {
	return []QueueNode{{PR: 100, Position: 0, State: "QUEUED"}}, nil
}

func (e *errQueueForge) MergeGroupRuns(context.Context, string) ([]MergeGroupRun, error) {
	return nil, e.err
}

func (e *errQueueForge) Jobs(context.Context, string, int64) ([]FailedJob, error) {
	return nil, e.err
}

func (e *errQueueForge) JobLog(context.Context, string, int64) (string, error) {
	return "", e.err
}

func TestExtractFailLinesSubtests(t *testing.T) {
	t.Parallel()

	log := `
=== RUN   TestEveryCommandMeetsTheOnboardingStandard
=== RUN   TestEveryCommandMeetsTheOnboardingStandard/nova-ci_queue
    standard_test.go:123: queue does not have -h
    standard_test.go:124: command failed
--- FAIL: TestEveryCommandMeetsTheOnboardingStandard (0.00s)
    --- FAIL: TestEveryCommandMeetsTheOnboardingStandard/nova-ci_queue (0.00s)
        standard_test.go:123: queue does not have -h
        standard_test.go:124: command failed
FAIL
`
	lines := ExtractFailLines("test", log, 3)
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "TestEveryCommandMeetsTheOnboardingStandard/nova-ci_queue") {
		t.Errorf("lines[0] = %q, want subtest header", lines[0])
	}
	if !strings.Contains(lines[1], "standard_test.go:123") {
		t.Errorf("lines[1] = %q, want standard_test.go:123", lines[1])
	}
	if !strings.Contains(lines[2], "standard_test.go:124") {
		t.Errorf("lines[2] = %q, want standard_test.go:124", lines[2])
	}
}

func TestInspectQueueFilterPR(t *testing.T) {
	t.Parallel()

	fake := &fakeQueueForge{
		// PR 4605 is not in queue (dequeued)
		nodes: []QueueNode{
			{PR: 4582, Position: 0, State: "AWAITING_CHECKS", HeadSHA: "e29673fe4"},
		},
		runs: []MergeGroupRun{
			{
				ID:         1001,
				HeadBranch: "gh-readonly-queue/dev/pr-4605-failsha",
				HeadSHA:    "failsha123",
				Status:     "completed",
				Conclusion: "failure",
			},
			{
				ID:         1002, // newer green run
				HeadBranch: "gh-readonly-queue/dev/pr-4605-greensha",
				HeadSHA:    "greensha456",
				Status:     "completed",
				Conclusion: "success",
			},
		},
		jobs: map[int64][]FailedJob{
			1001: {{ID: 99, Name: "test (darwin)", Conclusion: "failure"}},
		},
		logs: map[int64]string{
			99: "--- FAIL: TestSub (0.01s)\n    sub_test.go:10: bad\nFAIL\n",
		},
	}

	report, err := InspectQueue(context.Background(), fake, "mas-bandwidth/nova-tools", "dev", 3, QueueFilter{PR: 4605})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(report.Entries))
	}
	e := report.Entries[0]
	if e.PR != 4605 || e.Position != -1 || e.State != "DEQUEUED" {
		t.Errorf("entry = %+v, want PR 4605, pos -1, DEQUEUED", e)
	}
	// Prefer latest failed run
	if e.RunID != 1001 || e.Conclusion != "failure" || e.Job != "test (darwin)" {
		t.Errorf("entry = %+v, want RunID 1001 and failure conclusion", e)
	}
	if len(e.FailLines) != 2 || !strings.Contains(e.FailLines[1], "sub_test.go:10") {
		t.Errorf("fail lines = %v", e.FailLines)
	}
}

func TestInspectQueueFilterRun(t *testing.T) {
	t.Parallel()

	fake := &fakeQueueForge{
		nodes: []QueueNode{}, // empty queue
		runs: []MergeGroupRun{
			{
				ID:         2002,
				HeadBranch: "gh-readonly-queue/dev/pr-4605-abc",
				HeadSHA:    "abc1234",
				Status:     "completed",
				Conclusion: "failure",
			},
		},
		jobs: map[int64][]FailedJob{
			2002: {{ID: 77, Name: "lint", Conclusion: "failure"}},
		},
		logs: map[int64]string{
			77: "--- FAIL: TestLint (0.00s)\n    lint_test.go:5: lint error\nFAIL\n",
		},
	}

	report, err := InspectQueue(context.Background(), fake, "mas-bandwidth/nova-tools", "dev", 3, QueueFilter{RunID: 2002})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(report.Entries))
	}
	e := report.Entries[0]
	if e.PR != 4605 || e.Position != -1 || e.State != "DEQUEUED" || e.RunID != 2002 {
		t.Errorf("entry = %+v", e)
	}
	if e.Job != "lint" || len(e.FailLines) != 2 {
		t.Errorf("job=%q, failLines=%v", e.Job, e.FailLines)
	}
}

func TestInspectQueueEmptyJSON(t *testing.T) {
	t.Parallel()

	report := QueueReport{
		Repo:   "mas-bandwidth/nova-tools",
		Branch: "dev",
	}
	data, err := report.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "[]" {
		t.Errorf("got %q, want '[]'", string(data))
	}

	var back QueueReport
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal [] failed: %v", err)
	}
	if len(back.Entries) != 0 {
		t.Errorf("got %d entries, want 0", len(back.Entries))
	}
}

func TestCleanForgeError(t *testing.T) {
	t.Parallel()

	// 1. Unknown repo
	err1 := cleanForgeError(nil, "GraphQL: Could not resolve to a Repository with the name 'foo/bar'", "foo/bar")
	if err1.Error() != "repository foo/bar not found" {
		t.Errorf("got %q, want 'repository foo/bar not found'", err1.Error())
	}

	// 2. No credentials
	err2 := cleanForgeError(nil, "To get started with GitHub CLI, please run:  gh auth login", "foo/bar")
	if err2.Error() != "not authenticated to forge: run gh auth login" {
		t.Errorf("got %q, want 'not authenticated to forge: run gh auth login'", err2.Error())
	}

	// 3. Query string stripped
	raw := "query($owner:String!,$name:String!){repository(owner:$owner,name:$name){id}} error: rate limit"
	err3 := cleanForgeError(nil, raw, "foo/bar")
	if strings.Contains(err3.Error(), "query($") {
		t.Errorf("query string was not stripped: %q", err3.Error())
	}
}

