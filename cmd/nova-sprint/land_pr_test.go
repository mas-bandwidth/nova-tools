package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// landPRFake is the smallest GitHub the verb walks: PR 12's checks are green
// and it is clean at once, one queue run is in progress after the first
// tick and green after the second, and the PR is merged after the third.
// The fake clock (landPRNow, landPRSleep) advances the step; no real time.
func landPRFake(t *testing.T, fail bool) (*httptest.Server, *int) {
	t.Helper()
	var mu sync.Mutex
	step := 0
	now := time.Date(2026, 9, 26, 15, 0, 0, 0, time.UTC)
	prevNow, prevSleep := landPRNow, landPRSleep
	landPRNow = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	landPRSleep = func(_ context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
		step++
		return nil
	}
	t.Cleanup(func() { landPRNow, landPRSleep = prevNow, prevSleep })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		s := step
		mu.Unlock()
		var reply any
		switch {
		case r.URL.Path == "/repos/o/r/pulls/12":
			pr := map[string]any{"node_id": "PR_12", "state": "open", "merged": false, "mergeable_state": "clean", "head": map[string]any{"sha": strings.Repeat("a", 40)}}
			if s >= 3 && !fail {
				pr["merged"], pr["merge_commit_sha"] = true, strings.Repeat("b", 40)
			}
			reply = pr
		case strings.HasSuffix(r.URL.Path, "/check-runs"):
			reply = map[string]any{"total_count": 1, "check_runs": []map[string]any{{"name": "lint", "status": "completed", "conclusion": "success"}}}
		case r.URL.Path == "/graphql":
			reply = map[string]any{"data": map[string]any{"enqueuePullRequest": map[string]any{"mergeQueueEntry": map[string]any{"id": "MQE"}}}}
		case r.URL.Path == "/repos/o/r/actions/runs":
			run := map[string]any{"id": 500, "head_branch": "gh-readonly-queue/dev/pr-12-" + strings.Repeat("c", 40), "status": "in_progress", "conclusion": "", "created_at": "2026-09-26T15:00:10Z"}
			if s >= 2 {
				run["status"], run["conclusion"] = "completed", map[bool]string{true: "failure", false: "success"}[fail]
			}
			reply = map[string]any{"workflow_runs": []map[string]any{run}}
		case r.URL.Path == "/repos/o/r/actions/runs/500/jobs":
			reply = map[string]any{"jobs": []map[string]any{{"name": "go-test-cmd", "conclusion": "failure"}}}
		default:
			t.Errorf("unexpected call %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(reply)
	}))
	t.Cleanup(srv.Close)
	return srv, &step
}

// TestLandPRVerb: the verb walks one PR to MERGED with the receipt line
// (exit 0), a red queue run is exit 1 naming the job, a missing token is
// the typed refusal with nothing called (exit 2), and usage is exit 2.
func TestLandPRVerb(t *testing.T) {
	t.Setenv("GH_TOKEN", "t0k")
	t.Setenv("GITHUB_TOKEN", "")

	srv, _ := landPRFake(t, false)
	code, out, errOut := runSprint("land", "pr", "12", "--repo", "o/r", "--api", srv.URL, "--timeout", "5m", "--tick", "10s")
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out, errOut)
	}
	want := "PR 12 CHECKS 1/1\nPR 12 ENQUEUED\nPR 12 QUEUE RUN 500 in_progress\nPR 12 QUEUE RUN 500 completed success\nPR 12 MERGED " + strings.Repeat("b", 40) + "\n" +
		"LAND PR repo=o/r pr=#12 state=merged merge=bbbbbbbb enqueued=true failed=- rest_calls="
	if !strings.HasPrefix(out, want) {
		t.Fatalf("stdout:\n%s\nwant prefix:\n%s", out, want)
	}

	srv, _ = landPRFake(t, true)
	code, out, _ = runSprint("land", "pr", "--repo", "o/r", "--api", srv.URL, "#12")
	if code != 1 || !strings.Contains(out, "PR 12 FAILED go-test-cmd\nLAND PR repo=o/r pr=#12 state=failed merge=- enqueued=true failed=go-test-cmd rest_calls=") {
		t.Fatalf("red queue run: exit %d\n%s", code, out)
	}

	prev := landStreamToken
	landStreamToken = func() (string, error) { return "", nil }
	t.Cleanup(func() { landStreamToken = prev })
	code, out, errOut = runSprint("land", "pr", "12", "--repo", "o/r", "--api", srv.URL)
	if code != 2 || out != "" || !strings.Contains(errOut, "REFUSED no GitHub token remedy=export GH_TOKEN (or GITHUB_TOKEN)") {
		t.Fatalf("no token: exit %d %q %q", code, out, errOut)
	}
	for _, args := range [][]string{{}, {"x"}, {"0"}, {"12", "13"}, {"12", "--repo", "norepo"}, {"12", "--timeout", "0"}, {"12", "--tick", "-1s"}} {
		if code, _, errOut := runSprint(append([]string{"land", "pr"}, args...)...); code != 2 || !strings.HasPrefix(errOut, "nova-sprint land pr: ") {
			t.Fatalf("%v: exit %d %q, want usage", args, code, errOut)
		}
	}
}
