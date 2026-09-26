//go:build functional

package webhook_test

// The DONE-WHEN of card gh-ci-receipts against a throwaway redis-server with
// the nova_sprint library loaded (miniredis has no FCALL): one receipt from
// the runner writes the record the receiver path writes, and `land pr` on
// that head merges without a webhook and without polling. Nothing here dials
// a bench or GitHub: the GitHub is an httptest fake that refuses every
// check-state read.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/webhook"
	"github.com/redis/go-redis/v9"
)

func runnerStore(t *testing.T) (context.Context, *redis.Client) {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatalf("load nova_sprint library: %v", err)
	}
	return ctx, client
}

func TestRunnerReceiptWritesWhatTheReceiverWould(t *testing.T) {
	t.Parallel()

	ctx, c := runnerStore(t)
	r := goodReceipt()
	w, err := webhook.Write(ctx, c, r)
	if err != nil {
		t.Fatal(err)
	}
	key := webhook.Key(r.Repo, r.SHA)
	if w.Key != key || w.Word != webhook.Green || w.Fail != "" || w.Runs != 3 || w.Applied != 3 || w.EntryID == "" {
		t.Fatalf("written: %+v", w)
	}

	// The record: the same fields ns_ci_github writes from a delivery, plus the source stamp.
	rec, err := webhook.Read(ctx, c, r.Repo, r.SHA)
	if err != nil {
		t.Fatal(err)
	}
	if !rec.Found || rec.Word != webhook.Green || rec.PR != "4350" || rec.At != r.At || rec.EvID != w.EntryID || rec.Source != webhook.SourceRunner {
		t.Fatalf("record: %+v", rec)
	}
	if len(rec.Runs) != 3 || rec.Runs[0] != (webhook.Run{Kind: "check", Name: "lint", Word: "green", ID: r.RunID, At: r.At}) ||
		rec.Runs[1] != (webhook.Run{Kind: "check", Name: "lisp", Word: "green", ID: r.RunID, At: r.At}) ||
		rec.Runs[2] != (webhook.Run{Kind: "wf", Name: "ci", Word: "green", ID: r.RunID, At: r.At}) {
		t.Fatalf("runs: %+v", rec.Runs)
	}
	if got := webhook.Source(rec); got != webhook.SourceRunner {
		t.Fatalf("Source = %q", got)
	}

	// The ev:github row: the workflow_run key set the receiver appends, sender runner.
	msgs, err := c.XRange(ctx, ghevent.Stream, "-", "+").Result()
	if err != nil || len(msgs) != 1 {
		t.Fatalf("ev:github: %d rows, %v", len(msgs), err)
	}
	want, _ := ghevent.Fields(ghevent.Entry{Repo: r.Repo, Kind: "workflow_run", Number: "4350", Head: r.SHA, Action: "completed",
		At: r.At, Sender: webhook.Sender, RunID: r.RunID, Workflow: "ci", Status: "completed", Conclusion: "success"})
	if len(msgs[0].Values) != len(want) {
		t.Fatalf("row keys: %v, want %v", msgs[0].Values, want)
	}
	for k, v := range want {
		if got := msgs[0].Values[k]; got != v {
			t.Errorf("row %s = %v, want %v", k, got, v)
		}
	}

	// The consumer, if one runs, sees the row and never fights the record.
	cons := &webhook.Consumer{Client: c, Name: "seat-a", Block: -1}
	if err := cons.Start(ctx); err != nil {
		t.Fatal(err)
	}
	n, err := cons.Pass(ctx)
	if err != nil || n.Applied+n.Kept != 1 || n.Skipped != 0 {
		t.Fatalf("consumer pass: %+v %v", n, err)
	}
	after, _ := webhook.Read(ctx, c, r.Repo, r.SHA)
	if after.Word != webhook.Green || after.Source != webhook.SourceRunner || len(after.Runs) != 3 {
		t.Fatalf("after the consumer: %+v", after)
	}

	// No other key: the PR record is the lander's (its stream, its head and
	// its ci_request go through land_stream.lua's record op), never the
	// runner's, and an older run's receipt at the same head is KEPT.
	if n, _ := c.Exists(ctx, "pr:nova-tools:4350").Result(); n != 0 {
		t.Fatal("the runner wrote a PR record")
	}
	keys, _ := c.Keys(ctx, "*").Result()
	if len(keys) != 2 {
		t.Fatalf("keys after a receipt: %v, want the record and the stream only", keys)
	}
	older := goodReceipt()
	older.RunID, older.Conclusion, older.Jobs = "36300000000", "failure", []webhook.Job{{Name: "lint", Result: "failure"}}
	w2, err := webhook.Write(ctx, c, older)
	if err != nil {
		t.Fatal(err)
	}
	if w2.Applied != 0 || w2.Word != webhook.Green {
		t.Fatalf("an older run's receipt was applied over the newer: %+v", w2)
	}
}

func TestRunnerReceiptRedNamesTheFirstRedJob(t *testing.T) {
	t.Parallel()

	ctx, c := runnerStore(t)
	r := goodReceipt()
	r.PR = ""
	r.Conclusion = "failure"
	r.Jobs = []webhook.Job{{Name: "lint", Result: "success"}, {Name: "test", Result: "failure"}, {Name: "e2e", Result: "cancelled"}}
	w, err := webhook.Write(ctx, c, r)
	if err != nil {
		t.Fatal(err)
	}
	if w.Word != webhook.Red || w.Fail != "check:e2e" {
		t.Fatalf("written: %+v", w)
	}
}

// landPRFake is the smallest GitHub `land pr` calls: the PR read and its
// merge at the head. A check-runs or workflow-runs read fails the test.
func landPRFake(t *testing.T, head string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reply any
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/mas-bandwidth/nova-tools/pulls/4350":
			reply = map[string]any{"state": "open", "merged": false, "mergeable_state": "blocked", "title": "ci receipts", "head": map[string]any{"sha": head}}
		case r.Method == http.MethodPut && r.URL.Path == "/repos/mas-bandwidth/nova-tools/pulls/4350/merge":
			reply = map[string]any{"sha": strings.Repeat("e", 40), "merged": true}
		default:
			t.Errorf("land pr called %s %s: a check state read or a rerun", r.Method, r.URL)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(reply)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestLandPRMergesOnTheRunnerReceipt is the card's DONE-WHEN: with nothing
// recorded land pr is WAITING; after the runner's receipt it merges, with no
// webhook, no consumer and no poll.
func TestLandPRMergesOnTheRunnerReceipt(t *testing.T) {
	t.Parallel()

	ctx, c := runnerStore(t)
	r := goodReceipt()
	srv := landPRFake(t, r.SHA)
	gh := &stream.GitHub{API: srv.URL, Token: "t0k", Budget: 6}
	opts := stream.LandPROptions{Repo: r.Repo, N: 4350}

	rep, err := stream.LandPR(ctx, gh, c, opts)
	if err != nil || rep.State != "waiting" {
		t.Fatalf("before the receipt: %+v %v", rep, err)
	}
	if _, err := webhook.Write(ctx, c, r); err != nil {
		t.Fatal(err)
	}
	rep, err = stream.LandPR(ctx, gh, c, opts)
	if err != nil || rep.State != "merged" || rep.CI != webhook.Green || rep.MergeSHA != strings.Repeat("e", 40) {
		t.Fatalf("after the receipt: %+v %v", rep, err)
	}
	if gh.Calls != 3 {
		t.Fatalf("REST calls %d, want 3 (two reads, one merge; no check state read)", gh.Calls)
	}
}
