//go:build functional

package ntable_test

// Second-round reader probes: fresh variants not in zz_reader_probe.

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

func r3Snapshot(t *testing.T, c *redis.Client, key string) map[string]string {
	t.Helper()
	return c.HGetAll(context.Background(), key).Val()
}

func r3Keys(t *testing.T, c *redis.Client) []string {
	t.Helper()
	keys := c.Keys(context.Background(), "*").Val()
	sort.Strings(keys)
	return keys
}

// Epoch-1 snapshot after an ordinary first write versus a batch first write.
func TestR3EpochSnapshotParity(t *testing.T) {
	t.Parallel()
	type result struct {
		snap map[string]string
		keys []string
		read error
	}
	run := func(mode string) result {
		c, tb := epochFixture(t)
		ctx := context.Background()
		if err := c.HSet(ctx, tb.EpochKey, "n", 1).Err(); err != nil {
			t.Fatal(err)
		}
		rev := c.HGet(ctx, ntable.DefKey(tb.Name)+":revision", "n").Val()
		switch mode {
		case "ordinary":
			if err := ntable.MemberCreate(ctx, c, tb.Name, "f", ntable.WriteOptions{Epoch: 1}); err != nil {
				t.Fatal(err)
			}
		case "batch-noop":
			raw := `{"schema":1,"table":"epoch-test","epoch":"1","expected_table_revision":"` + rev + `","operation_id":"e1","actor":"p","members":[{"id":"f","expect":{"absent":true}}]}`
			ans, err := c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey(tb.Name)}, tb.Name, raw).Slice()
			t.Logf("%s: %v %v", mode, trunc(ans), err)
		}
		snap := r3Snapshot(t, c, "table:epoch-test:1:definition")
		keys := r3Keys(t, c)
		if err := c.HSet(ctx, tb.EpochKey, "n", 2).Err(); err != nil {
			t.Fatal(err)
		}
		_, err := ntable.ReadAt(ctx, c, tb.Name, 1)
		return result{snap, keys, err}
	}
	o, b := run("ordinary"), run("batch-noop")
	t.Logf("ordinary snapshot: %v", o.snap)
	t.Logf("batch    snapshot: %v", b.snap)
	t.Logf("ordinary keys: %v", o.keys)
	t.Logf("batch    keys: %v", b.keys)
	t.Logf("ReadAt(1): ordinary=%v batch=%v", o.read, b.read)
	if !reflect.DeepEqual(o.snap, b.snap) {
		t.Errorf("epoch-1 definition snapshots differ")
	}
	if b.read != nil {
		t.Errorf("ReadAt(1) after a batch first write: %v", b.read)
	}
}

// Batch as the first write of epoch 2 after an ordinary epoch 1, and a
// changing batch as a first write.
func TestR3EpochTwoFirstWriteByBatch(t *testing.T) {
	t.Parallel()
	c, tb := epochFixture(t)
	ctx := context.Background()
	if err := c.HSet(ctx, tb.EpochKey, "n", 1).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, tb.Name, "build", ntable.RowSpec{}, ntable.WriteOptions{Epoch: 1}); err != nil {
		t.Fatal(err)
	}
	s1 := r3Snapshot(t, c, "table:epoch-test:1:definition")
	if err := c.HSet(ctx, tb.EpochKey, "n", 2).Err(); err != nil {
		t.Fatal(err)
	}
	rev := c.HGet(ctx, ntable.DefKey(tb.Name)+":revision", "n").Val()
	// a changing batch: set a field on a new unplaced member is not possible
	// (create needs a row); use an absent-guard noop then check the snapshot.
	raw := `{"schema":1,"table":"epoch-test","epoch":"2","expected_table_revision":"` + rev + `","operation_id":"e2","actor":"p","members":[{"id":"g","expect":{"absent":true}}]}`
	ans, err := c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey(tb.Name)}, tb.Name, raw).Slice()
	t.Logf("apply at epoch 2: %v %v", trunc(ans), err)
	s2 := r3Snapshot(t, c, "table:epoch-test:2:definition")
	delete(s1, "_revision")
	delete(s2, "_revision")
	t.Logf("epoch1 %v\nepoch2 %v", s1, s2)
	if !reflect.DeepEqual(s1, s2) {
		t.Errorf("epoch-2 snapshot from a batch differs from epoch-1 snapshot from an ordinary write")
	}
	if err := c.HSet(ctx, tb.EpochKey, "n", 3).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.ReadAt(ctx, c, tb.Name, 2); err != nil {
		t.Errorf("ReadAt(2): %v", err)
	}
	if _, err := ntable.ReadAt(ctx, c, tb.Name, 1); err != nil {
		t.Errorf("ReadAt(1): %v", err)
	}
}

func r3Raw(ctx context.Context, c *redis.Client, rev, op, members string) ([]any, error) {
	raw := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + rev + `","operation_id":"` + op + `","actor":"p","members":[` + members + `]}`
	return rawApply(ctx, c, raw)
}

// A missing immutable identity hash is restored by a batch exactly as by an
// ordinary write.
func TestR3MissingIdentityRestored(t *testing.T) {
	t.Parallel()
	var images [2]map[string]string
	for i, viaApply := range []bool{false, true} {
		c, ctx := probeTable(t)
		if err := c.Del(ctx, ntable.DefKey("demo")+":identity").Err(); err != nil {
			t.Fatal(err)
		}
		if viaApply {
			ans, err := r3Raw(ctx, c, probeRev(ctx, c), "id", `{"id":"q","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1}}`)
			if err != nil || len(ans) == 0 || ans[0] != "OK" {
				t.Fatalf("apply: %v %v", trunc(ans), err)
			}
		} else if _, err := ntable.CellAdd(ctx, c, "demo", "build", "ready", "q", 1); err != nil {
			t.Fatal(err)
		}
		images[i] = c.HGetAll(ctx, ntable.DefKey("demo")+":identity").Val()
		if len(images[i]) == 0 {
			t.Errorf("viaApply=%v: identity not restored", viaApply)
		}
	}
	if !reflect.DeepEqual(images[0], images[1]) {
		t.Errorf("identity after ordinary write %v, after batch %v", images[0], images[1])
	}
}

// A missing template and its catalog entry are met by a batch exactly as by an
// ordinary write: the same refusal, and nothing written.
func TestBatchMissingTemplateMatchesOrdinaryWrite(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	if err := c.Del(ctx, ntable.DefKey("demo")).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.SRem(ctx, "tables", "demo").Err(); err != nil {
		t.Fatal(err)
	}
	before := storeImage(t, c)
	_, ordinary := ntable.CellAdd(ctx, c, "demo", "build", "ready", "q", 1)
	ans, err := r3Raw(ctx, c, "0", "tpl", `{"id":"q","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1}}`)
	if ordinary == nil || !errors.Is(ordinary, ntable.ErrNoTable) {
		t.Fatalf("ordinary write on a missing template: %v", ordinary)
	}
	if err != nil || len(ans) < 2 || ans[0] != "REFUSED" || ans[1] != "NOTABLE" {
		t.Errorf("batch on a missing template: %v %v, want REFUSED NOTABLE", trunc(ans), err)
	}
	if !reflect.DeepEqual(before, storeImage(t, c)) {
		t.Errorf("a refused write changed the store")
	}
}
