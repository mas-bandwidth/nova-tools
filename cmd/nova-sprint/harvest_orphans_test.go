package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// seedVerbOrphan writes one orphan-effect card in the shape ns_card_required
// leaves (state orphan-effect, the orphan index, the unresolved item) and
// returns its identity.
func seedVerbOrphan(t *testing.T, c *redis.Client, S, bench, label, token string) card.Identity {
	t.Helper()
	ctx := context.Background()
	id := card.Identity{Sprint: S, Label: label, BaseSHA: "09fbedc9", Bench: bench, Attempt: 1}
	branch := "nova/" + S + "/" + label + "-a1"
	now, err := c.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	pipe := c.TxPipeline()
	pipe.HSet(ctx, card.CardKey(S, label),
		"kind", "code", "repo", "nova-tools", "base", "dev", "base_sha", id.BaseSHA,
		"state", "orphan-effect", "reason", "orphan-effect", "bench", bench,
		"attempt", "1", "identity", id.String(), "token_sha", card.TokenSHA(token),
		"orphan_evidence", "branch="+branch, "orphan_at", fmt.Sprint(now.UnixMilli()))
	pipe.SAdd(ctx, card.IdxKey(S, "orphan-effect"), label)
	pipe.HSet(ctx, "s:"+S+":unresolved", label+":orphan-effect:1", id.String()+" branch="+branch)
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestHarvestOrphansVerbRunsOneSweep (#3796): `card harvest --orphans
// --sprint S` runs harvest.RunOrphans once and prints one receipt. One orphan
// has its own end record under --results and ends; the other has none and
// waits.
func TestHarvestOrphansVerbRunsOneSweep(t *testing.T) {
	t.Setenv(store.UserEnv, "")
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	const S, bench = "orphans-3796", "orph-b1"
	pipe := c.TxPipeline()
	pipe.SAdd(ctx, "sprints", S)
	pipe.HSet(ctx, "s:"+S, "status", "open")
	pipe.SAdd(ctx, "benches", bench)
	pipe.HSet(ctx, "bench:"+bench+":beat", "host", bench+".fixture", "user", "nova", "at", "1")
	pipe.HSet(ctx, "bench:"+bench+":state", "state", "UP", "at", "1")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	const token = "attempt-1-token-ends"
	id := seedVerbOrphan(t, c, S, bench, "ends", token)
	seedVerbOrphan(t, c, S, bench, "waits", "attempt-1-token-waits")
	root := t.TempDir()
	dir := filepath.Join(root, filepath.FromSlash(id.String()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := card.WriteEndRecord(dir, card.EndRecord{Identity: id, Outcome: "FAILED", Reason: "tests-red", ExitCode: 1,
		TokenSHA: card.TokenSHA(token), PushedSHA: "-", At: "2026-09-25T12:00:00Z"}); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runSprint("card", "harvest", "--orphans", "--redis", addr, "--sprint", S, "--results", root)
	if code != 0 {
		t.Fatalf("exit %d, want 0; stdout %q stderr %q", code, stdout, stderr)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout has %d lines, want one receipt: %q", len(lines), stdout)
	}
	for _, want := range []string{"HARVEST-ORPHANS OK", "sprint=" + S, "benches=1", "ended=1", "superseded=0", "waiting=1", "failed=0"} {
		if !strings.Contains(lines[0], want) {
			t.Fatalf("receipt %q lacks %q", lines[0], want)
		}
	}
	if st, _ := c.HGet(ctx, card.CardKey(S, "ends"), "state").Result(); st != "ended" {
		t.Fatalf("orphan with its record: state %q, want ended", st)
	}
	if st, _ := c.HGet(ctx, card.CardKey(S, "waits"), "state").Result(); st != "orphan-effect" {
		t.Fatalf("orphan with no record: state %q, want orphan-effect", st)
	}
}

// TestHarvestOrphansVerbUsage: --orphans is one pass over one sprint; the
// usage refusals come before the store is opened.
func TestHarvestOrphansVerbUsage(t *testing.T) {
	t.Setenv(store.UserEnv, "")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--orphans"}, "--orphans wants one --sprint <S>"},
		{[]string{"--orphans", "--sprint", "all"}, "--orphans wants one --sprint <S>"},
		{[]string{"--orphans", "--sprint", "s", "--loop"}, "--orphans is one pass"},
		{[]string{"--orphans", "--sprint", "s", "lbl"}, "--orphans takes no labels"},
		{[]string{"--orphans", "--sprint", "s", "--results", "rel/dir"}, "--results wants an absolute dir"},
		{[]string{"--once", "--sprint", "s", "--results", "/abs"}, "--results goes with --orphans"},
	} {
		args := append([]string{"card", "harvest", "--redis", "127.0.0.1:1"}, tc.args...)
		code, stdout, stderr := runSprint(args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, tc.want) {
			t.Fatalf("%v: exit %d stdout %q stderr %q, want exit 2 naming %q", tc.args, code, stdout, stderr, tc.want)
		}
	}
}
