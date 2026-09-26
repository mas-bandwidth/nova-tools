//go:build functional

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// TestPRReapVerb: pr reap refuses usage with exit 2 and one stderr line; on
// a sprint with one card PR whose branch is gone (marked by pr record
// --branch-gone) --dry-run prints the decision and calls nothing, and a run
// closes it with one PATCH and prints the PR REAP receipt.
func TestPRReapVerb(t *testing.T) {
	for _, args := range [][]string{
		{"pr", "reap"},
		{"pr", "reap", "--sprint", "s", "--budget", "0", "--redis", "127.0.0.1:1"},
		{"pr", "reap", "--sprint", "s", "extra", "--redis", "127.0.0.1:1"},
	} {
		if code, _, errOut := runSprint(args...); code != 2 || strings.Count(errOut, "\n") != 1 {
			t.Errorf("%v: exit %d, stderr %q", args, code, errOut)
		}
	}
	addr := testutil.Start(t)
	ctx := context.Background()
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	const S = "reap-verb"
	head := strings.Repeat("a", 40)
	if code, out, errOut := runSprint("pr", "record", "--redis", addr, "--repo", lsRepo, "--n", "77", "--head", head,
		"--base", "dev", "--stream", "landing", "--branch-gone", "fetch:404"); code != 0 {
		t.Fatalf("pr record: %d %s %s", code, out, errOut)
	}
	if err := c.HSet(ctx, "s:"+S+":prcard", lsRepo+"#77", "card-v").Err(); err != nil {
		t.Fatal(err)
	}
	var patches, reads atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/repos/"+lsRepo+"/pulls/77" {
			reads.Add(1)
			http.Error(w, `{"message":"no"}`, http.StatusForbidden)
			return
		}
		patches.Add(1)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	prev := landStreamToken
	landStreamToken = func() (string, error) { return "test-token", nil }
	t.Cleanup(func() { landStreamToken = prev })

	code, out, errOut := runSprint("pr", "reap", "--redis", addr, "--sprint", S, "--dry-run", "--api", srv.URL)
	if code != 0 || !strings.Contains(out, "REAP "+lsRepo+"#77 rule=branch-gone") || !strings.Contains(out, "dry_run=true") || patches.Load() != 0 {
		t.Fatalf("dry run: %d %q %q patches=%d", code, out, errOut, patches.Load())
	}
	code, out, errOut = runSprint("pr", "reap", "--redis", addr, "--sprint", S, "--api", srv.URL)
	if code != 0 || patches.Load() != 1 || reads.Load() != 0 || !strings.Contains(out, "close=done") ||
		!strings.Contains(out, "PR REAP sprint="+S+" prs=1 reap=1 closed=1") {
		t.Fatalf("run: %d %q %q patches=%d reads=%d", code, out, errOut, patches.Load(), reads.Load())
	}
	if m := c.HGetAll(ctx, "pr:nova-tools:77").Val(); m["state"] != "closed" || m["reap"] != "branch-gone" || m["branch_gone"] != "fetch:404" {
		t.Fatalf("record: %v", m)
	}
}
