package main

// One card's usage.tsv, the file a native run writes beside its RESULT.md (pkg/swarm
// CardUsageColumns): `profiles` walks them under a swarm root.

import (
	"io"
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

// cardUsage is what this tool reads from one card's usage.tsv: the model the card ran and
// the output tokens it reported, when it reported a number.
type cardUsage struct {
	model    string
	out      int64
	outKnown bool
	ok       bool
}

// readCardFile reads one card's usage.tsv, mapping its columns by the header so the reader
// never depends on a fixed index. It returns the card's model, the output tokens the card
// reported and whether it reported any, and whether the file held a row at all.
func readCardFile(path string) (u cardUsage) {
	raw, err := boundedReadFile(path, 1<<20) // 1 MiB cap for usage.tsv
	if err != nil {
		return cardUsage{}
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) < 2 {
		return cardUsage{}
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
		u.model = m
		u.out, u.outKnown = parseCardCount(get(row, "tokens_out"))
		u.ok = true
		return u
	}
	return cardUsage{}
}

// boundedReadFile reads a file with a size cap, returning an error if the file exceeds
// the cap or is not a regular file. This prevents reading arbitrarily large files
// that could exhaust memory.
func boundedReadFile(path string, cap int64) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, os.ErrNotExist
	}
	if fi.Size() > cap {
		return nil, os.ErrNotExist
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() // ignored: a read-only file
	return io.ReadAll(io.LimitReader(f, cap+1))
}
