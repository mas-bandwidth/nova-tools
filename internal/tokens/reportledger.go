package tokens

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// LedgerRow is one row of the fold-pool ledger.
type LedgerRow struct {
	Day      string
	Provider string
	Model    string
	Repo     string
	Tasks    int
	In       int64
	Out      int64
	Cr       int64
	UsdMicro int64
	UsdKnown bool
}

// ReadPoolLedger reads the fold-pool ledger at path, checking its header.
func ReadPoolLedger(path string) ([]LedgerRow, error) {
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
			Cr:       ledgerNum(f[8]),
			UsdMicro: ledgerUsdMicro(f[10]),
			UsdKnown: ledgerUsdKnown(f[10]),
		})
	}
	return out, nil
}

func ledgerNum(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == Dash {
		return 0
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func ledgerUsdMicro(s string) int64 {
	v, ok := ParseMicro(s)
	if !ok {
		return 0
	}
	return v
}

func ledgerUsdKnown(s string) bool {
	_, ok := ParseMicro(s)
	return ok
}

// LedgerGroup is one --by group's sums.
type LedgerGroup struct {
	Name       string
	Tasks      int
	In         int64
	Out        int64
	Cr         int64
	UsdMicro   int64
	UsdUnknown bool
}

// GroupLedger sums the rows of month by group (model, repo or day),
// sorted by cache_read descending, then name ascending.
func GroupLedger(rows []LedgerRow, month, by string) (groups []*LedgerGroup, monthRows int) {
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
		g.Cr += r.Cr
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

// GroupUsd renders a group's usd cell: - when any row's usd was unknown.
func GroupUsd(micro int64, unknown bool) string {
	if unknown {
		return Dash
	}
	return Usd(micro)
}
