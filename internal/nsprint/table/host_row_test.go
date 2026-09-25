package table_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/redis/go-redis/v9"
)

// TestControl2389HostRowFromBenchOwnKeys (DONE-WHEN of #2389): each host row
// of the whole sprint table is what the bench itself keeps in the store: its
// ready and working are ZCARD bench:<b>:cards:ready|working (the card views
// the one move primitive writes) and its load is bench:<b>:beat load1 (the
// bench's own Go beat). A bench:<b> hash written by the bash bench-row
// changes no cell, and a bench in the benches SET whose beat has expired
// still shows its cards with load "down" (cards never disappear).
func TestControl2389HostRowFromBenchOwnKeys(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	now := table.SprintFixtureNow()
	ms := strconv.FormatInt(now.Add(-1e9).UnixMilli(), 10)
	seedCommands(t, client, [][]string{
		{"SADD", "benches", "alpha", "beta"},
		// alpha: its own beat and card views ...
		{"HSET", "bench:alpha:beat", "host", "alpha.example", "load1", "0.50", "live", "1", "at", ms},
		{"ZADD", "bench:alpha:cards:ready", "1", "card:a1", "2", "card:a2"},
		{"ZADD", "bench:alpha:cards:working", "3", "card:a3"},
		// ... and a bash bench-row hash that disagrees with every cell.
		{"HSET", "bench:alpha", "host", "alpha", "queue", "99", "working", "99", "load1", "9.99", "at", now.Format("2006-01-02T15:04:05Z")},
		// beta: one dealt card, no beat (its loop is dead), no hash.
		{"ZADD", "bench:beta:cards:ready", "4", "card:b1"},
	})
	snap, err := table.NewSprintReader(client, table.SprintConfig{}).Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	got := snap.Render(now)
	for _, want := range []string{
		"alpha      |     2 |       1 |     - |     - |     - |    - |   0.50\n",
		"beta       |     1 |       0 |     - |     - |     - |    - |   down\n",
		"total      |     3 |       1 |     - |     - |     - |    - |\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("host block wants %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "99") || strings.Contains(got, "9.99") {
		t.Fatalf("a bash bench-row value reached the table:\n%s", got)
	}
}

// TestHostRowNomirror (#3804): the host row prints nomirror=<repos> when the
// bench's ci:nomirror set is non-empty, and omits it when empty.
func TestHostRowNomirror(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	now := table.SprintFixtureNow()
	ms := strconv.FormatInt(now.Add(-1e9).UnixMilli(), 10)
	seedCommands(t, client, [][]string{
		{"SADD", "benches", "alpha", "beta", "gamma"},
		{"HSET", "bench:alpha:beat", "host", "alpha.example", "load1", "0.50", "live", "1", "at", ms},
		{"ZADD", "bench:alpha:cards:ready", "1", "card:a1"},
		{"HSET", "bench:beta:beat", "host", "beta.example", "load1", "0.30", "live", "1", "at", ms},
		{"ZADD", "bench:beta:cards:ready", "2", "card:b1"},
		{"HSET", "bench:gamma:beat", "host", "gamma.example", "load1", "0.10", "live", "1", "at", ms},
		{"ZADD", "bench:gamma:cards:ready", "3", "card:g1"},
		// alpha has no nomirror entries (set does not exist)
		// beta has two repos with missing mirrors
		{"SADD", "ci:nomirror:beta", "nova-tools", "rowan-tools"},
		// gamma has one
		{"SADD", "ci:nomirror:gamma", "vision"},
	})
	snap, err := table.NewSprintReader(client, table.SprintConfig{}).Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	got := snap.Render(now)
	for _, want := range []string{
		"alpha      |     1 |       0 |     - |     - |     - |    - |   0.50\n",
		"beta       |     1 |       0 |     - |     - |     - |    - |   0.30 nomirror=nova-tools,rowan-tools\n",
		"gamma      |     1 |       0 |     - |     - |     - |    - |   0.10 nomirror=vision\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("host block wants %q:\n%s", want, got)
		}
	}
	// Verify the HostRow struct itself
	for _, h := range snap.Hosts {
		switch h.Name {
		case "alpha":
			if h.Nomirror != "" {
				t.Fatalf("alpha Nomirror=%q, want empty", h.Nomirror)
			}
		case "beta":
			if h.Nomirror != "nova-tools,rowan-tools" && h.Nomirror != "rowan-tools,nova-tools" {
				t.Fatalf("beta Nomirror=%q, want nova-tools,rowan-tools or rowan-tools,nova-tools", h.Nomirror)
			}
		case "gamma":
			if h.Nomirror != "vision" {
				t.Fatalf("gamma Nomirror=%q, want vision", h.Nomirror)
			}
		}
	}
}
