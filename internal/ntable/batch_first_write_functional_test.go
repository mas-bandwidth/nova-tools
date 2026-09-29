//go:build functional

package ntable_test

// A batch is committed by the same code as every ordinary write (T.finish): the
// definition snapshot of an epoch, the immutable identity, the template and the
// catalog are kept the same way, and an epoch's history survives a first write
// made by a batch.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

func hashOf(t *testing.T, c *redis.Client, key string) map[string]string {
	t.Helper()
	return c.HGetAll(context.Background(), key).Val()
}

// The epoch-1 snapshot after a batch first write equals the one after an ordinary first write.
func TestBatchFirstWriteSnapshotMatchesOrdinaryWrite(t *testing.T) {
	t.Parallel()
	type result struct {
		snap map[string]string
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
			if ans, err := c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey(tb.Name)}, tb.Name, raw).Slice(); err != nil || len(ans) == 0 || ans[0] != "OK" {
				t.Fatalf("%s: %v %v", mode, trunc(ans), err)
			}
		}
		snap := hashOf(t, c, "table:epoch-test:1:definition")
		if err := c.HSet(ctx, tb.EpochKey, "n", 2).Err(); err != nil {
			t.Fatal(err)
		}
		_, err := ntable.ReadAt(ctx, c, tb.Name, 1)
		return result{snap, err}
	}
	o, b := run("ordinary"), run("batch-noop")
	if !reflect.DeepEqual(o.snap, b.snap) {
		t.Errorf("epoch-1 definition snapshots differ")
	}
	if o.read != nil || b.read != nil {
		t.Errorf("ReadAt(1) after the epoch advanced: ordinary %v, batch %v", o.read, b.read)
	}
}

// A batch as the first write of epoch 2, after an ordinary epoch 1, snapshots
// epoch 2 as epoch 1 was and leaves both readable.
func TestBatchFirstWriteOfALaterEpochKeepsBothHistories(t *testing.T) {
	t.Parallel()
	c, tb := epochFixture(t)
	ctx := context.Background()
	if err := c.HSet(ctx, tb.EpochKey, "n", 1).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, tb.Name, "build", ntable.RowSpec{}, ntable.WriteOptions{Epoch: 1}); err != nil {
		t.Fatal(err)
	}
	s1 := hashOf(t, c, "table:epoch-test:1:definition")
	if err := c.HSet(ctx, tb.EpochKey, "n", 2).Err(); err != nil {
		t.Fatal(err)
	}
	rev := c.HGet(ctx, ntable.DefKey(tb.Name)+":revision", "n").Val()
	raw := `{"schema":1,"table":"epoch-test","epoch":"2","expected_table_revision":"` + rev + `","operation_id":"e2","actor":"p","members":[{"id":"g","expect":{"absent":true}}]}`
	if ans, err := c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey(tb.Name)}, tb.Name, raw).Slice(); err != nil || len(ans) == 0 || ans[0] != "OK" {
		t.Fatalf("apply at epoch 2: %v %v", trunc(ans), err)
	}
	s2 := hashOf(t, c, "table:epoch-test:2:definition")
	delete(s1, "_revision")
	delete(s2, "_revision")
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

func batchRaw(ctx context.Context, c *redis.Client, rev, op, members string) ([]any, error) {
	raw := `{"schema":1,"table":"demo","epoch":"0","expected_table_revision":"` + rev + `","operation_id":"` + op + `","actor":"p","members":[` + members + `]}`
	return rawApply(ctx, c, raw)
}

// A missing immutable identity hash is restored by a batch exactly as by an
// ordinary write.
func TestBatchRestoresAMissingIdentity(t *testing.T) {
	t.Parallel()
	var images [2]map[string]string
	for i, viaApply := range []bool{false, true} {
		c, ctx := probeTable(t)
		if err := c.Del(ctx, ntable.DefKey("demo")+":identity").Err(); err != nil {
			t.Fatal(err)
		}
		if viaApply {
			ans, err := batchRaw(ctx, c, probeRev(ctx, c), "id", `{"id":"q","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1}}`)
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
	ans, err := batchRaw(ctx, c, "0", "tpl", `{"id":"q","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1}}`)
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

// A thousand unset fields are one commit: one revision step, one change event,
// every field gone, the member's own fields kept.
func TestBatchUnsetOfAThousandFieldsIsOneCommit(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	seedTwo(t, ctx, c)
	fields := map[string]any{}
	for i := 0; i < ntable.LimitUnsetFields; i++ {
		fields[fmt.Sprintf("u%d", i)] = "v"
	}
	if err := c.HSet(ctx, ntable.MemberKey("a"), fields).Err(); err != nil {
		t.Fatal(err)
	}
	rev := probeRev(ctx, c)
	events := c.XLen(ctx, ntable.DefKey("demo")+":changes").Val()
	names := make([]string, ntable.LimitUnsetFields)
	for i := range names {
		names[i] = fmt.Sprintf(`"u%d"`, i)
	}
	ans, err := batchRaw(ctx, c, rev, "chunk", `{"id":"a","expect":{},"unset":[`+strings.Join(names, ",")+`]}`)
	if err != nil || len(ans) == 0 || ans[0] != "OK" {
		t.Fatalf("unset: %v %v", trunc(ans), err)
	}
	if left := c.HLen(ctx, ntable.MemberKey("a")).Val(); left != 4 { // epoch, place:demo, revision, role
		t.Errorf("fields left = %d, want 4", left)
	}
	after := probeRev(ctx, c)
	if n, m := mustUint(t, rev), mustUint(t, after); m != n+1 {
		t.Errorf("table revision %s -> %s, want one step", rev, after)
	}
	if got := c.XLen(ctx, ntable.DefKey("demo")+":changes").Val() - events; got != 1 {
		t.Errorf("%d change events, want 1", got)
	}
}

func mustUint(t *testing.T, s string) uint64 {
	t.Helper()
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
