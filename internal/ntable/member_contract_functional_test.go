//go:build functional

package ntable_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

// Refusals are checked against every key in this disposable store, so a
// refused shape or placement must not leave a partial record/set update.
func memberStoreImage(t *testing.T, c *redis.Client) map[string]string {
	t.Helper()
	ctx := context.Background()
	keys, err := c.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		v, err := c.Dump(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
		out[key] = v
	}
	return out
}

func memberFixture(t *testing.T) (*redis.Client, ntable.Table) {
	t.Helper()
	_, c := live(t)
	ctx := context.Background()
	cols, err := ntable.ParseColumns("ready,working")
	if err != nil {
		t.Fatal(err)
	}
	tb := ntable.Table{Name: "placement", Columns: cols}
	if err := ntable.Create(ctx, c, tb, now); err != nil {
		t.Fatal(err)
	}
	for _, row := range []string{"build", "test"} {
		if _, err := ntable.RowAdd(ctx, c, tb.Name, row, ntable.RowSpec{}); err != nil {
			t.Fatal(err)
		}
		tb.Rows = append(tb.Rows, ntable.NewRow(tb, row))
	}
	if _, err := ntable.CellAdd(ctx, c, tb.Name, "build", "ready", "m1", 7); err != nil {
		t.Fatal(err)
	}
	return c, tb
}

// These are the actual-code counterexamples reversed into the desired
// contract. The baseline accepts every bad call; the corrected function
// must refuse it before changing any key, even when called without Go.
func TestMemberContractRefusalsPreserveStore(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		call func(context.Context, *redis.Client, ntable.Table) error
	}{
		{"duplicate-cell", func(ctx context.Context, c *redis.Client, tb ntable.Table) error {
			_, err := ntable.CellAdd(ctx, c, tb.Name, "build", "working", "m1", 99)
			return err
		}},
		{"duplicate-row", func(ctx context.Context, c *redis.Client, tb ntable.Table) error {
			_, err := ntable.CellAdd(ctx, c, tb.Name, "test", "ready", "m1", 99)
			return err
		}},
		{"repeat-add-keeps-score", func(ctx context.Context, c *redis.Client, tb ntable.Table) error {
			_, err := ntable.CellAdd(ctx, c, tb.Name, "build", "ready", "m1", 99)
			return err
		}},
		{"bind-removes-occupied-row", func(ctx context.Context, c *redis.Client, tb ntable.Table) error {
			tb.Rows = tb.Rows[1:]
			return ntable.Bind(ctx, c, tb, now)
		}},
		{"bind-hides-owned-cell", func(ctx context.Context, c *redis.Client, tb ntable.Table) error {
			tb.Rows[0].Cells[0] = ntable.Cell{Key: "source:ready", Bound: true}
			return ntable.Bind(ctx, c, tb, now)
		}},
		{"row-add-hides-owned-cell", func(ctx context.Context, c *redis.Client, tb ntable.Table) error {
			_, err := ntable.RowAdd(ctx, c, tb.Name, "build", ntable.RowSpec{Binds: map[string]string{"ready": "source:ready"}})
			return err
		}},
		{"bind-aliases-owned-cell", func(ctx context.Context, c *redis.Client, tb ntable.Table) error {
			tb.Rows[1].Cells[0] = ntable.Cell{Key: ntable.CellKey(tb.Name, "build", "ready"), Bound: true}
			return ntable.Bind(ctx, c, tb, now)
		}},
		{"row-add-aliases-future-table", func(ctx context.Context, c *redis.Client, tb ntable.Table) error {
			_, err := ntable.RowAdd(ctx, c, tb.Name, "test", ntable.RowSpec{Binds: map[string]string{"ready": ntable.CellKey("future", "build", "ready")}})
			return err
		}},
		{"raw-function-duplicate", func(ctx context.Context, c *redis.Client, tb ntable.Table) error {
			reply, err := c.FCall(ctx, ntable.FnCellAdd, []string{ntable.DefKey(tb.Name)}, tb.Name, "build", "working", "m1", "99", `{"epoch":"0"}`).Slice()
			if err != nil {
				return err
			}
			if len(reply) > 1 && reply[0] == "REFUSED" && reply[1] == "PLACED" {
				return &storeContractRefusal{}
			}
			return nil
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, tb := memberFixture(t)
			before := memberStoreImage(t, c)
			err := tc.call(context.Background(), c, tb)
			if err == nil {
				t.Fatal("unsafe placement/shape accepted; wanted an unchanged-store refusal")
			}
			if strings.Contains(err.Error(), "ERR unknown") || strings.Contains(err.Error(), "NOPERM") {
				t.Fatalf("unrelated failure is not a contract refusal: %v", err)
			}
			if after := memberStoreImage(t, c); !reflect.DeepEqual(before, after) {
				t.Fatal("refused operation changed the store")
			}
		})
	}
}

type storeContractRefusal struct{}

func (*storeContractRefusal) Error() string { return "store returned REFUSED" }

func TestMemberRecordFollowsEveryDestructivePath(t *testing.T) {
	t.Parallel()
	c, tb := memberFixture(t)
	ctx := context.Background()
	place := func(table, want string) {
		t.Helper()
		got, err := c.HGet(ctx, ntable.MemberKey("m1"), "place:"+table).Result()
		if err == redis.Nil && want == "" {
			return
		}
		if err != nil || got != want {
			t.Fatalf("placement in %s = %q (%v), want %q", table, got, err, want)
		}
	}
	other := tb
	other.Name, other.Rows = "second", nil
	if err := ntable.Create(ctx, c, other, now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, other.Name, "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellAdd(ctx, c, other.Name, "build", "ready", "m1", 11); err != nil {
		t.Fatalf("cross-table placement is permitted: %v", err)
	}
	place(tb.Name, "build:ready")
	place(other.Name, "build:ready")
	if _, err := ntable.CellMove(ctx, c, tb.Name, "build", "ready", "working", "m1"); err != nil {
		t.Fatal(err)
	}
	place(tb.Name, "build:working")
	if score, err := c.ZScore(ctx, ntable.CellKey(tb.Name, "build", "working"), "m1").Result(); err != nil || score != 7 {
		t.Fatalf("move changed score: %v %v", score, err)
	}
	if _, err := ntable.CellRemove(ctx, c, other.Name, "build", "ready", "m1"); err != nil {
		t.Fatal(err)
	}
	place(other.Name, "")
	place(tb.Name, "build:working")
	if _, err := ntable.CellAdd(ctx, c, other.Name, "build", "ready", "m1", 11); err != nil {
		t.Fatalf("the same record may be placed again after removal: %v", err)
	}
	if _, err := ntable.RowDel(ctx, c, tb.Name, "build"); err != nil {
		t.Fatal(err)
	}
	place(tb.Name, "")
	place(other.Name, "build:ready")
	if _, err := ntable.CellAdd(ctx, c, tb.Name, "test", "ready", "m1", 13); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.Clear(ctx, c, tb.Name); err != nil {
		t.Fatal(err)
	}
	place(tb.Name, "")
	place(other.Name, "build:ready")
	if _, err := ntable.Drop(ctx, c, other.Name); err != nil {
		t.Fatal(err)
	}
	place(other.Name, "")
	if epoch, err := c.HGet(ctx, ntable.MemberKey("m1"), "epoch").Result(); err != nil || epoch != "0" {
		t.Fatalf("destruction lost immutable identity: epoch=%q err=%v", epoch, err)
	}
}

func TestMemberRecordFailureDoesNotPartiallyWrite(t *testing.T) {
	t.Parallel()
	t.Run("wrong-record-type", func(t *testing.T) {
		t.Parallel()
		c, tb := memberFixture(t)
		ctx := context.Background()
		if err := c.Set(ctx, ntable.MemberKey("m1"), "corrupt", 0).Err(); err != nil {
			t.Fatal(err)
		}
		before := memberStoreImage(t, c)
		if _, err := ntable.CellMove(ctx, c, tb.Name, "build", "ready", "working", "m1"); err == nil {
			t.Fatal("move accepted a malformed member record")
		}
		if !reflect.DeepEqual(before, memberStoreImage(t, c)) {
			t.Fatal("wrong record type caused a partial move")
		}
	})
	t.Run("record-write-denied", func(t *testing.T) {
		t.Parallel()
		c, tb := memberFixture(t)
		ctx := context.Background()
		if err := c.ACLSetUser(ctx, "denyrecords", "on", ">table-test-password", "~*", "&*", "+@all", "-hset").Err(); err != nil {
			t.Fatal(err)
		}
		opts := *c.Options()
		opts.Username, opts.Password = "denyrecords", "table-test-password"
		limited := redis.NewClient(&opts)
		t.Cleanup(func() {
			if err := limited.Close(); err != nil {
				t.Error(err)
			}
		})
		before := memberStoreImage(t, c)
		if _, err := ntable.CellAdd(ctx, limited, tb.Name, "build", "working", "new-member", 3); err == nil || !strings.Contains(err.Error(), "NOPERM") {
			t.Fatalf("record permission refusal = %v", err)
		}
		if !reflect.DeepEqual(before, memberStoreImage(t, c)) {
			t.Fatal("denied record write left a member in a cell")
		}
	})
}

// Redis executes functions with the caller's ACL. A command grant is not a
// function-only grant: a seat holding it can also issue the raw command.
func TestTableWriterACLUsesCallerPermissions(t *testing.T) {
	t.Parallel()
	c, tb := memberFixture(t)
	ctx := context.Background()
	if err := c.ACLSetUser(ctx, "writer", "on", ">test-password", "~*", "&*", "+@all", "-zadd").Err(); err != nil {
		t.Fatal(err)
	}
	opts := *c.Options()
	opts.Username, opts.Password = "writer", "test-password"
	writer := redis.NewClient(&opts)
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
	})
	key := ntable.CellKey(tb.Name, "build", "working")
	before := memberStoreImage(t, c)
	if err := writer.ZAdd(ctx, key, redis.Z{Score: 1, Member: "raw"}).Err(); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("raw write without ZADD grant = %v", err)
	}
	if _, err := ntable.CellAdd(ctx, writer, tb.Name, "build", "working", "through-function", 1); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("function write without ZADD grant = %v", err)
	}
	if !reflect.DeepEqual(before, memberStoreImage(t, c)) {
		t.Fatal("denied write changed the store")
	}
	if err := c.ACLSetUser(ctx, "writer", "+zadd").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellAdd(ctx, writer, tb.Name, "build", "working", "through-function", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.Check(ctx, c, tb.Name); err != nil {
		t.Fatal(err)
	}
	if err := writer.ZAdd(ctx, key, redis.Z{Score: 2, Member: "raw"}).Err(); err != nil {
		t.Fatalf("same grant did not permit the raw command: %v", err)
	}
	if _, err := ntable.Check(ctx, c, tb.Name); !errors.Is(err, ntable.ErrDrift) {
		t.Fatalf("raw write bypass was not detected: %v", err)
	}
}

// A refusal must preserve the stored key byte-for-byte, even for a binding
// installed by another tool. CLI output escapes terminal control bytes later.
func TestBoundRefusalPreservesStoredKey(t *testing.T) {
	t.Parallel()
	c, tb := memberFixture(t)
	ctx := context.Background()
	allBytes := make([]byte, 256)
	for i := range allBytes {
		allBytes[i] = byte(i)
	}
	for _, key := range []string{"bench:batman:cards:ready", "external:" + string(allBytes)} {
		if err := c.HSet(ctx, ntable.RowKey(tb.Name, "test"), "key:ready", key).Err(); err != nil {
			t.Fatal(err)
		}
		before := memberStoreImage(t, c)
		for _, verb := range []string{"add", "remove", "move", "clear"} {
			var err error
			switch verb {
			case "add":
				_, err = ntable.CellAdd(ctx, c, tb.Name, "test", "ready", "m2", 1)
			case "remove":
				_, err = ntable.CellRemove(ctx, c, tb.Name, "test", "ready", "m2")
			case "move":
				_, err = ntable.CellMove(ctx, c, tb.Name, "test", "ready", "working", "m2")
			case "clear":
				_, err = ntable.Clear(ctx, c, tb.Name)
			}
			var bound *ntable.BoundError
			if !errors.As(err, &bound) || bound.Key != key || !strings.Contains(err.Error(), key) {
				t.Fatalf("%s lost bound key %q: %v", verb, key, err)
			}
			if !reflect.DeepEqual(before, memberStoreImage(t, c)) {
				t.Fatalf("%s changed store on bound refusal", verb)
			}
		}
	}
}

// T6: corruption left by the old lossy Bind must not become a ghost when
// an unbound row is recreated. Refuse until an explicit migration repairs it.
func TestHiddenOwnedCellRefusesShapeAndRemoval(t *testing.T) {
	t.Parallel()
	for _, verb := range []string{"row-add", "row-del", "bind", "drop"} {
		t.Run(verb, func(t *testing.T) {
			t.Parallel()
			c, tb := memberFixture(t)
			ctx := context.Background()
			if err := c.HSet(ctx, ntable.RowKey(tb.Name, "build"), "key:ready", "external:ready").Err(); err != nil {
				t.Fatal(err)
			}
			before := memberStoreImage(t, c)
			var err error
			switch verb {
			case "row-add":
				_, err = ntable.RowAdd(ctx, c, tb.Name, "build", ntable.RowSpec{})
			case "row-del":
				_, err = ntable.RowDel(ctx, c, tb.Name, "build")
			case "bind":
				tb.Rows = nil
				err = ntable.Bind(ctx, c, tb, now)
			case "drop":
				_, err = ntable.Drop(ctx, c, tb.Name)
			}
			if !errors.Is(err, ntable.ErrOccupied) {
				t.Fatalf("hidden-cell %s = %v", verb, err)
			}
			if !reflect.DeepEqual(before, memberStoreImage(t, c)) {
				t.Fatal("hidden-cell refusal changed store")
			}
		})
	}
}

func TestRuntimeCheckFindsBothDirectionsAndHiddenCells(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"valid", "ghost-record", "missing-record", "duplicate", "hidden-cell"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			c, tb := memberFixture(t)
			ctx := context.Background()
			var err error
			switch mode {
			case "ghost-record":
				err = c.HSet(ctx, ntable.MemberKey("ghost"), "epoch", "0", "place:"+tb.Name, "build:ready").Err()
			case "missing-record":
				err = c.Del(ctx, ntable.MemberKey("m1")).Err()
			case "duplicate":
				err = c.ZAdd(ctx, ntable.CellKey(tb.Name, "test", "working"), redis.Z{Score: 7, Member: "m1"}).Err()
			case "hidden-cell":
				err = c.ZAdd(ctx, ntable.CellKey(tb.Name, "absent", "ready"), redis.Z{Score: 1, Member: "ghost"}).Err()
			}
			if err != nil {
				t.Fatal(err)
			}
			before := memberStoreImage(t, c)
			report, err := ntable.Check(ctx, c, tb.Name)
			if mode == "valid" {
				if err != nil || report.Members != 1 || report.Cells != 4 {
					t.Fatalf("valid check = %#v %v", report, err)
				}
			} else if !errors.Is(err, ntable.ErrDrift) {
				t.Fatalf("%s = %v", mode, err)
			}
			if !reflect.DeepEqual(before, memberStoreImage(t, c)) {
				t.Fatal("read-only check changed store")
			}
		})
	}
}

// Direct FCALL has the same definition boundary as the Go client. A bad
// raw declaration must not create a table that the reader cannot decode.
func TestRawDefinitionRefusesBeforeCreatingAnything(t *testing.T) {
	t.Parallel()
	for _, body := range []string{`{"order":"ready,,","col:ready":"count:sum:0:"}`, `{"order":"ready","col:ready":"count:sum:inf:"}`, `{"order":"ready","col:ready":"count:sum:9223372036854775808:"}`} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			_, c := live(t)
			ctx := context.Background()
			reply, err := c.FCall(ctx, ntable.FnCreate, []string{ntable.DefKey("raw")}, "raw", body, `{"epoch":"0"}`).Slice()
			if err != nil || len(reply) < 2 || reply[0] != "REFUSED" || reply[1] != "DEFINITION" {
				t.Fatalf("bad definition = %v %v", reply, err)
			}
			if got := memberStoreImage(t, c); len(got) != 0 {
				t.Fatalf("bad declaration created %v", got)
			}
		})
	}
}
