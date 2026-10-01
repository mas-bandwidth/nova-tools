//go:build functional

package ntable_test

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestViewDeleteStagesAllCommands(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"registry-type", "late-acl"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			_, c := live(t)
			ctx := context.Background()
			newTable(t, c, demo())
			require.NoError(t, ntable.ViewSet(ctx, c, ntable.View{Name: "v", Tables: []string{"demo"}}))
			writer := c
			if mode == "registry-type" {
				require.NoError(t, c.Del(ctx, "views").Err())
				require.NoError(t, c.Set(ctx, "views", "wrong type", 0).Err())
			} else {
				require.NoError(t, c.ACLSetUser(ctx, "deleter", "on", ">pw", "~*", "+@all", "-srem").Err())
				opts := *c.Options()
				opts.Username, opts.Password = "deleter", "pw"
				writer = redis.NewClient(&opts)
				t.Cleanup(func() { _ = writer.Close() })
			}
			before := review4456Image(t, c)
			_, err := ntable.ViewDelete(ctx, writer, "v")
			require.Error(t, err, "unsafe deletion accepted")
			require.Equal(t, before, review4456Image(t, c), "refused deletion changed the hash or registry")
		})
	}
}

func TestMemberFindReportsIndexedOwnedPlacementAndDrift(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	table := demo()
	table.EpochKey = "domain"
	table.MemberPrefix = "task:"
	newTable(t, c, table).rows("r: one").cell("r: one", "ready", "m", 2)
	before := review4456Image(t, c)
	loc, err := ntable.MemberFind(ctx, c, "demo", "m")
	require.NoError(t, err, "location: %+v", loc)
	require.Equal(t, "placed", loc.State, "location: %+v", loc)
	require.Equal(t, "r: one", loc.Row, "location: %+v", loc)
	require.Equal(t, "ready", loc.Column, "location: %+v", loc)
	require.Equal(t, before, review4456Image(t, c), "find wrote")
	require.NoError(t, c.ZRem(ctx, ntable.CellKey("demo", "r: one", "ready"), "m").Err())
	_, err = ntable.MemberFind(ctx, c, "demo", "m")
	require.ErrorIs(t, err, ntable.ErrDrift, "ghost backlink")
	require.NoError(t, c.HSet(ctx, "domain", "n", "1").Err())
	_, err = ntable.MemberFind(ctx, c, "demo", "m")
	require.ErrorIs(t, err, ntable.ErrMemberEpoch, "old identity: %v", err)
	require.ErrorContains(t, err, "0 1", "old identity: %v", err)
}
