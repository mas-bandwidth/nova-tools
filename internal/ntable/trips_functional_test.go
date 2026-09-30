//go:build functional

package ntable_test

import (
	"context"
	"strings"
	"testing"

	nsstore "github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
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
		err := f()
		require.NoError(t, err, "%s: %v", name, err)
		n := trips.N() - before
		require.Equal(t, int64(1), n, "%s took %d trips; want 1", name, n)
		writes := map[string]bool{"create": true, "same create": true, "row add": true, "cell add": true, "cell move": true, "cell remove": true, "bound row add": true, "row del": true, "clear": true, "bind": true, "drop": true, "drop definition": true, "member create": true, "apply batch": true}
		want := events
		if writes[name] {
			want++
		}
		got := c.XLen(ctx, ntable.ChangesKey("demo")).Val()
		require.Equal(t, want, got, "%s committed %d events; want %d", name, got-events, want-events)
	}
	one("create", func() error { return ntable.Create(ctx, c, demo(), now) })
	one("same create", func() error { return ntable.Create(ctx, c, demo(), now) })
	one("row add", func() error { _, err := ntable.RowAdd(ctx, c, "demo", "r", ntable.RowSpec{}); return err })
	one("cell add", func() error { _, err := ntable.CellAdd(ctx, c, "demo", "r", "ready", "job", 7); return err })
	one("cell members", func() error { _, err := ntable.CellMembers(ctx, c, "demo", "r", "ready"); return err })
	one("read set", func() error {
		_, err := ntable.ReadSet(ctx, c, "demo", ntable.ReadSetScope{Members: []string{"job"}})
		return err
	})
	rev := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	one("apply batch", func() error {
		_, err := ntable.ApplyBatch(ctx, c, ntable.BatchManifest{
			Schema:                1,
			Table:                 "demo",
			Epoch:                 "0",
			ExpectedTableRevision: rev,
			OperationID:           "trip-op-1",
			Actor:                 "trip-test",
			Members: []ntable.BatchMemberEntry{
				{
					ID: "batch-trip-job",
					Expect: &ntable.MemberExpect{
						Absent: true,
					},
					Create: &ntable.MemberCreateOp{
						Row:   "r",
						Col:   "ready",
						Score: 100,
					},
				},
			},
		})
		return err
	})
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
		if err == nil && !tb.Rows[1].Cells[0].Bound {
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
	require.NoError(t, ntable.Create(ctx, c, tb, now))
	row, member := "a row's name", "job ' ; echo wrong"
	if _, err := ntable.RowAdd(ctx, c, "demo", row, ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	_, err := ntable.CellMove(ctx, c, "demo", row, "ready", "working", member)
	require.ErrorIs(t, err, ntable.ErrNotMember, "missing member")
	for _, word := range []string{"demo", row, "ready", member, "run: nova-table cell members", "'a row'\\''s name'"} {
		require.ErrorContains(t, err, word, "refusal misses %q: %v", word, err)
	}
	_, err = ntable.CellAdd(ctx, c, "demo", row, "ready", member, 7)
	require.NoError(t, err)
	dst := ntable.CellKey("demo", row, "working")
	require.NoError(t, c.Set(ctx, dst, "wrong type", 0).Err())
	_, err = ntable.CellMove(ctx, c, "demo", row, "ready", "working", member)
	require.ErrorContains(t, err, "WRONGTYPE", "destination corruption")
	if score, err := c.ZScore(ctx, ntable.CellKey("demo", row, "ready"), member).Result(); err != nil || score != 7 {
		t.Fatalf("refused move lost its source: %v %v", score, err)
	}
}

func TestCreateDeniedRegistryWriteDoesNotLeaveDefinition(t *testing.T) {
	t.Parallel()
	addr, admin := live(t)
	ctx := context.Background()
	require.NoError(t, admin.Do(ctx, "ACL", "SETUSER", "no-registry", "on", ">pw", "~table:*", "~tables", "+@all", "-sadd").Err())
	c := redis.NewClient(&redis.Options{Addr: addr, Username: "no-registry", Password: "pw"})
	t.Cleanup(func() { _ = c.Close() })
	require.ErrorContains(t, ntable.Create(ctx, c, demo(), now), "NOPERM", "create without registry write")
	if n, err := admin.Exists(ctx, ntable.DefKey("demo")).Result(); err != nil || n != 0 {
		t.Fatalf("refused create left definition: %d %v", n, err)
	}
}

func TestNewRowFollowsLastRankAfterDeletion(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	require.NoError(t, ntable.Create(ctx, c, demo(), now))
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
	require.NoError(t, err)
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
	require.NoError(t, ntable.Create(ctx, writer, demo(), now))
	_, err := ntable.RowAdd(ctx, writer, "demo", "r", ntable.RowSpec{})
	require.NoError(t, err)
	_, err = ntable.CellAdd(ctx, writer, "demo", "r", "ready", "job", 7)
	require.NoError(t, err)
	// the coordinator row runs a batch as it is written in source; the reader row does not
	batch := ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: writer.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val(), OperationID: "acl-1", Members: []ntable.BatchMemberEntry{
		{ID: "bm", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "r", Col: "working", Score: 1}, Set: map[string]string{"k": "v"}, Unset: []string{"gone"}}}}
	_, err = ntable.ApplyBatch(ctx, writer, batch)
	require.NoError(t, err, "batch as ns-coordinator")
	batch.OperationID = "acl-2"
	if _, err := ntable.ApplyBatch(ctx, reader, batch); err == nil || !strings.Contains(err.Error(), "NOPERM") && !strings.Contains(err.Error(), "no permissions") {
		t.Fatalf("batch as the reader: %v", err)
	}
	if tb, err := ntable.Read(ctx, reader, "demo"); err != nil || len(tb.Rows) != 1 || tb.Rows[0].Cells[0].Unread || tb.Rows[0].Cells[0].Count != 1 {
		t.Fatalf("reader snapshot: %+v %v", tb, err)
	}
	if names, err := ntable.List(ctx, reader); err != nil || len(names) != 1 {
		t.Fatalf("reader list: %v %v", names, err)
	}
	if ms, err := ntable.CellMembers(ctx, reader, "demo", "r", "ready"); err != nil || len(ms) != 1 || ms[0].Member != "job" {
		t.Fatalf("reader members: %v %v", ms, err)
	}
	_, err = ntable.CellAdd(ctx, reader, "demo", "r", "ready", "forbidden", 1)
	require.ErrorContains(t, err, "NOPERM", "reader wrote")
}
