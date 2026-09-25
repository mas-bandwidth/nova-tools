package main

// The verb face of the GitHub leg (#3597): an ev:github check_run entry,
// `ci github --once` writes ci:<repo>:<sha>:gh, and `ci status --repo --sha`
// prints it from Redis with no request record (exit 5, MISSING for ours).

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ghevent"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func TestCIVerbGitHubOnceAndStatus(t *testing.T) {
	t.Parallel()

	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("c", 40)
	if _, err := ghevent.Publish(ctx, client, ghevent.Entry{Repo: "mas-bandwidth/fx", Kind: "check_run", Number: "7",
		Head: sha, Action: "completed", At: "2026-09-25T12:00:00Z", Check: "lint", CheckRunID: "41",
		Status: "completed", Conclusion: "failure"}); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runCI(ctx, []string{"github", "--redis", addr, "--consumer", "t1", "--once"}, &out, &errOut); code != 0 {
		t.Fatalf("github --once code=%d stderr=%q", code, errOut.String())
	}
	if got := strings.TrimSpace(out.String()); got != "CIGH applied=1 kept=0 skipped=0 reclaimed=0" {
		t.Fatalf("github --once: %q", got)
	}
	out.Reset()
	if code := runCI(ctx, []string{"status", "--redis", addr, "--repo", "fx", "--sha", sha}, &out, &errOut); code != 5 {
		t.Fatalf("status code=%d, want 5 (no request record)", code)
	}
	want := "ci:fx:" + sha + ":gh red fail=check:lint at=2026-09-25T12:00:00Z\n  check:lint red id=41 at=2026-09-25T12:00:00Z\n"
	if !strings.HasSuffix(out.String(), want) {
		t.Fatalf("status: %q, want suffix %q", out.String(), want)
	}
	if code := runCI(ctx, []string{"github", "--redis", addr, "extra"}, &out, &errOut); code != 2 {
		t.Fatalf("github with a positional code=%d, want 2", code)
	}
}
