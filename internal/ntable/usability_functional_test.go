//go:build functional

package ntable_test

import (
	"context"
	"errors"
	"strings"
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
			require.NoError(t, ntable.Create(ctx, c, demo(), now))
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
	require.NoError(t, ntable.Create(ctx, c, table, now))
	if _, err := ntable.RowAdd(ctx, c, "demo", "r: one", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellAdd(ctx, c, "demo", "r: one", "ready", "m", 2); err != nil {
		t.Fatal(err)
	}
	before := review4456Image(t, c)
	loc, err := ntable.MemberFind(ctx, c, "demo", "m")
	if err != nil || loc.State != "placed" || loc.Row != "r: one" || loc.Column != "ready" {
		t.Fatalf("location: %+v %v", loc, err)
	}
	require.Equal(t, before, review4456Image(t, c), "find wrote")
	require.NoError(t, c.ZRem(ctx, ntable.CellKey("demo", "r: one", "ready"), "m").Err())
	_, err = ntable.MemberFind(ctx, c, "demo", "m")
	require.ErrorIs(t, err, ntable.ErrDrift, "ghost backlink")
	require.NoError(t, c.HSet(ctx, "domain", "n", "1").Err())
	if _, err := ntable.MemberFind(ctx, c, "demo", "m"); !errors.Is(err, ntable.ErrMemberEpoch) || !strings.Contains(err.Error(), "0 1") {
		t.Fatalf("old identity: %v", err)
	}
}
