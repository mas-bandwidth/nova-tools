package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

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

	// Read and parse the ledger file directly
	rows, err := readLedgerFile(ledger, month)
	if err != nil {
		if os.IsNotExist(err) {
			r.add("--ledger " + ledger + ": no such file")
		} else {
			r.add("--ledger " + ledger + ": " + err.Error())
		}
		return r.print(stderr)
	}

	// Group the rows
	groups, monthRows := groupLedgerRows(rows, month, by)

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
			fmt.Fprintf(stdout, "REPORT repo=%s tasks=%d in=%d out=%d cache_write=%d cache_read=%d reasoning=%d usd=%s\n",
				oneline.Field(g.Name), g.Tasks, g.In, g.Out, g.Cw, g.Cr, g.Rsn, oneline.Field(tokens.GroupUsd(g.UsdMicro, g.UsdUnknown)))
		case "day":
			fmt.Fprintf(stdout, "REPORT day=%s tasks=%d in=%d out=%d cache_write=%d cache_read=%d reasoning=%d usd=%s\n",
				oneline.Field(g.Name), g.Tasks, g.In, g.Out, g.Cw, g.Cr, g.Rsn, oneline.Field(tokens.GroupUsd(g.UsdMicro, g.UsdUnknown)))
		default:
			fmt.Fprintf(stdout, "REPORT model=%s tasks=%d in=%d out=%d cache_write=%d cache_read=%d reasoning=%d usd=%s\n",
				oneline.Field(g.Name), g.Tasks, g.In, g.Out, g.Cw, g.Cr, g.Rsn, oneline.Field(tokens.GroupUsd(g.UsdMicro, g.UsdUnknown)))
		}
	}
	total := tokens.GroupUsd(totalUsd, totalUnknown)
	fmt.Fprintf(stdout, "REPORT OK month=%s groups=%d rows=%d usd=%s\n",
		oneline.Field(month), len(groups), monthRows, oneline.Field(total))
	return 0
}

// LedgerRow is one row of the fold-pool ledger with all five token types.
type LedgerRow struct {
	Day      string
	Provider string
	Model    string
	Repo     string
	Tasks    int
	In       int64
	Out      int64
	Cw       int64
	Cr       int64
	Rsn      int64
	UsdMicro int64
	UsdKnown bool
}

// LedgerGroup is one --by group's sums with all five token types.
type LedgerGroup struct {
	Name       string
	Tasks      int
	In         int64
	Out        int64
	Cw         int64
	Cr         int64
	Rsn        int64
	UsdMicro   int64
	UsdUnknown bool
}

// readLedgerFile reads the fold-pool ledger at path, checking its header and filtering by month.
func readLedgerFile(path, month string) ([]LedgerRow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return nil, fmt.Errorf("the ledger's header is %q; it wants %q", "", strings.Join(FoldPoolColumns, "\t"))
	}
	if lines[0] != strings.Join(FoldPoolColumns, "\t") {
		return nil, fmt.Errorf("the ledger's header is %q; it wants %q", lines[0], strings.Join(FoldPoolColumns, "\t"))
	}
	var out []LedgerRow
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		f := strings.Split(l, "\t")
		if len(f) != len(FoldPoolColumns) {
			return nil, fmt.Errorf("the ledger holds a row with %d columns, want %d: %q", len(f), len(FoldPoolColumns), l)
		}
		tasks, err := strconv.Atoi(strings.TrimSpace(f[4]))
		if err != nil {
			return nil, fmt.Errorf("the ledger holds a bad tasks cell %q", f[4])
		}
		out = append(out, LedgerRow{
			Day:      strings.TrimSpace(f[0]),
			Provider: strings.TrimSpace(f[1]),
			Model:    strings.TrimSpace(f[2]),
			Repo:     strings.TrimSpace(f[3]),
			Tasks:    tasks,
			In:       ledgerNum(f[5]),
			Out:      ledgerNum(f[6]),
			Cw:       ledgerNum(f[7]),
			Cr:       ledgerNum(f[8]),
			Rsn:      ledgerNum(f[9]),
			UsdMicro: ledgerUsdMicro(f[10]),
			UsdKnown: ledgerUsdKnown(f[10]),
		})
	}
	return out, nil
}

func ledgerNum(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == tokens.Dash {
		return 0
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func ledgerUsdMicro(s string) int64 {
	v, ok := tokens.ParseMicro(s)
	if !ok {
		return 0
	}
	return v
}

func ledgerUsdKnown(s string) bool {
	_, ok := tokens.ParseMicro(s)
	return ok
}

// groupLedgerRows sums the rows of month by group (model, repo or day),
// sorted by cache_read descending, then name ascending.
func groupLedgerRows(rows []LedgerRow, month, by string) (groups []*LedgerGroup, monthRows int) {
	by = strings.TrimSpace(by)
	if by == "" {
		by = "model"
	}
	m := map[string]*LedgerGroup{}
	for _, r := range rows {
		if len(r.Day) < 7 || r.Day[:7] != month {
			continue
		}
		var key string
		switch by {
		case "repo":
			key = r.Repo
		case "day":
			key = r.Day
		default:
			key = r.Model
		}
		g, ok := m[key]
		if !ok {
			g = &LedgerGroup{Name: key}
			m[key] = g
		}
		g.Tasks += r.Tasks
		g.In += r.In
		g.Out += r.Out
		g.Cw += r.Cw
		g.Cr += r.Cr
		g.Rsn += r.Rsn
		if r.UsdKnown {
			g.UsdMicro += r.UsdMicro
		} else {
			g.UsdUnknown = true
		}
		monthRows++
	}
	for _, g := range m {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Cr != groups[j].Cr {
			return groups[i].Cr > groups[j].Cr
		}
		return groups[i].Name < groups[j].Name
	})
	return groups, monthRows
}

// FoldPoolColumns are the monthly ledger columns fold-pool writes, in order.
var FoldPoolColumns = []string{
	"day", "provider", "model", "repo", "tasks",
	"tokens_in", "tokens_out", "cache_write", "cache_read", "reasoning",
	"usd", "source",
}

const wantsPoolLedger = "day, provider, model, repo, tasks, tokens_in, tokens_out, cache_write, cache_read, reasoning, usd, source"
