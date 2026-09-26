package gh

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// clock is the injected clock: Now reads it, Sleep advances it and records
// the waits. No test here waits on the wall.
type clock struct {
	mu    sync.Mutex
	t     time.Time
	slept []time.Duration
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Sleep(_ context.Context, d time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.slept = append(c.slept, d)
	c.t = c.t.Add(d)
	return nil
}

func newStore(t *testing.T) *redis.Client {
	t.Helper()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// forge answers a scripted list of replies in order and records requests.
type forge struct {
	mu      sync.Mutex
	replies []func(w http.ResponseWriter)
	reqs    []string
	srv     *httptest.Server
}

func newForge(t *testing.T, replies ...func(w http.ResponseWriter)) *forge {
	t.Helper()
	f := &forge{replies: replies}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.reqs = append(f.reqs, r.Method+" "+r.URL.RequestURI()+" auth="+r.Header.Get("Authorization"))
		if len(f.replies) == 0 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		f.replies[0](w)
		f.replies = f.replies[1:]
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func ok(remaining string, body string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		if remaining != "" {
			w.Header().Set("X-RateLimit-Remaining", remaining)
			w.Header().Set("X-RateLimit-Limit", "5000")
			w.Header().Set("X-RateLimit-Reset", "1790000000")
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(body))
	}
}

func secondary(retryAfter string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.Header().Set("X-RateLimit-Remaining", "4000")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"You have exceeded a secondary rate limit. Please wait a few minutes before you try again."}`))
	}
}

var t0 = time.Date(2026, 9, 26, 16, 0, 0, 0, time.UTC)

func newClient(f *forge, rdb redis.Cmdable, ck *clock, verb string) (*Client, *bytes.Buffer) {
	var log bytes.Buffer
	c := &Client{API: f.srv.URL, Token: "t0k", HTTP: f.srv.Client(), Verb: verb, Redis: rdb, Now: ck.Now, Sleep: ck.Sleep, Log: &log}
	return c, &log
}

// TestDoCountsPerVerbAndEndpoint: one call is one HINCRBY under
// gh:calls:<verb>:<endpoint> in its minute bucket, one index member, and
// the rate record from X-RateLimit-Remaining; gh budget reads it back.
func TestDoCountsPerVerbAndEndpoint(t *testing.T) {
	t.Parallel()
	rdb := newStore(t)
	ck := &clock{t: t0}
	f := newForge(t, ok("4990", `{"number":7}`), ok("4989", `{"number":8}`))
	c, _ := newClient(f, rdb, ck, "land stream")
	ctx := context.Background()
	n, err := c.OpenPR(ctx, "o/r", "h", "dev", "t", "b")
	if err != nil || n != 7 {
		t.Fatalf("OpenPR %d %v", n, err)
	}
	if _, err := c.OpenPR(ctx, "o/r", "h2", "dev", "t", "b"); err != nil {
		t.Fatal(err)
	}
	key := CallsKey("land-stream", "POST /repos/{repo}/pulls")
	minute := strconv.FormatInt(t0.Unix()/60, 10)
	if v := rdb.HGet(ctx, key, minute).Val(); v != "2" {
		t.Fatalf("%s[%s] = %q, want 2", key, minute, v)
	}
	if m := rdb.SMembers(ctx, IndexKey).Val(); len(m) != 1 || m[0] != "land-stream POST /repos/{repo}/pulls" {
		t.Fatalf("index %v", m)
	}
	rate := rdb.HGetAll(ctx, RateKey).Val()
	if rate["remaining"] != "4989" || rate["limit"] != "5000" || rate["verb"] != "land-stream" {
		t.Fatalf("rate %v", rate)
	}
	rep, err := Budget(ctx, rdb, t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Hour != 2 || len(rep.Rows) != 1 || rep.Rows[0].Hour != 2 || rep.Rate.Remaining != 4989 || !rep.Rate.Found {
		t.Fatalf("budget %+v", rep)
	}
	lines := rep.Lines()
	if !strings.HasPrefix(lines[0], "GH BUDGET hour=2 remaining=4989/5000") || lines[1] != "GH land-stream POST /repos/{repo}/pulls hour=2 total=2" {
		t.Fatalf("lines %q", lines)
	}
	if c.Calls != 2 {
		t.Fatalf("Calls %d", c.Calls)
	}
}

// TestSecondaryLimitRetriedAfterRetryAfter: a 403 secondary limit is
// retried after the Retry-After seconds, not surfaced (DONE-WHEN); the
// retry is a call of its own, counted, and the wait is one RETRY line.
func TestSecondaryLimitRetriedAfterRetryAfter(t *testing.T) {
	t.Parallel()
	rdb := newStore(t)
	ck := &clock{t: t0}
	f := newForge(t, secondary("7"), ok("4000", `{"id":55}`))
	c, log := newClient(f, rdb, ck, "read post")
	id, err := c.Comment(context.Background(), "o/r", 3, "SCORE 9 who=x head=abc")
	if err != nil || id != 55 {
		t.Fatalf("Comment %d %v", id, err)
	}
	if c.Calls != 2 || c.Retries != 1 {
		t.Fatalf("calls=%d retries=%d", c.Calls, c.Retries)
	}
	if len(ck.slept) != 1 || ck.slept[0] != 7*time.Second {
		t.Fatalf("slept %v, want one 7s wait", ck.slept)
	}
	if !strings.Contains(log.String(), "gh: RETRY read-post POST /repos/{repo}/issues/{n}/comments HTTP 403 secondary limit; waiting 7s (1/3)") {
		t.Fatalf("log %q", log.String())
	}
	rep, _ := Budget(context.Background(), rdb, t0.Add(10*time.Second))
	if rep.Hour != 2 {
		t.Fatalf("both requests counted, got %d", rep.Hour)
	}
}

// TestSecondaryLimitWithoutRetryAfterWaitsAMinute, and after MaxRetries the
// error surfaces with its REFUSED line.
func TestSecondaryLimitGivesUpAfterRetries(t *testing.T) {
	t.Parallel()
	ck := &clock{t: t0}
	f := newForge(t, secondary(""), secondary(""), secondary(""))
	c, log := newClient(f, nil, ck, "file")
	c.MaxRetries = 2
	_, err := c.Comment(context.Background(), "o/r", 3, "x")
	var herr *HTTPError
	if !errors.As(err, &herr) || herr.Status != 403 {
		t.Fatalf("err %v", err)
	}
	if len(ck.slept) != 2 || ck.slept[0] != DefaultRetryAfter {
		t.Fatalf("slept %v", ck.slept)
	}
	if !strings.Contains(log.String(), "gh: REFUSED file POST /repos/{repo}/issues/{n}/comments: secondary limit after 2 retries") {
		t.Fatalf("log %q", log.String())
	}
}

// TestPrimaryQuotaRefusedNotRetried: remaining 0 on a 403 is the hour's
// quota; the call is refused with the reset time and no wait.
func TestPrimaryQuotaRefusedNotRetried(t *testing.T) {
	t.Parallel()
	ck := &clock{t: t0}
	f := newForge(t, func(w http.ResponseWriter) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", "1790000000")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	})
	c, log := newClient(f, nil, ck, "land pr")
	_, err := c.ViewPR(context.Background(), "o/r", 1)
	if !errors.Is(err, ErrQuota) {
		t.Fatalf("err %v", err)
	}
	if len(ck.slept) != 0 || c.Calls != 1 {
		t.Fatalf("slept %v calls %d", ck.slept, c.Calls)
	}
	if !strings.Contains(log.String(), "gh: REFUSED land-pr GET /repos/{repo}/pulls/{n}: GitHub primary quota exhausted; resets at 1790000000") {
		t.Fatalf("log %q", log.String())
	}
}

// TestBudgetSpentIsErrBudget: the per-run cap refuses the call before any
// request is made.
func TestBudgetSpentIsErrBudget(t *testing.T) {
	t.Parallel()
	ck := &clock{t: t0}
	f := newForge(t, ok("", `{}`))
	c, _ := newClient(f, nil, ck, "reap")
	c.Budget = 1
	if err := c.Close(context.Background(), "o/r", 1); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(context.Background(), "o/r", 2); !errors.Is(err, ErrBudget) {
		t.Fatalf("err %v", err)
	}
	if len(f.reqs) != 1 {
		t.Fatalf("requests %v", f.reqs)
	}
}

// TestNon2xxIsHTTPErrorWithMessage: the reply's message is the error, and
// a 4xx that is not a limit is not retried.
func TestNon2xxIsHTTPErrorWithMessage(t *testing.T) {
	t.Parallel()
	ck := &clock{t: t0}
	f := newForge(t, func(w http.ResponseWriter) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"Head branch was modified"}`))
	})
	c, _ := newClient(f, nil, ck, "land pr")
	_, err := c.MergePR(context.Background(), "o/r", 9, "abc", "t")
	if err == nil || err.Error() != "PUT /repos/o/r/pulls/9/merge: HTTP 409: Head branch was modified" {
		t.Fatalf("err %v", err)
	}
	if c.Calls != 1 || len(ck.slept) != 0 {
		t.Fatalf("calls %d slept %v", c.Calls, ck.slept)
	}
}

// TestOneWriterPacesPerSecondAcrossClients: two clients on one store are
// one writer: with Rate 2 the third write in a second waits for the next
// second, whichever client makes it; reads are not paced.
func TestOneWriterPacesPerSecondAcrossClients(t *testing.T) {
	t.Parallel()
	rdb := newStore(t)
	ck := &clock{t: t0.Add(300 * time.Millisecond)}
	f := newForge(t, ok("", `{"id":1}`), ok("", `{"id":2}`), ok("", `{"id":3}`), ok("", `{}`), ok("", `{"id":4}`))
	a, _ := newClient(f, rdb, ck, "a")
	b, _ := newClient(f, rdb, ck, "b")
	a.Rate, b.Rate = 2, 2
	ctx := context.Background()
	for i, c := range []*Client{a, b} {
		if _, err := c.Comment(ctx, "o/r", i, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if len(ck.slept) != 0 {
		t.Fatalf("two writes in a second should not wait: %v", ck.slept)
	}
	if _, err := a.Comment(ctx, "o/r", 3, "x"); err != nil {
		t.Fatal(err)
	}
	if len(ck.slept) != 1 || ck.slept[0] != 700*time.Millisecond {
		t.Fatalf("third write waits to the next second: %v", ck.slept)
	}
	if _, err := b.ViewPR(ctx, "o/r", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Comment(ctx, "o/r", 4, "x"); err != nil {
		t.Fatal(err)
	}
	if len(ck.slept) != 1 {
		t.Fatalf("a read and the next second's first write do not wait: %v", ck.slept)
	}
	if n := len(rdb.HGetAll(ctx, WriterKey).Val()); n > 2 {
		t.Fatalf("writer hash keeps %d seconds; it self-prunes", n)
	}
}

// TestLocalPaceWithoutStore: no Redis, the same rule in-process.
func TestLocalPaceWithoutStore(t *testing.T) {
	t.Parallel()
	ck := &clock{t: t0}
	f := newForge(t, ok("", `{"id":1}`), ok("", `{"id":2}`))
	c, _ := newClient(f, nil, ck, "x")
	ctx := context.Background()
	for i := range 2 {
		if _, err := c.Comment(ctx, "o/r", i, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if len(ck.slept) != 1 || ck.slept[0] != time.Second {
		t.Fatalf("slept %v", ck.slept)
	}
}

// TestBudgetWindowAndPrune: a bucket older than the hour is total, not
// hour; one older than Keep is deleted by the read.
func TestBudgetWindowAndPrune(t *testing.T) {
	t.Parallel()
	rdb := newStore(t)
	ctx := context.Background()
	key := CallsKey("v", "GET /x")
	now := t0
	old := strconv.FormatInt(now.Add(-2*time.Hour).Unix()/60, 10)
	ancient := strconv.FormatInt(now.Add(-Keep-time.Hour).Unix()/60, 10)
	fresh := strconv.FormatInt(now.Add(-10*time.Minute).Unix()/60, 10)
	rdb.HSet(ctx, key, old, "5", ancient, "9", fresh, "3", "junk", "x")
	rdb.HSet(ctx, TotalKey, old, "5", ancient, "9", fresh, "3")
	rdb.SAdd(ctx, IndexKey, "v GET /x")
	rep, err := Budget(ctx, rdb, now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Hour != 3 || rep.Rows[0].Total != 8 {
		t.Fatalf("hour=%d total=%d", rep.Hour, rep.Rows[0].Total)
	}
	if rdb.HExists(ctx, key, ancient).Val() || rdb.HExists(ctx, TotalKey, ancient).Val() {
		t.Fatal("the ancient bucket was not pruned")
	}
	if l := rep.Lines(); rep.Rate.Found || len(l) != 2 || !strings.HasPrefix(l[0], "GH BUDGET hour=3 remaining=-") {
		t.Fatalf("lines %q", l)
	}
	n, err := HourCalls(ctx, rdb, now)
	if err != nil || n != 3 {
		t.Fatalf("HourCalls %d %v", n, err)
	}
}

func TestEndpoint(t *testing.T) {
	t.Parallel()
	for in, want := range map[[2]string]string{
		{"get", "/repos/mas-bandwidth/nova-tools/pulls/4343"}:                                 "GET /repos/{repo}/pulls/{n}",
		{"POST", "/repos/o/r/issues/12/comments"}:                                             "POST /repos/{repo}/issues/{n}/comments",
		{"GET", "/repos/o/r/commits/" + strings.Repeat("a", 40) + "/check-runs?per_page=100"}: "GET /repos/{repo}/commits/{sha}/check-runs",
		{"GET", "/rate_limit"}:                 "GET /rate_limit",
		{"GET", "/repos/o/r/issues?state=all"}: "GET /repos/{repo}/issues",
	} {
		if got := Endpoint(in[0], in[1]); got != want {
			t.Errorf("Endpoint(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

// TestNoStoreCountsNothingAndStillCalls: a client without Redis works and
// counts only in memory.
func TestNoStoreCountsNothingAndStillCalls(t *testing.T) {
	t.Parallel()
	ck := &clock{t: t0}
	f := newForge(t, ok("10", `{"body":"Closes #4"}`))
	c, _ := newClient(f, nil, ck, "")
	b, err := c.PRBody(context.Background(), "o/r", 2)
	if err != nil || b != "Closes #4" {
		t.Fatalf("%q %v", b, err)
	}
	if c.Calls != 1 || !strings.HasSuffix(f.reqs[0], "auth=Bearer t0k") {
		t.Fatalf("calls %d reqs %v", c.Calls, f.reqs)
	}
}
