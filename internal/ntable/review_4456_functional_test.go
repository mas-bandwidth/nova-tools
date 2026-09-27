//go:build functional

package ntable_test

import (
	"context"
	nsstore "github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"reflect"
	"strings"
	"testing"
)

func review4456Image(t *testing.T, c *redis.Client) map[string]string {
	t.Helper()
	ctx := context.Background()
	keys, err := c.Keys(ctx, "*").Result()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, key := range keys {
		value, err := c.Dump(ctx, key).Result()
		if err != nil {
			t.Fatal(err)
		}
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
			if err != nil {
				t.Fatal(err)
			}
			tb := ntable.Table{Name: "demo", Columns: cols}
			if err := ntable.Create(ctx, c, tb, now); err != nil {
				t.Fatal(err)
			}
			if _, err := ntable.RowAdd(ctx, c, tb.Name, "r", ntable.RowSpec{}); err != nil {
				t.Fatal(err)
			}
			if _, err := ntable.CellAdd(ctx, c, tb.Name, "r", "b", "m1", 1); err != nil {
				t.Fatal(err)
			}
			busy := tb
			busy.Name = "busy"
			if err := ntable.Create(ctx, c, busy, now); err != nil {
				t.Fatal(err)
			}
			before := review4456Image(t, c)
			switch mode {
			case "row-set-late-bad-column":
				_, err = ntable.RowSet(ctx, c, tb.Name, "r", map[string]string{"a": "changed", "z": "bad"})
			case "footer-before-existing-rename":
				label := "changed"
				_, err = ntable.Set(ctx, c, tb.Name, ntable.SetOpts{Footer: &label, Rename: "busy"})
			case "raw-invalid-definition":
				reply, callErr := c.FCall(ctx, ntable.FnSet, []string{ntable.DefKey(tb.Name)}, tb.Name, `{"columns":{"order":"a,missing","col:a":"text:none:0:"}}`, `{"epoch":"0"}`).Slice()
				if callErr != nil {
					t.Fatal(callErr)
				}
				if len(reply) < 2 || reply[0] != "REFUSED" || reply[1] != "DEFINITION" {
					t.Fatalf("raw reply=%v", reply)
				}
				if !reflect.DeepEqual(before, review4456Image(t, c)) {
					t.Fatal("REFUSED DEFINITION changed stored definition or cells")
				}
				return
			case "hide-occupied-column":
				changed, parseErr := ntable.ParseColumns("a:text:none,b:text:none")
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				_, err = ntable.Set(ctx, c, tb.Name, ntable.SetOpts{Columns: changed})
			}
			if err == nil {
				t.Fatal("unsafe mutation accepted")
			}
			if !reflect.DeepEqual(before, review4456Image(t, c)) {
				t.Fatalf("refusal partially changed the store: %v", err)
			}
		})
	}
}

func TestReview4456RowMetadataKeepsTextValues(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	cols, err := ntable.ParseColumns("a:text:none,b")
	if err != nil {
		t.Fatal(err)
	}
	tb := ntable.Table{Name: "demo", Columns: cols}
	if err := ntable.Create(ctx, c, tb, now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, tb.Name, "r", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowSet(ctx, c, tb.Name, "r", map[string]string{"a": "up"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, tb.Name, "r", ntable.RowSpec{Label: "new label"}); err != nil {
		t.Fatal(err)
	}
	got, err := ntable.Read(ctx, c, tb.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows[0].Texts["a"] != "up" {
		t.Fatalf("row metadata edit lost text: %#v", got.Rows[0].Texts)
	}
}

func TestReview4456FormulaUnreadStaysUnknown(t *testing.T) {
	t.Parallel()
	cols, err := ntable.ParseColumns("a,b,p:pct(a):pooled")
	if err != nil {
		t.Fatal(err)
	}
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
	if err != nil {
		t.Fatal(err)
	}
	tb := ntable.Table{Name: "friends", Columns: cols}
	row := ntable.NewRow(tb, "alpha")
	row.Texts = map[string]string{"status": "up"}
	tb.Rows = []ntable.Row{row}
	rendered := ntable.Render(tb, ntable.RenderOpts{Title: tb.Name})
	if !strings.Contains(rendered, "alpha") {
		t.Fatalf("row identity vanished when its first text cell was set:\n%s", rendered)
	}
}

func TestReview4456DeclaredWriterCanUseEdits(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	cols, err := ntable.ParseColumns("a:text:none,b")
	if err != nil {
		t.Fatal(err)
	}
	if err := ntable.Create(ctx, c, ntable.Table{Name: "demo", Columns: cols}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowAdd(ctx, c, "demo", "r", ntable.RowSpec{}); err != nil {
		t.Fatal(err)
	}
	if err := nsstore.DeployACLs(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := c.ACLSetUser(ctx, "ns-coordinator", ">review-password").Err(); err != nil {
		t.Fatal(err)
	}
	opts := *c.Options()
	opts.Username, opts.Password = "ns-coordinator", "review-password"
	writer := redis.NewClient(&opts)
	t.Cleanup(func() {
		if err := writer.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := ntable.RowSet(ctx, writer, "demo", "r", map[string]string{"a": "up"}); err != nil {
		t.Errorf("declared writer cannot row set: %v", err)
	}
	footer := "all"
	if _, err := ntable.Set(ctx, writer, "demo", ntable.SetOpts{Footer: &footer}); err != nil {
		t.Errorf("declared writer cannot set: %v", err)
	}
	if _, err := ntable.RowsAdd(ctx, writer, "demo", []string{"s", "t"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.RowsHide(ctx, writer, "demo", true, []string{"s", "t"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.CellsAdd(ctx, writer, "demo", "s", "b", 3, []string{"m1", "m2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ntable.Set(ctx, writer, "demo", ntable.SetOpts{Rename: "renamed"}); err != nil {
		t.Fatal(err)
	}
	if err := ntable.ViewSet(ctx, writer, ntable.View{Name: "v", Tables: []string{"renamed"}, Summary: "b"}); err != nil {
		t.Fatal(err)
	}
	if err := c.ACLSetUser(ctx, "ns-table", ">reader-password").Err(); err != nil {
		t.Fatal(err)
	}
	readerOpts := *c.Options()
	readerOpts.Username, readerOpts.Password = "ns-table", "reader-password"
	reader := redis.NewClient(&readerOpts)
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	})
	if v, err := ntable.ViewGet(ctx, reader, "v"); err != nil || v.Summary != "b" {
		t.Fatalf("declared reader view=%+v %v", v, err)
	}
	if _, err := ntable.Read(ctx, reader, "renamed"); err != nil {
		t.Fatal(err)
	}
	if err := ntable.ViewSet(ctx, reader, ntable.View{Name: "v", Tables: []string{"renamed"}}); err == nil {
		t.Fatal("reader wrote view")
	}

}
