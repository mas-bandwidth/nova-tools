//go:build functional

package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

func TestGeneratedTableRolesReadReceiptsAndCoordinatorAppliesMulti(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	addr := testutil.Start(t)
	admin := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = admin.Close() })
	if err := fn.Load(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if err := store.DeployACLs(ctx, admin); err != nil {
		t.Fatal(err)
	}
	seat := func(name string) *redis.Client {
		t.Helper()
		password := name + "-test-password"
		if err := admin.Do(ctx, "ACL", "SETUSER", name, ">"+password).Err(); err != nil {
			t.Fatal(err)
		}
		c := redis.NewClient(&redis.Options{Addr: addr, Username: name, Password: password})
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	coordinator := seat("ns-coordinator")
	tableSeat := seat("ns-table")

	cols, err := ntable.ParseColumns("ready")
	if err != nil {
		t.Fatal(err)
	}
	participants := make([]ntable.MultiBatchTable, 0, 2)
	for _, name := range []string{"role_a", "role_b"} {
		if err := ntable.Create(ctx, admin, ntable.Table{Name: name, Columns: cols}, time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := ntable.RowAdd(ctx, admin, name, "work", ntable.RowSpec{}); err != nil {
			t.Fatal(err)
		}
		participants = append(participants, ntable.MultiBatchTable{
			Name: name, Epoch: "0", ExpectedTableRevision: admin.HGet(ctx, ntable.RevisionKey(name), "n").Val(),
		})
	}
	before, err := ntable.ReadReceiptPage(ctx, tableSeat, "role_a", ntable.ReceiptCursor{}, 10)
	if err != nil || len(before.Events) != 2 {
		t.Fatalf("table seat cannot read initial receipts: %+v %v", before, err)
	}

	manifest := ntable.MultiBatchManifest{Schema: 2, Scope: "role_scope", OperationID: "op-1", Tables: participants,
		Members: []ntable.MultiBatchMember{{RecordTable: "role_a", ID: "card",
			Expect: &ntable.MultiMemberExpect{Absent: true, Places: []ntable.MultiPlaceExpect{{Table: "role_a", Absent: true}, {Table: "role_b", Absent: true}}},
			Placements: []ntable.MultiPlacement{
				{Table: "role_a", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 1}},
				{Table: "role_b", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 2}},
			},
		}},
	}
	if receipt, err := ntable.ApplyMultiBatch(ctx, coordinator, manifest); err != nil || receipt.Replay || receipt.ID == "" {
		t.Fatalf("coordinator cannot apply multi batch: %+v %v", receipt, err)
	}
	after, err := ntable.ReadReceiptPage(ctx, tableSeat, "role_a", before.Next, 10)
	if err != nil || len(after.Events) != 1 || after.Events[0].Verb != "multi_ref" {
		t.Fatalf("table seat cannot read multi receipt reference: %+v %v", after, err)
	}
	if _, err := ntable.ReadReceiptPage(ctx, coordinator, "role_b", ntable.ReceiptCursor{}, 10); err != nil {
		t.Fatalf("coordinator cannot read receipts: %v", err)
	}
	if _, err := ntable.ApplyMultiBatch(ctx, tableSeat, manifest); err == nil || !strings.Contains(err.Error(), "NOPERM") {
		t.Fatalf("read-only table seat applied multi batch: %v", err)
	}
}
