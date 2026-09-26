//go:build functional

package stream

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// Stella's reproduction (her independent read of #4410 at 1b3c3cb73, bus
// stella-65f927837aa2), copied as she wrote it and renamed to credit her.
// Review acceptance: a later ready-to-merge card cannot silently pass an
// earlier live card of the same ordered stream. No Git or forge call is made.
func TestLandSelectionRespectsEarlierLiveMember_Stella(t *testing.T) {
	t.Parallel()
	for _, earlier := range []string{"waiting", "ready", "working", "review"} {
		t.Run(earlier, func(t *testing.T) {
			t.Parallel()
			c := newRedis(t)
			ctx := context.Background()
			head := strings.Repeat("b", 40)
			seed(t, c, 2, head, 200, score("rowan", head, 10))
			if err := c.HSet(ctx, "task:t2", "where", "merging", "paths", "internal/shared.go", "ref", "nova-tools#2").Err(); err != nil {
				t.Fatal(err)
			}
			if err := c.ZAdd(ctx, WSKey(strm, earlier), redis.Z{Score: 100, Member: "t1"}).Err(); err != nil {
				t.Fatal(err)
			}
			if err := c.HSet(ctx, "task:t1", "stream", strm, "where", earlier, "state", earlier, "created_at", "100", "paths", "internal/shared.go", "ref", "nova-tools#1").Err(); err != nil {
				t.Fatal(err)
			}
			if _, err := ws.Reorder(ctx, c, strm, "review"); err != nil {
				t.Fatal(err)
			}
			orders, err := ws.ReadOrders(ctx, c, []string{strm})
			if err != nil || len(orders) != 1 || len(orders[0].Order) < 2 || orders[0].Order[0].ID != "t1" || orders[0].Order[1].ID != "t2" {
				t.Fatalf("fixture order: %+v %v", orders, err)
			}
			rep, err := LandStream(ctx, c, Options{Repo: repo, Streams: []string{strm}, DryRun: true, MinScore: 8})
			if err == nil && len(rep.Members) > 0 && len(rep.Skips) == 0 {
				t.Fatalf("earlier t1 is %s, computed order t1 -> t2, but default dry-run selects %+v without any blocker/escalation", earlier, rep.Members)
			}
		})
	}
}
