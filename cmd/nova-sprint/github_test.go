package main

// The verb face of the ingest consumer (#2657): a pull_request entry on
// ev:github and `github ingest --once` moves our PR record's head; no
// --lander is usage (exit 2), as is a positional.

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

func TestGitHubIngestVerbOnce(t *testing.T) {
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	old, head := strings.Repeat("1", 40), strings.Repeat("2", 40)
	client.HSet(ctx, "pr:fx:7", "repo", "mas-bandwidth/fx", "n", "7", "state", "open", "head", old)
	if _, err := ghevent.Publish(ctx, client, ghevent.Entry{Repo: "mas-bandwidth/fx", Kind: "pull_request", Number: "7",
		Head: head, Action: "synchronize", At: "2026-09-25T12:00:00Z", Sender: "someone"}); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runGitHub(ctx, []string{"ingest", "--redis", addr, "--lander", "rowan", "--consumer", "t1", "--once"}, &out, &errOut); code != 0 {
		t.Fatalf("ingest --once code=%d stderr=%q", code, errOut.String())
	}
	if got := strings.TrimSpace(out.String()); got != "INGEST applied=1 kept=0 unknown=0 landed=0 findings=0 skipped=0 reclaimed=0" {
		t.Fatalf("ingest --once: %q", got)
	}
	if got := client.HGet(ctx, "pr:fx:7", "head").Val(); got != head {
		t.Fatalf("pr:fx:7 head = %q, want %q", got, head)
	}
	if code := runGitHub(ctx, []string{"ingest", "--redis", addr, "--once"}, &out, &errOut); code != 2 {
		t.Fatalf("ingest with no --lander code=%d, want 2", code)
	}
	if code := runGitHub(ctx, []string{"ingest", "--redis", addr, "--lander", "rowan", "extra"}, &out, &errOut); code != 2 {
		t.Fatalf("ingest with a positional code=%d, want 2", code)
	}
}
