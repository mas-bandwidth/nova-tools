//go:build functional

package ntable_test

// A batch is committed by the same code as every ordinary write (T.finish): the
// definition snapshot of an epoch, the immutable identity, the template and the
// catalog are kept the same way, and an epoch's history survives a first write
// made by a batch.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		require.NoError(t, c.HSet(ctx, tb.EpochKey, "n", 1).Err())
		rev := c.HGet(ctx, ntable.DefKey(tb.Name)+":revision", "n").Val()
		switch mode {
		case "ordinary":
			require.NoError(t, ntable.MemberCreate(ctx, c, tb.Name, "f", ntable.WriteOptions{Epoch: 1}))
		case "batch-noop":
			raw := `{"schema":1,"table":"epoch-test","epoch":"1","expected_table_revision":"` + rev + `","operation_id":"e1","actor":"p","members":[{"id":"f","expect":{"absent":true}}]}`
			ans, err := c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey(tb.Name)}, tb.Name, raw).Slice()
			require.NoError(t, err, "%s: %v", mode, trunc(ans))
			require.NotEmpty(t, ans, "%s", mode)
			require.Equal(t, "OK", ans[0], "%s: %v", mode, trunc(ans))
		}
		snap := hashOf(t, c, "table:epoch-test:1:definition")
		require.NoError(t, c.HSet(ctx, tb.EpochKey, "n", 2).Err())
		_, err := ntable.ReadAt(ctx, c, tb.Name, 1)
		return result{snap, err}
	}
	o, b := run("ordinary"), run("batch-noop")
	assert.Equal(t, b.snap, o.snap, "epoch-1 definition snapshots differ")
	assert.NoError(t, o.read, "ReadAt(1) after the epoch advanced: ordinary %v, batch %v", o.read, b.read)
	assert.NoError(t, b.read, "ReadAt(1) after the epoch advanced: ordinary %v, batch %v", o.read, b.read)
}

// A batch as the first write of epoch 2, after an ordinary epoch 1, snapshots
// epoch 2 as epoch 1 was and leaves both readable.
func TestBatchFirstWriteOfALaterEpochKeepsBothHistories(t *testing.T) {
	t.Parallel()
	c, tb := epochFixture(t)
	ctx := context.Background()
	require.NoError(t, c.HSet(ctx, tb.EpochKey, "n", 1).Err())
	_, err := ntable.RowAdd(ctx, c, tb.Name, "build", ntable.RowSpec{}, ntable.WriteOptions{Epoch: 1})
	require.NoError(t, err)
	s1 := hashOf(t, c, "table:epoch-test:1:definition")
	require.NoError(t, c.HSet(ctx, tb.EpochKey, "n", 2).Err())
	rev := c.HGet(ctx, ntable.DefKey(tb.Name)+":revision", "n").Val()
	raw := `{"schema":1,"table":"epoch-test","epoch":"2","expected_table_revision":"` + rev + `","operation_id":"e2","actor":"p","members":[{"id":"g","expect":{"absent":true}}]}`
	ans, err := c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey(tb.Name)}, tb.Name, raw).Slice()
	require.NoError(t, err, "apply at epoch 2: %v", trunc(ans))
	require.NotEmpty(t, ans, "apply at epoch 2")
	require.Equal(t, "OK", ans[0], "apply at epoch 2: %v", trunc(ans))
	s2 := hashOf(t, c, "table:epoch-test:2:definition")
	delete(s1, "_revision")
	delete(s2, "_revision")
	assert.Equal(t, s2, s1, "epoch-2 snapshot from a batch differs from epoch-1 snapshot from an ordinary write")
	require.NoError(t, c.HSet(ctx, tb.EpochKey, "n", 3).Err())
	_, err = ntable.ReadAt(ctx, c, tb.Name, 2)
	assert.NoError(t, err, "ReadAt(2)")
	_, err = ntable.ReadAt(ctx, c, tb.Name, 1)
	assert.NoError(t, err, "ReadAt(1)")
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
		require.NoError(t, c.Del(ctx, ntable.DefKey("demo")+":identity").Err())
		if viaApply {
			ans, err := batchRaw(ctx, c, probeRev(ctx, c), "id", `{"id":"q","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1}}`)
			require.True(t, replyOpens(ans, err, "OK"), "apply: %v %v", trunc(ans), err)
		} else {
			_, err := ntable.CellAdd(ctx, c, "demo", "build", "ready", "q", 1)
			require.NoError(t, err)
		}
		images[i] = c.HGetAll(ctx, ntable.DefKey("demo")+":identity").Val()
		assert.NotEmpty(t, images[i], "viaApply=%v: identity not restored", viaApply)
	}
	assert.Equal(t, images[1], images[0], "identity after ordinary write %v, after batch %v", images[0], images[1])
}

// A missing template and its catalog entry are met by a batch exactly as by an
// ordinary write: the same refusal, and nothing written.
func TestBatchMissingTemplateMatchesOrdinaryWrite(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	require.NoError(t, c.Del(ctx, ntable.DefKey("demo")).Err())
	require.NoError(t, c.SRem(ctx, "tables", "demo").Err())
	before := storeImage(t, c)
	_, ordinary := ntable.CellAdd(ctx, c, "demo", "build", "ready", "q", 1)
	ans, err := batchRaw(ctx, c, "0", "tpl", `{"id":"q","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1}}`)
	require.Error(t, ordinary, "ordinary write on a missing template: %v", ordinary)
	require.ErrorIs(t, ordinary, ntable.ErrNoTable, "ordinary write on a missing template: %v", ordinary)
	assert.True(t, replyOpens(ans, err, "REFUSED", "NOTABLE"), "batch on a missing template: %v %v, want REFUSED NOTABLE", trunc(ans), err)
	assert.Equal(t, before, storeImage(t, c), "a refused write changed the store")
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
	require.NoError(t, c.HSet(ctx, ntable.MemberKey("a"), fields).Err())
	rev := probeRev(ctx, c)
	events := c.XLen(ctx, ntable.DefKey("demo")+":changes").Val()
	names := make([]string, ntable.LimitUnsetFields)
	for i := range names {
		names[i] = fmt.Sprintf(`"u%d"`, i)
	}
	ans, err := batchRaw(ctx, c, rev, "chunk", `{"id":"a","expect":{},"unset":[`+strings.Join(names, ",")+`]}`)
	require.True(t, replyOpens(ans, err, "OK"), "unset: %v %v", trunc(ans), err)
	left := c.HLen(ctx, ntable.MemberKey("a")).Val()
	assert.Equal(t, int64(4), left, "fields left, want 4 (epoch, place:demo, revision, role)")
	after := probeRev(ctx, c)
	n, m := mustUint(t, rev), mustUint(t, after)
	assert.Equal(t, n+1, m, "table revision %s -> %s, want one step", rev, after)
	got := c.XLen(ctx, ntable.DefKey("demo")+":changes").Val() - events
	assert.Equal(t, int64(1), got, "%d change events, want 1", got)
}

func mustUint(t *testing.T, s string) uint64 {
	t.Helper()
	n, err := strconv.ParseUint(s, 10, 64)
	require.NoError(t, err)
	return n
}
