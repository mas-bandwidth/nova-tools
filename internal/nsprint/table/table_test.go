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

// TestRetiredPipelineRowRefuses (#4411): a sprint the retired card family
// holds nothing of prints the census refusal and its remedy, never a row of
// zeros beside the one count; a sprint with a card there keeps its row, and
// `table --check` names the refusal as one cell.
func TestRetiredPipelineRowRefuses(t *testing.T) {
	t.Parallel()
	zero := []any{"time", "1", "pipeline", "probe", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0"}
	snap, err := table.Parse(zero)
	if err != nil {
		t.Fatal(err)
	}
	want := "pipeline probe " + table.RetiredPipeline + "\n"
	if got := snap.Render(); !strings.Contains(got, want) || strings.Contains(got, "landed=0") {
		t.Fatalf("retired row %q; want %q", got, want)
	}
	if want != `pipeline probe REFUSED pipeline reads a retired key family; remedy="nova-sprint ws counts"`+"\n" {
		t.Fatalf("the refusal changed: %q", want)
	}
	if d := table.DiffCells("name | up\n"+want, "name | up\n"+want); d != "" {
		t.Fatalf("an equal refused row differs: %s", d)
	}
	one := append([]any{}, zero...)
	one[4] = "1" // one queued card in the family
	if snap, err = table.Parse(one); err != nil {
		t.Fatal(err)
	}
	if got := snap.Render(); !strings.Contains(got, "pipeline probe queued=1 ") {
		t.Fatalf("a sprint with a card lost its row: %q", got)
	}
}
