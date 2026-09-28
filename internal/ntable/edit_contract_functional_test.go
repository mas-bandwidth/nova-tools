//go:build functional

package ntable_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	nsstore "github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

func editFixture(t *testing.T) (*redis.Client, ntable.Table) {
	t.Helper()
	_, c := live(t)
	cols, err := ntable.ParseColumns("status:text:none,a,b,x")
	if err != nil {
		t.Fatal(err)
	}
	tb := ntable.Table{Name: "edits", Columns: cols, EpochKey: "domain:epoch"}
	if err := ntable.Create(context.Background(), c, tb, now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowsAdd(context.Background(), c, tb.Name, []string{"r", "s"}); err != nil {
		t.Fatal(err)
	}
	return c, tb
}

func TestTableEditsPreserveEpochHistoryAndRenameLedger(t *testing.T) {
	t.Parallel()
	c, tb := editFixture(t)
	ctx := context.Background()
	if _, err := ntable.CellAdd(ctx, c, tb.Name, "r", "a", "old", 7); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowSet(ctx, c, tb.Name, "r", map[string]string{"status": "old status"}); err != nil {
		t.Fatal(err)
	}
	old, err := ntable.ReadAt(ctx, c, tb.Name, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.XGroupCreate(ctx, ntable.ChangesKey(tb.Name), "reader", "0").Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, tb.EpochKey, "n", 1).Err(); err != nil {
		t.Fatal(err)
	}
	var receipt ntable.Receipt
	opts := ntable.WriteOptions{Epoch: 1, Actor: "edit-test", Fence: "f7", Idem: "i9", Receipt: &receipt}
	trips := nsstore.New(c).CountTrips()
	one := func(name, table string, call func() error) {
		t.Helper()
		before := c.XLen(ctx, ntable.ChangesKey(table)).Val()
		n := trips.N()
		if err := call(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if trips.N()-n != 1 {
			t.Fatalf("%s took %d trips", name, trips.N()-n)
		}
		if receipt.ID == "" || receipt.Epoch != 1 || receipt.After != receipt.Before+1 {
			t.Fatalf("%s receipt=%+v", name, receipt)
		}
		events, err := c.XRangeN(ctx, ntable.ChangesKey(table), receipt.ID, receipt.ID, 1).Result()
		if err != nil || len(events) != 1 || c.XLen(ctx, ntable.ChangesKey(table)).Val() != before+1 {
			t.Fatalf("%s events=%v err=%v", name, events, err)
		}
		e := events[0].Values
		if e["actor"] != "edit-test" || e["fence"] != "f7" || e["idem"] != "i9" || e["epoch"] != "1" {
			t.Fatalf("metadata=%v", e)
		}
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
	if receipt.Outcome != "noop" {
		t.Fatalf("repeated text receipt=%+v", receipt)
	}
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
	if _, err := ntable.RowAdd(ctx, c, tb.Name, "bound", ntable.RowSpec{Binds: map[string]string{"x": "external:x"}}, opts); err != nil {
		t.Fatal(err)
	}
	if err := c.ZAdd(ctx, "external:x", redis.Z{Score: 9, Member: "external"}).Err(); err != nil {
		t.Fatal(err)
	}
	cols, err := ntable.ParseColumns("status:text:none,a,b")
	if err != nil {
		t.Fatal(err)
	}
	beforeEvents, err := c.XRange(ctx, ntable.ChangesKey(tb.Name), "-", "+").Result()
	if err != nil {
		t.Fatal(err)
	}
	n := trips.N()
	moved, err := ntable.Set(ctx, c, tb.Name, ntable.SetOpts{Rename: "renamed", Columns: cols}, opts)
	if err != nil || moved < 8 || trips.N()-n != 1 {
		t.Fatalf("rename=%d err=%v trips=%d", moved, err, trips.N()-n)
	}
	keys, err := c.Keys(ctx, "table:"+tb.Name+"*").Result()
	if err != nil || len(keys) != 0 {
		t.Fatalf("old keys=%v err=%v", keys, err)
	}
	afterEvents, err := c.XRange(ctx, ntable.ChangesKey("renamed"), "-", "+").Result()
	if err != nil || len(afterEvents) != len(beforeEvents)+1 || !reflect.DeepEqual(beforeEvents, afterEvents[:len(beforeEvents)]) {
		t.Fatalf("rename changed prior ledger: %v", err)
	}
	var moves []map[string]string
	if err := json.Unmarshal([]byte(afterEvents[len(afterEvents)-1].Values["renamed_keys"].(string)), &moves); err != nil || len(moves) != moved {
		t.Fatalf("rename receipt keys=%v err=%v", moves, err)
	}
	groups, err := c.XInfoGroups(ctx, ntable.ChangesKey("renamed")).Result()
	if err != nil || len(groups) != 1 || groups[0].Name != "reader" {
		t.Fatalf("lost stream group: %v %v", groups, err)
	}
	for id, epoch := range map[string]string{"old": "0", "new1": "1", "new2": "1"} {
		record, err := c.HGetAll(ctx, ntable.MemberKey(id)).Result()
		if err != nil || record["epoch"] != epoch || record["place:renamed"] != "r:a" || record["place:"+tb.Name] != "" {
			t.Fatalf("%s record=%v err=%v", id, record, err)
		}
	}
	historical, err := ntable.ReadAt(ctx, c, "renamed", 0)
	if err != nil {
		t.Fatal(err)
	}
	old.Name = "renamed"
	for i := range old.Rows {
		for j := range old.Rows[i].Cells {
			old.Rows[i].Cells[j].Key = strings.Replace(old.Rows[i].Cells[j].Key, "table:"+tb.Name+":", "table:renamed:", 1)
		}
	}
	if !reflect.DeepEqual(old, historical) {
		t.Fatalf("historical snapshot changed:\n%#v\n%#v", old, historical)
	}
	active, err := ntable.Read(ctx, c, "renamed")
	if err != nil {
		t.Fatal(err)
	}
	if active.Rows[0].Texts["status"] != "active" || !active.Rows[0].Hidden || active.FooterLabel != "all" || !active.IsHidden("b") {
		t.Fatalf("lost current metadata: %#v", active)
	}
	if c.ZCard(ctx, "external:x").Val() != 1 {
		t.Fatal("rename/reshape wrote external set")
	}
	if report, err := ntable.Check(ctx, c, "renamed"); err != nil || report.Members != 2 {
		t.Fatalf("check=%+v %v", report, err)
	}
}

func TestTableEditRefusalsDoNotChangeAnything(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"stale-set", "stale-text", "stale-rows-add", "stale-rows-hide", "stale-members", "rename-collision", "rename-stream-type", "rename-stream-full", "rename-acl", "shape-text", "rows-late-type", "rows-hide-late-type", "members-add-late-placed", "members-remove-late-epoch", "members-move-late-missing", "members-duplicate", "view-registry-type", "view-late-missing"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			c, tb := editFixture(t)
			ctx := context.Background()
			writer := c
			if _, err := ntable.CellsAdd(ctx, c, tb.Name, "r", "a", 4, []string{"m1", "m2"}); err != nil {
				t.Fatal(err)
			}
			var call func() error
			switch mode {
			case "stale-set", "stale-text", "stale-rows-add", "stale-rows-hide", "stale-members":
				if err := c.HSet(ctx, tb.EpochKey, "n", 1).Err(); err != nil {
					t.Fatal(err)
				}
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
					if err := c.HSet(ctx, "table:new:identity", "member_prefix", "table::member:").Err(); err != nil {
						t.Fatal(err)
					}
				case "rename-stream-type":
					if err := c.Del(ctx, ntable.ChangesKey(tb.Name)).Err(); err != nil {
						t.Fatal(err)
					}
					if err := c.Set(ctx, ntable.ChangesKey(tb.Name), "bad", 0).Err(); err != nil {
						t.Fatal(err)
					}
				case "rename-stream-full":
					if err := c.XAdd(ctx, &redis.XAddArgs{Stream: ntable.ChangesKey(tb.Name), ID: "18446744073709551615-18446744073709551615", Values: map[string]any{"full": "1"}}).Err(); err != nil {
						t.Fatal(err)
					}
				case "rename-acl":
					if err := c.ACLSetUser(ctx, "limited", "on", ">pw", "~*", "&*", "+@all", "-rename").Err(); err != nil {
						t.Fatal(err)
					}
					options := *c.Options()
					options.Username, options.Password = "limited", "pw"
					writer = redis.NewClient(&options)
					t.Cleanup(func() {
						if e := writer.Close(); e != nil {
							t.Error(e)
						}
					})
				}
				call = func() error { _, e := ntable.Set(ctx, writer, tb.Name, ntable.SetOpts{Rename: "new"}); return e }
			case "shape-text":
				if _, err := ntable.RowSet(ctx, c, tb.Name, "r", map[string]string{"status": "keep"}); err != nil {
					t.Fatal(err)
				}
				cols, err := ntable.ParseColumns("a,b,x")
				if err != nil {
					t.Fatal(err)
				}
				call = func() error { _, e := ntable.Set(ctx, c, tb.Name, ntable.SetOpts{Columns: cols}); return e }
			case "rows-late-type", "rows-hide-late-type":
				if err := c.Set(ctx, ntable.RowKey(tb.Name, "s"), "wrong type", 0).Err(); err != nil {
					t.Fatal(err)
				}
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
				if err := c.HSet(ctx, ntable.MemberKey("m2"), "epoch", "1").Err(); err != nil {
					t.Fatal(err)
				}
				call = func() error { _, e := ntable.CellsRemove(ctx, c, tb.Name, "r", "a", []string{"m1", "m2"}); return e }
			case "members-move-late-missing":
				call = func() error {
					_, e := ntable.CellsMove(ctx, c, tb.Name, "r", "a", "b", []string{"m1", "missing"})
					return e
				}
			case "members-duplicate":
				call = func() error { _, e := ntable.CellsMove(ctx, c, tb.Name, "r", "a", "b", []string{"m1", "m1"}); return e }
			case "view-registry-type":
				if err := c.Set(ctx, "views", "bad", 0).Err(); err != nil {
					t.Fatal(err)
				}
				call = func() error { return ntable.ViewSet(ctx, c, ntable.View{Name: "v", Tables: []string{tb.Name}}) }
			case "view-late-missing":
				call = func() error {
					return ntable.ViewSet(ctx, c, ntable.View{Name: "v", Tables: []string{tb.Name, "missing"}})
				}
			}
			before := review4456Image(t, c)
			err := call()
			if err == nil {
				t.Fatal("unsafe edit accepted")
			}
			if strings.HasPrefix(mode, "stale-") && !errors.Is(err, ntable.ErrStale) {
				t.Fatalf("stale call=%v", err)
			}
			if !reflect.DeepEqual(before, review4456Image(t, c)) {
				t.Fatalf("refusal partially changed store: %v", err)
			}
		})
	}
}

func TestBatchedMembersAndShapeRepair(t *testing.T) {
	t.Parallel()
	c, tb := editFixture(t)
	ctx := context.Background()
	if n, e := ntable.CellsAdd(ctx, c, tb.Name, "r", "a", 7, []string{"a", "b", "c"}); e != nil || n != 3 {
		t.Fatalf("add=%d %v", n, e)
	}
	if n, e := ntable.CellsMove(ctx, c, tb.Name, "r", "a", "b", []string{"b", "c"}); e != nil || n != 2 {
		t.Fatalf("move=%d %v", n, e)
	}
	if n, e := ntable.CellsRemove(ctx, c, tb.Name, "r", "b", []string{"missing", "b", "c"}); e != nil || n != 0 {
		t.Fatalf("remove=%d %v", n, e)
	}
	for _, id := range []string{"b", "c"} {
		if c.HExists(ctx, ntable.MemberKey(id), "place:"+tb.Name).Val() || c.HGet(ctx, ntable.MemberKey(id), "epoch").Val() != "0" {
			t.Fatalf("lost immutable identity for %s", id)
		}
	}
	// A former pct:avg definition can be repaired under the stricter pooled rule.
	if err := c.HSet(ctx, ntable.DefKey(tb.Name), "order", "status,a,b,x,p", "col:p", "pct(a):avg:0:").Err(); err != nil {
		t.Fatal(err)
	}
	cols, err := ntable.ParseColumns("status:text:none,a,b,x,p:pct(a):pooled")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.Set(ctx, c, tb.Name, ntable.SetOpts{Columns: cols, Hide: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.Set(ctx, c, tb.Name, ntable.SetOpts{Show: []string{"a"}, Hide: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	got, err := ntable.Read(ctx, c, tb.Name)
	if err != nil || !reflect.DeepEqual(got.Hidden, []string{"b", "x"}) || got.Rows[0].Cells[1].Count != 1 {
		t.Fatalf("repair/deltas=%#v %v", got, err)
	}
	if _, err := ntable.Check(ctx, c, tb.Name); err != nil {
		t.Fatal(err)
	}
}

func TestScalarColumnsRefuseOrderedSetMutations(t *testing.T) {
	t.Parallel()
	c, tb := editFixture(t)
	ctx := context.Background()
	cols, err := ntable.ParseColumns("status:text:none,a,b,x,p:pct(a):pooled")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.Set(ctx, c, tb.Name, ntable.SetOpts{Columns: cols}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellAdd(ctx, c, tb.Name, "r", "a", "m", 1); err != nil {
		t.Fatal(err)
	}
	before := review4456Image(t, c)
	for _, col := range []string{"status", "p"} {
		for _, call := range []func() error{
			func() error { _, e := ntable.CellAdd(ctx, c, tb.Name, "r", col, "new", 1); return e },
			func() error { _, e := ntable.CellRemove(ctx, c, tb.Name, "r", col, "m"); return e },
			func() error { _, e := ntable.CellMove(ctx, c, tb.Name, "r", "a", col, "m"); return e },
		} {
			if e := call(); e == nil {
				t.Fatalf("ordered-set write accepted on %s", col)
			}
			if !reflect.DeepEqual(before, review4456Image(t, c)) {
				t.Fatal("scalar refusal changed store")
			}
		}
	}
}
