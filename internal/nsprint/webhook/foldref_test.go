package webhook

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/gh"
	"github.com/redis/go-redis/v9"
)

// TestFoldRefKeepsTheCopyCurrent (#4343 BUILD 2, BUILD 4): an issues entry
// writes gh:ref with the issue's state; a pull_request closed or reopened
// entry drops the PR's copy; every other entry leaves the copy alone. All
// in the consumer's own pipeline, no GitHub call.
func TestFoldRefKeepsTheCopyCurrent(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := context.Background()
	if err := gh.WriteRef(ctx, rdb, "o/r", 5, gh.Ref{IsPR: true, State: "open", Base: "dev"}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := gh.WriteRef(ctx, rdb, "o/r", 6, gh.Ref{IsPR: true, State: "open", Base: "dev"}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := gh.WriteRef(ctx, rdb, "o/r", 7, gh.Ref{IsPR: true, State: "closed", Base: "dev"}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	pipe := rdb.Pipeline()
	for _, v := range []map[string]any{
		{"repo": "o/r", "kind": "issues", "number": "9", "action": "closed", "state": "closed", "at": "2026-09-26T16:00:00Z"},
		{"repo": "o/r", "kind": "pull_request", "number": "5", "action": "closed", "head": ""},
		{"repo": "o/r", "kind": "pull_request", "number": "6", "action": "synchronize", "head": ""},
		{"repo": "o/r", "kind": "pull_request", "number": "7", "action": "reopened", "head": ""},
		{"repo": "o/r", "kind": "issue_comment", "number": "6", "action": "created"},
		{"repo": "o/r", "kind": "issues", "number": "x", "action": "closed", "state": "closed"},
	} {
		foldRef(ctx, pipe, redis.XMessage{ID: "1-0", Values: v})
	}
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	r, ok, err := gh.ReadRef(ctx, rdb, "r", 9)
	if err != nil || !ok || r.IsPR || r.State != "closed" || r.At != "2026-09-26T16:00:00Z" {
		t.Fatalf("issue copy: %+v ok=%v err=%v", r, ok, err)
	}
	if _, ok, _ := gh.ReadRef(ctx, rdb, "o/r", 5); ok {
		t.Fatal("the closed PR's copy was not dropped")
	}
	if r, ok, _ := gh.ReadRef(ctx, rdb, "o/r", 6); !ok || r.State != "open" {
		t.Fatalf("the synchronized PR's copy moved: %+v ok=%v", r, ok)
	}
	if _, ok, _ := gh.ReadRef(ctx, rdb, "o/r", 7); ok {
		t.Fatal("the reopened PR's copy was not dropped")
	}
}
