package tokens

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// The rates table: a tab-separated price list that turns a model's input and output
// token counts into a dollar cost, and the price's own revision date. It is the dollar
// side of the accounting layer: the fold counts tokens, and this table prices them, so a
// run's spend can be a number rather than a story about a bill that has not come yet.

// ModelRate is one model's price: dollars per input token and per output token, and the
// date the price was last verified against the provider's published list.
type ModelRate struct {
	Model        string
	InputRate    float64
	OutputRate   float64
	VerifiedDate string
}

// RatesTable is the whole price list, keyed by model name and kept in file order.
type RatesTable struct {
	byModel map[string]ModelRate
	order   []string
}

// ratesHeader is the table's first line, the four column names.
var ratesHeader = []string{"model", "input_rate", "output_rate", "verified_date"}

// ParseRatesTable reads a tab-separated rates table whose first line is the header
// `model	input_rate	output_rate	verified_date` and whose remaining lines are one
// model each. A malformed line names itself rather than being skipped, because a price
// list a run half-reads is a cost that lies.
func ParseRatesTable(r io.Reader) (*RatesTable, error) {
	t := &RatesTable{byModel: map[string]ModelRate{}}
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	text := strings.TrimSuffix(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	lines := strings.Split(text, "\n")
	wantHead := strings.Join(ratesHeader, "\t")
	if len(lines) == 0 || lines[0] != wantHead {
		return nil, fmt.Errorf("rates table: the first line is not the four column names %q", wantHead)
	}
	for i := 1; i < len(lines); i++ {
		line := lines[i]
		if line == "" {
			continue
		}
		n := i + 1
		cells := strings.Split(line, "\t")
		if len(cells) != len(ratesHeader) {
			return nil, fmt.Errorf("rates table: line %d has %d columns, want %d (%s)", n, len(cells), len(ratesHeader), wantHead)
		}
		m := ModelRate{Model: cells[0], VerifiedDate: cells[3]}
		if m.InputRate, err = strconv.ParseFloat(cells[1], 64); err != nil {
			return nil, fmt.Errorf("rates table: line %d input_rate %q is not a number", n, cells[1])
		}
		if m.OutputRate, err = strconv.ParseFloat(cells[2], 64); err != nil {
			return nil, fmt.Errorf("rates table: line %d output_rate %q is not a number", n, cells[2])
		}
		if _, dup := t.byModel[m.Model]; dup {
			return nil, fmt.Errorf("rates table: line %d repeats model %q", n, m.Model)
		}
		t.byModel[m.Model] = m
		t.order = append(t.order, m.Model)
	}
	return t, nil
}

// CalculateCost is a model's cost in dollars for the given input and output token counts,
// and whether the model is priced. An unknown model is unpriced: the cost is zero and the
// second return is false, so "not priced" is told apart from "free".
func (t *RatesTable) CalculateCost(model string, in, out int64) (float64, bool) {
	m, ok := t.byModel[model]
	if !ok {
		return 0.0, false
	}
	return float64(in)*m.InputRate + float64(out)*m.OutputRate, true
}

// OldestVerifiedDate is the earliest verified_date among the table's entries, so a run of
// prices can say how far back they are pinned. An empty table has no date.
func (t *RatesTable) OldestVerifiedDate() string {
	dates := make([]string, 0, len(t.byModel))
	for _, m := range t.byModel {
		dates = append(dates, m.VerifiedDate)
	}
	sort.Strings(dates)
	if len(dates) == 0 {
		return ""
	}
	return dates[0]
}
