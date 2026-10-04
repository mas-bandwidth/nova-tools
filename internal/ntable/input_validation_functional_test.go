//go:build functional

package ntable_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"testing"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/ntable"
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
