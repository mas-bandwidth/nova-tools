package stream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeQueue is GitHub as land pr sees it: the PR, its head's check runs,
// the repository's merge_group runs and their jobs, each answered by the
// walk's step, which the fake clock advances on every Sleep. No real time
// passes: Now reads the fake clock and Sleep moves it by d.
type fakeQueue struct {
	mu       sync.Mutex
	step     int
	now      time.Time
	n        int
	mergeSHA string
	// pr, checks, runs and jobs answer for the current step.
	pr      func(step int) map[string]any
	checks  func(step int) []map[string]any
	runs    func(step int) []map[string]any
	jobs    map[int64][]map[string]any
	enqueue func(body string) map[string]any
	// what the walk sent
	mutations []string
	auth      []string
	tokens    int
}

func (f *fakeQueue) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeQueue) Sleep(_ context.Context, d time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	f.step++
	return nil
}

func (f *fakeQueue) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		body, _ := io.ReadAll(r.Body)
		step := f.step
		var reply any
		switch {
		case r.Method == http.MethodGet && r.URL.Path == fmt.Sprintf("/repos/o/r/pulls/%d", f.n):
			reply = f.pr(step)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/o/r/commits/") && strings.HasSuffix(r.URL.Path, "/check-runs"):
			runs := f.checks(step)
			reply = map[string]any{"total_count": len(runs), "check_runs": runs}
		case r.Method == http.MethodPost && r.URL.Path == "/graphql":
			f.mutations = append(f.mutations, string(body))
			reply = f.enqueue(string(body))
		case r.Method == http.MethodGet && r.URL.Path == "/repos/o/r/actions/runs":
			if r.URL.Query().Get("event") != "merge_group" {
				t.Errorf("runs listed without event=merge_group: %s", r.URL)
			}
			reply = map[string]any{"workflow_runs": f.runs(step)}
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/repos/o/r/actions/runs/") && strings.HasSuffix(r.URL.Path, "/jobs"):
			var id int64
			fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/repos/o/r/actions/runs/"), "%d/jobs", &id)
			reply = map[string]any{"jobs": f.jobs[id]}
		default:
			t.Errorf("unexpected call %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(reply)
	})
}

func check(name, status, conclusion string) map[string]any {
	return map[string]any{"name": name, "status": status, "conclusion": conclusion}
}

func queueRun(id int64, branch, status, conclusion, at string) map[string]any {
	return map[string]any{"id": id, "head_branch": branch, "status": status, "conclusion": conclusion, "created_at": at}
}

// newFakeQueue is the happy walk for PR 7: checks 2/3 then 3/3 and clean at
// step 1, one queue run in progress at step 2, green at step 3, merged at
// step 4. An older failed queue attempt for the same PR sits in the list
// and is ignored (only the newest group branch counts).
func newFakeQueue() *fakeQueue {
	f := &fakeQueue{n: 7, now: time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC), mergeSHA: strings.Repeat("m", 40)}
	f.pr = func(step int) map[string]any {
		state := "blocked"
		if step >= 1 {
			state = "clean"
		}
		pr := map[string]any{"node_id": "PR_node7", "state": "open", "merged": false, "mergeable_state": state,
			"head": map[string]any{"sha": strings.Repeat("h", 40)}}
		if step >= 4 {
			pr["merged"], pr["merge_commit_sha"], pr["state"] = true, f.mergeSHA, "closed"
		}
		return pr
	}
	f.checks = func(step int) []map[string]any {
		if step == 0 {
			return []map[string]any{check("lint", "completed", "success"), check("go-test-cmd", "in_progress", ""), check("docs", "completed", "skipped")}
		}
		return []map[string]any{check("lint", "completed", "success"), check("go-test-cmd", "completed", "success"), check("docs", "completed", "skipped")}
	}
	f.runs = func(step int) []map[string]any {
		old := queueRun(90, "gh-readonly-queue/dev/pr-7-0000000000000000000000000000000000000000", "completed", "failure", "2026-09-26T11:00:00Z")
		other := queueRun(91, "gh-readonly-queue/dev/pr-8-1111111111111111111111111111111111111111", "in_progress", "", "2026-09-26T12:00:30Z")
		switch {
		case step < 2:
			return []map[string]any{old, other}
		case step == 2:
			return []map[string]any{old, other, queueRun(100, "gh-readonly-queue/dev/pr-7-2222222222222222222222222222222222222222", "in_progress", "", "2026-09-26T12:00:20Z")}
		default:
			return []map[string]any{old, other, queueRun(100, "gh-readonly-queue/dev/pr-7-2222222222222222222222222222222222222222", "completed", "success", "2026-09-26T12:00:20Z")}
		}
	}
	f.jobs = map[int64][]map[string]any{}
	f.enqueue = func(body string) map[string]any {
		return map[string]any{"data": map[string]any{"enqueuePullRequest": map[string]any{"mergeQueueEntry": map[string]any{"id": "MQE_1"}}}}
	}
	return f
}

func runLandPR(t *testing.T, f *fakeQueue, timeout time.Duration) (LandPRReport, string, error) {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	var log bytes.Buffer
	gh := &GitHub{API: srv.URL, Token: "t0k", HTTP: srv.Client()}
	rep, err := LandPR(context.Background(), gh, LandPROptions{Repo: "o/r", N: f.n, Timeout: timeout, Tick: 10 * time.Second,
		Now: f.Now, Sleep: f.Sleep, Log: &log})
	return rep, log.String(), err
}

// TestLandPRMerges is nova-tools#4311's DONE-WHEN: the walk prints the
// checks as they change, ENQUEUED once the head is green and mergeable
// (one enqueuePullRequest mutation with the PR's node id, bearer token on
// every call), each queue run status as it changes, and MERGED <sha>; the
// older queue attempt and another PR's run are ignored, and no real time
// passes.
func TestLandPRMerges(t *testing.T) {
	t.Parallel()

	f := newFakeQueue()
	rep, log, err := runLandPR(t, f, 20*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	want := "PR 7 CHECKS 2/3\nPR 7 CHECKS 3/3\nPR 7 ENQUEUED\nPR 7 QUEUE RUN 100 in_progress\nPR 7 QUEUE RUN 100 completed success\nPR 7 MERGED " + f.mergeSHA + "\n"
	if log != want {
		t.Fatalf("log:\n%s\nwant:\n%s", log, want)
	}
	if rep.State != "merged" || rep.MergeSHA != f.mergeSHA || !rep.Enqueued || len(rep.Failed) != 0 {
		t.Fatalf("report %+v", rep)
	}
	if len(f.mutations) != 1 || !strings.Contains(f.mutations[0], "enqueuePullRequest(input:{pullRequestId:$id})") || !strings.Contains(f.mutations[0], `"PR_node7"`) {
		t.Fatalf("mutations %q", f.mutations)
	}
	for _, a := range f.auth {
		if a != "Bearer t0k" {
			t.Fatalf("a call went without the token: %q", f.auth)
		}
	}
	if f.step != 4 {
		t.Fatalf("the walk slept %d ticks, want 4", f.step)
	}
}

// TestLandPRQueueRunFails: a red merge_group run ends the walk with the
// failing job names, and the PR is left in whatever state the forge has.
func TestLandPRQueueRunFails(t *testing.T) {
	t.Parallel()

	f := newFakeQueue()
	f.runs = func(step int) []map[string]any {
		if step < 2 {
			return nil
		}
		status, conclusion := "in_progress", ""
		if step >= 3 {
			status, conclusion = "completed", "failure"
		}
		return []map[string]any{queueRun(100, "gh-readonly-queue/dev/pr-7-2222222222222222222222222222222222222222", status, conclusion, "2026-09-26T12:00:20Z")}
	}
	f.jobs[100] = []map[string]any{{"name": "lint", "conclusion": "success"}, {"name": "go-test-cmd", "conclusion": "failure"}, {"name": "go-test-internal", "conclusion": "cancelled"}}
	rep, log, err := runLandPR(t, f, 20*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(log, "PR 7 QUEUE RUN 100 completed failure\nPR 7 FAILED go-test-cmd,go-test-internal\n") {
		t.Fatalf("log:\n%s", log)
	}
	if rep.State != "failed" || strings.Join(rep.Failed, ",") != "go-test-cmd,go-test-internal" || !rep.Enqueued {
		t.Fatalf("report %+v", rep)
	}
}

// TestLandPRChecksFailOrTimeOut: a red check at the head ends the walk
// before any enqueue, naming it; checks that never finish end it at the
// timeout with nothing enqueued; a PR already in the queue is ENQUEUED
// already; a missing token is the typed refusal before any call.
func TestLandPRChecksFailOrTimeOut(t *testing.T) {
	t.Parallel()

	f := newFakeQueue()
	f.checks = func(int) []map[string]any {
		return []map[string]any{check("lint", "completed", "failure"), check("go-test-cmd", "completed", "success"), check("docs", "completed", "cancelled")}
	}
	rep, log, err := runLandPR(t, f, 20*time.Minute)
	if err != nil || log != "PR 7 CHECKS 1/3\nPR 7 FAILED docs,lint\n" || rep.State != "failed" || rep.Enqueued || len(f.mutations) != 0 {
		t.Fatalf("red check: %v %+v\n%s", err, rep, log)
	}

	f = newFakeQueue()
	f.checks = func(int) []map[string]any { return []map[string]any{check("go-test-cmd", "queued", "")} }
	rep, log, err = runLandPR(t, f, 25*time.Second)
	if err != nil || rep.State != "timeout" || rep.Enqueued || f.step != 3 || !strings.HasSuffix(log, "PR 7 FAILED timeout after 25s waiting for checks (mergeable_state=clean)\n") {
		t.Fatalf("timeout: %v %+v step=%d\n%s", err, rep, f.step, log)
	}

	f = newFakeQueue()
	f.enqueue = func(string) map[string]any {
		return map[string]any{"errors": []map[string]any{{"message": "Pull request is already enqueued"}}}
	}
	_, log, err = runLandPR(t, f, 20*time.Minute)
	if err != nil || !strings.Contains(log, "PR 7 ENQUEUED already\n") || !strings.HasSuffix(log, "PR 7 MERGED "+f.mergeSHA+"\n") {
		t.Fatalf("already enqueued: %v\n%s", err, log)
	}

	f = newFakeQueue()
	f.enqueue = func(string) map[string]any {
		return map[string]any{"errors": []map[string]any{{"message": "Merge queue is not enabled for this branch"}}}
	}
	if _, _, err = runLandPR(t, f, 20*time.Minute); err == nil || !strings.Contains(err.Error(), "enqueuePullRequest: Merge queue is not enabled") {
		t.Fatalf("mutation error: %v", err)
	}

	gh := &GitHub{API: "http://api.invalid", Token: ""}
	if _, err := LandPR(context.Background(), gh, LandPROptions{Repo: "o/r", N: 7}); err != ErrNoToken {
		t.Fatalf("no token: %v, want %v", err, ErrNoToken)
	}
	gh.Token = "x"
	var ref *Refusal
	if _, err := LandPR(context.Background(), gh, LandPROptions{Repo: "o/r", N: 0}); err == nil || !strings.Contains(err.Error(), "wants <n>") {
		t.Fatalf("n=0: %v", err)
	} else if _, ok := err.(*Refusal); !ok {
		t.Fatalf("n=0: %T, want %T", err, ref)
	}
}
