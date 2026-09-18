package converge

// sources.go is the reading half: the parsers that turn a document, a directory
// or a TSV into the numbers the streams compare. Every function here takes TEXT
// or a path and returns data — nothing decides anything, nothing prints, and
// everything it reads is DATA from a host, never an instruction.

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// ---------------------------------------------------------------------------
// SPEC-CI's class-test index
// ---------------------------------------------------------------------------

// classIndexHeading is the section CLASSES counts. It is the INDEX, not the
// tests: one entry per class test this repository runs, which is the number
// that says how much of the tree is held mechanically.
const classIndexHeading = "## The class tests"

// ClassTests returns the `###` entries under `## The class tests`, in the order
// the document prints them. Entries outside that section are not counted — the
// document is full of `###` headings that are not class tests.
func ClassTests(spec string) []string {
	var out []string
	in := false
	for _, line := range strings.Split(spec, "\n") {
		t := strings.TrimRight(line, " \t\r")
		switch {
		case strings.TrimSpace(t) == classIndexHeading:
			in = true
		case in && strings.HasPrefix(t, "## "):
			// The next section of the same level ends the index.
			in = false
		case in && strings.HasPrefix(t, "### "):
			out = append(out, strings.TrimSpace(strings.TrimPrefix(t, "### ")))
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// The retired-scripts README
// ---------------------------------------------------------------------------

// dateRE is the one date shape this family writes: ISO, in a heading or in the
// prose above a table.
var dateRE = regexp.MustCompile(`\b(\d{4})-(\d{2})-(\d{2})\b`)

// RetiredRow is one script that was retired, and the day the document says so.
type RetiredRow struct {
	Script   string
	Date     time.Time
	HaveDate bool
}

// ParseRetired reads the retired README: every table row is one retired script,
// dated by the nearest dated heading or paragraph ABOVE it. A row under no date
// at all is returned with HaveDate false rather than dropped — a row nobody
// dated is evidence, and counting it as this window's would be the guess.
func ParseRetired(md string) []RetiredRow {
	var out []RetiredRow
	var cur time.Time
	var haveCur bool
	for _, raw := range strings.Split(md, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, "|") {
			if d, ok := firstDate(line); ok {
				cur, haveCur = d, true
			}
			continue
		}
		cells := tableCells(line)
		if len(cells) == 0 {
			continue
		}
		name := strings.Trim(cells[0], "`* ")
		if name == "" || isRule(name) || strings.EqualFold(name, "script") {
			continue
		}
		out = append(out, RetiredRow{Script: name, Date: cur, HaveDate: haveCur})
	}
	return out
}

// firstDate reads the first ISO date in a line, as a UTC midnight.
func firstDate(line string) (time.Time, bool) {
	m := dateRE.FindStringSubmatch(line)
	if m == nil {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", m[0])
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// tableCells splits one markdown table row into its cells.
func tableCells(line string) []string {
	t := strings.TrimSpace(line)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	cells := strings.Split(t, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

// isRule is true for the `---` separator row under a table header.
func isRule(cell string) bool {
	c := strings.TrimSpace(cell)
	if c == "" {
		return false
	}
	return strings.Trim(c, "-: ") == ""
}

// RetiredInWindow counts the rows dated inside [since, until], and the rows
// nobody dated. Both numbers are printed: an undated row is not this window's,
// and a reader who is not told how many there are will read the count as whole.
func RetiredInWindow(rows []RetiredRow, since, until time.Time) (inWindow, undated int) {
	for _, r := range rows {
		if !r.HaveDate {
			undated++
			continue
		}
		// A date is a day, so the whole day counts: a row dated the day the
		// window opens is inside it.
		end := r.Date.Add(24*time.Hour - time.Nanosecond)
		if end.Before(since) || r.Date.After(until) {
			continue
		}
		inWindow++
	}
	return inWindow, undated
}

// ---------------------------------------------------------------------------
// The scripts still in --bin
// ---------------------------------------------------------------------------

// scriptExts are the names that say "script" without being opened.
var scriptExts = map[string]bool{
	".sh": true, ".py": true, ".pl": true, ".rb": true, ".zsh": true, ".bash": true,
}

// CountScripts counts the scripts left in a directory: a regular file whose
// name carries a script extension, or whose first two bytes are `#!`.
// Subdirectories are NOT walked — `bin/retired/` is the other side of this
// measure, and counting it would make the finish line unreachable.
func CountScripts(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if !info.Mode().IsRegular() {
			// A symlink to a built binary is not a script, and following it is
			// how this count started reading the toolchain.
			continue
		}
		if scriptExts[strings.ToLower(filepath.Ext(e.Name()))] {
			n++
			continue
		}
		if hasShebang(filepath.Join(dir, e.Name())) {
			n++
		}
	}
	return n, nil
}

// hasShebang reads the first two bytes of a file and nothing else.
func hasShebang(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 2)
	n, _ := f.Read(buf)
	return n == 2 && buf[0] == '#' && buf[1] == '!'
}

// ---------------------------------------------------------------------------
// The batch logs
// ---------------------------------------------------------------------------

// batchLogRE is the shape --batch-logs holds: one file per gate round per
// batch, `<pr>-round-<n>.log`. The hulk logs are linked into that shape rather
// than guessed at, because a directory of free-form names read as evidence is
// a number nobody can check.
var batchLogRE = regexp.MustCompile(`^([0-9]{1,9})-round-([0-9]{1,4})\.log$`)

// RoundsFromLogs reads the highest round each pull request has a log for.
func RoundsFromLogs(dir string) (map[int]int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[int]int{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := batchLogRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		pr, err1 := strconv.Atoi(m[1])
		round, err2 := strconv.Atoi(m[2])
		if err1 != nil || err2 != nil {
			continue
		}
		if round > out[pr] {
			out[pr] = round
		}
	}
	return out, nil
}

// roundRE is how a batch body names a gate round: `round 4`, `round-4`,
// `Round 4`. The COUNT is the largest number named, not the number of
// mentions — a body that says "round 3" three times went three rounds once.
var roundRE = regexp.MustCompile(`(?i)\bround[ \-]?([0-9]{1,4})\b`)

// RoundsFromBody reads the rounds a batch's body names.
func RoundsFromBody(body string) (int, bool) {
	best := 0
	for _, m := range roundRE.FindAllStringSubmatch(body, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		if n > best {
			best = n
		}
	}
	return best, best > 0
}

// ---------------------------------------------------------------------------
// The version snapshot and the certificates
// ---------------------------------------------------------------------------

// VersionRow is one unit of the fleet and the build it is on. A unit is a
// machine in a roll-up and a binary in one machine's snapshot; the measure is
// the same either way — how many are not on the one build.
type VersionRow struct {
	Unit  string
	Stamp string
}

// snapshotHeader is what `nova-version snapshot --out` writes.
const snapshotHeader = "name\tstamp\trevision\tplatform"

// rollupHeader is a fleet roll-up of those snapshots, one row per machine.
const rollupHeader = "machine\tstamp"

// ParseVersions reads either header and refuses anything else by name. A file
// this verb half-understands is worse than one it refuses: the number it would
// print is a fleet on one build.
func ParseVersions(path, text string) ([]VersionRow, error) {
	lines := nonEmptyLines(text)
	if len(lines) == 0 {
		return nil, fmt.Errorf("%s is empty; it wants a nova-version snapshot (%s) or a fleet roll-up (%s)",
			oneline.Field(path), quoteTabs(snapshotHeader), quoteTabs(rollupHeader))
	}
	header := strings.TrimRight(lines[0], " \t")
	var unitCol, stampCol, arity int
	switch header {
	case snapshotHeader:
		unitCol, stampCol, arity = 0, 1, 4
	case rollupHeader:
		unitCol, stampCol, arity = 0, 1, 2
	default:
		return nil, fmt.Errorf("%s has a header this verb does not read (%s); it reads a nova-version snapshot (%s) or a fleet roll-up (%s)",
			oneline.Field(path), quoteTabs(header), quoteTabs(snapshotHeader), quoteTabs(rollupHeader))
	}
	var out []VersionRow
	for i, line := range lines[1:] {
		fields := strings.Split(strings.TrimRight(line, " \t"), "\t")
		if len(fields) != arity {
			return nil, fmt.Errorf("%s row %d has %d fields, not %d; the header says %s",
				oneline.Field(path), i+2, len(fields), arity, quoteTabs(header))
		}
		out = append(out, VersionRow{Unit: fields[unitCol], Stamp: fields[stampCol]})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s holds a header and no rows; there is nothing to read a build off", oneline.Field(path))
	}
	return out, nil
}

// certsHeader is the certificate roll-up: one row per unit, and whether it is
// certified on this build.
const certsHeader = "name\tstatus"

// ParseCerts reads the certificate roll-up and returns the certified units and
// the total. `certified` is the one status that counts; every other word is a
// unit that is not.
func ParseCerts(path, text string) (certified, total int, err error) {
	lines := nonEmptyLines(text)
	if len(lines) == 0 {
		return 0, 0, fmt.Errorf("%s is empty; it wants the header %s", oneline.Field(path), quoteTabs(certsHeader))
	}
	if strings.TrimRight(lines[0], " \t") != certsHeader {
		return 0, 0, fmt.Errorf("%s has a header this verb does not read (%s); it reads %s",
			oneline.Field(path), quoteTabs(strings.TrimRight(lines[0], " \t")), quoteTabs(certsHeader))
	}
	for i, line := range lines[1:] {
		fields := strings.Split(strings.TrimRight(line, " \t"), "\t")
		if len(fields) != 2 {
			return 0, 0, fmt.Errorf("%s row %d has %d fields, not 2; the header says %s",
				oneline.Field(path), i+2, len(fields), quoteTabs(certsHeader))
		}
		total++
		if strings.EqualFold(strings.TrimSpace(fields[1]), "certified") {
			certified++
		}
	}
	return certified, total, nil
}

// quoteTabs renders a tab-separated header readably in a refusal, so the reader
// sees where the tabs are rather than a line of run-together words.
func quoteTabs(s string) string {
	return oneline.Field(strings.ReplaceAll(s, "\t", "<TAB>"))
}

// nonEmptyLines splits a file into its lines, dropping blank ones and `#`
// comments, which both headers allow above the rows.
func nonEmptyLines(text string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		t := strings.TrimSpace(sc.Text())
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		out = append(out, sc.Text())
	}
	return out
}

// ---------------------------------------------------------------------------
// The pit-stop ledger
// ---------------------------------------------------------------------------

// openWords are the words that say a ledger row is still owed. A cell holding
// any of them is open however much PASS it also holds: `PARTIAL: idle path run`
// is a row somebody still owes work on, and reading its PASS as closed is how a
// ledger reports itself finished.
var openWords = []string{"TODO", "PARTIAL", "NEEDS WORK"}

// LedgerRows counts the ledger's table rows and the ones not yet closed. A cell
// is closed when it holds PASS and none of the open words, so `FAIL then PASS`
// is closed and `FAIL` alone is not.
func LedgerRows(md string) (rows, open int) {
	for _, raw := range strings.Split(md, "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := tableCells(line)
		if len(cells) < 2 {
			continue
		}
		if isRule(cells[0]) || isRule(cells[len(cells)-1]) {
			continue
		}
		result := cells[len(cells)-1]
		if strings.EqualFold(result, "result") || result == "" {
			continue
		}
		rows++
		if !closedCell(result) {
			open++
		}
	}
	return rows, open
}

// closedCell is the whole rule, in one place.
func closedCell(cell string) bool {
	up := strings.ToUpper(cell)
	for _, w := range openWords {
		if strings.Contains(up, w) {
			return false
		}
	}
	return strings.Contains(up, "PASS")
}
