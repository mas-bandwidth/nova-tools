package table_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

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
		"alpha      |     2 |       1 |     0 |     0 |     0 |    - | 0.50      \n",
		"beta       |     1 |       0 |     0 |     0 |     0 |    - | down      \n",
		"total      |     3 |       1 |     0 |     0 |     0 |    - |\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("host block wants %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "99") || strings.Contains(got, "9.99") {
		t.Fatalf("a bash bench-row value reached the table:\n%s", got)
	}
}

// TestHostRowsFromBenchSets (DONE-WHEN of #3894): a host row's done, ok, fail
// and ok% are the bench's own card sets, bench:<b>:cards:ok|fail counted from
// s:<S> opened_at (every score is the card's created_at): a bench with 3 ok
// and 1 fail card since the sprint opened shows done=4 ok=3 fail=1 ok%=75.
// A card that ended before the sprint opened is not counted, the bash
// bench:<b> hash's done/ok/fail change no cell, and the next tick after a
// card moves working -> ok shows it in ok, done and ok%.
func TestHostRowsFromBenchSets(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	now := table.SprintFixtureNow()
	ms := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).UnixMilli(), 10) }
	seedCommands(t, client, [][]string{
		{"SADD", "benches", "alpha"},
		{"HSET", "s:S1", "status", "open", "opened_at", ms(-2 * time.Hour)},
		{"HSET", "bench:alpha:beat", "host", "alpha", "load1", "0.50", "live", "1", "at", ms(-time.Second)},
		{"ZADD", "bench:alpha:cards:working", ms(-50 * time.Minute), "s:S1:card:w1"},
		{"ZADD", "bench:alpha:cards:ok", ms(-90 * time.Minute), "s:S1:card:o1", ms(-80 * time.Minute), "s:S1:card:o2", ms(-70 * time.Minute), "s:S1:card:o3"},
		{"ZADD", "bench:alpha:cards:fail", ms(-60 * time.Minute), "s:S1:card:f1"},
		// ended in an earlier sprint: created before S1 opened
		{"ZADD", "bench:alpha:cards:ok", ms(-3 * time.Hour), "s:S0:card:old"},
		// the bash bench-row hash disagrees and is never read
		{"HSET", "bench:alpha", "host", "alpha", "done", "99", "ok", "98", "fail", "97"},
	})
	r := table.NewSprintReader(client, table.SprintConfig{Sprint: "S1"})
	snap, err := r.Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Hosts) != 1 {
		t.Fatalf("hosts %+v, want one", snap.Hosts)
	}
	if h := snap.Hosts[0]; h.Done() != 4 || h.OK != 3 || h.Fail != 1 || h.Unread {
		t.Fatalf("host row %+v done=%d, want done=4 ok=3 fail=1", h, h.Done())
	}
	got := snap.Render(now)
	for _, want := range []string{
		"alpha      |     0 |       1 |     4 |     3 |     1 |  75% | 0.50      \n",
		"total      |     0 |       1 |     4 |     3 |     1 |  75% |\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("host block wants %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "99") || strings.Contains(got, "98") || strings.Contains(got, "97") {
		t.Fatalf("a bash bench-row value reached the table:\n%s", got)
	}

	// The card move working -> done/ok: the next tick counts it.
	seedCommands(t, client, [][]string{
		{"ZREM", "bench:alpha:cards:working", "s:S1:card:w1"},
		{"ZADD", "bench:alpha:cards:ok", ms(-50 * time.Minute), "s:S1:card:w1"},
	})
	snap, err = r.Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if snap.RoundTrips != 1 {
		t.Fatalf("steady tick RoundTrips=%d, want 1", snap.RoundTrips)
	}
	if got := snap.Render(now); !strings.Contains(got, "alpha      |     0 |       0 |     5 |     4 |     1 |  80% | 0.50      \n") {
		t.Fatalf("after working -> ok:\n%s", got)
	}

	// No sprint named (or no opened_at): the whole set counts.
	snap, err = table.NewSprintReader(client, table.SprintConfig{}).Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.Render(now); !strings.Contains(got, "alpha      |     0 |       0 |     6 |     5 |     1 |  83% | 0.50      \n") {
		t.Fatalf("no sprint, every card counts:\n%s", got)
	}

	// A set that is not a ZSET is unread: "?", never a false 0.
	seedCommands(t, client, [][]string{{"DEL", "bench:alpha:cards:fail"}, {"SET", "bench:alpha:cards:fail", "x"}})
	snap, err = r.Read(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.Render(now); !strings.Contains(got, "alpha      |     0 |       0 |     ? |     ? |     ? |    ? | 0.50      \n") ||
		!strings.Contains(got, "total      |     0 |       0 |     ? |     ? |     ? |    ? |\n") {
		t.Fatalf("unread fail set:\n%s", got)
	}
}
