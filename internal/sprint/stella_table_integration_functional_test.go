//go:build functional

package sprint

import (
	"context"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"testing"
	"time"
)

func TestStellaSprintLibraryCoexistsWithTable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, rdb := throwaway(t)
	if e := fn.Load(ctx, rdb); e != nil {
		t.Fatal(e)
	}
	e := LoadFunctionsStrict(ctx, rdb)
	t.Logf("load ns_card alongside nova_sprint: %v", e)
	if e != nil {
		t.Errorf("card library cannot coexist with table runtime: %v", e)
	}
}
func TestStellaSprintUsesRealTableEpoch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, rdb := throwaway(t)
	if e := fn.Load(ctx, rdb); e != nil {
		t.Fatal(e)
	}
	if e := rdb.HSet(ctx, "domain:epoch", "n", "2").Err(); e != nil {
		t.Fatal(e)
	}
	cols, e := ntable.ParseColumns("waiting,ready,working,review,merging,landed,done")
	if e != nil {
		t.Fatal(e)
	}
	tb := ntable.Table{Name: "streams", Columns: cols, EpochKey: "domain:epoch"}
	opts := ntable.WriteOptions{Epoch: 2}
	if e = ntable.Create(ctx, rdb, tb, time.Now(), opts); e != nil {
		t.Fatal(e)
	}
	if _, e = ntable.RowAdd(ctx, rdb, "streams", "main", ntable.RowSpec{}, opts); e != nil {
		t.Fatal(e)
	}
	client := NewRedisCardClient(rdb, WithForceEval(true))
	for _, epoch := range []EpochID{1, 0} {
		card := CardID("stale-" + epoch.String())
		receipt, e := client.Push(ctx, card, "main", WriteOptions{Epoch: epoch, Actor: "fixture"})
		actual, re := rdb.HGet(ctx, MemberKey(string(card)), "epoch").Result()
		t.Logf("active_epoch=2 requested=%d receipt=%+v error=%v stored_member_epoch=%q read_error=%v", epoch, receipt, e, actual, re)
		if e == nil {
			t.Errorf("stale requested epoch %d accepted against real table epoch2", epoch)
		}
	}
}
func TestStellaSprintWrongTypeLeavesImageUnchanged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, client := live(t)
	rdb := client.RDB()
	if e := rdb.Set(ctx, "table:streams:changes", "wrong-type", 0).Err(); e != nil {
		t.Fatal(e)
	}
	_, e := client.Push(ctx, "partial-card", "main", WriteOptions{Epoch: 1, Actor: "fixture"})
	if e == nil {
		t.Fatal("expected wrongtype error")
	}
	exists, re := rdb.Exists(ctx, MemberKey("partial-card")).Result()
	if re != nil {
		t.Fatal(re)
	}
	cell, re := rdb.ZRange(ctx, "table:streams:1:cell:main:waiting", 0, -1).Result()
	if re != nil {
		t.Fatal(re)
	}
	rev, re := rdb.HGet(ctx, "table:streams:revision", "n").Result()
	t.Logf("error=%v member_exists=%d cell=%v revision=%q revision_error=%v", e, exists, cell, rev, re)
	if exists != 0 || len(cell) != 0 || rev != "" {
		t.Error("failed Push left partial writes")
	}
}
