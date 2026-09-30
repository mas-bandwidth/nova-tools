//go:build functional

package ntable_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func epochFixture(t *testing.T) (*redis.Client, ntable.Table) {
	t.Helper()
	_, c := live(t)
	cols, err := ntable.ParseColumns("ready,working")
	require.NoError(t, err)
	tb := ntable.Table{Name: "epoch-test", Columns: cols, EpochKey: "domain:epoch"}
	newTable(t, c, tb).rows("build").cell("build", "ready", "old", 7)
	return c, tb
}

func TestEpochTransitionKeepsHistoryAndRejectsEveryStaleWrite(t *testing.T) {
	t.Parallel()
	c, tb := epochFixture(t)
	ctx := context.Background()
	old, err := ntable.ReadAt(ctx, c, tb.Name, 0)
	require.NoError(t, err)
	require.NoError(t, c.HSet(ctx, tb.EpochKey, "n", 1).Err())
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
	before := storeImage(t, c)
	for name, call := range calls {
		if err := call(); !errors.Is(err, ntable.ErrStale) {
			t.Fatalf("%s = %v, want stale", name, err)
		}
		require.Equal(t, before, storeImage(t, c), "%s stale write changed store", name)
	}
	opts := ntable.WriteOptions{Epoch: 1}
	row, err := ntable.RowAdd(ctx, c, tb.Name, "build", ntable.RowSpec{}, opts)
	require.NoError(t, err, "epoch row = %#v, %v", row, err)
	require.Equal(t, ntable.CellKeyAt(tb.Name, "build", "ready", 1), row.Cells[0].Key, "epoch row = %#v, %v", row, err)
	before = storeImage(t, c)
	_, err = ntable.CellAdd(ctx, c, tb.Name, "build", "ready", "old", 9, opts)
	require.ErrorIs(t, err, ntable.ErrMemberEpoch, "old identity =")
	require.ErrorIs(t, ntable.MemberCreate(ctx, c, tb.Name, "old", opts), ntable.ErrMemberEpoch, "reused ID =")
	require.Equal(t, before, storeImage(t, c), "old member refusal changed store")
	_, err = ntable.CellAdd(ctx, c, tb.Name, "build", "ready", "new", 9, opts)
	require.NoError(t, err)
	_, err = ntable.CellMove(ctx, c, tb.Name, "build", "ready", "working", "new", opts)
	require.NoError(t, err)
	historical, err := ntable.ReadAt(ctx, c, tb.Name, 0)
	require.NoError(t, err, "history changed: before=%#v after=%#v err=%v", old, historical, err)
	require.Equal(t, historical, old, "history changed: before=%#v after=%#v err=%v", old, historical, err)
	got := c.HGet(ctx, ntable.MemberKey("old"), "place:"+tb.Name).Val()
	require.Equal(t, "build:ready", got, "old record changed: %s", got)
}

func TestEpochDropTemplateAndMemberIdentity(t *testing.T) {
	t.Parallel()
	c, tb := epochFixture(t)
	ctx := context.Background()
	old, err := ntable.ReadAt(ctx, c, tb.Name, 0)
	require.NoError(t, err)
	require.NoError(t, c.HSet(ctx, tb.EpochKey, "n", 1).Err())
	opts := ntable.WriteOptions{Epoch: 1}
	_, err = ntable.Drop(ctx, c, tb.Name, opts)
	require.NoError(t, err)
	_, err = ntable.Read(ctx, c, tb.Name)
	require.ErrorIs(t, err, ntable.ErrNoTable, "dropped presence =")
	require.True(t, c.HExists(ctx, ntable.DefKey(tb.Name), "order").Val(), "drop lost template")
	require.NoError(t, ntable.Create(ctx, c, tb, now, opts))
	require.NoError(t, ntable.MemberCreate(ctx, c, tb.Name, "unplaced", opts))
	before := storeImage(t, c)
	require.ErrorIs(t, ntable.MemberCreate(ctx, c, tb.Name, "unplaced", opts), ntable.ErrMemberExists, "duplicate create =")
	require.Equal(t, before, storeImage(t, c), "duplicate create changed store")
	_, err = ntable.DropDefinition(ctx, c, tb.Name, opts)
	require.NoError(t, err)
	require.Equal(t, int64(0), c.Exists(ctx, ntable.DefKey(tb.Name)).Val(), "explicit definition drop retained template")
	history, err := ntable.ReadAt(ctx, c, tb.Name, 0)
	require.NoError(t, err, "template drop erased history: %#v %v", history, err)
	require.Equal(t, history, old, "template drop erased history: %#v %v", history, err)
	epoch := c.HGet(ctx, ntable.MemberKey("unplaced"), "epoch").Val()
	require.Equal(t, "1", epoch, "unplaced identity lost: %s", epoch)
	require.NoError(t, c.HSet(ctx, tb.EpochKey, "n", 2).Err())
	_, err = ntable.Read(ctx, c, tb.Name)
	require.ErrorIs(t, err, ntable.ErrNoTable, "removed template reappeared")
	require.NoError(t, ntable.Create(ctx, c, tb, now, ntable.WriteOptions{Epoch: 2}))
}

func TestEpochCustomRecordsAndExactLargeEpoch(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	cols, err := ntable.ParseColumns("ready")
	require.NoError(t, err)
	tb := ntable.Table{Name: "task-view", Columns: cols, EpochKey: "domain:epoch", EpochField: "generation", MemberPrefix: "task:"}
	const epoch = uint64(18446744073709551615)
	require.NoError(t, c.HSet(ctx, tb.EpochKey, tb.EpochField, "18446744073709551615").Err())
	opts := ntable.WriteOptions{Epoch: epoch}
	require.NoError(t, ntable.Create(ctx, c, tb, now, opts))
	_, err = ntable.RowAdd(ctx, c, tb.Name, "build", ntable.RowSpec{}, opts)
	require.NoError(t, err)
	require.NoError(t, c.HSet(ctx, "task:m1", "epoch", "18446744073709551615", "title", "existing task").Err())
	_, err = ntable.CellAdd(ctx, c, tb.Name, "build", "ready", "m1", 3, opts)
	require.NoError(t, err)
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
	require.NoError(t, err, "receipt event = %v %v", events, err)
	require.Len(t, events, 1, "receipt event = %v %v", events, err)
	event := events[0].Values
	if event["rev_before"] != before || receipt.After != receipt.Before+1 || event["actor"] != "Stella" || event["fence"] != "fence-7" || event["idem"] != "attempt-9" || event["verb"] != "cell_move" || receipt.Outcome != "changed" {
		t.Fatalf("receipt = %#v event=%v", receipt, event)
	}
	var members []struct{ ID, From, To, Score string }
	require.NoError(t, json.Unmarshal([]byte(event["members"].(string)), &members))
	if len(members) != 1 || members[0].ID != "m1" || members[0].From != "build:ready" || members[0].To != "build:working" || members[0].Score != "7" {
		t.Fatalf("member change = %v", members)
	}
	n := c.XLen(ctx, ntable.ChangesKey(tb.Name)).Val()
	image := storeImage(t, c)
	_, err = ntable.CellAdd(ctx, c, tb.Name, "build", "ready", "m1", 9, opts)
	require.ErrorIs(t, err, ntable.ErrPlaced)
	require.Equal(t, storeImage(t, c), image, "refused placement produced an event or mutation")
	_, err = ntable.CellRemove(ctx, c, tb.Name, "build", "ready", "absent", opts)
	require.NoError(t, err)
	require.Equal(t, "noop", receipt.Outcome, "accepted noop receipt = %#v", receipt)
	require.Equal(t, n+1, c.XLen(ctx, ntable.ChangesKey(tb.Name)).Val(), "accepted noop receipt = %#v", receipt)
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
				require.NoError(t, c.Del(ctx, ntable.ChangesKey(tb.Name)).Err())
				require.NoError(t, c.Set(ctx, ntable.ChangesKey(tb.Name), "bad", 0).Err())
			case "stream-full":
				require.NoError(t, c.XAdd(ctx, &redis.XAddArgs{Stream: ntable.ChangesKey(tb.Name), ID: "18446744073709551615-18446744073709551615", Values: map[string]any{"sentinel": "end"}}).Err())
			case "revision-type":
				require.NoError(t, c.Del(ctx, ntable.RevisionKey(tb.Name)).Err())
				require.NoError(t, c.Set(ctx, ntable.RevisionKey(tb.Name), "bad type", 0).Err())
			case "revision-range":
				require.NoError(t, c.HSet(ctx, ntable.RevisionKey(tb.Name), "n", "18446744073709551615").Err())
			default:
				deny := "-xadd"
				if mode == "denied-revision" {
					deny = "-hset"
				}
				grants := []string{"on", ">pw", "~*", "&*", "+@all", deny}
				if mode == "denied-revision" {
					grants = append(grants, "(+hset ~table::member:*)", "(+hset ~table:*:definition)")
				}
				require.NoError(t, c.ACLSetUser(ctx, "limited", grants...).Err())
				opts := *c.Options()
				opts.Username, opts.Password = "limited", "pw"
				writer = redis.NewClient(&opts)
				t.Cleanup(func() {
					assert.NoError(t, writer.Close())
				})
			}
			before := storeImage(t, c)
			if _, err := ntable.CellMove(ctx, writer, tb.Name, "build", "ready", "working", "m1"); err == nil || strings.Contains(err.Error(), "unknown function") {
				t.Fatalf("preflight = %v", err)
			}
			require.Equal(t, before, storeImage(t, c), "receipt failure partially moved the member")
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
	require.NoError(t, err)
	tb := ntable.Table{Name: "tasks", Columns: cols, MemberPrefix: "task:"}
	newTable(t, c, tb).rows("r").cell("r", "ready", "existing", 1)
	before := storeImage(t, c)

	// An external write forging a different placement for the existing member is refused by CellMove and detected by Check.
	require.NoError(t, c.HSet(ctx, "task:existing", "place:tasks", "elsewhere:ready").Err())
	_, err = ntable.CellMove(ctx, c, tb.Name, "r", "ready", "ready", "existing")
	require.ErrorIs(t, err, ntable.ErrDrift, "forged placement move = %v, want ErrDrift", err)
	_, err = ntable.Check(ctx, c, tb.Name)
	require.ErrorIs(t, err, ntable.ErrDrift, "forged placement check = %v, want ErrDrift", err)

	// Restore and verify that an unplaced ghost record forging a placement is detected by Check.
	require.NoError(t, c.HSet(ctx, "task:existing", "place:tasks", "r:ready").Err())
	require.NoError(t, c.HSet(ctx, "task:new", "place:tasks", "r:ready").Err())
	_, err = ntable.Check(ctx, c, tb.Name)
	require.ErrorIs(t, err, ntable.ErrDrift, "ghost placement check = %v, want ErrDrift", err)
	require.NoError(t, c.Del(ctx, "task:new").Err())

	require.Equal(t, before, storeImage(t, c), "store did not match expected image after cleanup")
}
