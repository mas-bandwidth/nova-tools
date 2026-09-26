//go:build functional

package card_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/redis/go-redis/v9"
)

// poolLine renders the live table and returns its "pool: <n> undealt" line,
// or "" when the table prints none.
func poolLine(t *testing.T, ctx context.Context, client *redis.Client) string {
	t.Helper()
	snap, err := table.ReadLive(ctx, client, table.LiveConfig{Friends: []string{"rowan"}, Sprint: ndSprint})
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(snap.RenderLive(time.Now().UTC()), "\n") {
		if strings.HasPrefix(line, "pool:") {
			return line
		}
	}
	return ""
}

// TestTablePoolLineIsTheUndealtView is nova-tools#2733's DONE-WHEN: push five
// cards and deal three to a bench; the bench's row shows the three it holds
// and the table prints "pool: 2 undealt", the ZCARD of the undealt view
// bench:_pool:cards:ready, with no card-dealer bench:pool hash (retired) and
// no bench-row queue field in play. An empty pool prints "pool: 0 undealt"
// while the sprint has cards, never no line.
func TestTablePoolLineIsTheUndealtView(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	_, client := newSprint(t)
	srv := repoServer(t)
	repo := srv.URL + "/acme/public.git"
	ndSetup(t, ctx, client)
	if err := client.Del(ctx, "bench:pool").Err(); err != nil {
		t.Fatal(err)
	}

	labels := []string{"pl-a", "pl-b", "pl-c", "pl-d", "pl-e"}
	for _, l := range labels {
		ndPush(t, ctx, client, repo, l, "none", "pool")
	}
	if got := poolLine(t, ctx, client); got != "pool: 5 undealt" {
		t.Fatalf("after push: pool line %q, want %q", got, "pool: 5 undealt")
	}

	args := []any{ndBench, ndFence, "test", "2733"}
	for i, l := range labels[:3] {
		tok := attemptToken(1, fmt.Sprintf("%032x", i+1))
		args = append(args, ndSprint, l, "1", tok, card.TokenSHA(tok))
	}
	if reply, err := client.FCall(ctx, "ns_card_deal", nil, args...).StringSlice(); err != nil || len(reply) != 1+3*4 {
		t.Fatalf("deal = %v, %v", reply, err)
	}
	if got := poolLine(t, ctx, client); got != "pool: 2 undealt" {
		t.Fatalf("after dealing 3: pool line %q, want %q", got, "pool: 2 undealt")
	}
	cells := hostRow(t, ctx, client)
	held := int64(0)
	for i, w := range []string{"ready", "working"} {
		n := client.ZCard(ctx, card.BenchCardsKey(ndBench, w)).Val()
		if cells[i+1] != fmt.Sprint(n) {
			t.Fatalf("host row %s = %s, ZCARD = %d (row %v)", w, cells[i+1], n, cells)
		}
		held += n
	}
	if held != 3 {
		t.Fatalf("bench %s holds %d dealt cards, want 3 (row %v)", ndBench, held, cells)
	}

	// Drain the pool: the line stays, at 0.
	args = []any{ndBench, ndFence, "test", "2733"}
	for i, l := range labels[3:] {
		tok := attemptToken(1, fmt.Sprintf("%032x", i+4))
		args = append(args, ndSprint, l, "1", tok, card.TokenSHA(tok))
	}
	if reply, err := client.FCall(ctx, "ns_card_deal", nil, args...).StringSlice(); err != nil || len(reply) != 1+2*4 {
		t.Fatalf("deal = %v, %v", reply, err)
	}
	if got := poolLine(t, ctx, client); got != "pool: 0 undealt" {
		t.Fatalf("empty pool: pool line %q, want %q", got, "pool: 0 undealt")
	}
}
