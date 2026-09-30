//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestMemWriteFetchedBytesBoundary(t *testing.T) {
	t.Parallel()

	// This is a Mem.Step accounting boundary, not a Redis/Lua parity fixture.
	// Every observed record contributes six fixed bytes here: epoch, revision,
	// score, row, column, and the one-byte selected field name. The values are
	// distributed evenly and stay below MaxFieldValueBytes individually.
	const members = 128
	fixedBytesPerMember := len("0") + len("1") + len("1") + len("r") + len("c") + len("f")
	baseValueBytes := MaxFetchedBytes/members - fixedBytesPerMember
	if MaxFetchedBytes%members != 0 || baseValueBytes > MaxFieldValueBytes {
		t.Fatalf("fixture cannot reach fetched-byte cap with %d members: value-bytes=%d", members, baseValueBytes)
	}

	for _, tc := range []struct {
		name       string
		delta      int
		wantRefuse bool
	}{{name: "limit_minus_one", delta: -1}, {name: "limit", delta: 0}, {name: "limit_plus_one", delta: 1, wantRefuse: true}} {
		t.Run(tc.name, func(t *testing.T) {
			m := writeObservationMem(t, "write-fetched:")
			ids := make([]string, members)
			for i := range ids {
				id := fmt.Sprintf("id-%03d", i)
				ids[i] = id
				valueBytes := baseValueBytes
				if i == len(ids)-1 {
					valueBytes += tc.delta
				}
				if err := m.SeedMember("write-fetched:", "work", "0", id, MemRecord{
					Epoch: "0", Revision: "1", Row: "r", Column: "c", Score: "1",
					Fields: map[string]string{"f": strings.Repeat("x", valueBytes)},
				}); err != nil {
					t.Fatalf("seed %s: %v", id, err)
				}
			}
			step := Step{Epoch: "0", Space: "write-fetched:", Entries: []Entry{{
				Kind: "guard", Table: "work", From: "r:c", IDs: ids, BeforeFields: []string{"f"},
			}}}

			var before MemSnapshot
			if tc.wantRefuse {
				var snapshotErr error
				before, snapshotErr = m.Snapshot("write-fetched:")
				if snapshotErr != nil {
					t.Fatalf("snapshot before over-cap step: %v", snapshotErr)
				}
			}
			reply, err := m.Step(context.Background(), step)
			if tc.wantRefuse {
				var refusal *Refusal
				if !errors.As(err, &refusal) || refusal.Code != "LIMIT" ||
					refusal.Detail.Budget != "raw_fetched_bytes" {
					t.Fatalf("over-cap observation: reply=%+v err=%v", reply, err)
				}
				// These detail values are optional in the published refusal schema.
				// Check them when present without making them a new wire requirement.
				if refusal.Detail.Limit != nil && *refusal.Detail.Limit != MaxFetchedBytes {
					t.Fatalf("raw_fetched_bytes limit detail=%d, want %d", *refusal.Detail.Limit, MaxFetchedBytes)
				}
				if refusal.Detail.Actual != nil && *refusal.Detail.Actual != MaxFetchedBytes+1 {
					t.Fatalf("raw_fetched_bytes actual detail=%d, want %d", *refusal.Detail.Actual, MaxFetchedBytes+1)
				}
				if reply.Status != "" {
					t.Fatalf("refusal exposed reply: %+v", reply)
				}
				after, snapshotErr := m.Snapshot("write-fetched:")
				if snapshotErr != nil {
					t.Fatalf("snapshot after over-cap step: %v", snapshotErr)
				}
				if !reflect.DeepEqual(before, after) {
					t.Fatal("raw fetched-byte refusal changed the full Mem snapshot")
				}
				return
			}
			if err != nil || reply.Status != "ok" || reply.Guarded != members || reply.Changed != 0 {
				t.Fatalf("%s write observation: reply=%+v err=%v", tc.name, reply, err)
			}
			var counters struct {
				FetchedBytes int `json:"raw_fetched_bytes"`
			}
			if err := json.Unmarshal(reply.Counters, &counters); err != nil {
				t.Fatalf("decode counters %s: %v", tc.name, err)
			}
			want := MaxFetchedBytes + tc.delta
			if counters.FetchedBytes != want {
				t.Fatalf("%s raw_fetched_bytes=%d, want %d", tc.name, counters.FetchedBytes, want)
			}
		})
	}
}

func TestMemWriteFieldObservationMaximumIsReachable(t *testing.T) {
	t.Parallel()

	// Through one direct Mem.Step, the §6 field-observation ceiling is exactly
	// attainable: 2,000 mutation
	// candidates plus 4,000 additional guard-only members, each with the maximum
	// 128-field projection. It cannot be exceeded through Mem.Step because the
	// earlier candidate, guard, and per-member projection ceilings multiply to
	// exactly 768,000; a fabricated 768,001-observation Mem.Step request is
	// impossible. This does not bound multiple trusted S.before calls made by a
	// composed Lua preplan, which must be tested through that separate path.
	const guards = MaxGuardMembers
	const candidates = MaxMemberCandidates
	const members = guards + candidates
	fields := make([]string, MaxFieldsPerMember)
	stored := make(map[string]string, MaxFieldsPerMember)
	for i := range fields {
		fields[i] = fmt.Sprintf("f%03d", i)
		stored[fields[i]] = ""
	}
	m := writeObservationMem(t, "write-fields:")
	ids := make([]string, members)
	for i := range ids {
		id := fmt.Sprintf("id-%04d", i)
		ids[i] = id
		if err := m.SeedMember("write-fields:", "work", "0", id, MemRecord{
			Epoch: "0", Revision: "1", Row: "r", Column: "c", Score: "1", Fields: stored,
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	step := Step{Epoch: "0", Space: "write-fields:", Entries: []Entry{
		{Kind: "guard", Table: "work", From: "r:c", IDs: ids[:2000], BeforeFields: fields},
		{Kind: "guard", Table: "work", From: "r:c", IDs: ids[2000:guards], BeforeFields: fields},
		{Kind: "move", Table: "work", From: "r:c", To: "r:c", IDs: ids[guards:], BeforeFields: fields},
	}}

	reply, err := m.Step(context.Background(), step)
	if err != nil || reply.Status != "ok" || reply.Guarded != guards || reply.Changed != 0 {
		t.Fatalf("maximum write field observations: reply=%+v err=%v", reply, err)
	}
	var counters struct {
		FieldObservations int `json:"field_observations"`
	}
	if err := json.Unmarshal(reply.Counters, &counters); err != nil {
		t.Fatalf("decode counters: %v", err)
	}
	want := (MaxGuardMembers + MaxMemberCandidates) * MaxFieldsPerMember
	if want != 768000 || counters.FieldObservations != want {
		t.Fatalf("field_observations=%d, want exact reachable maximum %d", counters.FieldObservations, want)
	}
}

func writeObservationMem(t *testing.T, space string) *Mem {
	t.Helper()
	m := NewMem()
	if err := m.DefineTable(space, "work", TableDefinition{
		Columns: []string{"c"}, MemberPrefix: space + "member:",
		EpochKey: space + "sprint:epoch", EpochField: "n",
	}); err != nil {
		t.Fatalf("define table: %v", err)
	}
	if err := m.SeedRow(space, "work", "0", "r", "0"); err != nil {
		t.Fatalf("seed row: %v", err)
	}
	return m
}
