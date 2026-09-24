package cost

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/route"
)

// Exit codes of `nova-sprint cost import`. 7 is not used: in nova-sprint it already means
// DOWN (`task push`, #2929), so a partial write is 9, never 7.
const (
	ExitOK        = 0
	ExitUsage     = 2 // could not run: a flag, the provider, no --file or --redis
	ExitExport    = 3 // bad export
	ExitReconcile = 4 // does not reconcile; nothing written
	ExitPreWrite  = 6 // pre-write Redis failure; nothing written, proven
	ExitUnknown   = 8 // outcome unknown: a read-back got no reply
	ExitPartial   = 9 // the write failed in part after one retry; every reply received
)

// Writer is the value of every hash's `writer` field: the only writer of cost:*.
const Writer = "nova-sprint cost import"

// IndexKey is the zset of imported days, member `<provider>:<day>`, score YYYYMMDD.
const IndexKey = "cost:idx"

// tolerance is $0.01 in micro-dollars: how far a total may sit from the rows' own sum.
const tolerance = 10_000

// providerVia is the routes.yaml `via` each provider's rows are matched on. anthropic has
// none: every anthropic row is `model:<model>`.
var providerVia = map[string]string{
	"anthropic":  "",
	"openrouter": "openrouter",
	"oc":         "opencode",
}

// Providers are the accepted --provider values, sorted.
func Providers() []string {
	out := make([]string, 0, len(providerVia))
	for p := range providerVia {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ValidProvider reports whether p is a provider this verb imports.
func ValidProvider(p string) bool {
	_, ok := providerVia[p]
	return ok
}

// Day is one UTC day of an export, reconciled.
type Day struct {
	Day      string           // YYYY-MM-DD
	Rows     int              // data rows
	Unrouted int              // rows mapped to model:<model>
	Micro    int64            // the rows' sum in micro-dollars: the reference and the total
	Fields   map[string]int64 // <project>|<route> -> micro-dollars
	RowTotal *int64           // the export's own total row, when it has one
}

// Score is the day's cost:idx score, YYYYMMDD.
func (d Day) Score() float64 {
	n, _ := strconv.Atoi(strings.ReplaceAll(d.Day, "-", ""))
	return float64(n)
}

// Export is one provider export file, parsed.
type Export struct {
	Provider   string
	SourceName string // the file's basename
	SHA256     string // hex of the file's bytes
	Days       []Day  // in day order
}

// ExportError is a bad export: exit 3.
type ExportError struct{ Msg string }

func (e *ExportError) Error() string { return e.Msg }

// ReconcileError is an export whose fields or total do not add up: exit 4.
type ReconcileError struct{ Msg string }

func (e *ReconcileError) Error() string { return e.Msg }

func badExport(format string, args ...any) error {
	return &ExportError{Msg: fmt.Sprintf(format, args...)}
}

// Code is the exit code an error from Load, Parse or Reconcile maps to.
func Code(err error) int {
	var ee *ExportError
	var re *ReconcileError
	switch {
	case err == nil:
		return ExitOK
	case errors.As(err, &re):
		return ExitReconcile
	case errors.As(err, &ee):
		return ExitExport
	default:
		return ExitExport
	}
}

// Load reads the export at path, parses it against the embedded routes.yaml and reconciles
// it. The error, if any, is an *ExportError or a *ReconcileError (see Code).
func Load(provider, path string) (*Export, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, badExport("cannot read the export: %v", err)
	}
	table, err := route.Load()
	if err != nil {
		return nil, badExport("routes.yaml: %v", err)
	}
	e, err := Parse(provider, filepath.Base(path), data, table.Rows())
	if err != nil {
		return nil, err
	}
	if err := e.Reconcile(); err != nil {
		return nil, err
	}
	return e, nil
}

// Parse reads one CSV export. Each data row goes to the field `<project>|<route>`, where the
// route is the routes.yaml row with the provider's via and the same model, or
// `model:<model>` when there is none (and for every anthropic row). Rows that share a field
// are summed in integer micro-dollars.
func Parse(provider, sourceName string, data []byte, routes []route.Row) (*Export, error) {
	via, ok := providerVia[provider]
	if !ok {
		return nil, badExport("unknown provider %q (want one of %s)", provider, strings.Join(Providers(), ", "))
	}
	byModel := map[string]string{}
	if via != "" {
		for _, r := range routes {
			if r.Via == via {
				byModel[r.Model] = r.Route
			}
		}
	}
	sum := sha256.Sum256(data)
	e := &Export{Provider: provider, SourceName: sourceName, SHA256: hex.EncodeToString(sum[:])}

	r := csv.NewReader(bytes.NewReader(data))
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err == io.EOF {
		return nil, badExport("the export is empty: no header row")
	}
	if err != nil {
		return nil, badExport("the export's header: %v", err)
	}
	col, err := columnIndex(header)
	if err != nil {
		return nil, badExport("%v", err)
	}

	days := map[string]*Day{}
	var totals []int64
	line := 1
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			return nil, badExport("line %d: %v", line, err)
		}
		cell := func(c Column) string {
			i := col[c]
			if i < 0 || i >= len(rec) {
				return ""
			}
			return strings.TrimSpace(rec[i])
		}
		micro, err := parseUSD(cell(ColCost))
		if err != nil {
			return nil, badExport("line %d: cost %q: %v", line, cell(ColCost), err)
		}
		dayCell := cell(ColDay)
		if strings.EqualFold(dayCell, "total") {
			totals = append(totals, micro)
			continue
		}
		day, err := parseDay(dayCell)
		if err != nil {
			return nil, badExport("line %d: day %q: %v", line, dayCell, err)
		}
		model := cell(ColModel)
		if model == "" {
			return nil, badExport("line %d: empty model", line)
		}
		project := cell(ColProject)
		if project == "" {
			project = "-"
		}
		if strings.Contains(project, "|") || strings.Contains(model, "|") {
			return nil, badExport("line %d: a project or model contains |: %q, %q", line, project, model)
		}
		rt, routed := byModel[model]
		if !routed {
			rt = "model:" + model
		}
		d := days[day]
		if d == nil {
			d = &Day{Day: day, Fields: map[string]int64{}}
			days[day] = d
		}
		d.Rows++
		if !routed {
			d.Unrouted++
		}
		d.Micro += micro
		d.Fields[project+"|"+rt] += micro
	}
	if len(days) == 0 {
		return nil, badExport("the export has no data rows")
	}
	if len(totals) > 0 {
		if len(days) > 1 {
			return nil, badExport("a total row is allowed only in a single-day file; this one has %d days", len(days))
		}
		if len(totals) > 1 {
			return nil, badExport("the export has %d total rows; want at most one", len(totals))
		}
	}
	for _, d := range days {
		if len(totals) == 1 {
			t := totals[0]
			d.RowTotal = &t
		}
		e.Days = append(e.Days, *d)
	}
	sort.Slice(e.Days, func(i, j int) bool { return e.Days[i].Day < e.Days[j].Day })
	return e, nil
}

// Reconcile checks every day: the fields sum to the day's rows within $0.01, and the
// export's own total row, when there is one, equals that sum within $0.01. Any miss is a
// *ReconcileError, before anything is written.
func (e *Export) Reconcile() error {
	for _, d := range e.Days {
		var fields int64
		for _, v := range d.Fields {
			fields += v
		}
		if diff := fields - d.Micro; diff > tolerance || diff < -tolerance {
			return &ReconcileError{Msg: fmt.Sprintf("day %s: the fields sum to %s but the rows to %s", d.Day, formatMicro(fields), formatMicro(d.Micro))}
		}
		if d.RowTotal != nil {
			if diff := *d.RowTotal - d.Micro; diff > tolerance || diff < -tolerance {
				return &ReconcileError{Msg: fmt.Sprintf("day %s: the export's total row says %s but its rows sum to %s", d.Day, formatMicro(*d.RowTotal), formatMicro(d.Micro))}
			}
		}
	}
	return nil
}

// parseDay takes the first ten characters of a day cell as a UTC YYYY-MM-DD.
func parseDay(s string) (string, error) {
	if len(s) < 10 {
		return "", fmt.Errorf("want a UTC YYYY-MM-DD")
	}
	d := s[:10]
	if _, err := time.Parse("2006-01-02", d); err != nil {
		return "", fmt.Errorf("want a UTC YYYY-MM-DD")
	}
	return d, nil
}

// parseUSD reads a dollar amount >= 0 into micro-dollars.
func parseUSD(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v > 1e12 {
		return 0, fmt.Errorf("not a dollar amount")
	}
	if v < 0 {
		return 0, fmt.Errorf("negative")
	}
	return int64(math.Round(v * 1e6)), nil
}

// formatMicro prints micro-dollars as dollars with six decimals, exactly.
func formatMicro(m int64) string {
	sign := ""
	if m < 0 {
		sign, m = "-", -m
	}
	return fmt.Sprintf("%s%d.%06d", sign, m/1_000_000, m%1_000_000)
}
