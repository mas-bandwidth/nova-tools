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

func TestViewDeleteStagesAllCommands(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"registry-type", "late-acl"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			_, c := live(t)
			ctx := context.Background()
			if err := ntable.Create(ctx, c, demo(), now); err != nil {
				t.Fatal(err)
			}
			if err := ntable.ViewSet(ctx, c, ntable.View{Name: "v", Tables: []string{"demo"}}); err != nil {
				t.Fatal(err)
			}
			writer := c
			if mode == "registry-type" {
				if err := c.Del(ctx, "views").Err(); err != nil {
					t.Fatal(err)
				}
				if err := c.Set(ctx, "views", "wrong type", 0).Err(); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := c.ACLSetUser(ctx, "deleter", "on", ">pw", "~*", "+@all", "-srem").Err(); err != nil {
					t.Fatal(err)
				}
				opts := *c.Options()
				opts.Username, opts.Password = "deleter", "pw"
				writer = redis.NewClient(&opts)
				t.Cleanup(func() { _ = writer.Close() })
			}
			before := review4456Image(t, c)
			if _, err := ntable.ViewDelete(ctx, writer, "v"); err == nil {
				t.Fatal("unsafe deletion accepted")
			}
			if !reflect.DeepEqual(before, review4456Image(t, c)) {
				t.Fatal("refused deletion changed the hash or registry")
			}
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
	if err := ntable.Create(ctx, c, table, now); err != nil {
		t.Fatal(err)
	}
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
	if !reflect.DeepEqual(before, review4456Image(t, c)) {
		t.Fatal("find wrote")
	}
	if err := c.ZRem(ctx, ntable.CellKey("demo", "r: one", "ready"), "m").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.MemberFind(ctx, c, "demo", "m"); !errors.Is(err, ntable.ErrDrift) {
		t.Fatalf("ghost backlink: %v", err)
	}
	if err := c.HSet(ctx, "domain", "n", "1").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.MemberFind(ctx, c, "demo", "m"); !errors.Is(err, ntable.ErrMemberEpoch) || !strings.Contains(err.Error(), "0 1") {
		t.Fatalf("old identity: %v", err)
	}
}
