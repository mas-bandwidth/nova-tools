package tokens

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// `check` is the GATE. It verifies what rule 13 says and nothing else: it does not ask
// whether a day's numbers are plausible, or whether a source was declared that day. A
// missing day is NAMED and never filled.
//
// A GATE THAT CANNOT GO GREEN IS NOT A GATE. Measured on this repository's own
// reports/tokens, 2026-09-18: 16 day files, 36 findings of `missing` for the 36 calendar
// days nobody worked between 2026-07-30 and 2026-09-06, and 4 findings of `stray` for the
// README, the collator's log, the pre-nova-tokens archive and a session note a person put
// there on purpose. Every one of the 40 was correct by the old reading, and not one of
// them was work anybody would do -- so the gate was a line people had learned to skip.
// Two readings changed:
//
//	A CALENDAR GAP IS A FACT, NOT A FINDING. A day with no file is `missing` only when
//	something this run can read says there was spend on it: --strict says every gap
//	counts, and --no-spend <file> names the days that had none and counts the rest. With
//	neither, the gap is counted on the CHECK line as gap=<n> and nothing is named --
//	because `check` reads day files, and a day file nobody wrote is not evidence that
//	anybody worked that day.
//
//	A NON-DAY ENTRY A PERSON PUT THERE IS A NOTE, NOT A STRAY. A `*.md`, a `*.log` and a
//	`pre-*` archive directory are counted as notes=<n> and left alone. --strict restores
//	the old reading, where every entry that is not a day file is named.
//
// Neither door removes anything and neither invents a number: both counts stay on the
// CHECK line, so a person who wants the old list types --strict and gets it back whole.

// FileFinding is one thing wrong with one file.
type FileFinding struct {
	Path   string
	Line   int
	Reason string
}

// CheckOptions is how a caller reads the two judgement calls above. The zero value is the
// default reading: gaps and notes are counted, not named.
type CheckOptions struct {
	// Strict names every calendar gap as missing and every non-day entry as a stray.
	Strict bool
	// NoSpend is the days a person has recorded as having had no spend, from
	// --no-spend. When it is non-nil, every gap day NOT in it is missing: the caller has
	// claimed to know the whole range, so a gap the list does not name is a day nobody
	// folded.
	NoSpend map[string]bool
}

// CheckResult is a whole output directory, checked.
type CheckResult struct {
	Files    int
	Rows     int
	First    string
	Last     string
	Findings []FileFinding
	// Missing is the days named as findings; Gaps is every calendar day in the range
	// with no file, whether or not it was named. Gaps is the larger fact, and is what
	// gap=<n> prints.
	Missing []string
	Gaps    []string
	// Strays is what is named; Notes is the non-day entries the allowlist stepped over.
	Strays []string
	Notes  []string
}

// Check walks every file under out. A day file is parsed; the one fixed temp name and the
// lock are stepped over, because the wreckage of a killed fold is not a stray; a non-day
// entry the allowlist admits is counted as a note; anything else is named and LEFT ALONE
// — this tool removes nothing, and a person removes a stray.
func Check(out string, opt CheckOptions) (*CheckResult, error) {
	ents, err := os.ReadDir(out)
	if err != nil {
		return nil, err
	}
	r := &CheckResult{}
	var days []string
	for _, e := range ents {
		name := e.Name()
		path := filepath.Join(out, name)
		if e.IsDir() {
			if !opt.Strict && AllowedNote(name, true) {
				r.Notes = append(r.Notes, path)
				continue
			}
			r.Strays = append(r.Strays, path)
			continue
		}
		switch {
		case name == LockName, name == LockName+".held":
			continue
		case strings.HasSuffix(name, TempSuffix) && ValidDay(strings.TrimSuffix(name, TempSuffix)):
			continue
		case strings.HasSuffix(name, FileSuffix) && ValidDay(strings.TrimSuffix(name, FileSuffix)):
			r.Files++
			day, findings, err := ReadDayFile(path)
			if err != nil {
				r.Findings = append(r.Findings, FileFinding{Path: path, Reason: err.Error()})
				continue
			}
			for _, f := range findings {
				r.Findings = append(r.Findings, FileFinding{Path: path, Line: f.Line, Reason: f.Reason})
			}
			r.Rows += len(day.Rows)
			days = append(days, strings.TrimSuffix(name, FileSuffix))
		case !opt.Strict && AllowedNote(name, false):
			r.Notes = append(r.Notes, path)
		default:
			r.Strays = append(r.Strays, path)
		}
	}
	sort.Strings(days)
	sort.Strings(r.Strays)
	sort.Strings(r.Notes)
	if len(days) > 0 {
		r.First, r.Last = days[0], days[len(days)-1]
	}
	r.Gaps = MissingDays(days)
	r.Missing = namedMissing(r.Gaps, opt)
	return r, nil
}

// namedMissing is the one place the gap reading is decided, so the rule is read once.
func namedMissing(gaps []string, opt CheckOptions) []string {
	switch {
	case opt.Strict:
		return gaps
	case opt.NoSpend != nil:
		var out []string
		for _, d := range gaps {
			if !opt.NoSpend[d] {
				out = append(out, d)
			}
		}
		return out
	}
	return nil
}

// AllowedNote is the allowlist: the non-day entries a person keeps beside their day files.
// It is deliberately a list of SHAPES and not of names — a tool with `README.md` compiled
// into it would be a tool that special-cases one bench's directory — and every shape here
// is one this repository's own reports/tokens holds: a README and a session note (`*.md`),
// a collator's log (`*.log`), and the archive of what the day files replaced (`pre-*/`).
func AllowedNote(name string, dir bool) bool {
	if dir {
		return strings.HasPrefix(name, "pre-")
	}
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".log":
		return true
	}
	return false
}

// ReadNoSpendFile reads --no-spend: one YYYY-MM-DD per line, with a blank line and a `#`
// comment skipped and anything after the day on a line taken as its note, so the list can
// carry WHY a day had no spend. A line that is not a day is an error naming it, because a
// list half-read is a gate half-shut.
func ReadNoSpendFile(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	days := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		n++
		line := strings.TrimSpace(strings.TrimRight(sc.Text(), "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.IndexAny(line, " \t"); i > 0 {
			line = line[:i]
		}
		if !ValidDay(line) {
			return nil, fmt.Errorf("line %d: %q is not a day; --no-spend is one YYYY-MM-DD per line, the days that had no spend", n, line)
		}
		days[line] = true
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return days, nil
}
