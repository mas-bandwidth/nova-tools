package ghingest_test

// The DONE-WHEN of nova-tools #2657's ingest consumer: signed pull_request
// and issues deliveries go through the receiver (internal/post/hook) onto
// ev:github, and one pass of the ingest group updates OUR records from the
// entry alone: the PR record's head and state, and an issue's cards landed
// only when our lander closed it (else a finding, and no card moves). The
// store is the throwaway redis-server every Lua-backed control uses; nothing
// here dials a bench or GitHub.

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
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ghingest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/post/hook"
	"github.com/redis/go-redis/v9"
)

const (
	secret = "test-webhook-secret"
	repo   = "mas-bandwidth/nova-tools"
	sprint = "gh-2657"
	lander = "rowan-lander"
)

var (
	headA = strings.Repeat("a", 40)
	headB = strings.Repeat("b", 40)
	merge = strings.Repeat("m", 40)
)

type fixture struct {
	ctx    context.Context
	client *redis.Client
	h      http.Handler
	c      *ghingest.Consumer
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
	c := &ghingest.Consumer{Client: client, Name: "seat-a", Landers: []string{lander}, Block: -1}
	if _, err := c.Start(ctx, ""); err != nil {
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

func (f *fixture) pr(t *testing.T, action string, n int, head, at, extra string) {
	t.Helper()
	f.deliver(t, "pull_request", fmt.Sprintf(`{"action":%q,"number":%d,"pull_request":{"number":%d,
		"updated_at":%q,"head":{"sha":%q}%s},"repository":{"full_name":%q},"sender":{"login":"someone"}}`,
		action, n, n, at, head, extra, repo))
}

func (f *fixture) issueClosed(t *testing.T, n int, sender, reason string) {
	t.Helper()
	f.deliver(t, "issues", fmt.Sprintf(`{"action":"closed","issue":{"number":%d,"state":"closed",
		"state_reason":%q,"updated_at":"2026-09-25T12:00:00Z","labels":[]},
		"repository":{"full_name":%q},"sender":{"login":%q}}`, n, reason, repo, sender))
}

func (f *fixture) pass(t *testing.T) ghingest.Counts {
	t.Helper()
	n, err := f.c.Pass(f.ctx)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	return n
}

func (f *fixture) record(t *testing.T, n int) map[string]string {
	t.Helper()
	m, err := f.client.HGetAll(f.ctx, fmt.Sprintf("pr:nova-tools:%d", n)).Result()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// card pushes one card of the sprint through ns_card_push, the path every
// card takes, with origin as its ORIGIN: line.
func (f *fixture) card(t *testing.T, label, origin string) string {
	t.Helper()
	id := "s:" + sprint + ":card:" + label
	keys := []string{id, "s:" + sprint + ":pool", "s:" + sprint + ":waiting", "s:" + sprint + ":log",
		"s:" + sprint + ":idx:card:queued"}
	reply, err := f.client.FCall(f.ctx, "ns_card_push", keys,
		label, "sha-"+label, "0", "dev", strings.Repeat("0", 40), "[]", repo, "build",
		"", "", "", "1", "", "", "", "", "redis", origin, "").Text()
	if err != nil || !strings.HasPrefix(reply, "OK") {
		t.Fatalf("push %s: %q %v", label, reply, err)
	}
	return id
}

func (f *fixture) where(t *testing.T, id string) string {
	t.Helper()
	c := f.client.HMGet(f.ctx, id, "state", "where", "where_ok").Val()
	return fmt.Sprintf("%v/%v/%v", c[0], c[1], c[2])
}

func (f *fixture) settled(t *testing.T) {
	t.Helper()
	p, err := f.client.XPending(f.ctx, ghevent.Stream, ghingest.Group).Result()
	if err != nil {
		t.Fatal(err)
	}
	if p.Count != 0 {
		t.Fatalf("XPENDING %s %s = %d, want 0", ghevent.Stream, ghingest.Group, p.Count)
	}
}

func TestIngestUpdatesOurRecordsFromTheWebhook(t *testing.T) {
	t.Run("pr-head-and-state", func(t *testing.T) {
		f := newFixture(t)
		// Our record for PR 7 (the stream lander's record op writes it);
		// PR 8 has none and is not ours.
		f.client.HSet(f.ctx, "pr:nova-tools:7", "repo", repo, "n", "7", "state", "open", "head", headA, "ci", "green", "mergeable", "true")
		f.pr(t, "opened", 8, headA, "2026-09-25T10:00:00Z", "")
		f.pr(t, "synchronize", 7, headB, "2026-09-25T10:01:00Z", "")
		if got := f.pass(t); got != (ghingest.Counts{Applied: 1, Unknown: 1}) {
			t.Fatalf("pass 1: %s", got.Line())
		}
		r := f.record(t, 7)
		if r["head"] != headB || r["ci"] != "pending" || r["mergeable"] != "" || r["state"] != "open" || r["gh_at"] != "2026-09-25T10:01:00Z" {
			t.Fatalf("after synchronize: %v", r)
		}
		if f.client.Exists(f.ctx, "pr:nova-tools:8").Val() != 0 {
			t.Fatal("a PR with no record got one: the webhook only updates our records")
		}
		// An older delivery (a late redelivery) changes nothing.
		f.pr(t, "synchronize", 7, headA, "2026-09-25T09:00:00Z", "")
		if got := f.pass(t); got != (ghingest.Counts{Kept: 1}) {
			t.Fatalf("older entry: %s", got.Line())
		}
		if f.record(t, 7)["head"] != headB {
			t.Fatal("an older entry rewound the head")
		}
		// closed and merged: state merged at the merge commit.
		f.pr(t, "closed", 7, headB, "2026-09-25T10:05:00Z", `,"state":"closed","merged":true,"merge_commit_sha":"`+merge+`"`)
		if got := f.pass(t); got != (ghingest.Counts{Applied: 1}) {
			t.Fatalf("merged: %s", got.Line())
		}
		r = f.record(t, 7)
		if r["state"] != "merged" || r["merge_sha"] != merge || r["gh_state"] != "merged" || r["head"] != headB {
			t.Fatalf("after merge: %v", r)
		}
		f.settled(t)
	})

	t.Run("closed-unmerged-and-landed-stands", func(t *testing.T) {
		f := newFixture(t)
		f.client.HSet(f.ctx, "pr:nova-tools:9", "repo", repo, "n", "9", "state", "open", "head", headA)
		f.client.HSet(f.ctx, "pr:nova-tools:10", "repo", repo, "n", "10", "state", "landed", "head", headA)
		closed := `,"state":"closed","merged":false`
		f.pr(t, "closed", 9, headA, "2026-09-25T10:00:00Z", closed)
		f.pr(t, "closed", 10, headA, "2026-09-25T10:00:00Z", closed)
		f.pass(t)
		if s := f.record(t, 9)["state"]; s != "closed" {
			t.Fatalf("PR 9 closed unmerged: state %q, want closed", s)
		}
		if r := f.record(t, 10); r["state"] != "landed" || r["gh_state"] != "closed" {
			t.Fatalf("PR 10 landed by the stream lander then closed on GitHub: %v, want state landed", r)
		}
		f.pr(t, "reopened", 9, headA, "2026-09-25T10:02:00Z", `,"state":"open"`)
		f.pass(t)
		if s := f.record(t, 9)["state"]; s != "open" {
			t.Fatalf("PR 9 reopened: state %q, want open", s)
		}
		f.settled(t)
	})

	t.Run("issue-closed-by-lander-lands-the-card", func(t *testing.T) {
		f := newFixture(t)
		// The URL form of an origin (any host; the path names the issue).
		id := f.card(t, "c1", "https://example.com/"+repo+"/issues/2657")
		if got := f.client.ZRange(f.ctx, "issue:nova-tools:2657:cards", 0, -1).Val(); len(got) != 1 || got[0] != id {
			t.Fatalf("issue index after push: %v, want [%s]", got, id)
		}
		f.issueClosed(t, 2657, lander, "completed")
		if got := f.pass(t); got != (ghingest.Counts{Applied: 1, Landed: 1}) {
			t.Fatalf("lander close: %s", got.Line())
		}
		if w := f.where(t, id); w != "landed/done/ok" {
			t.Fatalf("card after the lander's close: %s, want landed/done/ok", w)
		}
		if f.client.ZScore(f.ctx, "ws:redis:done", id).Err() != nil {
			t.Fatal("landed card is not in ws:redis:done: the move did not move its view")
		}
		// A redelivered close finds it already landed and moves nothing.
		f.issueClosed(t, 2657, lander, "completed")
		if got := f.pass(t); got != (ghingest.Counts{Kept: 1}) {
			t.Fatalf("second close: %s", got.Line())
		}
		if fsck := f.client.FCall(f.ctx, "ns_card_fsck", nil, sprint).Val(); fmt.Sprint(fsck) == "" ||
			fmt.Sprint(fsck.([]any)[11]) != "0" {
			t.Fatalf("card fsck after landing: %v, want drift 0", fsck)
		}
		f.settled(t)
	})

	t.Run("issue-closed-by-anyone-else-is-a-finding", func(t *testing.T) {
		f := newFixture(t)
		id := f.card(t, "c2", repo+"#3000")
		f.issueClosed(t, 3000, "a-human", "completed")
		f.issueClosed(t, 3000, lander, "not_planned")
		f.issueClosed(t, 3001, lander, "completed") // no card came from 3001
		if got := f.pass(t); got != (ghingest.Counts{Findings: 3}) {
			t.Fatalf("closes that land nothing: %s", got.Line())
		}
		if w := f.where(t, id); w != "queued/ready/-" {
			t.Fatalf("card after a non-lander close: %s, want it unmoved at queued/ready/-", w)
		}
		msgs := f.client.XRange(f.ctx, ghingest.Findings, "-", "+").Val()
		var reasons []string
		for _, m := range msgs {
			reasons = append(reasons, fmt.Sprint(m.Values["reason"]))
		}
		if strings.Join(reasons, ",") != "not-lander,not-completed,no-card" {
			t.Fatalf("findings %v, want not-lander,not-completed,no-card", reasons)
		}
		f.settled(t)
	})

	t.Run("backfill-and-reclaim", func(t *testing.T) {
		f := newFixture(t)
		id := f.card(t, "c3", "nova-tools#77")
		// A card created before the index existed: Start backfills it.
		f.client.Del(f.ctx, "issue:nova-tools:77:cards")
		if n, err := f.c.Start(f.ctx, sprint); err != nil || n != 1 {
			t.Fatalf("Start backfill: %d %v, want 1", n, err)
		}
		// Consumer a reads the close and dies before its FCALL; b reclaims it.
		f.issueClosed(t, 77, lander, "")
		if _, err := f.client.XReadGroup(f.ctx, &redis.XReadGroupArgs{Group: ghingest.Group, Consumer: "seat-dead",
			Streams: []string{ghevent.Stream, ">"}, Count: 10, Block: -1}).Result(); err != nil {
			t.Fatal(err)
		}
		b := &ghingest.Consumer{Client: f.client, Name: "seat-b", Landers: []string{lander}, Block: -1, MinIdle: 50 * time.Millisecond}
		var got ghingest.Counts
		for deadline := time.Now().Add(testWait()); got.Reclaimed == 0 && time.Now().Before(deadline); {
			time.Sleep(10 * time.Millisecond)
			n, err := b.Pass(f.ctx)
			if err != nil {
				t.Fatal(err)
			}
			got = n
		}
		if got != (ghingest.Counts{Applied: 1, Landed: 1, Reclaimed: 1}) {
			t.Fatalf("reclaim pass: %s, want applied=1 landed=1 reclaimed=1", got.Line())
		}
		if w := f.where(t, id); w != "landed/done/ok" {
			t.Fatalf("card after reclaim: %s", w)
		}
		f.settled(t)
	})

	t.Run("no-lander-refused", func(t *testing.T) {
		f := newFixture(t)
		c := &ghingest.Consumer{Client: f.client, Name: "x"}
		if _, err := c.Pass(f.ctx); err == nil {
			t.Fatal("a consumer with no lander login ran")
		}
	})
}

// testWait is the poll bound: NOVA_TEST_WAIT, default 30 s.
func testWait() time.Duration {
	if d, err := time.ParseDuration(os.Getenv("NOVA_TEST_WAIT")); err == nil && d > 0 {
		return d
	}
	return 30 * time.Second
}
