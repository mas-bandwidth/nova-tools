package pulse

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// KindCost is tokens per green card of one kind, folded from usage.tsv by
// header name. Green is rc=0. Kind comes from the sibling PROMPT.md KIND:
// line, else from RESULT.md / PROMPT.md line 1. Cache-read / input is the
// context x turns bill (#855: 1,434.6M cache-read against 62.1M input).
type KindCost struct {
	Kind       string
	Cards      int
	Input      int64
	Output     int64
	CacheWrite int64
	CacheRead  int64
	Reasoning  int64
}

// MeanInput is tokens_in per green card of this kind, 0 when there are none.
func (k KindCost) MeanInput() int64 {
	if k.Cards == 0 {
		return 0
	}
	return k.Input / int64(k.Cards)
}

// CachePerInput is cache_read / tokens_in, the implied harness-turn
// multiplier. 0 when input is absent.
func (k KindCost) CachePerInput() float64 {
	if k.Input == 0 {
		return 0
	}
	return float64(k.CacheRead) / float64(k.Input)
}

// ReasoningPerOutput is reasoning / tokens_out. 0 when output is absent.
func (k KindCost) ReasoningPerOutput() float64 {
	if k.Output == 0 {
		return 0
	}
	return float64(k.Reasoning) / float64(k.Output)
}

// GreenCardCostByKind walks every usage.tsv under root, keeps rc=0 rows, and
// folds tokens by card kind. Columns are read by header name. A kind with no
// green card is omitted. Results are sorted cheapest-first by mean input, so
// the two cheapest cuts are costs[0] and costs[1] when two kinds landed.
func GreenCardCostByKind(root string) []KindCost {
	by := map[string]*KindCost{}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "usage.tsv" {
			return nil
		}
		for _, row := range parseUsageTokenRows(path) {
			if row.rc != "0" {
				continue
			}
			kind := kindOfCard(cardDir(path))
			k := by[kind]
			if k == nil {
				k = &KindCost{Kind: kind}
				by[kind] = k
			}
			k.Cards++
			k.Input += row.in
			k.Output += row.out
			k.CacheWrite += row.cw
			k.CacheRead += row.cr
			k.Reasoning += row.rs
		}
		return nil
	})
	out := make([]KindCost, 0, len(by))
	for _, k := range by {
		out = append(out, *k)
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := out[i].MeanInput(), out[j].MeanInput(); a != b {
			return a < b
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

type usageTokenRow struct {
	rc                  string
	in, out, cw, cr, rs int64
}

func parseUsageTokenRows(path string) []usageTokenRow {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	if len(lines) < 2 {
		return nil
	}
	head := strings.Split(lines[0], "\t")
	var out []usageTokenRow
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		values := strings.Split(line, "\t")
		cols := map[string]string{}
		for i, name := range head {
			if i < len(values) {
				cols[strings.TrimSpace(name)] = values[i]
			}
		}
		out = append(out, usageTokenRow{
			rc:  strings.TrimSpace(cols["rc"]),
			in:  parseTokenCell(cols["tokens_in"]),
			out: parseTokenCell(cols["tokens_out"]),
			cw:  parseTokenCell(cols["cache_write"]),
			cr:  parseTokenCell(cols["cache_read"]),
			rs:  parseTokenCell(cols["reasoning"]),
		})
	}
	return out
}

func parseTokenCell(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func cardDir(usagePath string) string {
	d := filepath.Dir(usagePath)
	if filepath.Base(d) == "export" {
		return filepath.Dir(d)
	}
	return d
}

func kindOfCard(dir string) string {
	for _, name := range []string{"PROMPT.md", "RESULT.md"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if k := kindFromText(string(raw)); k != "unknown" {
			return k
		}
	}
	return "unknown"
}

func kindFromText(raw string) string {
	for _, ln := range strings.Split(raw, "\n") {
		line := strings.TrimSpace(ln)
		if len(line) < 6 || !strings.EqualFold(line[:5], "KIND:") {
			continue
		}
		rest := strings.ToLower(strings.TrimSpace(line[5:]))
		if rest == "" {
			break
		}
		return strings.Fields(rest)[0]
	}
	line1 := ""
	if i := strings.IndexByte(raw, '\n'); i >= 0 {
		line1 = raw[:i]
	} else {
		line1 = raw
	}
	lower := strings.ToLower(line1)
	switch {
	case strings.Contains(lower, "replay"):
		return "replay"
	case strings.Contains(lower, "tone"):
		return "tone"
	case strings.Contains(lower, "drift"):
		return "drift"
	case strings.Contains(lower, "rebase"):
		return "rebase"
	case strings.Contains(lower, "read"):
		return "read"
	case strings.Contains(lower, "fix"):
		return "fix"
	case strings.Contains(lower, "spec:"):
		return "spec"
	}
	return "unknown"
}
