package tokens

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The usage receipt: one row per turn a worker ran, seventeen columns. It is the
// per-turn record the day file aggregates away -- a day file sums every turn into
// one (model, repo) row, and a receipt keeps the turns apart so a row can be traced
// back to the node, stage, session and source that produced it.

// Stage is one turn's place in the worker loop. A stage is a word, never a number, so
// a row can name where a turn happened without a by-position table a reader has to
// know.
type Stage string

// The six stages, in the order they appear in the loop.
const (
	StagePreparation    Stage = "preparation"
	StageImplementation Stage = "implementation"
	StageReview         Stage = "review"
	StageCorrection     Stage = "correction"
	StageCoordination   Stage = "coordination"
	StageValidation     Stage = "validation"
)

// StageNames are the six stage names, in order.
var StageNames = [6]Stage{
	StagePreparation, StageImplementation, StageReview,
	StageCorrection, StageCoordination, StageValidation,
}

// ValidStage reports whether s is one of the six stages.
func ValidStage(s string) bool {
	for _, n := range StageNames {
		if s == string(n) {
			return true
		}
	}
	return false
}

// ReceiptColumns are the seventeen columns, in order, and the header line of a receipt
// file is exactly these.
var ReceiptColumns = []string{
	"day", "model", "repo", "receipt", "node", "stage", "session", "turns",
	"input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens",
	"reasoning_tokens", "total_tokens", "cost_usd", "created_at", "source",
}

// ReceiptHeaderLine is the header line of a receipt file.
var ReceiptHeaderLine = strings.Join(ReceiptColumns, "\t")

// ReceiptRow is one written receipt row. The receipt id is the row's own 32-hex
// identifier; day, model, repo and source are where the turn happened and who counted it.
type ReceiptRow struct {
	Day              string
	Model            string
	Repo             string
	Receipt          string // 32 lowercase hex characters
	Node             string
	Stage            Stage
	Session          string
	Turns            int64
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	ReasoningTokens  int64
	TotalTokens      int64
	CostUsd          int64 // micro-dollars
	CreatedAt        string
	Source           string
}

// ParseReceiptRow reads one receipt line into a ReceiptRow, returning everything wrong
// with it as one error. A line is seventeen tab-separated columns, the receipt id is 32
// hexadecimal characters, and the stage is one of the six declared stages.
func ParseReceiptRow(line string) (ReceiptRow, error) {
	var r ReceiptRow
	if line == "" {
		return r, fmt.Errorf("a receipt row has %d columns; an empty line has none", len(ReceiptColumns))
	}
	cells := strings.Split(line, "\t")
	if len(cells) != len(ReceiptColumns) {
		return r, fmt.Errorf("%d columns, want %d (%s)", len(cells), len(ReceiptColumns), ReceiptHeaderLine)
	}
	r.Day = cells[0]
	r.Model = cells[1]
	r.Repo = cells[2]
	r.Receipt = cells[3]
	r.Node = cells[4]
	r.Stage = Stage(cells[5])
	r.Session = cells[6]
	var err error
	if r.Turns, err = parseCount64(cells[7]); err != nil {
		return r, fmt.Errorf("turns cell: %v", err)
	}
	cols := []struct {
		name string
		dst  *int64
	}{
		{"input_tokens", &r.InputTokens},
		{"output_tokens", &r.OutputTokens},
		{"cache_read_tokens", &r.CacheReadTokens},
		{"cache_write_tokens", &r.CacheWriteTokens},
		{"reasoning_tokens", &r.ReasoningTokens},
		{"total_tokens", &r.TotalTokens},
	}
	for i, c := range cols {
		if *c.dst, err = parseCount64(cells[8+i]); err != nil {
			return r, fmt.Errorf("%s cell: %v", c.name, err)
		}
	}
	var ok bool
	if r.CostUsd, ok = ParseMicro(cells[14]); !ok {
		return r, fmt.Errorf("cost_usd cell is not a dollar amount: %s", cells[14])
	}
	r.CreatedAt = cells[15]
	r.Source = cells[16]
	if !validReceiptID(r.Receipt) {
		return r, fmt.Errorf("receipt id is not 32 hexadecimal characters: %s", r.Receipt)
	}
	if !ValidStage(string(r.Stage)) {
		return r, fmt.Errorf("stage %q is not one of the six stages (%s)", r.Stage, strings.Join(stageStrings(), ", "))
	}
	return r, nil
}

// FormatReceiptRow renders one receipt row as its line: the seventeen columns in order,
// tab-separated.
func FormatReceiptRow(r ReceiptRow) string {
	cells := []string{
		oneline.Field(r.Day), oneline.Field(r.Model), oneline.Field(r.Repo), r.Receipt,
		oneline.Field(r.Node), string(r.Stage), oneline.Field(r.Session),
	}
	cells = append(cells,
		strconv.FormatInt(r.Turns, 10),
		strconv.FormatInt(r.InputTokens, 10),
		strconv.FormatInt(r.OutputTokens, 10),
		strconv.FormatInt(r.CacheReadTokens, 10),
		strconv.FormatInt(r.CacheWriteTokens, 10),
		strconv.FormatInt(r.ReasoningTokens, 10),
		strconv.FormatInt(r.TotalTokens, 10),
		Usd(r.CostUsd),
		oneline.Field(r.CreatedAt),
		oneline.Field(r.Source),
	)
	return strings.Join(cells, "\t")
}

func validReceiptID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func stageStrings() []string {
	out := make([]string, len(StageNames))
	for i, n := range StageNames {
		out[i] = string(n)
	}
	return out
}

func parseCount64(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty; a count is a whole number")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("neither a non-negative whole number: %s", s)
	}
	return n, nil
}
