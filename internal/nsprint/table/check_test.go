package table_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
)

func TestDiffCellsNamesTheCell(t *testing.T) {
	t.Parallel()

	golden := table.DefectGolden()
	if got := table.DiffCells(golden, golden); got != "" {
		t.Fatalf("equal tables differ: %s", got)
	}
	cases := []struct{ file, want string }{
		{strings.Replace(golden, "pipeline s1 "+table.RetiredPipeline, "pipeline s1 ready=5 waiting=0", 1), "cell pipeline s1 REFUSED: missing from the file"},
		{strings.Replace(golden, "friend:eight | up | 8 | 0 | 8 |", "friend:eight | up | 8 | 1 | 8 |", 1), "cell friend:eight ready:"},
		{strings.Replace(golden, "proc reconciler down age=-1s why=missing: pass", "proc reconciler up age=-1s why=missing: pass", 1), "cell proc reconciler state:"},
		{strings.Replace(golden, "bench:b3 | down | 1 | 0 | 0 | 0 | 0 | 0 | 0 | down: beat\n", "", 1), "cell bench:b3 up: missing from the file"},
		{golden + "RED two writers: bench:b1:width\n", "cell RED 1: in the file"},
	}
	for _, c := range cases {
		if got := table.DiffCells(c.file, golden); !strings.HasPrefix(got, c.want) {
			t.Errorf("got %q, want prefix %q", got, c.want)
		}
	}
	older := "name | up\nproc reconciler up age=3s\n"
	if got := table.DiffCells(older, "name | up\nproc reconciler up age=5s\n"); got != "" {
		t.Fatalf("a proc age 2 s on is the next tick, not a mismatch: %s", got)
	}
	if got := table.DiffCells(older, "name | up\nproc reconciler up age=6s\n"); !strings.HasPrefix(got, "cell proc reconciler age:") {
		t.Fatalf("a proc age 3 s on: %q", got)
	}
}
