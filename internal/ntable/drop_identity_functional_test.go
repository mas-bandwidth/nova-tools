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
