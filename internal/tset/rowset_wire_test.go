package tset

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestRowsetWireRoundTripsRequiredDenseRows(t *testing.T) {
	t.Parallel()
	step := rowsetStep(
		Entry{Kind: "rowset", Table: "cards", Rows: []RowRank{
			{Row: "zeta", Rank: "9007199254740991"},
			{Row: "alpha", Rank: "7"},
			{Row: "middle", Rank: "7"},
		}},
		Entry{Kind: "advance", AdvanceFrom: "0"},
	)
	raw, err := EncodeStep(step)
	if err != nil {
		t.Fatalf("EncodeStep rowset with unsorted rows and tied ranks: %v", err)
	}
	got, err := DecodeStep(raw)
	if err != nil {
		t.Fatalf("DecodeStep rowset: %v", err)
	}
	want := step.Entries[0].Rows
	if len(got.Entries) != 2 || !equalRowRanks(got.Entries[0].Rows, want) {
		t.Fatalf("rowset order/ranks changed: got %+v want %+v", got.Entries, step.Entries)
	}

	empty := rowsetStep(
		Entry{Kind: "rowset", Table: "cards", Rows: []RowRank{}},
		Entry{Kind: "advance", AdvanceFrom: "0"},
	)
	emptyRaw, err := EncodeStep(empty)
	if err != nil {
		t.Fatalf("EncodeStep empty rowset: %v", err)
	}
	if !bytes.Contains(emptyRaw, []byte(`"rows":[]`)) {
		t.Fatalf("empty rowset omitted its required dense rows array: %s", emptyRaw)
	}
	emptyDecoded, err := DecodeStep(emptyRaw)
	if err != nil || len(emptyDecoded.Entries) != 2 || emptyDecoded.Entries[0].Rows == nil || len(emptyDecoded.Entries[0].Rows) != 0 {
		t.Fatalf("empty rowset did not round-trip as an allocated empty array: entries=%+v err=%v", emptyDecoded.Entries, err)
	}
}

func TestRowsetWireRawShapeAndCanonicalRanks(t *testing.T) {
	t.Parallel()
	valid := []byte(`{"epoch":"0","space":"s","op":"rowset-wire-valid","intent":"canonical rows","entries":[{"kind":"rowset","t":"cards","rows":[{"row":"r","rank":"0"},{"row":"s","rank":"9007199254740991"}]},{"kind":"advance","from":"0"}]}`)
	if _, err := DecodeStep(valid); err != nil {
		t.Fatalf("DecodeStep canonical rank endpoints: %v", err)
	}

	badEntries := []string{
		`{"kind":"rowset","t":"cards"}`,
		`{"kind":"rowset","t":"cards","rows":null}`,
		`{"kind":"rowset","t":"cards","rows":{}}`,
		`{"kind":"rowset","t":"cards","rows":[{"rank":"1"}]}`,
		`{"kind":"rowset","t":"cards","rows":[{"row":"r"}]}`,
		`{"kind":"rowset","t":"cards","rows":[{"row":"r","rank":"1","extra":true}]}`,
		`{"kind":"rowset","t":"cards","rows":[{"row":1,"rank":"1"}]}`,
		`{"kind":"rowset","t":"cards","rows":[{"row":"r","rank":1}]}`,
		`{"kind":"rowset","t":"cards","rows":[{"row":"r","rank":"01"}]}`,
		`{"kind":"rowset","t":"cards","rows":[{"row":"r","rank":"+1"}]}`,
		`{"kind":"rowset","t":"cards","rows":[{"row":"r","rank":"-1"}]}`,
		`{"kind":"rowset","t":"cards","rows":[{"row":"r","rank":"9007199254740992"}]}`,
		`{"kind":"rowset","t":"cards","rows":[{"row":"r","rank":"1.0"}]}`,
		`{"kind":"rowset","t":"cards","rows":[{"row":"r","rank":"1"},{"row":"r","rank":"2"}]}`,
	}
	for _, entry := range badEntries {
		raw := rowsetRaw(entry, `{"kind":"advance","from":"0"}`)
		if _, err := DecodeStep(raw); !rowsetRefusal(err, "REQUEST") {
			t.Errorf("DecodeStep accepted malformed rowset %s: %v", entry, err)
		}
	}
	tooLongRow := fmt.Sprintf(`{"kind":"rowset","t":"cards","rows":[{"row":%q,"rank":"1"}]}`, string(bytes.Repeat([]byte{'r'}, MaxIdentifierBytes+1)))
	if _, err := DecodeStep(rowsetRaw(tooLongRow, `{"kind":"advance","from":"0"}`)); !rowsetRefusal(err, "LIMIT") {
		t.Errorf("DecodeStep overlong row name error = %v; want LIMIT", err)
	}

	typedMissing := rowsetStep(Entry{Kind: "rowset", Table: "cards"}, Entry{Kind: "advance", AdvanceFrom: "0"})
	if _, err := EncodeStep(typedMissing); !rowsetRefusal(err, "REQUEST") {
		t.Errorf("EncodeStep accepted nil rowset rows: %v", err)
	}
}

func TestRowsetWireMustBeAUniquePrefixBeforeAdvance(t *testing.T) {
	t.Parallel()
	valid := rowsetStep(
		Entry{Kind: "rowset", Table: "cards", Rows: []RowRank{{Row: "r", Rank: "0"}}},
		Entry{Kind: "advance", AdvanceFrom: "0"},
		Entry{Kind: "guard", Table: "cards", From: "r:c", IDs: []string{"c1"}},
		Entry{Kind: "rows", Table: "cards", Add: []string{"r"}},
	)
	if _, err := EncodeStep(valid); err != nil {
		t.Fatalf("EncodeStep with rowset prefix, first non-rowset advance, and following mutation: %v", err)
	}
	if _, err := EncodeStep(rowsetStep(Entry{Kind: "advance", AdvanceFrom: "0"})); err != nil {
		t.Fatalf("generic advance without rowset guards was rejected: %v", err)
	}

	invalid := [][]Entry{
		{{Kind: "rowset", Table: "cards", Rows: []RowRank{{Row: "r", Rank: "0"}}}},
		{{Kind: "rowset", Table: "cards", Rows: []RowRank{}}, {Kind: "rows", Table: "cards", Add: []string{"r"}}},
		{{Kind: "rowset", Table: "cards", Rows: []RowRank{}}, {Kind: "rows", Table: "cards", Add: []string{"r"}}, {Kind: "advance", AdvanceFrom: "0"}},
		{{Kind: "rowset", Table: "cards", Rows: []RowRank{}}, {Kind: "rowset", Table: "cards", Rows: []RowRank{}}, {Kind: "advance", AdvanceFrom: "0"}},
		{{Kind: "rowset", Table: "cards", Rows: []RowRank{}}, {Kind: "advance", AdvanceFrom: "0"}, {Kind: "rowset", Table: "other", Rows: []RowRank{}}},
		{{Kind: "rowset", Table: "cards", Rows: []RowRank{}}, {Kind: "guard", Table: "cards", From: "r:c", IDs: []string{"c1"}}, {Kind: "advance", AdvanceFrom: "0"}},
	}
	for i, entries := range invalid {
		if _, err := EncodeStep(rowsetStep(entries...)); !rowsetRefusal(err, "REQUEST") {
			t.Errorf("EncodeStep invalid rowset ordering case %d returned %v; want REQUEST", i, err)
		}
	}
}

func TestRowsetWireChargesDistinctTableRowsAcrossGuardsAndRestoration(t *testing.T) {
	t.Parallel()
	guarded := rowsetRows(1024)
	rows := rowsetWireNames(guarded)
	within := rowsetStep(
		Entry{Kind: "rowset", Table: "cards", Rows: guarded},
		Entry{Kind: "advance", AdvanceFrom: "0"},
		Entry{Kind: "rows", Table: "cards", Add: rows, Del: rows},
	)
	if _, err := EncodeStep(within); err != nil {
		t.Fatalf("EncodeStep 1024 guarded/restored distinct rows: %v", err)
	}

	tooMany := rowsetStep(
		Entry{Kind: "rowset", Table: "cards", Rows: guarded},
		Entry{Kind: "advance", AdvanceFrom: "0"},
		Entry{Kind: "rows", Table: "cards", Add: []string{"row-new"}},
	)
	if _, err := EncodeStep(tooMany); !rowsetRefusal(err, "LIMIT") {
		t.Errorf("EncodeStep 1025th distinct table/row pair returned %v; want LIMIT", err)
	}

	restored604 := rowsetRows(604)
	settlement := rowsetStep(
		Entry{Kind: "rowset", Table: "cards", Rows: restored604},
		Entry{Kind: "advance", AdvanceFrom: "0"},
		Entry{Kind: "rows", Table: "cards", Add: rowsetWireNames(restored604), Del: rowsetWireNames(restored604)},
	)
	if _, err := EncodeStep(settlement); err != nil {
		t.Fatalf("EncodeStep 604 guarded rows restored to the same names (union cost 604): %v", err)
	}
}

func rowsetStep(entries ...Entry) Step {
	step := Step{Epoch: "0", Space: "s", Entries: entries}
	for _, entry := range entries {
		if entry.Kind == "advance" {
			op, intent := "rowset-wire-advance", "rowset wire contract"
			step.Op, step.Intent = &op, &intent
			break
		}
	}
	return step
}

func rowsetRows(count int) []RowRank {
	rows := make([]RowRank, count)
	for i := range rows {
		rows[i] = RowRank{Row: "row-" + strconv.Itoa(i), Rank: Decimal(strconv.Itoa(i))}
	}
	return rows
}

func rowsetWireNames(rows []RowRank) []string {
	names := make([]string, len(rows))
	for i, row := range rows {
		names[i] = row.Row
	}
	return names
}

func rowsetRaw(entries ...string) []byte {
	return []byte(`{"epoch":"0","space":"s","op":"rowset-wire-raw","intent":"raw rowset shape","entries":[` + strings.Join(entries, ",") + `]}`)
}

func rowsetRefusal(err error, code string) bool {
	var refusal *Refusal
	return errors.As(err, &refusal) && refusal.Code == code
}

func equalRowRanks(a, b []RowRank) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
