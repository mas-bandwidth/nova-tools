package table_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/redis/go-redis/v9"
)

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

// TestPipelineCountsOnlyReadyCards is #3066's table half: the pipeline's ready
// cell is the pool, which holds only queued cards whose every DEPENDS-ON is
// merged on their base; a card still waiting on a dependency is counted in
// waiting and never in ready.
func TestPipelineCountsOnlyReadyCards(t *testing.T) {
	t.Parallel()

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
