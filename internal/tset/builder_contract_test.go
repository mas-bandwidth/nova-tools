package tset

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestBuildStepSplitsFatEntryWithAlignedOrderAndLeavesInputUntouched(t *testing.T) {
	t.Parallel()
	entry := builderMoveEntry(24, 48<<10)
	entryJSON, err := json.Marshal(entry)
	if err != nil || len(entryJSON) <= MaxLineBytes {
		t.Fatalf("fat fixture must exceed one line: encoded=%d error=%v", len(entryJSON), err)
	}
	step := Step{
		Epoch: "7", Space: "s", Entries: []Entry{
			entry,
			{Kind: "rows", Table: "cards", Add: []string{"later"}},
		},
		Notes: []Note{{Line: NoteLine{Kind: "note", Meta: json.RawMessage(`{"source":"fixture"}`)}, About: []string{"primary"}}},
	}
	before, err := json.Marshal(step)
	if err != nil {
		t.Fatalf("marshal input snapshot: %v", err)
	}
	cut, err := BuildStep(step)
	if err != nil {
		t.Fatalf("BuildStep fat member entry: %v", err)
	}
	after, err := json.Marshal(step)
	if err != nil {
		t.Fatalf("marshal input after BuildStep: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("BuildStep changed the caller's request while cutting the entry")
	}
	if len(cut.Entries) < 3 {
		t.Fatalf("fat line was not cut into bounded member entries before the trailing rows event: got %d entries", len(cut.Entries))
	}
	if cut.Entries[len(cut.Entries)-1].Kind != "rows" || cut.Entries[len(cut.Entries)-1].Add[0] != "later" {
		t.Fatalf("entry order changed around the split: final entry=%+v", cut.Entries[len(cut.Entries)-1])
	}
	position := 0
	for partIndex, part := range cut.Entries[:len(cut.Entries)-1] {
		partJSON, err := json.Marshal(part)
		if err != nil || len(partJSON) >= MaxLineBytes {
			t.Fatalf("split part %d still exceeds one line: encoded=%d error=%v", partIndex, len(partJSON), err)
		}
		if part.Kind != "move" || part.Table != entry.Table || part.From != entry.From || part.To != entry.To {
			t.Fatalf("split part %d changed member operation: %+v", partIndex, part)
		}
		if len(part.IDs) == 0 || len(part.IDs) >= len(entry.IDs) {
			t.Fatalf("split part %d has unexpected size %d", partIndex, len(part.IDs))
		}
		for local, id := range part.IDs {
			want := position + local
			if want >= len(entry.IDs) || id != entry.IDs[want] || part.Scores[local] != entry.Scores[want] || part.Revs[local] != entry.Revs[want] || part.About[local] != entry.About[want] || !reflect.DeepEqual(part.Each[local], entry.Each[want]) {
				t.Fatalf("part %d member %d lost aligned input data: got id=%q score=%q rev=%q about=%q", partIndex, local, id, part.Scores[local], part.Revs[local], part.About[local])
			}
		}
		position += len(part.IDs)
	}
	if position != len(entry.IDs) {
		t.Fatalf("split entries cover %d members; want all %d", position, len(entry.IDs))
	}
	if cut.Epoch != step.Epoch || cut.Space != step.Space || !reflect.DeepEqual(cut.Notes, step.Notes) {
		t.Fatalf("step envelope or notes changed during a member-only cut: got %+v", cut)
	}
}

func TestBuildStepAcceptsSingleMaximumFieldAndMemberCapacity(t *testing.T) {
	t.Parallel()
	maximumField := strings.Repeat("v", MaxFieldValueBytes)
	single := Step{Epoch: "0", Space: "s", Entries: []Entry{builderCreateEntry([]string{"one"}, maximumField)}}
	got, err := BuildStep(single)
	if err != nil {
		t.Fatalf("BuildStep rejected one storable %d-byte field: %v", MaxFieldValueBytes, err)
	}
	if len(got.Entries) != 1 || got.Entries[0].Set["payload"] != maximumField {
		t.Fatalf("maximum field did not survive the builder: entries=%d", len(got.Entries))
	}

	ids := builderIDs(MaxMemberCandidates)
	capacity := Step{Epoch: "0", Space: "s", Entries: []Entry{builderCreateEntry(ids, "x")}}
	got, err = BuildStep(capacity)
	if err != nil {
		t.Fatalf("BuildStep rejected exactly %d member candidates: %v", MaxMemberCandidates, err)
	}
	if len(got.Entries) != 1 || len(got.Entries[0].IDs) != MaxMemberCandidates {
		t.Fatalf("member capacity was changed: entries=%d members=%d", len(got.Entries), len(got.Entries[0].IDs))
	}
	tooMany := append([]string(nil), ids...)
	tooMany = append(tooMany, "member-over-cap")
	if _, err := BuildStep(Step{Epoch: "0", Space: "s", Entries: []Entry{builderCreateEntry(tooMany, "x")}}); !builderRefusalIs(err, "LIMIT") {
		t.Fatalf("BuildStep with %d candidates returned %v; want LIMIT", len(tooMany), err)
	}
}

func TestBuildStepChargesExpandedSharedFieldsAndRequestBytes(t *testing.T) {
	t.Parallel()
	ids := builderIDs(MaxMemberCandidates)
	within := Step{Epoch: "0", Space: "s", Entries: []Entry{builderCreateEntry(ids, strings.Repeat("a", 3800))}}
	got, err := BuildStep(within)
	if err != nil {
		t.Fatalf("BuildStep rejected shared-field expansion within the 8 MiB budget: %v", err)
	}
	if len(got.Entries) != 1 || len(got.Entries[0].IDs) != MaxMemberCandidates {
		t.Fatalf("valid shared field was split or lost: entries=%d members=%d", len(got.Entries), len(got.Entries[0].IDs))
	}
	over := Step{Epoch: "0", Space: "s", Entries: []Entry{builderCreateEntry(ids, strings.Repeat("b", 3900))}}
	if _, err := BuildStep(over); !builderRefusalIs(err, "LIMIT") {
		t.Fatalf("BuildStep failed to charge repeated shared-field writes above 8 MiB: %v", err)
	}

	request := Step{Epoch: "0", Space: "s", Entries: []Entry{}, Notes: make([]Note, 5)}
	for i := range request.Notes {
		meta := json.RawMessage(`{"payload":"` + strings.Repeat("x", 900<<10) + `"}`)
		request.Notes[i] = Note{
			Line:  NoteLine{Kind: "note", Meta: meta},
			About: []string{fmt.Sprintf("primary-%d", i)},
		}
		noteLine, marshalErr := json.Marshal(request.Notes[i])
		if marshalErr != nil || len(noteLine)+1024 >= MaxLineBytes {
			t.Fatalf("note %d must fit under the 1 MiB line cap: encoded=%d error=%v", i, len(noteLine), marshalErr)
		}
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatalf("marshal request-boundary fixture: %v", err)
	}
	if len(encoded) <= MaxWriteRequestBytes || len(encoded) >= MaxPlannedArgvBytes {
		t.Fatalf("fixture does not isolate the write-request cap: encoded=%d request-cap=%d argv-cap=%d", len(encoded), MaxWriteRequestBytes, MaxPlannedArgvBytes)
	}
	if _, err := BuildStep(request); !builderRefusalIs(err, "LIMIT") {
		t.Fatalf("BuildStep accepted a %d-byte request beyond the 4 MiB request cap: %v", len(encoded), err)
	}
}

func TestBuildStepEnforces256EntryLimitAndKeepsBoundary(t *testing.T) {
	t.Parallel()
	makeEntries := func(count int) []Entry {
		entries := make([]Entry, count)
		for i := range entries {
			entries[i] = builderCreateEntry([]string{fmt.Sprintf("entry-%03d", i)}, "v")
		}
		return entries
	}
	step := Step{Epoch: "0", Space: "s", Entries: makeEntries(MaxEntries)}
	got, err := BuildStep(step)
	if err != nil {
		t.Fatalf("BuildStep rejected exactly %d entries: %v", MaxEntries, err)
	}
	if len(got.Entries) != MaxEntries || got.Entries[0].IDs[0] != "entry-000" || got.Entries[MaxEntries-1].IDs[0] != "entry-255" {
		t.Fatalf("entry boundary or order changed: len=%d first=%v last=%v", len(got.Entries), got.Entries[0].IDs, got.Entries[len(got.Entries)-1].IDs)
	}
	tooMany := Step{Epoch: "0", Space: "s", Entries: makeEntries(MaxEntries + 1)}
	if _, err := BuildStep(tooMany); !builderRefusalIs(err, "LIMIT") {
		t.Fatalf("BuildStep accepted %d entries in one atomic operation: %v", len(tooMany.Entries), err)
	}
}

func TestBuildStepCannotSplitNamedAtomicOperation(t *testing.T) {
	t.Parallel()
	step := Step{Epoch: "0", Space: "s", Entries: []Entry{builderMoveEntry(24, 48<<10)}}
	op, intent := "operation-7", `{"part":"fixed","verb":"move"}`
	step.Op, step.Intent = &op, &intent
	before, err := json.Marshal(step)
	if err != nil {
		t.Fatalf("marshal input snapshot: %v", err)
	}
	if _, err := BuildStep(step); !builderRefusalIs(err, "LIMIT") {
		t.Fatalf("BuildStep changed an already named atomic operation: %v", err)
	}
	after, err := json.Marshal(step)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed cut mutated named operation: marshal error=%v", err)
	}
}

func TestBuildIndependentStepsFixesPartIdentityAcrossResume(t *testing.T) {
	t.Parallel()
	base := Step{Epoch: "9", Space: "s", Entries: []Entry{builderCreateEntry(builderIDs(MaxMemberCandidates+1), "v")}}
	before, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal input snapshot: %v", err)
	}
	var calls []int
	identity := func(part int) (string, string, string) {
		calls = append(calls, part)
		return fmt.Sprintf("op-%02d", part), fmt.Sprintf(`{"part":%d,"verb":"bulk-create"}`, part), fmt.Sprintf("result-%02d", part)
	}
	parts, err := BuildIndependentSteps(base, identity)
	if err != nil {
		t.Fatalf("BuildIndependentSteps: %v", err)
	}
	if len(parts) != 2 || !reflect.DeepEqual(calls, []int{0, 1}) {
		t.Fatalf("split produced %d steps with identity calls %v; want two ordered parts", len(parts), calls)
	}
	if len(parts[0].Entries) != 1 || len(parts[0].Entries[0].IDs) != MaxMemberCandidates || len(parts[1].Entries) != 1 || len(parts[1].Entries[0].IDs) != 1 {
		t.Fatalf("member boundary was not preserved across independent parts: first=%+v second=%+v", parts[0].Entries, parts[1].Entries)
	}
	if parts[0].Entries[0].IDs[0] != "member-0000" || parts[0].Entries[0].IDs[MaxMemberCandidates-1] != "member-1999" || parts[1].Entries[0].IDs[0] != "member-2000" {
		t.Fatalf("independent part order shifted across the member boundary: first=%q..%q second=%q", parts[0].Entries[0].IDs[0], parts[0].Entries[0].IDs[MaxMemberCandidates-1], parts[1].Entries[0].IDs[0])
	}
	for i, part := range parts {
		if part.Op == nil || *part.Op != fmt.Sprintf("op-%02d", i) || part.Intent == nil || *part.Intent != fmt.Sprintf(`{"part":%d,"verb":"bulk-create"}`, i) || part.Result != fmt.Sprintf("result-%02d", i) {
			t.Fatalf("part %d lost its fixed identity: %+v", i, part)
		}
	}
	after, err := json.Marshal(base)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("BuildIndependentSteps changed caller input: marshal error=%v", err)
	}

	// A resumed command keeps its original part index and exact wire bytes.
	pendingWire, err := EncodeStep(parts[1])
	if err != nil {
		t.Fatalf("encode pending part: %v", err)
	}
	pending, err := DecodeStep(pendingWire)
	if err != nil {
		t.Fatalf("decode retained pending part: %v", err)
	}
	rebuilt, err := BuildStep(pending)
	if err != nil {
		t.Fatalf("BuildStep retained pending atomic part: %v", err)
	}
	rebuiltWire, err := EncodeStep(rebuilt)
	if err != nil || !bytes.Equal(pendingWire, rebuiltWire) {
		t.Fatalf("retry changed retained bytes or part identity: encode error=%v", err)
	}
	if *rebuilt.Op != *parts[1].Op || *rebuilt.Intent != *parts[1].Intent || rebuilt.Result != parts[1].Result {
		t.Fatalf("resume changed part identity: got %+v want %+v", rebuilt, parts[1])
	}

	partial := Step{Epoch: base.Epoch, Space: base.Space, Entries: []Entry{parts[1].Entries[0]}}
	resumeCalls := 0
	resumed, err := BuildIndependentStepsFrom(partial, 1, func(part int) (string, string, string) {
		resumeCalls++
		return fmt.Sprintf("op-%02d", part), fmt.Sprintf(`{"part":%d,"verb":"bulk-create"}`, part), fmt.Sprintf("result-%02d", part)
	})
	if err != nil {
		t.Fatalf("BuildIndependentStepsFrom resumed subset: %v", err)
	}
	if resumeCalls != 1 || len(resumed) != 1 || *resumed[0].Op != *parts[1].Op || *resumed[0].Intent != *parts[1].Intent || resumed[0].Result != parts[1].Result {
		t.Fatalf("resumed subset did not retain original part 1 identity: calls=%d resumed=%+v original=%+v", resumeCalls, resumed, parts[1])
	}
}

func TestBuildIndependentStepsRejectsDuplicateOperationIdentityAtomically(t *testing.T) {
	t.Parallel()
	base := Step{Epoch: "9", Space: "s", Entries: []Entry{builderCreateEntry(builderIDs(MaxMemberCandidates+1), "v")}}

	for _, tc := range []struct {
		name   string
		intent func(int) string
	}{
		{name: "same operation and intent", intent: func(int) string { return `{"part":0}` }},
		{name: "same operation with different intent", intent: func(part int) string { return fmt.Sprintf(`{"part":%d}`, part) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			parts, err := BuildIndependentSteps(base, func(part int) (string, string, string) {
				calls++
				return "duplicate-op", tc.intent(part), fmt.Sprintf("result-%d", part)
			})
			if !builderRefusalIs(err, "REQUEST") {
				t.Fatalf("BuildIndependentSteps returned %d parts and %v for duplicate operation identity; want REQUEST refusal", len(parts), err)
			}
			if parts != nil {
				t.Fatalf("duplicate operation identity returned partial output: %+v", parts)
			}
			if calls != 2 {
				t.Fatalf("identity callback called %d times; want both generated identities checked before dispatch", calls)
			}
		})
	}
}

func builderCreateEntry(ids []string, value string) Entry {
	scores := make([]string, len(ids))
	about := make([]string, len(ids))
	for i, id := range ids {
		scores[i] = "1"
		about[i] = "primary-" + id
	}
	return Entry{Kind: "create", Table: "cards", To: "in:ready", IDs: append([]string(nil), ids...), Scores: scores, Set: map[string]string{"payload": value}, About: about}
}

func builderMoveEntry(count, valueBytes int) Entry {
	e := Entry{
		Kind: "move", Table: "cards", From: "todo:primary", To: "done:primary",
		IDs: make([]string, count), Scores: make([]string, count), Revs: make([]Decimal, count),
		Each: make([]map[string]string, count), About: make([]string, count),
	}
	for i := 0; i < count; i++ {
		e.IDs[i] = fmt.Sprintf("item-%02d", i)
		e.Scores[i] = strconv.Itoa(i + 1)
		e.Revs[i] = Decimal(strconv.Itoa(i + 1))
		e.Each[i] = map[string]string{"payload": strings.Repeat(string(rune('a'+i%26)), valueBytes)}
		e.About[i] = fmt.Sprintf("primary-%02d", i)
	}
	return e
}

func builderIDs(count int) []string {
	ids := make([]string, count)
	for i := range ids {
		ids[i] = fmt.Sprintf("member-%04d", i)
	}
	return ids
}

func builderRefusalIs(err error, code string) bool {
	var refusal *Refusal
	return errors.As(err, &refusal) && refusal.Code == code
}
