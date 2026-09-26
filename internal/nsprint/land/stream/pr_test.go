package stream

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/webhook"
	"github.com/redis/go-redis/v9"
)

// fakeForge is GitHub as land pr sees it: the PR read and the merge at a
// sha, nothing else. Any other path (a check-runs or workflow-runs read,
// GraphQL) fails the test.
type fakeForge struct {
	mu       sync.Mutex
	n        int
	pr       map[string]any
	mergeSHA string
	merges   []map[string]string
	auth     []string
}

func (f *fakeForge) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		var reply any
		switch {
		case r.Method == http.MethodGet && r.URL.Path == fmt.Sprintf("/repos/o/r/pulls/%d", f.n):
			reply = f.pr
		case r.Method == http.MethodPut && r.URL.Path == fmt.Sprintf("/repos/o/r/pulls/%d/merge", f.n):
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.merges = append(f.merges, body)
			if body["sha"] != f.pr["head"].(map[string]any)["sha"] {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]any{"message": "Head branch was modified"})
				return
			}
			reply = map[string]any{"sha": f.mergeSHA, "merged": true}
		default:
			t.Errorf("unexpected call %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(reply)
	})
}

var prHead = strings.Repeat("h", 40)

func newFakeForge() *fakeForge {
	return &fakeForge{n: 7, mergeSHA: strings.Repeat("m", 40), pr: map[string]any{
		"state": "open", "merged": false, "mergeable_state": "blocked", "title": "quack verbs",
		"head": map[string]any{"sha": prHead}}}
}

// ghLeg writes ci:r:<head>:gh the way the webhook ingest leaves it.
func ghLeg(t *testing.T, c *redis.Client, fields ...string) {
	t.Helper()
	if err := c.HSet(context.Background(), webhook.Key("o/r", prHead), fields).Err(); err != nil {
		t.Fatal(err)
	}
}

func runLandPR(t *testing.T, f *fakeForge, c *redis.Client) (LandPRReport, string, *GitHub, error) {
	t.Helper()
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	var log bytes.Buffer
	gh := &GitHub{API: srv.URL, Token: "t0k", HTTP: srv.Client()}
	rep, err := LandPR(context.Background(), gh, c, LandPROptions{Repo: "o/r", N: f.n, Log: &log})
	return rep, log.String(), gh, err
}

func prRedis(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// TestLandPRMergesOnGreen is nova-tools#4311's DONE-WHEN without polling:
// the head's GitHub leg in Redis is green, so one PR read and one merge at
// exactly the head (bearer token on both) end at MERGED <sha>; two REST
// calls, no check-state read, no GraphQL, no wait.
func TestLandPRMergesOnGreen(t *testing.T) {
	t.Parallel()

	f, c := newFakeForge(), prRedis(t)
	ghLeg(t, c, "gh", "green", "check:lint", "green 1 2026-09-26T12:00:00Z", "check:go-test-cmd", "green 2 2026-09-26T12:01:00Z", "wf:ci", "green 3 2026-09-26T12:01:00Z")
	rep, log, gh, err := runLandPR(t, f, c)
	if err != nil {
		t.Fatal(err)
	}
	want := "PR 7 CHECKS green 3/3 head=hhhhhhhh\nPR 7 MERGED " + f.mergeSHA + "\n"
	if log != want {
		t.Fatalf("log:\n%s\nwant:\n%s", log, want)
	}
	if rep.State != "merged" || rep.MergeSHA != f.mergeSHA || rep.CI != "green" || rep.Head != prHead || len(rep.Failed) != 0 {
		t.Fatalf("report %+v", rep)
	}
	if len(f.merges) != 1 || f.merges[0]["sha"] != prHead || f.merges[0]["merge_method"] != "merge" || f.merges[0]["commit_title"] != "quack verbs (#7)" {
		t.Fatalf("merges %v", f.merges)
	}
	if gh.Calls != 2 {
		t.Fatalf("rest calls %d, want 2", gh.Calls)
	}
	for _, a := range f.auth {
		if a != "Bearer t0k" {
			t.Fatalf("a call went without the token: %q", f.auth)
		}
	}
}

// TestLandPRWaitsRedAndDone: a pending or unrecorded leg is WAITING at once
// (one PR read, no merge, no second call); a red leg is FAILED naming the
// first red run; a merged, closed or conflicted PR ends on the PR read with
// nothing read from Redis mattering; a missing token or store is refused
// before any call.
func TestLandPRWaitsRedAndDone(t *testing.T) {
	t.Parallel()

	f, c := newFakeForge(), prRedis(t)
	rep, log, gh, err := runLandPR(t, f, c)
	if err != nil || rep.State != "waiting" || rep.CI != "" || len(f.merges) != 0 || gh.Calls != 1 ||
		log != "PR 7 CHECKS - 0/0 head=hhhhhhhh\nPR 7 WAITING ci:r:"+prHead+":gh has gh=-; run again once the webhook writes green\n" {
		t.Fatalf("unrecorded: %v %+v calls=%d\n%s", err, rep, gh.Calls, log)
	}

	ghLeg(t, c, "gh", "pending", "check:lint", "green 1 x", "check:go-test-cmd", "pending 2 x")
	rep, log, gh, err = runLandPR(t, f, c)
	if err != nil || rep.State != "waiting" || rep.CI != "pending" || len(f.merges) != 0 || gh.Calls != 1 || !strings.HasPrefix(log, "PR 7 CHECKS pending 1/2 head=hhhhhhhh\nPR 7 WAITING ") {
		t.Fatalf("pending: %v %+v\n%s", err, rep, log)
	}

	ghLeg(t, c, "gh", "red", "gh_fail", "check:go-test-cmd", "check:go-test-cmd", "red 2 x")
	rep, log, _, err = runLandPR(t, f, c)
	if err != nil || rep.State != "failed" || strings.Join(rep.Failed, ",") != "check:go-test-cmd" || len(f.merges) != 0 || !strings.HasSuffix(log, "PR 7 FAILED check:go-test-cmd\n") {
		t.Fatalf("red: %v %+v\n%s", err, rep, log)
	}

	for _, tc := range []struct {
		pr    map[string]any
		state string
		line  string
	}{
		{map[string]any{"state": "closed", "merged": true, "merge_commit_sha": strings.Repeat("b", 40), "head": map[string]any{"sha": prHead}}, "merged", "PR 7 MERGED " + strings.Repeat("b", 40) + "\n"},
		{map[string]any{"state": "closed", "merged": false, "head": map[string]any{"sha": prHead}}, "closed", "PR 7 FAILED closed without a merge\n"},
		{map[string]any{"state": "open", "mergeable_state": "dirty", "head": map[string]any{"sha": prHead}}, "conflict", "PR 7 FAILED conflict with the base (mergeable_state=dirty)\n"},
	} {
		f := newFakeForge()
		f.pr = tc.pr
		rep, log, gh, err := runLandPR(t, f, c)
		if err != nil || rep.State != tc.state || log != tc.line || gh.Calls != 1 || len(f.merges) != 0 {
			t.Fatalf("%s: %v %+v calls=%d\n%s", tc.state, err, rep, gh.Calls, log)
		}
	}

	// Green, but the head moved between the read and the merge: the forge
	// refuses the merge at the old sha and the error says so.
	ghLeg(t, c, "gh", "green", "gh_fail", "")
	f = newFakeForge()
	f.pr["head"] = map[string]any{"sha": prHead}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(f.pr)
			return
		}
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "Head branch was modified"})
	}))
	t.Cleanup(srv.Close)
	if _, err := LandPR(context.Background(), &GitHub{API: srv.URL, Token: "t", HTTP: srv.Client()}, c, LandPROptions{Repo: "o/r", N: 7}); err == nil || !strings.Contains(err.Error(), "Head branch was modified") {
		t.Fatalf("moved head: %v", err)
	}

	gh = &GitHub{API: "http://api.invalid", Token: ""}
	if _, err := LandPR(context.Background(), gh, c, LandPROptions{Repo: "o/r", N: 7}); err != ErrNoToken {
		t.Fatalf("no token: %v, want %v", err, ErrNoToken)
	}
	gh.Token = "x"
	for _, tc := range []struct {
		rdb  redis.Cmdable
		n    int
		want string
	}{{c, 0, "wants --pr <n>"}, {nil, 7, "has no store"}} {
		_, err := LandPR(context.Background(), gh, tc.rdb, LandPROptions{Repo: "o/r", N: tc.n})
		if _, ok := err.(*Refusal); !ok || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("refusal %q: %v (%T)", tc.want, err, err)
		}
	}
	if gh.Calls != 0 {
		t.Fatalf("a refusal made %d calls", gh.Calls)
	}
}

// TestLandPRWaitBlocksOnTheHeadsEvent (#4343: events over polling): with
// Wait set, a WAITING pass waits on ev:github and, after each wake, reads
// only the Redis leg: a timeout or a head event with the leg still pending
// costs no GitHub call; the wake after the webhook's green is the merge
// (one read, one merge). The wait is the injected seam: no clock.
func TestLandPRWaitBlocksOnTheHeadsEvent(t *testing.T) {
	t.Parallel()

	f, c := newFakeForge(), prRedis(t)
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	now := time.Date(2026, 9, 26, 16, 0, 0, 0, time.UTC)
	var waits []string
	await := func(_ context.Context, head string, d time.Duration) (string, error) {
		waits = append(waits, head[:4]+" "+d.String())
		now = now.Add(d)
		switch len(waits) {
		case 1:
			return "", nil // a timeout: nothing to read
		case 2:
			ghLeg(t, c, "gh", "pending", "check:lint", "green 1 x") // a head event, still pending
			return "head", nil
		}
		ghLeg(t, c, "gh", "green", "check:lint", "green 1 x") // the webhook wrote green
		return "head", nil
	}
	var log bytes.Buffer
	gh := &GitHub{API: srv.URL, Token: "t0k", HTTP: srv.Client()}
	o := LandPROptions{Repo: "o/r", N: f.n, Log: &log, Wait: 10 * time.Minute, Tick: 30 * time.Second,
		Now: func() time.Time { return now }, Await: await}
	rep, err := LandPRWait(context.Background(), gh, c, o)
	if err != nil || rep.State != "merged" || rep.MergeSHA != f.mergeSHA || len(f.merges) != 1 {
		t.Fatalf("%v %+v merges=%d\n%s", err, rep, len(f.merges), log.String())
	}
	if strings.Join(waits, ",") != "hhhh 30s,hhhh 30s,hhhh 30s" || gh.Calls != 3 {
		t.Fatalf("waits %v calls %d: three waits, GitHub only on the first pass and the green pass", waits, gh.Calls)
	}
	if !strings.Contains(log.String(), "PR 7 WAITING ci:r:"+prHead+":gh has gh=- after -\n") ||
		!strings.Contains(log.String(), "PR 7 WAITING ci:r:"+prHead+":gh has gh=pending after head\n") {
		t.Fatalf("log:\n%s", log.String())
	}

	// A red leg after a wake ends the wait with no call; a pull_request
	// event re-reads the PR (closed here) with one call.
	for _, tc := range []struct {
		wake  string
		leg   []string
		pr    map[string]any
		state string
		calls int
	}{
		{"head", []string{"gh", "red", "gh_fail", "check:go", "check:go", "red 2 x"}, nil, "failed", 1},
		{"pr", nil, map[string]any{"state": "closed", "merged": false, "head": map[string]any{"sha": prHead}}, "closed", 2},
	} {
		f2, c2 := newFakeForge(), prRedis(t)
		srv2 := httptest.NewServer(f2.handler(t))
		t.Cleanup(srv2.Close)
		gh2 := &GitHub{API: srv2.URL, Token: "t0k", HTTP: srv2.Client()}
		o2 := LandPROptions{Repo: "o/r", N: f2.n, Wait: time.Minute, Tick: 30 * time.Second,
			Now: func() time.Time { return now },
			Await: func(_ context.Context, _ string, d time.Duration) (string, error) {
				now = now.Add(d)
				if tc.leg != nil {
					ghLeg(t, c2, tc.leg...)
				}
				if tc.pr != nil {
					f2.mu.Lock()
					f2.pr = tc.pr
					f2.mu.Unlock()
				}
				return tc.wake, nil
			}}
		rep, err := LandPRWait(context.Background(), gh2, c2, o2)
		if err != nil || rep.State != tc.state || gh2.Calls != tc.calls || len(f2.merges) != 0 {
			t.Fatalf("%s: %v %+v calls=%d", tc.wake, err, rep, gh2.Calls)
		}
	}

	// Never green: the wait ends at the deadline, still waiting, no merge,
	// and no GitHub call past the first pass.
	f3, c3 := newFakeForge(), prRedis(t)
	srv3 := httptest.NewServer(f3.handler(t))
	t.Cleanup(srv3.Close)
	n := 0
	gh3 := &GitHub{API: srv3.URL, Token: "t0k", HTTP: srv3.Client()}
	o3 := LandPROptions{Repo: "o/r", N: f3.n, Wait: 70 * time.Second, Tick: 30 * time.Second,
		Now: func() time.Time { return now },
		Await: func(_ context.Context, _ string, d time.Duration) (string, error) {
			n++
			now = now.Add(d)
			return "", nil
		}}
	rep, err = LandPRWait(context.Background(), gh3, c3, o3)
	if err != nil || rep.State != "waiting" || n != 3 || len(f3.merges) != 0 || gh3.Calls != 1 {
		t.Fatalf("deadline: %v %+v waits=%d calls=%d (30s, 30s, then the 10s left; one call)", err, rep, n, gh3.Calls)
	}
}
