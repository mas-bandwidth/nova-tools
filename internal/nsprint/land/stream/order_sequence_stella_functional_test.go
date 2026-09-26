//go:build functional

package stream

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

// Stella's reproduction (her read of #4410 at cce8edc8e, bus
// stella-e343a0a85f35), copied as she wrote it.
// A changed order among the already selected members is still an order
// change; set membership alone cannot validate a built landing's sequence.
func TestStellaMergeRejectsReorderedSelectedMembers(t *testing.T) {
	t.Parallel()
	c := newRedis(t)
	ctx := context.Background()
	head := strings.Repeat("b", 40)
	for _, n := range []int{1, 2} {
		seed(t, c, n, head, int64(n*100), score("rowan", head, 10))
	}
	if err := c.HSet(ctx, "task:t1", "where", "merging", "state", "merging", "paths", "internal/one.go", "ref", "nova-tools#1").Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, "task:t2", "where", "merging", "state", "merging", "paths", "internal/two.go", "ref", "nova-tools#2").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Reorder(ctx, c, strm, "test"); err != nil {
		t.Fatal(err)
	}
	rep, err := LandStream(ctx, c, Options{Repo: repo, Streams: []string{strm}, DryRun: true, MinScore: 8})
	if err != nil || len(rep.Members) != 2 || rep.Members[0].Task != "t1" || rep.Members[1].Task != "t2" {
		t.Fatalf("initial selection %+v %v", rep, err)
	}
	slug, err := Slug(strm)
	if err != nil {
		t.Fatal(err)
	}
	landing := Landing{Repo: repo, Slug: slug, Streams: strm, Base: "dev", Branch: "stream/x", Head: head, Members: rep.Members, PR: 900, State: "open"}
	if _, err := SaveBuilt(ctx, c, landing, "test"); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, LandKey(repo, slug), "state", "open", "pr", 900, "head", head).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := Record(ctx, c, repo, 900, RecordFields{Head: head, Base: "dev", CI: "green", Mergeable: "true"}); err != nil {
		t.Fatal(err)
	}
	// A coordinator revision changes the order after the landing was built.
	if err := c.HSet(ctx, "task:t1", "blocked_on", "t2").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.Reorder(ctx, c, strm, "test"); err != nil {
		t.Fatal(err)
	}
	orders, err := ws.ReadOrders(ctx, c, []string{strm})
	if err != nil || len(orders) != 1 || len(orders[0].Order) < 2 || orders[0].Order[0].ID != "t2" || orders[0].Order[1].ID != "t1" {
		t.Fatalf("revised order %+v %v", orders, err)
	}
	_, err = Merge(ctx, c, MergeOptions{Repo: repo, Streams: []string{strm}, By: "test"})
	var refusal *Refusal
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Why, "ORDER") {
		t.Fatalf("built sequence t1,t2 differs from current t2,t1, but merge did not refuse for order: %v", err)
	}
}
