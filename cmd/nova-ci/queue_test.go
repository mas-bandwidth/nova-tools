package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ci"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

type fakeQueueForge struct {
	nodes []ci.QueueNode
	runs  []ci.MergeGroupRun
	jobs  map[int64][]ci.FailedJob
	logs  map[int64]string
	err   error
}

func (f *fakeQueueForge) QueueEntries(ctx context.Context, repo, branch string) ([]ci.QueueNode, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.nodes, nil
}

func (f *fakeQueueForge) MergeGroupRuns(ctx context.Context, repo string) ([]ci.MergeGroupRun, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.runs, nil
}

func (f *fakeQueueForge) Jobs(ctx context.Context, repo string, runID int64) ([]ci.FailedJob, error) {
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

func runQueue(args []string, forge ci.QueueForge) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := cmdQueue(context.Background(), args, &stdout, &stderr, func(string) ci.QueueForge { return forge })
	return code, stdout.String(), stderr.String()
}

func TestQueueFlagsAndRefusals(t *testing.T) {
	t.Parallel()

	fake := &fakeQueueForge{}

	tests := []struct {
		name    string
		args    []string
		wantSub string
	}{
		{"missing repo", []string{}, "--repo is required"},
		{"invalid repo no slash", []string{"--repo", "nova-tools"}, "--repo wants owner/name"},
		{"invalid repo spaces", []string{"--repo", "owner / name"}, "--repo wants owner/name"},
		{"empty branch", []string{"--repo", "mas-bandwidth/nova-tools", "--branch", ""}, "--branch must not be empty"},
		{"positional arg", []string{"--repo", "mas-bandwidth/nova-tools", "bogus"}, "nothing positional"},
		{"invalid format", []string{"--repo", "mas-bandwidth/nova-tools", "--format", "xml"}, "--format wants receipt, table, or json"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, _, stderr := runQueue(tt.args, fake)
			if code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if !strings.Contains(stderr, tt.wantSub) {
				t.Errorf("stderr %q does not contain %q", stderr, tt.wantSub)
			}
		})
	}
}

func TestQueueForgeError(t *testing.T) {
	t.Parallel()
	fake := &fakeQueueForge{err: errors.New("no merge queue found")}
	code, _, stderr := runQueue([]string{"--repo", "mas-bandwidth/nova-tools"}, fake)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "no merge queue found") {
		t.Errorf("stderr = %q, want 'no merge queue found'", stderr)
	}
}

type runsErrForge struct {
	fakeQueueForge
}

func (r *runsErrForge) MergeGroupRuns(ctx context.Context, repo string) ([]ci.MergeGroupRun, error) {
	return nil, errors.New("HTTP 500: internal server error")
}

func TestQueueRunsForgeError(t *testing.T) {
	t.Parallel()
	fake := &runsErrForge{
		fakeQueueForge: fakeQueueForge{
			nodes: []ci.QueueNode{{PR: 100, Position: 0, State: "QUEUED"}},
		},
	}
	code, _, stderr := runQueue([]string{"--repo", "mas-bandwidth/nova-tools"}, fake)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "listing merge_group runs") || !strings.Contains(stderr, "internal server error") {
		t.Errorf("stderr %q missing expected refusal", stderr)
	}
}

func TestQueueEmpty(t *testing.T) {
	t.Parallel()
	fake := &fakeQueueForge{}
	code, stdout, stderr := runQueue([]string{"--repo", "mas-bandwidth/nova-tools", "--branch", "dev"}, fake)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
	want := "QUEUE OK repo=mas-bandwidth/nova-tools branch=dev entries=0\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

func TestQueueEntriesWithFailuresAndFormats(t *testing.T) {
	t.Parallel()

	fake := &fakeQueueForge{
		nodes: []ci.QueueNode{
			{PR: 4582, Position: 0, State: "AWAITING_CHECKS", HeadSHA: "e29673fe4"},
			{PR: 4583, Position: 1, State: "QUEUED", HeadSHA: "148564976"},
		},
		runs: []ci.MergeGroupRun{
			{
				ID:         35375346271,
				HeadBranch: "gh-readonly-queue/dev/pr-4582-82c79a57ae0f",
				HeadSHA:    "e29673fe4",
				Status:     "completed",
				Conclusion: "failure",
			},
		},
		jobs: map[int64][]ci.FailedJob{
			35375346271: {
				{ID: 101, Name: "test (linux)", Conclusion: "failure"},
			},
		},
		logs: map[int64]string{
			101: "--- FAIL: TestFoo (0.01s)\n    foo_test.go:42: expected 1 got 2\nFAIL\n",
		},
	}

	// 1. Default (receipt lines)
	code, stdout, stderr := runQueue([]string{"--repo", "mas-bandwidth/nova-tools", "--branch", "dev"}, fake)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %v", len(lines), lines)
	}
	wantLine0 := `QUEUE repo=mas-bandwidth/nova-tools branch=dev pr=4582 pos=0 state=AWAITING_CHECKS run=35375346271 conclusion=failure job=test\x20(linux) fail=---\x20FAIL:\x20TestFoo\x20(0.01s)\x20;\x20foo_test.go:42:\x20expected\x201\x20got\x202`
	if lines[0] != wantLine0 {
		t.Errorf("line 0 =\n%s\nwant:\n%s", lines[0], wantLine0)
	}
	wantLine1 := "QUEUE repo=mas-bandwidth/nova-tools branch=dev pr=4583 pos=1 state=QUEUED run=- conclusion=- job=- fail=-"
	if lines[1] != wantLine1 {
		t.Errorf("line 1 =\n%s\nwant:\n%s", lines[1], wantLine1)
	}

	// 2. Table output (--table)
	code, stdout, stderr = runQueue([]string{"--repo", "mas-bandwidth/nova-tools", "--branch", "dev", "--table"}, fake)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "PR") || !strings.Contains(stdout, "4582") || !strings.Contains(stdout, "test (linux)") {
		t.Errorf("table output:\n%s", stdout)
	}

	// 3. JSON output (--json)
	code, stdout, stderr = runQueue([]string{"--repo", "mas-bandwidth/nova-tools", "--branch", "dev", "--json"}, fake)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	var rep ci.QueueReport
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("json unmarshal failed: %v", err)
	}
	if len(rep.Entries) != 2 || rep.Entries[0].PR != 4582 || rep.Entries[0].Job != "test (linux)" {
		t.Errorf("unmarshaled report: %+v", rep)
	}
}

func TestQueueIntegrationWithMockedGH(t *testing.T) {
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

	forge := ci.NewGHQueueForge("mas-bandwidth/nova-tools", 5*time.Second, runner)
	code, stdout, stderr := runQueue([]string{"--repo", "mas-bandwidth/nova-tools", "--branch", "dev"}, forge)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "pr=4582") || !strings.Contains(stdout, "job=test\\x20(linux)") || !strings.Contains(stdout, "TestRunner") {
		t.Errorf("stdout = %q", stdout)
	}
}

// TestTheCommandReferenceQueueIsWhatTheToolPrints executes the fenced
// transcript under docs/CLI.md's `### queue` line for line through
// onboarding.CompareTranscript.
func TestTheCommandReferenceQueueIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.Transcript(string(raw), "nova-ci", "queue")
	if err != nil {
		t.Fatal(err)
	}
	steps, err := onboarding.Steps("nova-ci", lines)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 {
		t.Fatalf("the `### queue` block runs %d commands, want 1", len(steps))
	}

	fake := &fakeQueueForge{
		nodes: []ci.QueueNode{
			{PR: 4582, Position: 0, State: "AWAITING_CHECKS", HeadSHA: "e29673fe4"},
		},
		runs: []ci.MergeGroupRun{
			{
				ID:         35375346271,
				HeadBranch: "gh-readonly-queue/dev/pr-4582-82c79a57ae0f",
				HeadSHA:    "e29673fe4",
				Status:     "completed",
				Conclusion: "failure",
			},
		},
		jobs: map[int64][]ci.FailedJob{
			35375346271: {
				{ID: 101, Name: "test (linux)", Conclusion: "failure"},
			},
		},
		logs: map[int64]string{
			101: "--- FAIL: TestFoo (0.01s)\n    foo_test.go:42: expected 1 got 2\nFAIL\n",
		},
	}

	got := make([]onboarding.Result, 0, len(steps))
	for _, s := range steps {
		if len(s.Args) < 1 || s.Args[0] != "queue" {
			t.Fatalf("the documented command %q is not `nova-ci queue ...`", s.Line)
		}
		var out, errb bytes.Buffer
		code := cmdQueue(context.Background(), s.Args[1:], &out, &errb, func(string) ci.QueueForge { return fake })
		got = append(got, onboarding.Result{Code: code, Stdout: out.String(), Stderr: errb.String()})
	}
	for _, p := range onboarding.CompareTranscript(steps, got, nil) {
		t.Error(p)
	}
}
