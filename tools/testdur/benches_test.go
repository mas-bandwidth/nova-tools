package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// benchHeading opens one bench's section of docs/TEST-DURATIONS.md, and
// budgetMark picks out the ONE whose numbers the budget is enforced against.
//
// THE BUDGET BELONGS TO A NAMED BENCH. The file has said so since it was written
// -- "the budget below is this bench's" -- and it only mattered once the file
// held a second one. A measurement from another machine is EVIDENCE: it is how
// we know what a Mac costs, which packages are slow for a reason that is not the
// test, and what ratio to expect on the Windows bench when it arrives. It is not
// a BUDGET, because a budget is enforced against a change and a change is not
// answerable for whichever machine somebody measured on. Without this the first
// darwin table would silently become a second budget, and a package one second
// over on a laptop would be a red on every pull request in the repository.
const (
	benchHeading = "## Bench:"
	budgetMark   = "[budget]"
)

func durationsPath() string { return filepath.Join("..", "..", "docs", "TEST-DURATIONS.md") }

// benchRow is one `| <package> | <seconds> | <slowest test> |` line, with the
// bench whose section it was found in.
type benchRow struct {
	bench string
	pkg   string
	secs  float64
}

// benchSection is one `## Bench:` section: its heading, the platform and budget
// factor read out of that heading, and its package rows.
type benchSection struct {
	heading  string
	platform string  // the `<goos>/<goarch>` the heading names, "" when it names none
	budget   bool    // marked [budget]
	factor   float64 // the heading's `budget-factor:`, 1 when it states none
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
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, benchHeading) {
			heading := strings.TrimSpace(strings.TrimPrefix(line, benchHeading))
			benches = append(benches, benchSection{heading: heading, factor: 1})
			cur = len(benches) - 1
			continue
		}
		// Another TOP-LEVEL heading closes the bench's section, so a table
		// written after one cannot be read as that bench's. A `###` inside the
		// section -- the tests over five seconds -- does not close it.
		if strings.HasPrefix(line, "## ") {
			cur = -1
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
		benches[cur].rows = append(benches[cur].rows, benchRow{bench: benches[cur].heading, pkg: pkg, secs: secs})
	}
	return benches, loose, nil
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

// budgetFor answers the ceiling a package total is judged against when the suite
// is running on `platform`, and the bench that ceiling came from.
//
// TODAY IT IGNORES THE PLATFORM. The budget is the `[budget]` bench's sixty
// seconds wherever the suite runs, which is why a Mac has no ceiling at all --
// `cmd/nova-wake` at 62.9 s on the Air is over a minute and nothing reads it.
// platform_budget_test.go is the red that says what this should answer instead.
func budgetFor(benches []benchSection, platform string) (float64, benchSection, error) {
	for _, b := range benches {
		if strings.Contains(b.heading, budgetMark) {
			return budgetSeconds, b, nil
		}
	}
	return 0, benchSection{}, fmt.Errorf("no `%s … %s` section: the budget has to belong to a named bench", benchHeading, budgetMark)
}

// budgetBench is the one bench the budget is enforced against, refusing if the
// file does not say which that is. It is a function rather than a constant for
// the reason every path in this estate is a flag: the answer lives in the file
// a person edits, not in a string here that could fall out of step with it.
func budgetBench(t *testing.T, benches []string) string {
	t.Helper()
	budget := ""
	for _, b := range benches {
		if !strings.Contains(b, budgetMark) {
			continue
		}
		if budget != "" {
			t.Fatalf("two benches are marked %s -- %q and %q; the budget is one bench's", budgetMark, budget, b)
		}
		budget = b
	}
	if budget == "" {
		t.Fatalf("no `%s … %s` section in %s: the budget has to belong to a named bench", benchHeading, budgetMark, durationsPath())
	}
	return budget
}

// EVERY OTHER BENCH IN THE FILE IS STILL A MEASUREMENT SOMEBODY CAN READ. A
// second bench's numbers are not enforced, which is exactly why they need a
// check of their own: an unenforced table is the one nobody would notice had
// gone empty, or had been written outside its heading where nothing reads it.
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
	// And exactly one of them is the budget's.
	budgetBench(t, benches)
}
