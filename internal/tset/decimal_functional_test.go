//go:build functional

package tset

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// TestDecimalWireAndOverflow checks the decimal boundary through the public
// Redis Functions as well as the independent Mem planner. In particular, a
// number that CJSON could round must never stand in for an exact string.
func TestDecimalWireAndOverflow(t *testing.T) {
	t.Parallel()

	t.Run("exact decimal helpers and wire", func(t *testing.T) {
		for _, value := range []Decimal{"0", "1", "9007199254740991", "9007199254740992", "18446744073709551615"} {
			if !ValidDecimal(value) {
				t.Fatalf("valid decimal %q refused", value)
			}
		}
		for _, value := range []Decimal{"", "00", "01", "+1", "-1", "1.0", "1e0", " 1", "18446744073709551616"} {
			if ValidDecimal(value) {
				t.Fatalf("malformed decimal %q accepted", value)
			}
		}
		if next, err := NextDecimal("9007199254740992"); err != nil || next != "9007199254740993" {
			t.Fatalf("successor past double precision: %q, %v", next, err)
		}
		if next, err := NextDecimal("18446744073709551614"); err != nil || next != "18446744073709551615" {
			t.Fatalf("last exact successor: %q, %v", next, err)
		}
		_, err := NextDecimal("18446744073709551615")
		requireRefusal(t, err, "OVERFLOW")

		fx := newTSetFixture(t)
		fx.Activate(t)
		for _, literal := range []string{"1", "1.0", "1e0", "null", `"01"`, `"+1"`, `"1e0"`, `"18446744073709551616"`} {
			raw := fmt.Sprintf(`{"epoch":%s,"space":%q,"entries":[]}`, literal, fx.Space)
			_, decodeErr := DecodeStep([]byte(raw))
			requireRefusal(t, decodeErr, "REQUEST")
			decimalRawStepRefusal(t, fx, raw, "REQUEST")
		}
		// A syntactically valid value above 2^53 remains a string. The store
		// refuses EPOCHAHEAD because its active epoch is zero, not REQUEST because
		// of a rounded or malformed decimal.
		raw := fmt.Sprintf(`{"epoch":"9007199254740992","space":%q,"entries":[]}`, fx.Space)
		decoded, err := DecodeStep([]byte(raw))
		if err != nil || decoded.Epoch != "9007199254740992" {
			t.Fatalf("exact large epoch decode: %+v, %v", decoded, err)
		}
		decimalRawStepRefusal(t, fx, raw, "EPOCHAHEAD")
	})

	t.Run("exact revisions and atomic overflow", func(t *testing.T) {
		const maximum = Decimal("18446744073709551615")
		fx, mem := namedRefusalFixture(t, maximum)
		h := &namedStateHarness{fx: fx, mem: mem}
		space := fx.Space
		for _, literal := range []string{"1", "1.0", "1e0", "null", `"01"`, `"+1"`, `"1e0"`, `"18446744073709551616"`} {
			raw := fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[{"kind":"guard","t":"work","from":"r:c","ids":["existing"],"revs":[%s]}]}`, space, literal)
			_, decodeErr := DecodeStep([]byte(raw))
			requireRefusal(t, decodeErr, "REQUEST")
			decimalRawStepRefusal(t, fx, raw, "REQUEST")
		}
		guard := stateStep(space, "0", Entry{Kind: "guard", Table: "work", From: "r:c",
			IDs: []string{"existing"}, Revs: []Decimal{maximum}})
		if reply := h.unchanged(t, guard); reply.Guarded != 1 {
			t.Fatalf("exact max revision guard: %+v", reply)
		}
		noOp := stateStep(space, "0", Entry{Kind: "move", Table: "work", From: "r:c", To: "r:c",
			IDs: []string{"existing"}, Revs: []Decimal{maximum}, Set: map[string]string{"state": "live"}})
		if reply := h.unchanged(t, noOp); reply.Changed != 0 {
			t.Fatalf("no-op at max revision changed=%d", reply.Changed)
		}
		if got := stateWork(t, h.snapshot(t), "0").Records["existing"].Revision; got != maximum {
			t.Fatalf("no-op changed exact revision to %q", got)
		}

		h.apply(t, stateStep(space, "0", Entry{Kind: "create", Table: "work", To: "r:c",
			IDs: []string{"first"}, Scores: []string{"1"}}))
		mixed := stateStep(space, "0", Entry{Kind: "move", Table: "work", From: "r:c", To: "r:c",
			IDs: []string{"first", "existing"}, Revs: []Decimal{"1", maximum},
			Set: map[string]string{"state": "changed"}})
		h.refuse(t, mixed, "OVERFLOW")
		final := stateWork(t, h.snapshot(t), "0")
		if final.Records["first"].Revision != "1" || final.Records["existing"].Revision != maximum {
			t.Fatalf("mixed overflow changed revisions: %+v", final.Records)
		}
	})

	t.Run("adjacent revisions and integer count and read limit lexemes", func(t *testing.T) {
		fx, mem := namedRefusalFixture(t, "9007199254740992")
		h := &namedStateHarness{fx: fx, mem: mem}
		guard := func(rev Decimal) Step {
			return stateStep(fx.Space, "0", Entry{Kind: "guard", Table: "work", From: "r:c",
				IDs: []string{"existing"}, Revs: []Decimal{rev}})
		}
		h.unchanged(t, guard("9007199254740992"))
		h.refuse(t, guard("9007199254740993"), "REVISION")
		for _, literal := range []string{"1.0", "1e0", "100e-2"} {
			raw := fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[{"kind":"count","t":"work","cells":["r:c"],"max":[%s]}]}`, fx.Space, literal)
			if _, err := DecodeStep([]byte(raw)); err != nil {
				t.Fatalf("count max %s rejected by Go: %v", literal, err)
			}
			decimalRawStepOK(t, fx, raw)

			read := fmt.Sprintf(`{"epoch":"0","space":%q,"queries":[{"kind":"range","t":"work","cell":"r:c","min":"-inf","max":"+inf","limit":%s}]}`, fx.Space, literal)
			if _, err := DecodeReadPlan([]byte(read)); err != nil {
				t.Fatalf("read limit %s rejected by Go: %v", literal, err)
			}
			decimalRawReadOK(t, fx, read)
		}
		for _, zero := range []string{"-0.0", "0e1000000"} {
			raw := fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[{"kind":"count","t":"work","cells":["r:c"],"max":[%s]}]}`, fx.Space, zero)
			if _, err := DecodeStep([]byte(raw)); err != nil {
				t.Fatalf("mathematical zero %s rejected by Go: %v", zero, err)
			}
			decimalRawStepRefusal(t, fx, raw, "CELLFULL")
		}
		for _, literal := range []string{"1.5", "1.00000000000000001", "1e1000000"} {
			raw := fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[{"kind":"count","t":"work","cells":["r:c"],"max":[%s]}]}`, fx.Space, literal)
			_, decodeErr := DecodeStep([]byte(raw))
			requireRefusal(t, decodeErr, "REQUEST")
			decimalRawStepRefusal(t, fx, raw, "REQUEST")
			read := fmt.Sprintf(`{"epoch":"0","space":%q,"queries":[{"kind":"range","t":"work","cell":"r:c","min":"-inf","max":"+inf","limit":%s}]}`, fx.Space, literal)
			_, decodeErr = DecodeReadPlan([]byte(read))
			requireRefusal(t, decodeErr, "REQUEST")
			decimalRawReadRefusal(t, fx, read, "REQUEST")
			rcount := fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[{"kind":"rcount","t":"work","cells":["r:c"],"min":"-inf","max":"+inf","atleast":%s}]}`, fx.Space, literal)
			_, decodeErr = DecodeStep([]byte(rcount))
			requireRefusal(t, decodeErr, "REQUEST")
			decimalRawStepRefusal(t, fx, rcount, "REQUEST")
		}
		// Go's map decoder and CJSON both retain the last duplicate name.
		// Precision checking must follow the surviving max value.
		duplicate := fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[{"kind":"count","t":"work","cells":["r:c"],"max":[1.00000000000000001],"max":[1]}]}`, fx.Space)
		if _, err := DecodeStep([]byte(duplicate)); err != nil {
			t.Fatalf("last valid duplicate max rejected by Go: %v", err)
		}
		decimalRawStepOK(t, fx, duplicate)
		lastFractional := fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[{"kind":"count","t":"work","cells":["r:c"],"max":[1],"max":[1.00000000000000001]}]}`, fx.Space)
		_, decodeErr := DecodeStep([]byte(lastFractional))
		requireRefusal(t, decodeErr, "REQUEST")
		decimalRawStepRefusal(t, fx, lastFractional, "REQUEST")
		quoted := fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[{"kind":"count","t":"work","cells":["r:c"],"max":["1.00000000000000001"]}]}`, fx.Space)
		_, decodeErr = DecodeStep([]byte(quoted))
		requireRefusal(t, decodeErr, "REQUEST")
		decimalRawStepRefusal(t, fx, quoted, "REQUEST")
		meta := fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[{"kind":"move","t":"work","from":"r:c","to":"r:c","ids":["existing"],"revs":["9007199254740992"],"set":{"state":"live"},"meta":{"fraction":1.00000000000000001,"null":null}}]}`, fx.Space)
		if _, err := DecodeStep([]byte(meta)); err != nil {
			t.Fatalf("opaque fractional metadata rejected by Go: %v", err)
		}
		decimalRawStepOK(t, fx, meta)
	})

	t.Run("sequence tokens are strings", func(t *testing.T) {
		fx := newTSetFixture(t)
		fx.Activate(t)
		for _, literal := range []string{"0", "1.0", "1e0", "null", `"00"`, `"+1"`, `"1e0"`, `"18446744073709551616"`} {
			raw := fmt.Sprintf(`{"epoch":"0","space":%q,"queries":[{"kind":"lines","after_seq":%s,"limit":1}]}`, fx.Space, literal)
			_, decodeErr := DecodeReadPlan([]byte(raw))
			requireRefusal(t, decodeErr, "REQUEST")
			decimalRawReadRefusal(t, fx, raw, "REQUEST")
		}
		for _, read := range []string{
			fmt.Sprintf(`{"epoch":"0","space":%q,"queries":[{"kind":"lines","after_seq":"0","limit":1,"ids_limit":1.00000000000000001}]}`, fx.Space),
			fmt.Sprintf(`{"epoch":"0","space":%q,"mode":"page","queries":[{"kind":"cardlines","abouts":["x"],"limit":1,"cursor":{"epoch":"0","fields":[],"include_meta":false,"positions":[{"about":"x","next_index":1.00000000000000001,"through_index":-1}]}}]}`, fx.Space),
		} {
			_, decodeErr := DecodeReadPlan([]byte(read))
			requireRefusal(t, decodeErr, "REQUEST")
			decimalRawReadRefusal(t, fx, read, "REQUEST")
		}
	})
}

func TestNotesRequireNamedOp(t *testing.T) {
	t.Parallel()
	note := Note{Line: NoteLine{Kind: "note", Meta: json.RawMessage(`{"source":"wire"}`)},
		About: []string{"p"}}
	unnamed := newTSetFixture(t)
	unnamed.Activate(t)
	for _, raw := range []string{
		fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[],"notes":[{"line":{"kind":"note","meta":{"source":"wire"}},"about":["p"]}]}`, unnamed.Space),
		fmt.Sprintf(`{"epoch":"0","space":%q,"op":"note-op","entries":[],"notes":[{"line":{"kind":"note","meta":{"source":"wire"}},"about":["p"]}]}`, unnamed.Space),
	} {
		_, decodeErr := DecodeStep([]byte(raw))
		requireRefusal(t, decodeErr, "REQUEST")
		decimalRawStepRefusal(t, unnamed, raw, "REQUEST")
	}
	emptyNotes := fmt.Sprintf(`{"epoch":"0","space":%q,"entries":[],"notes":[]}`, unnamed.Space)
	if _, err := DecodeStep([]byte(emptyNotes)); err != nil {
		t.Fatalf("empty unnamed notes refused by Go: %v", err)
	}
	decimalRawStepOK(t, unnamed, emptyNotes)

	t.Run("composed named note", func(t *testing.T) {
		t.Parallel()
		fx := newComposedTSetFixture(t)
		fx.Define(t, "work", "c")
		fx.Activate(t)
		op, intent := "note-op", "semantic note intent"
		step := Step{Epoch: "0", Space: fx.Space, Op: &op, Intent: &intent,
			Entries: []Entry{}, Notes: []Note{note}}
		raw, err := EncodeStep(step)
		if err != nil {
			t.Fatalf("named note rejected by Go: %v", err)
		}
		var reply Reply
		if err := json.Unmarshal(decimalRawStep(t, fx, string(raw)), &reply); err != nil {
			t.Fatal(err)
		}
		if reply.Status != "ok" || reply.Lines != 1 || reply.FirstSeq != "1" || reply.LastSeq != "1" {
			t.Fatalf("named note reply: %+v", reply)
		}
	})
}

func TestIdentifierByteLimits(t *testing.T) {
	t.Parallel()
	fx, _ := namedRefusalFixture(t, "3")
	utf8Name := func(n int) string {
		value := strings.Repeat("é", n/2)
		if n%2 != 0 {
			value += "a"
		}
		return value
	}
	for _, n := range []int{255, 256, 257} {
		for _, kind := range []string{"id", "row", "column", "field", "op", "space"} {
			step := Step{Epoch: "0", Space: fx.Space, Entries: []Entry{}}
			switch kind {
			case "id":
				step.Entries = []Entry{{Kind: "guard", Table: "work", From: "r:c", IDs: []string{utf8Name(n)}}}
			case "row":
				step.Entries = []Entry{{Kind: "guard", Table: "work", From: utf8Name(n) + ":c", IDs: []string{"existing"}}}
			case "column":
				step.Entries = []Entry{{Kind: "guard", Table: "work", From: "r:" + strings.Repeat("a", n), IDs: []string{"existing"}}}
			case "field":
				step.Entries = []Entry{{Kind: "guard", Table: "work", From: "r:c", IDs: []string{"existing"}, BeforeFields: []string{utf8Name(n)}}}
			case "op":
				op, intent := utf8Name(n), "byte boundary"
				step.Op, step.Intent = &op, &intent
			case "space":
				step.Space = utf8Name(n)
			}
			raw, err := json.Marshal(step)
			if err != nil {
				t.Fatalf("marshal %s/%d: %v", kind, n, err)
			}
			_, decodeErr := DecodeStep(raw)
			if n > 256 {
				requireRefusal(t, decodeErr, "LIMIT")
				decimalRawStepRefusal(t, fx, string(raw), "LIMIT")
				continue
			}
			if decodeErr != nil {
				t.Fatalf("valid %s of %d UTF-8 bytes refused by Go: %v", kind, n, decodeErr)
			}
			var envelope struct {
				Status string `json:"status"`
				Code   string `json:"code"`
			}
			if err := json.Unmarshal(decimalRawStep(t, fx, string(raw)), &envelope); err != nil {
				t.Fatal(err)
			}
			if envelope.Status == "refused" && (envelope.Code == "REQUEST" || envelope.Code == "LIMIT") {
				t.Fatalf("valid %s of %d UTF-8 bytes got static Lua refusal %s", kind, n, envelope.Code)
			}
		}
	}
	for _, tc := range []struct {
		name  string
		entry Entry
		code  string
	}{
		{"reserved field", Entry{Kind: "guard", Table: "work", From: "r:c", IDs: []string{"existing"},
			BeforeFields: []string{"place:" + strings.Repeat("a", 252)}}, "FIELDNAME"},
		{"controlled row", Entry{Kind: "guard", Table: "work", From: strings.Repeat("a", 256) + "\u0080:c", IDs: []string{"existing"}}, "REQUEST"},
		{"nonsymbolic column", Entry{Kind: "guard", Table: "work", From: "r:" + utf8Name(257), IDs: []string{"existing"}}, "REQUEST"},
	} {
		step := Step{Epoch: "0", Space: fx.Space, Entries: []Entry{tc.entry}}
		raw, err := json.Marshal(step)
		if err != nil {
			t.Fatal(err)
		}
		_, decodeErr := DecodeStep(raw)
		requireRefusal(t, decodeErr, tc.code)
		decimalRawStepRefusal(t, fx, string(raw), tc.code)
	}

	// A cell reference is bounded by each component, not by its total size.
	wide := newTSetFixture(t)
	column, row := strings.Repeat("a", 256), utf8Name(256)
	wide.Define(t, "work", column)
	wide.AddRow(t, "work", row, 0)
	wide.Activate(t)
	cell := row + ":" + column
	if len(cell) != 513 {
		t.Fatalf("cell fixture length=%d, want 513", len(cell))
	}
	step := Step{Epoch: "0", Space: wide.Space, Entries: []Entry{{Kind: "count", Table: "work", Cells: []string{cell}, CountMax: []uint64{0}}}}
	raw, err := json.Marshal(step)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeStep(raw); err != nil {
		t.Fatalf("valid 513-byte cell rejected by Go: %v", err)
	}
	decimalRawStepOK(t, wide, string(raw))
}

func decimalRawStep(t *testing.T, fx *tsetFixture, raw string) []byte {
	t.Helper()
	wire, err := fx.Step(raw)
	if err != nil {
		t.Fatalf("raw Lua step: %v", err)
	}
	switch value := wire.(type) {
	case string:
		return []byte(value)
	case []byte:
		return value
	default:
		t.Fatalf("raw Lua step type %T", wire)
		return nil
	}
}

func decimalRawStepRefusal(t *testing.T, fx *tsetFixture, raw, code string) {
	t.Helper()
	before := commitProbeImage(t, fx.Client)
	var got Refusal
	if err := json.Unmarshal(decimalRawStep(t, fx, raw), &got); err != nil {
		t.Fatal(err)
	}
	requireRefusal(t, &got, code)
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Fatalf("%s raw step changed Redis image", code)
	}
}

func decimalRawStepOK(t *testing.T, fx *tsetFixture, raw string) {
	t.Helper()
	before := commitProbeImage(t, fx.Client)
	var got Reply
	encoded := decimalRawStep(t, fx, raw)
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "ok" || got.Changed != 0 {
		t.Fatalf("raw count step: %+v", got)
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &shape); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"epoch_before", "epoch_after", "first_seq", "last_seq"} {
		var exact string
		if err := json.Unmarshal(shape[name], &exact); err != nil || !ValidDecimal(Decimal(exact)) {
			t.Fatalf("%s must be an exact decimal JSON string: %s (%v)", name, shape[name], err)
		}
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Fatal("guard-only raw step changed Redis image")
	}
}

func decimalRawRead(t *testing.T, fx *tsetFixture, raw string) []byte {
	t.Helper()
	wire, err := fx.Client.FCallRO(context.Background(), "ns_tset_read", []string{}, Version, raw).Result()
	if err != nil {
		t.Fatalf("raw Lua read: %v", err)
	}
	switch value := wire.(type) {
	case string:
		return []byte(value)
	case []byte:
		return value
	default:
		t.Fatalf("raw Lua read type %T", wire)
		return nil
	}
}

func decimalRawReadRefusal(t *testing.T, fx *tsetFixture, raw, code string) {
	t.Helper()
	before := commitProbeImage(t, fx.Client)
	var got Refusal
	if err := json.Unmarshal(decimalRawRead(t, fx, raw), &got); err != nil {
		t.Fatal(err)
	}
	requireRefusal(t, &got, code)
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Fatalf("%s raw read changed Redis image", code)
	}
}

func decimalRawReadOK(t *testing.T, fx *tsetFixture, raw string) {
	t.Helper()
	before := commitProbeImage(t, fx.Client)
	var got struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(decimalRawRead(t, fx, raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.Status != "read" {
		t.Fatalf("raw range read status %q", got.Status)
	}
	if after := commitProbeImage(t, fx.Client); !reflect.DeepEqual(before, after) {
		t.Fatal("raw range read changed Redis image")
	}
}
