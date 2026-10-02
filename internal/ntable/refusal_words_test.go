package ntable

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The words of a refusal and the small checks a manifest's values pass are
// pure functions; each table below pins one rule at its boundary.

// A refusal names where it happened: the table, and what of the table the
// operation held, each only when it held it.
func TestAnOperationsLocationNamesOnlyWhatItHolds(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		op   operation
		want string
	}{
		{"a table", operation{table: "t"}, `table "t"`},
		{"a row and a column", operation{table: "t", row: "r", col: "c"}, `table "t" row "r" column "c"`},
		{"a member", operation{table: "t", member: "m"}, `table "t" member "m"`},
		{"a batch and its read set", operation{table: "t", batch: true, opID: "op", readSet: true}, `table "t" batch "op" read set`},
		{"a view", operation{table: "v", view: true}, `view "v"`},
		{"the views", operation{view: true}, `views`},
	} {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, c.op.location(), "%s: location", c.name)
		})
	}
}

// A string is a number for a manifest when it starts with a digit or a minus
// sign and reads as one: no plus sign, no bare point, no sign alone.
func TestAStringIsANumberTokenOnlyWhenItStartsLikeOne(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		tok  any
		want bool
	}{
		{"12", true}, {"-3", true}, {"0.5", true}, {"1e3", true},
		{"", false}, {"-", false}, {"x", false}, {"+1", false}, {".5", false}, {"1x", false},
		{json.Number("4"), true}, {json.Number("x"), false}, {2.5, true}, {7, true}, {true, false}, {nil, false},
	} {
		t.Run(fmt.Sprintf("%#v", c.tok), func(t *testing.T) {
			assert.Equal(t, c.want, isNumberToken(c.tok), "isNumberToken(%#v)", c.tok)
		})
	}
}

// A word is nonempty and holds no control character, the delete character
// among them.
func TestAWordHoldsNoControlCharacter(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		s    string
		want bool
	}{
		{"a b", true}, {"é", true}, {"~", true},
		{"", false}, {"a\x00b", false}, {"a\nb", false}, {"a\x1fb", false}, {"a\x7fb", false},
	} {
		t.Run(fmt.Sprintf("%q", c.s), func(t *testing.T) {
			assert.Equal(t, c.want, word(c.s), "word(%q)", c.s)
		})
	}
}

// A refusal carries a value of at most 64 bytes whole, and a longer one as its
// first 32 bytes and its length.
func TestABoundedValueIsWholeUpTo64Bytes(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		n    int
		want string
	}{
		{0, ""},
		{64, strings.Repeat("a", 64)},
		{65, strings.Repeat("a", 32) + "...(65 bytes)"},
	} {
		t.Run(fmt.Sprintf("%d bytes", c.n), func(t *testing.T) {
			assert.Equal(t, c.want, bounded(strings.Repeat("a", c.n)), "bounded of %d bytes", c.n)
		})
	}
}

// A value of the wrong type is named by the last word of its place, except a
// place that ends in an index, which is named whole.
func TestAWrongTypeNamesItsPlace(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		where, want string
	}{
		{"schema", "schema must be a number, found a string"},
		{"members[0].id", "id must be a number, found a string"},
		{"members[0]", "members[0] must be a number, found a string"},
	} {
		t.Run(c.where, func(t *testing.T) {
			err := wrongType(c.where, "a number", "x")
			assert.Equal(t, c.want, err.(*ManifestError).Msg, "wrongType at %q", c.where)
		})
	}
}

// A create over a dropped table whose saved definition differs is refused
// with both ways out: the saved definition as the create that brings the
// table back, and drop --definition, which forgets it, as the remedy (USE
// defect 3: the remedy named set, which refuses a dropped table, and set's
// remedy named create again). A present table keeps set as its remedy.
func TestACreateOverADroppedDefinitionNamesBothWaysOut(t *testing.T) {
	t.Parallel()
	op := operation{table: "tmp1"}
	for _, c := range []struct {
		name  string
		reply []any
		want  []string
	}{
		{"a present table", []any{"REFUSED", "EXISTS"},
			[]string{`table "tmp1": exists with another definition; run: nova-table set 'tmp1' --columns <columns>`}},
		{"a dropped table", []any{"REFUSED", "EXISTS", "DROPPED", "0", "order", "a,b", "col:a", "count:sum:0:", "col:b", "text:none:7:Bee", "footer", ""},
			[]string{`table "tmp1": exists with another definition: the table was dropped and keeps its saved definition until drop --definition;`,
				`to bring it back as it was: nova-table create tmp1 --columns a:count:sum,b:text:none:Bee --width b=7 --epoch 0; run:`,
				`; run: nova-table drop 'tmp1' --definition`}},
		{"a dropped table at a later epoch", []any{"REFUSED", "EXISTS", "DROPPED", "3", "order", "a", "col:a", "count:sum:0:", "footer", "total"},
			[]string{`nova-table create tmp1 --columns a:count:sum --footer total --epoch 3; run:`, `; run: nova-table drop 'tmp1' --definition --epoch 3`}},
		// the saved routing comes back with it, each word shell-quoted, and the
		// epoch is the store's exact uint64, past what a float64 holds
		{"a dropped table with its own routing", []any{"REFUSED", "EXISTS", "DROPPED", "18446744073709551615", "order", "a", "col:a", "text:none:0:it's done", "footer", "all rows",
			"epoch_key", "work epoch", "epoch_field", "n", "member_prefix", "it's members:"},
			[]string{`nova-table create tmp1 --columns 'a:text:none:it'"'"'s done' --footer 'all rows' --epoch-key 'work epoch' --epoch-field n --member-prefix 'it'"'"'s members:' --epoch 18446744073709551615; run:`}},
		{"an epoch that is not a number", []any{"REFUSED", "EXISTS", "DROPPED", "x1", "order", "a", "col:a", "count:sum:0:"},
			[]string{`the store's epoch "x1" is not an unsigned integer`}},
	} {
		t.Run(c.name, func(t *testing.T) {
			err := op.refused(c.reply)
			assert.ErrorIs(t, err, ErrExists, "%s: %v", c.name, err)
			for _, w := range c.want {
				assert.Contains(t, fmt.Sprint(err), w, "%s", c.name)
			}
		})
	}
}

// A drop's reply is the rows it removed and then the views that name the
// table, each of which refuses to render without it, so the drop names them
// (USE defect 4: the drop said nothing, and every such view broke).
func TestADropsReplyNamesTheViewsThatNameTheTable(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		reply []any
		want  Dropped
	}{
		{"no view", []any{"OK", int64(3)}, Dropped{Rows: 3}},
		{"two views", []any{"OK", int64(0), "v1", "v2"}, Dropped{Views: []string{"v1", "v2"}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := dropped(c.reply)
			assert.NoError(t, err, "%s", c.name)
			assert.Equal(t, c.want, got, "%s", c.name)
		})
	}
}
