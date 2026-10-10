// sum.go holds the sum verb: its flags, its run and the helpers only it uses.

package main

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/mas-bandwidth/nova-tools/pkg/bounded"
)

// cmdSum ASSERTS NOTHING and is never a gate. It exits 0 whenever it ran, including over a
// month with gaps, because answering is its job and missing=<n> is the answer.
func cmdSum(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := newFlagSet("sum")
	out := fs.String("out", "", "directory holding daily token files")
	month := fs.String("month", "", "month to sum as YYYY-MM")
	max := fs.Int("max", bounded.Default, "maximum rows to print; 0 prints all")
	s, code, ok := start(fs, args, "SUM", stdout, stderr)
	if !ok {
		return code
	}
	r := &refusals{token: "SUM", s: s}
	r.required("out", *out, wantsOut)
	switch {
	case *month == "":
		r.add("--month is required; it wants " + wantsMonth + "; refusing to guess")
	case !validMonth(*month):
		r.add("--month is not a month: " + *month + "; it wants " + wantsMonth)
	}
	checkMax(r, *max)
	if len(r.list) > 0 {
		return r.print(stderr)
	}
	sm, err := tokens.SumMonth(*out, *month)
	if err != nil {
		r.add(err.Error())
		return r.print(stderr)
	}
	turns := tokens.Dash
	if sm.HaveTurn {
		turns = strconv.Itoa(sm.Turns)
	}
	first, last := tokens.Dash, tokens.Dash
	if len(sm.Days) > 0 {
		first, last = sm.Days[0], sm.Days[len(sm.Days)-1]
	}
	fmt.Fprintln(s.out(), s.line("SUM", "MONTH", "", "month", *month, "at", stamp(now), "build", buildVersion(),
		"days", len(sm.Days), "first", first, "last", last, "missing", len(sm.Missing), "rows", sm.Rows, "turns", count(turns)))

	widen := "nova-tokens sum --out " + *out + " --month " + *month + " --max 0"
	pairs := s.list(false, *max, "SUM", "pair", widen)
	for _, p := range sm.Pairs {
		pairs.Line(s.line("SUM", "PAIR", "", append(append([]any{"model", p.Model, "repo", p.Repo}, aggFields(p.Agg)...), "days", p.Agg.Days())...))
	}
	pairs.More()
	models := s.list(false, *max, "SUM", "model", widen)
	for _, m := range sm.Models {
		models.Line(s.line("SUM", "MODEL", "", append(append([]any{"model", m.Model}, aggFields(m.Agg)...), "repos", m.Agg.Keys())...))
	}
	models.More()
	fmt.Fprintln(s.out(), s.line("SUM", "TOTAL", "", append(aggFields(sm.Total), "turns", count(turns), "pairs", len(sm.Pairs), "models", len(sm.Models))...))
	counts := []any{"month", *month, "days", len(sm.Days), "missing", len(sm.Missing), "pairs", len(sm.Pairs), "models", len(sm.Models), "nonutc", sm.Total.NonUTC}
	fmt.Fprintf(s.out(), "SUM OK%s\n", s.factFields(counts...))
	return s.done(0, *max)
}

// aggFields is the five totals, the rough count, the per-column dash counts and the
// non-UTC count: everything a reader needs to know what a total does NOT cover.
func aggFields(a *tokens.Agg) []any {
	return []any{"input", count(a.Cell(tokens.Input)), "output", count(a.Cell(tokens.Output)), "cache_write", count(a.Cell(tokens.CacheWrite)),
		"cache_read", count(a.Cell(tokens.CacheRead)), "reasoning", count(a.Cell(tokens.Reasoning)), "rough", a.Rough,
		"dashes", fmt.Sprintf("%d,%d,%d,%d,%d", a.Dashes[tokens.Input], a.Dashes[tokens.Output], a.Dashes[tokens.CacheWrite],
			a.Dashes[tokens.CacheRead], a.Dashes[tokens.Reasoning]), "nonutc", a.NonUTC}
}
