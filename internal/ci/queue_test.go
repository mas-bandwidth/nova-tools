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
	nodes []QueueNode
	runs  []MergeGroupRun
	jobs  map[int64][]FailedJob
	logs  map[int64]string
	err   error
}

func (f *fakeQueueForge) QueueEntries(ctx context.Context, repo, branch string) ([]QueueNode, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.nodes, nil
}

func (f *fakeQueueForge) MergeGroupRuns(ctx context.Context, repo string) ([]MergeGroupRun, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.runs, nil
}

func (f *fakeQueueForge) Jobs(ctx context.Context, repo string, runID int64) ([]FailedJob, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.jobs[runID], nil
}

func (f *fakeQueueForge) JobLog(ctx context.Context, repo string, jobID int64) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.logs[jobID], nil
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
	if !strings.Contains(err.Error(), "API rate limit exceeded") {
		t.Errorf("expected error mentioning rate limit, got: %v", err)
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

