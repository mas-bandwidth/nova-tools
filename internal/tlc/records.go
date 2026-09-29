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
	InputSHA256 string // fingerprint of what the case reads (Source.Inputs, Digest)
	InputFiles  int    // how many inputs the fingerprint covers
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
var RecordsHeader = []string{"config", "module", "input_sha256", "input_files", "jar_sha256", "host", "started_utc", "generated", "distinct", "seconds", "exit", "result", "expected", "property", "budget", "mode"}

func (r Record) fields() []string {
	return []string{r.Config, r.Module, r.InputSHA256, fmt.Sprint(r.InputFiles), r.JarSHA256, r.Host, r.StartedUTC, r.Generated, r.Distinct, r.Seconds, fmt.Sprint(r.Exit), r.Result, r.Expected, r.Property, r.Budget, r.Mode}
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
		var files, code int
		if _, err := fmt.Sscanf(f[3], "%d", &files); err != nil || files < 1 {
			return nil, fmt.Errorf("record %s has input_files %q, not a count", f[0], f[3])
		}
		if _, err := fmt.Sscanf(f[10], "%d", &code); err != nil {
			return nil, fmt.Errorf("record %s has exit %q, not a number", f[0], f[10])
		}
		out = append(out, Record{f[0], f[1], f[2], files, f[4], f[5], f[6], f[7], f[8], f[9], code, f[11], f[12], f[13], f[14], f[15]})
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
// does not declare, a case measured twice, a record whose fingerprint is not
// the one src gives its case now (measured on other models, another plan row or
// another runner than these: a merge of a stale run and a fresh one is a
// mixture no fingerprint describes), and a declared case that has no record.
func Merge(src Source, cases []Case, runs ...[]Record) ([]Record, error) {
	byConfig := map[string]Record{}
	declared := map[string]bool{}
	for _, c := range cases {
		declared[c.Config] = true
	}
	for _, run := range runs {
		for _, r := range run {
			if !declared[r.Config] {
				return nil, fmt.Errorf("record for %s: no such case in the plan", r.Config)
			}
			if _, dup := byConfig[r.Config]; dup {
				return nil, fmt.Errorf("record for %s appears twice", r.Config)
			}
			byConfig[r.Config] = r
		}
	}
	var stale []string
	for _, c := range cases {
		r, ok := byConfig[c.Config]
		if !ok {
			continue
		}
		fresh, err := current(src, r)
		if err != nil {
			return nil, err
		}
		if !fresh {
			fp, files, _ := src.Fingerprint(c.Config)
			stale = append(stale, fmt.Sprintf("%s (recorded %s over %d files, now %s over %d)", c.Config, short(r.InputSHA256), r.InputFiles, short(fp), files))
		}
	}
	if len(stale) > 0 {
		return nil, fmt.Errorf("%d records were measured on other inputs than these: %s", len(stale), strings.Join(stale, ", "))
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
	if err := OneJar(cases, merged); err != nil {
		return nil, err
	}
	return merged, nil
}

// OneJar refuses a set of records that was not all measured with one jar: a
// file mixing jars says nothing about any one of them. The refusal names each
// jar with how many records it measured, and the groups of the cases recorded
// under the jars other than the commonest, which are the ones to run again.
func OneJar(cases []Case, records []Record) error {
	count := map[string]int{}
	for _, r := range records {
		count[r.JarSHA256]++
	}
	if len(count) <= 1 {
		return nil
	}
	jars := make([]string, 0, len(count))
	for j := range count {
		jars = append(jars, j)
	}
	sort.Slice(jars, func(a, b int) bool {
		if count[jars[a]] != count[jars[b]] {
			return count[jars[a]] > count[jars[b]]
		}
		return jars[a] < jars[b]
	})
	group := map[string]string{}
	for _, c := range cases {
		group[c.Config] = c.Group
	}
	var parts []string
	for _, j := range jars {
		parts = append(parts, fmt.Sprintf("%s (%d records)", short(j), count[j]))
	}
	other := map[string]bool{}
	var cfgs []string
	for _, r := range records {
		if r.JarSHA256 != jars[0] {
			other[group[r.Config]] = true
			cfgs = append(cfgs, r.Config)
		}
	}
	var groups []string
	for g := range other {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	return fmt.Errorf("the records hold %d jars: %s; one jar measures the whole file: run again, with the jar %s, the groups recorded under the other jars (%s: %s), or run every group with one jar and merge without --keep",
		len(jars), strings.Join(parts, ", "), short(jars[0]), strings.Join(groups, ", "), strings.Join(cfgs, ", "))
}

func short(h string) string { return h[:min(12, len(h))] }

// current reports whether r names the fingerprint and the count of files that
// src gives its case now.
func current(src Source, r Record) (bool, error) {
	fp, files, err := src.Fingerprint(r.Config)
	if err != nil {
		return false, err
	}
	return r.InputSHA256 == fp && r.InputFiles == files, nil
}

// StaleGroups returns the groups, sorted, that hold a declared case with no
// current record among records: the groups to run again after an edit. A case
// is current when its record names the fingerprint src gives it now.
func StaleGroups(src Source, cases []Case, records []Record) ([]string, error) {
	byConfig := map[string]Record{}
	for _, r := range records {
		byConfig[r.Config] = r
	}
	set := map[string]bool{}
	for _, c := range cases {
		r, ok := byConfig[c.Config]
		if !ok {
			set[c.Group] = true
			continue
		}
		fresh, err := current(src, r)
		if err != nil {
			return nil, err
		}
		if !fresh {
			set[c.Group] = true
		}
	}
	out := make([]string, 0, len(set))
	for g := range set {
		out = append(out, g)
	}
	sort.Strings(out)
	return out, nil
}

// Carry returns the records of base that a merge keeps beside the records of
// runs: those of declared cases that no run measured again and that are still
// current. A base record that is stale is left out, so the merge names its case
// as missing instead of carrying a measurement of other inputs.
func Carry(src Source, cases []Case, base []Record, runs ...[]Record) ([]Record, error) {
	measured := map[string]bool{}
	for _, run := range runs {
		for _, r := range run {
			measured[r.Config] = true
		}
	}
	declared := map[string]bool{}
	for _, c := range cases {
		declared[c.Config] = true
	}
	var kept []Record
	for _, r := range base {
		if measured[r.Config] || !declared[r.Config] {
			continue
		}
		fresh, err := current(src, r)
		if err != nil {
			return nil, err
		}
		if fresh {
			kept = append(kept, r)
		}
	}
	return kept, nil
}
