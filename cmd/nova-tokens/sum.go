// sum.go holds the sum verb: its flags, its run and the helpers only it uses.

package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// sumVerb declares the sum verb.
func sumVerb(now time.Time) tool.Verb {
	return tool.Verb{
		Name:    "sum",
		Token:   "SUM",
		Usage:   "sum --out <dir> --month <YYYY-MM> [--max <n>]",
		Example: "sum --out ./out --month 2026-09",
		Effect:  tool.Inspection,
		Flags: func(f *tool.Flags) {
			f.Required("out", wantsOut)
			f.Required("month", wantsMonth)
			f.Max()
			f.Check(func(c *tool.Call) {
				month := c.Str("month")
				switch {
				case month != "" && !validMonth(month):
					c.Problem("--month is not a month: " + month + "; it wants " + wantsMonth)
				}
			})
		},
		Run: func(c *tool.Call) *tool.Out {
			return runSum(c, now)
		},
	}
}

// runSum ASSERTS NOTHING and is never a gate. It exits 0 whenever it ran, including over a
// month with gaps, because answering is its job and missing=<n> is the answer.
func runSum(c *tool.Call, now time.Time) *tool.Out {
	outDir := c.Str("out")
	month := c.Str("month")

	sm, err := tokens.SumMonth(outDir, month)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	turns := tokens.Dash
	if sm.HaveTurn {
		turns = strconv.Itoa(sm.Turns)
	}
	first, last := tokens.Dash, tokens.Dash
	if len(sm.Days) > 0 {
		first, last = sm.Days[0], sm.Days[len(sm.Days)-1]
	}
	o := tool.Done()
	o.Item("month", "month", month, "at", stamp(now), "build", buildVersion(),
		"days", len(sm.Days), "first", first, "last", last, "missing", len(sm.Missing), "rows", sm.Rows, "turns", count(turns))

	for _, p := range sm.Pairs {
		o.Item("pair", append(append([]any{"model", p.Model, "repo", p.Repo}, aggFields(p.Agg)...), "days", p.Agg.Days())...)
	}
	for _, m := range sm.Models {
		o.Item("model", append(append([]any{"model", m.Model}, aggFields(m.Agg)...), "repos", m.Agg.Keys())...)
	}
	o.Item("total", append(aggFields(sm.Total), "turns", count(turns), "pairs", len(sm.Pairs), "models", len(sm.Models))...)

	o.Fact("month", month).
		Fact("days", len(sm.Days)).
		Fact("missing", len(sm.Missing)).
		Fact("pairs", len(sm.Pairs)).
		Fact("models", len(sm.Models)).
		Fact("nonutc", sm.Total.NonUTC)

	return o
}

// aggFields is the five totals, the rough count, the per-column dash counts and the
// non-UTC count: everything a reader needs to know what a total does NOT cover.
func aggFields(a *tokens.Agg) []any {
	return []any{"input", count(a.Cell(tokens.Input)), "output", count(a.Cell(tokens.Output)), "cache_write", count(a.Cell(tokens.CacheWrite)),
		"cache_read", count(a.Cell(tokens.CacheRead)), "reasoning", count(a.Cell(tokens.Reasoning)), "rough", a.Rough,
		"dashes", fmt.Sprintf("%d,%d,%d,%d,%d", a.Dashes[tokens.Input], a.Dashes[tokens.Output], a.Dashes[tokens.CacheWrite],
			a.Dashes[tokens.CacheRead], a.Dashes[tokens.Reasoning]), "nonutc", a.NonUTC}
}
