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
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/deal"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/harvest"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// seedOrphanEffectCard writes one orphan-effect card in the shape
// ns_card_required leaves (state orphan-effect, the orphan index, the
// unresolved item) and returns its identity.
func seedOrphanEffectCard(t *testing.T, c *redis.Client, S, bench, label, token string) card.Identity {
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

// TestReconcilerRunsHarvestOrphansDuty is #3820's DONE-WHEN: with the
// reconciler running one tick against a throwaway Redis holding an
// orphan-effect card whose own end record exists under the results root, the
// card is ended and its unresolved item settled with no verb typed by hand.
func TestReconcilerRunsHarvestOrphansDuty(t *testing.T) {
	t.Setenv(store.UserEnv, "")
	addr := startThrowawayRedis(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	const S, bench = "reconcile-orphans-3820", "orph-b1"
	pipe := c.TxPipeline()
	pipe.SAdd(ctx, "sprints", S)
	pipe.HSet(ctx, "s:"+S, "status", "open")
	pipe.SAdd(ctx, "benches", bench)
	pipe.HSet(ctx, "bench:"+bench+":beat", "host", bench+".fixture", "user", "nova", "at", "1")
	pipe.HSet(ctx, "bench:"+bench+":state", "state", "UP", "at", "1")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}

	const tokenEnds = "token-ends-3820"
	idEnds := seedOrphanEffectCard(t, c, S, bench, "card-ends", tokenEnds)
	seedOrphanEffectCard(t, c, S, bench, "card-waits", "token-waits-3820")

	root := t.TempDir()
	dir := filepath.Join(root, filepath.FromSlash(idEnds.String()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := card.WriteEndRecord(dir, card.EndRecord{
		Identity:  idEnds,
		Outcome:   "FAILED",
		Reason:    "tests-red",
		ExitCode:  1,
		TokenSHA:  card.TokenSHA(tokenEnds),
		PushedSHA: "-",
		At:        "2026-09-25T12:00:00Z",
	}); err != nil {
		t.Fatal(err)
	}

	dealSeams := reconcileSeams
	reconcileSeams = func() (deal.Dialer, deal.PRs) { return &verbSSH{}, verbForge{} }
	harvestSeams := consumeHarvestSeams
	forge := &consumeForge{prs: map[string]harvest.PR{}}
	pusher := &consumePusher{pushes: map[string]int{}}
	consumeHarvestSeams = func() (harvest.Forge, harvest.Pusher) { return forge, pusher }
	t.Cleanup(func() {
		reconcileSeams = dealSeams
		consumeHarvestSeams = harvestSeams
	})

	out := reconcileOnce(t, addr, "--results", root)
	if !strings.Contains(out, "DUTIES refill,ok-to-friend,harvest") {
		t.Fatalf("reconcile output lacks harvest duty:\n%s", out)
	}

	// card-ends had its end record: it is ended, removed from orphan-effect
	// index, and its unresolved item is settled.
	if st, _ := c.HGet(ctx, card.CardKey(S, "card-ends"), "state").Result(); st != "ended" {
		t.Fatalf("card-ends state %q, want ended", st)
	}
	if unres, _ := c.HGet(ctx, "s:"+S+":unresolved", "card-ends:orphan-effect:1").Result(); unres != "" {
		t.Fatalf("card-ends unresolved item %q still present, want settled", unres)
	}
	if isMem, _ := c.SIsMember(ctx, card.IdxKey(S, "orphan-effect"), "card-ends").Result(); isMem {
		t.Fatal("card-ends still in orphan-effect index, want removed")
	}

	// card-waits had no end record: it stays orphan-effect and its unresolved
	// item is preserved.
	if st, _ := c.HGet(ctx, card.CardKey(S, "card-waits"), "state").Result(); st != "orphan-effect" {
		t.Fatalf("card-waits state %q, want orphan-effect", st)
	}
	if unres, _ := c.HGet(ctx, "s:"+S+":unresolved", "card-waits:orphan-effect:1").Result(); unres == "" {
		t.Fatal("card-waits unresolved item missing, want preserved")
	}
	if isMem, _ := c.SIsMember(ctx, card.IdxKey(S, "orphan-effect"), "card-waits").Result(); !isMem {
		t.Fatal("card-waits missing from orphan-effect index")
	}
}
