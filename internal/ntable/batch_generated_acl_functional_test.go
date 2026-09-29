//go:build functional

package ntable_test

import (
	"context"
	"testing"
	"time"

	nsstore "github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

// Both batch versions read a member's existence by HLEN, then probe named
// application fields by HSTRLEN and HEXISTS. A present empty value must be
// distinguished from a missing value. Use the source ACL role itself so a
// missing Redis command grant fails an ordinary batch, not only a token check.
func TestGeneratedCoordinatorBatchNamedFieldProbes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	addr, admin := live(t)
	if err := nsstore.DeployACLs(ctx, admin); err != nil {
		t.Fatal(err)
	}
	const password = "batch-probes-test-password"
	if err := admin.Do(ctx, "ACL", "SETUSER", "ns-coordinator", ">"+password).Err(); err != nil {
		t.Fatal(err)
	}
	coordinator := redis.NewClient(&redis.Options{Addr: addr, Username: "ns-coordinator", Password: password})
	t.Cleanup(func() { _ = coordinator.Close() })

	cols, err := ntable.ParseColumns("ready")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"probe_a", "probe_b"} {
		if err := ntable.Create(ctx, admin, ntable.Table{Name: name, Columns: cols}, time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := ntable.RowAdd(ctx, admin, name, "work", ntable.RowSpec{}); err != nil {
			t.Fatal(err)
		}
	}
	revision := func(name string) string {
		t.Helper()
		return admin.HGet(ctx, ntable.RevisionKey(name), "n").Val()
	}
	participants := func() []ntable.MultiBatchTable {
		return []ntable.MultiBatchTable{
			{Name: "probe_a", Epoch: "0", ExpectedTableRevision: revision("probe_a")},
			{Name: "probe_b", Epoch: "0", ExpectedTableRevision: revision("probe_b")},
		}
	}
	const field = "empty"
	empty := ""

	// Schema 1 reads an absent record and absent named field. Its next batch
	// reads that same field as present with a zero-length value.
	seed := ntable.BatchManifest{Schema: 1, Table: "probe_a", Epoch: "0",
		ExpectedTableRevision: revision("probe_a"), OperationID: "schema1-seed",
		Members: []ntable.BatchMemberEntry{{ID: "schema1", Expect: &ntable.MemberExpect{Absent: true},
			Create: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 1}, Set: map[string]string{field: empty}}}}
	if _, err := ntable.ApplyBatch(ctx, coordinator, seed); err != nil {
		t.Fatalf("schema 1 absent named field under generated coordinator ACL: %v", err)
	}
	change := ntable.BatchManifest{Schema: 1, Table: "probe_a", Epoch: "0",
		ExpectedTableRevision: revision("probe_a"), OperationID: "schema1-empty",
		Members: []ntable.BatchMemberEntry{{ID: "schema1",
			Expect: &ntable.MemberExpect{Revision: "1", Fields: map[string]ntable.FieldGuard{field: {Equals: &empty}}},
			Set:    map[string]string{field: "filled"}}}}
	if _, err := ntable.ApplyBatch(ctx, coordinator, change); err != nil {
		t.Fatalf("schema 1 present empty named field under generated coordinator ACL: %v", err)
	}

	// Schema 2 probes the same two states on a different physical member and
	// writes one shared record placed in both participating tables.
	multiSeed := ntable.MultiBatchManifest{Schema: 2, Scope: "probe_scope", OperationID: "schema2-seed", Tables: participants(),
		Members: []ntable.MultiBatchMember{{ID: "schema2", RecordTable: "probe_a",
			Expect: &ntable.MultiMemberExpect{Absent: true, Places: []ntable.MultiPlaceExpect{
				{Table: "probe_a", Absent: true}, {Table: "probe_b", Absent: true}}},
			Placements: []ntable.MultiPlacement{
				{Table: "probe_a", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 2}},
				{Table: "probe_b", Add: &ntable.MemberCreateOp{Row: "work", Col: "ready", Score: 3}},
			}, Set: map[string]string{field: empty}}}}
	if _, err := ntable.ApplyMultiBatch(ctx, coordinator, multiSeed); err != nil {
		t.Fatalf("schema 2 absent named field under generated coordinator ACL: %v", err)
	}
	multiChange := ntable.MultiBatchManifest{Schema: 2, Scope: "probe_scope", OperationID: "schema2-empty", Tables: participants(),
		Members: []ntable.MultiBatchMember{{ID: "schema2", RecordTable: "probe_a",
			Expect: &ntable.MultiMemberExpect{Revision: "1", Fields: map[string]ntable.FieldGuard{field: {Equals: &empty}}},
			Set:    map[string]string{field: "filled"}}}}
	if _, err := ntable.ApplyMultiBatch(ctx, coordinator, multiChange); err != nil {
		t.Fatalf("schema 2 present empty named field under generated coordinator ACL: %v", err)
	}
	for _, id := range []string{"schema1", "schema2"} {
		if got := admin.HGet(ctx, ntable.MemberKey(id), field).Val(); got != "filled" {
			t.Errorf("member %s field = %q, want filled", id, got)
		}
	}
}
