//go:build functional

package ntable_test

// Operation identity is table, epoch and operation id, and a drop ends the
// table's incarnation: after drop and create, the operation records written
// before the drop neither replay nor conflict.

import (
	"reflect"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

func TestBatchOperationRecordsDoNotSurviveADrop(t *testing.T) {
	t.Parallel()
	c, ctx := probeTable(t)
	member := `{"id":"a","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1}}`
	old := manifestWith(probeRev(ctx, c), "op-1", member)
	first, err := rawApply(ctx, c, old)
	if err != nil || first[0] != "OK" {
		t.Fatalf("first apply: %v %v", trunc(first), err)
	}
	// within one incarnation the replay returns the original receipt
	if again, err := rawApply(ctx, c, old); err != nil || !reflect.DeepEqual(first, again) {
		t.Fatalf("replay: %v %v", trunc(again), err)
	}

	if _, err := ntable.Drop(ctx, c, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := ntable.Create(ctx, c, demo(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}

	// the same bytes: the table never saw this change, so it is not replayed
	ans, err := rawApply(ctx, c, old)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(first, ans) {
		t.Fatalf("a replay after drop and create returned the old receipt: %v", trunc(ans))
	}
	if len(ans) < 2 || ans[0] != "REFUSED" {
		t.Errorf("the old request against the new table: %v; want a refusal on its stale revision", trunc(ans))
	}
	if score := c.ZScore(ctx, ntable.CellKey("demo", "build", "ready"), "a"); score.Err() == nil {
		t.Errorf("the refused replay placed a member")
	}

	// the same operation id with a request that fits applies freshly, without a conflict
	fresh := manifestWith(probeRev(ctx, c), "op-1", `{"id":"b","expect":{"absent":true},"create":{"row":"build","col":"ready","score":1}}`)
	ans, err = rawApply(ctx, c, fresh)
	if err != nil || ans[0] != "OK" {
		t.Fatalf("a fresh request under a reused operation id: %v %v", trunc(ans), err)
	}
	if reflect.DeepEqual(first, ans) {
		t.Errorf("the fresh request returned the old receipt")
	}
	// and now it is recorded: its own replay returns its own receipt
	if again, err := rawApply(ctx, c, fresh); err != nil || !reflect.DeepEqual(ans, again) {
		t.Errorf("replay of the fresh request: %v %v", trunc(again), err)
	}
}
