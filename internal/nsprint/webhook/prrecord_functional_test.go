//go:build functional

package webhook_test

// The DONE-WHEN of card pr-record-follows-github on a throwaway
// redis-server with the nova_sprint library loaded: a pull_request opened
// delivery creates the PR record, a synchronize moves its head and the head
// index follows, the runner's receipt at that head writes ci=green onto the
// record, and a merged delivery with a land pr on a fake GitHub lands the
// card the record names, in the same verb, through a pit stop.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/webhook"
	"github.com/redis/go-redis/v9"
)

// deliverPR appends the webhook fixture (internal/ghevent/testdata) as PR
// 42 of branch rowan/prfollow with action and head, through the receiver's
// own Accept.
func deliverPR(t *testing.T, ctx context.Context, c *redis.Client, action, head string, merged bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "ghevent", "testdata", "pull_request.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	p["action"] = action
	pr := p["pull_request"].(map[string]any)
	pr["head"] = map[string]any{"sha": head, "ref": "rowan/prfollow"}
	pr["base"] = map[string]any{"ref": "dev"}
	pr["merged"] = merged
	raw, _ = json.Marshal(p)
	if _, err := ghevent.Accept(ctx, c, "pull_request", raw); err != nil {
		t.Fatal(err)
	}
}

func TestPRRecordFollowsGitHubAndLandPRLandsTheCard(t *testing.T) {
	t.Parallel()

	ctx, c := runnerStore(t)
	const key = "pr:nova-tools:42"
	a, b, merge := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("e", 40)

	// The card: pushed, taken and done with PR 42, so it waits in merging.
	c.SAdd(ctx, "friends", "rowan")
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "prfollow", Stream: "github", Friend: "rowan", Sprint: "S1",
		Kind: "build", Title: "pr record follows github", PR: "42", Repo: "mas-bandwidth/nova-tools", By: "rowan"}); err != nil {
		t.Fatal(err)
	}
	if _, err := taskcard.Take(ctx, c, "rowan", 1, "rowan"); err != nil {
		t.Fatal(err)
	}
	if m, err := taskcard.Done(ctx, c, "prfollow", "rowan", "PR #42 opened", "42"); err != nil || m.To != "merging" {
		t.Fatalf("done: %+v %v", m, err)
	}

	cons := &webhook.Consumer{Client: c, Name: "seat-a", Block: -1}
	if err := cons.Start(ctx); err != nil {
		t.Fatal(err)
	}
	pass := func(want int) {
		t.Helper()
		n, err := cons.Pass(ctx)
		if err != nil || n.Records != want || n.Failed != 0 {
			t.Fatalf("pass: %s %v, want records=%d", n.Line(), err, want)
		}
	}

	// opened: the record exists, its stream and task from the branch's card.
	deliverPR(t, ctx, c, "opened", a, false)
	pass(1)
	rec := c.HGetAll(ctx, key).Val()
	if rec["head"] != a || rec["state"] != "open" || rec["stream"] != "github" || rec["task"] != "prfollow" ||
		rec["branch"] != "rowan/prfollow" || rec["base"] != "dev" || rec["gh_head"] != a || rec["gh_src"] != "delivery" {
		t.Fatalf("opened: %v", rec)
	}
	if !c.SIsMember(ctx, prkey.HeadKey("nova-tools", a), "42").Val() {
		t.Fatal("opened: the head index does not name 42")
	}

	// synchronize: the head moves and the index follows.
	deliverPR(t, ctx, c, "synchronize", b, false)
	pass(1)
	rec = c.HGetAll(ctx, key).Val()
	if rec["head"] != b || rec["head_prev"] != a || rec["ci"] != "pending" || rec["gh_head"] != b {
		t.Fatalf("synchronize: %v", rec)
	}
	if c.SIsMember(ctx, prkey.HeadKey("nova-tools", a), "42").Val() || !c.SIsMember(ctx, prkey.HeadKey("nova-tools", b), "42").Val() {
		t.Fatal("synchronize: the head index did not follow the head")
	}
	if err := land.StaleHead(key, rec); err != nil {
		t.Fatalf("a record at the delivered head is stale: %v", err)
	}

	// The runner's receipt at that head: ci=green on the record.
	r := goodReceipt()
	r.PR, r.SHA, r.HeadBranch = "42", b, "rowan/prfollow"
	w, err := webhook.Write(ctx, c, r)
	if err != nil {
		t.Fatal(err)
	}
	if w.PR.Outcome != "same" || strings.Join(w.Folded, ",") != "42" || w.FoldLine() != "CIGH FOLD ci:nova-tools:"+b+":gh ci=green prs=42" {
		t.Fatalf("receipt: %s | %s | %+v", w.PR.Line(), w.FoldLine(), w)
	}
	rec = c.HGetAll(ctx, key).Val()
	if rec["ci"] != "green" || rec["ci_sha"] != b || rec["gh_run_id"] != r.RunID {
		t.Fatalf("after the receipt: %v", rec)
	}
	// A cancelled older run at the old head neither rewinds nor reddens.
	old := goodReceipt()
	old.PR, old.SHA, old.RunID, old.Conclusion = "42", a, "36300000000", "cancelled"
	if w, err = webhook.Write(ctx, c, old); err != nil || w.PR.Key != "" || len(w.Folded) != 0 {
		t.Fatalf("cancelled: %+v %v", w, err)
	}
	if rec = c.HGetAll(ctx, key).Val(); rec["head"] != b || rec["ci"] != "green" {
		t.Fatalf("after a cancelled run: %v", rec)
	}

	// merged: the delivery marks the record; land pr lands the card, in a
	// pit stop (it records what GitHub already did).
	deliverPR(t, ctx, c, "closed", b, true)
	pass(0)
	if st := c.HGet(ctx, key, "state").Val(); st != "merged" {
		t.Fatalf("closed merged: state %q", st)
	}
	c.HSet(ctx, "s:S1:pitstop", "by", "rowan", "why", "a pit stop", "scope", "all")
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != "/repos/mas-bandwidth/nova-tools/pulls/42" {
			t.Errorf("land pr called %s %s", req.Method, req.URL)
			rw.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{"state": "closed", "merged": true, "merge_commit_sha": merge,
			"head": map[string]any{"sha": b}})
	}))
	t.Cleanup(srv.Close)
	gh := &stream.GitHub{API: srv.URL, Token: "t0k", Budget: 3}
	rep, err := stream.LandPR(ctx, gh, c, stream.LandPROptions{Repo: "mas-bandwidth/nova-tools", N: 42})
	if err != nil || rep.State != "merged" || rep.Record != "merged" || rep.Card != "prfollow" || rep.CardMove != "merging->landed" {
		t.Fatalf("land pr: %+v %v", rep, err)
	}
	task := c.HGetAll(ctx, "task:prfollow").Val()
	if task["where"] != "landed" || task["merge_sha"] != merge {
		t.Fatalf("the card after land pr: where=%s merge_sha=%s", task["where"], task["merge_sha"])
	}
	// Again: the card is already landed and nothing moves.
	if rep, err = stream.LandPR(ctx, gh, c, stream.LandPROptions{Repo: "mas-bandwidth/nova-tools", N: 42}); err != nil || rep.CardMove != "already landed" {
		t.Fatalf("land pr again: %+v %v", rep, err)
	}
}
