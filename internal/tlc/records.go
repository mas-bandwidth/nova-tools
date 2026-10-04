package tlc

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Record is one measured case, one row of tla/RUNS.tsv.
type Record struct {
	Config      string
	Module      string
	InputSHA256 string // fingerprint of what the case reads (Source.Inputs, Digest)
	InputFiles  int    // how many inputs the fingerprint covers
	JarSHA256   string
	JavaVersion string // the version java reported, as JavaVersion reads it
	Host        string // the platform label of the machine that ran TLC (Platform), never a machine name
	CPUs        int    // logical CPUs of that machine
	StartedUTC  string // RFC 3339, microseconds, +00:00
	Workers     int    // TLC workers this case ran with
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
var RecordsHeader = []string{"config", "module", "input_sha256", "input_files", "jar_sha256", "java_version", "host", "cpus", "started_utc", "workers", "generated", "distinct", "seconds", "exit", "result", "expected", "property", "budget", "mode"}

func (r Record) fields() []string {
	return []string{r.Config, r.Module, r.InputSHA256, fmt.Sprint(r.InputFiles), r.JarSHA256, r.JavaVersion, r.Host, fmt.Sprint(r.CPUs), r.StartedUTC, fmt.Sprint(r.Workers), r.Generated, r.Distinct, r.Seconds, fmt.Sprint(r.Exit), r.Result, r.Expected, r.Property, r.Budget, r.Mode}
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

// ReadRecords reads a records file and refuses a header that is not the
// current layout (naming the layout it found and the one expected), a short
// row, and a count or exit cell that is not a plain integer (no trailing text,
// sign or leading zero).
func ReadRecords(r io.Reader) ([]Record, error) {
	cr := csv.NewReader(r)
	cr.Comma = '\t'
	cr.LazyQuotes = true
	cr.FieldsPerRecord = -1
	rows, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("records are not tab-separated text: %v", err)
	}
	if len(rows) == 0 {
		return nil, errors.New("records are empty: no header")
	}
	if strings.Join(rows[0], "\t") != strings.Join(RecordsHeader, "\t") {
		return nil, fmt.Errorf("records are in another layout: found %d columns (%s), this tool reads and writes %d (%s)",
			len(rows[0]), strings.Join(rows[0], ","), len(RecordsHeader), strings.Join(RecordsHeader, ","))
	}
	for n, f := range rows[1:] {
		if len(f) != len(RecordsHeader) {
			return nil, fmt.Errorf("record on line %d has %d fields, want %d", n+2, len(f), len(RecordsHeader))
		}
	}
	col := map[string]int{}
	for i, name := range RecordsHeader {
		col[name] = i
	}
	var out []Record
	for _, f := range rows[1:] {
		count := func(name string) (int, error) {
			n, err := strconv.Atoi(f[col[name]])
			if err != nil || n < 1 || strconv.Itoa(n) != f[col[name]] {
				return 0, fmt.Errorf("record %s has %s %q, not a count", f[0], name, f[col[name]])
			}
			return n, nil
		}
		files, err := count("input_files")
		if err != nil {
			return nil, err
		}
		workers, err := count("workers")
		if err != nil {
			return nil, err
		}
		cpus, err := count("cpus")
		if err != nil {
			return nil, err
		}
		code, err := strconv.Atoi(f[col["exit"]])
		if err != nil || strconv.Itoa(code) != f[col["exit"]] {
			return nil, fmt.Errorf("record %s has exit %q, not a number", f[0], f[col["exit"]])
		}
		g := func(name string) string { return f[col[name]] }
		out = append(out, Record{Config: g("config"), Module: g("module"), InputSHA256: g("input_sha256"), InputFiles: files,
			JarSHA256: g("jar_sha256"), JavaVersion: g("java_version"), Host: g("host"), CPUs: cpus, StartedUTC: g("started_utc"),
			Workers: workers, Generated: g("generated"), Distinct: g("distinct"), Seconds: g("seconds"), Exit: code,
			Result: g("result"), Expected: g("expected"), Property: g("property"), Budget: g("budget"), Mode: g("mode")})
	}
	return out, nil
}

// Platforms are the platform labels a record's host column may hold: the
// <goos>-<goarch> pairs of the operating systems the runner accepts (Linux
// only: TLC runs on a Linux bench) over the architectures Go supports there.
// The column never holds a machine's name.
var Platforms = []string{
	"linux-386", "linux-amd64", "linux-arm", "linux-arm64", "linux-loong64", "linux-mips", "linux-mips64",
	"linux-mips64le", "linux-mipsle", "linux-ppc64", "linux-ppc64le", "linux-riscv64", "linux-s390x",
}

// Platform is the label of the machine that runs TLC: goos and goarch as Go
// names them, joined by a dash. It refuses a pair that is not in Platforms.
func Platform(goos, goarch string) (string, error) {
	label := goos + "-" + goarch
	if !ValidPlatform(label) {
		return "", fmt.Errorf("platform %q is not one the runner records (%s)", label, strings.Join(Platforms, ", "))
	}
	return label, nil
}

// ValidPlatform reports whether label is in Platforms.
func ValidPlatform(label string) bool {
	return slices.Contains(Platforms, label)
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
		if why := declaredMismatch(c, r); why != "" {
			return nil, fmt.Errorf("record for %s does not match the plan: %s; a record names the case it measures, so run the case again and merge without editing the row", c.Config, why)
		}
		fresh, err := current(src, c, r)
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
		return nil, &MissingError{Cases: missing}
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
	jars := slices.Collect(maps.Keys(count))
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
	groups := slices.Sorted(maps.Keys(other))
	return fmt.Errorf("the records hold %d jars: %s; one jar measures the whole file: run again, with the jar %s, the groups recorded under the other jars (%s: %s), or run every group with one jar and merge without --keep",
		len(jars), strings.Join(parts, ", "), short(jars[0]), strings.Join(groups, ", "), strings.Join(cfgs, ", "))
}

func short(h string) string { return h[:min(12, len(h))] }

// declaredMismatch says how a record's copy of its case's declaration (module,
// expected outcome, property) differs from the case the plan declares, or ""
// when they are the same. The copies are not the fingerprint's business (the
// plan row is hashed, the record's cells are not), so a row edited by hand
// would keep a valid fingerprint and still describe another case.
func declaredMismatch(c Case, r Record) string {
	var parts []string
	for _, f := range []struct{ name, recorded, declared string }{
		{"module", r.Module, c.Module},
		{"expected", r.Expected, c.Expected},
		{"property", r.Property, c.Property},
	} {
		if f.recorded != f.declared {
			parts = append(parts, fmt.Sprintf("%s is %q and the plan declares %q", f.name, f.recorded, f.declared))
		}
	}
	return strings.Join(parts, ", ")
}

// current reports whether r is a measurement of case c as it stands: it names
// the fingerprint and the count of files that src gives the case now, and the
// module, expected outcome and property the plan declares for it.
func current(src Source, c Case, r Record) (bool, error) {
	fp, files, err := src.Fingerprint(c.Config)
	if err != nil {
		return false, err
	}
	return r.InputSHA256 == fp && r.InputFiles == files && declaredMismatch(c, r) == "", nil
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
		fresh, err := current(src, c, r)
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

// Dropped is a record of the file a merge keeps that the merge does not carry,
// and why.
type Dropped struct {
	Config string
	Group  string // the case's group, "" when the plan no longer declares the case
	Why    string // DroppedStale or DroppedGone
}

// The reasons a kept record is dropped.
const (
	DroppedStale = "stale"           // its case reads other inputs than it was measured on
	DroppedGone  = "not-in-the-plan" // the plan no longer declares its case
)

// Carry returns the records of base that a merge keeps beside the records of
// runs: those of declared cases that no run measured again and that are still
// current. A base record that is stale, or whose case the plan no longer
// declares, is left out and returned as dropped, so the merge can name it
// instead of carrying a measurement of other inputs.
func Carry(src Source, cases []Case, base []Record, runs ...[]Record) (kept []Record, dropped []Dropped, err error) {
	measured := map[string]bool{}
	for _, run := range runs {
		for _, r := range run {
			measured[r.Config] = true
		}
	}
	declaredCase := map[string]Case{}
	for _, c := range cases {
		declaredCase[c.Config] = c
	}
	for _, r := range base {
		if measured[r.Config] {
			continue
		}
		c, declared := declaredCase[r.Config]
		if !declared {
			dropped = append(dropped, Dropped{Config: r.Config, Why: DroppedGone})
			continue
		}
		fresh, err := current(src, c, r)
		if err != nil {
			return nil, nil, err
		}
		if fresh {
			kept = append(kept, r)
		} else {
			dropped = append(dropped, Dropped{Config: r.Config, Group: c.Group, Why: DroppedStale})
		}
	}
	return kept, dropped, nil
}

// MissingError is a merge that lacks a record for declared cases.
type MissingError struct{ Cases []string }

func (e *MissingError) Error() string {
	return fmt.Sprintf("no record for %d declared cases (%s)", len(e.Cases), strings.Join(e.Cases, ", "))
}
