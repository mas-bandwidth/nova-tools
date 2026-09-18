package tokens

import (
	"fmt"
	"sort"
	"strings"
)

// DiffRow is one token entry that changed between two sets: the old tokens, the new
// tokens, and the signed delta (new minus old). A side that has no entry for the key is a
// dash, an absence rather than a zero, exactly as it is everywhere else in this package.
type DiffRow struct {
	Key   string
	Old   string // the tokens on the old side, or a dash where the key was absent
	New   string // the tokens on the new side, or a dash where the key is now absent
	Delta int64  // the signed delta, new minus old
}

// ComputeTokensDiff compares old and new token entries by key and returns one row per
// key that differs, sorted by absolute token movement descending so the largest movement
// is first. Keys held at zero on one side and simply not present on the other are
// treated the same way a day file treats them: an absent entry is a dash, never a zero.
func ComputeTokensDiff(oldEntries, newEntries map[string]int64) []DiffRow {
	keys := make(map[string]bool, len(oldEntries)+len(newEntries))
	for k := range oldEntries {
		keys[k] = true
	}
	for k := range newEntries {
		keys[k] = true
	}

	var rows []DiffRow
	for k := range keys {
		oldV, oldOK := oldEntries[k]
		newV, newOK := newEntries[k]

		row := DiffRow{
			Key:   k,
			Old:   Dash,
			New:   Dash,
			Delta: newV - oldV,
		}
		// A side that holds the key with the value zero is still a measurement of zero,
		// not an absence, so it renders as its number rather than a dash.
		if oldOK {
			row.Old = itoa64(oldV)
		}
		if newOK {
			row.New = itoa64(newV)
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		ai, aj := abs64(rows[i].Delta), abs64(rows[j].Delta)
		if ai != aj {
			return ai > aj
		}
		return rows[i].Key < rows[j].Key
	})
	return rows
}

// FormatDiff renders the rows one per line, key, old, new, then the signed delta, in the
// order ComputeTokensDiff produced them. The largest movement is the first line.
func FormatDiff(rows []DiffRow) string {
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s\t%+d", r.Key, r.Old, r.New, r.Delta))
	}
	return strings.Join(lines, "\n")
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
