package tokens

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// `check` is the GATE. It verifies what rule 13 says and nothing else: it does not ask
// whether a day's numbers are plausible, or whether a source was declared that day. A
// missing day is NAMED and never filled.

// FileFinding is one thing wrong with one file.
type FileFinding struct {
	Path   string
	Line   int
	Reason string
}

// CheckResult is a whole output directory, checked.
type CheckResult struct {
	Files    int
	Rows     int
	First    string
	Last     string
	Findings []FileFinding
	Missing  []string
	Strays   []string
}

// Check walks every file under out. A day file is parsed; the one fixed temp name and the
// lock are stepped over, because the wreckage of a killed fold is not a stray; anything
// else is named and LEFT ALONE — this tool removes nothing, and a person removes a stray.
func Check(out string) (*CheckResult, error) {
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
		default:
			r.Strays = append(r.Strays, path)
		}
	}
	sort.Strings(days)
	sort.Strings(r.Strays)
	if len(days) > 0 {
		r.First, r.Last = days[0], days[len(days)-1]
	}
	r.Missing = MissingDays(days)
	return r, nil
}
