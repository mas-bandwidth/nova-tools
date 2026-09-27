//go:build functional

package ntable_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	nsstore "github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

func TestEveryTableOperationCountsOneTrip(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	trips := nsstore.New(c).CountTrips()
	one := func(name string, f func() error) {
		t.Helper()
		events := c.XLen(ctx, ntable.ChangesKey("demo")).Val()
		before := trips.N()
		if err := f(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if n := trips.N() - before; n != 1 {
			t.Fatalf("%s took %d trips; want 1", name, n)
		}
		writes := map[string]bool{"create": true, "same create": true, "row add": true, "cell add": true, "cell move": true, "cell remove": true, "bound row add": true, "row del": true, "clear": true, "bind": true, "drop": true, "drop definition": true, "member create": true}
		want := events
		if writes[name] {
			want++
		}
		if got := c.XLen(ctx, ntable.ChangesKey("demo")).Val(); got != want {
			t.Fatalf("%s committed %d events; want %d", name, got-events, want-events)
		}
	}
	one("create", func() error { return ntable.Create(ctx, c, demo(), now) })
	one("same create", func() error { return ntable.Create(ctx, c, demo(), now) })
	one("row add", func() error { _, err := ntable.RowAdd(ctx, c, "demo", "r", ntable.RowSpec{}); return err })
	one("cell add", func() error { _, err := ntable.CellAdd(ctx, c, "demo", "r", "ready", "job", 7); return err })
	one("cell members", func() error { _, err := ntable.CellMembers(ctx, c, "demo", "r", "ready"); return err })
	one("cell move", func() error { _, err := ntable.CellMove(ctx, c, "demo", "r", "ready", "working", "job"); return err })
	one("cell remove", func() error { _, err := ntable.CellRemove(ctx, c, "demo", "r", "working", "job"); return err })
	one("member create", func() error { return ntable.MemberCreate(ctx, c, "demo", "unplaced") })
	one("check", func() error { _, err := ntable.Check(ctx, c, "demo"); return err })
	one("history", func() error { _, err := ntable.ReadAt(ctx, c, "demo", 0); return err })
	one("shape", func() error { _, err := ntable.Shape(ctx, c, "demo"); return err })
	one("cold read", func() error { _, err := ntable.Read(ctx, c, "demo"); return err })
	one("list", func() error { _, err := ntable.List(ctx, c); return err })
	one("summaries", func() error { _, err := ntable.Summaries(ctx, c); return err })
	reader := ntable.NewReader("demo")
	one("reader first", func() error { _, err := reader.Read(ctx, c); return err })
	one("bound row add", func() error {
		_, err := ntable.RowAdd(ctx, c, "demo", "bound", ntable.RowSpec{Owner: "other-tool move", Binds: map[string]string{"ready": "external:other:ready"}})
		return err
	})
	one("reader changed", func() error {
		tb, err := reader.Read(ctx, c)
		if err == nil && !tb.Rows[1].Cells[1].Bound {
			t.Fatal("new binding missing")
		}
		return err
	})
	one("multiple readers cold", func() error {
		p := c.Pipeline()
		a := ntable.NewReader("demo").Queue(ctx, p)
		b := ntable.NewReader("demo").Queue(ctx, p)
		if _, err := p.Exec(ctx); err != nil {
			return err
		}
		if _, _, err := a.Result(); err != nil {
			return err
		}
		_, _, err := b.Result()
		return err
	})
	one("row del", func() error { _, err := ntable.RowDel(ctx, c, "demo", "bound"); return err })
	one("clear", func() error { _, err := ntable.Clear(ctx, c, "demo"); return err })
	one("bind", func() error {
		tb := demo()
		tb.Rows = []ntable.Row{ntable.NewRow(tb, "a")}
		return ntable.Bind(ctx, c, tb, now)
	})
	one("drop", func() error { _, err := ntable.Drop(ctx, c, "demo"); return err })
	one("drop definition", func() error { _, err := ntable.DropDefinition(ctx, c, "demo"); return err })
}

func TestRefusedMoveIsAtomicAndNamesItsRepair(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	tb := demo()
	if err := ntable.Create(ctx, c, tb, now); err != nil {
		t.Fatal(err)
	}
	row, member := "a row's name", "job ' ; echo wrong"
	if _, err := ntable.RowAdd(ctx, c, "demo", row, ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	_, err := ntable.CellMove(ctx, c, "demo", row, "ready", "working", member)
	if !errors.Is(err, ntable.ErrNotMember) {
		t.Fatalf("missing member: %v", err)
	}
	for _, word := range []string{"demo", row, "ready", member, "run: nova-table cell members", "'a row'\\''s name'"} {
		if !strings.Contains(err.Error(), word) {
			t.Fatalf("refusal misses %q: %v", word, err)
		}
	}
	if _, err := ntable.CellAdd(ctx, c, "demo", row, "ready", member, 7); err != nil {
		t.Fatal(err)
	}
	dst := ntable.CellKey("demo", row, "working")
	if err := c.Set(ctx, dst, "wrong type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellMove(ctx, c, "demo", row, "ready", "working", member); err == nil || !strings.Contains(err.Error(), "WRONGTYPE") {
		t.Fatalf("destination corruption: %v", err)
	}
	if score, err := c.ZScore(ctx, ntable.CellKey("demo", row, "ready"), member).Result(); err != nil || score != 7 {
		t.Fatalf("refused move lost its source: %v %v", score, err)
	}
}

func TestCreateDeniedRegistryWriteDoesNotLeaveDefinition(t *testing.T) {
	t.Parallel()
	addr, admin := live(t)
	ctx := context.Background()
	if err := admin.Do(ctx, "ACL", "SETUSER", "no-registry", "on", ">pw", "~table:*", "~tables", "+@all", "-sadd").Err(); err != nil {
		t.Fatal(err)
	}
	c := redis.NewClient(&redis.Options{Addr: addr, Username: "no-registry", Password: "pw"})
	t.Cleanup(func() { _ = c.Close() })
	if err := ntable.Create(ctx, c, demo(), now); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("create without registry write: %v", err)
	}
	if n, err := admin.Exists(ctx, ntable.DefKey("demo")).Result(); err != nil || n != 0 {
		t.Fatalf("refused create left definition: %d %v", n, err)
	}
}

func TestNewRowFollowsLastRankAfterDeletion(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	if err := ntable.Create(ctx, c, demo(), now); err != nil {
		t.Fatal(err)
	}
	for _, row := range []string{"a", "b", "z"} {
		if _, err := ntable.RowAdd(ctx, c, "demo", row, ntable.RowSpec{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ntable.RowDel(ctx, c, "demo", "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "c", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	tb, err := ntable.Read(ctx, c, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(tb.Rows) != 3 || tb.Rows[1].Key != "z" || tb.Rows[2].Key != "c" {
		t.Fatalf("new row reordered an existing one: %+v", tb.Rows)
	}
}

// The deployed reader role must keep its existing table-read capability
// after reads move behind FCALL_RO, without gaining a write door.
func TestSourceACLTableReaderKeepsReadOnlyAccess(t *testing.T) {
	t.Parallel()
	var extra []string
	for _, rule := range nsstore.ACLRules {
		name, body, _ := strings.Cut(rule, " ")
		if name == "ns-table" || name == "ns-coordinator" {
			extra = append(extra, "--user", name, "on", ">pw")
			extra = append(extra, strings.Fields(body)...)
		}
	}
	addr, _ := live(t, extra...)
	ctx := context.Background()
	as := func(name string) *redis.Client {
		c := redis.NewClient(&redis.Options{Addr: addr, Username: name, Password: "pw"})
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	writer, reader := as("ns-coordinator"), as("ns-table")
	if err := ntable.Create(ctx, writer, demo(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, writer, "demo", "r", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellAdd(ctx, writer, "demo", "r", "ready", "job", 7); err != nil {
		t.Fatal(err)
	}
	if tb, err := ntable.Read(ctx, reader, "demo"); err != nil || len(tb.Rows) != 1 || tb.Rows[0].Cells[1].Unread || tb.Rows[0].Cells[1].Count != 1 {
		t.Fatalf("reader snapshot: %+v %v", tb, err)
	}
	if names, err := ntable.List(ctx, reader); err != nil || len(names) != 1 {
		t.Fatalf("reader list: %v %v", names, err)
	}
	if ms, err := ntable.CellMembers(ctx, reader, "demo", "r", "ready"); err != nil || len(ms) != 1 || ms[0].Member != "job" {
		t.Fatalf("reader members: %v %v", ms, err)
	}
	if _, err := ntable.CellAdd(ctx, reader, "demo", "r", "ready", "forbidden", 1); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("reader wrote: %v", err)
	}
}
