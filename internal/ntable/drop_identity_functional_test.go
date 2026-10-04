//go:build functional

package ntable_test

// drop --definition removes the table's identity hash with its template, so a
// table created again under the name takes its own configuration; a store
// left with the identity alone (an earlier build's drop) is named by every
// verb and repaired by drop --definition. Model: docs/SPEC-NOVA-TABLE.md,
// "Key namespaces"; the Lua is T.delete and T.open in the nova_sprint library.

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liveKeys is every key of the table at epoch zero that a drop --definition
// removes, from the library's key helpers: the template, the identity, the
// rows, each row's hash, each owned cell, the properties and the operation
// records. rows and cols name the rows and set columns the test made.
func liveKeys(name string, rows, cols []string) []string {
	keys := []string{ntable.DefKey(name), ntable.IdentityKey(name), ntable.RowsKey(name), ntable.PropsKeyAt(name, 0), ntable.DefKey(name) + ":ops"}
	for _, r := range rows {
		keys = append(keys, ntable.RowKey(name, r))
		for _, c := range cols {
			keys = append(keys, ntable.CellKey(name, r, c))
		}
	}
	return keys
}

// keptKeys is what a drop --definition keeps by design: the revision counter,
// the change log and the epoch snapshot (marked absent).
func keptKeys(name string) []string {
	return []string{ntable.RevisionKey(name), ntable.ChangesKey(name), ntable.EpochPrefix(name, 0) + ":definition"}
}

func existing(ctx context.Context, c *redis.Client, keys []string) []string {
	var out []string
	for _, k := range keys {
		if c.Exists(ctx, k).Val() == 1 {
			out = append(out, k)
		}
	}
	return out
}

func TestDropDefinitionRemovesEveryKeyOfTheTableButItsRecord(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	tb := demo()
	tb.MemberPrefix = "old:member:"
	newTable(t, c, tb).rows("build", "test").cell("build", "ready", "m1", 1)
	keys := liveKeys("demo", []string{"build", "test"}, []string{"ready", "working", "done", "who"})
	require.Subset(t, existing(ctx, c, keys), []string{ntable.DefKey("demo"), ntable.IdentityKey("demo"), ntable.RowsKey("demo"), ntable.CellKey("demo", "build", "ready")}, "the table made its keys")

	n, err := ntable.DropDefinition(ctx, c, "demo")
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	assert.Empty(t, existing(ctx, c, keys), "keys of the table left after drop --definition")
	assert.ElementsMatch(t, keptKeys("demo"), existing(ctx, c, keptKeys("demo")), "the revision, the change log and the epoch snapshot stay")
	assert.False(t, c.SIsMember(ctx, "tables", "demo").Val(), "the table is still in the catalog")
}

func TestATableDroppedByDefinitionIsCreatedAgainWithAnotherConfiguration(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	tb := demo()
	tb.MemberPrefix = "old:member:"
	newTable(t, c, tb).rows("build")
	_, err := ntable.DropDefinition(ctx, c, "demo")
	require.NoError(t, err)

	again := demo()
	again.MemberPrefix = "new:member:"
	require.NoError(t, ntable.Create(ctx, c, again, now), "create under the name after drop --definition")
	assert.Equal(t, "new:member:", c.HGet(ctx, ntable.IdentityKey("demo"), "member_prefix").Val(), "the new table's own configuration")
	got, err := ntable.Read(ctx, c, "demo")
	require.NoError(t, err)
	assert.Equal(t, "new:member:", got.MemberPrefix)
}

func TestDropKeepsTheIdentityAndTheDefinition(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	newTable(t, c, demo()).rows("build")
	_, err := ntable.Drop(ctx, c, "demo")
	require.NoError(t, err)
	assert.Equal(t, int64(1), c.Exists(ctx, ntable.IdentityKey("demo")).Val(), "drop keeps the identity")
	assert.Equal(t, int64(1), c.Exists(ctx, ntable.DefKey("demo")).Val(), "drop keeps the definition")
	other := demo()
	other.MemberPrefix = "new:member:"
	assert.ErrorContains(t, ntable.Create(ctx, c, other, now), "CONFIG", "the kept table refuses another configuration")
}

func TestAnOrphanIdentityIsNamedAndRemovedByDropDefinition(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	newTable(t, c, demo()).rows("build")
	_, err := ntable.DropDefinition(ctx, c, "demo")
	require.NoError(t, err)
	// the state an earlier build's drop --definition left: the identity alone
	require.NoError(t, c.HSet(ctx, ntable.IdentityKey("demo"), "epoch_key", "", "epoch_field", "n", "member_prefix", "old:member:").Err())

	_, err = ntable.Read(ctx, c, "demo")
	require.ErrorIs(t, err, ntable.ErrOrphan)
	assert.ErrorContains(t, err, "table:demo:identity")
	assert.ErrorContains(t, err, "; run: nova-table drop 'demo' --definition")
	_, err = ntable.RowAdd(ctx, c, "demo", "r", ntable.RowSpec{})
	assert.ErrorIs(t, err, ntable.ErrOrphan, "a write on the orphan names it too")
	ans, err := batchRaw(ctx, c, "0", "orphan", `{"id":"q","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1}}`)
	assert.True(t, replyOpens(ans, err, "REFUSED", "ORPHAN"), "a batch on the orphan: %v %v, want REFUSED ORPHAN", trunc(ans), err)

	n, err := ntable.DropDefinition(ctx, c, "demo")
	require.NoError(t, err, "drop --definition repairs the orphan")
	assert.Zero(t, n)
	assert.Empty(t, existing(ctx, c, liveKeys("demo", nil, nil)))
	assert.False(t, c.SIsMember(ctx, "tables", "demo").Val(), "the repair adds nothing to the catalog")
	_, err = ntable.Read(ctx, c, "demo")
	assert.ErrorIs(t, err, ntable.ErrNoTable, "after the repair the table is simply absent")
	other := demo()
	other.MemberPrefix = "new:member:"
	assert.NoError(t, ntable.Create(ctx, c, other, now), "create after the repair")
}

// epochedStore is an epoched table with a row and a placed member at epoch 0
// and at epoch 1, the epoch key at 1, the writer's options at epoch 1.
func epochedStore(t *testing.T) (*redis.Client, ntable.Table, ntable.WriteOptions) {
	t.Helper()
	c, _ := store(t)
	ctx := context.Background()
	tb := demo()
	tb.EpochKey = "domain:epoch"
	newTable(t, c, tb).rows("old0").cell("old0", "ready", "m0", 1)
	require.NoError(t, c.HSet(ctx, tb.EpochKey, "n", 1).Err())
	opts := ntable.WriteOptions{Epoch: 1}
	_, err := ntable.RowAdd(ctx, c, "demo", "new1", ntable.RowSpec{}, opts)
	require.NoError(t, err)
	_, err = ntable.CellAdd(ctx, c, "demo", "new1", "ready", "m1", 1, opts)
	require.NoError(t, err)
	return c, tb, opts
}

func TestDropDefinitionAtALaterEpochLeavesNoRowsToAdopt(t *testing.T) {
	t.Parallel()
	c, _, opts := epochedStore(t)
	ctx := context.Background()
	var keys []string
	for e := uint64(0); e <= 1; e++ {
		keys = append(keys, ntable.RowsKeyAt("demo", e), ntable.PropsKeyAt("demo", e))
		for _, r := range []string{"old0", "new1"} {
			keys = append(keys, ntable.RowKeyAt("demo", r, e))
			for _, col := range []string{"ready", "working", "done", "who"} {
				keys = append(keys, ntable.CellKeyAt("demo", r, col, e))
			}
		}
	}
	require.Subset(t, existing(ctx, c, keys), []string{ntable.RowsKeyAt("demo", 0), ntable.RowsKeyAt("demo", 1), ntable.CellKeyAt("demo", "old0", "ready", 0), ntable.CellKeyAt("demo", "new1", "ready", 1)})

	_, err := ntable.DropDefinition(ctx, c, "demo", opts)
	require.NoError(t, err)
	assert.Empty(t, existing(ctx, c, keys), "rows of an epoch are left after drop --definition")
	for _, id := range []string{"m0", "m1"} {
		assert.False(t, c.HExists(ctx, ntable.MemberKey(id), "place:demo").Val(), "member %s is still placed in a cell that is gone", id)
	}

	// a table created again, with no epoch key, adopts nothing
	again := demo()
	require.NoError(t, ntable.Create(ctx, c, again, now))
	got, err := ntable.Read(ctx, c, "demo")
	require.NoError(t, err)
	assert.Empty(t, got.Rows, "the new table holds the old table's rows")
	loc, err := ntable.MemberFind(ctx, c, "demo", "m0")
	require.NoError(t, err)
	assert.NotEqual(t, "placed", loc.State, "member find places a member of the old table")
	_, err = ntable.Check(ctx, c, "demo")
	assert.NoError(t, err)
}

func TestCreateRefusesTheRowsAnEarlierTableLeft(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	require.NoError(t, c.ZAdd(ctx, ntable.RowsKey("demo"), redis.Z{Score: 1, Member: "old0"}).Err())
	require.NoError(t, c.HSet(ctx, ntable.PropsKeyAt("demo", 0), "k", "v").Err())
	err := ntable.Create(ctx, c, demo(), now)
	require.ErrorIs(t, err, ntable.ErrResidue)
	assert.ErrorContains(t, err, "table:demo:rows, table:demo:props")
	assert.ErrorContains(t, err, "; run: nova-table drop 'demo' --definition")
	assert.Zero(t, c.Exists(ctx, ntable.DefKey("demo"), ntable.IdentityKey("demo")).Val(), "a refused create wrote")
}

func TestCreateOnAnOrphanIdentityNamesItWithAnotherConfiguration(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	require.NoError(t, c.HSet(ctx, ntable.IdentityKey("demo"), "epoch_key", "", "epoch_field", "n", "member_prefix", "old:member:").Err())
	other := demo()
	other.MemberPrefix = "new:member:"
	err := ntable.Create(ctx, c, other, now)
	require.ErrorIs(t, err, ntable.ErrOrphan)
	assert.NotContains(t, err.Error(), "CONFIG")
	same := demo()
	same.MemberPrefix = "old:member:"
	assert.NoError(t, ntable.Create(ctx, c, same, now), "the same configuration takes the identity back")
}

func TestAnEpochedOrphanNamesAnEpochItsRemedyCanUse(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	require.NoError(t, c.HSet(ctx, "domain:epoch", "n", "3").Err())
	require.NoError(t, c.HSet(ctx, ntable.IdentityKey("demo"), "epoch_key", "domain:epoch", "epoch_field", "n", "member_prefix", "table::member:").Err())
	_, err := ntable.Read(ctx, c, "demo")
	require.ErrorIs(t, err, ntable.ErrOrphan)
	assert.ErrorContains(t, err, "; run: nova-table drop 'demo' --definition --epoch 3")
	other := demo()
	other.EpochKey = "domain:epoch"
	other.MemberPrefix = "new:member:"
	err = ntable.Create(ctx, c, other, now, ntable.WriteOptions{Epoch: 3})
	require.ErrorIs(t, err, ntable.ErrOrphan)
	assert.ErrorContains(t, err, "--definition --epoch 3")
	_, err = ntable.DropDefinition(ctx, c, "demo", ntable.WriteOptions{Epoch: 3})
	require.NoError(t, err, "the remedy the refusal printed")
	assert.Zero(t, c.Exists(ctx, ntable.IdentityKey("demo")).Val())
}

func TestTheRevisionAndTheChangeLogContinueAcrossADropAndACreate(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	newTable(t, c, demo()).rows("build")
	before, err := c.HGet(ctx, ntable.RevisionKey("demo"), "n").Uint64()
	require.NoError(t, err)
	events := c.XLen(ctx, ntable.ChangesKey("demo")).Val()
	_, err = ntable.DropDefinition(ctx, c, "demo")
	require.NoError(t, err)
	require.NoError(t, ntable.Create(ctx, c, demo(), now))
	after, err := c.HGet(ctx, ntable.RevisionKey("demo"), "n").Uint64()
	require.NoError(t, err)
	assert.Greater(t, after, before, "the revision starts again")
	assert.Greater(t, c.XLen(ctx, ntable.ChangesKey("demo")).Val(), events, "the change log continues")
}
