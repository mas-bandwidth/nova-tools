//go:build functional

package ntable_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/pkg/ntable"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBindRequiresAnActualJSONArray(t *testing.T) {
	t.Parallel()
	for _, rows := range []string{`{}`, `{"x":{"key":"a"}}`, `{"1":{"key":"a"}}`, `null`, `"[]"`, `true`, `[null]`, `["a"]`, `[{"key":"a"},null]`} {
		t.Run(rows, func(t *testing.T) {
			t.Parallel()
			_, c := live(t)
			ctx := context.Background()
			cols, _ := ntable.ParseColumns("a")
			require.NoError(t, ntable.Create(ctx, c, ntable.Table{Name: "t", Columns: cols}, now))
			_, err := ntable.RowAdd(ctx, c, "t", "keep", ntable.RowSpec{})
			require.NoError(t, err)
			before := storeImage(t, c)
			body := `{"fields":{"order":"a","col:a":"count:sum:0:","footer":""},"rows":` + rows + `}`
			got, err := c.FCall(ctx, ntable.FnBind, nil, "t", body, `{"epoch":"0"}`).Slice()
			assert.True(t, replyOpens(got, err, "REFUSED", "ROW"), "want ROW refusal, got %v %v", got, err)
			require.Equal(t, before, storeImage(t, c), "invalid row list changed store")
		})
	}
	// Empty arrays remain the way to declare a table without rows. JSON field
	// spelling, order, whitespace and strings containing syntax are immaterial.
	for _, tail := range []string{`"rows":[]`, `"rows" : [ ]`, `"r\u006fws":[]`, `"ignored":{"rows":{}},"rows":[]`, `"ignored":"\"rows\":[{}]", "rows":[]`} {
		t.Run(tail, func(t *testing.T) {
			t.Parallel()
			_, c := live(t)
			body := `{"fields":{"order":"a","col:a":"count:sum:0:","footer":""},` + tail + `}`
			got, err := c.FCall(context.Background(), ntable.FnBind, nil, "t", body, `{"epoch":"0"}`).Slice()
			require.True(t, replyOpens(got, err, "OK"), "empty array refused: %v %v", got, err)
		})
	}
}

func TestInvalidUTF8RowIdentityRefusesBeforeJSONOrMutation(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"bad\xff", "bad\xc0\xaf", "bad\xed\xa0\x80", "bad\xf4\x90\x80\x80", "bad\xe2\x82"} {
		t.Run(fmt.Sprintf("%x", bad), func(t *testing.T) {
			t.Parallel()
			_, c := live(t)
			ctx := context.Background()
			cols, _ := ntable.ParseColumns("a")
			tab := ntable.Table{Name: "t", Columns: cols}
			newTable(t, c, tab)
			// This is the real, distinct name encoding/json would silently substitute.
			var replacement string
			encoded, _ := json.Marshal(bad)
			require.NoError(t, json.Unmarshal(encoded, &replacement))
			_, err := ntable.RowsAdd(ctx, c, "t", []string{replacement, "last"})
			require.NoError(t, err)
			before := storeImage(t, c)
			calls := map[string]func() error{
				"row add":   func() error { _, err := ntable.RowAdd(ctx, c, "t", bad, ntable.RowSpec{}); return err },
				"rows add":  func() error { _, err := ntable.RowsAdd(ctx, c, "t", []string{"new", bad}); return err },
				"rows hide": func() error { _, err := ntable.RowsHide(ctx, c, "t", true, []string{bad}); return err },
				"row order": func() error { _, err := ntable.Set(ctx, c, "t", ntable.SetOpts{RowOrder: []string{bad}}); return err },
				"row move": func() error {
					_, err := ntable.Set(ctx, c, "t", ntable.SetOpts{RowMove: &ntable.Reorder{Item: bad, Place: ntable.Place{Where: "last"}}})
					return err
				},
				"row move reference": func() error {
					_, err := ntable.Set(ctx, c, "t", ntable.SetOpts{RowMove: &ntable.Reorder{Item: "last", Place: ntable.Place{Where: "before", Ref: bad}}})
					return err
				},
				"bind":        func() error { tab.Rows = []ntable.Row{ntable.NewRow(tab, bad)}; return ntable.Bind(ctx, c, tab, now) },
				"raw row add": func() error { return inputRefusal(c.FCall(ctx, ntable.FnRowAdd, nil, "t", bad, `{}`, `{"epoch":"0"}`)) },
				"raw rows add": func() error {
					return inputRefusal(c.FCall(ctx, ntable.FnRowsAdd, nil, "t", `{"rows":["`+bad+`"],"spec":{}}`, `{"epoch":"0"}`))
				},
				"raw bind": func() error {
					return inputRefusal(c.FCall(ctx, ntable.FnBind, nil, "t", `{"fields":{"order":"a","col:a":"count:sum:0:","footer":""},"rows":[{"key":"`+bad+`"}]}`, `{"epoch":"0"}`))
				},
			}
			for name, call := range calls {
				assert.Error(t, call(), "%s accepted invalid UTF-8", name)
				require.Equal(t, before, storeImage(t, c), "%s changed state", name)
			}
		})
	}
}

func inputRefusal(cmd *redis.Cmd) error {
	got, err := cmd.Slice()
	if err != nil {
		return nil // A server error is not the required structured ROW refusal.
	}
	if len(got) > 1 && got[0] == "REFUSED" && got[1] == "ROW" {
		return fmt.Errorf("row refused")
	}
	return nil
}

// The single-call write paths bound the stored text the batch path already
// bounds (docs/SPEC-NOVA-TABLE.md, "Manifest, identity and bounds": the
// bounds table; table.lua T.limits holds the same numbers): a row set's text
// value, a row add's label, exclude or owner, and a view's title or summary
// over the field value bound, and a member id over the member id bound, are
// refused LIMIT naming the bound and the count with the store untouched, and
// exactly at the bound each call succeeds. The title carries the view path's
// call at the bound; a summary at the bound would have to name a count column
// that long, and the summary's own at-the-bound column ("a") succeeds beside
// it.
func TestStoreRefusesOverlongRowTextLabelTitleAndMemberIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		name   string
		limit  string // the limit's name as refused
		bound  int
		member string // the member the refusal names, "" for none
		at     func(c *redis.Client, n int) error
		over   func(c *redis.Client, n int) error
	}{
		{"row set value", "field value bytes", ntable.LimitFieldValueBytes, "r",
			func(c *redis.Client, n int) error {
				_, err := ntable.RowSet(ctx, c, "t", "r", map[string]string{"status": strings.Repeat("v", n)})
				return err
			},
			func(c *redis.Client, n int) error {
				_, err := ntable.RowSet(ctx, c, "t", "r", map[string]string{"status": strings.Repeat("w", n)})
				return err
			}},
		{"row add label", "field value bytes", ntable.LimitFieldValueBytes, "r3",
			func(c *redis.Client, n int) error {
				_, err := ntable.RowAdd(ctx, c, "t", "r2", ntable.RowSpec{Label: strings.Repeat("v", n)})
				return err
			},
			func(c *redis.Client, n int) error {
				_, err := ntable.RowAdd(ctx, c, "t", "r3", ntable.RowSpec{Label: strings.Repeat("w", n)})
				return err
			}},
		{"row add exclude", "field value bytes", ntable.LimitFieldValueBytes, "r5",
			func(c *redis.Client, n int) error {
				_, err := ntable.RowAdd(ctx, c, "t", "r4", ntable.RowSpec{Exclude: strings.Repeat("v", n)})
				return err
			},
			func(c *redis.Client, n int) error {
				_, err := ntable.RowAdd(ctx, c, "t", "r5", ntable.RowSpec{Exclude: strings.Repeat("w", n)})
				return err
			}},
		{"row add owner", "field value bytes", ntable.LimitFieldValueBytes, "r7",
			func(c *redis.Client, n int) error {
				_, err := ntable.RowAdd(ctx, c, "t", "r6", ntable.RowSpec{Owner: strings.Repeat("v", n)})
				return err
			},
			func(c *redis.Client, n int) error {
				_, err := ntable.RowAdd(ctx, c, "t", "r7", ntable.RowSpec{Owner: strings.Repeat("w", n)})
				return err
			}},
		{"view set title", "field value bytes", ntable.LimitFieldValueBytes, "",
			func(c *redis.Client, n int) error {
				return ntable.ViewSet(ctx, c, ntable.View{Name: "v", Tables: []string{"t"}, Title: strings.Repeat("v", n), Summary: "a"})
			},
			func(c *redis.Client, n int) error {
				return ntable.ViewSet(ctx, c, ntable.View{Name: "v", Tables: []string{"t"}, Title: strings.Repeat("w", n), Summary: "a"})
			}},
		{"view set summary", "field value bytes", ntable.LimitFieldValueBytes, "",
			func(c *redis.Client, n int) error {
				return ntable.ViewSet(ctx, c, ntable.View{Name: "v", Tables: []string{"t"}, Title: "T", Summary: "a"})
			},
			func(c *redis.Client, n int) error {
				return ntable.ViewSet(ctx, c, ntable.View{Name: "v", Tables: []string{"t"}, Title: "T", Summary: strings.Repeat("w", n)})
			}},
		{"member id", "member id bytes", ntable.LimitMemberIDBytes, "",
			func(c *redis.Client, n int) error {
				_, err := ntable.CellAdd(ctx, c, "t", "r", "a", strings.Repeat("v", n), 1)
				return err
			},
			func(c *redis.Client, n int) error {
				_, err := ntable.CellAdd(ctx, c, "t", "r", "a", strings.Repeat("w", n), 1)
				return err
			}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, c := live(t)
			cols, err := ntable.ParseColumns("status:text:none,a")
			require.NoError(t, err)
			newTable(t, c, ntable.Table{Name: "t", Columns: cols})
			_, err = ntable.RowAdd(ctx, c, "t", "r", ntable.RowSpec{})
			require.NoError(t, err)
			require.NoError(t, tc.at(c, tc.bound), "exactly at the bound refused")
			before := storeImage(t, c)
			err = tc.over(c, tc.bound+1)
			var le *ntable.LimitError
			require.ErrorAs(t, err, &le, "one byte over the bound accepted: %v", err)
			require.ErrorIs(t, err, ntable.ErrLimit, "one byte over the bound: %v", err)
			assert.Equal(t, tc.limit, le.Name, "refusal %v", err)
			assert.Equal(t, tc.bound, le.Bound, "refusal %v", err)
			assert.Equal(t, tc.bound+1, le.Observed, "refusal %v", err)
			assert.Equal(t, tc.member, le.Member, "refusal %v", err)
			assert.False(t, le.AtLeast, "refusal %v", err)
			assert.Equal(t, before, storeImage(t, c), "a refused over-bound call changed the store")
		})
	}
}

// A summary exactly at the field value bound passes the length check: it is
// refused SUMMARY (it names no count column), not LIMIT, while one byte over
// is refused LIMIT before the column is looked up.
func TestViewSetSummaryAtFieldValueBoundIsNotALimitRefusal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, c := live(t)
	cols, err := ntable.ParseColumns("status:text:none,a")
	require.NoError(t, err)
	newTable(t, c, ntable.Table{Name: "t", Columns: cols})
	set := func(n int) error {
		return ntable.ViewSet(ctx, c, ntable.View{Name: "v", Tables: []string{"t"}, Title: "T", Summary: strings.Repeat("w", n)})
	}
	var le *ntable.LimitError
	err = set(ntable.LimitFieldValueBytes)
	require.Error(t, err, "a summary naming no count column was accepted")
	require.False(t, errors.As(err, &le), "a summary exactly at the bound was refused LIMIT: %v", err)
	err = set(ntable.LimitFieldValueBytes + 1)
	require.ErrorAs(t, err, &le, "a summary one byte over the bound was not refused LIMIT: %v", err)
	assert.Equal(t, ntable.LimitFieldValueBytes+1, le.Observed)
}

// The raw writer and Go API must agree on arbitrary bytes and valid Unicode;
// the independent oracle is the standard UTF-8 decoder plus the ASCII rule.
func TestRawAndGoRowKeysAgreeOnBytes(t *testing.T) {
	t.Parallel()
	_, c := live(t)
	ctx := context.Background()
	cols, _ := ntable.ParseColumns("a")
	require.NoError(t, ntable.Create(ctx, c, ntable.Table{Name: "t", Columns: cols}, now))
	cases := []string{"", "日本語", "é", "\u0080", "\u07ff", "\u0800", "\ud7ff", "\ue000", "\uffff", "\U00010000", "\U0010ffff", "�", "\x00", "\x7f", "\xc2", "\xe0\xa0", "\xf0\x90\x80", "\xed\xa0\x80", "\xf4\x90\x80\x80"}
	r := rand.New(rand.NewPCG(42, 19))
	for i := 0; i < 200; i++ {
		b := make([]byte, r.IntN(8)+1)
		for j := range b {
			b[j] = byte(r.IntN(256))
		}
		cases = append(cases, string(b), string(rune(r.IntN(0x110000))))
	}
	for _, key := range cases {
		valid := key != "" && utf8.ValidString(key)
		for _, b := range []byte(key) {
			if b < 32 || b == 127 {
				valid = false
			}
		}
		require.Equal(t, valid, ntable.ValidRowKey(key), "Go row key %q", key)
		revision := c.HGet(ctx, ntable.RevisionKey("t"), "n").Val()
		got, err := c.FCall(ctx, ntable.FnRowAdd, nil, "t", key, `{}`, `{"epoch":"0"}`).Slice()
		require.NoError(t, err, "raw row key %q: %v", key, err)
		accepted := len(got) > 0 && got[0] == "ROW"
		require.Equal(t, valid, accepted, "raw row key %q: %v want accepted=%v", key, got, valid)
		if !valid {
			require.Equal(t, revision, c.HGet(ctx, ntable.RevisionKey("t"), "n").Val(), "invalid %q did not refuse cleanly: %v", key, got)
			require.GreaterOrEqual(t, len(got), 2, "invalid %q did not refuse cleanly: %v", key, got)
			require.Equal(t, "ROW", got[1], "invalid %q did not refuse cleanly: %v", key, got)
		}
	}
}

// The view family (the 'views' SET and every 'view:<name>' hash that
// ns_view_set/state/del own) is reserved like the table family: a member
// prefix or an epoch key inside it is a CONFIG refusal at create, and no
// definition is written (security#78 finding 5).
func TestCreateRefusesAMemberPrefixOrEpochKeyInTheViewFamily(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, prefix, epochKey string }{
		{"member-prefix view:", "view:", ""},
		{"member-prefix view:x:", "view:x:", ""},
		{"epoch-key views", "", "views"},
		{"epoch-key view:today", "", "view:today"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, c := live(t)
			ctx := context.Background()
			cols, _ := ntable.ParseColumns("done")
			err := ntable.Create(ctx, c, ntable.Table{Name: "t", Columns: cols, MemberPrefix: tc.prefix, EpochKey: tc.epochKey}, now)
			var r *ntable.Refusal
			require.ErrorAs(t, err, &r)
			assert.Equal(t, "CONFIG", r.Code)
			n, err := c.Exists(ctx, ntable.DefKey("t")).Result()
			require.NoError(t, err)
			assert.Zero(t, n, "a refused create wrote a definition")
		})
	}
	t.Run("a binding into the view family", func(t *testing.T) {
		t.Parallel()
		_, c := live(t)
		ctx := context.Background()
		cols, _ := ntable.ParseColumns("a")
		require.NoError(t, ntable.Create(ctx, c, ntable.Table{Name: "t", Columns: cols}, now))
		for _, key := range []string{"views", "view:today"} {
			_, err := ntable.RowAdd(ctx, c, "t", "r", ntable.RowSpec{Binds: map[string]string{"a": key}})
			var r *ntable.Refusal
			require.ErrorAs(t, err, &r, "a binding to %s", key)
			assert.Equal(t, "OWNEDALIAS", r.Code, "a binding to %s", key)
		}
	})
	t.Run("a plain member prefix still creates", func(t *testing.T) {
		t.Parallel()
		_, c := live(t)
		cols, _ := ntable.ParseColumns("done")
		require.NoError(t, ntable.Create(context.Background(), c, ntable.Table{Name: "t", Columns: cols, MemberPrefix: "job:member:"}, now))
	})
}
