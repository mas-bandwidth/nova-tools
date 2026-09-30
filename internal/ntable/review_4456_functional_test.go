//go:build functional

package ntable_test

import (
	"context"
	"strings"
	"testing"

	nsstore "github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func review4456Image(t *testing.T, c *redis.Client) map[string]string {
	t.Helper()
	ctx := context.Background()
	keys, err := c.Keys(ctx, "*").Result()
	require.NoError(t, err)
	out := map[string]string{}
	for _, key := range keys {
		value, err := c.Dump(ctx, key).Result()
		require.NoError(t, err)
		out[key] = value
	}
	return out
}

func TestReview4456RefusalsAreAtomic(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"row-set-late-bad-column", "footer-before-existing-rename", "raw-invalid-definition", "hide-occupied-column"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			_, c := live(t)
			ctx := context.Background()
			cols, err := ntable.ParseColumns("a:text:none,b")
			require.NoError(t, err)
			tb := ntable.Table{Name: "demo", Columns: cols}
			newTable(t, c, tb).rows("r").cell("r", "b", "m1", 1)
			busy := tb
			busy.Name = "busy"
			newTable(t, c, busy)
			before := review4456Image(t, c)
			switch mode {
			case "row-set-late-bad-column":
				_, err = ntable.RowSet(ctx, c, tb.Name, "r", map[string]string{"a": "changed", "z": "bad"})
			case "footer-before-existing-rename":
				label := "changed"
				_, err = ntable.Set(ctx, c, tb.Name, ntable.SetOpts{Footer: &label, Rename: "busy"})
			case "raw-invalid-definition":
				reply, callErr := c.FCall(ctx, ntable.FnSet, []string{ntable.DefKey(tb.Name)}, tb.Name, `{"columns":{"order":"a,missing","col:a":"text:none:0:"}}`, `{"epoch":"0"}`).Slice()
				require.NoError(t, callErr)
				require.True(t, replyOpens(reply, nil, "REFUSED", "DEFINITION"), "raw reply=%v", reply)
				require.Equal(t, before, review4456Image(t, c), "REFUSED DEFINITION changed stored definition or cells")
				return
			case "hide-occupied-column":
				changed, parseErr := ntable.ParseColumns("a:text:none,b:text:none")
				require.NoError(t, parseErr)
				_, err = ntable.Set(ctx, c, tb.Name, ntable.SetOpts{Columns: changed})
			}
			require.Error(t, err, "unsafe mutation accepted")
			require.Equal(t, before, review4456Image(t, c), "refusal partially changed the store: %v", err)
		})
	}
}

func TestReview4456RowMetadataKeepsTextValues(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	cols, err := ntable.ParseColumns("a:text:none,b")
	require.NoError(t, err)
	tb := ntable.Table{Name: "demo", Columns: cols}
	newTable(t, c, tb).rows("r")
	_, err = ntable.RowSet(ctx, c, tb.Name, "r", map[string]string{"a": "up"})
	require.NoError(t, err)
	_, err = ntable.RowAdd(ctx, c, tb.Name, "r", ntable.RowSpec{Label: "new label"})
	require.NoError(t, err)
	got, err := ntable.Read(ctx, c, tb.Name)
	require.NoError(t, err)
	require.Equal(t, "up", got.Rows[0].Texts["a"], "row metadata edit lost text: %#v", got.Rows[0].Texts)
}

func TestReview4456FormulaUnreadStaysUnknown(t *testing.T) {
	t.Parallel()
	cols, err := ntable.ParseColumns("a,b,p:pct(a):pooled")
	require.NoError(t, err)
	tb := ntable.Table{Name: "demo", Columns: cols}
	unknown := ntable.NewRow(tb, "unknown")
	unknown.Cells[0].Count = 1
	unknown.Cells[1].Unread = true
	known := ntable.NewRow(tb, "known")
	known.Cells[0].Count = 2
	tb.Rows = []ntable.Row{unknown, known}
	lines := strings.Split(strings.TrimSpace(ntable.Render(tb, ntable.RenderOpts{Title: "demo"})), "\n")
	last := func(s string) string { parts := strings.Split(s, "|"); return strings.TrimSpace(parts[len(parts)-1]) }
	if last(lines[2]) != "?" || last(lines[len(lines)-1]) != "?" {
		t.Fatalf("unknown formula input became an empty value or a known average:\n%s", strings.Join(lines, "\n"))
	}
}

func TestReview4456LeadingTextDoesNotHideRowIdentity(t *testing.T) {
	t.Parallel()
	cols, err := ntable.ParseColumns("status:text:none,n")
	require.NoError(t, err)
	tb := ntable.Table{Name: "friends", Columns: cols}
	row := ntable.NewRow(tb, "alpha")
	row.Texts = map[string]string{"status": "up"}
	tb.Rows = []ntable.Row{row}
	rendered := ntable.Render(tb, ntable.RenderOpts{Title: tb.Name})
	require.Contains(t, rendered, "alpha", "row identity vanished when its first text cell was set:\n%s", rendered)
}

func TestReview4456DeclaredWriterCanUseEdits(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	cols, err := ntable.ParseColumns("a:text:none,b")
	require.NoError(t, err)
	require.NoError(t, ntable.Create(ctx, c, ntable.Table{Name: "demo", Columns: cols}, now))
	_, err = ntable.RowAdd(ctx, c, "demo", "r", ntable.RowSpec{})
	require.NoError(t, err)
	require.NoError(t, nsstore.DeployACLs(ctx, c))
	require.NoError(t, c.ACLSetUser(ctx, "ns-coordinator", ">review-password").Err())
	opts := *c.Options()
	opts.Username, opts.Password = "ns-coordinator", "review-password"
	writer := redis.NewClient(&opts)
	t.Cleanup(func() {
		assert.NoError(t, writer.Close())
	})
	_, err = ntable.RowSet(ctx, writer, "demo", "r", map[string]string{"a": "up"})
	assert.NoError(t, err, "declared writer cannot row set")
	footer := "all"
	_, err = ntable.Set(ctx, writer, "demo", ntable.SetOpts{Footer: &footer})
	assert.NoError(t, err, "declared writer cannot set")
	_, err = ntable.RowsAdd(ctx, writer, "demo", []string{"s", "t"})
	require.NoError(t, err)
	_, err = ntable.RowsHide(ctx, writer, "demo", true, []string{"s", "t"})
	require.NoError(t, err)
	_, err = ntable.CellsAdd(ctx, writer, "demo", "s", "b", 3, []string{"m1", "m2"})
	require.NoError(t, err)
	_, err = ntable.Set(ctx, writer, "demo", ntable.SetOpts{Rename: "renamed"})
	require.NoError(t, err)
	require.NoError(t, ntable.ViewSet(ctx, writer, ntable.View{Name: "v", Tables: []string{"renamed"}, Summary: "b"}))
	require.NoError(t, c.ACLSetUser(ctx, "ns-table", ">reader-password").Err())
	readerOpts := *c.Options()
	readerOpts.Username, readerOpts.Password = "ns-table", "reader-password"
	reader := redis.NewClient(&readerOpts)
	t.Cleanup(func() {
		assert.NoError(t, reader.Close())
	})
	if v, err := ntable.ViewGet(ctx, reader, "v"); err != nil || v.Summary != "b" {
		t.Fatalf("declared reader view=%+v %v", v, err)
	}
	_, err = ntable.Read(ctx, reader, "renamed")
	require.NoError(t, err)
	if names, err := ntable.ViewList(ctx, reader); err != nil || len(names) != 1 || names[0] != "v" {
		t.Fatalf("reader view list: %v %v", names, err)
	}
	if loc, err := ntable.MemberFind(ctx, reader, "renamed", "m1"); err != nil || loc.State != "placed" || loc.Row != "s" || loc.Column != "b" {
		t.Fatalf("reader member find: %+v %v", loc, err)
	}
	_, err = ntable.ViewDelete(ctx, reader, "v")
	require.Error(t, err, "reader deleted view")

	require.Error(t, ntable.ViewSet(ctx, reader, ntable.View{Name: "v", Tables: []string{"renamed"}}), "reader wrote view")
	if n, err := ntable.ViewDelete(ctx, writer, "v"); err != nil || n != 1 {
		t.Fatalf("writer delete: %d %v", n, err)
	}

}
