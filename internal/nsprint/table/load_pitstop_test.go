package table_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
)

// TestLoadPrintsAsPercentOfCores (Glenn 2026-09-26 8:33 AM ET: "normalize it
// so that 100% is total usage of all cores at 100%"; 8:38 AM: "show the load
// as 1.1%"): a beat with load1 and ncpu prints load1/ncpu as a percent with
// one decimal; a beat with load1 alone prints the raw load1 as before.
func TestLoadPrintsAsPercentOfCores(t *testing.T) {
	t.Parallel()
	now := table.SprintFixtureNow()
	at := now.UnixMilli()
	client, _ := consumerStore(t, [][]string{
		{"SADD", "benches", "alpha", "beta"},
		{"HSET", "bench:alpha:beat", "load1", "3.20", "ncpu", "16", "at", strconv.FormatInt(at, 10)},
		{"HSET", "bench:beta:beat", "load1", "0.45", "at", strconv.FormatInt(at, 10)},
	})
	snap, err := table.NewSprintReader(client, table.SprintConfig{}).Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	got := snap.Render(now)
	for _, want := range []string{
		"alpha                     |     0 |       0 |   0/0 |    - | up     | 20.0%\n",
		"beta                      |     0 |       0 |   0/0 |    - | up     | 0.45\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("load cell: want %q in:\n%s", want, got)
		}
	}
}

// TestPitstopOfTheOpenSprintWithoutAFlag (Glenn 2026-09-26 8:50 AM ET:
// "There is only one sprint active at a current time"): with no Sprint in
// the config the banner follows the one open sprint of sprint:order; a
// closed sprint's stop shows nothing.
func TestPitstopOfTheOpenSprintWithoutAFlag(t *testing.T) {
	t.Parallel()
	now := table.SprintFixtureNow()
	client, _ := consumerStore(t, [][]string{
		{"ZADD", "sprint:order", "1", "old", "2", "cur"},
		{"HSET", "s:old", "status", "closed", "opened_at", "1"},
		{"HSET", "s:old:pitstop", "by", "rowan"},
		{"HSET", "s:cur", "status", "open", "opened_at", "2"},
	})
	r := table.NewSprintReader(client, table.SprintConfig{})
	snap, err := r.Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Pitstop {
		t.Fatal("a closed sprint's stop showed as the pit stop")
	}
	if err := client.HSet(context.Background(), "s:cur:pitstop", "by", "rowan").Err(); err != nil {
		t.Fatal(err)
	}
	snap, err = r.Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.Pitstop || !strings.HasPrefix(snap.Render(now), "SPRINT TABLE *** PIT STOP ***") {
		t.Fatalf("the open sprint's stop is not the banner:\n%s", snap.Render(now))
	}
}
