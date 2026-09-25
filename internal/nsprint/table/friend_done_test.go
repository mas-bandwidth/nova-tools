package table_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// friendDoneCell is the done cell of friend's row on the rendered table.
func friendDoneCell(t *testing.T, snap *table.SprintSnapshot, now time.Time, friend string) string {
	t.Helper()
	for _, line := range strings.Split(snap.Render(now), "\n") {
		cells := strings.Split(line, "|")
		if len(cells) == 5 && strings.TrimSpace(cells[0]) == friend {
			return strings.TrimSpace(cells[3])
		}
	}
	t.Fatalf("no row for %s:\n%s", friend, snap.Render(now))
	return ""
}

// TestFriendDoneCountsOnlyThisSprint (DONE-WHEN of #3883): the friend done
// column counts only the cards done in the current sprint, the members of
// friend:<f>:cards:done scored (created_at) at or after s:<S> opened_at, not
// the all-time size of the set. A friend with 3 cards done before the open
// and 2 after shows done=2; after sprint close and a new sprint open the
// column reads 0 until a card is done. The sprints are opened and closed by
// the real ns_sprint_open and ns_sprint_close on a throwaway server.
func TestFriendDoneCountsOnlyThisSprint(t *testing.T) {
	addr := testutil.Start(t)
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, client); err != nil {
		t.Fatalf("load Redis Functions: %v", err)
	}
	st := store.New(client)
	t0 := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	done := func(id string, at time.Time) {
		t.Helper()
		if err := client.ZAdd(ctx, table.FriendCardsKey("rowan", "done"), redis.Z{Score: float64(at.UnixMilli()), Member: id}).Err(); err != nil {
			t.Fatal(err)
		}
	}
	open := func(name string, at time.Time) {
		t.Helper()
		for _, step := range []func() (string, error){
			func() (string, error) { return sprint.Begin(ctx, st, name, "fixture", "f00dfeed", at) },
			func() (string, error) { return sprint.Finish(ctx, st, name, "fixture", "f00dfeed", at, nil) },
		} {
			if refused, err := step(); err != nil || refused != "" {
				t.Fatalf("open %s: %v %s", name, err, refused)
			}
		}
	}
	read := func(r *table.SprintReader, now time.Time) *table.SprintSnapshot {
		t.Helper()
		snap, err := r.Read(ctx, now)
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	cfg := func(s string) table.SprintConfig {
		return table.SprintConfig{Sprint: s, Friends: []string{"rowan"}, RowStale: 10 * time.Second}
	}

	for i := 0; i < 3; i++ {
		done(fmt.Sprintf("s:s0:card:old-%d", i), t0.Add(-time.Duration(3-i)*time.Hour))
	}
	open("s1", t0)
	done("s:s1:card:new-0", t0.Add(time.Minute))
	done("s:s1:card:new-1", t0.Add(2*time.Minute))

	// The reader with no sprint named follows the newest sprint of
	// sprint:order; the named reader reads its own sprint.
	follow := table.NewSprintReader(client, cfg(""))
	named := table.NewSprintReader(client, cfg("s1"))
	now := t0.Add(3 * time.Minute)
	for _, r := range []*table.SprintReader{follow, named} {
		snap := read(r, now)
		if got := friendDoneCell(t, snap, now, "rowan"); got != "2" {
			t.Fatalf("sprint %q: done=%s, want 2 (3 before opened_at, 2 after):\n%s", r.Config.Sprint, got, snap.Render(now))
		}
		if snap.DoneSprint != "s1" || snap.DoneFrom != "opened_at" || snap.DoneSince != strconv.FormatInt(t0.UnixMilli(), 10) {
			t.Fatalf("scope %q from %q since %q, want s1 opened_at %d", snap.DoneSprint, snap.DoneFrom, snap.DoneSince, t0.UnixMilli())
		}
	}
	if snap := read(follow, now); snap.RoundTrips != 1 {
		t.Fatalf("steady tick took %d round trips, want 1", snap.RoundTrips)
	}

	// Close s1, open s2: the column reads 0 until a card is done.
	if _, refused, err := sprint.SetClosed(ctx, st, "s1", t0.Add(time.Hour)); err != nil || refused != "" {
		t.Fatalf("close s1: %v %s", err, refused)
	}
	t2 := t0.Add(2 * time.Hour)
	open("s2", t2)
	now = t2.Add(time.Second)
	named2 := table.NewSprintReader(client, cfg("s2"))
	for _, r := range []*table.SprintReader{follow, named2} {
		if got := friendDoneCell(t, read(r, now), now, "rowan"); got != "0" {
			t.Fatalf("sprint %q after close and a new open: done=%s, want 0", r.Config.Sprint, got)
		}
	}
	done("s:s2:card:first", t2.Add(time.Minute))
	now = t2.Add(2 * time.Minute)
	for _, r := range []*table.SprintReader{follow, named2} {
		if got := friendDoneCell(t, read(r, now), now, "rowan"); got != "1" {
			t.Fatalf("sprint %q after the first card of s2: done=%s, want 1", r.Config.Sprint, got)
		}
	}

	// A sprint with no opened_at is open since its oldest card's created_at
	// (the first member of sprint:<S>:cards), and the snapshot says so.
	legacy := t0.Add(90 * time.Minute)
	if err := client.HSet(ctx, "s:legacy", "status", "open").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.ZAdd(ctx, "sprint:legacy:cards", redis.Z{Score: float64(legacy.UnixMilli()), Member: "s:legacy:card:a"},
		redis.Z{Score: float64(legacy.Add(time.Minute).UnixMilli()), Member: "s:legacy:card:b"}).Err(); err != nil {
		t.Fatal(err)
	}
	snap := read(table.NewSprintReader(client, cfg("legacy")), now)
	if got := friendDoneCell(t, snap, now, "rowan"); got != "1" || snap.DoneFrom != "oldest card" || snap.DoneSince != strconv.FormatInt(legacy.UnixMilli(), 10) {
		t.Fatalf("legacy sprint: done=%s from %q since %q, want 1 from the oldest card at %d", got, snap.DoneFrom, snap.DoneSince, legacy.UnixMilli())
	}
}
