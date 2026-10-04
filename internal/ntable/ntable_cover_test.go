package ntable

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNtableCoverTableRowLookup covers Table.Row: the hit, the miss and the
// empty table.
func TestNtableCoverTableRowLookup(t *testing.T) {
	t.Parallel()
	cols, err := ParseColumns("ok,failed")
	require.NoError(t, err)
	tbl := Table{Name: "fleet", Columns: cols, Rows: []Row{{Key: "alpha"}, {Key: "beta"}}}
	cases := []struct {
		name string
		key  string
		want int
	}{
		{"first row", "alpha", 0},
		{"second row", "beta", 1},
		{"missing row", "gamma", -1},
		{"empty key", "", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tbl.Row(tc.key))
		})
	}
	t.Run("no rows", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, -1, Table{Name: "empty"}.Row("alpha"))
	})
}

// TestNtableCoverEpochKeyHelpers covers the epoch-carrying key builders at
// epoch zero and at a later epoch.
func TestNtableCoverEpochKeyHelpers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"rows at epoch zero", RowsKeyAt("t", 0), "table:t:rows"},
		{"rows at epoch seven", RowsKeyAt("t", 7), "table:t:7:rows"},
		{"props at epoch zero", PropsKeyAt("t", 0), "table:t:props"},
		{"props at epoch three", PropsKeyAt("t", 3), "table:t:3:props"},
		{"row at epoch zero", RowKeyAt("t", "r", 0), "table:t:row:r"},
		{"row at epoch two", RowKeyAt("t", "r", 2), "table:t:2:row:r"},
		{"cell at epoch zero", CellKeyAt("t", "r", "c", 0), "table:t:cell:r:c"},
		{"cell at epoch nine", CellKeyAt("t", "r", "c", 9), "table:t:9:cell:r:c"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.got)
		})
	}
}

// TestNtableCoverEpochZeroKeyHelpers covers the epoch-zero aliases beside the
// At helpers they name.
func TestNtableCoverEpochZeroKeyHelpers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"rows key", RowsKey("t"), "table:t:rows"},
		{"row key", RowKey("t", "r"), "table:t:row:r"},
		{"cell key", CellKey("t", "r", "c"), "table:t:cell:r:c"},
		{"names with dots and dashes", RowsKey("my-table.1"), "table:my-table.1:rows"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.got)
		})
	}
	t.Run("epoch zero alias", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, RowsKeyAt("t", 0), RowsKey("t"))
		assert.Equal(t, RowKeyAt("t", "r", 0), RowKey("t", "r"))
		assert.Equal(t, CellKeyAt("t", "r", "c", 0), CellKey("t", "r", "c"))
	})
}

// TestNtableCoverMetadataKeys covers the member, changes, revision and
// identity keys: reserved metadata outside every valid table-name prefix.
func TestNtableCoverMetadataKeys(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"member key", MemberKey("42"), "table::member:42"},
		{"changes key", ChangesKey("fleet"), "table:fleet:changes"},
		{"revision key", RevisionKey("fleet"), "table:fleet:revision"},
		{"identity key", IdentityKey("fleet"), "table:fleet:identity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.got)
		})
	}
	t.Run("member key is no table name", func(t *testing.T) {
		t.Parallel()
		assert.False(t, ValidName(MemberKey("42")), "the member prefix must not parse as a table name")
	})
}

// TestNtableCoverValidRowKey covers ValidRowKey: the accept path and its
// refusals (empty, bad UTF-8, ASCII controls).
func TestNtableCoverValidRowKey(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		key   string
		valid bool
	}{
		{"plain word", "alpha", true},
		{"dots dashes underscores", "a.b-c_d1", true},
		{"space is printable", "two words", true},
		{"unicode letters", "日本語", true},
		{"empty is refused", "", false},
		{"invalid utf8 is refused", "\xff\xfe", false},
		{"tab is refused", "a\tb", false},
		{"newline is refused", "a\nb", false},
		{"nul is refused", "\x00", false},
		{"del is refused", "a\x7fb", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.valid, ValidRowKey(tc.key), "ValidRowKey(%q)", tc.key)
		})
	}
}

// TestNtableCoverColumnCodec covers encodeColumn and decodeColumn: the round
// trip and each refusal (short value, non-numeric width, bad grammar).
func TestNtableCoverColumnCodec(t *testing.T) {
	t.Parallel()
	t.Run("encode", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "count:sum:4:OK", encodeColumn(Column{Name: "ok", Projection: Count, Fold: Sum, Width: 4, Label: "OK"}))
		assert.Equal(t, "text:none:0:", encodeColumn(Column{Name: "note", Projection: Text, Fold: None}))
	})
	t.Run("round trip", func(t *testing.T) {
		t.Parallel()
		for _, c := range []Column{
			{Name: "ok", Projection: Count, Fold: Sum, Width: 3, Label: "OK"},
			{Name: "share", Projection: "pct(ok)", Fold: Pooled, Width: 0, Label: ""},
			{Name: "note", Projection: Text, Fold: None, Width: 8, Label: "a:label:with:colons"},
		} {
			back, err := decodeColumn(c.Name, encodeColumn(c))
			require.NoError(t, err, "decode %q", encodeColumn(c))
			assert.Equal(t, c, back, "round trip of %q", c.Name)
		}
	})
	cases := []struct {
		name    string
		value   string
		wantErr string
	}{
		{"too short", "count:sum:4", `not projection:fold:width:label`},
		{"width not a number", "count:sum:x:", "is not a number"},
		{"unknown projection", "bogus:sum:4:", "wants a projection"},
		{"fold does not fit", "count:union:4:", "folds union"},
	}
	for _, tc := range cases {
		t.Run("refuse "+tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := decodeColumn("ok", tc.value)
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// TestNtableCoverDefinitionCodec covers definitionFields and
// decodeDefinition: the fields a table writes, the read-back, the absent
// table and each malformed definition.
func TestNtableCoverDefinitionCodec(t *testing.T) {
	t.Parallel()
	cols, err := ParseColumns("ok,failed:text,note:text:none")
	require.NoError(t, err)
	t.Run("fields and read-back", func(t *testing.T) {
		t.Parallel()
		src := Table{Name: "fleet", Columns: cols, FooterLabel: "total",
			EpochKey: "ws:1", EpochField: "n", MemberPrefix: "table::member:",
			Hidden: []string{"failed"}, HiddenTable: true}
		h := definitionFields(src)
		assert.Equal(t, "ok,failed,note", h["order"])
		assert.Equal(t, "total", h["footer"])
		assert.Equal(t, "count:sum:0:", h["col:ok"])
		assert.Equal(t, "ws:1", h["epoch_key"])
		assert.Equal(t, "n", h["epoch_field"])
		assert.Equal(t, "table::member:", h["member_prefix"])
		assert.Equal(t, "failed", h["hidden"])
		assert.Equal(t, "0", h["visible"])
		got, ok, err := decodeDefinition(src.Name, h)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, src.Name, got.Name)
		assert.Equal(t, src.FooterLabel, got.FooterLabel)
		assert.Equal(t, src.EpochKey, got.EpochKey)
		assert.Equal(t, src.EpochField, got.EpochField)
		assert.Equal(t, src.MemberPrefix, got.MemberPrefix)
		assert.Equal(t, src.Hidden, got.Hidden)
		assert.True(t, got.HiddenTable)
		assert.Equal(t, src.Columns, got.Columns)
	})
	t.Run("plain table writes no optional fields", func(t *testing.T) {
		t.Parallel()
		h := definitionFields(Table{Name: "t", Columns: cols})
		assert.Equal(t, "", h["footer"])
		_, hasPrefix := h["member_prefix"]
		assert.False(t, hasPrefix, "no member_prefix field without one")
		_, hasEpoch := h["epoch_key"]
		assert.False(t, hasEpoch, "no epoch fields without an epoch key")
		_, hasHidden := h["hidden"]
		assert.False(t, hasHidden, "no hidden field without hidden columns")
		_, hasVisible := h["visible"]
		assert.False(t, hasVisible, "no visible field for a visible table")
	})
	t.Run("epoch field defaults to n", func(t *testing.T) {
		t.Parallel()
		h := definitionFields(Table{Name: "t", Columns: cols, EpochKey: "shared"})
		assert.Equal(t, "n", h["epoch_field"])
	})
	t.Run("read epoch and revision and sort", func(t *testing.T) {
		t.Parallel()
		h := definitionFields(Table{Name: "t", Columns: cols})
		h["read_epoch"] = "5"
		h["read_revision"] = "7"
		h["sort"] = "name"
		got, ok, err := decodeDefinition("t", h)
		require.NoError(t, err)
		require.True(t, ok)
		assert.Equal(t, uint64(5), got.Epoch)
		assert.Equal(t, uint64(7), got.Revision)
		assert.Equal(t, "name", got.Sort)
	})
	t.Run("absent table", func(t *testing.T) {
		t.Parallel()
		got, ok, err := decodeDefinition("gone", nil)
		require.NoError(t, err)
		assert.False(t, ok, "an empty hash is an absent table, not an error")
		assert.Equal(t, Table{}, got)
	})
	cases := []struct {
		name    string
		h       map[string]string
		wantErr string
	}{
		{"no order", map[string]string{"col:ok": "count:sum:0:"}, "has no column order"},
		{"ordered but undefined", map[string]string{"order": "ok", "col:failed": "count:sum:0:"}, "orders column ok, which it does not define"},
		{"malformed read epoch", map[string]string{"order": "ok", "col:ok": "count:sum:0:", "read_epoch": "five"}, "malformed read_epoch"},
		{"malformed column", map[string]string{"order": "ok", "col:ok": "count:sum"}, "not projection:fold:width:label"},
	}
	for _, tc := range cases {
		t.Run("refuse "+tc.name, func(t *testing.T) {
			t.Parallel()
			_, ok, err := decodeDefinition("t", tc.h)
			require.Error(t, err)
			assert.True(t, ok, "a present-but-bad table is still present")
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// TestNtableCoverSameDefinition covers SameDefinition: the equal defaults and
// each refusal (footer, columns, epoch domain, member prefix).
func TestNtableCoverSameDefinition(t *testing.T) {
	t.Parallel()
	cols, err := ParseColumns("ok,failed")
	require.NoError(t, err)
	base := Table{Name: "t", Columns: cols, FooterLabel: "total"}
	t.Run("equal", func(t *testing.T) {
		t.Parallel()
		assert.True(t, SameDefinition(base, base))
		other := Table{Name: "other", FooterLabel: "total", Columns: cols}
		assert.True(t, SameDefinition(base, other), "the name is not the definition")
		withDefaults := Table{Columns: cols, FooterLabel: "total", EpochField: "n", MemberPrefix: "table::member:"}
		assert.True(t, SameDefinition(base, withDefaults), "blank epoch field and member prefix take their defaults")
	})
	cases := []struct {
		name string
		mod  func(*Table)
	}{
		{"footer differs", func(t *Table) { t.FooterLabel = "summed" }},
		{"column set differs", func(t *Table) { t.Columns = cols[:1] }},
		{"column width differs", func(t *Table) { t.Columns = append([]Column{}, t.Columns...); t.Columns[0].Width = 9 }},
		{"epoch key differs", func(t *Table) { t.EpochKey = "shared" }},
		{"epoch field differs", func(t *Table) { t.EpochKey = "shared"; t.EpochField = "seq" }},
		{"member prefix differs", func(t *Table) { t.MemberPrefix = "s:" }},
	}
	for _, tc := range cases {
		t.Run("refuse "+tc.name, func(t *testing.T) {
			t.Parallel()
			bad := base
			bad.Columns = append([]Column{}, cols...)
			tc.mod(&bad)
			assert.False(t, SameDefinition(base, bad))
			assert.False(t, SameDefinition(bad, base), "the refusal is symmetric")
		})
	}
}

// TestNtableCoverRowFields covers rowFields: the label, exclude and owner
// when set, key:<col> for bound cells only, and the bare owned row.
func TestNtableCoverRowFields(t *testing.T) {
	t.Parallel()
	cols, err := ParseColumns("ok,members:members:union")
	require.NoError(t, err)
	tbl := Table{Name: "t", Columns: cols}
	t.Run("bare row writes nothing", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, rowFields(tbl, NewRow(tbl, "alpha")))
	})
	t.Run("label exclude owner and bindings", func(t *testing.T) {
		t.Parallel()
		r := NewRow(tbl, "alpha")
		r.Label, r.Exclude, r.Owner = "Alpha", "stopped", "ws"
		r.Cells[1] = Cell{Key: "ws:1:waiting", Bound: true}
		m := rowFields(tbl, r)
		assert.Equal(t, "Alpha", m["label"])
		assert.Equal(t, "stopped", m["exclude"])
		assert.Equal(t, "ws", m["owner"])
		assert.Equal(t, "ws:1:waiting", m["key:members"])
		_, owned := m["key:ok"]
		assert.False(t, owned, "an owned cell names no key in the row hash")
	})
	t.Run("bound cell without a key is skipped", func(t *testing.T) {
		t.Parallel()
		r := NewRow(tbl, "alpha")
		r.Cells[0] = Cell{Bound: true}
		_, has := rowFields(tbl, r)["key:ok"]
		assert.False(t, has)
	})
}

// TestNtableCoverSameShape covers SameShape: the equal shape and each refusal
// (row key, label, exclude, owner, cell count, binding, cell key).
func TestNtableCoverSameShape(t *testing.T) {
	t.Parallel()
	cols, err := ParseColumns("ok,members:members:union")
	require.NoError(t, err)
	newTable := func(rows ...Row) Table {
		return Table{Name: "t", Columns: cols, Rows: rows}
	}
	bind := func(key string) Row {
		r := NewRow(newTable(), key)
		r.Cells[1] = Cell{Key: "ws:1:waiting", Bound: true}
		return r
	}
	t.Run("equal", func(t *testing.T) {
		t.Parallel()
		a := newTable(NewRow(newTable(), "alpha"), bind("beta"))
		b := newTable(NewRow(newTable(), "alpha"), bind("beta"))
		assert.True(t, SameShape(a, b))
		c := newTable(NewRow(newTable(), "alpha"), bind("beta"))
		c.Rows[0].Cells[0].Count = 7
		assert.True(t, SameShape(a, c), "cell values are the shape's apart")
	})
	cases := []struct {
		name string
		mod  func(*Table)
	}{
		{"row count differs", func(t *Table) { t.Rows = t.Rows[:1] }},
		{"row key differs", func(t *Table) { t.Rows[0].Key = "gamma" }},
		{"label differs", func(t *Table) { t.Rows[0].Label = "Alpha" }},
		{"exclude differs", func(t *Table) { t.Rows[1].Exclude = "stopped" }},
		{"owner differs", func(t *Table) { t.Rows[1].Owner = "ws" }},
		{"cell count differs", func(t *Table) { t.Rows[0].Cells = t.Rows[0].Cells[:1] }},
		{"binding differs", func(t *Table) { t.Rows[1].Cells[1] = Cell{Key: "ws:1:waiting"} }},
		{"cell key differs", func(t *Table) { t.Rows[1].Cells[1] = Cell{Key: "ws:2:waiting", Bound: true} }},
		{"definition differs", func(t *Table) { t.Columns = cols[:1] }},
	}
	for _, tc := range cases {
		t.Run("refuse "+tc.name, func(t *testing.T) {
			t.Parallel()
			base := newTable(NewRow(newTable(), "alpha"), bind("beta"))
			other := newTable(NewRow(newTable(), "alpha"), bind("beta"))
			tc.mod(&other)
			assert.False(t, SameShape(base, other))
			assert.False(t, SameShape(other, base), "the refusal is symmetric")
		})
	}
}
