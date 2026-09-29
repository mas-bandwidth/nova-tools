package tlc

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Record is one measured case, one row of tla/RUNS.tsv.
type Record struct {
	Config      string
	Module      string
	InputSHA256 string // Fingerprint of the inputs the case ran on
	JarSHA256   string
	Host        string
	StartedUTC  string // RFC 3339, microseconds, +00:00
	Generated   string // states generated; "-" when unknown
	Distinct    string // distinct states; "-" when unknown
	Seconds     string // elapsed, three decimals
	Exit        int
	Result      string // PASS or FAIL
	Expected    string
	Property    string
	Budget      string // seconds, shortest form
	Mode        string // bounded or manual
}

// RecordsHeader is the header of tla/RUNS.tsv.
var RecordsHeader = []string{"config", "module", "input_sha256", "jar_sha256", "host", "started_utc", "generated", "distinct", "seconds", "exit", "result", "expected", "property", "budget", "mode"}

func (r Record) fields() []string {
	return []string{r.Config, r.Module, r.InputSHA256, r.JarSHA256, r.Host, r.StartedUTC, r.Generated, r.Distinct, r.Seconds, fmt.Sprint(r.Exit), r.Result, r.Expected, r.Property, r.Budget, r.Mode}
}

// WriteRecords writes the header and the records, one line each.
func WriteRecords(w io.Writer, records []Record) error {
	if _, err := io.WriteString(w, strings.Join(RecordsHeader, "\t")+"\n"); err != nil {
		return err
	}
	for _, r := range records {
		row := r.fields()
		for _, f := range row {
			if strings.ContainsAny(f, "\t\r\n") {
				return fmt.Errorf("record field for %s holds a tab or a line break", r.Config)
			}
		}
		if _, err := io.WriteString(w, strings.Join(row, "\t")+"\n"); err != nil {
			return err
		}
	}
	return nil
}

// ReadRecords reads a records file and refuses a wrong header or a short row.
func ReadRecords(r io.Reader) ([]Record, error) {
	cr := csv.NewReader(r)
	cr.Comma = '\t'
	cr.LazyQuotes = true
	cr.FieldsPerRecord = len(RecordsHeader)
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("records are not tab-separated with %d fields: %v", len(RecordsHeader), err)
	}
	if len(rows) == 0 || strings.Join(rows[0], "\t") != strings.Join(RecordsHeader, "\t") {
		return nil, fmt.Errorf("records have an incorrect TSV header")
	}
	var out []Record
	for _, f := range rows[1:] {
		var code int
		if _, err := fmt.Sscanf(f[9], "%d", &code); err != nil {
			return nil, fmt.Errorf("record %s has exit %q, not a number", f[0], f[9])
		}
		out = append(out, Record{f[0], f[1], f[2], f[3], f[4], f[5], f[6], f[7], f[8], code, f[10], f[11], f[12], f[13], f[14]})
	}
	return out, nil
}

// ReadRecordsFile reads the records at path.
func ReadRecordsFile(path string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	recs, err := ReadRecords(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	return recs, nil
}

// Merge joins the records of several runs (one per group) into the complete
// set, in the order of the case plan. It refuses a record for a case the plan
// does not declare, a case measured twice, records taken on different inputs
// (a merge of a stale run and a fresh one is a mixture no fingerprint
// describes), and a declared case that has no record.
func Merge(cases []Case, runs ...[]Record) ([]Record, error) {
	byConfig := map[string]Record{}
	declared := map[string]bool{}
	for _, c := range cases {
		declared[c.Config] = true
	}
	inputs := map[string]bool{}
	for _, run := range runs {
		for _, r := range run {
			if !declared[r.Config] {
				return nil, fmt.Errorf("record for %s: no such case in the plan", r.Config)
			}
			if _, dup := byConfig[r.Config]; dup {
				return nil, fmt.Errorf("record for %s appears twice", r.Config)
			}
			byConfig[r.Config] = r
			inputs[r.InputSHA256] = true
		}
	}
	if len(inputs) > 1 {
		var hashes []string
		for h := range inputs {
			hashes = append(hashes, h[:min(12, len(h))])
		}
		sort.Strings(hashes)
		return nil, fmt.Errorf("records were measured on %d different inputs (%s)", len(inputs), strings.Join(hashes, ", "))
	}
	var missing []string
	merged := make([]Record, 0, len(cases))
	for _, c := range cases {
		r, ok := byConfig[c.Config]
		if !ok {
			missing = append(missing, c.Config)
			continue
		}
		merged = append(merged, r)
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("no record for %d declared cases (%s)", len(missing), strings.Join(missing, ", "))
	}
	return merged, nil
}
