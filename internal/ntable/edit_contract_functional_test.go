//go:build functional

package ntable_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	nsstore "github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func editFixture(t *testing.T) (*redis.Client, ntable.Table) {
	t.Helper()
	_, c := live(t)
	cols, err := ntable.ParseColumns("status:text:none,a,b,x")
	require.NoError(t, err)
	tb := ntable.Table{Name: "edits", Columns: cols, EpochKey: "domain:epoch"}
	newTable(t, c, tb)
	_, err = ntable.RowsAdd(context.Background(), c, tb.Name, []string{"r", "s"})
	require.NoError(t, err)
	return c, tb
}

func TestTableEditsPreserveEpochHistoryAndRenameLedger(t *testing.T) {
	t.Parallel()
	c, tb := editFixture(t)
	ctx := context.Background()
	_, err := ntable.CellAdd(ctx, c, tb.Name, "r", "a", "old", 7)
	require.NoError(t, err)
	_, err = ntable.RowSet(ctx, c, tb.Name, "r", map[string]string{"status": "old status"})
	require.NoError(t, err)
	old, err := ntable.ReadAt(ctx, c, tb.Name, 0)
	require.NoError(t, err)
	require.NoError(t, c.XGroupCreate(ctx, ntable.ChangesKey(tb.Name), "reader", "0").Err())
	require.NoError(t, c.HSet(ctx, tb.EpochKey, "n", 1).Err())
	var receipt ntable.Receipt
	opts := ntable.WriteOptions{Epoch: 1, Actor: "edit-test", Fence: "f7", Idem: "i9", Receipt: &receipt}
	trips := nsstore.New(c).CountTrips()
	one := func(name, table string, call func() error) {
		t.Helper()
		before := c.XLen(ctx, ntable.ChangesKey(table)).Val()
		n := trips.N()
		require.NoError(t, call(), "%s", name)
		require.Equal(t, int64(1), trips.N()-n, "%s took %d trips", name, trips.N()-n)
		require.NotEmpty(t, receipt.ID, "%s receipt=%+v", name, receipt)
		require.Equal(t, uint64(1), receipt.Epoch, "%s receipt=%+v", name, receipt)
		require.Equal(t, receipt.Before+1, receipt.After, "%s receipt=%+v", name, receipt)
		events, err := c.XRangeN(ctx, ntable.ChangesKey(table), receipt.ID, receipt.ID, 1).Result()
		require.NoError(t, err, "%s events=%v", name, events)
		require.Len(t, events, 1, "%s events=%v", name, events)
		require.Equal(t, before+1, c.XLen(ctx, ntable.ChangesKey(table)).Val(), "%s events=%v", name, events)
		e := events[0].Values
		require.Equal(t, "edit-test", e["actor"], "metadata=%v", e)
		require.Equal(t, "f7", e["fence"], "metadata=%v", e)
		require.Equal(t, "i9", e["idem"], "metadata=%v", e)
		require.Equal(t, "1", e["epoch"], "metadata=%v", e)
	}
	one("rows add", tb.Name, func() error { _, e := ntable.RowsAdd(ctx, c, tb.Name, []string{"r", "s"}, opts); return e })
	one("text", tb.Name, func() error {
		_, e := ntable.RowSet(ctx, c, tb.Name, "r", map[string]string{"status": "active"}, opts)
		return e
	})
	one("text noop", tb.Name, func() error {
		_, e := ntable.RowSet(ctx, c, tb.Name, "r", map[string]string{"status": "active"}, opts)
		return e
	})
	require.Equal(t, "noop", receipt.Outcome, "repeated text receipt=%+v", receipt)
	one("members", tb.Name, func() error {
		_, e := ntable.CellsAdd(ctx, c, tb.Name, "r", "a", 4, []string{"new1", "new2"}, opts)
		return e
	})
	one("hide rows", tb.Name, func() error { _, e := ntable.RowsHide(ctx, c, tb.Name, true, []string{"r", "s"}, opts); return e })
	one("metadata", tb.Name, func() error {
		_, e := ntable.RowAdd(ctx, c, tb.Name, "r", ntable.RowSpec{Label: "renamed row"}, opts)
		return e
	})
	footer := "all"
	one("hide columns", tb.Name, func() error {
		_, e := ntable.Set(ctx, c, tb.Name, ntable.SetOpts{Footer: &footer, Hide: []string{"b"}}, opts)
		return e
	})
	// The only field on this row is a binding being removed in the same operation
	// as rename. Deleting it before RENAME would cause a late missing-key error.
	_, err = ntable.RowAdd(ctx, c, tb.Name, "bound", ntable.RowSpec{Binds: map[string]string{"x": "external:x"}}, opts)
	require.NoError(t, err)
	require.NoError(t, c.ZAdd(ctx, "external:x", redis.Z{Score: 9, Member: "external"}).Err())
	cols, err := ntable.ParseColumns("status:text:none,a,b")
	require.NoError(t, err)
	beforeEvents, err := c.XRange(ctx, ntable.ChangesKey(tb.Name), "-", "+").Result()
	require.NoError(t, err)
	n := trips.N()
	moved, err := ntable.Set(ctx, c, tb.Name, ntable.SetOpts{Rename: "renamed", Columns: cols}, opts)
	renameTrips := trips.N() - n
	require.NoError(t, err, "rename=%d trips=%d", moved, renameTrips)
	require.GreaterOrEqual(t, moved, 8, "rename=%d trips=%d", moved, renameTrips)
	require.Equal(t, int64(1), renameTrips, "rename=%d trips=%d", moved, renameTrips)
	keys, err := c.Keys(ctx, "table:"+tb.Name+"*").Result()
	require.NoError(t, err, "old keys=%v err=%v", keys, err)
	require.Empty(t, keys, "old keys=%v err=%v", keys, err)
	afterEvents, err := c.XRange(ctx, ntable.ChangesKey("renamed"), "-", "+").Result()
	require.NoError(t, err, "rename changed prior ledger")
	require.Len(t, afterEvents, len(beforeEvents)+1, "rename changed prior ledger")
	require.Equal(t, beforeEvents, afterEvents[:len(beforeEvents)], "rename changed prior ledger")
	var moves []map[string]string
	err = json.Unmarshal([]byte(afterEvents[len(afterEvents)-1].Values["renamed_keys"].(string)), &moves)
	require.NoError(t, err, "rename receipt keys=%v", moves)
	require.Len(t, moves, moved, "rename receipt keys=%v", moves)
	groups, err := c.XInfoGroups(ctx, ntable.ChangesKey("renamed")).Result()
	require.NoError(t, err, "lost stream group: %v", groups)
	require.Len(t, groups, 1, "lost stream group: %v", groups)
	require.Equal(t, "reader", groups[0].Name, "lost stream group: %v", groups)
	for id, epoch := range map[string]string{"old": "0", "new1": "1", "new2": "1"} {
		record, err := c.HGetAll(ctx, ntable.MemberKey(id)).Result()
		require.NoError(t, err, "%s record=%v", id, record)
		require.Equal(t, epoch, record["epoch"], "%s record=%v", id, record)
		require.Equal(t, "r:a", record["place:renamed"], "%s record=%v", id, record)
		require.Equal(t, "", record["place:"+tb.Name], "%s record=%v", id, record)
	}
	historical, err := ntable.ReadAt(ctx, c, "renamed", 0)
	require.NoError(t, err)
	old.Name = "renamed"
	for i := range old.Rows {
		for j := range old.Rows[i].Cells {
			old.Rows[i].Cells[j].Key = strings.Replace(old.Rows[i].Cells[j].Key, "table:"+tb.Name+":", "table:renamed:", 1)
		}
	}
	require.Equal(t, historical, old, "historical snapshot changed:\n%#v\n%#v", old, historical)
	active, err := ntable.Read(ctx, c, "renamed")
	require.NoError(t, err)
	require.Equal(t, "active", active.Rows[0].Texts["status"], "lost current metadata: %#v", active)
	require.True(t, active.Rows[0].Hidden, "lost current metadata: %#v", active)
	require.Equal(t, "all", active.FooterLabel, "lost current metadata: %#v", active)
	require.True(t, active.IsHidden("b"), "lost current metadata: %#v", active)
	require.Equal(t, int64(1), c.ZCard(ctx, "external:x").Val(), "rename/reshape wrote external set")
	report, err := ntable.Check(ctx, c, "renamed")
	require.NoError(t, err, "check=%+v", report)
	require.Equal(t, uint64(2), report.Members, "check=%+v", report)
}

func TestTableEditRefusalsDoNotChangeAnything(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"stale-set", "stale-text", "stale-rows-add", "stale-rows-hide", "stale-members", "rename-collision", "rename-stream-type", "rename-stream-full", "rename-acl", "shape-text", "rows-late-type", "rows-hide-late-type", "members-add-late-placed", "members-remove-late-epoch", "members-move-late-missing", "members-duplicate", "view-registry-type", "view-late-missing"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			c, tb := editFixture(t)
			ctx := context.Background()
			writer := c
			_, err := ntable.CellsAdd(ctx, c, tb.Name, "r", "a", 4, []string{"m1", "m2"})
			require.NoError(t, err)
			var call func() error
			switch mode {
			case "stale-set", "stale-text", "stale-rows-add", "stale-rows-hide", "stale-members":
				require.NoError(t, c.HSet(ctx, tb.EpochKey, "n", 1).Err())
				switch mode {
				case "stale-set":
					call = func() error { _, e := ntable.Set(ctx, c, tb.Name, ntable.SetOpts{Rename: "new"}); return e }
				case "stale-text":
					call = func() error {
						_, e := ntable.RowSet(ctx, c, tb.Name, "r", map[string]string{"status": "bad"})
						return e
					}
				case "stale-rows-add":
					call = func() error { _, e := ntable.RowsAdd(ctx, c, tb.Name, []string{"r", "s"}); return e }
				case "stale-rows-hide":
					call = func() error { _, e := ntable.RowsHide(ctx, c, tb.Name, true, []string{"r", "s"}); return e }
				case "stale-members":
					call = func() error { _, e := ntable.CellsMove(ctx, c, tb.Name, "r", "a", "b", []string{"m1", "m2"}); return e }
				}
			case "rename-collision", "rename-stream-type", "rename-stream-full", "rename-acl":
				switch mode {
				case "rename-collision":
					require.NoError(t, c.HSet(ctx, "table:new:identity", "member_prefix", "table::member:").Err())
				case "rename-stream-type":
					require.NoError(t, c.Del(ctx, ntable.ChangesKey(tb.Name)).Err())
					require.NoError(t, c.Set(ctx, ntable.ChangesKey(tb.Name), "bad", 0).Err())
				case "rename-stream-full":
					require.NoError(t, c.XAdd(ctx, &redis.XAddArgs{Stream: ntable.ChangesKey(tb.Name), ID: "18446744073709551615-18446744073709551615", Values: map[string]any{"full": "1"}}).Err())
				case "rename-acl":
					require.NoError(t, c.ACLSetUser(ctx, "limited", "on", ">pw", "~*", "&*", "+@all", "-rename").Err())
					options := *c.Options()
					options.Username, options.Password = "limited", "pw"
					writer = redis.NewClient(&options)
					t.Cleanup(func() {
						assert.NoError(t, writer.Close())
					})
				}
				call = func() error { _, e := ntable.Set(ctx, writer, tb.Name, ntable.SetOpts{Rename: "new"}); return e }
			case "shape-text":
				_, err := ntable.RowSet(ctx, c, tb.Name, "r", map[string]string{"status": "keep"})
				require.NoError(t, err)
				cols, err := ntable.ParseColumns("a,b,x")
				require.NoError(t, err)
				call = func() error { _, e := ntable.Set(ctx, c, tb.Name, ntable.SetOpts{Columns: cols}); return e }
			case "rows-late-type", "rows-hide-late-type":
				require.NoError(t, c.Set(ctx, ntable.RowKey(tb.Name, "s"), "wrong type", 0).Err())
				if mode == "rows-late-type" {
					call = func() error {
						_, e := ntable.RowsAddWithSpec(ctx, c, tb.Name, []string{"r", "s"}, ntable.RowSpec{Label: "bad partial"})
						return e
					}
				} else {
					call = func() error { _, e := ntable.RowsHide(ctx, c, tb.Name, true, []string{"r", "s"}); return e }
				}
			case "members-add-late-placed":
				call = func() error { _, e := ntable.CellsAdd(ctx, c, tb.Name, "r", "a", 5, []string{"new", "m1"}); return e }
			case "members-remove-late-epoch":
				require.NoError(t, c.HSet(ctx, ntable.MemberKey("m2"), "epoch", "1").Err())
				call = func() error { _, e := ntable.CellsRemove(ctx, c, tb.Name, "r", "a", []string{"m1", "m2"}); return e }
			case "members-move-late-missing":
				call = func() error {
					_, e := ntable.CellsMove(ctx, c, tb.Name, "r", "a", "b", []string{"m1", "missing"})
					return e
				}
			case "members-duplicate":
				call = func() error { _, e := ntable.CellsMove(ctx, c, tb.Name, "r", "a", "b", []string{"m1", "m1"}); return e }
			case "view-registry-type":
				require.NoError(t, c.Set(ctx, "views", "bad", 0).Err())
				call = func() error { return ntable.ViewSet(ctx, c, ntable.View{Name: "v", Tables: []string{tb.Name}}) }
			case "view-late-missing":
				call = func() error {
					return ntable.ViewSet(ctx, c, ntable.View{Name: "v", Tables: []string{tb.Name, "missing"}})
				}
			}
			before := review4456Image(t, c)
			err = call()
			require.Error(t, err, "unsafe edit accepted")
			if strings.HasPrefix(mode, "stale-") {
				require.ErrorIs(t, err, ntable.ErrStale, "stale call=%v", err)
			}
			require.Equal(t, before, review4456Image(t, c), "refusal partially changed store: %v", err)
		})
	}
}

func TestBatchedMembersAndShapeRepair(t *testing.T) {
	t.Parallel()
	c, tb := editFixture(t)
	ctx := context.Background()
	n, e := ntable.CellsAdd(ctx, c, tb.Name, "r", "a", 7, []string{"a", "b", "c"})
	require.NoError(t, e, "add=%d", n)
	require.Equal(t, int64(3), n, "add=%d", n)
	n, e = ntable.CellsMove(ctx, c, tb.Name, "r", "a", "b", []string{"b", "c"})
	require.NoError(t, e, "move=%d", n)
	require.Equal(t, int64(2), n, "move=%d", n)
	n, e = ntable.CellsRemove(ctx, c, tb.Name, "r", "b", []string{"missing", "b", "c"})
	require.NoError(t, e, "remove=%d", n)
	require.Equal(t, int64(0), n, "remove=%d", n)
	for _, id := range []string{"b", "c"} {
		require.False(t, c.HExists(ctx, ntable.MemberKey(id), "place:"+tb.Name).Val(), "lost immutable identity for %s", id)
		require.Equal(t, "0", c.HGet(ctx, ntable.MemberKey(id), "epoch").Val(), "lost immutable identity for %s", id)
	}
	// A former pct:avg definition can be repaired under the stricter pooled rule.
	require.NoError(t, c.HSet(ctx, ntable.DefKey(tb.Name), "order", "status,a,b,x,p", "col:p", "pct(a):avg:0:").Err())
	cols, err := ntable.ParseColumns("status:text:none,a,b,x,p:pct(a):pooled")
	require.NoError(t, err)
	_, err = ntable.Set(ctx, c, tb.Name, ntable.SetOpts{Columns: cols, Hide: []string{"a", "b"}})
	require.NoError(t, err)
	_, err = ntable.Set(ctx, c, tb.Name, ntable.SetOpts{Show: []string{"a"}, Hide: []string{"x"}})
	require.NoError(t, err)
	got, err := ntable.Read(ctx, c, tb.Name)
	require.NoError(t, err, "repair/deltas=%#v", got)
	require.Equal(t, []string{"b", "x"}, got.Hidden, "repair/deltas=%#v", got)
	require.Equal(t, int64(1), got.Rows[0].Cells[1].Count, "repair/deltas=%#v", got)
	_, err = ntable.Check(ctx, c, tb.Name)
	require.NoError(t, err)
}

func TestScalarColumnsRefuseOrderedSetMutations(t *testing.T) {
	t.Parallel()
	c, tb := editFixture(t)
	ctx := context.Background()
	cols, err := ntable.ParseColumns("status:text:none,a,b,x,p:pct(a):pooled")
	require.NoError(t, err)
	_, err = ntable.Set(ctx, c, tb.Name, ntable.SetOpts{Columns: cols})
	require.NoError(t, err)
	_, err = ntable.CellAdd(ctx, c, tb.Name, "r", "a", "m", 1)
	require.NoError(t, err)
	before := review4456Image(t, c)
	for _, col := range []string{"status", "p"} {
		for _, call := range []func() error{
			func() error { _, e := ntable.CellAdd(ctx, c, tb.Name, "r", col, "new", 1); return e },
			func() error { _, e := ntable.CellRemove(ctx, c, tb.Name, "r", col, "m"); return e },
			func() error { _, e := ntable.CellMove(ctx, c, tb.Name, "r", "a", col, "m"); return e },
		} {
			require.Error(t, call(), "ordered-set write accepted on %s", col)
			require.Equal(t, before, review4456Image(t, c), "scalar refusal changed store")
		}
	}
}
