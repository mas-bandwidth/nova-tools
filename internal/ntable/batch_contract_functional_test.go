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

func TestBatchContractWrongTypeMemberRefusal(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	if err := ntable.Create(ctx, c, demo(), now); err != nil {
		t.Fatal(err)
	}
	rev := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()

	// Pre-populate member key as a Redis string instead of hash
	mkey := ntable.MemberKey("m_bad")
	if err := c.Set(ctx, mkey, "string-not-hash", 0).Err(); err != nil {
		t.Fatal(err)
	}

	before := storeImage(t, c)
	manifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: rev,
		OperationID:           "op-bad-mkey-type",
		Actor:                 "tester",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m_bad",
				Expect: &ntable.MemberExpect{
					Absent: true,
				},
				Create: &ntable.MemberCreateOp{
					Row:   "build",
					Col:   "ready",
					Score: 10,
				},
			},
		},
	}
	_, err := ntable.ApplyBatch(ctx, c, manifest)
	after := storeImage(t, c)
	if err == nil {
		t.Fatal("expected ApplyBatch to fail on wrong type member key")
	}
	if !errors.Is(err, ntable.ErrWrongType) {
		t.Fatalf("expected ErrWrongType, got: %v", err)
	}
	if !strings.Contains(err.Error(), "changed=no") {
		t.Fatalf("expected changed=no, got: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("store changed on wrong type member key refusal")
	}
}

func TestBatchContractRemoveAndOverlapValidation(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	if err := ntable.Create(ctx, c, demo(), now); err != nil {
		t.Fatal(err)
	}
	rev := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()

	// 1. remove: false in raw JSON refuses before writes
	before1 := storeImage(t, c)
	rawRemoveFalse := fmt.Sprintf(`{"schema":1,"table":"demo","epoch":"0","expected_table_revision":%q,"operation_id":"op-rem-false","actor":"test","members":[{"id":"m1","expect":{"revision":"0"},"remove":false}]}`, rev)
	ans, err := c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey("demo")}, "demo", rawRemoveFalse).Slice()
	after1 := storeImage(t, c)
	if err != nil {
		t.Fatalf("FCall err: %v", err)
	}
	if len(ans) < 2 || ans[0] != "REFUSED" || ans[1] != "ARGS" {
		t.Fatalf("expected REFUSED ARGS for remove:false, got: %v", ans)
	}
	if !reflect.DeepEqual(before1, after1) {
		t.Fatal("store changed on remove:false refusal")
	}

	// 2. remove: true on unplaced member refuses NOTMEMBER
	before2 := storeImage(t, c)
	manifestUnplacedRemove := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: rev,
		OperationID:           "op-rem-unplaced",
		Actor:                 "test",
		Members: []ntable.BatchMemberEntry{
			{
				ID:     "m_unplaced",
				Expect: &ntable.MemberExpect{Absent: false},
				Remove: true,
			},
		},
	}
	_, err = ntable.ApplyBatch(ctx, c, manifestUnplacedRemove)
	after2 := storeImage(t, c)
	if err == nil {
		t.Fatal("expected failure on unplaced member remove")
	}
	if !errors.Is(err, ntable.ErrNotMember) {
		t.Fatalf("expected ErrNotMember, got: %v", err)
	}
	if !strings.Contains(err.Error(), "changed=no") {
		t.Fatalf("expected changed=no, got: %v", err)
	}
	if !reflect.DeepEqual(before2, after2) {
		t.Fatal("store changed on unplaced member remove refusal")
	}

	// 3. Set + Unset overlap on same field refuses MUTATION
	before3 := storeImage(t, c)
	manifestOverlap := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: rev,
		OperationID:           "op-overlap",
		Actor:                 "test",
		Members: []ntable.BatchMemberEntry{
			{
				ID:     "m1",
				Expect: &ntable.MemberExpect{Absent: false},
				Set:    map[string]string{"role": "worker"},
				Unset:  []string{"role"},
			},
		},
	}
	_, err = ntable.ApplyBatch(ctx, c, manifestOverlap)
	after3 := storeImage(t, c)
	if err == nil {
		t.Fatal("expected failure on set/unset overlap")
	}
	if !errors.Is(err, ntable.ErrMutation) {
		t.Fatalf("expected ErrMutation, got: %v", err)
	}
	if !strings.Contains(err.Error(), "changed=no") {
		t.Fatalf("expected changed=no, got: %v", err)
	}
	if !reflect.DeepEqual(before3, after3) {
		t.Fatal("store changed on set/unset overlap refusal")
	}
}

func TestBatchContractMoveNoopAndScoreSemantics(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	if err := ntable.Create(ctx, c, demo(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	rev := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()

	// Initial create of m1 at build:ready with score 10
	createManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: rev,
		OperationID:           "op-create-m1",
		Actor:                 "test",
		Members: []ntable.BatchMemberEntry{
			{
				ID:     "m1",
				Expect: &ntable.MemberExpect{Absent: true},
				Create: &ntable.MemberCreateOp{
					Row:   "build",
					Col:   "ready",
					Score: 10,
				},
				Set: map[string]string{"env": "prod"},
			},
		},
	}
	rcptCreate, err := ntable.ApplyBatch(ctx, c, createManifest)
	if err != nil {
		t.Fatalf("create m1: %v", err)
	}
	if rcptCreate.Outcome != "changed" {
		t.Fatalf("expected outcome changed, got %q", rcptCreate.Outcome)
	}

	// 1. Move to same cell with same score and no field changes is a NO-OP
	revAfterCreate := strconv.FormatUint(rcptCreate.After, 10)
	score10 := float64(10)
	noopManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: revAfterCreate,
		OperationID:           "op-move-noop",
		Actor:                 "test",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m1",
				Expect: &ntable.MemberExpect{
					Revision: "1",
					Place:    &ntable.PlaceExpect{Row: "build", Col: "ready"},
				},
				Move: &ntable.MemberMoveOp{
					Row:   "build",
					Col:   "ready",
					Score: &score10,
				},
			},
		},
	}
	rcptNoop, err := ntable.ApplyBatch(ctx, c, noopManifest)
	if err != nil {
		t.Fatalf("apply noop move: %v", err)
	}
	if rcptNoop.Outcome != "noop" {
		t.Fatalf("expected outcome noop, got %q", rcptNoop.Outcome)
	}
	if rcptNoop.BatchDelta.ChangedCount != 0 {
		t.Fatalf("expected changed_count=0 for noop move, got %d", rcptNoop.BatchDelta.ChangedCount)
	}
	// Entire batch of no-ops increments table revision once and records receipt
	if rcptNoop.After != rcptCreate.After+1 {
		t.Fatalf("expected table revision to increment to %d, got %d", rcptCreate.After+1, rcptNoop.After)
	}
	// Verify member revision was NOT incremented
	rsNoop, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1"})
	if err != nil {
		t.Fatalf("ReadSet: %v", err)
	}
	m1Noop, ok := rsNoop.Member("m1")
	if !ok || m1Noop.Revision != 1 {
		t.Fatalf("expected m1 revision to remain 1 after no-op move, got %d", m1Noop.Revision)
	}

	// 2. Score-only change increments member revision once
	revAfterNoop := strconv.FormatUint(rcptNoop.After, 10)
	score25 := float64(25)
	scoreChangeManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: revAfterNoop,
		OperationID:           "op-move-score-only",
		Actor:                 "test",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m1",
				Expect: &ntable.MemberExpect{
					Revision: "1",
					Place:    &ntable.PlaceExpect{Row: "build", Col: "ready"},
				},
				Move: &ntable.MemberMoveOp{
					Row:   "build",
					Col:   "ready",
					Score: &score25,
				},
			},
		},
	}
	rcptScore, err := ntable.ApplyBatch(ctx, c, scoreChangeManifest)
	if err != nil {
		t.Fatalf("apply score change: %v", err)
	}
	if rcptScore.Outcome != "changed" {
		t.Fatalf("expected outcome changed, got %q", rcptScore.Outcome)
	}
	if rcptScore.BatchDelta.ChangedCount != 1 {
		t.Fatalf("expected changed_count=1 for score change, got %d", rcptScore.BatchDelta.ChangedCount)
	}
	rsScore, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1"})
	if err != nil {
		t.Fatalf("ReadSet: %v", err)
	}
	m1Score, ok := rsScore.Member("m1")
	if !ok || m1Score.Revision != 2 || m1Score.Score != 25 {
		t.Fatalf("expected m1 revision 2 and score 25, got rev=%d score=%g", m1Score.Revision, m1Score.Score)
	}
}

func TestBatchContractReceiptBeforeAfterDeltas(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	if err := ntable.Create(ctx, c, demo(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	rev := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()

	// Initial batch: create m1 with fields and m3 placed
	initManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: rev,
		OperationID:           "op-init-delta",
		Actor:                 "test",
		Members: []ntable.BatchMemberEntry{
			{
				ID:     "m1",
				Expect: &ntable.MemberExpect{Absent: true},
				Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 10},
				Set:    map[string]string{"role": "dev", "notes": ""},
			},
			{
				ID:     "m3",
				Expect: &ntable.MemberExpect{Absent: true},
				Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 30},
			},
		},
	}
	rcptInit, err := ntable.ApplyBatch(ctx, c, initManifest)
	if err != nil {
		t.Fatalf("init batch: %v", err)
	}

	// Mixed batch:
	// - m1: move to build:done with score 15, set {"role":"lead", "team":"core"}, unset ["notes"]
	// - m2: create at build:ready with score 5, set {"role":"intern"}
	// - m3: remove from table
	score15 := float64(15)
	mixedManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: strconv.FormatUint(rcptInit.After, 10),
		OperationID:           "op-mixed-deltas",
		Actor:                 "test",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m1",
				Expect: &ntable.MemberExpect{
					Revision: "1",
					Place:    &ntable.PlaceExpect{Row: "build", Col: "ready"},
				},
				Move:  &ntable.MemberMoveOp{Row: "build", Col: "done", Score: &score15},
				Set:   map[string]string{"role": "lead", "team": "core"},
				Unset: []string{"notes"},
			},
			{
				ID:     "m2",
				Expect: &ntable.MemberExpect{Absent: true},
				Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 5},
				Set:    map[string]string{"role": "intern"},
			},
			{
				ID:     "m3",
				Expect: &ntable.MemberExpect{Revision: "1", Place: &ntable.PlaceExpect{Row: "build", Col: "ready"}},
				Remove: true,
			},
		},
	}

	rcptMixed, err := ntable.ApplyBatch(ctx, c, mixedManifest)
	if err != nil {
		t.Fatalf("apply mixed batch: %v", err)
	}

	deltas := make(map[string]ntable.BatchMemberDelta)
	for _, m := range rcptMixed.BatchDelta.Members {
		deltas[m.ID] = m
	}

	// Verify m1 deltas
	d1, ok := deltas["m1"]
	if !ok {
		t.Fatal("missing m1 delta")
	}
	if d1.BeforePlace != "build:ready" || d1.AfterPlace != "build:done" {
		t.Fatalf("m1 place before=%q after=%q", d1.BeforePlace, d1.AfterPlace)
	}
	if d1.BeforeScore == nil || *d1.BeforeScore != 10 {
		t.Fatalf("m1 before_score want 10, got %v", d1.BeforeScore)
	}
	if d1.AfterScore == nil || *d1.AfterScore != 15 {
		t.Fatalf("m1 after_score want 15, got %v", d1.AfterScore)
	}
	// m1 fields: role before="dev" after="lead"
	if fRole, ok := d1.Fields["role"]; !ok || fRole.Before == nil || *fRole.Before != "dev" || fRole.After == nil || *fRole.After != "lead" {
		t.Fatalf("m1 role field delta mismatch: %+v", fRole)
	}
	// m1 fields: team before=nil after="core"
	if fTeam, ok := d1.Fields["team"]; !ok || fTeam.Before != nil || fTeam.After == nil || *fTeam.After != "core" {
		t.Fatalf("m1 team field delta mismatch (want nil before): %+v", fTeam)
	}
	// m1 fields: notes before="" after=nil (empty string distinct from absent!)
	if fNotes, ok := d1.Fields["notes"]; !ok || fNotes.Before == nil || *fNotes.Before != "" || fNotes.After != nil {
		t.Fatalf("m1 notes field delta mismatch (want \"\" before, nil after): %+v", fNotes)
	}

	// Verify m2 deltas (create)
	d2, ok := deltas["m2"]
	if !ok {
		t.Fatal("missing m2 delta")
	}
	if d2.BeforePlace != "" || d2.AfterPlace != "build:ready" {
		t.Fatalf("m2 place before=%q after=%q", d2.BeforePlace, d2.AfterPlace)
	}
	if d2.BeforeScore != nil {
		t.Fatalf("m2 created before_score should be nil, got %v", d2.BeforeScore)
	}
	if d2.AfterScore == nil || *d2.AfterScore != 5 {
		t.Fatalf("m2 after_score want 5, got %v", d2.AfterScore)
	}
	if fRole, ok := d2.Fields["role"]; !ok || fRole.Before != nil || fRole.After == nil || *fRole.After != "intern" {
		t.Fatalf("m2 role field delta mismatch (want nil before): %+v", fRole)
	}

	// Verify m3 deltas (remove)
	d3, ok := deltas["m3"]
	if !ok {
		t.Fatal("missing m3 delta")
	}
	if d3.BeforePlace != "build:ready" || d3.AfterPlace != "" {
		t.Fatalf("m3 place before=%q after=%q", d3.BeforePlace, d3.AfterPlace)
	}
	if d3.BeforeScore == nil || *d3.BeforeScore != 30 {
		t.Fatalf("m3 before_score want 30, got %v", d3.BeforeScore)
	}
	if d3.AfterScore != nil {
		t.Fatalf("m3 removed after_score should be nil, got %v", d3.AfterScore)
	}
}

func TestBatchContractRefusalStoreImagePreserved(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	if err := ntable.Create(ctx, c, demo(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	rev := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()

	// Initial setup: place m1 at build:ready with field role=worker
	initManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: rev,
		OperationID:           "op-store-img-init",
		Actor:                 "test",
		Members: []ntable.BatchMemberEntry{
			{
				ID:     "m1",
				Expect: &ntable.MemberExpect{Absent: true},
				Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 10},
				Set:    map[string]string{"role": "worker"},
			},
		},
	}
	rcptInit, err := ntable.ApplyBatch(ctx, c, initManifest)
	if err != nil {
		t.Fatalf("init batch: %v", err)
	}
	curRev := strconv.FormatUint(rcptInit.After, 10)

	valWrong := "manager"
	valCorrect := "worker"

	cases := []struct {
		name     string
		manifest ntable.BatchManifest
	}{
		{
			name: "stale table revision",
			manifest: ntable.BatchManifest{
				Schema:                1,
				Table:                 "demo",
				Epoch:                 "0",
				ExpectedTableRevision: "9999",
				OperationID:           "op-ref-stale-rev",
				Actor:                 "test",
				Members: []ntable.BatchMemberEntry{
					{ID: "m1", Expect: &ntable.MemberExpect{Revision: "1"}},
				},
			},
		},
		{
			name: "stale table epoch",
			manifest: ntable.BatchManifest{
				Schema:                1,
				Table:                 "demo",
				Epoch:                 "999",
				ExpectedTableRevision: curRev,
				OperationID:           "op-ref-stale-epoch",
				Actor:                 "test",
				Members: []ntable.BatchMemberEntry{
					{ID: "m1", Expect: &ntable.MemberExpect{Revision: "1"}},
				},
			},
		},
		{
			name: "member revision mismatch",
			manifest: ntable.BatchManifest{
				Schema:                1,
				Table:                 "demo",
				Epoch:                 "0",
				ExpectedTableRevision: curRev,
				OperationID:           "op-ref-mem-rev",
				Actor:                 "test",
				Members: []ntable.BatchMemberEntry{
					{ID: "m1", Expect: &ntable.MemberExpect{Revision: "99"}},
				},
			},
		},
		{
			name: "field guard equals mismatch",
			manifest: ntable.BatchManifest{
				Schema:                1,
				Table:                 "demo",
				Epoch:                 "0",
				ExpectedTableRevision: curRev,
				OperationID:           "op-ref-fg-equals",
				Actor:                 "test",
				Members: []ntable.BatchMemberEntry{
					{
						ID: "m1",
						Expect: &ntable.MemberExpect{
							Revision: "1",
							Fields:   map[string]ntable.FieldGuard{"role": {Equals: &valWrong}},
						},
					},
				},
			},
		},
		{
			name: "field guard absent mismatch",
			manifest: ntable.BatchManifest{
				Schema:                1,
				Table:                 "demo",
				Epoch:                 "0",
				ExpectedTableRevision: curRev,
				OperationID:           "op-ref-fg-absent",
				Actor:                 "test",
				Members: []ntable.BatchMemberEntry{
					{
						ID: "m1",
						Expect: &ntable.MemberExpect{
							Revision: "1",
							Fields:   map[string]ntable.FieldGuard{"role": {Absent: boolPtr(true)}},
						},
					},
				},
			},
		},
		{
			name: "field guard one_of mismatch",
			manifest: ntable.BatchManifest{
				Schema:                1,
				Table:                 "demo",
				Epoch:                 "0",
				ExpectedTableRevision: curRev,
				OperationID:           "op-ref-fg-oneof",
				Actor:                 "test",
				Members: []ntable.BatchMemberEntry{
					{
						ID: "m1",
						Expect: &ntable.MemberExpect{
							Revision: "1",
							Fields:   map[string]ntable.FieldGuard{"role": {OneOf: []string{"lead", "director"}}},
						},
					},
				},
			},
		},
		{
			name: "duplicate member ID (TWICE)",
			manifest: ntable.BatchManifest{
				Schema:                1,
				Table:                 "demo",
				Epoch:                 "0",
				ExpectedTableRevision: curRev,
				OperationID:           "op-ref-twice",
				Actor:                 "test",
				Members: []ntable.BatchMemberEntry{
					{ID: "m1", Expect: &ntable.MemberExpect{Revision: "1"}},
					{ID: "m1", Expect: &ntable.MemberExpect{Revision: "1"}},
				},
			},
		},
		{
			name: "reserved field revision",
			manifest: ntable.BatchManifest{
				Schema:                1,
				Table:                 "demo",
				Epoch:                 "0",
				ExpectedTableRevision: curRev,
				OperationID:           "op-ref-res-rev",
				Actor:                 "test",
				Members: []ntable.BatchMemberEntry{
					{ID: "m1", Expect: &ntable.MemberExpect{Revision: "1"}, Set: map[string]string{"revision": "100"}},
				},
			},
		},
		{
			name: "reserved field place prefix",
			manifest: ntable.BatchManifest{
				Schema:                1,
				Table:                 "demo",
				Epoch:                 "0",
				ExpectedTableRevision: curRev,
				OperationID:           "op-ref-res-place",
				Actor:                 "test",
				Members: []ntable.BatchMemberEntry{
					{ID: "m1", Expect: &ntable.MemberExpect{Revision: "1"}, Set: map[string]string{"place:demo": "fake"}},
				},
			},
		},
		{
			name: "mutation combine create and move",
			manifest: ntable.BatchManifest{
				Schema:                1,
				Table:                 "demo",
				Epoch:                 "0",
				ExpectedTableRevision: curRev,
				OperationID:           "op-ref-create-move",
				Actor:                 "test",
				Members: []ntable.BatchMemberEntry{
					{
						ID:     "m_new",
						Expect: &ntable.MemberExpect{Absent: true},
						Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 5},
						Move:   &ntable.MemberMoveOp{Row: "build", Col: "done"},
					},
				},
			},
		},
		{
			name: "mutation combine move and remove",
			manifest: ntable.BatchManifest{
				Schema:                1,
				Table:                 "demo",
				Epoch:                 "0",
				ExpectedTableRevision: curRev,
				OperationID:           "op-ref-move-rem",
				Actor:                 "test",
				Members: []ntable.BatchMemberEntry{
					{
						ID:     "m1",
						Expect: &ntable.MemberExpect{Revision: "1"},
						Move:   &ntable.MemberMoveOp{Row: "build", Col: "done"},
						Remove: true,
					},
				},
			},
		},
		{
			name: "mutation create expecting existing record",
			manifest: ntable.BatchManifest{
				Schema:                1,
				Table:                 "demo",
				Epoch:                 "0",
				ExpectedTableRevision: curRev,
				OperationID:           "op-ref-create-existing",
				Actor:                 "test",
				Members: []ntable.BatchMemberEntry{
					{
						ID:     "m_new",
						Expect: &ntable.MemberExpect{Revision: "1"},
						Create: &ntable.MemberCreateOp{Row: "build", Col: "ready", Score: 5},
					},
				},
			},
		},
		{
			name: "set and unset overlap",
			manifest: ntable.BatchManifest{
				Schema:                1,
				Table:                 "demo",
				Epoch:                 "0",
				ExpectedTableRevision: curRev,
				OperationID:           "op-ref-set-unset",
				Actor:                 "test",
				Members: []ntable.BatchMemberEntry{
					{
						ID:     "m1",
						Expect: &ntable.MemberExpect{Revision: "1"},
						Set:    map[string]string{"env": "staging"},
						Unset:  []string{"env"},
					},
				},
			},
		},
		{
			name: "late invalid entry",
			manifest: ntable.BatchManifest{
				Schema:                1,
				Table:                 "demo",
				Epoch:                 "0",
				ExpectedTableRevision: curRev,
				OperationID:           "op-ref-late-invalid",
				Actor:                 "test",
				Members: []ntable.BatchMemberEntry{
					// Valid first entry
					{
						ID:     "m1",
						Expect: &ntable.MemberExpect{Revision: "1", Fields: map[string]ntable.FieldGuard{"role": {Equals: &valCorrect}}},
						Set:    map[string]string{"role": "architect"},
					},
					// Invalid second entry: unplaced member remove
					{
						ID:     "m_missing",
						Expect: &ntable.MemberExpect{Absent: false},
						Remove: true,
					},
				},
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			before := storeImage(t, c)
			_, err := ntable.ApplyBatch(ctx, c, tc.manifest)
			after := storeImage(t, c)
			if err == nil {
				t.Fatalf("%s: expected ApplyBatch to refuse, got nil", tc.name)
			}
			if !strings.Contains(err.Error(), "changed=no") {
				t.Fatalf("%s: expected changed=no, got: %v", tc.name, err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("%s: store was modified despite refusal: before != after", tc.name)
			}
		})
	}
}

func TestBatchAcceptedInteractingCrossRowMultiMemberWitness(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()

	cols, err := ntable.ParseColumns("col1,col2")
	if err != nil {
		t.Fatal(err)
	}
	tb := ntable.Table{Name: "demo", Columns: cols}
	if err := ntable.Create(ctx, c, tb, now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "row1", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "row2", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}

	revInit := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()

	// Prestate: member m1 placed in row1:col1 with score 10; member m2 placed in row2:col2 with score 20.
	preManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: revInit,
		OperationID:           "op-interacting-prestate",
		Actor:                 "setup",
		Members: []ntable.BatchMemberEntry{
			{
				ID:     "m1",
				Expect: &ntable.MemberExpect{Absent: true},
				Create: &ntable.MemberCreateOp{
					Row:   "row1",
					Col:   "col1",
					Score: 10,
				},
				Set: map[string]string{
					"role":  "leader",
					"phase": "pre",
				},
			},
			{
				ID:     "m2",
				Expect: &ntable.MemberExpect{Absent: true},
				Create: &ntable.MemberCreateOp{
					Row:   "row2",
					Col:   "col2",
					Score: 20,
				},
				Set: map[string]string{
					"role":  "follower",
					"phase": "pre",
				},
			},
		},
	}
	rcptPre, err := ntable.ApplyBatch(ctx, c, preManifest)
	if err != nil {
		t.Fatalf("setup prestate batch: %v", err)
	}
	if rcptPre.Outcome != "changed" {
		t.Fatalf("setup prestate outcome = %q, want changed", rcptPre.Outcome)
	}

	// Verify prestate via ReadSet
	rsPre, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1", "m2"})
	if err != nil {
		t.Fatalf("ReadSet prestate: %v", err)
	}
	m1Pre, ok1 := rsPre.Member("m1")
	m2Pre, ok2 := rsPre.Member("m2")
	if !ok1 || !m1Pre.Placed || m1Pre.Row != "row1" || m1Pre.Col != "col1" || m1Pre.Score != 10 || m1Pre.Revision != 1 {
		t.Fatalf("unexpected m1 prestate: %+v", m1Pre)
	}
	if !ok2 || !m2Pre.Placed || m2Pre.Row != "row2" || m2Pre.Col != "col2" || m2Pre.Score != 20 || m2Pre.Revision != 1 {
		t.Fatalf("unexpected m2 prestate: %+v", m2Pre)
	}

	// Batch: In a single atomic batch manifest:
	// - Move m1 to row2:col2 with score 30 and field/place guard evaluating prestate
	// - Move m2 to row1:col1 with score 40 and field/place guard evaluating prestate
	// Both guards evaluate against the common pre-state in a single atomic transaction.
	revCurrent := strconv.FormatUint(rcptPre.After, 10)
	score30 := float64(30)
	score40 := float64(40)
	valLeader := "leader"
	valFollower := "follower"
	valPre := "pre"

	batchManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: revCurrent,
		OperationID:           "op-interacting-cross-row-witness",
		Actor:                 "worker",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m1",
				Expect: &ntable.MemberExpect{
					Revision: "1",
					Place:    &ntable.PlaceExpect{Row: "row1", Col: "col1"},
					Fields: map[string]ntable.FieldGuard{
						"role":  {Equals: &valLeader},
						"phase": {Equals: &valPre},
					},
				},
				Move: &ntable.MemberMoveOp{
					Row:   "row2",
					Col:   "col2",
					Score: &score30,
				},
				Set: map[string]string{
					"phase": "post",
				},
			},
			{
				ID: "m2",
				Expect: &ntable.MemberExpect{
					Revision: "1",
					Place:    &ntable.PlaceExpect{Row: "row2", Col: "col2"},
					Fields: map[string]ntable.FieldGuard{
						"role":  {Equals: &valFollower},
						"phase": {Equals: &valPre},
					},
				},
				Move: &ntable.MemberMoveOp{
					Row:   "row1",
					Col:   "col1",
					Score: &score40,
				},
				Set: map[string]string{
					"phase": "post",
				},
			},
		},
	}

	rcptBatch, err := ntable.ApplyBatch(ctx, c, batchManifest)
	if err != nil {
		t.Fatalf("interacting cross-row ApplyBatch failed: %v", err)
	}

	// Verify acceptance and receipts
	if rcptBatch.Outcome != "changed" {
		t.Fatalf("expected outcome changed, got %q", rcptBatch.Outcome)
	}
	if rcptBatch.BatchDelta == nil {
		t.Fatal("expected non-nil BatchDelta")
	}
	if rcptBatch.BatchDelta.ChangedCount != 2 {
		t.Fatalf("expected changed_count=2, got %d", rcptBatch.BatchDelta.ChangedCount)
	}
	if rcptBatch.BatchDelta.SelectedCount != 2 {
		t.Fatalf("expected selected_count=2, got %d", rcptBatch.BatchDelta.SelectedCount)
	}
	if rcptBatch.BatchDelta.GuardCount != 0 {
		t.Fatalf("expected syntactic guard_count=0 for moves, got %d", rcptBatch.BatchDelta.GuardCount)
	}

	deltaMembers := make(map[string]ntable.BatchMemberDelta)
	for _, md := range rcptBatch.BatchDelta.Members {
		deltaMembers[md.ID] = md
	}
	d1, ok1 := deltaMembers["m1"]
	d2, ok2 := deltaMembers["m2"]
	if !ok1 || !ok2 {
		t.Fatalf("missing member delta: m1=%v, m2=%v", ok1, ok2)
	}
	if d1.BeforePlace != "row1:col1" || d1.AfterPlace != "row2:col2" || d1.BeforeScore == nil || *d1.BeforeScore != 10 || d1.AfterScore == nil || *d1.AfterScore != 30 || d1.BeforeRev != "1" || d1.AfterRev != "2" {
		t.Fatalf("unexpected m1 delta: %+v", d1)
	}
	if d2.BeforePlace != "row2:col2" || d2.AfterPlace != "row1:col1" || d2.BeforeScore == nil || *d2.BeforeScore != 20 || d2.AfterScore == nil || *d2.AfterScore != 40 || d2.BeforeRev != "1" || d2.AfterRev != "2" {
		t.Fatalf("unexpected m2 delta: %+v", d2)
	}

	// Verify full poststate
	rsPost, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1", "m2"})
	if err != nil {
		t.Fatalf("ReadSet poststate: %v", err)
	}
	m1Post, _ := rsPost.Member("m1")
	m2Post, _ := rsPost.Member("m2")
	if !m1Post.Placed || m1Post.Row != "row2" || m1Post.Col != "col2" || m1Post.Score != 30 || m1Post.Revision != 2 || m1Post.Fields["phase"] != "post" {
		t.Fatalf("unexpected m1 poststate: %+v", m1Post)
	}
	if !m2Post.Placed || m2Post.Row != "row1" || m2Post.Col != "col1" || m2Post.Score != 40 || m2Post.Revision != 2 || m2Post.Fields["phase"] != "post" {
		t.Fatalf("unexpected m2 poststate: %+v", m2Post)
	}

	// Verify physical Redis cell zsets
	if sc := c.ZScore(ctx, ntable.CellKey("demo", "row2", "col2"), "m1").Val(); sc != 30 {
		t.Fatalf("expected m1 score 30 in row2:col2, got %v", sc)
	}
	if sc := c.ZScore(ctx, ntable.CellKey("demo", "row2", "col2"), "m2").Val(); sc != 0 {
		t.Fatalf("expected m2 removed from row2:col2, got %v", sc)
	}
	if sc := c.ZScore(ctx, ntable.CellKey("demo", "row1", "col1"), "m2").Val(); sc != 40 {
		t.Fatalf("expected m2 score 40 in row1:col1, got %v", sc)
	}
	if sc := c.ZScore(ctx, ntable.CellKey("demo", "row1", "col1"), "m1").Val(); sc != 0 {
		t.Fatalf("expected m1 removed from row1:col1, got %v", sc)
	}

	// Verify reverse indexes
	if p := c.HGet(ctx, ntable.MemberKey("m1"), "place:demo").Val(); p != "row2:col2" {
		t.Fatalf("expected m1 reverse index row2:col2, got %q", p)
	}
	if p := c.HGet(ctx, ntable.MemberKey("m2"), "place:demo").Val(); p != "row1:col1" {
		t.Fatalf("expected m2 reverse index row1:col1, got %q", p)
	}
}

func TestBatchLateInvalidAtNMaxRefusal(t *testing.T) {
	t.Parallel()
	testBatchLateInvalidAtNMaxRefusal(t)
}

func testBatchLateInvalidAtNMaxRefusal(t *testing.T) {
	c, _ := store(t)
	ctx := context.Background()

	cols, err := ntable.ParseColumns("ready")
	if err != nil {
		t.Fatal(err)
	}
	tb := ntable.Table{Name: "demo", Columns: cols}
	if err := ntable.Create(ctx, c, tb, now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}

	revInit := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()

	// Create 128 members in the table in a single batch (max mutation entries = 128)
	createEntries := make([]ntable.BatchMemberEntry, 128)
	for i := 0; i < 128; i++ {
		createEntries[i] = ntable.BatchMemberEntry{
			ID:     fmt.Sprintf("m_%03d", i),
			Expect: &ntable.MemberExpect{Absent: true},
			Create: &ntable.MemberCreateOp{
				Row:   "build",
				Col:   "ready",
				Score: float64(i + 1),
			},
			Set: map[string]string{
				"idx": strconv.Itoa(i),
			},
		}
	}
	createManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: revInit,
		OperationID:           "op-create-128-max",
		Actor:                 "setup",
		Members:               createEntries,
	}
	rcptCreate, err := ntable.ApplyBatch(ctx, c, createManifest)
	if err != nil {
		t.Fatalf("create 128 members batch: %v", err)
	}
	if rcptCreate.Outcome != "changed" || rcptCreate.BatchDelta.ChangedCount != 128 {
		t.Fatalf("expected 128 changed, got outcome=%q delta=%+v", rcptCreate.Outcome, rcptCreate.BatchDelta)
	}

	curRev := strconv.FormatUint(rcptCreate.After, 10)

	// Subtest A: Stale revision on the 128th entry
	t.Run("stale revision on 128th entry", func(t *testing.T) {
		entries := make([]ntable.BatchMemberEntry, 128)
		for i := 0; i < 127; i++ {
			entries[i] = ntable.BatchMemberEntry{
				ID:     fmt.Sprintf("m_%03d", i),
				Expect: &ntable.MemberExpect{Revision: "1"},
				Set:    map[string]string{"processed": "true"},
			}
		}
		// 128th entry is invalid (stale member revision: expected 999, actual is 1)
		entries[127] = ntable.BatchMemberEntry{
			ID:     "m_127",
			Expect: &ntable.MemberExpect{Revision: "999"},
			Set:    map[string]string{"processed": "true"},
		}

		manifest := ntable.BatchManifest{
			Schema:                1,
			Table:                 "demo",
			Epoch:                 "0",
			ExpectedTableRevision: curRev,
			OperationID:           "op-late-invalid-128-rev",
			Actor:                 "tester",
			Members:               entries,
		}

		before, err := dumpStore(ctx, c)
		if err != nil {
			t.Fatalf("dumpStore before: %v", err)
		}
		_, err = ntable.ApplyBatch(ctx, c, manifest)
		after, err2 := dumpStore(ctx, c)
		if err2 != nil {
			t.Fatalf("dumpStore after: %v", err2)
		}

		if err == nil {
			t.Fatal("expected ApplyBatch to fail on 128th invalid entry")
		}
		if !errors.Is(err, ntable.ErrMemberRevision) {
			t.Fatalf("expected ErrMemberRevision, got: %v", err)
		}
		if !strings.Contains(err.Error(), "changed=no") {
			t.Fatalf("expected changed=no, got: %v", err)
		}
		if diff := diffSnapshots(before, after); diff != "" {
			t.Fatalf("store modified despite refusal: %s", diff)
		}
	})

	// Subtest B: Invalid remove on unplaced member as 128th entry
	t.Run("invalid remove on 128th entry", func(t *testing.T) {
		entries := make([]ntable.BatchMemberEntry, 128)
		for i := 0; i < 127; i++ {
			entries[i] = ntable.BatchMemberEntry{
				ID:     fmt.Sprintf("m_%03d", i),
				Expect: &ntable.MemberExpect{Revision: "1"},
				Set:    map[string]string{"step": "2"},
			}
		}
		// 128th entry is invalid remove on unplaced member
		entries[127] = ntable.BatchMemberEntry{
			ID:     "m_nonexistent",
			Expect: &ntable.MemberExpect{Absent: false},
			Remove: true,
		}

		manifest := ntable.BatchManifest{
			Schema:                1,
			Table:                 "demo",
			Epoch:                 "0",
			ExpectedTableRevision: curRev,
			OperationID:           "op-late-invalid-128-rem",
			Actor:                 "tester",
			Members:               entries,
		}

		before, err := dumpStore(ctx, c)
		if err != nil {
			t.Fatalf("dumpStore before: %v", err)
		}
		_, err = ntable.ApplyBatch(ctx, c, manifest)
		after, err2 := dumpStore(ctx, c)
		if err2 != nil {
			t.Fatalf("dumpStore after: %v", err2)
		}

		if err == nil {
			t.Fatal("expected ApplyBatch to fail on 128th invalid remove entry")
		}
		if !errors.Is(err, ntable.ErrNotMember) {
			t.Fatalf("expected ErrNotMember, got: %v", err)
		}
		if !strings.Contains(err.Error(), "changed=no") {
			t.Fatalf("expected changed=no, got: %v", err)
		}
		if diff := diffSnapshots(before, after); diff != "" {
			t.Fatalf("store modified despite refusal: %s", diff)
		}
	})
}

func TestBatchLateInvalidAtNMaxWitness(t *testing.T) {
	t.Parallel()
	testBatchLateInvalidAtNMaxRefusal(t)
}

func TestBatchRetainedUnplacedMemberRemovalWitness(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()

	cols, err := ntable.ParseColumns("ready")
	if err != nil {
		t.Fatal(err)
	}
	tb := ntable.Table{Name: "demo", Columns: cols}
	if err := ntable.Create(ctx, c, tb, now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}

	revInit := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()

	// 1. Create and place member m_unplaced
	createManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: revInit,
		OperationID:           "op-create-unplaced-witness",
		Actor:                 "test",
		Members: []ntable.BatchMemberEntry{
			{
				ID:     "m_unplaced",
				Expect: &ntable.MemberExpect{Absent: true},
				Create: &ntable.MemberCreateOp{
					Row:   "build",
					Col:   "ready",
					Score: 10,
				},
				Set: map[string]string{
					"role":   "tester",
					"custom": "payload",
				},
			},
		},
	}
	rcptCreate, err := ntable.ApplyBatch(ctx, c, createManifest)
	if err != nil {
		t.Fatalf("create m_unplaced: %v", err)
	}
	if rcptCreate.Outcome != "changed" {
		t.Fatalf("expected outcome changed, got %q", rcptCreate.Outcome)
	}

	// 2. Remove m_unplaced by a prior batch write so it is retained as an unplaced record
	revAfterCreate := strconv.FormatUint(rcptCreate.After, 10)
	removeManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: revAfterCreate,
		OperationID:           "op-prior-remove-witness",
		Actor:                 "test",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m_unplaced",
				Expect: &ntable.MemberExpect{
					Revision: "1",
					Place:    &ntable.PlaceExpect{Row: "build", Col: "ready"},
				},
				Remove: true,
			},
		},
	}

	rcptRemove, err := ntable.ApplyBatch(ctx, c, removeManifest)
	if err != nil {
		t.Fatalf("prior remove m_unplaced: %v", err)
	}

	// Verify acceptance and receipt deltas for the removal
	if rcptRemove.Outcome != "changed" {
		t.Fatalf("expected outcome changed on remove, got %q", rcptRemove.Outcome)
	}
	if rcptRemove.BatchDelta == nil || rcptRemove.BatchDelta.ChangedCount != 1 {
		t.Fatalf("unexpected remove delta: %+v", rcptRemove.BatchDelta)
	}
	if len(rcptRemove.BatchDelta.Members) != 1 {
		t.Fatalf("expected 1 member delta, got %d", len(rcptRemove.BatchDelta.Members))
	}
	remDelta := rcptRemove.BatchDelta.Members[0]
	if remDelta.ID != "m_unplaced" || remDelta.BeforePlace != "build:ready" || remDelta.AfterPlace != "" {
		t.Fatalf("unexpected remDelta places: %+v", remDelta)
	}
	if remDelta.BeforeScore == nil || *remDelta.BeforeScore != 10 || remDelta.AfterScore != nil {
		t.Fatalf("unexpected remDelta scores: %+v", remDelta)
	}
	if remDelta.BeforeRev != "1" || remDelta.AfterRev != "2" {
		t.Fatalf("unexpected remDelta revs: %+v", remDelta)
	}

	// Verify full store state: member record is retained, application fields and revision are preserved,
	// but placement is cleared and cell zset no longer contains the member.
	if sc := c.ZScore(ctx, ntable.CellKey("demo", "build", "ready"), "m_unplaced").Val(); sc != 0 {
		t.Fatalf("expected m_unplaced removed from zset, got score %v", sc)
	}
	if p := c.HGet(ctx, ntable.MemberKey("m_unplaced"), "place:demo").Val(); p != "" {
		t.Fatalf("expected place:demo to be cleared, got %q", p)
	}
	if r := c.HGet(ctx, ntable.MemberKey("m_unplaced"), "revision").Val(); r != "2" {
		t.Fatalf("expected member revision 2, got %q", r)
	}
	if role := c.HGet(ctx, ntable.MemberKey("m_unplaced"), "role").Val(); role != "tester" {
		t.Fatalf("expected retained field role=tester, got %q", role)
	}
	if custom := c.HGet(ctx, ntable.MemberKey("m_unplaced"), "custom").Val(); custom != "payload" {
		t.Fatalf("expected retained field custom=payload, got %q", custom)
	}

	rsUnplaced, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m_unplaced"})
	if err != nil {
		t.Fatalf("ReadSet unplaced: %v", err)
	}
	mUnplaced, ok := rsUnplaced.Member("m_unplaced")
	if !ok || mUnplaced.Placed || mUnplaced.Revision != 2 || mUnplaced.Fields["role"] != "tester" {
		t.Fatalf("ReadSet unplaced record mismatch: %+v", mUnplaced)
	}

	// 3. Now, a batch attempts remove: true on this retained already-unplaced member.
	// Contract: Removal requires existing owned placement; an already-unplaced member refuses
	// with NOTMEMBER before writes. The store must remain completely unchanged.
	revAfterRemove := strconv.FormatUint(rcptRemove.After, 10)
	reRemoveManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: revAfterRemove,
		OperationID:           "op-re-remove-unplaced-refusal",
		Actor:                 "test",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m_unplaced",
				Expect: &ntable.MemberExpect{
					Revision: "2",
				},
				Remove: true,
			},
		},
	}

	beforeDump, err := dumpStore(ctx, c)
	if err != nil {
		t.Fatalf("dumpStore before: %v", err)
	}
	_, err = ntable.ApplyBatch(ctx, c, reRemoveManifest)
	afterDump, err2 := dumpStore(ctx, c)
	if err2 != nil {
		t.Fatalf("dumpStore after: %v", err2)
	}

	if err == nil {
		t.Fatal("expected ApplyBatch to refuse remove:true on already-unplaced member")
	}
	if !errors.Is(err, ntable.ErrNotMember) {
		t.Fatalf("expected ErrNotMember, got: %v", err)
	}
	if !strings.Contains(err.Error(), "changed=no") {
		t.Fatalf("expected changed=no, got: %v", err)
	}
	if diff := diffSnapshots(beforeDump, afterDump); diff != "" {
		t.Fatalf("store modified on unplaced remove refusal: %s", diff)
	}
}

func TestBatchNoopGuardCountAndCardinalityWitness(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()

	cols, err := ntable.ParseColumns("ready")
	if err != nil {
		t.Fatal(err)
	}
	tb := ntable.Table{Name: "demo", Columns: cols}
	if err := ntable.Create(ctx, c, tb, now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}

	revInit := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()

	// Initial create of m1 at build:ready with score 10
	createManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: revInit,
		OperationID:           "op-create-m1-noop-witness",
		Actor:                 "test",
		Members: []ntable.BatchMemberEntry{
			{
				ID:     "m1",
				Expect: &ntable.MemberExpect{Absent: true},
				Create: &ntable.MemberCreateOp{
					Row:   "build",
					Col:   "ready",
					Score: 10,
				},
				Set: map[string]string{"env": "prod"},
			},
		},
	}
	rcptCreate, err := ntable.ApplyBatch(ctx, c, createManifest)
	if err != nil {
		t.Fatalf("create m1: %v", err)
	}
	if rcptCreate.Outcome != "changed" {
		t.Fatalf("expected outcome changed, got %q", rcptCreate.Outcome)
	}

	// Capture state before no-op move
	revBefore := strconv.FormatUint(rcptCreate.After, 10)
	changesKey := ntable.ChangesKey("demo")
	streamLenBefore, err := c.XLen(ctx, changesKey).Result()
	if err != nil {
		t.Fatalf("XLen before: %v", err)
	}

	// Apply a move of member m1 to the same cell and same score with no field changes (a no-op move)
	score10 := float64(10)
	noopManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: revBefore,
		OperationID:           "op-move-noop-witness",
		Actor:                 "test",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m1",
				Expect: &ntable.MemberExpect{
					Revision: "1",
					Place:    &ntable.PlaceExpect{Row: "build", Col: "ready"},
				},
				Move: &ntable.MemberMoveOp{
					Row:   "build",
					Col:   "ready",
					Score: &score10,
				},
			},
		},
	}

	rcptNoop, err := ntable.ApplyBatch(ctx, c, noopManifest)
	if err != nil {
		t.Fatalf("apply noop move: %v", err)
	}

	// Assert outcome is noop
	if rcptNoop.Outcome != "noop" {
		t.Fatalf("expected outcome noop, got %q", rcptNoop.Outcome)
	}
	if rcptNoop.BatchDelta == nil {
		t.Fatal("expected non-nil BatchDelta")
	}

	// Assert rcpt.BatchDelta.GuardCount == 0 (syntactic guard_count is 0, since move is mutation syntax)
	if rcptNoop.BatchDelta.GuardCount != 0 {
		t.Fatalf("expected syntactic GuardCount == 0 for move syntax, got %d", rcptNoop.BatchDelta.GuardCount)
	}
	if rcptNoop.BatchDelta.ChangedCount != 0 {
		t.Fatalf("expected ChangedCount == 0 for no-op move, got %d", rcptNoop.BatchDelta.ChangedCount)
	}
	if rcptNoop.BatchDelta.SelectedCount != 1 {
		t.Fatalf("expected SelectedCount == 1, got %d", rcptNoop.BatchDelta.SelectedCount)
	}

	// Assert member revision is unchanged
	rsAfter, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1"})
	if err != nil {
		t.Fatalf("ReadSet after: %v", err)
	}
	m1After, ok := rsAfter.Member("m1")
	if !ok || m1After.Revision != 1 {
		t.Fatalf("expected m1 revision unchanged at 1, got %+v", m1After)
	}
	if r := c.HGet(ctx, ntable.MemberKey("m1"), "revision").Val(); r != "1" {
		t.Fatalf("expected m1 hash revision 1, got %q", r)
	}

	// Assert table revision +1
	if rcptNoop.After != rcptCreate.After+1 {
		t.Fatalf("expected table revision to increment from %d to %d, got %d", rcptCreate.After, rcptCreate.After+1, rcptNoop.After)
	}
	tableRevCurrent := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	if tableRevCurrent != strconv.FormatUint(rcptCreate.After+1, 10) {
		t.Fatalf("expected table revision %d, got %s", rcptCreate.After+1, tableRevCurrent)
	}

	// Assert stream cardinality is exactly 1 event
	streamLenAfter, err := c.XLen(ctx, changesKey).Result()
	if err != nil {
		t.Fatalf("XLen after: %v", err)
	}
	if streamLenAfter != streamLenBefore+1 {
		t.Fatalf("expected stream cardinality to increase by 1 (from %d to %d), got %d", streamLenBefore, streamLenBefore+1, streamLenAfter)
	}
	events, err := c.XRange(ctx, changesKey, rcptNoop.ID, rcptNoop.ID).Result()
	if err != nil || len(events) != 1 {
		t.Fatalf("expected exactly 1 stream event for rcptNoop.ID %s, got %d (err: %v)", rcptNoop.ID, len(events), err)
	}
	if events[0].Values["outcome"] != "noop" {
		t.Fatalf("expected stream event outcome noop, got %v", events[0].Values["outcome"])
	}

	// Assert operation-record cardinality is exactly 1 record
	opKey := ntable.DefKey("demo") + ":op:op-move-noop-witness"
	opRecord, err := c.HGetAll(ctx, opKey).Result()
	if err != nil {
		t.Fatalf("HGetAll opRecord: %v", err)
	}
	if len(opRecord) == 0 {
		t.Fatalf("expected operation record at %s, got none", opKey)
	}
	if opRecord["outcome"] != "noop" {
		t.Fatalf("expected opRecord outcome noop, got %q", opRecord["outcome"])
	}
	if opRecord["stream_id"] != rcptNoop.ID {
		t.Fatalf("expected opRecord stream_id %s, got %s", rcptNoop.ID, opRecord["stream_id"])
	}
}

func TestBatchAcceptedOmittedRevisionAfterWriterAdvanceWitness(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()

	cols, err := ntable.ParseColumns("ready,working")
	if err != nil {
		t.Fatal(err)
	}
	tb := ntable.Table{Name: "demo", Columns: cols}
	if err := ntable.Create(ctx, c, tb, now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}

	revInit := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()

	// Initial creation: member m1 placed with status "step0", revision 1
	createManifest := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: revInit,
		OperationID:           "op-init-m1-omitted-witness",
		Actor:                 "creator",
		Members: []ntable.BatchMemberEntry{
			{
				ID:     "m1",
				Expect: &ntable.MemberExpect{Absent: true},
				Create: &ntable.MemberCreateOp{
					Row:   "build",
					Col:   "ready",
					Score: 10,
				},
				Set: map[string]string{
					"status": "step0",
					"tag":    "initial",
				},
			},
		},
	}
	rcptInit, err := ntable.ApplyBatch(ctx, c, createManifest)
	if err != nil {
		t.Fatalf("create m1: %v", err)
	}
	if rcptInit.Outcome != "changed" {
		t.Fatalf("expected outcome changed, got %q", rcptInit.Outcome)
	}

	// Writer 1 advances member m1's revision (via field update / move)
	revAfterInit := strconv.FormatUint(rcptInit.After, 10)
	manifestWriter1 := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: revAfterInit,
		OperationID:           "op-writer1-advance-rev",
		Actor:                 "writer1",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m1",
				Expect: &ntable.MemberExpect{
					Revision: "1",
					Place:    &ntable.PlaceExpect{Row: "build", Col: "ready"},
				},
				Move: &ntable.MemberMoveOp{Row: "build", Col: "working"},
				Set: map[string]string{
					"status": "step1",
				},
			},
		},
	}
	rcptW1, err := ntable.ApplyBatch(ctx, c, manifestWriter1)
	if err != nil {
		t.Fatalf("writer1 apply batch: %v", err)
	}
	if rcptW1.Outcome != "changed" {
		t.Fatalf("writer1 expected outcome changed, got %q", rcptW1.Outcome)
	}

	// Verify member m1's revision advanced to 2
	rsW1, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1"})
	if err != nil {
		t.Fatalf("ReadSet after writer1: %v", err)
	}
	m1W1, ok := rsW1.Member("m1")
	if !ok || m1W1.Revision != 2 || m1W1.Fields["status"] != "step1" || m1W1.Col != "working" {
		t.Fatalf("expected m1 at revision 2, col=working, got %+v", m1W1)
	}

	// Writer 2 submits a batch modifying m1 with Revision omitted (empty string)
	// and only explicit field guards.
	revAfterW1 := strconv.FormatUint(rcptW1.After, 10)
	valStep1 := "step1"
	manifestWriter2 := ntable.BatchManifest{
		Schema:                1,
		Table:                 "demo",
		Epoch:                 "0",
		ExpectedTableRevision: revAfterW1,
		OperationID:           "op-writer2-omitted-rev-witness",
		Actor:                 "writer2",
		Members: []ntable.BatchMemberEntry{
			{
				ID: "m1",
				Expect: &ntable.MemberExpect{
					// Revision omitted!
					Fields: map[string]ntable.FieldGuard{
						"status": {Equals: &valStep1},
					},
				},
				Set: map[string]string{
					"status":  "step2",
					"writer2": "applied",
				},
			},
		},
	}

	rcptW2, err := ntable.ApplyBatch(ctx, c, manifestWriter2)
	if err != nil {
		t.Fatalf("writer2 apply batch with omitted revision: %v", err)
	}
	if rcptW2.Outcome != "changed" {
		t.Fatalf("writer2 expected outcome changed, got %q", rcptW2.Outcome)
	}

	// Verify batch succeeded and m1's revision advanced to 3
	rsW2, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1"})
	if err != nil {
		t.Fatalf("ReadSet after writer2: %v", err)
	}
	m1W2, ok := rsW2.Member("m1")
	if !ok || m1W2.Revision != 3 || m1W2.Fields["status"] != "step2" || m1W2.Fields["writer2"] != "applied" {
		t.Fatalf("expected m1 at revision 3 after writer2, got %+v", m1W2)
	}

	// Writer 3 submits a batch modifying m1 with raw JSON expect without revision
	// confirming omitted revision in wire format is accepted after revision advance.
	revAfterW2 := strconv.FormatUint(rcptW2.After, 10)
	rawWriter3 := fmt.Sprintf(`{"schema":1,"table":"demo","epoch":"0","expected_table_revision":%q,"operation_id":"op-w3-wire-omitted","actor":"writer3","members":[{"id":"m1","expect":{"fields":{"status":{"equals":"step2"}}},"set":{"status":"step3","wire":"accepted"}}]}`, revAfterW2)
	ans, err := c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey("demo")}, "demo", rawWriter3).Slice()
	if err != nil {
		t.Fatalf("writer3 raw apply: %v", err)
	}
	if len(ans) < 2 || ans[0] != "OK" {
		t.Fatalf("writer3 expected OK, got %v", ans)
	}

	// Verify m1 revision advanced to 4 and status is step3
	rsW3, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1"})
	if err != nil {
		t.Fatalf("ReadSet after writer3: %v", err)
	}
	m1W3, ok := rsW3.Member("m1")
	if !ok || m1W3.Revision != 4 || m1W3.Fields["status"] != "step3" || m1W3.Fields["wire"] != "accepted" {
		t.Fatalf("expected m1 at revision 4 after writer3, got %+v", m1W3)
	}
}
