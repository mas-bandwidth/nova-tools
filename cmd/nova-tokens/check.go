// check.go holds the check verb: its flags, its run and the helpers only it uses.

package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
)

// checkVerb declares the check verb.
func checkVerb(now time.Time) tool.Verb {
	return tool.Verb{
		Name:    "check",
		Usage:   "check --out <dir> [--strict | --no-spend <file>] [--through <YYYY-MM-DD>] [--max <n>]",
		Example: "check --out ./out",
		Effect:  tool.Inspection,
		Flags: func(f *tool.Flags) {
			f.String("out", "", "directory containing daily token files")
			f.Bool("strict", false, "treat every gap and note as a finding")
			f.String("no-spend", "", "file listing UTC dates with no spend, one per line")
			f.String("through", "", "require coverage through this UTC day, YYYY-MM-DD")
			f.Max()
			f.Check(func(c *tool.Call) {
				c.Want("out", wantsOut)
				if c.Bool("strict") && strings.TrimSpace(c.Str("no-spend")) != "" {
					c.Problem("--strict and --no-spend are two answers to one question: --strict names every calendar gap, --no-spend names the gaps your list does not account for; give one")
				}
				through := strings.TrimSpace(c.Str("through"))
				if through != "" && !tokens.ValidDay(through) {
					c.Problem("--through is not a day: " + through + "; it wants YYYY-MM-DD (e.g. 2026-09-18)")
				}
			})
		},
		Run: func(c *tool.Call) *tool.Out {
			return runCheck(c, now)
		},
	}
}

// runCheck is the GATE. It says FAILED on any malformed file, any malformed row, any missing
// day and any stray, and it prints the count line either way. A missing day is NAMED and
// never filled: nobody folded it, and this tool does not invent what nobody measured.
func runCheck(c *tool.Call, now time.Time) *tool.Out {
	outDir := c.Str("out")
	strict := c.Bool("strict")
	noSpend := c.Str("no-spend")
	through := strings.TrimSpace(c.Str("through"))

	opt := tokens.CheckOptions{Strict: strict, Through: through}
	if strings.TrimSpace(noSpend) != "" {
		days, err := tokens.ReadNoSpendFile(noSpend)
		if err != nil {
			return tool.Refuse("--no-spend " + noSpend + ": " + err.Error())
		}
		opt.NoSpend = days
	}
	res, err := tokens.Check(outDir, opt)
	if err != nil {
		return tool.Refuse(err.Error())
	}

	asJSON := c.Bool("json")
	max := c.Int("max")

	if !asJSON {
		remedyLine := "nova-tokens check --out " + outDir + " --max 0"
		files := bounded.Capped(c.Stderr, max, "CHECK", "file", remedyLine)
		rowsList := bounded.Capped(c.Stderr, max, "CHECK", "row", remedyLine)
		missing := bounded.Capped(c.Stderr, max, "CHECK", "missing", remedyLine)
		strays := bounded.Capped(c.Stderr, max, "CHECK", "stray", remedyLine)
		for _, f := range res.Findings {
			reason := oneline.Cap(f.Reason, oneline.TailBytes)
			line := fmt.Sprintf("CHECK FAILED %s: %s", oneline.Escape(f.Path), oneline.Escape(reason))
			if f.Line > 0 {
				line = fmt.Sprintf("CHECK FAILED %s:%d: %s", oneline.Escape(f.Path), f.Line, oneline.Escape(reason))
			}
			if f.Line > 2 {
				rowsList.Line(line)
			} else {
				files.Line(line)
			}
		}
		files.More()
		rowsList.More()
		for _, d := range res.Missing {
			missing.Line("CHECK MISSING date=" + oneline.Field(d))
		}
		missing.More()
		for _, p := range res.Strays {
			strays.Line("CHECK STRAY " + oneline.Escape(p))
		}
		strays.More()

		first, last := tokens.Dash, tokens.Dash
		if res.First != "" {
			first, last = res.First, res.Last
		}
		if res.Stale {
			fmt.Fprintf(c.Stderr, "CHECK FAILED stale last=%s through=%s\n", oneline.Field(last), oneline.Field(through))
		}
		empty := res.Files == 0
		if empty {
			why := "--out " + outDir + " holds no day file, so there is nothing to check; fold one first: nova-tokens fold --out " + outDir + " --day <YYYY-MM-DD> --repos <file> <source flags>"
			fmt.Fprintf(c.Stderr, "CHECK FAILED %s\n", oneline.Escape(why))
		}
		bad := files.Total() + rowsList.Total()
		if bad > 0 || len(res.Missing) > 0 || len(res.Strays) > 0 || res.Stale || empty {
			fmt.Fprintf(c.Stderr, "CHECK FAILED files=%d rows=%d first=%s last=%s bad=%d missing=%d stray=%d gap=%d notes=%d\n",
				res.Files, res.Rows, first, last, bad, len(res.Missing), len(res.Strays), len(res.Gaps), len(res.Notes))
			return tool.Exit(1)
		}
		fmt.Fprintf(c.Stdout, "CHECK OK at=%s build=%s files=%d rows=%d first=%s last=%s missing=0 stray=0 gap=%d notes=%d\n",
			stamp(now), buildVersion(), res.Files, res.Rows, first, last, len(res.Gaps), len(res.Notes))
		return tool.Exit(0)
	}

	o := tool.Done()

	filesBad, rowsBad := 0, 0
	for _, f := range res.Findings {
		reason := oneline.Cap(f.Reason, oneline.TailBytes)
		kind := "file"
		if f.Line > 2 {
			kind = "row"
			rowsBad++
		} else {
			filesBad++
		}
		o.ItemText(kind, reason, "path", f.Path, "line", f.Line)
	}
	for _, d := range res.Missing {
		o.Item("missing", "date", d)
	}
	for _, p := range res.Strays {
		o.Item("stray", "path", p)
	}

	first, last := tokens.Dash, tokens.Dash
	if res.First != "" {
		first, last = res.First, res.Last
	}
	if res.Stale {
		o.Item("stale", "last", last, "through", through)
	}

	empty := res.Files == 0
	if empty {
		why := "--out " + outDir + " holds no day file, so there is nothing to check; fold one first: nova-tokens fold --out " + outDir + " --day <YYYY-MM-DD> --repos <file> <source flags>"
		o.Why = append(o.Why, why)
	}
	bad := filesBad + rowsBad
	if bad > 0 || len(res.Missing) > 0 || len(res.Strays) > 0 || res.Stale || empty {
		o.Status = tool.Failed
		o.Exit = 1
		o.Fact("files", res.Files).
			Fact("rows", res.Rows).
			Fact("first", first).
			Fact("last", last).
			Fact("bad", bad).
			Fact("missing", len(res.Missing)).
			Fact("stray", len(res.Strays)).
			Fact("gap", len(res.Gaps)).
			Fact("notes", len(res.Notes))
		return o
	}

	o.Fact("at", stamp(now)).
		Fact("build", buildVersion()).
		Fact("files", res.Files).
		Fact("rows", res.Rows).
		Fact("first", first).
		Fact("last", last).
		Fact("missing", 0).
		Fact("stray", 0).
		Fact("gap", len(res.Gaps)).
		Fact("notes", len(res.Notes))

	return o
}
