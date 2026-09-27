//go:build functional

package ntable_test

import (
	"context"
	"encoding/json"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/redis/go-redis/v9"
	"reflect"
	"testing"
)

func TestReviewRemainingRefusalsAtomic(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"rows-add-late-type", "rows-hide-late-type", "set-late-acl", "repair-occupied", "shape-text", "view-registry-type"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			_, c := live(t)
			ctx := context.Background()
			cols, err := ntable.ParseColumns("a,b,status:text:none,p:pct(a):pooled")
			if err != nil {
				t.Fatal(err)
			}
			if err := ntable.Create(ctx, c, ntable.Table{Name: "t", Columns: cols}, now); err != nil {
				t.Fatal(err)
			}
			if _, err := ntable.RowsAdd(ctx, c, "t", []string{"r", "s"}); err != nil {
				t.Fatal(err)
			}
			var call func() error
			switch mode {
			case "rows-add-late-type", "rows-hide-late-type":
				if err := c.Set(ctx, ntable.RowKey("t", "s"), "bad", 0).Err(); err != nil {
					t.Fatal(err)
				}
				if mode == "rows-add-late-type" {
					call = func() error { _, e := ntable.RowsAdd(ctx, c, "t", []string{"new", "s"}); return e }
				} else {
					call = func() error { _, e := ntable.RowsHide(ctx, c, "t", true, []string{"r", "s"}); return e }
				}
			case "set-late-acl":
				if err := c.ACLSetUser(ctx, "limited", "on", ">pw", "~*", "&*", "+@all", "-rename").Err(); err != nil {
					t.Fatal(err)
				}
				o := *c.Options()
				o.Username, o.Password = "limited", "pw"
				writer := redis.NewClient(&o)
				t.Cleanup(func() { _ = writer.Close() })
				footer := "changed"
				call = func() error {
					_, e := ntable.Set(ctx, writer, "t", ntable.SetOpts{Footer: &footer, Rename: "next"})
					return e
				}
			case "repair-occupied":
				if _, err := ntable.CellAdd(ctx, c, "t", "r", "a", "member", 7); err != nil {
					t.Fatal(err)
				}
				if err := c.HSet(ctx, ntable.DefKey("t"), "col:p", "pct(a):avg:0:").Err(); err != nil {
					t.Fatal(err)
				}
				next, err := ntable.ParseColumns("b")
				if err != nil {
					t.Fatal(err)
				}
				call = func() error { _, e := ntable.Set(ctx, c, "t", ntable.SetOpts{Columns: next}); return e }
			case "shape-text":
				if _, err := ntable.RowSet(ctx, c, "t", "r", map[string]string{"status": "keep me"}); err != nil {
					t.Fatal(err)
				}
				next, err := ntable.ParseColumns("a,b")
				if err != nil {
					t.Fatal(err)
				}
				call = func() error { _, e := ntable.Set(ctx, c, "t", ntable.SetOpts{Columns: next}); return e }
			case "view-registry-type":
				if err := c.Set(ctx, "views", "bad", 0).Err(); err != nil {
					t.Fatal(err)
				}
				call = func() error { return ntable.ViewSet(ctx, c, ntable.View{Name: "v", Tables: []string{"t"}}) }
			}
			image := func() map[string]string {
				out := map[string]string{}
				keys, e := c.Keys(ctx, "*").Result()
				if e != nil {
					t.Fatal(e)
				}
				for _, key := range keys {
					out[key] = c.Dump(ctx, key).Val()
				}
				return out
			}
			before := image()
			err = call()
			after := image()
			if err == nil || !reflect.DeepEqual(before, after) {
				changed := []string{}
				for k, v := range after {
					if before[k] != v {
						changed = append(changed, k)
					}
				}
				for k := range before {
					if _, ok := after[k]; !ok {
						changed = append(changed, k)
					}
				}
				wire, _ := json.Marshal(changed)
				t.Fatalf("unsafe result err=%v changed keys=%s", err, wire)
			}
		})
	}
}
