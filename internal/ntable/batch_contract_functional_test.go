//go:build functional

package ntable_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
)

func boolPtr(b bool) *bool     { return &b }
func floatPtr(f float64) *float64 { return &f }

func TestBatchApplyAndReadSetContract(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()

	tb := demo()
	if err := ntable.Create(ctx, c, tb, now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "test", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}

	// 1. Initial ReadSet on empty table
	rs, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1", "m2"})
	if err != nil {
		t.Fatalf("initial ReadSetMembers: %v", err)
	}
	if rs.Table != "demo" || len(rs.Members) != 0 || len(rs.Missing) != 2 {
		t.Fatalf("unexpected initial ReadSet: %+v", rs)
	}
	if !rs.IsMissing("m1") || !rs.IsMissing("m2") {
		t.Fatalf("expected m1 and m2 to be missing: %+v", rs)
	}

	// 2. Successful ApplyBatch creating m1 and m2
	rev1 := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	manifest1 := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: rev1,
		OperationID:           "op-create-1",
		Actor:                 "builder",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m1",
				Expect: &ntable.MemberExpect{
					Absent: true,
				},
				Create: &ntable.MemberCreateOp{
					Row:   "build",
					Col:   "ready",
					Score: 10,
				},
				Set: map[string]string{
					"definition": "job-1",
				},
			},
			{
				ID: "m2",
				Expect: &ntable.MemberExpect{
					Absent: true,
				},
				Create: &ntable.MemberCreateOp{
					Row:   "build",
					Col:   "ready",
					Score: 20,
				},
				Set: map[string]string{
					"definition": "job-2",
				},
			},
		},
	}

	rcpt1, err := ntable.ApplyBatch(ctx, c, manifest1)
	if err != nil {
		t.Fatalf("ApplyBatch create: %v", err)
	}
	if rcpt1.Outcome != "changed" {
		t.Fatalf("expected outcome changed, got %q", rcpt1.Outcome)
	}
	if rcpt1.BatchDelta == nil || rcpt1.BatchDelta.ChangedCount != 2 || rcpt1.BatchDelta.GuardCount != 0 {
		t.Fatalf("unexpected delta: %+v", rcpt1.BatchDelta)
	}

	// 3. Verify ReadSet sees m1 and m2 placed with revision 1
	rs2, err := ntable.ReadSet(ctx, c, "demo", ntable.ReadSetScope{
		Members: []string{"m1", "m2", "m3"},
	})
	if err != nil {
		t.Fatalf("ReadSet after create: %v", err)
	}
	if len(rs2.Members) != 2 || len(rs2.Missing) != 1 || rs2.Missing[0] != "m3" {
		t.Fatalf("unexpected ReadSet result: %+v", rs2)
	}
	m1, found1 := rs2.Member("m1")
	if !found1 || !m1.Placed || m1.Row != "build" || m1.Col != "ready" || m1.Score != 10 || m1.Revision != 1 || m1.Fields["definition"] != "job-1" {
		t.Fatalf("unexpected m1: %+v", m1)
	}
	m2, found2 := rs2.Member("m2")
	if !found2 || !m2.Placed || m2.Row != "build" || m2.Col != "ready" || m2.Score != 20 || m2.Revision != 1 || m2.Fields["definition"] != "job-2" {
		t.Fatalf("unexpected m2: %+v", m2)
	}

	// ReadSet by selection
	rsSel, err := ntable.ReadSet(ctx, c, "demo", ntable.ReadSetScope{
		Selection: []ntable.CellSelection{
			{Row: "build", Col: "ready"},
		},
	})
	if err != nil {
		t.Fatalf("ReadSet by selection: %v", err)
	}
	if len(rsSel.Members) != 2 {
		t.Fatalf("selection expected 2 members, got %d", len(rsSel.Members))
	}

	// 4. Idempotent replay of exact same manifest
	rcptReplay, err := ntable.ApplyBatch(ctx, c, manifest1)
	if err != nil {
		t.Fatalf("idempotent replay failed: %v", err)
	}
	if rcptReplay.ID != rcpt1.ID || rcptReplay.After != rcpt1.After {
		t.Fatalf("replay receipt mismatch: got %+v, want %+v", rcptReplay, rcpt1)
	}

	// 5. Conflicting operation ID replay
	conflictingManifest := manifest1
	conflictingManifest.Actor = "different-actor"
	_, err = ntable.ApplyBatch(ctx, c, conflictingManifest)
	if !errors.Is(err, ntable.ErrOpConflict) || !strings.Contains(err.Error(), "changed=no") {
		t.Fatalf("expected ErrOpConflict with changed=no, got: %v", err)
	}

	// 6. Table revision mismatch refusal
	staleTableManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: "9999",
		OperationID:           "op-stale-rev",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m1",
				Move: &ntable.MemberMoveOp{
					Row: "build",
					Col: "working",
				},
			},
		},
	}
	_, err = ntable.ApplyBatch(ctx, c, staleTableManifest)
	if !errors.Is(err, ntable.ErrRevisionMismatch) || !strings.Contains(err.Error(), "changed=no") {
		t.Fatalf("expected ErrRevisionMismatch with changed=no, got: %v", err)
	}

	// 7. Member revision mismatch refusal
	currentTableRev := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	badMemberRevManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: currentTableRev,
		OperationID:           "op-bad-m-rev",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m1",
				Expect: &ntable.MemberExpect{
					Revision: "99",
				},
				Move: &ntable.MemberMoveOp{
					Row: "build",
					Col: "working",
				},
			},
		},
	}
	_, err = ntable.ApplyBatch(ctx, c, badMemberRevManifest)
	if !errors.Is(err, ntable.ErrMemberRevision) || !strings.Contains(err.Error(), "changed=no") {
		t.Fatalf("expected ErrMemberRevision with changed=no, got: %v", err)
	}

	// 8. Field guard failures
	// Equals mismatch
	badGuardManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: currentTableRev,
		OperationID:           "op-bad-guard",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m1",
				Expect: &ntable.MemberExpect{
					Fields: map[string]ntable.FieldGuard{
						"definition": {Equals: strPtr("wrong-def")},
					},
				},
			},
		},
	}
	_, err = ntable.ApplyBatch(ctx, c, badGuardManifest)
	if !errors.Is(err, ntable.ErrFieldGuard) || !strings.Contains(err.Error(), "changed=no") {
		t.Fatalf("expected ErrFieldGuard with changed=no, got: %v", err)
	}

	// Absent mismatch
	badGuardAbsent := badGuardManifest
	badGuardAbsent.OperationID = "op-bad-guard-absent"
	badGuardAbsent.Members = []ntable.BatchMemberEntry{
		{
			ID: "m1",
			Expect: &ntable.MemberExpect{
				Fields: map[string]ntable.FieldGuard{
					"definition": {Absent: boolPtr(true)},
				},
			},
		},
	}
	_, err = ntable.ApplyBatch(ctx, c, badGuardAbsent)
	if !errors.Is(err, ntable.ErrFieldGuard) || !strings.Contains(err.Error(), "changed=no") {
		t.Fatalf("expected ErrFieldGuard absent with changed=no, got: %v", err)
	}

	// OneOf mismatch
	badGuardOneOf := badGuardManifest
	badGuardOneOf.OperationID = "op-bad-guard-oneof"
	badGuardOneOf.Members = []ntable.BatchMemberEntry{
		{
			ID: "m1",
			Expect: &ntable.MemberExpect{
				Fields: map[string]ntable.FieldGuard{
					"definition": {OneOf: []string{"a", "b"}},
				},
			},
		},
	}
	_, err = ntable.ApplyBatch(ctx, c, badGuardOneOf)
	if !errors.Is(err, ntable.ErrFieldGuard) || !strings.Contains(err.Error(), "changed=no") {
		t.Fatalf("expected ErrFieldGuard one_of with changed=no, got: %v", err)
	}

	// 9. Move + field updates + field guard + guard-only entry
	manifest2 := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: currentTableRev,
		OperationID:           "op-move-m1",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m1",
				Expect: &ntable.MemberExpect{
					Revision: "1",
					Place:    &ntable.PlaceExpect{Row: "build", Col: "ready"},
					Fields: map[string]ntable.FieldGuard{
						"definition": {Equals: strPtr("job-1")},
					},
				},
				Move: &ntable.MemberMoveOp{
					Row:   "build",
					Col:   "working",
					Score: floatPtr(15),
				},
				Set: map[string]string{
					"status": "in-progress",
				},
				Unset: []string{"definition"},
			},
			{
				ID: "m2",
				Expect: &ntable.MemberExpect{
					Revision: "1",
					Place:    &ntable.PlaceExpect{Row: "build", Col: "ready"},
				},
			},
		},
	}

	rcpt2, err := ntable.ApplyBatch(ctx, c, manifest2)
	if err != nil {
		t.Fatalf("ApplyBatch move: %v", err)
	}
	if rcpt2.Outcome != "changed" {
		t.Fatalf("expected outcome changed, got %q", rcpt2.Outcome)
	}
	if rcpt2.BatchDelta.ChangedCount != 1 || rcpt2.BatchDelta.GuardCount != 1 {
		t.Fatalf("unexpected counts: changed=%d guard=%d", rcpt2.BatchDelta.ChangedCount, rcpt2.BatchDelta.GuardCount)
	}

	// Verify post-state via ReadSet
	rs3, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1", "m2"})
	if err != nil {
		t.Fatalf("ReadSet after move: %v", err)
	}
	m1After, _ := rs3.Member("m1")
	if m1After.Revision != 2 || m1After.Row != "build" || m1After.Col != "working" || m1After.Score != 15 {
		t.Fatalf("unexpected m1 after move: %+v", m1After)
	}
	if m1After.Fields["status"] != "in-progress" || m1After.Fields["definition"] != "" {
		t.Fatalf("unexpected m1 fields: %+v", m1After.Fields)
	}
	m2After, _ := rs3.Member("m2")
	if m2After.Revision != 1 { // Guard-only was not modified
		t.Fatalf("m2 revision changed unexpectedly: %d", m2After.Revision)
	}

	// 10. Reserved field write refusal
	currentTableRev = c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	reservedFieldManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: currentTableRev,
		OperationID:           "op-reserved",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m1",
				Set: map[string]string{
					"epoch": "99",
				},
			},
		},
	}
	_, err = ntable.ApplyBatch(ctx, c, reservedFieldManifest)
	if !errors.Is(err, ntable.ErrReservedField) || !strings.Contains(err.Error(), "changed=no") {
		t.Fatalf("expected ErrReservedField with changed=no, got: %v", err)
	}

	// 11. Duplicate member refusal
	duplicateManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: currentTableRev,
		OperationID:           "op-duplicate",
		Members: []ntable.BatchMemberEntry{
			{ID: "m1", Remove: true},
			{ID: "m1", Remove: true},
		},
	}
	_, err = ntable.ApplyBatch(ctx, c, duplicateManifest)
	if !errors.Is(err, ntable.ErrDuplicateMember) || !strings.Contains(err.Error(), "TWICE") || !strings.Contains(err.Error(), "changed=no") {
		t.Fatalf("expected ErrDuplicateMember with TWICE and changed=no, got: %v", err)
	}

	// 12. Incompatible mutation refusal
	incompatibleManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: currentTableRev,
		OperationID:           "op-incompatible",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m3",
				Create: &ntable.MemberCreateOp{
					Row:   "build",
					Col:   "ready",
					Score: 1,
				},
				Remove: true,
			},
		},
	}
	_, err = ntable.ApplyBatch(ctx, c, incompatibleManifest)
	if !errors.Is(err, ntable.ErrMutation) || !strings.Contains(err.Error(), "changed=no") {
		t.Fatalf("expected ErrMutation with changed=no, got: %v", err)
	}

	// 13. Remove member via batch
	removeManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: currentTableRev,
		OperationID:           "op-remove",
		Members: []ntable.BatchMemberEntry{
			{
				ID:     "m2",
				Remove: true,
			},
		},
	}
	rcptRemove, err := ntable.ApplyBatch(ctx, c, removeManifest)
	if err != nil {
		t.Fatalf("ApplyBatch remove: %v", err)
	}
	if rcptRemove.Outcome != "changed" {
		t.Fatalf("expected outcome changed, got %q", rcptRemove.Outcome)
	}

	// ReadSet verifies m2 is unplaced
	rs4, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m2"})
	if err != nil {
		t.Fatalf("ReadSet after remove: %v", err)
	}
	m2Removed, _ := rs4.Member("m2")
	if m2Removed.Placed {
		t.Fatalf("expected m2 to be unplaced: %+v", m2Removed)
	}
}
