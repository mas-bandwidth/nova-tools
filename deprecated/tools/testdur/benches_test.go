package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// benchHeading opens one bench's section of docs/TEST-DURATIONS.md.
//
// THE RECORD IS EVIDENCE, NOT A BUDGET (nova-tools#4413, 2026-09-26: one place
// for every time budget). It is how we know what a Mac costs against a Linux
// bench and which packages are slow for a reason that is not the test. The one
// time budget is `nova-ci slowtests` over a live run's go test -json, enforced
// on the nightly space legs only (internal/ci/slowtests); the record's own
// sixty-second check and its per-platform budget-factor went with #4413.
const benchHeading = "## Bench:"

func durationsPath() string { return filepath.Join("..", "..", "docs", "TEST-DURATIONS.md") }

// benchRow is one `| <package> | <seconds> | <slowest test> |` line, with the
// bench whose section it was found in.
type benchRow struct {
	bench string
	pkg   string
	secs  float64
}

// benchSection is one `## Bench:` section: its heading, the platform read out
// of that heading, and its package rows.
type benchSection struct {
	heading  string
	platform string // the `<goos>/<goarch>` the heading names, "" when it names none
	rows     []benchRow
}

// parseRecord splits the record's TEXT into its bench sections. It takes text
// rather than a path so the shapes below can be pinned against a record written
// out in the test, which is the only way to prove what the parser does with a
// heading the real file does not happen to carry today.
//
// A row outside any bench section is REPORTED rather than ignored: the file is
// hand-edited, and a table that drifted above the first heading is a measurement
// nothing is holding.
func parseRecord(text string) (benches []benchSection, loose []string, err error) {
	cur := -1
	// A bench's PACKAGE table is the one directly under its heading. Any
	// sub-heading ends it, because the tables under a `###` are about something
	// else and their columns only look alike. #1411's "The five biggest,
	// against Space" is `| package | darwin/arm64 | linux/amd64 | ratio |`,
	// whose second cell is a number too -- so without this every row of it was
	// read as a second, contradictory measurement for the same package on the
	// same bench, silently.
	inRows := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, benchHeading) {
			heading := strings.TrimSpace(strings.TrimPrefix(line, benchHeading))
			section, herr := parseHeading(heading)
			if herr != nil {
				return nil, nil, herr
			}
			benches = append(benches, section)
			cur, inRows = len(benches)-1, true
			continue
		}
		// Another TOP-LEVEL heading closes the bench's section, so a table
		// written after one cannot be read as that bench's at all.
		if strings.HasPrefix(line, "## ") {
			cur, inRows = -1, false
			continue
		}
		// A sub-heading keeps us inside the bench -- a row here is not loose --
		// but ends its package table.
		if strings.HasPrefix(line, "#") {
			inRows = false
			continue
		}
		// The table rows are `| <package> | <seconds> | <slowest test> |`; the
		// header and its dashes are the two rows whose second cell is not a
		// number, and in the "tests over five seconds" table the second cell is
		// a test name, so neither parses here.
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(cells) < 2 {
			continue
		}
		pkg := strings.TrimSpace(cells[0])
		secs, perr := strconv.ParseFloat(strings.TrimSpace(cells[1]), 64)
		if perr != nil {
			continue
		}
		if cur < 0 {
			loose = append(loose, pkg)
			continue
		}
		if !inRows {
			continue // a row of a labelled sub-table, not a package measurement
		}
		benches[cur].rows = append(benches[cur].rows, benchRow{bench: benches[cur].heading, pkg: pkg, secs: secs})
	}
	return benches, loose, nil
}

// parseHeading reads the platform a `## Bench:` heading states: the
// comma-separated field that looks like `<goos>/<goarch>`, found by its shape
// and not by its position, because the heading is also the line a PERSON reads
// (`the Air, darwin/arm64, 8 cores`).
func parseHeading(heading string) (benchSection, error) {
	section := benchSection{heading: heading}
	for _, field := range strings.Split(heading, ",") {
		if field = strings.TrimSpace(field); goosArch(field) {
			section.platform = field
		}
	}
	return section, nil
}

// goosArch reports whether a heading field is a `<goos>/<goarch>` pair: two
// non-empty halves around exactly one slash, and no space in either. It is a
// SHAPE and not a list of the platforms we run on today, so a bench on a
// platform nobody has thought of is still read.
func goosArch(field string) bool {
	goos, goarch, ok := strings.Cut(field, "/")
	if !ok || goos == "" || goarch == "" {
		return false
	}
	return !strings.ContainsAny(goos, " \t") && !strings.ContainsAny(goarch, " \t/")
}

// readRecord reads the file and parses it.
func readRecord(t *testing.T) (rows []benchRow, benches []string, loose []string) {
	t.Helper()
	raw, err := os.ReadFile(durationsPath())
	if err != nil {
		t.Fatalf("the recorded durations are missing: %v", err)
	}
	sections, loose, perr := parseRecord(string(raw))
	if perr != nil {
		t.Fatalf("%s: %v", durationsPath(), perr)
	}
	for _, s := range sections {
		benches = append(benches, s.heading)
		rows = append(rows, s.rows...)
	}
	return rows, benches, loose
}

// EVERY BENCH IN THE FILE IS A MEASUREMENT SOMEBODY CAN READ. No bench's
// numbers are enforced here, which is exactly why they need a check of their
// own: an unenforced table is the one nobody would notice had gone empty, or
// had been written outside its heading where nothing reads it.
func TestEveryRecordedBenchIsNamedAndHasRows(t *testing.T) {
	rows, benches, loose := readRecord(t)
	if len(benches) < 2 {
		t.Errorf("%s records %d bench(es); the ratio between a Linux bench and a Mac one is a thing a person comes to this file for, and the Windows bench will be a third",
			durationsPath(), len(benches))
	}
	if len(loose) > 0 {
		t.Errorf("%s holds rows outside any `%s` section (%s); a table above the first heading belongs to no bench and nothing reads it",
			durationsPath(), benchHeading, strings.Join(loose, ", "))
	}
	for _, b := range benches {
		n := 0
		for _, r := range rows {
			if r.bench == b {
				n++
			}
		}
		if n == 0 {
			t.Errorf("the `%s %s` section holds no package rows", benchHeading, b)
		}
	}
	// Each heading names the platform it was measured on, which is the one
	// thing a reader needs in order to tell two rows for one package apart.
	for _, b := range benches {
		if !strings.Contains(b, "/") {
			t.Errorf("the bench heading %q does not name its <goos>/<goarch>", b)
		}
	}
}

// A bench's package table is the one under its own heading. The record carries
// other tables inside a bench's section whose second column is a number too,
// and reading those as package measurements is a second, contradictory row for
// the same package on the same bench -- silently.
func TestOnlyTheTableUnderTheHeadingIsTheMeasurement(t *testing.T) {
	const record = `## Bench: the Air, darwin/arm64, 8 cores

| package | total seconds | slowest test |
| --- | --- | --- |
| cmd/nova-wake | 62.9 | - |

### The five biggest, against Space

| package | darwin/arm64 | linux/amd64 | ratio |
| --- | --- | --- | --- |
| cmd/nova-wake | 62.9 | 12.4 | 5.1x |
| cmd/nova-merge | 51.4 | 16.7 | 3.1x |
`
	benches, loose, err := parseRecord(record)
	if err != nil {
		t.Fatalf("parseRecord: %v", err)
	}
	if len(loose) != 0 {
		t.Errorf("rows inside a bench's sub-table read as loose: %v", loose)
	}
	if len(benches) != 1 || benches[0].platform != "darwin/arm64" {
		t.Fatalf("parsed %+v, want one darwin/arm64 section", benches)
	}
	if len(benches[0].rows) != 1 || benches[0].rows[0].pkg != "cmd/nova-wake" {
		t.Errorf("the Air's package rows = %v, want the one row under its heading", benches[0].rows)
	}
}
