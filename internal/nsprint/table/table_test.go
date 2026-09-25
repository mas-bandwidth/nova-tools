package table_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func controlStore(t *testing.T) *redis.Client {
	t.Helper()
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatal(err)
	}
	return client
}

func seedCommands(t *testing.T, client *redis.Client, cmds [][]string) {
	t.Helper()
	for _, cmd := range cmds {
		args := make([]any, len(cmd))
		for i, value := range cmd {
			args[i] = value
		}
		if err := client.Do(context.Background(), args...).Err(); err != nil {
			t.Fatalf("seed %v: %v", cmd, err)
		}
	}
}

func TestControl21TableCheckFixture(t *testing.T) {
	ctx := context.Background()
	client := controlStore(t)
	seedCommands(t, client, table.DefectFixture())
	snap, err := table.Read(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := snap.Render(), table.DefectGolden(); got != want {
		t.Fatalf("fixture output differs\ngot:\n%s\nwant:\n%s", got, want)
	}
	if len(snap.Errors) != 0 {
		t.Fatalf("clean fixture errors: %v", snap.Errors)
	}
	if err := client.HSet(ctx, "proc:reconciler", "pass_at", "1").Err(); err != nil {
		t.Fatal(err)
	}
	snap, err = table.Read(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snap.Render(), "proc reconciler down age=") || !strings.Contains(snap.Render(), "why=stale: pass") {
		t.Fatalf("old reconciler pass looked current:\n%s", snap.Render())
	}
	if err := client.Del(ctx, "proc:reconciler").Err(); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"bench:b1:width", "bench:b1:queue", "bench:b1:done",
		"friend:fran:width", "friend:fran:queue", "friend:fran:done", "friend:fran:slots",
	} {
		if err := client.Set(ctx, key, "99", 0).Err(); err != nil {
			t.Fatal(err)
		}
		snap, err := table.Read(ctx, client)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(snap.Render(), "two writers: "+key) {
			t.Fatalf("%s accepted:\n%s", key, snap.Render())
		}
		if err := client.Del(ctx, key).Err(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestControl24BenchCellsFromCardIndexes(t *testing.T) {
	ctx := context.Background()
	client := controlStore(t)
	seedCommands(t, client, [][]string{
		{"SADD", "sprints", "control-a"},
		{"HSET", "s:control-a", "status", "open"},
		{"SADD", "benches", "ctl-b"},
		{"HSET", "bench:ctl-b:desired", "slots", "4"},
		{"HSET", "bench:ctl-b:beat", "at", "1"},
		{"ZADD", "s:control-a:bench:ctl-b:queue", "0", "c1", "0", "c2", "0", "c3"},
		{"SADD", "s:control-a:bench:ctl-b:ended", "e1", "e2"},
		{"ZADD", "s:control-a:open:ghost", "0", "x1", "0", "x2", "0", "x3", "0", "x4", "0", "x5"},
		{"SADD", "s:control-a:done:ghost", "y1", "y2", "y3", "y4", "y5", "y6", "y7"},
	})
	snap, err := table.ReadNamed(ctx, client, "control-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Benches) != 1 {
		t.Fatalf("bench rows=%d", len(snap.Benches))
	}
	row := snap.Benches[0]
	if row.Queue != 3 || row.Done != 2 {
		t.Fatalf("bench queue=%d done=%d, want 3/2; friend task indexes must not feed bench", row.Queue, row.Done)
	}
	if strings.Contains(snap.Render(), " | 5 | 7 |") {
		t.Fatalf("friend task cells leaked into bench:\n%s", snap.Render())
	}
}

// TestPipelineCountsOnlyReadyCards is #3066's table half: the pipeline's ready
// cell is the pool, which holds only queued cards whose every DEPENDS-ON is
// merged on their base; a card still waiting on a dependency is counted in
// waiting and never in ready.
func TestPipelineCountsOnlyReadyCards(t *testing.T) {
	raw := []any{"time", "1", "pipeline", "s1", "3", "0", "0", "0", "0", "0", "0", "0", "2", "1", "0", "0", "0"}
	snap, err := table.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	got := snap.Render()
	if !strings.Contains(got, " ready=2 waiting=1 ") || strings.Contains(got, "pool=") {
		t.Fatalf("pipeline line %q: want ready=2 waiting=1 and no pool= cell", got)
	}
}
