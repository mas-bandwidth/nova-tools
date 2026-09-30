package main

// One card's usage.tsv, the file a native run writes beside its RESULT.md (internal/swarm
// CardUsageColumns): `profiles` walks them under a swarm root.

import (
	"os"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// parseCardCount reads a numeric cell and whether the cell held one. A dash, an empty cell,
// or a cell that is not a number is an absence, and an absence is unknown: never the zero
// that would make a route that did not report a count look free.
func parseCardCount(v string) (int64, bool) {
	v = strings.TrimSpace(v)
	if v == "" || v == tokens.Dash {
		return 0, false
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		return n, true
	}
	return 0, false
}

// parseCardUsd reads the dollar cell and whether the cell held it. An absence here is
// unknown too, never a free-route zero.
func parseCardUsd(v string) (float64, bool) {
	v = strings.TrimSpace(v)
	if v == "" || v == tokens.Dash {
		return 0, false
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f, true
	}
	return 0, false
}

// readCardFile reads one card's usage.tsv, mapping its columns by the header so the reader
// never depends on a fixed index. It returns the started stamp, the model, the repo the
// receipt names (unattributed when it names none), the tool, the rc, and the three numbers
// the ledger keeps, and reports whether the file held a row at all.
func readCardFile(path string) (started, model, repo, tool, rc string, in, out int64, usd float64, inKnown, outKnown, usdKnown, ok bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", "", "", 0, 0, 0, false, false, false, false
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) < 2 {
		return "", "", "", "", "", 0, 0, 0, false, false, false, false
	}
	head := strings.Split(lines[0], "\t")
	idx := map[string]int{}
	for i, name := range head {
		if _, seen := idx[name]; !seen {
			idx[name] = i
		}
	}
	get := func(row []string, name string) string {
		if i, has := idx[name]; has && i < len(row) {
			return row[i]
		}
		return ""
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		row := strings.Split(line, "\t")
		s := get(row, "started")
		m := get(row, "model")
		if s == "" || m == "" {
			continue
		}
		in, inKnown = parseCardCount(get(row, "tokens_in"))
		out, outKnown = parseCardCount(get(row, "tokens_out"))
		usd, usdKnown = parseCardUsd(get(row, "usd"))
		repo = strings.TrimSpace(get(row, "repo"))
		if repo == "" || repo == tokens.Dash {
			repo = tokens.Unattributed
		}
		return s, m, repo, get(row, "tool"), get(row, "rc"), in, out, usd, inKnown, outKnown, usdKnown, true
	}
	return "", "", "", "", "", 0, 0, 0, false, false, false, false
}
