package gh

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// TestCachedRefReadsTheCopyFirst (#4343 BUILD 4): the first read of a
// reference asks GitHub (a PR, or an issue behind a 404) and writes the
// copy; every read after answers from the copy with no call; an
// invalidated copy costs one more call.
func TestCachedRefReadsTheCopyFirst(t *testing.T) {
	t.Parallel()
	rdb := newStore(t)
	ck := &clock{t: t0}
	f := newForge(t,
		ok("", `{"merged":false,"state":"open","base":{"ref":"dev"}}`),
		func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		},
		ok("", `{"state":"closed"}`),
		ok("", `{"merged":true,"state":"closed","base":{"ref":"dev"}}`),
	)
	c, _ := newClient(f, rdb, ck, "reconcile")
	ctx := context.Background()
	r, cached, err := c.CachedRef(ctx, "o/r", 1)
	if err != nil || cached || !r.IsPR || r.Merged || r.State != "open" || r.Base != "dev" {
		t.Fatalf("first read: %+v cached=%v err=%v", r, cached, err)
	}
	r, cached, err = c.CachedRef(ctx, "o/r", 1)
	if err != nil || !cached || r.State != "open" || c.Calls != 1 {
		t.Fatalf("second read: %+v cached=%v calls=%d err=%v", r, cached, c.Calls, err)
	}
	// An issue: the 404 on pulls, then the issue read; terminal, so cached.
	r, cached, err = c.CachedRef(ctx, "o/r", 2)
	if err != nil || cached || r.IsPR || r.State != "closed" || !r.Terminal() || c.Calls != 3 {
		t.Fatalf("issue: %+v cached=%v calls=%d err=%v", r, cached, c.Calls, err)
	}
	// The webhook saw the PR close: the copy goes, one call re-reads it.
	if err := InvalidateRef(ctx, rdb, "o/r", 1); err != nil {
		t.Fatal(err)
	}
	r, cached, err = c.CachedRef(ctx, "o/r", 1)
	if err != nil || cached || !r.Merged || c.Calls != 4 {
		t.Fatalf("after invalidate: %+v cached=%v calls=%d err=%v", r, cached, c.Calls, err)
	}
	got, found, err := ReadRef(ctx, rdb, "o/r", 1)
	if err != nil || !found || !got.Merged || got.At != strconv.FormatInt(t0.Unix(), 10) {
		t.Fatalf("copy %+v found=%v err=%v", got, found, err)
	}
	if err := WriteRef(ctx, rdb, "o/r", 3, Ref{State: "closed"}, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if r, cached, _ := c.CachedRef(ctx, "o/r", 3); !cached || r.IsPR || r.State != "closed" || c.Calls != 4 {
		t.Fatalf("a copy the webhook wrote answers without a call: %+v cached=%v calls=%d", r, cached, c.Calls)
	}
}
