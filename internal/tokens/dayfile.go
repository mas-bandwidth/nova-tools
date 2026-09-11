package tokens

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The day file: one file per day, eleven columns, every one written on every row.
//
// It is RECOMPUTED WHOLE from the sources every time and never appended to, never edited
// in place: the write goes to one fixed temp name in the same directory and lands by one
// atomic rename. The fixed name is safe because one fold runs per output directory (the
// lock in lock.go), and a stranded temp is then a name a person can see rather than a
// scatter of `.tmp.<pid>` files nobody can tell apart. `check` steps over exactly that
// name, so the wreckage of a killed fold is not reported as a stray.

// Version is the first token of every day file's first line. A file whose first line is
// not this is refused by `sum` and named by `check`, and the repair is `fold --day <d>`.
const Version = "nova-tokens v1"

// TempSuffix is the one fixed temp name, per rule 8.
const TempSuffix = ".tsv.tmp"

// FileSuffix is a day file's extension.
const FileSuffix = ".tsv"

// Columns are the eleven, in order, and the file's second line is exactly these.
var Columns = []string{"date", "model", "repo", "input", "output", "cache_write", "cache_read", "reasoning", "rough", "day_basis", "sources"}

// HeaderLine is the second line of every day file.
var HeaderLine = strings.Join(Columns, "\t")

// DayRow is one written row.
type DayRow struct {
	Date, Model, Repo string
	Counts            Counts
	Rough             int
	Basis             string
	Sources           []string
}

// DayFile is a whole day file, parsed or about to be written.
type DayFile struct {
	Day     string
	At      string
	Build   string
	Turns   string // a decimal integer, or a dash
	Sources []string
	Rows    []DayRow
}

// Finding is one thing wrong with a file on disk, with the line it is on (0 for the file
// as a whole).
type Finding struct {
	Line   int
	Reason string
}

// Path is where a day file lives under an output directory.
func Path(out, day string) string { return filepath.Join(out, day+FileSuffix) }

// TempPath is the one fixed temp name beside it.
func TempPath(out, day string) string { return filepath.Join(out, day+TempSuffix) }

// Render is the file's bytes.
//
// EVERY STORED CELL GOES THROUGH oneline.Field, and that is what makes eleven
// tab-separated columns a promise rather than a hope: a model id or a repo name holding a
// tab would otherwise write a twelve-column row, and one holding a newline would write two
// rows, from a value this tool copied out of somebody's transcript. Field escapes
// whitespace and "=", so an ordinary name is untouched and a hostile one is one token.
func (d *DayFile) Render() string {
	rows := make([]string, 0, len(d.Rows)+2)
	rows = append(rows, fmt.Sprintf("%s day=%s at=%s build=%s turns=%s sources=%s",
		Version, oneline.Field(d.Day), oneline.Field(d.At), oneline.Field(d.Build),
		oneline.Field(d.Turns), oneline.Field(strings.Join(d.Sources, ","))), HeaderLine)
	for _, r := range d.Rows {
		cells := []string{oneline.Field(r.Date), oneline.Field(r.Model), oneline.Field(r.Repo)}
		for t := Type(0); t < NTypes; t++ {
			cells = append(cells, r.Counts.Cell(t))
		}
		cells = append(cells, strconv.Itoa(r.Rough), oneline.Field(r.Basis),
			oneline.Field(strings.Join(r.Sources, ",")))
		rows = append(rows, strings.Join(cells, "\t"))
	}
	return strings.Join(rows, "\n") + "\n"
}

// Save recomputes the file whole: the bytes to the fixed temp name in the same directory,
// then one rename. Nothing is appended and nothing is edited in place.
func (d *DayFile) Save(out string) error {
	tmp := TempPath(out, d.Day)
	if err := os.WriteFile(tmp, []byte(d.Render()), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, Path(out, d.Day))
}

// Totals is the day's per-type totals, and whether any row reported each type. It is what
// the shrink comparison compares.
func (d *DayFile) Totals() Counts {
	var c Counts
	for _, r := range d.Rows {
		c.Add(r.Counts)
	}
	return c
}

// Shrink is one type that would go backwards.
type Shrink struct {
	Day  string
	Type Type
	File string // the number in the file
	Now  string // the number now, or a dash where the source went quiet
}

// Shrinks compares what is on disk with what a fold has just computed. A type that is
// lower, or that was a number and is a dash now, is a source that went quiet, and a source
// that went quiet must never silently lower a day's spend.
//
// A dash in the file that is a number now is NOT a shrink: that is coverage arriving.
func Shrinks(old, now Counts, day string) []Shrink {
	var out []Shrink
	for t := Type(0); t < NTypes; t++ {
		was, hadIt := old.Get(t)
		is, hasIt := now.Get(t)
		switch {
		case hadIt && !hasIt:
			out = append(out, Shrink{Day: day, Type: t, File: strconv.FormatInt(was, 10), Now: Dash})
		case hadIt && hasIt && is < was:
			out = append(out, Shrink{Day: day, Type: t, File: strconv.FormatInt(was, 10), Now: strconv.FormatInt(is, 10)})
		}
	}
	return out
}

// ParseDayFile reads a day file and returns everything wrong with it rather than the first
// thing: a run over a month wants one line per finding, and a reader sent back once per
// problem is a reader sent back eight times.
//
// name is the file's own name without its suffix, which the `date` column must equal.
func ParseDayFile(name, text string) (DayFile, []Finding) {
	var d DayFile
	var f []Finding
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return d, []Finding{{Line: 0, Reason: "the file is empty; a day with no rows has no file, and an empty one is malformed"}}
	}
	head := lines[0]
	if !strings.HasPrefix(head, Version+" ") {
		return d, []Finding{{Line: 1, Reason: "the first line is not `" + Version + " day=… at=… build=… turns=… sources=…`; the repair is fold --day <d>"}}
	}
	fields := map[string]string{}
	for _, tok := range strings.Fields(strings.TrimPrefix(head, Version+" ")) {
		if k, v, ok := strings.Cut(tok, "="); ok {
			fields[k] = v
		}
	}
	d.Day, d.At, d.Build, d.Turns = fields["day"], fields["at"], fields["build"], fields["turns"]
	if s := fields["sources"]; s != "" {
		d.Sources = strings.Split(s, ",")
	}
	for _, want := range []string{"day", "at", "build", "turns"} {
		if _, ok := fields[want]; !ok {
			f = append(f, Finding{Line: 1, Reason: "the version line carries no `" + want + "=`; it wants " + Version + " day=… at=… build=… turns=<n or -> sources=…"})
		}
	}
	// Rule 13: "the version line carries `turns=` as an integer or `-`". An EMPTY value is
	// present but says nothing, and a NEGATIVE one is not a count of messages; both read
	// clean when the check was only `!= "" && != Dash`.
	if _, ok := fields["turns"]; ok && d.Turns != Dash {
		n, err := strconv.Atoi(d.Turns)
		switch {
		case d.Turns == "":
			f = append(f, Finding{Line: 1, Reason: "turns= carries no value; it wants a whole number of messages counted, or `-` when no source counted any"})
		case err != nil:
			f = append(f, Finding{Line: 1, Reason: "turns= is neither a whole number nor `-`: " + d.Turns})
		case n < 0:
			f = append(f, Finding{Line: 1, Reason: "turns= is negative: " + d.Turns + "; it counts messages, and a count is never below zero"})
		}
	}
	if d.Day != name {
		f = append(f, Finding{Line: 1, Reason: "the version line says day=" + d.Day + " and the file is named " + name})
	}
	if len(lines) < 2 || lines[1] != HeaderLine {
		f = append(f, Finding{Line: 2, Reason: "the second line is not the eleven column names: " + HeaderLine})
		return d, f
	}
	var last string
	seen := map[string]bool{}
	for i := 2; i < len(lines); i++ {
		n := i + 1
		line := lines[i]
		if line == "" {
			f = append(f, Finding{Line: n, Reason: "a blank line; every row has eleven columns"})
			continue
		}
		cells := strings.Split(line, "\t")
		if len(cells) != len(Columns) {
			f = append(f, Finding{Line: n, Reason: fmt.Sprintf("%d columns, want %d (%s)", len(cells), len(Columns), HeaderLine)})
			continue
		}
		row := DayRow{Date: cells[0], Model: cells[1], Repo: cells[2]}
		bad := false
		for t := Type(0); t < NTypes; t++ {
			cell := cells[3+int(t)]
			switch {
			case cell == "":
				f = append(f, Finding{Line: n, Reason: "the " + TypeNames[t] + " cell is empty; a type a source did not report is `-`, never empty"})
				bad = true
			case cell == Dash:
			default:
				v, err := strconv.ParseInt(cell, 10, 64)
				if err != nil || v < 0 {
					f = append(f, Finding{Line: n, Reason: "the " + TypeNames[t] + " cell is neither a non-negative whole number nor `-`: " + cell})
					bad = true
					continue
				}
				row.Counts.Set(t, v)
			}
		}
		rough, err := strconv.Atoi(cells[8])
		if err != nil || rough < 0 {
			f = append(f, Finding{Line: n, Reason: "the rough cell is not a count: " + cells[8]})
			bad = true
		}
		row.Rough = rough
		row.Basis = cells[9]
		if row.Basis != UTC && !ValidZone(row.Basis) {
			f = append(f, Finding{Line: n, Reason: "day_basis is neither `utc` nor a zone name without whitespace: " + cells[9]})
			bad = true
		}
		if cells[10] == "" {
			f = append(f, Finding{Line: n, Reason: "the sources cell is empty; every number is traceable to the flags of the run that wrote it"})
			bad = true
		}
		row.Sources = strings.Split(cells[10], ",")
		if row.Date != name {
			f = append(f, Finding{Line: n, Reason: "the date column is " + row.Date + " and the file is named " + name})
			bad = true
		}
		key := row.Model + "\t" + row.Repo
		if seen[key] {
			f = append(f, Finding{Line: n, Reason: "a second row for (" + row.Model + ", " + row.Repo + "); rows are unique by (model, repo)"})
			bad = true
		} else if last != "" && key < last {
			f = append(f, Finding{Line: n, Reason: "out of order; rows are sorted by (model, repo)"})
			bad = true
		}
		seen[key] = true
		last = key
		if !bad {
			d.Rows = append(d.Rows, row)
		}
	}
	if len(lines) == 2 {
		f = append(f, Finding{Line: 0, Reason: "no rows; a day with no rows has no file"})
	}
	return d, f
}

// ReadDayFile reads and parses one day file from disk.
func ReadDayFile(path string) (DayFile, []Finding, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return DayFile{}, nil, err
	}
	name := strings.TrimSuffix(filepath.Base(path), FileSuffix)
	d, f := ParseDayFile(name, string(raw))
	return d, f, nil
}

// MissingDays is every day between the first and the last with no file. A missing day is
// NAMED and never filled: nobody folded it, and the tool will not invent what nobody
// measured.
func MissingDays(days []string) []string {
	if len(days) < 2 {
		return nil
	}
	sorted := append([]string(nil), days...)
	sort.Strings(sorted)
	have := map[string]bool{}
	for _, d := range sorted {
		have[d] = true
	}
	var out []string
	for d := nextDay(sorted[0]); d != "" && d < sorted[len(sorted)-1]; d = nextDay(d) {
		if !have[d] {
			out = append(out, d)
		}
	}
	return out
}

// nextDay is the day after, by the calendar, without pulling in a clock.
func nextDay(day string) string {
	y, err1 := strconv.Atoi(day[0:4])
	m, err2 := strconv.Atoi(day[5:7])
	d, err3 := strconv.Atoi(day[8:10])
	if err1 != nil || err2 != nil || err3 != nil {
		return ""
	}
	d++
	if d > daysIn(y, m) {
		d = 1
		m++
		if m > 12 {
			m = 1
			y++
		}
	}
	return fmt.Sprintf("%04d-%02d-%02d", y, m, d)
}

func daysIn(y, m int) int {
	switch m {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	}
	if y%4 == 0 && (y%100 != 0 || y%400 == 0) {
		return 29
	}
	return 28
}
