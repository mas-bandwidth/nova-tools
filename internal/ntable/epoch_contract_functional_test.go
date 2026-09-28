//go:build functional

package ntable_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

func epochFixture(t *testing.T) (*redis.Client, ntable.Table) {
	t.Helper()
	_, c := live(t)
	cols, err := ntable.ParseColumns("ready,working")
	if err != nil {
		t.Fatal(err)
	}
	tb := ntable.Table{Name: "epoch-test", Columns: cols, EpochKey: "domain:epoch"}
	ctx := context.Background()
	if err := ntable.Create(ctx, c, tb, now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, tb.Name, "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellAdd(ctx, c, tb.Name, "build", "ready", "old", 7); err != nil {
		t.Fatal(err)
	}
	return c, tb
}

func TestEpochTransitionKeepsHistoryAndRejectsEveryStaleWrite(t *testing.T) {
	t.Parallel()
	c, tb := epochFixture(t)
	ctx := context.Background()
	old, err := ntable.ReadAt(ctx, c, tb.Name, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, tb.EpochKey, "n", 1).Err(); err != nil {
		t.Fatal(err)
	}
	fresh, err := ntable.Read(ctx, c, tb.Name)
	if err != nil || fresh.Epoch != 1 || len(fresh.Rows) != 0 || !ntable.SameDefinition(old, fresh) {
		t.Fatalf("fresh epoch = %#v, %v", fresh, err)
	}
	calls := map[string]func() error{
		"create":          func() error { return ntable.Create(ctx, c, tb, now) },
		"bind":            func() error { return ntable.Bind(ctx, c, tb, now) },
		"row-add":         func() error { _, e := ntable.RowAdd(ctx, c, tb.Name, "build", ntable.RowSpec{}); return e },
		"row-del":         func() error { _, e := ntable.RowDel(ctx, c, tb.Name, "build"); return e },
		"cell-add":        func() error { _, e := ntable.CellAdd(ctx, c, tb.Name, "build", "ready", "new", 1); return e },
		"cell-remove":     func() error { _, e := ntable.CellRemove(ctx, c, tb.Name, "build", "ready", "old"); return e },
		"cell-move":       func() error { _, e := ntable.CellMove(ctx, c, tb.Name, "build", "ready", "working", "old"); return e },
		"clear":           func() error { _, e := ntable.Clear(ctx, c, tb.Name); return e },
		"drop":            func() error { _, e := ntable.Drop(ctx, c, tb.Name); return e },
		"drop-definition": func() error { _, e := ntable.DropDefinition(ctx, c, tb.Name); return e },
		"member-create":   func() error { return ntable.MemberCreate(ctx, c, tb.Name, "new") },
	}
	before := memberStoreImage(t, c)
	for name, call := range calls {
		if err := call(); !errors.Is(err, ntable.ErrStale) {
			t.Fatalf("%s = %v, want stale", name, err)
		}
		if !reflect.DeepEqual(before, memberStoreImage(t, c)) {
			t.Fatalf("%s stale write changed store", name)
		}
	}
	opts := ntable.WriteOptions{Epoch: 1}
	row, err := ntable.RowAdd(ctx, c, tb.Name, "build", ntable.RowSpec{}, opts)
	if err != nil || row.Cells[0].Key != ntable.CellKeyAt(tb.Name, "build", "ready", 1) {
		t.Fatalf("epoch row = %#v, %v", row, err)
	}
	before = memberStoreImage(t, c)
	if _, err := ntable.CellAdd(ctx, c, tb.Name, "build", "ready", "old", 9, opts); !errors.Is(err, ntable.ErrMemberEpoch) {
		t.Fatalf("old identity = %v", err)
	}
	if err := ntable.MemberCreate(ctx, c, tb.Name, "old", opts); !errors.Is(err, ntable.ErrMemberEpoch) {
		t.Fatalf("reused ID = %v", err)
	}
	if !reflect.DeepEqual(before, memberStoreImage(t, c)) {
		t.Fatal("old member refusal changed store")
	}
	if _, err := ntable.CellAdd(ctx, c, tb.Name, "build", "ready", "new", 9, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellMove(ctx, c, tb.Name, "build", "ready", "working", "new", opts); err != nil {
		t.Fatal(err)
	}
	historical, err := ntable.ReadAt(ctx, c, tb.Name, 0)
	if err != nil || !reflect.DeepEqual(old, historical) {
		t.Fatalf("history changed: before=%#v after=%#v err=%v", old, historical, err)
	}
	if got := c.HGet(ctx, ntable.MemberKey("old"), "place:"+tb.Name).Val(); got != "build:ready" {
		t.Fatalf("old record changed: %s", got)
	}
}

func TestEpochDropTemplateAndMemberIdentity(t *testing.T) {
	t.Parallel()
	c, tb := epochFixture(t)
	ctx := context.Background()
	old, err := ntable.ReadAt(ctx, c, tb.Name, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, tb.EpochKey, "n", 1).Err(); err != nil {
		t.Fatal(err)
	}
	opts := ntable.WriteOptions{Epoch: 1}
	if _, err := ntable.Drop(ctx, c, tb.Name, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.Read(ctx, c, tb.Name); !errors.Is(err, ntable.ErrNoTable) {
		t.Fatalf("dropped presence = %v", err)
	}
	if !c.HExists(ctx, ntable.DefKey(tb.Name), "order").Val() {
		t.Fatal("drop lost template")
	}
	if err := ntable.Create(ctx, c, tb, now, opts); err != nil {
		t.Fatal(err)
	}
	if err := ntable.MemberCreate(ctx, c, tb.Name, "unplaced", opts); err != nil {
		t.Fatal(err)
	}
	before := memberStoreImage(t, c)
	if err := ntable.MemberCreate(ctx, c, tb.Name, "unplaced", opts); !errors.Is(err, ntable.ErrMemberExists) {
		t.Fatalf("duplicate create = %v", err)
	}
	if !reflect.DeepEqual(before, memberStoreImage(t, c)) {
		t.Fatal("duplicate create changed store")
	}
	if _, err := ntable.DropDefinition(ctx, c, tb.Name, opts); err != nil {
		t.Fatal(err)
	}
	if c.Exists(ctx, ntable.DefKey(tb.Name)).Val() != 0 {
		t.Fatal("explicit definition drop retained template")
	}
	history, err := ntable.ReadAt(ctx, c, tb.Name, 0)
	if err != nil || !reflect.DeepEqual(old, history) {
		t.Fatalf("template drop erased history: %#v %v", history, err)
	}
	if epoch := c.HGet(ctx, ntable.MemberKey("unplaced"), "epoch").Val(); epoch != "1" {
		t.Fatalf("unplaced identity lost: %s", epoch)
	}
	if err := c.HSet(ctx, tb.EpochKey, "n", 2).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.Read(ctx, c, tb.Name); !errors.Is(err, ntable.ErrNoTable) {
		t.Fatalf("removed template reappeared: %v", err)
	}
	if err := ntable.Create(ctx, c, tb, now, ntable.WriteOptions{Epoch: 2}); err != nil {
		t.Fatal(err)
	}
}

func TestEpochCustomRecordsAndExactLargeEpoch(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	cols, err := ntable.ParseColumns("ready")
	if err != nil {
		t.Fatal(err)
	}
	tb := ntable.Table{Name: "task-view", Columns: cols, EpochKey: "domain:epoch", EpochField: "generation", MemberPrefix: "task:"}
	const epoch = uint64(18446744073709551615)
	if err := c.HSet(ctx, tb.EpochKey, tb.EpochField, "18446744073709551615").Err(); err != nil {
		t.Fatal(err)
	}
	opts := ntable.WriteOptions{Epoch: epoch}
	if err := ntable.Create(ctx, c, tb, now, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, tb.Name, "build", ntable.RowSpec{}, opts); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, "task:m1", "epoch", "18446744073709551615", "title", "existing task").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellAdd(ctx, c, tb.Name, "build", "ready", "m1", 3, opts); err != nil {
		t.Fatal(err)
	}
	got, err := ntable.Read(ctx, c, tb.Name)
	if err != nil || got.Epoch != epoch || got.Rows[0].Cells[0].Key != ntable.CellKeyAt(tb.Name, "build", "ready", epoch) {
		t.Fatalf("large epoch = %#v, %v", got, err)
	}
	record, err := c.HGetAll(ctx, "task:m1").Result()
	if err != nil || record["title"] != "existing task" || record["place:"+tb.Name] != "build:ready" || c.Exists(ctx, ntable.MemberKey("m1")).Val() != 0 {
		t.Fatalf("record binding = %v %v", record, err)
	}
}

func TestTableChangeReceiptAndRefusalAreAtomic(t *testing.T) {
	t.Parallel()
	c, tb := memberFixture(t)
	ctx := context.Background()
	var receipt ntable.Receipt
	opts := ntable.WriteOptions{Actor: "Stella", Fence: "fence-7", Idem: "attempt-9", Receipt: &receipt}
	before := c.HGet(ctx, ntable.RevisionKey(tb.Name), "n").Val()
	if _, err := ntable.CellMove(ctx, c, tb.Name, "build", "ready", "working", "m1", opts); err != nil {
		t.Fatal(err)
	}
	events, err := c.XRangeN(ctx, ntable.ChangesKey(tb.Name), receipt.ID, receipt.ID, 1).Result()
	if err != nil || len(events) != 1 {
		t.Fatalf("receipt event = %v %v", events, err)
	}
	event := events[0].Values
	if event["rev_before"] != before || receipt.After != receipt.Before+1 || event["actor"] != "Stella" || event["fence"] != "fence-7" || event["idem"] != "attempt-9" || event["verb"] != "cell_move" || receipt.Outcome != "changed" {
		t.Fatalf("receipt = %#v event=%v", receipt, event)
	}
	var members []struct{ ID, From, To, Score string }
	if err := json.Unmarshal([]byte(event["members"].(string)), &members); err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].ID != "m1" || members[0].From != "build:ready" || members[0].To != "build:working" || members[0].Score != "7" {
		t.Fatalf("member change = %v", members)
	}
	n := c.XLen(ctx, ntable.ChangesKey(tb.Name)).Val()
	image := memberStoreImage(t, c)
	if _, err := ntable.CellAdd(ctx, c, tb.Name, "build", "ready", "m1", 9, opts); !errors.Is(err, ntable.ErrPlaced) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(image, memberStoreImage(t, c)) {
		t.Fatal("refused placement produced an event or mutation")
	}
	if _, err := ntable.CellRemove(ctx, c, tb.Name, "build", "ready", "absent", opts); err != nil {
		t.Fatal(err)
	}
	if receipt.Outcome != "noop" || c.XLen(ctx, ntable.ChangesKey(tb.Name)).Val() != n+1 {
		t.Fatalf("accepted noop receipt = %#v", receipt)
	}
}

func TestTableReceiptPreflightPreventsPartialMoves(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"stream-type", "stream-full", "revision-type", "revision-range", "denied-xadd", "denied-revision"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			c, tb := memberFixture(t)
			ctx := context.Background()
			writer := c
			switch mode {
			case "stream-type":
				if err := c.Del(ctx, ntable.ChangesKey(tb.Name)).Err(); err != nil {
					t.Fatal(err)
				}
				if err := c.Set(ctx, ntable.ChangesKey(tb.Name), "bad", 0).Err(); err != nil {
					t.Fatal(err)
				}
			case "stream-full":
				if err := c.XAdd(ctx, &redis.XAddArgs{Stream: ntable.ChangesKey(tb.Name), ID: "18446744073709551615-18446744073709551615", Values: map[string]any{"sentinel": "end"}}).Err(); err != nil {
					t.Fatal(err)
				}
			case "revision-type":
				if err := c.Del(ctx, ntable.RevisionKey(tb.Name)).Err(); err != nil {
					t.Fatal(err)
				}
				if err := c.Set(ctx, ntable.RevisionKey(tb.Name), "bad type", 0).Err(); err != nil {
					t.Fatal(err)
				}
			case "revision-range":
				if err := c.HSet(ctx, ntable.RevisionKey(tb.Name), "n", "18446744073709551615").Err(); err != nil {
					t.Fatal(err)
				}
			default:
				deny := "-xadd"
				if mode == "denied-revision" {
					deny = "-hset"
				}
				grants := []string{"on", ">pw", "~*", "&*", "+@all", deny}
				if mode == "denied-revision" {
					grants = append(grants, "(+hset ~table::member:*)", "(+hset ~table:*:definition)")
				}
				if err := c.ACLSetUser(ctx, "limited", grants...).Err(); err != nil {
					t.Fatal(err)
				}
				opts := *c.Options()
				opts.Username, opts.Password = "limited", "pw"
				writer = redis.NewClient(&opts)
				t.Cleanup(func() {
					if err := writer.Close(); err != nil {
						t.Error(err)
					}
				})
			}
			before := memberStoreImage(t, c)
			if _, err := ntable.CellMove(ctx, writer, tb.Name, "build", "ready", "working", "m1"); err == nil || strings.Contains(err.Error(), "unknown function") {
				t.Fatalf("preflight = %v", err)
			}
			if !reflect.DeepEqual(before, memberStoreImage(t, c)) {
				t.Fatal("receipt failure partially moved the member")
			}
		})
	}
}

// Generic table drift detection: external writes forging or tampering with a
// member's placement pointer are detected by Check and refused by CellMove with
// ErrDrift (reverting the forged write turns it red on ErrDrift).
func TestGenericTableDriftDetection(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	cols, err := ntable.ParseColumns("ready")
	if err != nil {
		t.Fatal(err)
	}
	tb := ntable.Table{Name: "tasks", Columns: cols, MemberPrefix: "task:"}
	if err := ntable.Create(ctx, c, tb, now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, tb.Name, "r", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellAdd(ctx, c, tb.Name, "r", "ready", "existing", 1); err != nil {
		t.Fatal(err)
	}
	before := memberStoreImage(t, c)

	// An external write forging a different placement for the existing member is refused by CellMove and detected by Check.
	if err := c.HSet(ctx, "task:existing", "place:tasks", "elsewhere:ready").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellMove(ctx, c, tb.Name, "r", "ready", "ready", "existing"); !errors.Is(err, ntable.ErrDrift) {
		t.Fatalf("forged placement move = %v, want ErrDrift", err)
	}
	if _, err := ntable.Check(ctx, c, tb.Name); !errors.Is(err, ntable.ErrDrift) {
		t.Fatalf("forged placement check = %v, want ErrDrift", err)
	}

	// Restore and verify that an unplaced ghost record forging a placement is detected by Check.
	if err := c.HSet(ctx, "task:existing", "place:tasks", "r:ready").Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, "task:new", "place:tasks", "r:ready").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.Check(ctx, c, tb.Name); !errors.Is(err, ntable.ErrDrift) {
		t.Fatalf("ghost placement check = %v, want ErrDrift", err)
	}
	if err := c.Del(ctx, "task:new").Err(); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(before, memberStoreImage(t, c)) {
		t.Fatal("store did not match expected image after cleanup")
	}
}
