//go:build functional

package main

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDropDefinitionThenCreateWithAnotherConfiguration is the sitting a fleet
// store showed: drop --definition leaves nothing the next create refuses.
func TestDropDefinitionThenCreateWithAnotherConfiguration(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	steps := []struct {
		args []string
		want string
	}{
		{[]string{"create", "fleet", "--columns", "a", "--member-prefix", "old:member:"}, "TABLE CREATE table=fleet columns=1 trips=1\n"},
		{[]string{"row", "add", "fleet", "r1"}, "TABLE ROW ADD table=fleet row=r1 cols=1 bound=0 trips=1\n"},
		{[]string{"drop", "fleet", "--definition"}, "TABLE DROP table=fleet rows=1 trips=1\n"},
	}
	for _, s := range steps {
		code, stdout, stderr := runTable(at(addr, s.args...)...)
		require.Equal(t, 0, code, "%v: %s", s.args, stderr)
		assert.Equal(t, s.want, stdout, "%v", s.args)
	}
	code, _, stderr := runTable(at(addr, "show", "fleet")...)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "no such table; run: nova-table create")
	code, stdout, stderr := runTable(at(addr, "create", "fleet", "--columns", "a", "--member-prefix", "new:member:")...)
	assert.Equal(t, 0, code, stderr)
	assert.Equal(t, "TABLE CREATE table=fleet columns=1 trips=1\n", stdout)
}

// TestShowNamesAnOrphanIdentityAndDropDefinitionRepairsIt: a store holding a
// table's identity hash alone is named in one line with its remedy, and the
// remedy removes it.
func TestShowNamesAnOrphanIdentityAndDropDefinitionRepairsIt(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { assert.NoError(t, c.Close()) })
	ctx := context.Background()
	require.NoError(t, c.HSet(ctx, ntable.IdentityKey("fleet"), "epoch_key", "", "epoch_field", "n", "member_prefix", "old:member:").Err())

	code, stdout, stderr := runTable(at(addr, "show", "fleet")...)
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Equal(t, "SHOW REFUSED: table \"fleet\": no such table, but table:fleet:identity is left behind by a dropped definition; run: nova-table drop 'fleet' --definition\n", stderr)

	code, stdout, stderr = runTable(at(addr, "drop", "fleet", "--definition")...)
	assert.Equal(t, 0, code, stderr)
	assert.Equal(t, "TABLE DROP table=fleet rows=0 trips=1\n", stdout)
	assert.Zero(t, c.Exists(ctx, ntable.IdentityKey("fleet")).Val())

	code, _, stderr = runTable(at(addr, "show", "fleet")...)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "no such table; run: nova-table create")
}

// TestAnEpochedOrphanLineCarriesTheEpochItsRemedyNeeds: the remedy the line
// prints runs as printed (a stale epoch would be refused), and create with
// another configuration prints the same line.
func TestAnEpochedOrphanLineCarriesTheEpochItsRemedyNeeds(t *testing.T) {
	t.Parallel()
	addr := firstRunStore(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { assert.NoError(t, c.Close()) })
	ctx := context.Background()
	require.NoError(t, c.HSet(ctx, "domain:epoch", "n", "3").Err())
	require.NoError(t, c.HSet(ctx, ntable.IdentityKey("fleet"), "epoch_key", "domain:epoch", "epoch_field", "n", "member_prefix", "table::member:").Err())

	const remedy = "run: nova-table drop 'fleet' --definition --epoch 3\n"
	code, _, stderr := runTable(at(addr, "show", "fleet")...)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, remedy)
	code, _, stderr = runTable(at(addr, "create", "fleet", "--columns", "a", "--epoch-key", "domain:epoch", "--member-prefix", "new:member:", "--epoch", "3")...)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, remedy)
	assert.NotContains(t, stderr, "CONFIG")

	code, stdout, stderr := runTable(at(addr, "drop", "fleet", "--definition", "--epoch", "3")...)
	assert.Equal(t, 0, code, stderr)
	assert.Equal(t, "TABLE DROP table=fleet rows=0 trips=1\n", stdout)
}
