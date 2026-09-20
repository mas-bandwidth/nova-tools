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
	Kind            string
	Cards           int
	Input           int64
	InputKnown      int
	Output          int64
	OutputKnown     int
	CacheWrite      int64
	CacheWriteKnown int
	CacheRead       int64
	CacheReadKnown  int
	Reasoning       int64
	ReasoningKnown  int
}

// MeanInput is tokens_in per green card that reported a number. ok is false
// when every card of this kind left input unknown (dash, absent, malformed):
// unknown is not a measured zero and is not cheapest.
func (k KindCost) MeanInput() (int64, bool) {
	if k.InputKnown == 0 {
		return 0, false
	}
	return k.Input / int64(k.InputKnown), true
}

// CachePerInput is cache_read / tokens_in over the known cells of each
// counter. ok is false when either counter is fully unknown.
func (k KindCost) CachePerInput() (float64, bool) {
	if k.InputKnown == 0 || k.CacheReadKnown == 0 || k.Input == 0 {
		return 0, false
	}
	return float64(k.CacheRead) / float64(k.Input), true
}

// ReasoningPerOutput is reasoning / tokens_out over the known cells. ok is
// false when either counter is fully unknown.
func (k KindCost) ReasoningPerOutput() (float64, bool) {
	if k.OutputKnown == 0 || k.ReasoningKnown == 0 || k.Output == 0 {
		return 0, false
	}
	return float64(k.Reasoning) / float64(k.Output), true
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
			addKnown(&k.Input, &k.InputKnown, row.in, row.inOK)
			addKnown(&k.Output, &k.OutputKnown, row.out, row.outOK)
			addKnown(&k.CacheWrite, &k.CacheWriteKnown, row.cw, row.cwOK)
			addKnown(&k.CacheRead, &k.CacheReadKnown, row.cr, row.crOK)
			addKnown(&k.Reasoning, &k.ReasoningKnown, row.rs, row.rsOK)
		}
		return nil
	})
	out := make([]KindCost, 0, len(by))
	for _, k := range by {
		out = append(out, *k)
	}
	sort.Slice(out, func(i, j int) bool {
		a, aOK := out[i].MeanInput()
		b, bOK := out[j].MeanInput()
		if aOK != bOK {
			return aOK
		}
		if !aOK {
			return out[i].Kind < out[j].Kind
		}
		if a != b {
			return a < b
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

func addKnown(sum *int64, n *int, v int64, ok bool) {
	if !ok {
		return
	}
	*sum += v
	*n++
}

type usageTokenRow struct {
	rc                            string
	in, out, cw, cr, rs           int64
	inOK, outOK, cwOK, crOK, rsOK bool
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
		in, inOK := parseTokenCell(cols["tokens_in"])
		outv, outOK := parseTokenCell(cols["tokens_out"])
		cw, cwOK := parseTokenCell(cols["cache_write"])
		cr, crOK := parseTokenCell(cols["cache_read"])
		rs, rsOK := parseTokenCell(cols["reasoning"])
		out = append(out, usageTokenRow{
			rc: strings.TrimSpace(cols["rc"]),
			in: in, inOK: inOK,
			out: outv, outOK: outOK,
			cw: cw, cwOK: cwOK,
			cr: cr, crOK: crOK,
			rs: rs, rsOK: rsOK,
		})
	}
	return out
}

func parseTokenCell(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
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
