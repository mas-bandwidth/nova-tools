//go:build functional

package ntable_test

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func boolPtr(b bool) *bool        { return &b }
func floatPtr(f float64) *float64 { return &f }

func TestBatchApplyAndReadSetContract(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()

	tb := demo()
	require.NoError(t, ntable.Create(ctx, c, tb, now))
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "test", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}

	// 1. Initial ReadSet on empty table
	rs, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1", "m2"})
	require.NoError(t, err, "initial ReadSetMembers")
	if rs.Table != "demo" || len(rs.Members) != 0 || len(rs.Missing) != 2 {
		t.Fatalf("unexpected initial ReadSet: %+v", rs)
	}
	require.True(t, rs.IsMissing("m1"), "expected m1 and m2 to be missing: %+v", rs)
	require.True(t, rs.IsMissing("m2"), "expected m1 and m2 to be missing: %+v", rs)

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
	require.NoError(t, err, "ApplyBatch create")
	require.Equal(t, "changed", rcpt1.Outcome, "expected outcome changed, got %q", rcpt1.Outcome)
	if rcpt1.BatchDelta == nil || rcpt1.BatchDelta.ChangedCount != 2 || rcpt1.BatchDelta.GuardCount != 0 {
		t.Fatalf("unexpected delta: %+v", rcpt1.BatchDelta)
	}

	// 3. Verify ReadSet sees m1 and m2 placed with revision 1
	rs2, err := ntable.ReadSet(ctx, c, "demo", ntable.ReadSetScope{
		Members: []string{"m1", "m2", "m3"},
	})
	require.NoError(t, err, "ReadSet after create")
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
	require.NoError(t, err, "ReadSet by selection")
	require.Len(t, rsSel.Members, 2, "selection expected 2 members, got %d", len(rsSel.Members))

	// 4. Idempotent replay of exact same manifest
	rcptReplay, err := ntable.ApplyBatch(ctx, c, manifest1)
	require.NoError(t, err, "idempotent replay failed")
	require.Equal(t, rcpt1.ID, rcptReplay.ID, "replay receipt mismatch: got %+v, want %+v", rcptReplay, rcpt1)
	require.Equal(t, rcpt1.After, rcptReplay.After, "replay receipt mismatch: got %+v, want %+v", rcptReplay, rcpt1)

	// 5. Conflicting operation ID replay
	conflictingManifest := manifest1
	conflictingManifest.Actor = "different-actor"
	_, err = ntable.ApplyBatch(ctx, c, conflictingManifest)
	require.ErrorIs(t, err, ntable.ErrOpConflict, "expected ErrOpConflict with changed=no, got: %v", err)
	require.ErrorContains(t, err, "changed=no", "expected ErrOpConflict with changed=no, got: %v", err)

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
	require.ErrorIs(t, err, ntable.ErrRevisionMismatch, "expected ErrRevisionMismatch with changed=no, got: %v", err)
	require.ErrorContains(t, err, "changed=no", "expected ErrRevisionMismatch with changed=no, got: %v", err)

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
	require.ErrorIs(t, err, ntable.ErrMemberRevision, "expected ErrMemberRevision with changed=no, got: %v", err)
	require.ErrorContains(t, err, "changed=no", "expected ErrMemberRevision with changed=no, got: %v", err)

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
	require.ErrorIs(t, err, ntable.ErrFieldGuard, "expected ErrFieldGuard with changed=no, got: %v", err)
	require.ErrorContains(t, err, "changed=no", "expected ErrFieldGuard with changed=no, got: %v", err)

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
	require.ErrorIs(t, err, ntable.ErrFieldGuard, "expected ErrFieldGuard absent with changed=no, got: %v", err)
	require.ErrorContains(t, err, "changed=no", "expected ErrFieldGuard absent with changed=no, got: %v", err)

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
	require.ErrorIs(t, err, ntable.ErrFieldGuard, "expected ErrFieldGuard one_of with changed=no, got: %v", err)
	require.ErrorContains(t, err, "changed=no", "expected ErrFieldGuard one_of with changed=no, got: %v", err)

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
	require.NoError(t, err, "ApplyBatch move")
	require.Equal(t, "changed", rcpt2.Outcome, "expected outcome changed, got %q", rcpt2.Outcome)
	require.Equal(t, 1, rcpt2.BatchDelta.ChangedCount, "unexpected counts: changed=%d guard=%d", rcpt2.BatchDelta.ChangedCount, rcpt2.BatchDelta.GuardCount)
	require.Equal(t, 1, rcpt2.BatchDelta.GuardCount, "unexpected counts: changed=%d guard=%d", rcpt2.BatchDelta.ChangedCount, rcpt2.BatchDelta.GuardCount)

	// Verify post-state via ReadSet
	rs3, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1", "m2"})
	require.NoError(t, err, "ReadSet after move")
	m1After, _ := rs3.Member("m1")
	if m1After.Revision != 2 || m1After.Row != "build" || m1After.Col != "working" || m1After.Score != 15 {
		t.Fatalf("unexpected m1 after move: %+v", m1After)
	}
	require.Equal(t, "in-progress", m1After.Fields["status"], "unexpected m1 fields: %+v", m1After.Fields)
	require.Empty(t, m1After.Fields["definition"], "unexpected m1 fields: %+v", m1After.Fields)
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
	require.ErrorIs(t, err, ntable.ErrReservedField, "expected ErrReservedField with changed=no, got: %v", err)
	require.ErrorContains(t, err, "changed=no", "expected ErrReservedField with changed=no, got: %v", err)

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
	require.ErrorIs(t, err, ntable.ErrMutation, "expected ErrMutation with changed=no, got: %v", err)
	require.ErrorContains(t, err, "changed=no", "expected ErrMutation with changed=no, got: %v", err)

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
	require.NoError(t, err, "ApplyBatch remove")
	require.Equal(t, "changed", rcptRemove.Outcome, "expected outcome changed, got %q", rcptRemove.Outcome)

	// ReadSet verifies m2 is unplaced
	rs4, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m2"})
	require.NoError(t, err, "ReadSet after remove")
	m2Removed, _ := rs4.Member("m2")
	require.False(t, m2Removed.Placed, "expected m2 to be unplaced: %+v", m2Removed)
}

func TestBatchReceiptReplayExhaustive(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()

	tb := demo()
	require.NoError(t, ntable.Create(ctx, c, tb, now))
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
	require.NoError(t, err, "marshal manifest1")
	expectedDigest1 := fmt.Sprintf("%x", sha1.Sum(raw1))

	rcpt1, err := ntable.ApplyBatch(ctx, c, manifest1)
	require.NoError(t, err, "ApplyBatch manifest1")
	require.Equal(t, "changed", rcpt1.Outcome, "expected outcome changed, got %q", rcpt1.Outcome)
	if rcpt1.BatchDelta == nil || rcpt1.BatchDelta.ChangedCount != 2 || rcpt1.BatchDelta.GuardCount != 0 {
		t.Fatalf("unexpected delta in rcpt1: %+v", rcpt1.BatchDelta)
	}

	// 4. Verify the stored operation record fields
	opRecord1 := operationRecord(t, c, "demo", "0", "op-create-m1-m2")
	assert.Equal(t, "op-create-m1-m2", opRecord1["operation_id"], "opRecord1 operation_id = %q, want %q", opRecord1["operation_id"], "op-create-m1-m2")
	assert.Equal(t, expectedDigest1, opRecord1["digest"], "opRecord1 digest = %q, want %q", opRecord1["digest"], expectedDigest1)
	assert.Equal(t, string(raw1), opRecord1["request"], "opRecord1 request mismatch: got %q, want %q", opRecord1["request"], string(raw1))
	assert.Equal(t, rcpt1.ID, opRecord1["stream_id"], "opRecord1 stream_id = %q, want %q", opRecord1["stream_id"], rcpt1.ID)
	assert.Equal(t, "0", opRecord1["epoch"], "opRecord1 epoch = %q, want 0", opRecord1["epoch"])
	assert.Equal(t, strconv.FormatUint(rcpt1.Before, 10), opRecord1["rev_before"], "opRecord1 rev_before = %q, want %d", opRecord1["rev_before"], rcpt1.Before)
	assert.Equal(t, strconv.FormatUint(rcpt1.After, 10), opRecord1["rev_after"], "opRecord1 rev_after = %q, want %d", opRecord1["rev_after"], rcpt1.After)
	assert.Equal(t, "changed", opRecord1["outcome"], "opRecord1 outcome = %q, want changed", opRecord1["outcome"])
	assert.NotEmpty(t, opRecord1["result"], "opRecord1 result is empty")

	// 5. Verify stream event batch_delta field on table:demo:changes
	changesKey := ntable.ChangesKey("demo")
	events, err := c.XRange(ctx, changesKey, rcpt1.ID, rcpt1.ID).Result()
	require.NoError(t, err, "XRange for rcpt1.ID: %v, count=%d", err, len(events))
	require.Len(t, events, 1, "XRange for rcpt1.ID: %v, count=%d", err, len(events))
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
	require.NoError(t, json.Unmarshal([]byte(fmt.Sprint(ev1["batch_delta"])), &streamDelta1), "unmarshal stream batch_delta")
	if streamDelta1.OperationID != "op-create-m1-m2" || streamDelta1.ChangedCount != 2 || streamDelta1.GuardCount != 0 {
		t.Errorf("streamDelta1 mismatch: %+v", streamDelta1)
	}
	require.Len(t, streamDelta1.Members, 2, "streamDelta1 members len = %d, want 2", len(streamDelta1.Members))

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
	require.NoError(t, err, "ApplyBatch manifestNoop")
	require.Equal(t, "noop", rcptNoop.Outcome, "expected outcome noop, got %q", rcptNoop.Outcome)
	if rcptNoop.BatchDelta == nil || rcptNoop.BatchDelta.GuardCount != 2 || rcptNoop.BatchDelta.ChangedCount != 0 {
		t.Fatalf("expected guard_count=2, changed_count=0: %+v", rcptNoop.BatchDelta)
	}

	// Verify no-op op record outcome
	opRecordNoop := operationRecord(t, c, "demo", "0", "op-noop-guards")
	assert.Equal(t, "noop", opRecordNoop["outcome"], "opRecordNoop outcome = %q, want noop", opRecordNoop["outcome"])

	// Verify no-op stream event
	eventsNoop, err := c.XRange(ctx, changesKey, rcptNoop.ID, rcptNoop.ID).Result()
	require.NoError(t, err, "XRange for rcptNoop.ID: %v, count=%d", err, len(eventsNoop))
	require.Len(t, eventsNoop, 1, "XRange for rcptNoop.ID: %v, count=%d", err, len(eventsNoop))
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
	require.NoError(t, err, "ApplyBatch manifestMove")
	require.Equal(t, "changed", rcptMove.Outcome, "expected rcptMove outcome changed, got %q", rcptMove.Outcome)

	// Table revision has advanced well past rcpt1 and rcptNoop!
	tableRevAdvanced := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	streamLenBeforeReplays, err := c.XLen(ctx, changesKey).Result()
	require.NoError(t, err, "XLen")

	// 1. Replay applied batch 1 after table revision has advanced
	// (manifest1.ExpectedTableRevision is stale, but replay succeeds unconditionally!)
	rcpt1Replay, err := ntable.ApplyBatch(ctx, c, manifest1)
	require.NoError(t, err, "replay manifest1 failed")
	if rcpt1Replay.ID != rcpt1.ID || rcpt1Replay.Before != rcpt1.Before || rcpt1Replay.After != rcpt1.After || rcpt1Replay.Outcome != rcpt1.Outcome {
		t.Fatalf("rcpt1Replay mismatch: got %+v, want %+v", rcpt1Replay, rcpt1)
	}
	require.Equal(t, rcpt1.BatchDelta, rcpt1Replay.BatchDelta, "rcpt1Replay delta mismatch: got %+v, want %+v", rcpt1Replay.BatchDelta, rcpt1.BatchDelta)

	// Assert zero new stream entries and table revision did not bump
	streamLenAfter1, _ := c.XLen(ctx, changesKey).Result()
	assert.Equal(t, streamLenBeforeReplays, streamLenAfter1, "replay of manifest1 appended stream entry: len %d, want %d", streamLenAfter1, streamLenBeforeReplays)
	tableRevAfter1 := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	assert.Equal(t, tableRevAdvanced, tableRevAfter1, "replay of manifest1 bumped table revision: got %s, want %s", tableRevAfter1, tableRevAdvanced)

	// Replay no-op batch after table revision has advanced
	rcptNoopReplay, err := ntable.ApplyBatch(ctx, c, manifestNoop)
	require.NoError(t, err, "replay manifestNoop failed")
	if rcptNoopReplay.ID != rcptNoop.ID || rcptNoopReplay.Outcome != rcptNoop.Outcome || rcptNoopReplay.After != rcptNoop.After {
		t.Fatalf("rcptNoopReplay mismatch: got %+v, want %+v", rcptNoopReplay, rcptNoop)
	}
	streamLenAfterNoop, _ := c.XLen(ctx, changesKey).Result()
	assert.Equal(t, streamLenBeforeReplays, streamLenAfterNoop, "replay of manifestNoop appended stream entry: len %d, want %d", streamLenAfterNoop, streamLenBeforeReplays)

	// 3. Conflicting payload with identical operation_id returns ErrOpConflict with changed=no
	conflicting1 := manifest1
	conflicting1.Actor = "malicious-actor"
	_, err = ntable.ApplyBatch(ctx, c, conflicting1)
	require.ErrorIs(t, err, ntable.ErrOpConflict, "expected ErrOpConflict, got")
	require.ErrorContains(t, err, "changed=no", "expected changed=no in error message")

	conflictingNoop := manifestNoop
	conflictingNoop.ExpectedTableRevision = "0"
	_, err = ntable.ApplyBatch(ctx, c, conflictingNoop)
	require.ErrorIs(t, err, ntable.ErrOpConflict, "expected ErrOpConflict for noop replay conflict, got")
	require.ErrorContains(t, err, "changed=no", "expected changed=no in error message")

	// Verify state remained unchanged after conflicts
	streamLenFinal, _ := c.XLen(ctx, changesKey).Result()
	assert.Equal(t, streamLenBeforeReplays, streamLenFinal, "conflicts appended to stream: %d vs %d", streamLenFinal, streamLenBeforeReplays)
}

func TestBatchDualStoreReplayFromStream(t *testing.T) {
	t.Parallel()
	_, c1 := live(t)
	_, c2 := live(t)
	ctx := context.Background()

	tb := demo()
	require.NoError(t, ntable.Create(ctx, c1, tb, now))
	require.NoError(t, ntable.Create(ctx, c2, tb, now))
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
	require.NoError(t, err, "XRange c1")

	// Replay each batch stream event sequentially on store 2:
	replayedCount := 0
	for _, event := range streamEvents1 {
		v := event.Values
		if v["verb"] != "apply" {
			continue
		}
		replayedCount++
		var args []string
		require.NoError(t, json.Unmarshal([]byte(fmt.Sprint(v["args"])), &args), "unmarshal event args")
		tableName := args[0]
		opID := args[1]

		// Fetch the durable request payload from store 1's operation record:
		reqJSON := operationRecord(t, c1, tableName, "0", opID)["request"]

		var replayManifest ntable.BatchManifest
		require.NoError(t, json.Unmarshal([]byte(reqJSON), &replayManifest), "unmarshal replay manifest")

		// Replay on store 2:
		rcpt2, err := ntable.ApplyBatch(ctx, c2, replayManifest)
		require.NoError(t, err, "replay batch %s on c2: %v", opID, err)

		// Assert receipt from store 2 matches the stream event on store 1:
		assert.Equal(t, fmt.Sprint(v["rev_before"]), strconv.FormatUint(rcpt2.Before, 10), "op %s replay rev_before = %d, want %v", opID, rcpt2.Before, v["rev_before"])
		assert.Equal(t, fmt.Sprint(v["rev_after"]), strconv.FormatUint(rcpt2.After, 10), "op %s replay rev_after = %d, want %v", opID, rcpt2.After, v["rev_after"])
		assert.Equal(t, fmt.Sprint(v["outcome"]), rcpt2.Outcome, "op %s replay outcome = %s, want %v", opID, rcpt2.Outcome, v["outcome"])
	}
	require.Equal(t, 4, replayedCount, "expected 4 batch stream events replayed, got %d", replayedCount)

	// Assert store 1 and store 2 reached identical state:
	// 1. Table revisions match
	c1Rev := c1.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	c2Rev := c2.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	require.Equal(t, c2Rev, c1Rev, "table revision mismatch: c1=%s, c2=%s", c1Rev, c2Rev)

	// 2. ReadSet across all members returns identical state
	rs1, err := ntable.ReadSetMembers(ctx, c1, "demo", []string{"m1", "m2", "m3"})
	require.NoError(t, err, "ReadSet c1")
	rs2, err := ntable.ReadSetMembers(ctx, c2, "demo", []string{"m1", "m2", "m3"})
	require.NoError(t, err, "ReadSet c2")
	require.Equal(t, rs2, rs1, "ReadSet mismatch between stores:\nstore 1: %+v\nstore 2: %+v", rs1, rs2)

	// 3. Stream event counts and event contents match (excluding stream ID timestamps)
	streamEvents2, err := c2.XRange(ctx, ntable.ChangesKey("demo"), "-", "+").Result()
	require.NoError(t, err, "XRange c2")
	require.Len(t, streamEvents1, len(streamEvents2), "stream length mismatch: c1 has %d, c2 has %d", len(streamEvents1), len(streamEvents2))
	for i := range streamEvents1 {
		v1 := streamEvents1[i].Values
		v2 := streamEvents2[i].Values
		for _, key := range []string{"verb", "args", "epoch", "rev_before", "rev_after", "actor", "outcome", "batch_delta", "cells", "members"} {
			assert.Equal(t, fmt.Sprint(v2[key]), fmt.Sprint(v1[key]), "event %d key %s mismatch: c1=%v, c2=%v", i, key, v1[key], v2[key])
		}
	}

	// 4. Verify cell scores and member hashes match
	for _, cell := range []string{"build:ready", "build:working", "test:ready"} {
		z1 := c1.ZRangeWithScores(ctx, "table:demo:cell:"+cell, 0, -1).Val()
		z2 := c2.ZRangeWithScores(ctx, "table:demo:cell:"+cell, 0, -1).Val()
		assert.Equal(t, z2, z1, "cell %s mismatch: c1=%v, c2=%v", cell, z1, z2)
	}
	m2Hash1 := c1.HGetAll(ctx, "table::member:m2").Val()
	m2Hash2 := c2.HGetAll(ctx, "table::member:m2").Val()
	assert.Equal(t, m2Hash2, m2Hash1, "member m2 hash mismatch: c1=%v, c2=%v", m2Hash1, m2Hash2)
}

func TestReviewWrongTypeDestinationPartialWrite(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	require.NoError(t, ntable.Create(ctx, c, demo(), now))
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellAdd(ctx, c, "demo", "build", "ready", "m1", 7); err != nil {
		t.Fatal(err)
	}
	dest := ntable.CellKey("demo", "build", "working")
	require.NoError(t, c.Set(ctx, dest, "wrong-type", 0).Err())
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
	require.Error(t, err, "expected error on destination WRONGTYPE, got nil")
	require.ErrorIs(t, err, ntable.ErrWrongType, "expected ErrWrongType, got")
	require.ErrorContains(t, err, "changed=no", "expected changed=no, got")
	require.Equal(t, before, after, "expected store unchanged on destination WRONGTYPE refusal")
	score := c.ZScore(ctx, ntable.CellKey("demo", "build", "ready"), "m1").Val()
	require.Equal(t, float64(7), score, "expected m1 score 7 in source, got %v", score)
}

func TestReviewBatchStreamWrongTypePartialWrite(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	require.NoError(t, ntable.Create(ctx, c, demo(), now))
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	require.NoError(t, c.Set(ctx, ntable.DefKey("demo")+":changes", "wrong-type", 0).Err())
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
	require.Error(t, err, "expected error on changes stream WRONGTYPE, got nil")
	require.ErrorIs(t, err, ntable.ErrWrongType, "expected ErrWrongType, got")
	require.ErrorContains(t, err, "changed=no", "expected changed=no, got")
	require.Equal(t, before, after, "expected store unchanged on stream WRONGTYPE refusal")
	placed := c.ZScore(ctx, ntable.CellKey("demo", "build", "ready"), "m1").Val()
	require.Equal(t, float64(0), placed, "expected m1 not placed, got %v", placed)
	revAfter := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	require.Equal(t, rev, revAfter, "expected revision unchanged (%s), got %s", rev, revAfter)
}

func TestReviewReadSetHidesPlacementDrift(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	require.NoError(t, ntable.Create(ctx, c, demo(), now))
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellAdd(ctx, c, "demo", "build", "ready", "m1", 7); err != nil {
		t.Fatal(err)
	}
	require.NoError(t, c.ZRem(ctx, ntable.CellKey("demo", "build", "ready"), "m1").Err())
	_, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1"})
	require.Error(t, err, "expected ReadSetMembers to refuse placement drift, got nil")
	require.ErrorIs(t, err, ntable.ErrDrift, "expected ErrDrift, got")
}

func TestReviewHiddenDuplicatePlacementAccepted(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	require.NoError(t, ntable.Create(ctx, c, demo(), now))
	if _, err := ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellAdd(ctx, c, "demo", "build", "ready", "m1", 7); err != nil {
		t.Fatal(err)
	}
	hidden := ntable.CellKey("demo", "build", "working")
	require.NoError(t, c.ZAdd(ctx, hidden, redis.Z{Score: 9, Member: "m1"}).Err())
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
	require.Error(t, err, "expected ApplyBatch to refuse hidden duplicate placement, got nil")
	require.ErrorIs(t, err, ntable.ErrDrift, "expected ErrDrift, got")
	require.ErrorContains(t, err, "changed=no", "expected changed=no, got")
	require.Equal(t, before, after, "expected store unchanged on hidden duplicate refusal")
	destScore := c.ZScore(ctx, ntable.CellKey("demo", "build", "done"), "m1").Val()
	require.Equal(t, float64(0), destScore, "expected dest score 0, got %v", destScore)
}

func TestReviewUnknownAndDuplicateJSONAccepted(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	require.NoError(t, ntable.Create(ctx, c, demo(), now))
	rev := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()

	// 1. Unknown top-level field rejected with REFUSED MANIFEST and zero mutation
	rawUnknown := fmt.Sprintf(`{"schema":1,"table":"demo","epoch":"0","expected_table_revision":%q,"operation_id":"review-unknown","actor":"review","members":[],"unknown":true}`, rev)
	before := storeImage(t, c)
	ans, err := c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey("demo")}, "demo", rawUnknown).Slice()
	after := storeImage(t, c)
	require.NoError(t, err, "FCall err")
	if len(ans) < 2 || ans[0] != "REFUSED" || ans[1] != "MANIFEST" {
		t.Fatalf("expected REFUSED MANIFEST for unknown top-level field, got: %v", ans)
	}
	require.Equal(t, before, after, "expected store unchanged after unknown field refusal")

	// 2. Duplicate top-level key rejected with REFUSED MANIFEST and zero mutation
	rawDuplicate := fmt.Sprintf(`{"schema":1,"table":"demo","epoch":"0","expected_table_revision":%q,"operation_id":"review-duplicate","actor":"first","actor":"second","members":[]}`, rev)
	before2 := storeImage(t, c)
	ans, err = c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey("demo")}, "demo", rawDuplicate).Slice()
	after2 := storeImage(t, c)
	require.NoError(t, err, "FCall err")
	if len(ans) < 2 || ans[0] != "REFUSED" || ans[1] != "MANIFEST" {
		t.Fatalf("expected REFUSED MANIFEST for duplicate top-level key, got: %v", ans)
	}
	require.Equal(t, before2, after2, "expected store unchanged after duplicate key refusal")

	// 3. Nested unknown key inside expect rejected
	rawNestedUnknown := fmt.Sprintf(`{"schema":1,"table":"demo","epoch":"0","expected_table_revision":%q,"operation_id":"review-nested-unknown","actor":"review","members":[{"id":"m1","expect":{"revision":"1","unknown":true}}]}`, rev)
	ans, err = c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey("demo")}, "demo", rawNestedUnknown).Slice()
	require.NoError(t, err, "FCall err")
	if len(ans) < 2 || ans[0] != "REFUSED" || ans[1] != "MANIFEST" {
		t.Fatalf("expected REFUSED MANIFEST for nested unknown key, got: %v", ans)
	}

	// 4. Nested duplicate key inside expect rejected
	rawNestedDuplicate := fmt.Sprintf(`{"schema":1,"table":"demo","epoch":"0","expected_table_revision":%q,"operation_id":"review-nested-duplicate","actor":"review","members":[{"id":"m1","expect":{"revision":"1","revision":"2"}}]}`, rev)
	ans, err = c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey("demo")}, "demo", rawNestedDuplicate).Slice()
	require.NoError(t, err, "FCall err")
	if len(ans) < 2 || ans[0] != "REFUSED" || ans[1] != "MANIFEST" {
		t.Fatalf("expected REFUSED MANIFEST for nested duplicate key, got: %v", ans)
	}
}

func TestBatchContractWrongTypeMemberRefusal(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	require.NoError(t, ntable.Create(ctx, c, demo(), now))
	rev := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()

	// Pre-populate member key as a Redis string instead of hash
	mkey := ntable.MemberKey("m_bad")
	require.NoError(t, c.Set(ctx, mkey, "string-not-hash", 0).Err())

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
	require.Error(t, err, "expected ApplyBatch to fail on wrong type member key")
	require.ErrorIs(t, err, ntable.ErrWrongType, "expected ErrWrongType, got")
	require.ErrorContains(t, err, "changed=no", "expected changed=no, got")
	require.Equal(t, before, after, "store changed on wrong type member key refusal")
}

func TestBatchContractRemoveAndOverlapValidation(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	require.NoError(t, ntable.Create(ctx, c, demo(), now))
	rev := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()

	// 1. remove: false in raw JSON refuses before writes
	before1 := storeImage(t, c)
	rawRemoveFalse := fmt.Sprintf(`{"schema":1,"table":"demo","epoch":"0","expected_table_revision":%q,"operation_id":"op-rem-false","actor":"test","members":[{"id":"m1","expect":{"revision":"0"},"remove":false}]}`, rev)
	ans, err := c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey("demo")}, "demo", rawRemoveFalse).Slice()
	after1 := storeImage(t, c)
	require.NoError(t, err, "FCall err")
	if len(ans) < 2 || ans[0] != "REFUSED" || ans[1] != "ARGS" {
		t.Fatalf("expected REFUSED ARGS for remove:false, got: %v", ans)
	}
	require.Equal(t, before1, after1, "store changed on remove:false refusal")

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
	require.Error(t, err, "expected failure on unplaced member remove")
	require.ErrorIs(t, err, ntable.ErrNotMember, "expected ErrNotMember, got")
	require.ErrorContains(t, err, "changed=no", "expected changed=no, got")
	require.Equal(t, before2, after2, "store changed on unplaced member remove refusal")

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
	require.Error(t, err, "expected failure on set/unset overlap")
	require.ErrorIs(t, err, ntable.ErrMutation, "expected ErrMutation, got")
	require.ErrorContains(t, err, "changed=no", "expected changed=no, got")
	require.Equal(t, before3, after3, "store changed on set/unset overlap refusal")
}

func TestBatchContractMoveNoopAndScoreSemantics(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	require.NoError(t, ntable.Create(ctx, c, demo(), now))
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
	require.NoError(t, err, "create m1")
	require.Equal(t, "changed", rcptCreate.Outcome, "expected outcome changed, got %q", rcptCreate.Outcome)

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
	require.NoError(t, err, "apply noop move")
	require.Equal(t, "noop", rcptNoop.Outcome, "expected outcome noop, got %q", rcptNoop.Outcome)
	require.Equal(t, 0, rcptNoop.BatchDelta.ChangedCount, "expected changed_count=0 for noop move, got %d", rcptNoop.BatchDelta.ChangedCount)
	// Entire batch of no-ops increments table revision once and records receipt
	require.Equal(t, rcptCreate.After+1, rcptNoop.After, "expected table revision to increment to %d, got %d", rcptCreate.After+1, rcptNoop.After)
	// Verify member revision was NOT incremented
	rsNoop, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1"})
	require.NoError(t, err, "ReadSet")
	m1Noop, ok := rsNoop.Member("m1")
	require.True(t, ok, "expected m1 revision to remain 1 after no-op move, got %d", m1Noop.Revision)
	require.Equal(t, uint64(1), m1Noop.Revision, "expected m1 revision to remain 1 after no-op move, got %d", m1Noop.Revision)

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
	require.NoError(t, err, "apply score change")
	require.Equal(t, "changed", rcptScore.Outcome, "expected outcome changed, got %q", rcptScore.Outcome)
	require.Equal(t, 1, rcptScore.BatchDelta.ChangedCount, "expected changed_count=1 for score change, got %d", rcptScore.BatchDelta.ChangedCount)
	rsScore, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1"})
	require.NoError(t, err, "ReadSet")
	m1Score, ok := rsScore.Member("m1")
	if !ok || m1Score.Revision != 2 || m1Score.Score != 25 {
		t.Fatalf("expected m1 revision 2 and score 25, got rev=%d score=%g", m1Score.Revision, m1Score.Score)
	}
}

func TestBatchContractReceiptBeforeAfterDeltas(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	require.NoError(t, ntable.Create(ctx, c, demo(), now))
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
	require.NoError(t, err, "init batch")

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
	require.NoError(t, err, "apply mixed batch")

	deltas := make(map[string]ntable.BatchMemberDelta)
	for _, m := range rcptMixed.BatchDelta.Members {
		deltas[m.ID] = m
	}

	// Verify m1 deltas
	d1, ok := deltas["m1"]
	require.True(t, ok, "missing m1 delta")
	require.Equal(t, "build:ready", d1.BeforePlace, "m1 place before=%q after=%q", d1.BeforePlace, d1.AfterPlace)
	require.Equal(t, "build:done", d1.AfterPlace, "m1 place before=%q after=%q", d1.BeforePlace, d1.AfterPlace)
	require.NotNil(t, d1.BeforeScore, "m1 before_score want 10, got %v", d1.BeforeScore)
	require.Equal(t, float64(10), *d1.BeforeScore, "m1 before_score want 10, got %v", d1.BeforeScore)
	require.NotNil(t, d1.AfterScore, "m1 after_score want 15, got %v", d1.AfterScore)
	require.Equal(t, float64(15), *d1.AfterScore, "m1 after_score want 15, got %v", d1.AfterScore)
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
	require.True(t, ok, "missing m2 delta")
	require.Empty(t, d2.BeforePlace, "m2 place before=%q after=%q", d2.BeforePlace, d2.AfterPlace)
	require.Equal(t, "build:ready", d2.AfterPlace, "m2 place before=%q after=%q", d2.BeforePlace, d2.AfterPlace)
	require.Nil(t, d2.BeforeScore, "m2 created before_score should be nil, got %v", d2.BeforeScore)
	require.NotNil(t, d2.AfterScore, "m2 after_score want 5, got %v", d2.AfterScore)
	require.Equal(t, float64(5), *d2.AfterScore, "m2 after_score want 5, got %v", d2.AfterScore)
	if fRole, ok := d2.Fields["role"]; !ok || fRole.Before != nil || fRole.After == nil || *fRole.After != "intern" {
		t.Fatalf("m2 role field delta mismatch (want nil before): %+v", fRole)
	}

	// Verify m3 deltas (remove)
	d3, ok := deltas["m3"]
	require.True(t, ok, "missing m3 delta")
	require.Equal(t, "build:ready", d3.BeforePlace, "m3 place before=%q after=%q", d3.BeforePlace, d3.AfterPlace)
	require.Empty(t, d3.AfterPlace, "m3 place before=%q after=%q", d3.BeforePlace, d3.AfterPlace)
	require.NotNil(t, d3.BeforeScore, "m3 before_score want 30, got %v", d3.BeforeScore)
	require.Equal(t, float64(30), *d3.BeforeScore, "m3 before_score want 30, got %v", d3.BeforeScore)
	require.Nil(t, d3.AfterScore, "m3 removed after_score should be nil, got %v", d3.AfterScore)
}

func TestBatchContractRefusalStoreImagePreserved(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()
	require.NoError(t, ntable.Create(ctx, c, demo(), now))
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
	require.NoError(t, err, "init batch")
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
			require.Error(t, err, "%s: expected ApplyBatch to refuse, got nil", tc.name)
			require.ErrorContains(t, err, "changed=no", "%s: expected changed=no, got: %v", tc.name, err)
			require.Equal(t, before, after, "%s: store was modified despite refusal: before != after", tc.name)
		})
	}
}

func TestBatchAcceptedInteractingCrossRowMultiMemberWitness(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()

	cols, err := ntable.ParseColumns("col1,col2")
	require.NoError(t, err)
	tb := ntable.Table{Name: "demo", Columns: cols}
	require.NoError(t, ntable.Create(ctx, c, tb, now))
	_, err = ntable.RowAdd(ctx, c, "demo", "row1", ntable.RowSpec{})
	require.NoError(t, err)
	_, err = ntable.RowAdd(ctx, c, "demo", "row2", ntable.RowSpec{})
	require.NoError(t, err)

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
	require.NoError(t, err, "setup prestate batch")
	require.Equal(t, "changed", rcptPre.Outcome, "setup prestate outcome = %q, want changed", rcptPre.Outcome)

	// Verify prestate via ReadSet
	rsPre, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1", "m2"})
	require.NoError(t, err, "ReadSet prestate")
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
	require.NoError(t, err, "interacting cross-row ApplyBatch failed")

	// Verify acceptance and receipts
	require.Equal(t, "changed", rcptBatch.Outcome, "expected outcome changed, got %q", rcptBatch.Outcome)
	require.NotNil(t, rcptBatch.BatchDelta, "expected non-nil BatchDelta")
	require.Equal(t, 2, rcptBatch.BatchDelta.ChangedCount, "expected changed_count=2, got %d", rcptBatch.BatchDelta.ChangedCount)
	require.Equal(t, 2, rcptBatch.BatchDelta.SelectedCount, "expected selected_count=2, got %d", rcptBatch.BatchDelta.SelectedCount)
	require.Equal(t, 0, rcptBatch.BatchDelta.GuardCount, "expected syntactic guard_count=0 for moves, got %d", rcptBatch.BatchDelta.GuardCount)

	deltaMembers := make(map[string]ntable.BatchMemberDelta)
	for _, md := range rcptBatch.BatchDelta.Members {
		deltaMembers[md.ID] = md
	}
	d1, ok1 := deltaMembers["m1"]
	d2, ok2 := deltaMembers["m2"]
	require.True(t, ok1, "missing member delta: m1=%v, m2=%v", ok1, ok2)
	require.True(t, ok2, "missing member delta: m1=%v, m2=%v", ok1, ok2)
	if d1.BeforePlace != "row1:col1" || d1.AfterPlace != "row2:col2" || d1.BeforeScore == nil || *d1.BeforeScore != 10 || d1.AfterScore == nil || *d1.AfterScore != 30 || d1.BeforeRev != "1" || d1.AfterRev != "2" {
		t.Fatalf("unexpected m1 delta: %+v", d1)
	}
	if d2.BeforePlace != "row2:col2" || d2.AfterPlace != "row1:col1" || d2.BeforeScore == nil || *d2.BeforeScore != 20 || d2.AfterScore == nil || *d2.AfterScore != 40 || d2.BeforeRev != "1" || d2.AfterRev != "2" {
		t.Fatalf("unexpected m2 delta: %+v", d2)
	}

	// Verify full poststate
	rsPost, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1", "m2"})
	require.NoError(t, err, "ReadSet poststate")
	m1Post, _ := rsPost.Member("m1")
	m2Post, _ := rsPost.Member("m2")
	if !m1Post.Placed || m1Post.Row != "row2" || m1Post.Col != "col2" || m1Post.Score != 30 || m1Post.Revision != 2 || m1Post.Fields["phase"] != "post" {
		t.Fatalf("unexpected m1 poststate: %+v", m1Post)
	}
	if !m2Post.Placed || m2Post.Row != "row1" || m2Post.Col != "col1" || m2Post.Score != 40 || m2Post.Revision != 2 || m2Post.Fields["phase"] != "post" {
		t.Fatalf("unexpected m2 poststate: %+v", m2Post)
	}

	// Verify physical Redis cell zsets
	sc := c.ZScore(ctx, ntable.CellKey("demo", "row2", "col2"), "m1").Val()
	require.Equal(t, float64(30), sc, "expected m1 score 30 in row2:col2, got %v", sc)
	sc = c.ZScore(ctx, ntable.CellKey("demo", "row2", "col2"), "m2").Val()
	require.Equal(t, float64(0), sc, "expected m2 removed from row2:col2, got %v", sc)
	sc = c.ZScore(ctx, ntable.CellKey("demo", "row1", "col1"), "m2").Val()
	require.Equal(t, float64(40), sc, "expected m2 score 40 in row1:col1, got %v", sc)
	sc = c.ZScore(ctx, ntable.CellKey("demo", "row1", "col1"), "m1").Val()
	require.Equal(t, float64(0), sc, "expected m1 removed from row1:col1, got %v", sc)

	// Verify reverse indexes
	p := c.HGet(ctx, ntable.MemberKey("m1"), "place:demo").Val()
	require.Equal(t, "row2:col2", p, "expected m1 reverse index row2:col2, got %q", p)
	p = c.HGet(ctx, ntable.MemberKey("m2"), "place:demo").Val()
	require.Equal(t, "row1:col1", p, "expected m2 reverse index row1:col1, got %q", p)
}

func TestBatchLateInvalidAtNMaxRefusal(t *testing.T) {
	t.Parallel()
	testBatchLateInvalidAtNMaxRefusal(t)
}

func testBatchLateInvalidAtNMaxRefusal(t *testing.T) {
	c, _ := store(t)
	ctx := context.Background()

	cols, err := ntable.ParseColumns("ready")
	require.NoError(t, err)
	tb := ntable.Table{Name: "demo", Columns: cols}
	require.NoError(t, ntable.Create(ctx, c, tb, now))
	_, err = ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{})
	require.NoError(t, err)

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
	require.NoError(t, err, "create 128 members batch")
	require.Equal(t, "changed", rcptCreate.Outcome, "expected 128 changed, got outcome=%q delta=%+v", rcptCreate.Outcome, rcptCreate.BatchDelta)
	require.Equal(t, 128, rcptCreate.BatchDelta.ChangedCount, "expected 128 changed, got outcome=%q delta=%+v", rcptCreate.Outcome, rcptCreate.BatchDelta)

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
		require.NoError(t, err, "dumpStore before")
		_, err = ntable.ApplyBatch(ctx, c, manifest)
		after, err2 := dumpStore(ctx, c)
		require.NoError(t, err2, "dumpStore after")

		require.Error(t, err, "expected ApplyBatch to fail on 128th invalid entry")
		require.ErrorIs(t, err, ntable.ErrMemberRevision, "expected ErrMemberRevision, got")
		require.ErrorContains(t, err, "changed=no", "expected changed=no, got")
		diff := diffSnapshots(before, after)
		require.Empty(t, diff, "store modified despite refusal: %s", diff)
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
		require.NoError(t, err, "dumpStore before")
		_, err = ntable.ApplyBatch(ctx, c, manifest)
		after, err2 := dumpStore(ctx, c)
		require.NoError(t, err2, "dumpStore after")

		require.Error(t, err, "expected ApplyBatch to fail on 128th invalid remove entry")
		require.ErrorIs(t, err, ntable.ErrNotMember, "expected ErrNotMember, got")
		require.ErrorContains(t, err, "changed=no", "expected changed=no, got")
		diff := diffSnapshots(before, after)
		require.Empty(t, diff, "store modified despite refusal: %s", diff)
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
	require.NoError(t, err)
	tb := ntable.Table{Name: "demo", Columns: cols}
	require.NoError(t, ntable.Create(ctx, c, tb, now))
	_, err = ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{})
	require.NoError(t, err)

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
	require.NoError(t, err, "create m_unplaced")
	require.Equal(t, "changed", rcptCreate.Outcome, "expected outcome changed, got %q", rcptCreate.Outcome)

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
	require.NoError(t, err, "prior remove m_unplaced")

	// Verify acceptance and receipt deltas for the removal
	require.Equal(t, "changed", rcptRemove.Outcome, "expected outcome changed on remove, got %q", rcptRemove.Outcome)
	require.NotNil(t, rcptRemove.BatchDelta, "unexpected remove delta: %+v", rcptRemove.BatchDelta)
	require.Equal(t, 1, rcptRemove.BatchDelta.ChangedCount, "unexpected remove delta: %+v", rcptRemove.BatchDelta)
	require.Len(t, rcptRemove.BatchDelta.Members, 1, "expected 1 member delta, got %d", len(rcptRemove.BatchDelta.Members))
	remDelta := rcptRemove.BatchDelta.Members[0]
	if remDelta.ID != "m_unplaced" || remDelta.BeforePlace != "build:ready" || remDelta.AfterPlace != "" {
		t.Fatalf("unexpected remDelta places: %+v", remDelta)
	}
	if remDelta.BeforeScore == nil || *remDelta.BeforeScore != 10 || remDelta.AfterScore != nil {
		t.Fatalf("unexpected remDelta scores: %+v", remDelta)
	}
	require.Equal(t, "1", remDelta.BeforeRev, "unexpected remDelta revs: %+v", remDelta)
	require.Equal(t, "2", remDelta.AfterRev, "unexpected remDelta revs: %+v", remDelta)

	// Verify full store state: member record is retained, application fields and revision are preserved,
	// but placement is cleared and cell zset no longer contains the member.
	sc := c.ZScore(ctx, ntable.CellKey("demo", "build", "ready"), "m_unplaced").Val()
	require.Equal(t, float64(0), sc, "expected m_unplaced removed from zset, got score %v", sc)
	p := c.HGet(ctx, ntable.MemberKey("m_unplaced"), "place:demo").Val()
	require.Empty(t, p, "expected place:demo to be cleared, got %q", p)
	r := c.HGet(ctx, ntable.MemberKey("m_unplaced"), "revision").Val()
	require.Equal(t, "2", r, "expected member revision 2, got %q", r)
	role := c.HGet(ctx, ntable.MemberKey("m_unplaced"), "role").Val()
	require.Equal(t, "tester", role, "expected retained field role=tester, got %q", role)
	custom := c.HGet(ctx, ntable.MemberKey("m_unplaced"), "custom").Val()
	require.Equal(t, "payload", custom, "expected retained field custom=payload, got %q", custom)

	rsUnplaced, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m_unplaced"})
	require.NoError(t, err, "ReadSet unplaced")
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
	require.NoError(t, err, "dumpStore before")
	_, err = ntable.ApplyBatch(ctx, c, reRemoveManifest)
	afterDump, err2 := dumpStore(ctx, c)
	require.NoError(t, err2, "dumpStore after")

	require.Error(t, err, "expected ApplyBatch to refuse remove:true on already-unplaced member")
	require.ErrorIs(t, err, ntable.ErrNotMember, "expected ErrNotMember, got")
	require.ErrorContains(t, err, "changed=no", "expected changed=no, got")
	diff := diffSnapshots(beforeDump, afterDump)
	require.Empty(t, diff, "store modified on unplaced remove refusal: %s", diff)
}

func TestBatchNoopGuardCountAndCardinalityWitness(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()

	cols, err := ntable.ParseColumns("ready")
	require.NoError(t, err)
	tb := ntable.Table{Name: "demo", Columns: cols}
	require.NoError(t, ntable.Create(ctx, c, tb, now))
	_, err = ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{})
	require.NoError(t, err)

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
	require.NoError(t, err, "create m1")
	require.Equal(t, "changed", rcptCreate.Outcome, "expected outcome changed, got %q", rcptCreate.Outcome)

	// Capture state before no-op move
	revBefore := strconv.FormatUint(rcptCreate.After, 10)
	changesKey := ntable.ChangesKey("demo")
	streamLenBefore, err := c.XLen(ctx, changesKey).Result()
	require.NoError(t, err, "XLen before")

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
	require.NoError(t, err, "apply noop move")

	// Assert outcome is noop
	require.Equal(t, "noop", rcptNoop.Outcome, "expected outcome noop, got %q", rcptNoop.Outcome)
	require.NotNil(t, rcptNoop.BatchDelta, "expected non-nil BatchDelta")

	// Assert rcpt.BatchDelta.GuardCount == 0 (syntactic guard_count is 0, since move is mutation syntax)
	require.Equal(t, 0, rcptNoop.BatchDelta.GuardCount, "expected syntactic GuardCount == 0 for move syntax, got %d", rcptNoop.BatchDelta.GuardCount)
	require.Equal(t, 0, rcptNoop.BatchDelta.ChangedCount, "expected ChangedCount == 0 for no-op move, got %d", rcptNoop.BatchDelta.ChangedCount)
	require.Equal(t, 1, rcptNoop.BatchDelta.SelectedCount, "expected SelectedCount == 1, got %d", rcptNoop.BatchDelta.SelectedCount)

	// Assert member revision is unchanged
	rsAfter, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1"})
	require.NoError(t, err, "ReadSet after")
	m1After, ok := rsAfter.Member("m1")
	require.True(t, ok, "expected m1 revision unchanged at 1, got %+v", m1After)
	require.Equal(t, uint64(1), m1After.Revision, "expected m1 revision unchanged at 1, got %+v", m1After)
	r := c.HGet(ctx, ntable.MemberKey("m1"), "revision").Val()
	require.Equal(t, "1", r, "expected m1 hash revision 1, got %q", r)

	// Assert table revision +1
	require.Equal(t, rcptCreate.After+1, rcptNoop.After, "expected table revision to increment from %d to %d, got %d", rcptCreate.After, rcptCreate.After+1, rcptNoop.After)
	tableRevCurrent := c.HGet(ctx, ntable.DefKey("demo")+":revision", "n").Val()
	require.Equal(t, strconv.FormatUint(rcptCreate.After+1, 10), tableRevCurrent, "expected table revision %d, got %s", rcptCreate.After+1, tableRevCurrent)

	// Assert stream cardinality is exactly 1 event
	streamLenAfter, err := c.XLen(ctx, changesKey).Result()
	require.NoError(t, err, "XLen after")
	require.Equal(t, streamLenBefore+1, streamLenAfter, "expected stream cardinality to increase by 1 (from %d to %d), got %d", streamLenBefore, streamLenBefore+1, streamLenAfter)
	events, err := c.XRange(ctx, changesKey, rcptNoop.ID, rcptNoop.ID).Result()
	require.NoError(t, err, "expected exactly 1 stream event for rcptNoop.ID %s, got %d (err: %v)", rcptNoop.ID, len(events), err)
	require.Len(t, events, 1, "expected exactly 1 stream event for rcptNoop.ID %s, got %d (err: %v)", rcptNoop.ID, len(events), err)
	if events[0].Values["outcome"] != "noop" {
		t.Fatalf("expected stream event outcome noop, got %v", events[0].Values["outcome"])
	}

	// Assert operation-record cardinality is exactly 1 record
	opRecord := operationRecord(t, c, "demo", "0", "op-move-noop-witness")
	require.Equal(t, "noop", opRecord["outcome"], "expected opRecord outcome noop, got %q", opRecord["outcome"])
	require.Equal(t, rcptNoop.ID, opRecord["stream_id"], "expected opRecord stream_id %s, got %s", rcptNoop.ID, opRecord["stream_id"])
}

func TestBatchAcceptedOmittedRevisionAfterWriterAdvanceWitness(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := context.Background()

	cols, err := ntable.ParseColumns("ready,working")
	require.NoError(t, err)
	tb := ntable.Table{Name: "demo", Columns: cols}
	require.NoError(t, ntable.Create(ctx, c, tb, now))
	_, err = ntable.RowAdd(ctx, c, "demo", "build", ntable.RowSpec{})
	require.NoError(t, err)

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
	require.NoError(t, err, "create m1")
	require.Equal(t, "changed", rcptInit.Outcome, "expected outcome changed, got %q", rcptInit.Outcome)

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
	require.NoError(t, err, "writer1 apply batch")
	require.Equal(t, "changed", rcptW1.Outcome, "writer1 expected outcome changed, got %q", rcptW1.Outcome)

	// Verify member m1's revision advanced to 2
	rsW1, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1"})
	require.NoError(t, err, "ReadSet after writer1")
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
	require.NoError(t, err, "writer2 apply batch with omitted revision")
	require.Equal(t, "changed", rcptW2.Outcome, "writer2 expected outcome changed, got %q", rcptW2.Outcome)

	// Verify batch succeeded and m1's revision advanced to 3
	rsW2, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1"})
	require.NoError(t, err, "ReadSet after writer2")
	m1W2, ok := rsW2.Member("m1")
	if !ok || m1W2.Revision != 3 || m1W2.Fields["status"] != "step2" || m1W2.Fields["writer2"] != "applied" {
		t.Fatalf("expected m1 at revision 3 after writer2, got %+v", m1W2)
	}

	// Writer 3 submits a batch modifying m1 with raw JSON expect without revision
	// confirming omitted revision in wire format is accepted after revision advance.
	revAfterW2 := strconv.FormatUint(rcptW2.After, 10)
	rawWriter3 := fmt.Sprintf(`{"schema":1,"table":"demo","epoch":"0","expected_table_revision":%q,"operation_id":"op-w3-wire-omitted","actor":"writer3","members":[{"id":"m1","expect":{"fields":{"status":{"equals":"step2"}}},"set":{"status":"step3","wire":"accepted"}}]}`, revAfterW2)
	ans, err := c.FCall(ctx, ntable.FnApply, []string{ntable.DefKey("demo")}, "demo", rawWriter3).Slice()
	require.NoError(t, err, "writer3 raw apply")
	if len(ans) < 2 || ans[0] != "OK" {
		t.Fatalf("writer3 expected OK, got %v", ans)
	}

	// Verify m1 revision advanced to 4 and status is step3
	rsW3, err := ntable.ReadSetMembers(ctx, c, "demo", []string{"m1"})
	require.NoError(t, err, "ReadSet after writer3")
	m1W3, ok := rsW3.Member("m1")
	if !ok || m1W3.Revision != 4 || m1W3.Fields["status"] != "step3" || m1W3.Fields["wire"] != "accepted" {
		t.Fatalf("expected m1 at revision 4 after writer3, got %+v", m1W3)
	}
}
