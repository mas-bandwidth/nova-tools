//go:build functional

package ntable_test

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestReviewRemainingRefusalsAtomic(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"rows-add-late-type", "rows-hide-late-type", "set-late-acl", "repair-occupied", "shape-text", "view-registry-type"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			_, c := live(t)
			ctx := context.Background()
			cols, err := ntable.ParseColumns("a,b,status:text:none,p:pct(a):pooled")
			require.NoError(t, err)
			require.NoError(t, ntable.Create(ctx, c, ntable.Table{Name: "t", Columns: cols}, now))
			_, err = ntable.RowsAdd(ctx, c, "t", []string{"r", "s"})
			require.NoError(t, err)
			var call func() error
			switch mode {
			case "rows-add-late-type", "rows-hide-late-type":
				require.NoError(t, c.Set(ctx, ntable.RowKey("t", "s"), "bad", 0).Err())
				if mode == "rows-add-late-type" {
					call = func() error { _, e := ntable.RowsAdd(ctx, c, "t", []string{"new", "s"}); return e }
				} else {
					call = func() error { _, e := ntable.RowsHide(ctx, c, "t", true, []string{"r", "s"}); return e }
				}
			case "set-late-acl":
				require.NoError(t, c.ACLSetUser(ctx, "limited", "on", ">pw", "~*", "&*", "+@all", "-rename").Err())
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
				_, err := ntable.CellAdd(ctx, c, "t", "r", "a", "member", 7)
				require.NoError(t, err)
				require.NoError(t, c.HSet(ctx, ntable.DefKey("t"), "col:p", "pct(a):avg:0:").Err())
				next, err := ntable.ParseColumns("b")
				require.NoError(t, err)
				call = func() error { _, e := ntable.Set(ctx, c, "t", ntable.SetOpts{Columns: next}); return e }
			case "shape-text":
				_, err := ntable.RowSet(ctx, c, "t", "r", map[string]string{"status": "keep me"})
				require.NoError(t, err)
				next, err := ntable.ParseColumns("a,b")
				require.NoError(t, err)
				call = func() error { _, e := ntable.Set(ctx, c, "t", ntable.SetOpts{Columns: next}); return e }
			case "view-registry-type":
				require.NoError(t, c.Set(ctx, "views", "bad", 0).Err())
				call = func() error { return ntable.ViewSet(ctx, c, ntable.View{Name: "v", Tables: []string{"t"}}) }
			}
			image := func() map[string]string {
				out := map[string]string{}
				keys, e := c.Keys(ctx, "*").Result()
				require.NoError(t, e)
				for _, key := range keys {
					out[key] = c.Dump(ctx, key).Val()
				}
				return out
			}
			before := image()
			err = call()
			after := image()
			require.Error(t, err, "unsafe result: the call was accepted")
			require.Equal(t, before, after, "unsafe result err=%v: the refused call changed keys", err)
		})
	}
}
