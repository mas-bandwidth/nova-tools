//go:build functional

package table_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
	"github.com/redis/go-redis/v9"
)

// TestControl3637ClearUnderOneSecond (DONE-WHEN of #3637): table clear moves
// every landed member to closed, waiting, ready, working, review and
// merging untouched, in under one second; the checkpoint names every moved
// task and every done count. The consumer table is untouched (#4071: its
// done is ok + fail, ZCARDs with no base from a clear).
// sprintStoreLua is the fixture on a throwaway redis-server with the
// nova_sprint library loaded: the clear moves through the one task move.
func sprintStoreLua(t *testing.T) (*redis.Client, *cmdLog) {
	t.Helper()
	_, client := wstest.Start(t)
	seedCommands(t, client, table.SprintFixture())
	log := &cmdLog{}
	client.AddHook(log)
	return client, log
}

func TestControl3637ClearUnderOneSecond(t *testing.T) {
	t.Parallel()

	client, log := sprintStoreLua(t)
	ctx, now := context.Background(), table.SprintFixtureNow()
	r := table.NewSprintReader(client, table.SprintFixtureConfig())
	before, err := r.Read(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	log.reset()
	start := time.Now()
	plan, err := table.PlanClear(ctx, client, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	cp := plan.Checkpoint()
	if err := plan.Apply(ctx, client, "rowan", "test", "receipt"); err != nil {
		t.Fatal(err)
	}
	// The one-second rule is structural: four round trips whatever the
	// number of landed tasks (lists, landed sets, task fields, one
	// MULTI/EXEC). The wall time is logged, not asserted (the waits class).
	t.Logf("CLEAR ms=%.2f landed=%d", float64(time.Since(start).Microseconds())/1000, plan.Count())
	if names, trips := log.reset(); trips != 4 {
		t.Fatalf("clear took %d round trips, want 4: %v", trips, names)
	}
	if plan.Count() != 6 || strings.Count(cp, "\ntask\t") != 6 || !strings.Contains(cp, "\nfriend\trowan\t0\n") {
		t.Fatalf("plan count %d; checkpoint:\n%s", plan.Count(), cp)
	}
	after, err := r.Read(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range after.Streams {
		b := before.Streams[i]
		// The clear's move registers a stream it touches, which creates the
		// stream's sentinel in waiting (#4318); the one count counts it
		// like any card, so waiting may grow by that one card.
		stop := int64(0)
		if _, err := client.ZScore(ctx, ws.Key(row.Name, ws.Waiting), ws.SentinelID(row.Name)).Result(); err == nil {
			stop = 1
		}
		if row.Landed != 0 || (row.Waiting != b.Waiting && row.Waiting != b.Waiting+stop) || row.Ready != b.Ready || row.Working != b.Working ||
			row.Review != b.Review || row.Merging != b.Merging {
			t.Fatalf("stream %q after clear %+v, before %+v", row.Name, row, b)
		}
	}
	got := after.Render(now)
	all := after.Counts.All()
	if after.Counts.Done() != 0 || !strings.Contains(got, fmt.Sprintf("\n0/%d done 0%%, left %d, eta ", all, all)) {
		t.Fatalf("after clear:\n%s", got)
	}
	if b, a := before.Render(now), got; b[strings.Index(b, "\nworker "):] != a[strings.Index(a, "\nworker "):] {
		t.Fatalf("a clear changed the consumer table:\nbefore\n%s\nafter\n%s", b, a)
	}
	if state, _ := client.HGet(ctx, "task:t9-landed-0", "state").Result(); state != "closed" {
		t.Fatalf("task:t9-landed-0 state=%q, want closed", state)
	}
	msgs, _ := client.XRange(ctx, "ws:log", "-", "+").Result()
	closed := 0
	for _, m := range msgs {
		if m.Values["to"] == "done/ok" && m.Values["from"] == "landed" && m.Values["by"] == "rowan" {
			closed++
		}
	}
	if closed != 6 {
		t.Fatalf("ws:log has %d landed->done/ok entries, want 6", closed)
	}
	if v, _ := client.Get(ctx, "ws:checkpoint").Result(); v != "receipt" {
		t.Fatalf("ws:checkpoint=%q", v)
	}
}
