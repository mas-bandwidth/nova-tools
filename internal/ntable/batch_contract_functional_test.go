//go:build functional

package ntable_test

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

func boolPtr(b bool) *bool        { return &b }
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
				Expect: &ntable.MemberExpect{
					Revision: "1",
				},
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
				ID:     "m1",
				Expect: &ntable.MemberExpect{Revision: "2"},
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
			{ID: "m1", Expect: &ntable.MemberExpect{Revision: "2"}, Remove: true},
			{ID: "m1", Expect: &ntable.MemberExpect{Revision: "2"}, Remove: true},
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
				ID:     "m3",
				Expect: &ntable.MemberExpect{Absent: true},
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
				Expect: &ntable.MemberExpect{Revision: "1"},
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

func TestBatchReceiptReplayExhaustive(t *testing.T) {
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

	initTableRev := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()

	// 1. First batch: create m1 and m2
	manifest1 := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: initTableRev,
		OperationID:           "op-create-m1-m2",
		Actor:                 "builder-1",
		Members: []ntable.BatchMemberEntry{
			{
				ID:     "m1",
				Expect: &ntable.MemberExpect{Absent: true},
				Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 10},
				Set:    map[string]string{"env": "linux", "status": "pending"},
			},
			{
				ID:     "m2",
				Expect: &ntable.MemberExpect{Absent: true},
				Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 20},
				Set:    map[string]string{"env": "darwin"},
			},
		},
	}

	raw1, err := json.Marshal(manifest1)
	if err != nil {
		t.Fatalf("marshal manifest1: %v", err)
	}
	expectedDigest1 := fmt.Sprintf("%x", sha1.Sum(raw1))

	rcpt1, err := ntable.ApplyBatch(ctx, c, manifest1)
	if err != nil {
		t.Fatalf("ApplyBatch manifest1: %v", err)
	}
	if rcpt1.Outcome != "changed" {
		t.Fatalf("expected outcome changed, got %q", rcpt1.Outcome)
	}
	if rcpt1.BatchDelta == nil || rcpt1.BatchDelta.ChangedCount != 2 || rcpt1.BatchDelta.GuardCount != 0 {
		t.Fatalf("unexpected delta in rcpt1: %+v", rcpt1.BatchDelta)
	}

	// 4. Verify stored table:demo:op:<operation_id> hash fields
	opKey1 := ntable.DefKey("demo") + ":op:op-create-m1-m2"
	opRecord1, err := c.HGetAll(ctx, opKey1).Result()
	if err != nil {
		t.Fatalf("HGetAll %s: %v", opKey1, err)
	}
	if opRecord1["operation_id"] != "op-create-m1-m2" {
		t.Errorf("opRecord1 operation_id = %q, want %q", opRecord1["operation_id"], "op-create-m1-m2")
	}
	if opRecord1["digest"] != expectedDigest1 {
		t.Errorf("opRecord1 digest = %q, want %q", opRecord1["digest"], expectedDigest1)
	}
	if opRecord1["request"] != string(raw1) {
		t.Errorf("opRecord1 request mismatch: got %q, want %q", opRecord1["request"], string(raw1))
	}
	if opRecord1["stream_id"] != rcpt1.ID {
		t.Errorf("opRecord1 stream_id = %q, want %q", opRecord1["stream_id"], rcpt1.ID)
	}
	if opRecord1["epoch"] != "0" {
		t.Errorf("opRecord1 epoch = %q, want 0", opRecord1["epoch"])
	}
	if opRecord1["rev_before"] != strconv.FormatUint(rcpt1.Before, 10) {
		t.Errorf("opRecord1 rev_before = %q, want %d", opRecord1["rev_before"], rcpt1.Before)
	}
	if opRecord1["rev_after"] != strconv.FormatUint(rcpt1.After, 10) {
		t.Errorf("opRecord1 rev_after = %q, want %d", opRecord1["rev_after"], rcpt1.After)
	}
	if opRecord1["outcome"] != "changed" {
		t.Errorf("opRecord1 outcome = %q, want changed", opRecord1["outcome"])
	}
	if opRecord1["result"] == "" {
		t.Errorf("opRecord1 result is empty")
	}

	// 5. Verify stream event batch_delta field on table:demo:changes
	changesKey := ntable.ChangesKey("demo")
	events, err := c.XRange(ctx, changesKey, rcpt1.ID, rcpt1.ID).Result()
	if err != nil || len(events) != 1 {
		t.Fatalf("XRange for rcpt1.ID: %v, count=%d", err, len(events))
	}
	ev1 := events[0].Values
	if ev1["verb"] != "apply" {
		t.Errorf("ev1 verb = %v, want apply", ev1["verb"])
	}
	if ev1["outcome"] != "changed" {
		t.Errorf("ev1 outcome = %v, want changed", ev1["outcome"])
	}
	if fmt.Sprint(ev1["rev_before"]) != strconv.FormatUint(rcpt1.Before, 10) || fmt.Sprint(ev1["rev_after"]) != strconv.FormatUint(rcpt1.After, 10) {
		t.Errorf("ev1 revs = (%v, %v), want (%d, %d)", ev1["rev_before"], ev1["rev_after"], rcpt1.Before, rcpt1.After)
	}
	var streamDelta1 ntable.BatchDelta
	if err := json.Unmarshal([]byte(fmt.Sprint(ev1["batch_delta"])), &streamDelta1); err != nil {
		t.Fatalf("unmarshal stream batch_delta: %v", err)
	}
	if streamDelta1.OperationID != "op-create-m1-m2" || streamDelta1.ChangedCount != 2 || streamDelta1.GuardCount != 0 {
		t.Errorf("streamDelta1 mismatch: %+v", streamDelta1)
	}
	if len(streamDelta1.Members) != 2 {
		t.Fatalf("streamDelta1 members len = %d, want 2", len(streamDelta1.Members))
	}

	// 2. Second batch: No-op batch receipt (outcome: "noop", guard_count > 0, changed_count: 0)
	manifestNoop := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: strconv.FormatUint(rcpt1.After, 10),
		OperationID:           "op-noop-guards",
		Actor:                 "guard-checker",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m1",
				Expect: &ntable.MemberExpect{
					Revision: "1",
					Place:    &ntable.PlaceExpect{Row: "build", Col: "ready"},
					Fields: map[string]ntable.FieldGuard{
						"status": {Equals: strPtr("pending")},
					},
				},
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
	rcptNoop, err := ntable.ApplyBatch(ctx, c, manifestNoop)
	if err != nil {
		t.Fatalf("ApplyBatch manifestNoop: %v", err)
	}
	if rcptNoop.Outcome != "noop" {
		t.Fatalf("expected outcome noop, got %q", rcptNoop.Outcome)
	}
	if rcptNoop.BatchDelta == nil || rcptNoop.BatchDelta.GuardCount != 2 || rcptNoop.BatchDelta.ChangedCount != 0 {
		t.Fatalf("expected guard_count=2, changed_count=0: %+v", rcptNoop.BatchDelta)
	}

	// Verify no-op op record outcome
	opKeyNoop := ntable.DefKey("demo") + ":op:op-noop-guards"
	opRecordNoop, err := c.HGetAll(ctx, opKeyNoop).Result()
	if err != nil {
		t.Fatalf("HGetAll %s: %v", opKeyNoop, err)
	}
	if opRecordNoop["outcome"] != "noop" {
		t.Errorf("opRecordNoop outcome = %q, want noop", opRecordNoop["outcome"])
	}

	// Verify no-op stream event
	eventsNoop, err := c.XRange(ctx, changesKey, rcptNoop.ID, rcptNoop.ID).Result()
	if err != nil || len(eventsNoop) != 1 {
		t.Fatalf("XRange for rcptNoop.ID: %v, count=%d", err, len(eventsNoop))
	}
	evNoop := eventsNoop[0].Values
	if evNoop["outcome"] != "noop" {
		t.Errorf("evNoop outcome = %v, want noop", evNoop["outcome"])
	}

	// Advance the table revision further with a third batch (move m1)
	manifestMove := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: strconv.FormatUint(rcptNoop.After, 10),
		OperationID:           "op-move-m1",
		Actor:                 "builder-2",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m1",
				Expect: &ntable.MemberExpect{
					Revision: "1",
					Place:    &ntable.PlaceExpect{Row: "build", Col: "ready"},
				},
				Move: &ntable.MemberMoveOp{
					Row:   "build",
					Col:   "working",
					Score: floatPtr(50),
				},
				Set: map[string]string{"status": "in-progress"},
			},
		},
	}
	rcptMove, err := ntable.ApplyBatch(ctx, c, manifestMove)
	if err != nil {
		t.Fatalf("ApplyBatch manifestMove: %v", err)
	}
	if rcptMove.Outcome != "changed" {
		t.Fatalf("expected rcptMove outcome changed, got %q", rcptMove.Outcome)
	}

	// Table revision has advanced well past rcpt1 and rcptNoop!
	tableRevAdvanced := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	streamLenBeforeReplays, err := c.XLen(ctx, changesKey).Result()
	if err != nil {
		t.Fatalf("XLen: %v", err)
	}

	// 1. Replay applied batch 1 after table revision has advanced
	// (manifest1.ExpectedTableRevision is stale, but replay succeeds unconditionally!)
	rcpt1Replay, err := ntable.ApplyBatch(ctx, c, manifest1)
	if err != nil {
		t.Fatalf("replay manifest1 failed: %v", err)
	}
	if rcpt1Replay.ID != rcpt1.ID || rcpt1Replay.Before != rcpt1.Before || rcpt1Replay.After != rcpt1.After || rcpt1Replay.Outcome != rcpt1.Outcome {
		t.Fatalf("rcpt1Replay mismatch: got %+v, want %+v", rcpt1Replay, rcpt1)
	}
	if !reflect.DeepEqual(rcpt1Replay.BatchDelta, rcpt1.BatchDelta) {
		t.Fatalf("rcpt1Replay delta mismatch: got %+v, want %+v", rcpt1Replay.BatchDelta, rcpt1.BatchDelta)
	}

	// Assert zero new stream entries and table revision did not bump
	streamLenAfter1, _ := c.XLen(ctx, changesKey).Result()
	if streamLenAfter1 != streamLenBeforeReplays {
		t.Errorf("replay of manifest1 appended stream entry: len %d, want %d", streamLenAfter1, streamLenBeforeReplays)
	}
	tableRevAfter1 := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	if tableRevAfter1 != tableRevAdvanced {
		t.Errorf("replay of manifest1 bumped table revision: got %s, want %s", tableRevAfter1, tableRevAdvanced)
	}

	// Replay no-op batch after table revision has advanced
	rcptNoopReplay, err := ntable.ApplyBatch(ctx, c, manifestNoop)
	if err != nil {
		t.Fatalf("replay manifestNoop failed: %v", err)
	}
	if rcptNoopReplay.ID != rcptNoop.ID || rcptNoopReplay.Outcome != rcptNoop.Outcome || rcptNoopReplay.After != rcptNoop.After {
		t.Fatalf("rcptNoopReplay mismatch: got %+v, want %+v", rcptNoopReplay, rcptNoop)
	}
	streamLenAfterNoop, _ := c.XLen(ctx, changesKey).Result()
	if streamLenAfterNoop != streamLenBeforeReplays {
		t.Errorf("replay of manifestNoop appended stream entry: len %d, want %d", streamLenAfterNoop, streamLenBeforeReplays)
	}

	// 3. Conflicting payload with identical operation_id returns ErrOpConflict with changed=no
	conflicting1 := manifest1
	conflicting1.Actor = "malicious-actor"
	_, err = ntable.ApplyBatch(ctx, c, conflicting1)
	if !errors.Is(err, ntable.ErrOpConflict) {
		t.Fatalf("expected ErrOpConflict, got: %v", err)
	}
	if !strings.Contains(err.Error(), "changed=no") {
		t.Fatalf("expected changed=no in error message: %v", err)
	}

	conflictingNoop := manifestNoop
	conflictingNoop.ExpectedTableRevision = "0"
	_, err = ntable.ApplyBatch(ctx, c, conflictingNoop)
	if !errors.Is(err, ntable.ErrOpConflict) {
		t.Fatalf("expected ErrOpConflict for noop replay conflict, got: %v", err)
	}
	if !strings.Contains(err.Error(), "changed=no") {
		t.Fatalf("expected changed=no in error message: %v", err)
	}

	// Verify state remained unchanged after conflicts
	streamLenFinal, _ := c.XLen(ctx, changesKey).Result()
	if streamLenFinal != streamLenBeforeReplays {
		t.Errorf("conflicts appended to stream: %d vs %d", streamLenFinal, streamLenBeforeReplays)
	}
}

func TestBatchDualStoreReplayFromStream(t *testing.T) {
	t.Parallel()
	_, c1 := live(t)
	_, c2 := live(t)
	ctx := context.Background()

	tb := demo()
	if err := ntable.Create(ctx, c1, tb, now); err != nil {
		t.Fatal(err)
	}
	if err := ntable.Create(ctx, c2, tb, now); err != nil {
		t.Fatal(err)
	}
	for _, row := range []string{"build", "test"} {
		if _, err := ntable.RowAdd(ctx, c1, "demo", row, ntable.RowSpec{}); err != nil {
			t.Fatal(err)
		}
		if _, err := ntable.RowAdd(ctx, c2, "demo", row, ntable.RowSpec{}); err != nil {
			t.Fatal(err)
		}
	}

	// Sequence of batches applied on store 1:
	// Batch 1: Create m1 and m2
	rev1 := c1.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	m1 := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: rev1,
		OperationID:           "op-s1-1",
		Actor:                 "worker-a",
		Members: []ntable.BatchMemberEntry{
			{
				ID:     "m1",
				Expect: &ntable.MemberExpect{Absent: true},
				Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 100},
				Set:    map[string]string{"flavor": "vanilla", "priority": "high"},
			},
			{
				ID:     "m2",
				Expect: &ntable.MemberExpect{Absent: true},
				Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 200},
				Set:    map[string]string{"flavor": "chocolate"},
			},
		},
	}
	if _, err := ntable.ApplyBatch(ctx, c1, m1); err != nil {
		t.Fatalf("batch 1 on c1: %v", err)
	}

	// Batch 2: Move m1 to working, update fields on m2
	rev2 := c1.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	m2 := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: rev2,
		OperationID:           "op-s1-2",
		Actor:                 "worker-b",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m1",
				Expect: &ntable.MemberExpect{
					Revision: "1",
					Place:    &ntable.PlaceExpect{Row: "build", Col: "ready"},
				},
				Move: &ntable.MemberMoveOp{Row: "build", Col: "working", Score: floatPtr(150)},
				Set:  map[string]string{"state": "running"},
			},
			{
				ID: "m2",
				Expect: &ntable.MemberExpect{
					Revision: "1",
					Fields:   map[string]ntable.FieldGuard{"flavor": {Equals: strPtr("chocolate")}},
				},
				Set: map[string]string{"extra": "sprinkles"},
			},
		},
	}
	if _, err := ntable.ApplyBatch(ctx, c1, m2); err != nil {
		t.Fatalf("batch 2 on c1: %v", err)
	}

	// Batch 3: No-op batch (guards only)
	rev3 := c1.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	m3 := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: rev3,
		OperationID:           "op-s1-3",
		Actor:                 "auditor",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m1",
				Expect: &ntable.MemberExpect{
					Revision: "2",
					Place:    &ntable.PlaceExpect{Row: "build", Col: "working"},
				},
			},
			{
				ID: "m2",
				Expect: &ntable.MemberExpect{
					Revision: "2",
					Place:    &ntable.PlaceExpect{Row: "build", Col: "ready"},
				},
			},
		},
	}
	if _, err := ntable.ApplyBatch(ctx, c1, m3); err != nil {
		t.Fatalf("batch 3 on c1: %v", err)
	}

	// Batch 4: Remove m1, move m2 to test:ready
	rev4 := c1.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	m4 := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: rev4,
		OperationID:           "op-s1-4",
		Actor:                 "worker-c",
		Members: []ntable.BatchMemberEntry{
			{
				ID:     "m1",
				Expect: &ntable.MemberExpect{Revision: "2"},
				Remove: true,
			},
			{
				ID: "m2",
				Expect: &ntable.MemberExpect{
					Revision: "2",
					Place:    &ntable.PlaceExpect{Row: "build", Col: "ready"},
				},
				Move:  &ntable.MemberMoveOp{Row: "test", Col: "ready", Score: floatPtr(250)},
				Unset: []string{"extra"},
			},
		},
	}
	if _, err := ntable.ApplyBatch(ctx, c1, m4); err != nil {
		t.Fatalf("batch 4 on c1: %v", err)
	}

	// Read all stream events from store 1:
	streamEvents1, err := c1.XRange(ctx, ntable.ChangesKey("demo"), "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange c1: %v", err)
	}

	// Replay each batch stream event sequentially on store 2:
	replayedCount := 0
	for _, event := range streamEvents1 {
		v := event.Values
		if v["verb"] != "apply" {
			continue
		}
		replayedCount++
		var args []string
		if err := json.Unmarshal([]byte(fmt.Sprint(v["args"])), &args); err != nil {
			t.Fatalf("unmarshal event args: %v", err)
		}
		tableName := args[0]
		opID := args[1]

		// Fetch the durable request payload from store 1's operation record:
		reqJSON, err := c1.HGet(ctx, ntable.DefKey(tableName)+":op:"+opID, "request").Result()
		if err != nil {
			t.Fatalf("HGet request for op %s from c1: %v", opID, err)
		}

		var replayManifest ntable.BatchManifest
		if err := json.Unmarshal([]byte(reqJSON), &replayManifest); err != nil {
			t.Fatalf("unmarshal replay manifest: %v", err)
		}

		// Replay on store 2:
		rcpt2, err := ntable.ApplyBatch(ctx, c2, replayManifest)
		if err != nil {
			t.Fatalf("replay batch %s on c2: %v", opID, err)
		}

		// Assert receipt from store 2 matches the stream event on store 1:
		if strconv.FormatUint(rcpt2.Before, 10) != fmt.Sprint(v["rev_before"]) {
			t.Errorf("op %s replay rev_before = %d, want %v", opID, rcpt2.Before, v["rev_before"])
		}
		if strconv.FormatUint(rcpt2.After, 10) != fmt.Sprint(v["rev_after"]) {
			t.Errorf("op %s replay rev_after = %d, want %v", opID, rcpt2.After, v["rev_after"])
		}
		if rcpt2.Outcome != fmt.Sprint(v["outcome"]) {
			t.Errorf("op %s replay outcome = %s, want %v", opID, rcpt2.Outcome, v["outcome"])
		}
	}
	if replayedCount != 4 {
		t.Fatalf("expected 4 batch stream events replayed, got %d", replayedCount)
	}

	// Assert store 1 and store 2 reached identical state:
	// 1. Table revisions match
	c1Rev := c1.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	c2Rev := c2.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	if c1Rev != c2Rev {
		t.Fatalf("table revision mismatch: c1=%s, c2=%s", c1Rev, c2Rev)
	}

	// 2. ReadSet across all members returns identical state
	rs1, err := ntable.ReadSetMembers(ctx, c1, "demo", []string{"m1", "m2", "m3"})
	if err != nil {
		t.Fatalf("ReadSet c1: %v", err)
	}
	rs2, err := ntable.ReadSetMembers(ctx, c2, "demo", []string{"m1", "m2", "m3"})
	if err != nil {
		t.Fatalf("ReadSet c2: %v", err)
	}
	if !reflect.DeepEqual(rs1, rs2) {
		t.Fatalf("ReadSet mismatch between stores:\nstore 1: %+v\nstore 2: %+v", rs1, rs2)
	}

	// 3. Stream event counts and event contents match (excluding stream ID timestamps)
	streamEvents2, err := c2.XRange(ctx, ntable.ChangesKey("demo"), "-", "+").Result()
	if err != nil {
		t.Fatalf("XRange c2: %v", err)
	}
	if len(streamEvents1) != len(streamEvents2) {
		t.Fatalf("stream length mismatch: c1 has %d, c2 has %d", len(streamEvents1), len(streamEvents2))
	}
	for i := range streamEvents1 {
		v1 := streamEvents1[i].Values
		v2 := streamEvents2[i].Values
		for _, key := range []string{"verb", "args", "epoch", "rev_before", "rev_after", "actor", "outcome", "batch_delta", "cells", "members"} {
			if fmt.Sprint(v1[key]) != fmt.Sprint(v2[key]) {
				t.Errorf("event %d key %s mismatch: c1=%v, c2=%v", i, key, v1[key], v2[key])
			}
		}
	}

	// 4. Verify cell scores and member hashes match
	for _, cell := range []string{"build:ready", "build:working", "test:ready"} {
		z1 := c1.ZRangeWithScores(ctx, "table:demo:cell:"+cell, 0, -1).Val()
		z2 := c2.ZRangeWithScores(ctx, "table:demo:cell:"+cell, 0, -1).Val()
		if !reflect.DeepEqual(z1, z2) {
			t.Errorf("cell %s mismatch: c1=%v, c2=%v", cell, z1, z2)
		}
	}
	m2Hash1 := c1.HGetAll(ctx, "table::member:m2").Val()
	m2Hash2 := c2.HGetAll(ctx, "table::member:m2").Val()
	if !reflect.DeepEqual(m2Hash1, m2Hash2) {
		t.Errorf("member m2 hash mismatch: c1=%v, c2=%v", m2Hash1, m2Hash2)
	}
}

func TestReviewWrongTypeDestinationPartialWrite(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	if err := ntable.Create(ctx, c, demo(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellAdd(ctx, c, "demo", "build", "ready", "m1", 7); err != nil {
		t.Fatal(err)
	}
	dest := ntable.CellKey("demo", "build", "working")
	if err := c.Set(ctx, dest, "wrong-type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	before := storeImage(t, c)
	rev := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	manifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: rev,
		OperationID:           "review-wrongtype",
		Actor:                 "review",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m1",
				Expect: &ntable.MemberExpect{
					Place: &ntable.PlaceExpect{Row: "build", Col: "ready"},
				},
				Move: &ntable.MemberMoveOp{
					Row: "build",
					Col: "working",
				},
			},
		},
	}
	_, err := ntable.ApplyBatch(ctx, c, manifest)
	after := storeImage(t, c)
	if err == nil {
		t.Fatal("expected error on destination WRONGTYPE, got nil")
	}
	if !errors.Is(err, ntable.ErrWrongType) {
		t.Fatalf("expected ErrWrongType, got: %v", err)
	}
	if !strings.Contains(err.Error(), "changed=no") {
		t.Fatalf("expected changed=no, got: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("expected store unchanged on destination WRONGTYPE refusal")
	}
	if score := c.ZScore(ctx, ntable.CellKey("demo", "build", "ready"), "m1").Val(); score != 7 {
		t.Fatalf("expected m1 score 7 in source, got %v", score)
	}
}

func TestReviewBatchStreamWrongTypePartialWrite(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	if err := ntable.Create(ctx, c, demo(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(ctx, ntable.DefKey("demo")+":changes", "wrong-type", 0).Err(); err != nil {
		t.Fatal(err)
	}
	before := storeImage(t, c)
	rev := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	manifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: rev,
		OperationID:           "review-stream",
		Actor:                 "review",
		Members: []ntable.BatchMemberEntry{
			{
				ID:     "m1",
				Expect: &ntable.MemberExpect{Absent: true},
				Create: &ntable.MemberCreateOp{
					Row:   "build",
					Col:   "ready",
					Score: 3,
				},
			},
		},
	}
	_, err := ntable.ApplyBatch(ctx, c, manifest)
	after := storeImage(t, c)
	if err == nil {
		t.Fatal("expected error on changes stream WRONGTYPE, got nil")
	}
	if !errors.Is(err, ntable.ErrWrongType) {
		t.Fatalf("expected ErrWrongType, got: %v", err)
	}
	if !strings.Contains(err.Error(), "changed=no") {
		t.Fatalf("expected changed=no, got: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("expected store unchanged on stream WRONGTYPE refusal")
	}
	if placed := c.ZScore(ctx, ntable.CellKey("demo", "build", "ready"), "m1").Val(); placed != 0 {
		t.Fatalf("expected m1 not placed, got %v", placed)
	}
	if revAfter := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val(); revAfter != rev {
		t.Fatalf("expected revision unchanged (%s), got %s", rev, revAfter)
	}
}

func TestReviewReadSetHidesPlacementDrift(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	if err := ntable.Create(ctx, c, demo(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellAdd(ctx, c, "demo", "build", "ready", "m1", 7); err != nil {
		t.Fatal(err)
	}
	if err := c.ZRem(ctx, ntable.CellKey("demo", "build", "ready"), "m1").Err(); err != nil {
		t.Fatal(err)
	}
	_, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1"})
	if err == nil {
		t.Fatal("expected ReadSetMembers to refuse placement drift, got nil")
	}
	if !errors.Is(err, ntable.ErrDrift) {
		t.Fatalf("expected ErrDrift, got: %v", err)
	}
}

func TestReviewHiddenDuplicatePlacementAccepted(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	if err := ntable.Create(ctx, c, demo(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellAdd(ctx, c, "demo", "build", "ready", "m1", 7); err != nil {
		t.Fatal(err)
	}
	hidden := ntable.CellKey("demo", "build", "working")
	if err := c.ZAdd(ctx, hidden, redis.Z{Score: 9, Member: "m1"}).Err(); err != nil {
		t.Fatal(err)
	}
	before := storeImage(t, c)
	rev := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	manifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: rev,
		OperationID:           "review-hidden",
		Actor:                 "review",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m1",
				Expect: &ntable.MemberExpect{
					Place: &ntable.PlaceExpect{Row: "build", Col: "ready"},
				},
				Move: &ntable.MemberMoveOp{
					Row: "build",
					Col: "done",
				},
			},
		},
	}
	_, err := ntable.ApplyBatch(ctx, c, manifest)
	after := storeImage(t, c)
	if err == nil {
		t.Fatal("expected ApplyBatch to refuse hidden duplicate placement, got nil")
	}
	if !errors.Is(err, ntable.ErrDrift) {
		t.Fatalf("expected ErrDrift, got: %v", err)
	}
	if !strings.Contains(err.Error(), "changed=no") {
		t.Fatalf("expected changed=no, got: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("expected store unchanged on hidden duplicate refusal")
	}
	if destScore := c.ZScore(ctx, ntable.CellKey("demo", "build", "done"), "m1").Val(); destScore != 0 {
		t.Fatalf("expected dest score 0, got %v", destScore)
	}
}

func TestReviewUnknownAndDuplicateJSONAccepted(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	if err := ntable.Create(ctx, c, demo(), now); err != nil {
		t.Fatal(err)
	}
	rev := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()

	// 1. Unknown top-level field rejected with REFUSED MANIFEST and zero mutation
	rawUnknown := fmt.Sprintf(`{"schema":1,"table":"demo","epoch":"0","expected_table_revision":%q,"operation_id":"review-unknown","actor":"review","members":[],"unknown":true}`, rev)
	before := storeImage(t, c)
	ans, err := c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey("demo")}, "demo", rawUnknown).Slice()
	after := storeImage(t, c)
	if err != nil {
		t.Fatalf("FCall err: %v", err)
	}
	if len(ans) < 2 || ans[0] != "REFUSED" || ans[1] != "MANIFEST" {
		t.Fatalf("expected REFUSED MANIFEST for unknown top-level field, got: %v", ans)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("expected store unchanged after unknown field refusal")
	}

	// 2. Duplicate top-level key rejected with REFUSED MANIFEST and zero mutation
	rawDuplicate := fmt.Sprintf(`{"schema":1,"table":"demo","epoch":"0","expected_table_revision":%q,"operation_id":"review-duplicate","actor":"first","actor":"second","members":[]}`, rev)
	before2 := storeImage(t, c)
	ans, err = c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey("demo")}, "demo", rawDuplicate).Slice()
	after2 := storeImage(t, c)
	if err != nil {
		t.Fatalf("FCall err: %v", err)
	}
	if len(ans) < 2 || ans[0] != "REFUSED" || ans[1] != "MANIFEST" {
		t.Fatalf("expected REFUSED MANIFEST for duplicate top-level key, got: %v", ans)
	}
	if !reflect.DeepEqual(before2, after2) {
		t.Fatal("expected store unchanged after duplicate key refusal")
	}

	// 3. Nested unknown key inside expect rejected
	rawNestedUnknown := fmt.Sprintf(`{"schema":1,"table":"demo","epoch":"0","expected_table_revision":%q,"operation_id":"review-nested-unknown","actor":"review","members":[{"id":"m1","expect":{"revision":"1","unknown":true}}]}`, rev)
	ans, err = c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey("demo")}, "demo", rawNestedUnknown).Slice()
	if err != nil {
		t.Fatalf("FCall err: %v", err)
	}
	if len(ans) < 2 || ans[0] != "REFUSED" || ans[1] != "MANIFEST" {
		t.Fatalf("expected REFUSED MANIFEST for nested unknown key, got: %v", ans)
	}

	// 4. Nested duplicate key inside expect rejected
	rawNestedDuplicate := fmt.Sprintf(`{"schema":1,"table":"demo","epoch":"0","expected_table_revision":%q,"operation_id":"review-nested-duplicate","actor":"review","members":[{"id":"m1","expect":{"revision":"1","revision":"2"}}]}`, rev)
	ans, err = c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey("demo")}, "demo", rawNestedDuplicate).Slice()
	if err != nil {
		t.Fatalf("FCall err: %v", err)
	}
	if len(ans) < 2 || ans[0] != "REFUSED" || ans[1] != "MANIFEST" {
		t.Fatalf("expected REFUSED MANIFEST for nested duplicate key, got: %v", ans)
	}
}
