package webhook_test

// The DONE-WHEN of nova-tools #3597's GitHub leg: a signed check_run or
// workflow_run delivery goes through the receiver (internal/post/hook) onto
// ev:github, one pass of the ci-github consumer writes ci:<repo>:<sha>:gh and
// acks the entry in the same function call, and no nova-sprint path reads a
// check state from GitHub. The store is the throwaway redis-server every
// Lua-backed control uses (miniredis has no FCALL); nothing here dials a
// bench or GitHub.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/webhook"
	"github.com/mas-bandwidth/nova-tools/internal/post/hook"
	"github.com/redis/go-redis/v9"
)

const (
	secret = "test-webhook-secret"
	head   = "3597359735973597359735973597359735973597"
)

type fixture struct {
	ctx    context.Context
	client *redis.Client
	h      http.Handler
	c      *webhook.Consumer
	n      int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	h, err := hook.NewHandler(client, []byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	c := &webhook.Consumer{Client: client, Name: "seat-a", Block: -1}
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return &fixture{ctx: ctx, client: client, h: h, c: c}
}

// deliver posts one signed delivery through the receiver; it must be written.
func (f *fixture) deliver(t *testing.T, event, body string) {
	t.Helper()
	f.n++
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(body))
	r := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	r.Header.Set("X-GitHub-Event", event)
	r.Header.Set("X-GitHub-Delivery", fmt.Sprintf("00000000-0000-0000-0000-%012d", f.n))
	r.Header.Set(hook.SignatureHeader, "sha256="+hex.EncodeToString(m.Sum(nil)))
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("deliver %s: HTTP %d %s", event, w.Code, w.Body.String())
	}
}

func (f *fixture) pass(t *testing.T) webhook.Counts {
	t.Helper()
	n, err := f.c.Pass(f.ctx)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	return n
}

func (f *fixture) gh(t *testing.T) map[string]string {
	t.Helper()
	m, err := f.client.HGetAll(f.ctx, webhook.Key("mas-bandwidth/nova-tools", head)).Result()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// settled asserts the group holds nothing pending and our own CI's request
// record was never created by the GitHub side (ns_ci_request is create-only
// by EXISTS).
func (f *fixture) settled(t *testing.T) {
	t.Helper()
	p, err := f.client.XPending(f.ctx, ghevent.Stream, webhook.Group).Result()
	if err != nil {
		t.Fatal(err)
	}
	if p.Count != 0 {
		t.Fatalf("XPENDING %s %s = %d, want 0", ghevent.Stream, webhook.Group, p.Count)
	}
	if n := f.client.Exists(f.ctx, "ci:nova-tools:"+head).Val(); n != 0 {
		t.Fatalf("the GitHub leg created the request record ci:nova-tools:%s", head)
	}
}

// testWait is the poll bound: NOVA_TEST_WAIT, default 30 s.
func testWait() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("NOVA_TEST_WAIT")); err == nil && d > 0 {
		return d
	}
	return 30 * time.Second
}

func checkRun(id int64, name, status, conclusion, at string) string {
	c := "null"
	if conclusion != "" {
		c = `"` + conclusion + `"`
	}
	return fmt.Sprintf(`{"action":"%s","check_run":{"id":%d,"name":"%s","head_sha":"%s","status":"%s","conclusion":%s,
"started_at":"2026-09-25T12:00:00Z","completed_at":%s,"pull_requests":[{"number":3597,"head":{"sha":"%s"}}]},
"repository":{"full_name":"mas-bandwidth/nova-tools"},"sender":{"login":"github-actions"}}`,
		map[bool]string{true: "completed", false: "created"}[status == "completed"], id, name, head, status, c,
		map[bool]string{true: `"` + at + `"`, false: "null"}[at != ""], head)
}

func workflowRun(id int64, name, action, status, conclusion, at string) string {
	c := "null"
	if conclusion != "" {
		c = `"` + conclusion + `"`
	}
	return fmt.Sprintf(`{"action":"%s","workflow_run":{"id":%d,"name":"%s","head_sha":"%s","status":"%s","conclusion":%s,
"created_at":"2026-09-25T12:00:00Z","updated_at":"%s","pull_requests":[{"number":3597}]},
"repository":{"full_name":"mas-bandwidth/nova-tools"},"sender":{"login":"github-actions"}}`,
		action, id, name, head, status, c, at)
}

// TestCheckRunEventWritesCI: check_run deliveries write one field per check
// on ci:<repo>:<sha>:gh and the fold; a red check turns the head red and
// names itself; a stale redelivery is kept out; a rerun with a newer id
// replaces the red attempt; every entry is acked in the writing call.
func TestCheckRunEventWritesCI(t *testing.T) {
	f := newFixture(t)
	f.deliver(t, "check_run", checkRun(501, "build", "completed", "success", "2026-09-25T12:05:00Z"))
	if n := f.pass(t); n.Applied != 1 {
		t.Fatalf("first pass %s, want applied=1", n.Line())
	}
	m := f.gh(t)
	if m["check:build"] != "green 501 2026-09-25T12:05:00Z" || m["gh"] != "green" || m["gh_fail"] != "" || m["pr"] != "3597" || m["n"] != "1" {
		t.Fatalf("after one green check: %v", m)
	}

	f.deliver(t, "check_run", checkRun(502, "test-packages", "in_progress", "", ""))
	f.pass(t)
	if m = f.gh(t); m["gh"] != "pending" {
		t.Fatalf("a running check: gh=%q, want pending (%v)", m["gh"], m)
	}
	f.deliver(t, "check_run", checkRun(502, "test-packages", "completed", "failure", "2026-09-25T12:09:00Z"))
	f.pass(t)
	if m = f.gh(t); m["gh"] != "red" || m["gh_fail"] != "check:test-packages" || m["check:test-packages"] != "red 502 2026-09-25T12:09:00Z" {
		t.Fatalf("a failed check: %v", m)
	}

	// A late in_progress redelivery of the same attempt is older: kept out.
	f.deliver(t, "check_run", checkRun(502, "test-packages", "in_progress", "", ""))
	if n := f.pass(t); n.Kept != 1 || n.Applied != 0 {
		t.Fatalf("stale redelivery %s, want kept=1 applied=0", n.Line())
	}
	if m = f.gh(t); m["gh"] != "red" {
		t.Fatalf("stale redelivery moved the fold: %v", m)
	}

	// A rerun is a new check_run id; it replaces the red attempt.
	f.deliver(t, "check_run", checkRun(777, "test-packages", "completed", "success", "2026-09-25T12:20:00Z"))
	f.pass(t)
	if m = f.gh(t); m["gh"] != "green" || m["gh_fail"] != "" || m["gh_at"] != "2026-09-25T12:20:00Z" || m["n"] != "2" {
		t.Fatalf("after the rerun: %v", m)
	}

	rec, err := webhook.Read(f.ctx, f.client, "nova-tools", head)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Found || rec.Word != webhook.Green || len(rec.Runs) != 2 || rec.Runs[1].Name != "test-packages" || rec.Runs[1].ID != "777" {
		t.Fatalf("Read: %+v", rec)
	}
	f.settled(t)
}

// TestWorkflowRunEventWritesCI: workflow_run deliveries write wf:<name>; a
// queued run is pending, its failure turns the head red; a delivery of
// another kind is acked as a no-op; an entry another seat read and dropped
// is reclaimed and written once.
func TestWorkflowRunEventWritesCI(t *testing.T) {
	f := newFixture(t)
	f.deliver(t, "workflow_run", workflowRun(9001, "ci", "requested", "queued", "", "2026-09-25T12:00:01Z"))
	f.deliver(t, "pull_request", `{"action":"opened","number":3597,"pull_request":{"number":3597,"head":{"sha":"`+head+`"}},
"repository":{"full_name":"mas-bandwidth/nova-tools"},"sender":{"login":"rowan"}}`)
	if n := f.pass(t); n.Applied != 1 || n.Skipped != 1 {
		t.Fatalf("first pass %s, want applied=1 skipped=1", n.Line())
	}
	if m := f.gh(t); m["wf:ci"] != "pending 9001 2026-09-25T12:00:01Z" || m["gh"] != "pending" {
		t.Fatalf("queued run: %v", m)
	}

	// Seat b reads the completion and dies before its call; seat a, after
	// MinIdle, reclaims it and writes it once.
	f.deliver(t, "workflow_run", workflowRun(9001, "ci", "completed", "completed", "failure", "2026-09-25T12:14:00Z"))
	b := &webhook.Consumer{Client: f.client, Name: "seat-b", Block: -1}
	if _, err := f.client.XReadGroup(f.ctx, &redis.XReadGroupArgs{Group: webhook.Group, Consumer: b.Name,
		Streams: []string{ghevent.Stream, ">"}, Count: 10, Block: -1}).Result(); err != nil {
		t.Fatal(err)
	}
	f.c.MinIdle = 50 * time.Millisecond
	var n webhook.Counts
	for deadline := time.Now().Add(testWait()); n.Reclaimed == 0 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
		n = f.pass(t)
	}
	if n.Reclaimed != 1 || n.Applied != 1 {
		t.Fatalf("reclaim pass %s, want reclaimed=1 applied=1", n.Line())
	}
	m := f.gh(t)
	if m["wf:ci"] != "red 9001 2026-09-25T12:14:00Z" || m["gh"] != "red" || m["gh_fail"] != "wf:ci" {
		t.Fatalf("failed run: %v", m)
	}
	// Re-run all jobs keeps the run id; a later update wins.
	f.deliver(t, "workflow_run", workflowRun(9001, "ci", "completed", "completed", "success", "2026-09-25T12:30:00Z"))
	f.pass(t)
	if m = f.gh(t); m["gh"] != "green" {
		t.Fatalf("re-run of the workflow: %v", m)
	}
	f.settled(t)
}
