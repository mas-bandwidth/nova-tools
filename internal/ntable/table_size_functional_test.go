//go:build functional

package ntable_test

// A table has at most LimitColumns columns and LimitRows rows: create, set and
// row add refuse the one past the bound by name, and nothing is written.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func wideColumns(n int) []ntable.Column {
	cols := make([]ntable.Column, n)
	for i := range cols {
		cols[i] = ntable.Column{Name: fmt.Sprintf("c%d", i), Projection: "count", Fold: "sum"}
	}
	return cols
}

func requireLimit(t *testing.T, what string, err error, name string, bound, observed int) {
	t.Helper()
	var le *ntable.LimitError
	if assert.ErrorAs(t, err, &le, what) {
		assert.Equal(t, ntable.LimitError{Name: name, Bound: bound, Observed: observed}, ntable.LimitError{Name: le.Name, Bound: le.Bound, Observed: le.Observed}, what)
	}
}

func TestTableColumnsAreBounded(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := t.Context()
	require.NoError(t, ntable.Create(ctx, c, ntable.Table{Name: "wide", Columns: wideColumns(ntable.LimitColumns)}, now), "a table at the bound")
	// the definition is not written past the bound: the server refuses one more, by name
	before := storeImage(t, c)
	_, err := ntable.Set(ctx, c, "wide", ntable.SetOpts{ColAdd: &ntable.Column{Name: "extra", Projection: "count", Fold: "sum"}})
	requireLimit(t, "col add past the bound", err, "columns per table", ntable.LimitColumns, ntable.LimitColumns+1)
	assert.Equal(t, before, storeImage(t, c), "a refused col add changed the store")

	// create past the bound, through the library (refused before the store is asked)
	err = ntable.Create(ctx, c, ntable.Table{Name: "wider", Columns: wideColumns(ntable.LimitColumns + 1)}, now)
	requireLimit(t, "create past the bound (library)", err, "columns per table", ntable.LimitColumns, ntable.LimitColumns+1)

	// and through the function itself, as a raw call
	fields := []string{`"footer":""`, `"created_at":"` + now.Format(time.RFC3339) + `"`}
	names := make([]string, ntable.LimitColumns+1)
	for i := range names {
		names[i] = fmt.Sprintf("c%d", i)
		fields = append(fields, fmt.Sprintf(`"col:c%d":"count:sum:0:"`, i))
	}
	fields = append(fields, `"order":"`+strings.Join(names, ",")+`"`)
	ans, err := c.FCall(ctx, ntable.FnCreate, []string{ntable.DefKey("wider")}, "wider", "{"+strings.Join(fields, ",")+"}", `{"epoch":"0","actor":"","fence":"","idem":""}`).Slice()
	assert.True(t, replyOpens(ans, err, "REFUSED", "LIMIT", "columns per table"), "raw create past the bound: %v %v", trunc(ans), err)
	assert.Equal(t, int64(0), c.Exists(ctx, ntable.DefKey("wider")).Val(), "a refused create wrote the template")
}

func TestTableRowsAreBounded(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := t.Context()
	cols, err := ntable.ParseColumns("a,b")
	require.NoError(t, err)
	require.NoError(t, ntable.Create(ctx, c, ntable.Table{Name: "tall", Columns: cols}, now))
	rows := func(from, n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("r%d", from+i)
		}
		return out
	}
	// one call past the bound writes nothing
	before := storeImage(t, c)
	_, err = ntable.RowsAdd(ctx, c, "tall", rows(0, ntable.LimitRows+1))
	requireLimit(t, "rows add past the bound", err, "rows per table", ntable.LimitRows, ntable.LimitRows+1)
	assert.Equal(t, before, storeImage(t, c), "a refused rows add changed the store")
	// the bound is reachable
	_, err = ntable.RowsAdd(ctx, c, "tall", rows(0, ntable.LimitRows))
	require.NoError(t, err, "a table at the bound")
	// one more row is refused, whatever verb adds it; a row that exists is not a new row
	_, err = ntable.RowAdd(ctx, c, "tall", "one-more", ntable.RowSpec{})
	requireLimit(t, "row add past the bound", err, "rows per table", ntable.LimitRows, ntable.LimitRows+1)
	_, err = ntable.RowAdd(ctx, c, "tall", "r7", ntable.RowSpec{})
	assert.NoError(t, err, "re-adding a row that exists at the bound")
}

// A bind leaves the table with exactly the rows it names, so the row bound holds
// for it as for row add: refused before anything changes, through the library and
// through the function itself.
func TestBindRowsAreBounded(t *testing.T) {
	t.Parallel()
	c, _ := store(t)
	ctx := t.Context()
	cols, err := ntable.ParseColumns("a,b")
	require.NoError(t, err)
	table := func(n int) ntable.Table {
		rows := make([]ntable.Row, n)
		for i := range rows {
			rows[i] = ntable.Row{Key: fmt.Sprintf("r%d", i), Cells: make([]ntable.Cell, len(cols))}
		}
		return ntable.Table{Name: "bound", Columns: cols, Rows: rows}
	}
	require.NoError(t, ntable.Bind(ctx, c, table(3), now), "a bind of three rows")
	before := storeImage(t, c)

	// the library refuses before it sends
	err = ntable.Bind(ctx, c, table(ntable.LimitRows+1), now)
	requireLimit(t, "bind past the bound (library)", err, "rows per table", ntable.LimitRows, ntable.LimitRows+1)
	assert.Equal(t, before, storeImage(t, c), "a refused bind changed the store")

	// the function refuses too, as a raw call with the definition the table has
	fields := c.HGetAll(ctx, ntable.DefKey("bound")).Val()
	def, _ := json.Marshal(fields)
	rows := make([]string, ntable.LimitRows+1)
	for i := range rows {
		rows[i] = fmt.Sprintf(`{"key":"r%d"}`, i)
	}
	body := `{"fields":` + string(def) + `,"rows":[` + strings.Join(rows, ",") + `]}`
	ans, err := c.FCall(ctx, ntable.FnBind, []string{ntable.DefKey("bound")}, "bound", body, `{"epoch":"0","actor":"","fence":"","idem":""}`).Slice()
	require.True(t, replyOpens(ans, err, "REFUSED", "LIMIT", "rows per table"), "raw bind past the bound: %v %v", trunc(ans), err)
	require.GreaterOrEqual(t, len(ans), 5, "raw bind past the bound: %v", trunc(ans))
	require.Equal(t, fmt.Sprint(ntable.LimitRows), fmt.Sprint(ans[3]), "raw bind past the bound: bound")
	require.Equal(t, fmt.Sprint(ntable.LimitRows+1), fmt.Sprint(ans[4]), "raw bind past the bound: observed")
	assert.Equal(t, before, storeImage(t, c), "a raw bind refused for its rows changed the store")
}
