package table_test

import (
	"context"
	"regexp"
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

// TestPipelineRowRefusesWhateverTheFamilyHolds (#4411, the cold read's
// probe: one SADD s:<S>:idx:card:landed made the row print landed=1 beside
// the one count's 1/8): the wide table's pipeline row prints the census
// refusal and its remedy for a sprint whose retired card family is empty,
// holds one landed card, or holds a ready pool, never a number; `table
// --check` names the refusal as one cell.
func TestPipelineRowRefusesWhateverTheFamilyHolds(t *testing.T) {
	t.Parallel()
	want := "pipeline probe " + table.RetiredPipeline + "\n"
	if want != `pipeline probe REFUSED pipeline reads a retired key family; remedy="nova-sprint ws counts"`+"\n" {
		t.Fatalf("the refusal changed: %q", want)
	}
	zero := []any{"time", "1", "pipeline", "probe", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "0"}
	for name, set := range map[string]func([]any){
		"empty":        func([]any) {},
		"landed=1":     func(r []any) { r[11] = "1" }, // the reader's SADD s:probe:idx:card:landed old1
		"queued=3":     func(r []any) { r[4] = "3" },
		"ready pool=2": func(r []any) { r[12], r[13] = "2", "1" },
	} {
		raw := append([]any{}, zero...)
		set(raw)
		snap, err := table.Parse(raw)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := snap.Render()
		if !strings.Contains(got, want) || regexp.MustCompile(`pipeline probe .*=\d`).MatchString(got) {
			t.Fatalf("%s: row %q; want %q and no number", name, got, want)
		}
	}
	if d := table.DiffCells("name | up\n"+want, "name | up\n"+want); d != "" {
		t.Fatalf("an equal refused row differs: %s", d)
	}
	if d := table.DiffCells("name | up\npipeline probe queued=1\n", "name | up\n"+want); d == "" {
		t.Fatal("a file with a pipeline number matched the refused row")
	}
}
