package main

import (
	"fmt"
	"io"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// cmdReportLedger sums the fold-pool ledger's rows of one month by group.
func cmdReportLedger(ledger, month, by string, max int, stdout, stderr io.Writer) int {
	r := &refusals{token: "REPORT"}
	r.required("ledger", ledger, "the ledger file, <ledger>.tsv, whose header is "+wantsPoolLedger)
	switch {
	case month == "":
		r.add("--month is required; it wants " + wantsMonth + "; refusing to guess")
	case !validMonth(month):
		r.add("--month is not a month: " + month + "; it wants " + wantsMonth)
	}
	switch by {
	case "model", "repo", "day":
	default:
		r.add("--by is model, repo or day, got " + by)
	}
	checkMax(r, max)
	if len(r.list) > 0 {
		return r.print(stderr)
	}
	if fi, err := os.Stat(ledger); err != nil || !fi.IsDir() {
		if err != nil && !os.IsNotExist(err) && fi != nil && fi.IsDir() {
			r.add("--ledger is a directory: " + ledger + "; it wants the ledger file")
			return r.print(stderr)
		}
		if fi != nil && fi.IsDir() {
			r.add("--ledger is a directory: " + ledger + "; it wants the ledger file")
			return r.print(stderr)
		}
	} else {
		r.add("--ledger is a directory: " + ledger + "; it wants the ledger file")
		return r.print(stderr)
	}
	rows, err := tokens.ReadPoolLedger(ledger)
	if err != nil {
		if os.IsNotExist(err) {
			r.add("--ledger " + ledger + ": no such file")
		} else {
			r.add("--ledger " + ledger + ": " + err.Error())
		}
		return r.print(stderr)
	}
	groups, monthRows := tokens.GroupLedger(rows, month, by)
	capped := groups
	if max != 0 && len(groups) > max {
		capped = groups[:max]
	}
	var totalUsd int64
	totalUnknown := false
	for _, g := range groups {
		if g.UsdUnknown {
			totalUnknown = true
		} else {
			totalUsd += g.UsdMicro
		}
	}
	for _, g := range capped {
		switch by {
		case "repo":
			fmt.Fprintf(stdout, "REPORT repo=%s tasks=%d in=%d out=%d cache_read=%d usd=%s\n",
				oneline.Field(g.Name), g.Tasks, g.In, g.Out, g.Cr, oneline.Field(tokens.GroupUsd(g.UsdMicro, g.UsdUnknown)))
		case "day":
			fmt.Fprintf(stdout, "REPORT day=%s tasks=%d in=%d out=%d cache_read=%d usd=%s\n",
				oneline.Field(g.Name), g.Tasks, g.In, g.Out, g.Cr, oneline.Field(tokens.GroupUsd(g.UsdMicro, g.UsdUnknown)))
		default:
			fmt.Fprintf(stdout, "REPORT model=%s tasks=%d in=%d out=%d cache_read=%d usd=%s\n",
				oneline.Field(g.Name), g.Tasks, g.In, g.Out, g.Cr, oneline.Field(tokens.GroupUsd(g.UsdMicro, g.UsdUnknown)))
		}
	}
	total := tokens.GroupUsd(totalUsd, totalUnknown)
	fmt.Fprintf(stdout, "REPORT OK month=%s groups=%d rows=%d usd=%s\n",
		oneline.Field(month), len(groups), monthRows, oneline.Field(total))
	return 0
}

const wantsPoolLedger = "day, provider, model, repo, tasks, tokens_in, tokens_out, cache_write, cache_read, reasoning, usd, source"
