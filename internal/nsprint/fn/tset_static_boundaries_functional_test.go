//go:build functional

package fn

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// These are raw JSON requests sent to the production Lua validator fragment
// loaded by tsetValidatorProbe. No Go wire or Mem admission participates.
func TestTSetStaticBoundsRawLua(t *testing.T) {
	t.Parallel()
	c, ctx := tsetValidatorProbe(t)
	type boundary struct {
		name     string
		limit    int
		request  func(int) (string, string)
		overCode string
	}
	cases := []boundary{
		{"entries", 256, func(n int) (string, string) {
			entries := make([]any, n)
			for i := range entries {
				entries[i] = rowsEntry("t", []string{})
			}
			return "step", rawStep(entries, nil)
		}, "LIMIT"},
		{"ids_per_entry", 2000, func(n int) (string, string) {
			return "step", rawStep([]any{guardEntry(names("id", 0, n))}, nil)
		}, "LIMIT"},
		{"member_candidates", 2000, func(n int) (string, string) {
			return "step", rawStep([]any{
				createEntry(names("id", 0, 1000), nil),
				createEntry(names("id", 1000, n-1000), nil),
			}, nil)
		}, "LIMIT"},
		{"guard_members", 4000, func(n int) (string, string) {
			return "step", rawStep([]any{
				guardEntry(names("id", 0, 2000)),
				guardEntry(names("id", 2000, 1000)),
				guardEntry(names("id", 3000, n-3000)),
			}, nil)
		}, "LIMIT"},
		{"ordinary_rows", 100, func(n int) (string, string) {
			return "step", rawStep([]any{rowsEntry("t", names("r", 0, n))}, nil)
		}, "LIMIT"},
		{"advance_rowset_union", 1024, func(n int) (string, string) {
			guarded := make([]any, 500)
			for i := range guarded {
				guarded[i] = map[string]any{"row": fmt.Sprintf("r%d", i), "rank": fmt.Sprint(i)}
			}
			add := append([]string{"r0"}, names("r", 500, n-500)...)
			return "step", rawStep([]any{
				map[string]any{"kind": "rowset", "t": "t", "rows": guarded},
				map[string]any{"kind": "advance", "from": "0"},
				rowsEntry("t", add),
			}, map[string]any{"op": "advance-rowset-union", "intent": "static boundary rowset union"})
		}, "LIMIT"},
		{"id_bytes", 256, func(n int) (string, string) {
			return "step", rawStep([]any{guardEntry([]string{strings.Repeat("a", n)})}, nil)
		}, "LIMIT"},
		{"row_bytes", 256, func(n int) (string, string) {
			return "step", rawStep([]any{rowsEntry("t", []string{strings.Repeat("a", n)})}, nil)
		}, "LIMIT"},
		{"column_bytes", 256, func(n int) (string, string) {
			e := createEntry([]string{"a"}, nil)
			e["to"] = "r:" + strings.Repeat("a", n)
			return "step", rawStep([]any{e}, nil)
		}, "LIMIT"},
		{"table_bytes", 256, func(n int) (string, string) {
			return "step", rawStep([]any{rowsEntry(strings.Repeat("a", n), []string{})}, nil)
		}, "LIMIT"},
		{"field_name_bytes", 256, func(n int) (string, string) {
			return "step", rawStep([]any{createEntry([]string{"a"}, map[string]string{strings.Repeat("a", n): "v"})}, nil)
		}, "LIMIT"},
		{"op_bytes", 256, func(n int) (string, string) {
			return "step", rawStep([]any{}, map[string]any{"op": strings.Repeat("a", n), "intent": "i"})
		}, "LIMIT"},
		{"space_bytes", 256, func(n int) (string, string) {
			return "step", rawStep([]any{}, map[string]any{"space": strings.Repeat("a", n)})
		}, "LIMIT"},
		{"set_fields", 128, func(n int) (string, string) {
			return "step", rawStep([]any{createEntry([]string{"a"}, fields("f", 0, n))}, nil)
		}, "LIMIT"},
		{"each_fields", 128, func(n int) (string, string) {
			e := createEntry([]string{"a"}, nil)
			e["each"] = []any{fields("f", 0, n)}
			return "step", rawStep([]any{e}, nil)
		}, "LIMIT"},
		{"set_each_union", 128, func(n int) (string, string) {
			e := createEntry([]string{"a"}, fields("f", 0, 64))
			e["each"] = []any{fields("f", 64, n-64)}
			return "step", rawStep([]any{e}, nil)
		}, "LIMIT"},
		{"unset_occurrences", 128, func(n int) (string, string) {
			return "step", rawStep([]any{map[string]any{"kind": "move", "t": "t", "from": "r:c", "ids": []string{"a"}, "unset": repeated("f", n)}}, nil)
		}, "LIMIT"},
		{"before_fields_occurrences", 128, func(n int) (string, string) {
			e := guardEntry([]string{"a"})
			e["before_fields"] = repeated("f", n)
			return "step", rawStep([]any{e}, nil)
		}, "LIMIT"},
		{"field_value_bytes", 65536, func(n int) (string, string) {
			return "step", rawStep([]any{createEntry([]string{"a"}, map[string]string{"f": strings.Repeat("x", n)})}, nil)
		}, "LIMIT"},
		{"result_bytes", 4096, func(n int) (string, string) {
			return "step", rawStep([]any{}, map[string]any{"result": strings.Repeat("x", n)})
		}, "LIMIT"},
		{"intent_bytes", 65536, func(n int) (string, string) {
			return "step", rawStep([]any{}, map[string]any{"op": "o", "intent": strings.Repeat("x", n)})
		}, "LIMIT"},
		{"notes", 100, func(n int) (string, string) {
			notes := make([]any, n)
			for i := range notes {
				notes[i] = note([]string{})
			}
			return "step", rawStep([]any{}, map[string]any{"op": "o", "intent": "i", "notes": notes})
		}, "LIMIT"},
		{"about_occurrences", 4000, func(n int) (string, string) {
			return "step", rawStep([]any{}, map[string]any{"op": "o", "intent": "i", "notes": []any{
				note(repeated("a", 2000)),
				note(repeated("a", 1000)),
				note(repeated("a", n-3000)),
			}})
		}, "LIMIT"},
		{"about_distinct_per_note", 2000, func(n int) (string, string) {
			return "step", rawStep([]any{}, map[string]any{"op": "o", "intent": "i", "notes": []any{note(names("a", 0, n))}})
		}, "LIMIT"},
		{"tables", 4, func(n int) (string, string) {
			entries := make([]any, n)
			for i := range entries {
				entries[i] = rowsEntry(fmt.Sprintf("t%d", i), []string{})
			}
			return "step", rawStep(entries, nil)
		}, "LIMIT"},
		{"json_depth", 16, func(n int) (string, string) {
			var nested any = 0
			for i := 5; i < n; i++ {
				nested = []any{nested}
			}
			tn := map[string]any{"line": map[string]any{"kind": "note", "meta": map[string]any{"v": nested}}, "about": []string{}}
			return "step", rawStep([]any{}, map[string]any{"op": "o", "intent": "i", "notes": []any{tn}})
		}, "REQUEST"},
		{"write_cells", 20000, func(n int) (string, string) {
			maxima := make([]int, n)
			for i := range maxima {
				maxima[i] = 1
			}
			return "step", rawStep([]any{map[string]any{"kind": "count", "t": "t", "cells": repeated("r:c", n), "max": maxima}}, nil)
		}, "LIMIT"},
		{"read_queries", 1024, func(n int) (string, string) {
			queries := make([]any, n)
			for i := range queries {
				queries[i] = map[string]any{"kind": "rows", "t": "t"}
			}
			return "read", rawRead(queries)
		}, "LIMIT"},
		{"read_ids", 10000, func(n int) (string, string) {
			return "read", rawRead([]any{map[string]any{"kind": "ids", "t": "t", "ids": repeated("a", n)}})
		}, "LIMIT"},
		{"read_cells", 20000, func(n int) (string, string) {
			return "read", rawRead([]any{map[string]any{"kind": "count", "t": "t", "cells": repeated("r:c", n)}})
		}, "LIMIT"},
		{"read_fields", 128, func(n int) (string, string) {
			return "read", rawRead([]any{map[string]any{"kind": "ids", "t": "t", "ids": []string{"a"}, "fields": repeated("f", n)}})
		}, "LIMIT"},
		{"range_limit", 2000, func(n int) (string, string) {
			return "read", rawRead([]any{map[string]any{"kind": "range", "t": "t", "cell": "r:c", "min": "0", "max": "+inf", "limit": n}})
		}, "LIMIT"},
		{"lines_limit", 5000, func(n int) (string, string) {
			return "read", rawRead([]any{map[string]any{"kind": "lines", "after_seq": "0", "limit": n}})
		}, "LIMIT"},
		{"lines_ids_limit", 200000, func(n int) (string, string) {
			return "read", rawRead([]any{map[string]any{"kind": "lines", "after_seq": "0", "limit": 1, "ids_limit": n}})
		}, "LIMIT"},
		{"cardlines_limit", 500, func(n int) (string, string) {
			return "read", rawRead([]any{map[string]any{"kind": "cardlines", "abouts": []string{"a"}, "limit": n}})
		}, "LIMIT"},
		{"cardlines_abouts", 2000, func(n int) (string, string) {
			return "read", rawRead([]any{map[string]any{"kind": "cardlines", "abouts": repeated("a", n), "limit": 1}})
		}, "LIMIT"},
		{"cardlines_fields", 128, func(n int) (string, string) {
			return "read", rawRead([]any{map[string]any{"kind": "cardlines", "abouts": []string{"a"}, "limit": 1, "fields": repeated("f", n)}})
		}, "LIMIT"},
		{"done_ops", 2000, func(n int) (string, string) {
			ops := make([]any, n)
			for i := range ops {
				ops[i] = map[string]any{"epoch": "0", "op": fmt.Sprintf("o%d", i), "intent_digest": strings.Repeat("a", 40)}
			}
			return "read", rawRead([]any{map[string]any{"kind": "done", "ops": ops}})
		}, "LIMIT"},
		{"write_request_bytes", 4 << 20, func(n int) (string, string) {
			return "step", paddedRequest(rawStep([]any{}, nil), n)
		}, "LIMIT"},
		{"read_request_bytes", 4 << 20, func(n int) (string, string) {
			return "read", paddedRequest(rawRead([]any{map[string]any{"kind": "rows", "t": "t"}}), n)
		}, "LIMIT"},
	}
	if len(cases) != 40 {
		t.Fatalf("static boundary inventory has %d rows, want 40", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, n := range []int{tc.limit - 1, tc.limit, tc.limit + 1} {
				operation, raw := tc.request(n)
				got, err := c.FCall(ctx, "ns_tset_validator_probe", nil, operation, raw).Text()
				if err != nil {
					t.Fatalf("%s at %d: %v", tc.name, n, err)
				}
				if n <= tc.limit {
					if !json.Valid([]byte(got)) {
						t.Errorf("%s at %d: static validator refused %q", tc.name, n, got)
					}
				} else if got != tc.overCode {
					t.Errorf("%s at %d: got %q, want %s", tc.name, n, got, tc.overCode)
				}
			}
		})
	}
	if size, err := c.DBSize(ctx).Result(); err != nil || size != 0 {
		t.Fatalf("static validator wrote %d keys: %v", size, err)
	}
}

func rawStep(entries []any, extra map[string]any) string {
	req := map[string]any{"epoch": "0", "space": "s:", "entries": entries}
	for k, v := range extra {
		req[k] = v
	}
	return mustRawJSON(req)
}

func rawRead(queries []any) string {
	return mustRawJSON(map[string]any{"epoch": "0", "space": "s:", "queries": queries})
}

func mustRawJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func paddedRequest(raw string, size int) string {
	if size < len(raw) {
		panic("static boundary request base exceeds requested size")
	}
	return raw + strings.Repeat(" ", size-len(raw))
}

func names(prefix string, start, count int) []string {
	out := make([]string, count)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d", prefix, start+i)
	}
	return out
}

func repeated(value string, count int) []string {
	out := make([]string, count)
	for i := range out {
		out[i] = value
	}
	return out
}

func fields(prefix string, start, count int) map[string]string {
	out := make(map[string]string, count)
	for i := 0; i < count; i++ {
		out[fmt.Sprintf("%s%d", prefix, start+i)] = "v"
	}
	return out
}

func rowsEntry(table string, add []string) map[string]any {
	return map[string]any{"kind": "rows", "t": table, "add": add}
}

func guardEntry(ids []string) map[string]any {
	return map[string]any{"kind": "guard", "t": "t", "from": "r:c", "ids": ids}
}

func createEntry(ids []string, set map[string]string) map[string]any {
	scores := repeated("1", len(ids))
	e := map[string]any{"kind": "create", "t": "t", "to": "r:c", "ids": ids, "scores": scores}
	if set != nil {
		e["set"] = set
	}
	return e
}

func note(about []string) map[string]any {
	return map[string]any{"line": map[string]any{"kind": "note", "meta": map[string]any{}}, "about": about}
}
