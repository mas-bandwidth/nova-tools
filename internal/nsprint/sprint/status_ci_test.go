package sprint_test

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/civerdict"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

const (
	fxSprint = "control-3046c1c1"
	fxRepo   = "nova-tools"
	fxBase   = "2222222222222222222222222222222222222222"
)

func full(prefix string) string { return prefix + strings.Repeat("0", 40-len(prefix)) }

// ciHead writes one ci card and its verdict record in the GID shape
// internal/nsprint/fn/lua/ci.lua writes: the card hash under
// s:<S>:card:ci-<pr>-<sha8> in its state index, and the receipt
// ci:<repo>:<head>:<gid> in ci:<repo>:<head>:gids. verdict "" leaves the
// record without a verdict field (MISSING).
func ciHead(t *testing.T, c *redis.Client, pr, head, cardBase, recBase, verdict string, attempt int) {
	t.Helper()
	ctx := context.Background()
	label := "ci-" + pr + "-" + head[:8]
	card := map[string]any{
		"kind": "script", "state": "ended", "outcome": "DONE", "reason": "done",
		"ci_for":  fxRepo + " " + pr + " " + head + " " + cardBase,
		"ci_repo": fxRepo, "ci_pr": pr, "ci_head": head, "base": cardBase,
		"attempt": attempt, "reruns": attempt - 1,
	}
	gid := civerdict.GID("single", recBase, fxBase, "req1", "pol1", "run1")
	rec := map[string]any{"attempt": attempt, "card": fxSprint + "/" + label,
		"head": head, "base": recBase, "base_sha": fxBase, "gid": gid, "pr": pr, "repo": fxRepo}
	if verdict != "" {
		rec["verdict"] = verdict
	}
	for _, err := range []error{
		c.HSet(ctx, "s:"+fxSprint+":card:"+label, card).Err(),
		c.SAdd(ctx, "s:"+fxSprint+":idx:card:ended", label).Err(),
		c.HSet(ctx, civerdict.Key(fxRepo, head, gid), rec).Err(),
		c.SAdd(ctx, civerdict.GIDsKey(fxRepo, head), gid).Err(),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func seedStatus(t *testing.T) (*redis.Client, *store.Store) {
	t.Helper()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	c.HSet(ctx, "s:"+fxSprint, "status", "open")
	c.HSet(ctx, civerdict.PolicyKey(fxRepo, fxBase), "policy_id", "pol1", "required_set_id", "req1", "runner_id", "run1")
	c.HSet(ctx, civerdict.TipKey(fxRepo, fxBase), "sha", fxBase)
	// Model cards in the same indexes are not ci verdicts.
	c.HSet(ctx, "s:"+fxSprint+":card:m1", "kind", "model", "state", "landed", "repo", fxRepo, "pr", "101")
	c.SAdd(ctx, "s:"+fxSprint+":idx:card:landed", "m1")
	c.HSet(ctx, "s:"+fxSprint+":card:m2", "kind", "model", "state", "ended", "outcome", "DONE")
	c.SAdd(ctx, "s:"+fxSprint+":idx:card:ended", "m2")

	// Three required heads: one OK first time; one whose first attempt
	// crashed (no verdict, MISSING) and whose one rerun came back OK, so the
	// card is on attempt 2; one FAIL.
	ciHead(t, c, "101", full("aaaa1111"), fxBase, fxBase, "OK", 1)
	ciHead(t, c, "102", full("aaaa2222"), fxBase, fxBase, "OK", 2)
	ciHead(t, c, "103", full("aaaa3333"), fxBase, fxBase, "FAIL", 1)
	// The attempts are receipts in the log; the ci x/y never reads them.
	for i := 0; i < 5; i++ {
		c.XAdd(ctx, &redis.XAddArgs{Stream: "s:" + fxSprint + ":log", Values: []string{"kind", "ci end", "id", "ci-102-aaaa2222"}})
	}
	return c, store.New(c)
}

// #2756 10.4 item 5: `sprint status` prints `ci a/b` beside the work line,
// a = unique required head-and-base verdicts OK, b = required; a rerun is
// another attempt of the same head, never an extra done.
func TestStatusPrintsCIxy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("three-heads-one-rerun-one-fail", func(t *testing.T) {
		_, st := seedStatus(t)
		xy, err := sprint.ReadCIxy(ctx, st, fxSprint)
		if err != nil {
			t.Fatal(err)
		}
		if got := xy.String(); got != "ci 2/3" {
			t.Fatalf("ci line %q, want %q (%+v)", got, "ci 2/3", xy)
		}
		if line := sprint.WithCI("12/40 30% -> eta 21:40", xy); line != "12/40 30% -> eta 21:40 | ci 2/3" {
			t.Fatalf("status line %q: the ci cell goes beside the work line", line)
		}
	})

	t.Run("flaky-pending-missing-and-another-base-are-not-ok", func(t *testing.T) {
		c, st := seedStatus(t)
		ciHead(t, c, "104", full("bbbb4444"), fxBase, fxBase, "FLAKY", 2)
		ciHead(t, c, "105", full("bbbb5555"), fxBase, fxBase, "PENDING", 1)
		ciHead(t, c, "106", full("bbbb6666"), fxBase, fxBase, "", 1)
		// The record's OK was for this head on another base: not this pair.
		ciHead(t, c, "107", full("bbbb7777"), fxBase, full("3333"), "OK", 1)
		xy, err := sprint.ReadCIxy(ctx, st, fxSprint)
		if err != nil {
			t.Fatal(err)
		}
		if got := xy.String(); got != "ci 2/7" {
			t.Fatalf("ci line %q, want ci 2/7 (%+v)", got, xy)
		}
	})

	t.Run("one-head-cut-for-two-prs-counts-once", func(t *testing.T) {
		c, st := seedStatus(t)
		// The same head and base under a second label is one required verdict.
		ctx := context.Background()
		head := full("aaaa1111")
		c.HSet(ctx, "s:"+fxSprint+":card:ci-201-aaaa1111", "kind", "script", "state", "ended",
			"ci_repo", fxRepo, "ci_pr", "201", "ci_head", head, "base", fxBase, "attempt", "1")
		c.SAdd(ctx, "s:"+fxSprint+":idx:card:ended", "ci-201-aaaa1111")
		xy, err := sprint.ReadCIxy(ctx, st, fxSprint)
		if err != nil {
			t.Fatal(err)
		}
		if got := xy.String(); got != "ci 2/3" {
			t.Fatalf("ci line %q, want ci 2/3 (%+v)", got, xy)
		}
	})

	t.Run("no-ci-cards", func(t *testing.T) {
		mr := miniredis.RunT(t)
		c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
		defer c.Close()
		xy, err := sprint.ReadCIxy(ctx, store.New(c), fxSprint)
		if err != nil || xy.String() != "ci 0/0" {
			t.Fatalf("empty sprint: %q %v, want ci 0/0", xy.String(), err)
		}
	})
}
