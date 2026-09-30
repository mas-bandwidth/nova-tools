package tset

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"testing"
)

// Unit witnesses for Stella's R1, R2, and R3 contract invariants on the Mem twin
// (from notes stella-1f9d50b66e3f and stella-54bf705d7a69).
// Companion functional tests for live Redis 8 and Mem parity live in
// property_contract_invariants_functional_test.go.

func r1r2r3MemFixture(t *testing.T, space string) *Mem {
	t.Helper()
	m := NewMem()
	if err := m.DefineTable(space, "cards", TableDefinition{
		Columns:      []string{"ready", "busy"},
		MemberPrefix: space + "member:cards:",
		EpochKey:     space + "sprint:epoch",
		EpochField:   "n",
	}); err != nil {
		t.Fatalf("define table: %v", err)
	}
	if err := m.SeedRow(space, "cards", "0", "r0", "0"); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	return m
}

// TestPropertyContractR1MemCreate2000PlusProp verifies that in Mem, a step
// creating 2,000 members plus 1 table property records changed=2001,
// immediate replay returns replay=true with changed=2001, and a done lookup
// returns a matching receipt with changed=2001.
func TestPropertyContractR1MemCreate2000PlusProp(t *testing.T) {
	t.Parallel()
	const space = "r1-mem:"
	m := r1r2r3MemFixture(t, space)
	ctx := context.Background()

	ids := make([]string, MaxMemberCandidates)
	scores := make([]string, MaxMemberCandidates)
	for i := 0; i < MaxMemberCandidates; i++ {
		ids[i] = fmt.Sprintf("card-%04d", i)
		scores[i] = "1"
	}
	propVal := "active"
	op, intent := "op-create2000-prop", "step with 2000 members and table property"
	step := Step{
		Epoch:  "0",
		Space:  space,
		Op:     &op,
		Intent: &intent,
		Entries: []Entry{
			{
				Kind:   "create",
				Table:  "cards",
				To:     "r0:ready",
				IDs:    ids,
				Scores: scores,
			},
			{
				Kind:  "prop",
				Table: "cards",
				Name:  "status",
				Value: &propVal,
			},
		},
	}

	fresh, err := m.Step(ctx, step)
	if err != nil {
		t.Fatalf("fresh step failed: %v", err)
	}
	if fresh.Status != "ok" || fresh.Replay || fresh.Changed != MaxMemberCandidates+1 {
		t.Fatalf("fresh reply = %+v, want status ok, replay false, changed %d", fresh, MaxMemberCandidates+1)
	}
	if !reflect.DeepEqual(fresh.ChangedPerEntry, []int{MaxMemberCandidates, 1}) {
		t.Fatalf("changed_per_entry = %v, want [%d, 1]", fresh.ChangedPerEntry, MaxMemberCandidates)
	}

	// Idempotent replay: exact same request with same op and intent.
	beforeReplay, err := m.Snapshot(space)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := m.Step(ctx, step)
	if err != nil {
		t.Fatalf("replay failed: %v", err)
	}
	if !replay.Replay || replay.Status != "ok" || replay.Changed != MaxMemberCandidates+1 {
		t.Fatalf("replay reply = %+v, want replay true, status ok, changed %d", replay, MaxMemberCandidates+1)
	}
	afterReplay, err := m.Snapshot(space)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(beforeReplay, afterReplay) {
		t.Fatal("replay modified Mem state")
	}

	// Done query: verify the stored receipt in the done slot.
	plan := ReadPlan{
		Epoch: "0", Space: space, Mode: "atomic",
		Queries: []ReadQuery{{
			Kind: "done",
			Ops:  []DoneIdentity{{Epoch: "0", Op: op, IntentDigest: intentDigest(intent)}},
		}},
	}
	reply, err := m.Read(ctx, plan)
	if err != nil {
		t.Fatalf("done read: %v", err)
	}
	if len(reply.Answers) != 1 || len(reply.Answers[0].Done) != 1 {
		t.Fatalf("done answers = %+v", reply.Answers)
	}
	slot := reply.Answers[0].Done[0]
	if slot.Status != "match" || slot.Receipt == nil {
		t.Fatalf("done slot = %+v, want match with non-nil receipt", slot)
	}
	if slot.Receipt.Changed != MaxMemberCandidates+1 {
		t.Fatalf("done receipt changed = %d, want %d", slot.Receipt.Changed, MaxMemberCandidates+1)
	}
}

// TestPropertyContractR2MemAdvanceEmptinessPreflight verifies that in Mem:
// (a) advance + equal-value no-op prop with preseeded x=v on successor refuses DRIFT.
// (b) advance + absent-name propguard with preseeded x=v on successor refuses DRIFT.
// Both preserve the Mem state completely unchanged before mutation.
// (c) advance into an empty successor succeeds and mutates normally.
func TestPropertyContractR2MemAdvanceEmptinessPreflight(t *testing.T) {
	t.Parallel()

	t.Run("equal_value_noop_prop_refuses_drift", func(t *testing.T) {
		t.Parallel()
		const space = "r2-mem-equal:"
		m := r1r2r3MemFixture(t, space)
		ctx := context.Background()

		// Preseed successor property x=v in epoch 1.
		if err := m.SeedProperty(space, "cards", "1", "x", "v"); err != nil {
			t.Fatalf("seed successor property: %v", err)
		}
		before, err := m.Snapshot(space)
		if err != nil {
			t.Fatal(err)
		}

		propVal := "v"
		op, intent := "advance-equal-prop", "advance with equal prop"
		step := Step{
			Epoch: "0", Space: space,
			Op:     &op,
			Intent: &intent,
			Entries: []Entry{
				{Kind: "advance", AdvanceFrom: "0"},
				{Kind: "prop", Table: "cards", Name: "x", Value: &propVal},
			},
		}

		reply, err := m.Step(ctx, step)
		ref := requireRefusal(t, err, "DRIFT")
		if reply.Status == "ok" {
			t.Fatalf("advance into non-empty successor succeeded: %+v", reply)
		}
		if ref.Code != "DRIFT" {
			t.Fatalf("refusal code = %s, want DRIFT", ref.Code)
		}

		after, err := m.Snapshot(space)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatal("refused step modified Mem state")
		}
		if after.ActiveEpoch != "0" {
			t.Fatalf("active epoch = %s, want 0", after.ActiveEpoch)
		}
	})

	t.Run("absent_name_guard_refuses_drift", func(t *testing.T) {
		t.Parallel()
		const space = "r2-mem-absent:"
		m := r1r2r3MemFixture(t, space)
		ctx := context.Background()

		// Preseed successor property x=v in epoch 1.
		if err := m.SeedProperty(space, "cards", "1", "x", "v"); err != nil {
			t.Fatalf("seed successor property: %v", err)
		}
		before, err := m.Snapshot(space)
		if err != nil {
			t.Fatal(err)
		}

		op, intent := "advance-absent-guard", "advance with absent guard"
		step := Step{
			Epoch: "0", Space: space,
			Op:     &op,
			Intent: &intent,
			Entries: []Entry{
				{Kind: "advance", AdvanceFrom: "0"},
				{Kind: "propguard", Table: "cards", Name: "absent_name", Value: nil},
			},
		}

		reply, err := m.Step(ctx, step)
		ref := requireRefusal(t, err, "DRIFT")
		if reply.Status == "ok" {
			t.Fatalf("advance into non-empty successor succeeded: %+v", reply)
		}
		if ref.Code != "DRIFT" {
			t.Fatalf("refusal code = %s, want DRIFT", ref.Code)
		}

		after, err := m.Snapshot(space)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatal("refused step modified Mem state")
		}
		if after.ActiveEpoch != "0" {
			t.Fatalf("active epoch = %s, want 0", after.ActiveEpoch)
		}
	})

	t.Run("empty_successor_positive_control", func(t *testing.T) {
		t.Parallel()
		const space = "r2-mem-empty:"
		m := r1r2r3MemFixture(t, space)
		ctx := context.Background()

		propVal := "v"
		op, intent := "advance-empty-successor", "advance into empty successor"
		step := Step{
			Epoch: "0", Space: space,
			Op:     &op,
			Intent: &intent,
			Entries: []Entry{
				{Kind: "advance", AdvanceFrom: "0"},
				{Kind: "prop", Table: "cards", Name: "x", Value: &propVal},
			},
		}

		reply, err := m.Step(ctx, step)
		if err != nil || reply.Status != "ok" || reply.EpochAfter != "1" {
			t.Fatalf("advance into empty successor failed: reply=%+v err=%v", reply, err)
		}
		after, err := m.Snapshot(space)
		if err != nil {
			t.Fatal(err)
		}
		if after.ActiveEpoch != "1" {
			t.Fatalf("active epoch = %s, want 1", after.ActiveEpoch)
		}
		if after.Epochs["1"].Tables["cards"].Props["x"] != "v" {
			t.Fatalf("successor property x = %q, want v", after.Epochs["1"].Tables["cards"].Props["x"])
		}
	})
}

// TestPropertyContractR3MemCellProbeLimit verifies that Mem enforces the 20,000
// probe limit when a property entry encounters an unobserved name (triggering an HLEN probe).
func TestPropertyContractR3MemCellProbeLimit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("probe_limit_refusal", func(t *testing.T) {
		t.Parallel()
		m2 := NewMem()
		const sp = "r3-probe-cap:"
		// Define 1 table with 30 columns and 100 rows.
		cols := make([]string, 30)
		for ci := 0; ci < 30; ci++ {
			cols[ci] = fmt.Sprintf("c%02d", ci)
		}
		if err := m2.DefineTable(sp, "cards", TableDefinition{
			Columns:      cols,
			MemberPrefix: sp + "m:cards:",
			EpochKey:     sp + "epoch",
			EpochField:   "n",
		}); err != nil {
			t.Fatalf("define table cards: %v", err)
		}
		for ri := 0; ri < 100; ri++ {
			row := fmt.Sprintf("r%03d", ri)
			if err := m2.SeedRow(sp, "cards", "0", row, Decimal(strconv.Itoa(ri))); err != nil {
				t.Fatalf("seed row %s: %v", row, err)
			}
		}

		// Table cards has 100 rows * 30 columns = 3,000 valid distinct cells.
		var allCells []string
		for ri := 0; ri < 100; ri++ {
			for ci := 0; ci < 30; ci++ {
				allCells = append(allCells, fmt.Sprintf("r%03d:c%02d", ri, ci))
			}
		}

		// 6 count entries of 3,000 cells each = 18,000 probes.
		// 1 count entry of 2,000 cells = 2,000 probes.
		// Total count probes = exactly 20,000.
		var entries []Entry
		for i := 0; i < 6; i++ {
			maxes := make([]uint64, len(allCells))
			for j := range maxes {
				maxes[j] = 100
			}
			entries = append(entries, Entry{
				Kind:     "count",
				Table:    "cards",
				Cells:    allCells,
				CountMax: maxes,
			})
		}
		tailCells := allCells[:2000]
		tailMaxes := make([]uint64, len(tailCells))
		for j := range tailMaxes {
			tailMaxes[j] = 100
		}
		entries = append(entries, Entry{
			Kind:     "count",
			Table:    "cards",
			Cells:    tailCells,
			CountMax: tailMaxes,
		})

		// Now add an unobserved property: this triggers the HLEN probe,
		// raising cellProbes from 20,000 to 20,001, which must return LIMIT.
		val := "test"
		entries = append(entries, Entry{
			Kind:  "prop",
			Table: "cards",
			Name:  "extra_prop",
			Value: &val,
		})

		step := Step{
			Epoch:   "0",
			Space:   sp,
			Entries: entries,
		}

		reply, err := m2.Step(ctx, step)
		ref := requireRefusal(t, err, "LIMIT")
		if reply.Status == "ok" {
			t.Fatalf("step over 20,000 cell probes succeeded: %+v", reply)
		}
		if ref.Detail.Budget != "cell_probes" {
			t.Fatalf("refusal budget = %s, want cell_probes", ref.Detail.Budget)
		}
		if ref.Detail.Limit != nil && *ref.Detail.Limit != 20000 {
			t.Fatalf("limit = %d, want 20000", *ref.Detail.Limit)
		}
	})
}

// TestPropertyContractR3MemDecimalHLENByteAccounting verifies that Mem accounts
// for decimal HLEN reply bytes in raw_fetched_bytes when probing table properties.
func TestPropertyContractR3MemDecimalHLENByteAccounting(t *testing.T) {
	t.Parallel()
	const space = "r3-mem-bytes:"
	ctx := context.Background()

	for _, tc := range []struct {
		name          string
		preseedProps  int
		wantByteDelta int // len(strconv.Itoa(preseedProps))
	}{
		{"zero_existing_properties", 0, 1}, // HLEN returns 0 -> len("0") = 1 byte
		{"nine_existing_properties", 9, 1}, // HLEN returns 9 -> len("9") = 1 byte
		{"ten_existing_properties", 10, 2}, // HLEN returns 10 -> len("10") = 2 bytes
		{"50_existing_properties", 50, 2},  // HLEN returns 50 -> len("50") = 2 bytes
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sp := fmt.Sprintf("%s%d:", space, tc.preseedProps)
			m := r1r2r3MemFixture(t, sp)

			// Preseed properties if requested
			for i := 0; i < tc.preseedProps; i++ {
				if err := m.SeedProperty(sp, "cards", "0", fmt.Sprintf("pre_%02d", i), "v"); err != nil {
					t.Fatalf("seed property: %v", err)
				}
			}

			// Add one new unobserved property
			propVal := "new_value"
			step := Step{
				Epoch: "0", Space: sp,
				Entries: []Entry{
					{Kind: "prop", Table: "cards", Name: "brand_new", Value: &propVal},
				},
			}

			reply, err := m.Step(ctx, step)
			if err != nil || reply.Status != "ok" {
				t.Fatalf("step failed: reply=%+v err=%v", reply, err)
			}

			var counters struct {
				CellProbes   int `json:"cell_probes"`
				FetchedBytes int `json:"raw_fetched_bytes"`
			}
			if err := json.Unmarshal(reply.Counters, &counters); err != nil {
				t.Fatalf("unmarshal counters: %v", err)
			}

			// In this step, only 1 property was added to cards.
			// HLEN probe should contribute exactly 1 cell probe.
			if counters.CellProbes != 1 {
				t.Fatalf("cell_probes = %d, want 1", counters.CellProbes)
			}
			// HLEN probe reply decimal bytes must equal len(strconv.Itoa(preseedProps)).
			if counters.FetchedBytes != tc.wantByteDelta {
				t.Fatalf("raw_fetched_bytes = %d, want %d (HLEN reply %q)", counters.FetchedBytes, tc.wantByteDelta, strconv.Itoa(tc.preseedProps))
			}
		})
	}
}

// TestPropertyContractR3MemSuccessorAccounting verifies that on clean advance:
// 1. An absent propguard charges 1 successor HLEN cell probe and 1 HGET field observation.
// 2. A subsequent new-name prop reuses that capacity without double-charging HLEN.
// 3. Ordinary nonadvance propguards do NOT charge successor HLEN.
func TestPropertyContractR3MemSuccessorAccounting(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("advance_absent_propguard_charges_successor_hlen", func(t *testing.T) {
		t.Parallel()
		const sp = "r3-succ-guard:"
		m := r1r2r3MemFixture(t, sp)

		op, intent := "adv-guard", "advance with absent propguard"
		step := Step{
			Epoch: "0", Space: sp,
			Op:     &op,
			Intent: &intent,
			Entries: []Entry{
				{Kind: "advance", AdvanceFrom: "0"},
				{Kind: "propguard", Table: "cards", Name: "absent_guard", Value: nil},
			},
		}

		reply, err := m.Step(ctx, step)
		if err != nil || reply.Status != "ok" || reply.EpochAfter != "1" {
			t.Fatalf("advance + propguard failed: reply=%+v err=%v", reply, err)
		}
		var counters struct {
			CellProbes        int `json:"cell_probes"`
			FieldObservations int `json:"field_observations"`
			FetchedBytes      int `json:"raw_fetched_bytes"`
		}
		if err := json.Unmarshal(reply.Counters, &counters); err != nil {
			t.Fatalf("unmarshal counters: %v", err)
		}
		// Successor HLEN probe must contribute 1 cell probe and 1 fetched byte (len("0") = 1).
		if counters.CellProbes != 1 {
			t.Fatalf("cell_probes = %d, want 1", counters.CellProbes)
		}
		// HGET for absent_guard contributes 1 field observation and 0 fetched bytes.
		if counters.FieldObservations != 1 {
			t.Fatalf("field_observations = %d, want 1", counters.FieldObservations)
		}
		if counters.FetchedBytes != 1 {
			t.Fatalf("raw_fetched_bytes = %d, want 1", counters.FetchedBytes)
		}
	})

	t.Run("advance_absent_propguard_plus_new_prop_no_double_charge", func(t *testing.T) {
		t.Parallel()
		const sp = "r3-succ-nodbl:"
		m := r1r2r3MemFixture(t, sp)

		val := "v"
		op, intent := "adv-guard-prop", "advance with absent propguard and prop"
		step := Step{
			Epoch: "0", Space: sp,
			Op:     &op,
			Intent: &intent,
			Entries: []Entry{
				{Kind: "advance", AdvanceFrom: "0"},
				{Kind: "propguard", Table: "cards", Name: "absent_guard", Value: nil},
				{Kind: "prop", Table: "cards", Name: "new_prop", Value: &val},
			},
		}

		reply, err := m.Step(ctx, step)
		if err != nil || reply.Status != "ok" || reply.EpochAfter != "1" {
			t.Fatalf("advance + propguard + prop failed: reply=%+v err=%v", reply, err)
		}
		var counters struct {
			CellProbes        int `json:"cell_probes"`
			FieldObservations int `json:"field_observations"`
			FetchedBytes      int `json:"raw_fetched_bytes"`
		}
		if err := json.Unmarshal(reply.Counters, &counters); err != nil {
			t.Fatalf("unmarshal counters: %v", err)
		}
		// Exactly 1 cell probe: successor HLEN is reused, not double charged.
		if counters.CellProbes != 1 {
			t.Fatalf("cell_probes = %d, want 1 (no double charge)", counters.CellProbes)
		}
		// Two field observations: absent_guard and new_prop.
		if counters.FieldObservations != 2 {
			t.Fatalf("field_observations = %d, want 2", counters.FieldObservations)
		}
		if counters.FetchedBytes != 1 {
			t.Fatalf("raw_fetched_bytes = %d, want 1", counters.FetchedBytes)
		}
	})

	t.Run("nonadvance_propguard_no_successor_hlen", func(t *testing.T) {
		t.Parallel()
		const sp = "r3-nonadv-guard:"
		m := r1r2r3MemFixture(t, sp)

		step := Step{
			Epoch: "0", Space: sp,
			Entries: []Entry{
				{Kind: "propguard", Table: "cards", Name: "absent_guard", Value: nil},
			},
		}

		reply, err := m.Step(ctx, step)
		if err != nil || reply.Status != "ok" {
			t.Fatalf("nonadvance propguard failed: reply=%+v err=%v", reply, err)
		}
		var counters struct {
			CellProbes        int `json:"cell_probes"`
			FieldObservations int `json:"field_observations"`
			FetchedBytes      int `json:"raw_fetched_bytes"`
		}
		if err := json.Unmarshal(reply.Counters, &counters); err != nil {
			t.Fatalf("unmarshal counters: %v", err)
		}
		// Nonadvance propguard does NOT charge successor HLEN: 0 cell probes.
		if counters.CellProbes != 0 {
			t.Fatalf("cell_probes = %d, want 0", counters.CellProbes)
		}
		if counters.FieldObservations != 1 {
			t.Fatalf("field_observations = %d, want 1", counters.FieldObservations)
		}
		if counters.FetchedBytes != 0 {
			t.Fatalf("raw_fetched_bytes = %d, want 0", counters.FetchedBytes)
		}
	})
}
