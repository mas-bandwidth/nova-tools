//go:build functional

package ntable_test

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
)

// The order verbs against a real store (tla/TableOrder.tla is the model):
// a move is a permutation, a refusal writes nothing, a standing sort places
// every later row, and one column added or removed leaves the rest alone.

func orderTable(t *testing.T, c *redis.Client) {
	t.Helper()
	ctx := context.Background()
	cols, err := ntable.ParseColumns("a,b,note:text:none,p:pct(a):pooled")
	if err != nil {
		t.Fatal(err)
	}
	if err := ntable.Create(ctx, c, ntable.Table{Name: "t", Columns: cols}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowsAdd(ctx, c, "t", []string{"m", "z", "c", "k"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellsAdd(ctx, c, "t", "z", "a", 1, []string{"x1", "x2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellAdd(ctx, c, "t", "c", "b", "x3", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowSet(ctx, c, "t", "k", map[string]string{"note": "Beta"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowSet(ctx, c, "t", "m", map[string]string{"note": "alpha"}); err != nil {
		t.Fatal(err)
	}
}

func orderOf(t *testing.T, c *redis.Client, name string) (rows, cols []string) {
	t.Helper()
	tab, err := ntable.Read(context.Background(), c, name)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range tab.Rows {
		rows = append(rows, r.Key)
	}
	for _, col := range tab.Columns {
		cols = append(cols, col.Name)
	}
	return rows, cols
}

// held is everything the table holds apart from order: every placed member
// with its cell and every text value.
func held(t *testing.T, c *redis.Client, name string) map[string]string {
	t.Helper()
	tab, err := ntable.Read(context.Background(), c, name)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, r := range tab.Rows {
		for _, col := range tab.Columns {
			if v := r.Texts[col.Name]; v != "" {
				out["text:"+r.Key+":"+col.Name] = v
			}
			if !col.HasSet() {
				continue
			}
			members, err := ntable.CellMembers(context.Background(), c, name, r.Key, col.Name)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range members {
				out["member:"+m.Member] = r.Key + ":" + col.Name
			}
		}
	}
	return out
}

func storeImage(t *testing.T, c *redis.Client) map[string]string {
	t.Helper()
	ctx := context.Background()
	keys, err := c.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, key := range keys {
		out[key], _ = imageOf(ctx, c, key)
	}
	return out
}

func at(where, ref string) ntable.Place { return ntable.Place{Where: where, Ref: ref} }

func TestOrderMovesArePermutations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	steps := []struct {
		name       string
		change     ntable.SetOpts
		rows, cols string
	}{
		{"row first", ntable.SetOpts{RowMove: &ntable.Reorder{Item: "k", Place: at("first", "")}}, "k,m,z,c", "a,b,note,p"},
		{"row last", ntable.SetOpts{RowMove: &ntable.Reorder{Item: "k", Place: at("last", "")}}, "m,z,c,k", "a,b,note,p"},
		{"row before", ntable.SetOpts{RowMove: &ntable.Reorder{Item: "k", Place: at("before", "z")}}, "m,k,z,c", "a,b,note,p"},
		{"row after", ntable.SetOpts{RowMove: &ntable.Reorder{Item: "m", Place: at("after", "c")}}, "k,z,c,m", "a,b,note,p"},
		{"row where it is", ntable.SetOpts{RowMove: &ntable.Reorder{Item: "m", Place: at("last", "")}}, "k,z,c,m", "a,b,note,p"},
		{"rows named first", ntable.SetOpts{RowOrder: []string{"m", "c"}}, "m,c,k,z", "a,b,note,p"},
		{"col after", ntable.SetOpts{ColMove: &ntable.Reorder{Item: "a", Place: at("after", "note")}}, "m,c,k,z", "b,note,a,p"},
		{"col first", ntable.SetOpts{ColMove: &ntable.Reorder{Item: "p", Place: at("first", "")}}, "m,c,k,z", "p,b,note,a"},
		{"sort by name", ntable.SetOpts{RowSort: &ntable.Sort{By: "name"}}, "c,k,m,z", "p,b,note,a"},
		{"sort by name desc", ntable.SetOpts{RowSort: &ntable.Sort{By: "name", Desc: true}}, "z,m,k,c", "p,b,note,a"},
		{"sort by count desc, ties by name", ntable.SetOpts{RowSort: &ntable.Sort{By: "a", Desc: true}}, "z,c,k,m", "p,b,note,a"},
		{"sort by text, case folded", ntable.SetOpts{RowSort: &ntable.Sort{By: "note"}}, "c,z,m,k", "p,b,note,a"},
		{"sort then move, one call", ntable.SetOpts{RowSort: &ntable.Sort{By: "name"}, RowMove: &ntable.Reorder{Item: "z", Place: at("first", "")}}, "z,c,k,m", "p,b,note,a"},
	}
	_, c := live(t)
	orderTable(t, c)
	before := held(t, c, "t")
	for _, s := range steps {
		if _, err := ntable.Set(ctx, c, "t", s.change); err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}
		rows, cols := orderOf(t, c, "t")
		if got := strings.Join(rows, ","); got != s.rows {
			t.Fatalf("%s: rows %s, want %s", s.name, got, s.rows)
		}
		if got := strings.Join(cols, ","); got != s.cols {
			t.Fatalf("%s: columns %s, want %s", s.name, got, s.cols)
		}
		if after := held(t, c, "t"); !reflect.DeepEqual(before, after) {
			t.Fatalf("%s: a move changed what the table holds: %v, was %v", s.name, after, before)
		}
	}
	if len(before) != 5 {
		t.Fatalf("the fixture holds 3 members and 2 texts, read %v", before)
	}
}

func TestOrderNoopLeavesANoopReceipt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, c := live(t)
	orderTable(t, c)
	var r ntable.Receipt
	if _, err := ntable.Set(ctx, c, "t", ntable.SetOpts{RowMove: &ntable.Reorder{Item: "m", Place: at("first", "")}}, ntable.WriteOptions{Receipt: &r}); err != nil {
		t.Fatal(err)
	}
	if r.Outcome != "noop" || r.After != r.Before+1 {
		t.Fatalf("a move to where the row is: outcome %q, revision %d -> %d", r.Outcome, r.Before, r.After)
	}
}

func TestStandingSortPlacesLaterRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, c := live(t)
	orderTable(t, c)
	if _, err := ntable.Set(ctx, c, "t", ntable.SetOpts{RowSort: &ntable.Sort{By: "label", Keep: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowsAdd(ctx, c, "t", []string{"d", "a"}); err != nil {
		t.Fatal(err)
	}
	// a label sorts the row, not its key
	if _, err := ntable.RowAdd(ctx, c, "t", "q", ntable.RowSpec{Label: "B side"}); err != nil {
		t.Fatal(err)
	}
	rows, _ := orderOf(t, c, "t")
	if got := strings.Join(rows, ","); got != "a,q,c,d,k,m,z" {
		t.Fatalf("standing sort by label: %s", got)
	}
	tab, err := ntable.Read(ctx, c, "t")
	if err != nil || tab.Sort != "label" {
		t.Fatalf("the definition names the standing sort: %q %v", tab.Sort, err)
	}
	// by hand is refused while the sort stands, and writes nothing
	image := storeImage(t, c)
	_, err = ntable.Set(ctx, c, "t", ntable.SetOpts{RowMove: &ntable.Reorder{Item: "z", Place: at("first", "")}})
	if err == nil || !strings.Contains(err.Error(), "row sort 't' --manual") {
		t.Fatalf("a move under a standing sort: %v", err)
	}
	if !reflect.DeepEqual(image, storeImage(t, c)) {
		t.Fatal("the refused move wrote")
	}
	if _, err := ntable.Set(ctx, c, "t", ntable.SetOpts{RowSort: &ntable.Sort{Manual: true}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.Set(ctx, c, "t", ntable.SetOpts{RowMove: &ntable.Reorder{Item: "z", Place: at("first", "")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowsAdd(ctx, c, "t", []string{"b"}); err != nil {
		t.Fatal(err)
	}
	rows, _ = orderOf(t, c, "t")
	if got := strings.Join(rows, ","); got != "z,a,q,c,d,k,m,b" {
		t.Fatalf("by hand again, a new row goes last: %s", got)
	}
}

func TestOneColumnAddedOrRemoved(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, c := live(t)
	orderTable(t, c)
	before := held(t, c, "t")
	col, err := ntable.ParseColumn("q:members:union")
	if err != nil {
		t.Fatal(err)
	}
	at := at("before", "b")
	if _, err := ntable.Set(ctx, c, "t", ntable.SetOpts{ColAdd: &col, ColAt: &at}); err != nil {
		t.Fatal(err)
	}
	if _, cols := orderOf(t, c, "t"); strings.Join(cols, ",") != "a,q,b,note,p" {
		t.Fatalf("col add --before b: %v", cols)
	}
	if _, err := ntable.Set(ctx, c, "t", ntable.SetOpts{Hide: []string{"q"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.Set(ctx, c, "t", ntable.SetOpts{ColDel: "q"}); err != nil {
		t.Fatal(err)
	}
	tab, err := ntable.Read(ctx, c, "t")
	if err != nil || len(tab.Hidden) != 0 {
		t.Fatalf("a removed column leaves the hidden list: %v %v", tab.Hidden, err)
	}
	if _, cols := orderOf(t, c, "t"); strings.Join(cols, ",") != "a,b,note,p" {
		t.Fatalf("col del: %v", cols)
	}
	if after := held(t, c, "t"); !reflect.DeepEqual(before, after) {
		t.Fatalf("adding and removing an empty column changed what the table holds: %v", after)
	}
}

func TestOrderRefusalsWriteNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dup, _ := ntable.ParseColumn("a")
	pct, _ := ntable.ParseColumn("r:pct(nope)")
	text, _ := ntable.ParseColumn("r:pct(note)")
	share, _ := ntable.ParseColumn("r:pct(a/a+nope)")
	sumText, _ := ntable.ParseColumn("r:sum(a+note)")
	cases := []struct {
		name   string
		change ntable.SetOpts
		want   string
	}{
		{"row that is not there", ntable.SetOpts{RowMove: &ntable.Reorder{Item: "nope", Place: at("first", "")}}, `row "nope": no such row`},
		{"neighbour that is not there", ntable.SetOpts{RowMove: &ntable.Reorder{Item: "m", Place: at("after", "nope")}}, `row "nope": no such row`},
		{"row after itself", ntable.SetOpts{RowMove: &ntable.Reorder{Item: "m", Place: at("after", "m")}}, "itself"},
		{"order names a missing row, late", ntable.SetOpts{RowOrder: []string{"k", "nope"}}, `row "nope": no such row`},
		{"column that is not there", ntable.SetOpts{ColMove: &ntable.Reorder{Item: "nope", Place: at("first", "")}}, `column "nope": no such column`},
		{"column neighbour not there", ntable.SetOpts{ColMove: &ntable.Reorder{Item: "a", Place: at("before", "nope")}}, `column "nope": no such column`},
		{"column already there", ntable.SetOpts{ColAdd: &dup}, "already there"},
		{"percentage of no column", ntable.SetOpts{ColAdd: &pct}, `col add 't' 'nope'`},
		{"percentage of a text column", ntable.SetOpts{ColAdd: &text}, `reads column "note", a text column`},
		{"named denominator with no column", ntable.SetOpts{ColAdd: &share}, `col add 't' 'nope'`},
		{"sum of a text column", ntable.SetOpts{ColAdd: &sumText}, `reads column "note", a text column`},
		{"remove a column a percentage reads", ntable.SetOpts{ColDel: "a"}, `col del 't' 'p'`},
		{"remove a column holding a member", ntable.SetOpts{ColDel: "b"}, `cell remove 't' 'c' 'b' 'x3'`},
		{"remove a column holding text", ntable.SetOpts{ColDel: "note"}, "clear it with row set first"},
		{"remove a column that is not there", ntable.SetOpts{ColDel: "nope"}, `column "nope": no such column`},
		{"sort by a column that is not there", ntable.SetOpts{RowSort: &ntable.Sort{By: "nope"}}, `column "nope": no such column`},
		{"sort by a percentage", ntable.SetOpts{RowSort: &ntable.Sort{By: "p"}}, "not p"},
		{"standing sort by a count", ntable.SetOpts{RowSort: &ntable.Sort{By: "a", Keep: true}}, "row sort 't' --by 'a'"},
		{"good footer, then a bad move", ntable.SetOpts{Footer: ptr("total"), RowMove: &ntable.Reorder{Item: "nope", Place: at("first", "")}}, "no such row"},
		{"good column move, then a bad column", ntable.SetOpts{ColMove: &ntable.Reorder{Item: "a", Place: at("last", "")}, ColDel: "nope"}, "no such column"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, c := live(t)
			orderTable(t, c)
			image := storeImage(t, c)
			_, err := ntable.Set(ctx, c, "t", tc.change)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal %v, want it to say %q", err, tc.want)
			}
			after := storeImage(t, c)
			if !reflect.DeepEqual(image, after) {
				var changed []string
				for k, v := range after {
					if image[k] != v {
						changed = append(changed, k)
					}
				}
				sort.Strings(changed)
				t.Fatalf("the refusal wrote: %v", changed)
			}
		})
	}
}

// A row key of the wrong type, met late in a sort by label, refuses whole.
func TestOrderLateWrongTypeWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, c := live(t)
	orderTable(t, c)
	if err := c.Del(ctx, ntable.RowKey("t", "z")).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(ctx, ntable.RowKey("t", "z"), "bad", 0).Err(); err != nil {
		t.Fatal(err)
	}
	image := storeImage(t, c)
	if _, err := ntable.Set(ctx, c, "t", ntable.SetOpts{RowSort: &ntable.Sort{By: "label", Desc: true}}); err == nil {
		t.Fatal("a sort over a row of the wrong type was accepted")
	}
	if !reflect.DeepEqual(image, storeImage(t, c)) {
		t.Fatal("the failed sort wrote")
	}
}

func ptr(s string) *string { return &s }
