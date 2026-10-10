// check.go holds the check verb: its flags, its run and the helpers only it uses.

package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/mas-bandwidth/nova-tools/pkg/bounded"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// cmdCheck is the GATE. It says FAILED on any malformed file, any malformed row, any missing
// day and any stray, and it prints the count line either way. A missing day is NAMED and
// never filled: nobody folded it, and this tool does not invent what nobody measured.
//
// What `missing` and `stray` MEAN is internal/tokens/check.go's paragraph, and the short
// of it is that a calendar gap and a person's README are counted here (gap=, notes=) and
// named only under --strict or a --no-spend list. A gate that cannot go green is a gate
// people learn to skip, and this one could not: 40 findings on reports/tokens, none of
// them work anybody would do.
func cmdCheck(args []string, stdout, stderr io.Writer, now time.Time) int {
	fs := newFlagSet("check")
	out := fs.String("out", "", "directory containing daily token files")
	max := fs.Int("max", bounded.Default, "maximum findings to print; 0 prints all")
	strict := fs.Bool("strict", false, "treat every gap and note as a finding")
	noSpend := fs.String("no-spend", "", "file listing UTC dates with no spend, one per line")
	through := fs.String("through", "", "require coverage through this UTC day, YYYY-MM-DD")
	allowEmpty := fs.Bool("allow-empty", false, "answer OK on an --out holding no day file; without it files=0 is FAILED, never a green over nothing")
	s, code, ok := start(fs, args, "CHECK", stdout, stderr)
	if !ok {
		return code
	}
	r := &refusals{token: "CHECK", s: s}
	r.required("out", *out, wantsOut)
	checkMax(r, *max)
	if *strict && strings.TrimSpace(*noSpend) != "" {
		r.add("--strict and --no-spend are two answers to one question: --strict names every calendar gap, --no-spend names the gaps your list does not account for; give one")
	}
	if strings.TrimSpace(*through) != "" {
		if !tokens.ValidDay(*through) {
			r.add("--through is not a day: " + *through + "; it wants YYYY-MM-DD (e.g. 2026-09-18)")
		}
	}
	opt := tokens.CheckOptions{Strict: *strict, Through: strings.TrimSpace(*through)}
	if strings.TrimSpace(*noSpend) != "" {
		days, err := tokens.ReadNoSpendFile(*noSpend)
		if err != nil {
			r.add("--no-spend " + *noSpend + ": " + err.Error())
		} else {
			opt.NoSpend = days
		}
	}
	if len(r.list) > 0 {
		return r.print(stderr)
	}
	res, err := tokens.Check(*out, opt)
	if err != nil {
		r.add(err.Error())
		return r.print(stderr)
	}
	remedyLine := "nova-tokens check --out " + *out + " --max 0"
	files := s.list(true, *max, "CHECK", "file", remedyLine)
	rowsList := s.list(true, *max, "CHECK", "row", remedyLine)
	missing := s.list(true, *max, "CHECK", "missing", remedyLine)
	strays := s.list(true, *max, "CHECK", "stray", remedyLine)
	for _, f := range res.Findings {
		reason := oneline.Cap(f.Reason, oneline.TailBytes)
		line := fmt.Sprintf("CHECK FAILED %s: %s", oneline.Escape(f.Path), oneline.Escape(reason))
		if f.Line > 0 {
			line = fmt.Sprintf("CHECK FAILED %s:%d: %s", oneline.Escape(f.Path), f.Line, oneline.Escape(reason))
		}
		kind, list := "file", files
		if f.Line > 2 {
			kind, list = "row", rowsList
		}
		list.Line(line)
		s.item(kind, "path", f.Path, "line", f.Line, "why", tool.Text(reason))
	}
	files.More()
	rowsList.More()
	for _, d := range res.Missing {
		missing.Line("CHECK MISSING date=" + oneline.Field(d))
		s.item("missing", "date", d)
	}
	missing.More()
	for _, p := range res.Strays {
		strays.Line("CHECK STRAY " + oneline.Escape(p))
		s.item("stray", "path", p)
	}
	strays.More()

	first, last := tokens.Dash, tokens.Dash
	if res.First != "" {
		first, last = res.First, res.Last
	}
	if res.Stale {
		fmt.Fprintf(s.err(), "CHECK FAILED stale last=%s through=%s\n", oneline.Field(last), oneline.Field(*through))
		s.item("stale", "last", last, "through", *through)
	}
	// A GATE THAT CANNOT GO RED IS NO GATE. The standard's Verb.Looks rule (files read):
	// check reads day files, so an --out holding none has nothing to pass, and a green
	// over nothing reads exactly like a green over a month. Without --allow-empty it is a
	// finding; --allow-empty is the one word that says the empty --out is deliberate.
	empty := res.Files == 0 && !*allowEmpty
	if empty {
		why := "looked at nothing: --out " + *out + " holds no day file; fold one first, or run: nova-tokens check --out " + *out + " --allow-empty"
		fmt.Fprintf(s.err(), "CHECK FAILED %s\n", oneline.Escape(why))
		s.o.Why = append(s.o.Why, why)
		s.o.Remedy = "nova-tokens check --out " + *out + " --allow-empty"
	}
	bad := files.Total() + rowsList.Total()
	counts := []any{"files", res.Files, "rows", res.Rows, "first", first, "last", last}
	if bad > 0 || len(res.Missing) > 0 || len(res.Strays) > 0 || res.Stale || empty {
		counts = append(counts, "bad", bad, "missing", len(res.Missing), "stray", len(res.Strays), "gap", len(res.Gaps), "notes", len(res.Notes))
		fmt.Fprintf(s.err(), "CHECK FAILED%s\n", s.factFields(counts...))
		return s.done(1, *max)
	}
	// gap= and notes= are on the OK line too, and that is the whole point: what the gate
	// stopped naming it still counts, so nothing was hidden to make the line green.
	counts = append([]any{"at", stamp(now), "build", buildVersion()}, append(counts, "missing", 0, "stray", 0, "gap", len(res.Gaps), "notes", len(res.Notes))...)
	fmt.Fprintf(s.out(), "CHECK OK%s\n", s.factFields(counts...))
	return s.done(0, *max)
}
