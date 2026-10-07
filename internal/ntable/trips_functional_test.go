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
		if err == nil {
			require.True(t, tb.Rows[1].Cells[0].Bound, "new binding missing")
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
	row, member := "a row's name", "job ' ; echo wrong"
	newTable(t, c, tb).rows(row)
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
	score, err := c.ZScore(ctx, ntable.CellKey("demo", row, "ready"), member).Result()
	require.NoError(t, err, "refused move lost its source")
	require.Equal(t, float64(7), score, "refused move lost its source: %v", score)
}

func TestCreateDeniedRegistryWriteDoesNotLeaveDefinition(t *testing.T) {
	t.Parallel()
	addr, admin := live(t)
	ctx := context.Background()
	require.NoError(t, admin.Do(ctx, "ACL", "SETUSER", "no-registry", "on", ">pw", "~table:*", "~tables", "+@all", "-sadd").Err())
	c := redis.NewClient(&redis.Options{Addr: addr, Username: "no-registry", Password: "pw"})
	t.Cleanup(func() { _ = c.Close() })
	require.ErrorContains(t, ntable.Create(ctx, c, demo(), now), "NOPERM", "create without registry write")
	n, err := admin.Exists(ctx, ntable.DefKey("demo")).Result()
	require.NoError(t, err, "refused create left definition")
	require.Equal(t, int64(0), n, "refused create left definition")
}

func TestNewRowFollowsLastRankAfterDeletion(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	newTable(t, c, demo()).rows("a", "b", "z")
	_, err := ntable.RowDel(ctx, c, "demo", "b")
	require.NoError(t, err)
	_, err = ntable.RowAdd(ctx, c, "demo", "c", ntable.RowSpec{})
	require.NoError(t, err)
	tb, err := ntable.Read(ctx, c, "demo")
	require.NoError(t, err)
	require.Len(t, tb.Rows, 3, "new row reordered an existing one: %+v", tb.Rows)
	require.Equal(t, "z", tb.Rows[1].Key, "new row reordered an existing one: %+v", tb.Rows)
	require.Equal(t, "c", tb.Rows[2].Key, "new row reordered an existing one: %+v", tb.Rows)
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
	newTable(t, writer, demo()).rows("r").cell("r", "ready", "job", 7)
	// the coordinator row runs a batch as it is written in source; the reader row does not
	batch := ntable.BatchManifest{Schema: 1, Table: "demo", Epoch: "0", ExpectedTableRevision: writer.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val(), OperationID: "acl-1", Members: []ntable.BatchMemberEntry{
		{ID: "bm", Expect: &ntable.MemberExpect{Absent: true}, Create: &ntable.MemberCreateOp{Row: "r", Col: "working", Score: 1}, Set: map[string]string{"k": "v"}, Unset: []string{"gone"}}}}
	_, err := ntable.ApplyBatch(ctx, writer, batch)
	require.NoError(t, err, "batch as ns-coordinator")
	batch.OperationID = "acl-2"
	_, err = ntable.ApplyBatch(ctx, reader, batch)
	require.Error(t, err, "batch as the reader")
	require.Regexp(t, "NOPERM|no permissions", err.Error(), "batch as the reader: %v", err)
	tb, err := ntable.Read(ctx, reader, "demo")
	require.NoError(t, err, "reader snapshot: %+v", tb)
	require.Len(t, tb.Rows, 1, "reader snapshot: %+v", tb)
	require.False(t, tb.Rows[0].Cells[0].Unread, "reader snapshot: %+v", tb)
	require.Equal(t, int64(1), tb.Rows[0].Cells[0].Count, "reader snapshot: %+v", tb)
	names, err := ntable.List(ctx, reader)
	require.NoError(t, err, "reader list: %v", names)
	require.Len(t, names, 1, "reader list: %v", names)
	ms, err := ntable.CellMembers(ctx, reader, "demo", "r", "ready")
	require.NoError(t, err, "reader members: %v", ms)
	require.Len(t, ms, 1, "reader members: %v", ms)
	require.Equal(t, "job", ms[0].Member, "reader members: %v", ms)
	_, err = ntable.CellAdd(ctx, reader, "demo", "r", "ready", "forbidden", 1)
	require.ErrorContains(t, err, "NOPERM", "reader wrote")
}
