package table_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
)

// TestHostRowPrintsNoMirror (#3804): a bench whose ci:nomirror:<b> set is
// non-empty ends its host row " | nomirror=<repos>" (sorted), a stale row
// too; a bench with no set prints the row unchanged; a set that cannot be
// read (WRONGTYPE here) prints nomirror=?, never nothing.
func TestHostRowPrintsNoMirror(t *testing.T) {
	t.Parallel()

	now := table.Fixture2674Now()
	at := now.Add(-1 * time.Second).UTC().Format("2006-01-02T15:04:05Z")
	old := now.Add(-1 * time.Hour).UTC().Format("2006-01-02T15:04:05Z")
	client := liveStore(t, [][]string{
		{"HSET", "bench:alpha", "host", "alpha", "at", at, "load1", "1.00"},
		{"SADD", table.NoMirrorKey("alpha"), "rowan-tools", "nova-tools"},
		{"HSET", "bench:beta", "host", "beta", "at", at},
		{"HSET", "bench:gamma", "host", "gamma", "at", old},
		{"SADD", table.NoMirrorKey("gamma"), "nova-tools"},
		{"HSET", "bench:delta", "host", "delta", "at", at},
		{"SET", table.NoMirrorKey("delta"), "not-a-set"},
	})
	snap, err := table.ReadLive(context.Background(), client, table.LiveConfig{Friends: []string{"rowan"}, Sprint: "x"})
	if err != nil {
		t.Fatal(err)
	}
	out := snap.RenderLive(now)
	for _, want := range []string{
		"alpha      |     0 |       0 |     0 |     0 |     0 |   0% |   1.00 | nomirror=nova-tools,rowan-tools\n",
		"beta       |     0 |       0 |     0 |     0 |     0 |   0% |      -\n",
		"gamma      | stale |       ? |     ? |     ? |     ? |    ? |      ? | nomirror=nova-tools\n",
		"delta      |     0 |       0 |     0 |     0 |     0 |   0% |      - | nomirror=?\n",
		"total      |     0 |       0 |     0 |     0 |     0 |   0% |\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("table lacks %q\n%s", want, out)
		}
	}
}
