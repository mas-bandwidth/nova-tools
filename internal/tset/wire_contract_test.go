package tset

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestWireDuplicateJSONUsesLastValue(t *testing.T) {
	t.Parallel()
	step := Step{Epoch: "0", Space: "chosen", Entries: []Entry{{
		Kind: "create", Table: "cards", To: "in:ready", IDs: []string{"c1"},
		Scores: []string{"1"}, Set: map[string]string{"title": "hello"}, About: []string{"c1"},
	}}}
	raw, err := EncodeStep(step)
	if err != nil {
		t.Fatalf("EncodeStep: %v", err)
	}
	needle := []byte(`"space":"chosen"`)
	if !bytes.Contains(raw, needle) {
		t.Fatalf("encoded request has no space field: %s", raw)
	}
	raw = bytes.Replace(raw, needle, []byte(`"space":"earlier","space":"chosen"`), 1)
	field := []byte(`"title":"hello"`)
	if !bytes.Contains(raw, field) {
		t.Fatalf("encoded entry has no title field: %s", raw)
	}
	raw = bytes.Replace(raw, field, []byte(`"title":"earlier","title":"last"`), 1)
	got, err := DecodeStep(raw)
	if err != nil {
		t.Fatalf("DecodeStep duplicate object names: %v", err)
	}
	if got.Space != "chosen" {
		t.Fatalf("last duplicate did not win: Space=%q", got.Space)
	}
	if len(got.Entries) != 1 || got.Entries[0].Set["title"] != "last" {
		t.Fatalf("last duplicate map value did not win: entries=%+v", got.Entries)
	}
}

func TestWireMetaKeepsNumbersAndNull(t *testing.T) {
	t.Parallel()
	step := Step{
		Epoch: "0", Space: "s",
		Entries: []Entry{{
			Kind: "create", Table: "cards", To: "in:ready", IDs: []string{"c1"},
			Scores: []string{"1"}, Set: map[string]string{"title": "hello"},
			Meta: []byte(`{"count":9007199254740993,"unset":null}`),
		}},
	}
	raw, err := EncodeStep(step)
	if err != nil {
		t.Fatalf("EncodeStep with object meta: %v", err)
	}
	got, err := DecodeStep(raw)
	if err != nil {
		t.Fatalf("DecodeStep with object meta: %v", err)
	}
	if len(got.Entries) != 1 || !bytes.Equal(bytes.TrimSpace(got.Entries[0].Meta), step.Entries[0].Meta) {
		t.Fatalf("meta changed across wire round trip: got %s want %s", got.Entries[0].Meta, step.Entries[0].Meta)
	}
	step.Entries[0].Meta = []byte(`null`)
	if _, err := EncodeStep(step); err == nil {
		t.Fatal("accepted null in place of the required meta object")
	}
}

func TestWireSetAndEachRequireStringValues(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`{"epoch":"0","space":"s","entries":[{"kind":"create","t":"cards","to":"in:ready","ids":["c1"],"scores":["1"],"set":{"payload":null},"each":[{}],"about":["c1"]}]}`,
		`{"epoch":"0","space":"s","entries":[{"kind":"create","t":"cards","to":"in:ready","ids":["c1"],"scores":["1"],"set":{},"each":[{"payload":null}],"about":["c1"]}]}`,
	} {
		if _, err := DecodeStep([]byte(raw)); err == nil {
			t.Errorf("DecodeStep accepted null field value in %s", raw)
		} else {
			var refusal *Refusal
			if !errors.As(err, &refusal) || refusal.Code != "REQUEST" {
				t.Errorf("DecodeStep null field value error = %v; want REQUEST", err)
			}
		}
	}
	emptyString := []byte(`{"epoch":"0","space":"s","entries":[{"kind":"create","t":"cards","to":"in:ready","ids":["c1"],"scores":["1"],"set":{"payload":""},"each":[{"payload":""}],"about":["c1"]}]}`)
	if _, err := DecodeStep(emptyString); err != nil {
		t.Fatalf("DecodeStep rejected empty string field values: %v", err)
	}
}

func TestWireNotesRequireStableOperationIdentity(t *testing.T) {
	t.Parallel()
	note := Note{Line: NoteLine{Kind: "note", Meta: json.RawMessage(`{"source":"test"}`)}, About: []string{"primary"}}
	validOp, validIntent := "note-op", `{"part":"stable"}`
	valid := Step{Epoch: "0", Space: "s", Entries: []Entry{}, Op: &validOp, Intent: &validIntent, Notes: []Note{note}}
	raw, err := EncodeStep(valid)
	if err != nil {
		t.Fatalf("EncodeStep named note request: %v", err)
	}
	if _, err := DecodeStep(raw); err != nil {
		t.Fatalf("DecodeStep named note request: %v", err)
	}

	unnamed := Step{Epoch: "0", Space: "s", Entries: []Entry{}, Notes: []Note{note}}
	if _, err := EncodeStep(unnamed); !requestRefusalIs(err) {
		t.Errorf("EncodeStep note without identity = %v; want REQUEST", err)
	}
	if _, err := DecodeStep([]byte(`{"epoch":"0","space":"s","entries":[],"notes":[{"line":{"kind":"note","meta":{"source":"test"}},"about":["primary"]}]}`)); !requestRefusalIs(err) {
		t.Errorf("DecodeStep raw note without identity = %v; want REQUEST", err)
	}
}

func TestWireJSONDepthLimit(t *testing.T) {
	t.Parallel()
	step := Step{Epoch: "0", Space: "s", Entries: []Entry{{
		Kind: "create", Table: "cards", To: "in:ready", IDs: []string{"c1"},
		Scores: []string{"1"}, Set: map[string]string{"title": "hello"},
		About: []string{"c1"}, Meta: []byte(`{"x":0}`),
	}}}
	raw, err := EncodeStep(step)
	if err != nil {
		t.Fatalf("EncodeStep: %v", err)
	}
	// The top-level step, entries array, and entry object already contribute
	// three levels around meta. Verify the exact whole-request depth boundary.
	metaDepth := MaxJSONDepth - 3
	meta := strings.Repeat(`{"x":`, metaDepth) + `0` + strings.Repeat(`}`, metaDepth)
	needle := []byte(`"meta":{"x":0}`)
	if !bytes.Contains(raw, needle) {
		t.Fatalf("encoded request has no entry meta field: %s", raw)
	}
	belowMeta := strings.Repeat(`{"x":`, metaDepth-1) + `0` + strings.Repeat(`}`, metaDepth-1)
	belowLimit := bytes.Replace(raw, needle, append([]byte(`"meta":`), []byte(belowMeta)...), 1)
	if got := jsonDepth(belowLimit); got != MaxJSONDepth-1 {
		t.Fatalf("below-boundary fixture depth=%d; want %d", got, MaxJSONDepth-1)
	}
	if _, err := DecodeStep(belowLimit); err != nil {
		t.Fatalf("DecodeStep refused JSON depth %d below limit: %v", MaxJSONDepth-1, err)
	}
	atLimit := bytes.Replace(raw, needle, append([]byte(`"meta":`), []byte(meta)...), 1)
	if got := jsonDepth(atLimit); got != MaxJSONDepth {
		t.Fatalf("boundary fixture depth=%d; want %d", got, MaxJSONDepth)
	}
	if _, err := DecodeStep(atLimit); err != nil {
		t.Fatalf("DecodeStep refused exact JSON depth %d: %v", MaxJSONDepth, err)
	}
	tooDeepMeta := strings.Repeat(`{"x":`, metaDepth+1) + `0` + strings.Repeat(`}`, metaDepth+1)
	tooDeep := bytes.Replace(raw, needle, append([]byte(`"meta":`), []byte(tooDeepMeta)...), 1)
	if got := jsonDepth(tooDeep); got != MaxJSONDepth+1 {
		t.Fatalf("over-boundary fixture depth=%d; want %d", got, MaxJSONDepth+1)
	}
	if _, err := DecodeStep(tooDeep); err == nil {
		t.Fatalf("accepted JSON deeper than %d levels", MaxJSONDepth)
	}
}

func TestWireReadQueryLimitAndRequestBytes(t *testing.T) {
	t.Parallel()
	queries := make([]ReadQuery, MaxQueries)
	for i := range queries {
		queries[i] = ReadQuery{Kind: "last"}
	}
	plan := ReadPlan{Epoch: "0", Space: "s", Queries: queries}
	if _, err := EncodeReadPlan(plan); err != nil {
		t.Fatalf("accepted query limit %d was refused: %v", MaxQueries, err)
	}
	plan.Queries = plan.Queries[:MaxQueries-1]
	if _, err := EncodeReadPlan(plan); err != nil {
		t.Fatalf("accepted query count %d below the limit was refused: %v", len(plan.Queries), err)
	}
	plan.Queries = append(plan.Queries, ReadQuery{Kind: "last"})
	plan.Queries = append(plan.Queries, ReadQuery{Kind: "last"})
	if _, err := EncodeReadPlan(plan); err == nil {
		t.Fatalf("accepted %d read queries; limit is %d", len(plan.Queries), MaxQueries)
	} else {
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != "LIMIT" {
			t.Fatalf("%d queries returned %v; want LIMIT", len(plan.Queries), err)
		}
	}

	valid, err := EncodeReadPlan(ReadPlan{Epoch: "0", Space: "s", Queries: []ReadQuery{{Kind: "last"}}})
	if err != nil {
		t.Fatalf("EncodeReadPlan minimal request: %v", err)
	}
	if len(valid) > MaxReadRequestBytes {
		t.Fatalf("minimal request unexpectedly exceeds byte limit: %d", len(valid))
	}
	// Trailing JSON whitespace is legal and counts toward the encoded request
	// ceiling. Check the exact boundary and the first byte over it.
	atLimit := append(append([]byte(nil), valid...), bytes.Repeat([]byte{' '}, MaxReadRequestBytes-len(valid))...)
	underLimit := atLimit[:len(atLimit)-1]
	if _, err := DecodeReadPlan(underLimit); err != nil {
		t.Fatalf("refused request at %d bytes below the limit: %v", len(underLimit), err)
	}
	if _, err := DecodeReadPlan(atLimit); err != nil {
		t.Fatalf("accepted request at %d-byte boundary was refused: %v", MaxReadRequestBytes, err)
	}
	over := append(atLimit, ' ')
	if _, err := DecodeReadPlan(over); err == nil {
		t.Fatalf("accepted %d-byte request; limit is %d", len(over), MaxReadRequestBytes)
	}
}

func TestWireUint64DecimalAndIntentRoundTrip(t *testing.T) {
	t.Parallel()
	const maximum = "18446744073709551615"
	intent := "{\"verb\":\"move\",\"part\":\"stable-17\"}"
	op := "op-17"
	step := Step{Epoch: Decimal(maximum), Space: "s", Op: &op, Intent: &intent, Entries: []Entry{}}
	raw, err := EncodeStep(step)
	if err != nil {
		t.Fatalf("EncodeStep maximum uint64 epoch: %v", err)
	}
	got, err := DecodeStep(raw)
	if err != nil {
		t.Fatalf("DecodeStep maximum uint64 epoch: %v", err)
	}
	if got.Epoch != Decimal(maximum) || got.Intent == nil || *got.Intent != intent {
		t.Fatalf("exact wire identity changed: epoch=%q intent=%v", got.Epoch, got.Intent)
	}
	for _, bad := range []string{"", "00", "+1", " 1", "18446744073709551616"} {
		badRaw := bytes.Replace(raw, []byte(`"epoch":"`+maximum+`"`), []byte(`"epoch":"`+bad+`"`), 1)
		if _, err := DecodeStep(badRaw); err == nil {
			t.Errorf("accepted non-canonical or out-of-range uint64 epoch %q", bad)
		}
	}
}

func TestWireAcceptsExactIntegralJSONNumberLexemes(t *testing.T) {
	t.Parallel()
	for _, literal := range []string{"1.0", "1e0"} {
		stepRaw := []byte(`{"epoch":"0","space":"s","entries":[{"kind":"count","t":"cards","cells":["r:c"],"max":[` + literal + `]}]}`)
		step, err := DecodeStep(stepRaw)
		if err != nil {
			t.Errorf("DecodeStep rejected exact integral count max %s: %v", literal, err)
		} else if len(step.Entries) != 1 || !reflect.DeepEqual(step.Entries[0].CountMax, []uint64{1}) {
			t.Errorf("DecodeStep count max %s = %+v; want [1]", literal, step.Entries)
		}

		readRaw := []byte(`{"epoch":"0","space":"s","queries":[{"kind":"range","t":"cards","cell":"r:c","min":"-inf","max":"+inf","limit":` + literal + `}]}`)
		plan, err := DecodeReadPlan(readRaw)
		if err != nil {
			t.Errorf("DecodeReadPlan rejected exact integral limit %s: %v", literal, err)
		} else if len(plan.Queries) != 1 || plan.Queries[0].Limit != 1 {
			t.Errorf("DecodeReadPlan limit %s = %+v; want 1", literal, plan.Queries)
		}
	}
	for _, tc := range []struct {
		literal string
		want    uint64
	}{
		{literal: "0e1000000", want: 0},
		{literal: "-0.0", want: 0},
		{literal: "100e-2", want: 1},
	} {
		stepRaw := []byte(`{"epoch":"0","space":"s","entries":[{"kind":"count","t":"cards","cells":["r:c"],"max":[` + tc.literal + `]}]}`)
		step, err := DecodeStep(stepRaw)
		if err != nil {
			t.Errorf("DecodeStep rejected exact integral count max %s: %v", tc.literal, err)
		} else if len(step.Entries) != 1 || !reflect.DeepEqual(step.Entries[0].CountMax, []uint64{tc.want}) {
			t.Errorf("DecodeStep count max %s = %+v; want [%d]", tc.literal, step.Entries, tc.want)
		}
	}
	readOne := []byte(`{"epoch":"0","space":"s","queries":[{"kind":"range","t":"cards","cell":"r:c","min":"-inf","max":"+inf","limit":100e-2}]}`)
	if plan, err := DecodeReadPlan(readOne); err != nil || len(plan.Queries) != 1 || plan.Queries[0].Limit != 1 {
		t.Errorf("DecodeReadPlan exact 100e-2 limit = %+v, %v; want limit 1", plan.Queries, err)
	}
	for _, literal := range []string{"1.5", "1.00000000000000001"} {
		stepFraction := []byte(`{"epoch":"0","space":"s","entries":[{"kind":"count","t":"cards","cells":["r:c"],"max":[` + literal + `]}]}`)
		if _, err := DecodeStep(stepFraction); err == nil {
			t.Errorf("DecodeStep accepted mathematically fractional count max %s", literal)
		}
		readFraction := []byte(`{"epoch":"0","space":"s","queries":[{"kind":"range","t":"cards","cell":"r:c","min":"-inf","max":"+inf","limit":` + literal + `}]}`)
		if _, err := DecodeReadPlan(readFraction); err == nil {
			t.Errorf("DecodeReadPlan accepted mathematically fractional limit %s", literal)
		}
	}
	if _, err := DecodeStep([]byte(`{"epoch":"0","space":"s","entries":[{"kind":"count","t":"cards","cells":["r:c"],"max":[1e1000000]}]}`)); !requestRefusalIs(err) {
		t.Errorf("DecodeStep huge positive exponent = %v; want REQUEST", err)
	}
	if _, err := DecodeReadPlan([]byte(`{"epoch":"0","space":"s","queries":[{"kind":"range","t":"cards","cell":"r:c","min":"-inf","max":"+inf","limit":1e1000000}]}`)); !requestRefusalIs(err) {
		t.Errorf("DecodeReadPlan huge positive exponent = %v; want REQUEST", err)
	}
}

func TestWireCellSplitsAtLastColon(t *testing.T) {
	t.Parallel()
	row, col, err := ParseCellRef("archive:west:ready")
	if err != nil {
		t.Fatalf("ParseCellRef: %v", err)
	}
	if row != "archive:west" || col != "ready" {
		t.Fatalf("cell split = (%q,%q), want (%q,%q)", row, col, "archive:west", "ready")
	}
	for _, bad := range []string{"", "row", ":col", "row:"} {
		if _, _, err := ParseCellRef(bad); err == nil {
			t.Errorf("accepted malformed cell reference %q", bad)
		}
	}
}

func requestRefusalIs(err error) bool {
	var refusal *Refusal
	return errors.As(err, &refusal) && refusal.Code == "REQUEST"
}
