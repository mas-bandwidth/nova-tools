// Package allowlist is the one reader and the one writer of the exception lists the
// class tests in internal/ci keep under internal/ci/testdata (nova-tools#4339).
//
// A list is a text file: `#` comment lines, blank lines, and rows. Each row has a key
// (by default its first field) and whatever reason follows it for a reader. A class
// test Loads its list, walks the tree, and hands Check the set of keys it measured:
// every offender it found, listed or not. Check returns the rows nobody needs any more
// (stale) and the keys the list does not hold (unlisted); the test prints both with
// its own remedy.
//
// Under NOVA_CI_UPDATE=1, Check rewrites the file to the measured set instead: the
// stale rows go, every comment and every kept row stays byte for byte, and the test
// fails once with "updated, rerun". A ceiling-only list -- one whose count only falls,
// which is every list in internal/ci today -- refuses to grow under the update and says
// so for each key it will not add: an update never writes a reason on anyone's behalf.
// A `# ceiling: N` line caps a list's row count; the update lowers N to the new count
// and never raises it.
//
// A counted list carries a site count after the key (`key 3 reason`): the number of
// sites the row covers. A row that covers sites alone is not a shrink-only list, because
// a new site under an existing key reads as already listed; the count closes that. The
// measured count of a key must equal its row's count, a higher one is a new site (red),
// a lower one is a count to lower (red, and NOVA_CI_UPDATE=1 lowers it), and an update
// never raises one.
//
// No script edits a list file: a removal regenerates every list with one env var.
package allowlist

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

// UpdateEnv is the variable that turns Check into a rewrite. Only the value "1" does.
const UpdateEnv = "NOVA_CI_UPDATE"

// Updating reports whether this run rewrites the lists (NOVA_CI_UPDATE=1).
func Updating() bool { return updatingFrom(os.Getenv) }

func updatingFrom(getenv func(string) string) bool { return getenv(UpdateEnv) == "1" }

// UpdatedRerun is the sentence the one failure of an update run carries.
const UpdatedRerun = "updated, rerun"

// ceilingPrefix opens the one comment line Check reads: the row count's cap.
const ceilingPrefix = "# ceiling:"

// Reporter is the part of testing.TB Check writes to. It is an interface here so the
// package does not link the testing package into a binary that imports internal/ci.
type Reporter interface {
	Helper()
	Errorf(format string, args ...any)
}

// Options says how a list's rows are keyed and whether it may grow.
type Options struct {
	// Key derives a row's key from its trimmed text. Nil is FirstField.
	Key func(row string) string
	// Ceiling marks a list whose count only falls: an update drops stale rows and
	// refuses to add a row for an unlisted key.
	Ceiling bool
	// NewRow is the text an update appends for an unlisted key on a list that may
	// grow. Nil appends the key alone. Unused on a ceiling list.
	NewRow func(key string) string
	// MissingIsEmpty loads a missing file as an empty list instead of an error: a
	// tree with nothing parked in it is the goal.
	MissingIsEmpty bool
	// Counted marks a list whose rows carry a site count as the second field
	// (`key N reason`, N a positive integer); use CheckCounted with it.
	Counted bool
	// PackageKeys makes LoadPackages interpret a counted key as
	// `<repo-relative-package>:<kind>` instead of a file-qualified key.
	// The row key itself remains unchanged; this only selects its shard.
	PackageKeys bool
}

// FirstField is the default key: the row's first whitespace-separated field.
func FirstField(row string) string {
	if f := strings.Fields(row); len(f) > 0 {
		return f[0]
	}
	return ""
}

// Fields returns a key of the row's first n fields joined by one space, for a list
// whose identity is more than one field (`file:line kind`, `file spell`).
func Fields(n int) func(string) string {
	return func(row string) string {
		f := strings.Fields(row)
		if len(f) > n {
			f = f[:n]
		}
		return strings.Join(f, " ")
	}
}

// WholeRow keys a row by its whole trimmed text.
func WholeRow(row string) string { return row }

// Row is one row of a list.
type Row struct {
	Line int    // 1-based line in the file
	Text string // the row, trimmed
	Key  string
}

// List is a loaded allowlist.
type List struct {
	Path    string
	counts  map[string]int // key -> the row's site count (Counted lists)
	opt     Options
	lines   []string // the file, split on "\n"; the last element is "" when it ends in one
	rows    []Row
	keys    map[string]bool
	ceiling int // the `# ceiling: N` value; -1 when the list has none
	ceilAt  int // 0-based index of the ceiling line in lines; -1 when none
}

// Load reads the list at path.
func Load(path string, opt Options) (*List, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if opt.MissingIsEmpty && errors.Is(err, fs.ErrNotExist) {
			return Parse(path, "", opt)
		}
		return nil, err
	}
	return Parse(path, string(raw), opt)
}

// Parse reads a list from its text; path is where Check writes it back.
func Parse(path, raw string, opt Options) (*List, error) {
	if opt.Key == nil {
		opt.Key = FirstField
	}
	l := &List{Path: path, opt: opt, keys: map[string]bool{}, counts: map[string]int{}, ceiling: -1, ceilAt: -1}
	if raw != "" {
		l.lines = strings.Split(raw, "\n")
	}
	for i, line := range l.lines {
		text := strings.TrimSpace(line)
		if strings.HasPrefix(text, ceilingPrefix) {
			if l.ceilAt >= 0 {
				return nil, fmt.Errorf("%s:%d: a second %q line; a list has one ceiling", path, i+1, ceilingPrefix)
			}
			n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(text, ceilingPrefix)))
			if err != nil || n < 0 {
				return nil, fmt.Errorf("%s:%d: %q is not `%s <rows>`", path, i+1, text, ceilingPrefix)
			}
			l.ceiling, l.ceilAt = n, i
			continue
		}
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key := opt.Key(text)
		if opt.Counted {
			f := strings.Fields(text)
			n := 0
			if len(f) > 1 {
				n, _ = strconv.Atoi(f[1])
			}
			if n < 1 {
				return nil, fmt.Errorf("%s:%d: %q is not `<key> <sites> <reason>`; the second field is the row's site count, a positive integer", path, i+1, text)
			}
			l.counts[key] = n
		}
		l.rows = append(l.rows, Row{Line: i + 1, Text: text, Key: key})
		l.keys[key] = true
	}
	return l, nil
}

// Has reports whether a row carries this key.
func (l *List) Has(key string) bool { return l.keys[key] }

// Count is a counted row's site count, 0 for a key no row carries.
func (l *List) Count(key string) int { return l.counts[key] }

// Rows returns the rows in file order.
func (l *List) Rows() []Row { return append([]Row(nil), l.rows...) }

// Text is the file as it was loaded.
func (l *List) Text() string { return strings.Join(l.lines, "\n") }

// Len is the number of rows.
func (l *List) Len() int { return len(l.rows) }

// Ceiling returns the `# ceiling: N` cap, and false when the list carries none.
func (l *List) Ceiling() (int, bool) { return l.ceiling, l.ceilAt >= 0 }

// Result is what Check found.
type Result struct {
	// Stale are the rows whose key was not measured. Each is a red run; an update
	// drops them and returns none.
	Stale []Row
	// Unlisted are the measured keys no row carries, sorted. Each is a red run; an
	// update on a list that may grow adds them and returns none, and a ceiling list
	// keeps them (it refuses to grow).
	Unlisted []string
	// Over are the keys of a counted list measured at more sites than their row
	// allows: a new site under an existing row. Each is a red run, and an update
	// refuses to raise the count.
	Over []CountRow
	// Lowered are the keys of a counted list measured at fewer sites than their
	// row says. Each is a red run outside an update; an update lowers the count and
	// returns none.
	Lowered []CountRow
	// Updated is true when this run rewrote the file.
	Updated bool
}

// CountRow is one counted key: the sites its row lists and the sites measured.
type CountRow struct {
	Key      string
	Listed   int
	Measured int
}

// IsStale reports whether a row with this key is stale.
func (r Result) IsStale(key string) bool {
	for _, row := range r.Stale {
		if row.Key == key {
			return true
		}
	}
	return false
}

// Check compares the measured keys with the list. Outside an update it only reports:
// a list over its ceiling is refused here, and the stale rows and unlisted keys come
// back for the caller to print with its own remedy. Under NOVA_CI_UPDATE=1 it
// rewrites the file to the measured set and fails once with "updated, rerun"; a
// ceiling list refuses each key it would have to add, one line per key.
func Check(r Reporter, l *List, measured map[string]bool) Result {
	r.Helper()
	return CheckMode(r, l, measured, Updating())
}

// CheckMode is Check with the update mode given, not read from the environment, so a
// test of this package does not depend on how its own run was started.
func CheckMode(r Reporter, l *List, measured map[string]bool, update bool) Result {
	r.Helper()
	var res Result
	for _, row := range l.rows {
		if !measured[row.Key] {
			res.Stale = append(res.Stale, row)
		}
	}
	for k := range measured {
		if !l.keys[k] {
			res.Unlisted = append(res.Unlisted, k)
		}
	}
	sort.Strings(res.Unlisted)

	if !update {
		if n, ok := l.Ceiling(); ok && len(l.rows) > n {
			r.Errorf("%s has %d rows, over its ceiling of %d; the list only shrinks", l.Path, len(l.rows), n)
		}
		return res
	}

	var grow []string
	if l.opt.Ceiling {
		for _, k := range res.Unlisted {
			r.Errorf("%s is ceiling-only and refuses to grow under %s=1: %s is not listed, and the update adds no row for it (fix the finding; the list only shrinks)", l.Path, UpdateEnv, k)
		}
	} else {
		grow = res.Unlisted
		res.Unlisted = nil
	}
	drop := map[int]bool{}
	for _, row := range res.Stale {
		drop[row.Line-1] = true
	}
	kept := len(l.rows) - len(res.Stale) + len(grow)
	out := l.render(drop, nil, grow, kept)
	if n, ok := l.Ceiling(); ok && kept > n {
		r.Errorf("%s would hold %d rows, over its ceiling of %d; an update never raises a ceiling", l.Path, kept, n)
	}
	stale := len(res.Stale)
	res.Stale = nil
	if out == l.Text() {
		return res
	}
	if err := WriteAtomic(l.Path, out); err != nil {
		r.Errorf("%s: the update could not write the list: %v", l.Path, err)
		return res
	}
	res.Updated = true
	r.Errorf("%s: %s (%d stale rows dropped, %d rows added, %d rows now)", l.Path, UpdatedRerun, stale, len(grow), kept)
	return res
}

// CheckCounted is Check for a counted list: measured is the number of sites found
// per key. See CheckCountedMode.
func CheckCounted(r Reporter, l *List, measured map[string]int) Result {
	r.Helper()
	return CheckCountedMode(r, l, measured, Updating())
}

// CheckCountedMode compares the measured site counts with a counted list. Stale rows
// (a key measured at none) and Unlisted keys are as in CheckMode; Over holds the keys
// measured above their row's count and Lowered those below it. Outside an update it
// only reports. Under an update it drops the stale rows, lowers the lowered counts
// and refuses, one line per key, to add a row or raise a count: the list only shrinks.
func CheckCountedMode(r Reporter, l *List, measured map[string]int, update bool) Result {
	r.Helper()
	if !l.opt.Counted {
		r.Errorf("%s: CheckCounted needs a list loaded with Options.Counted", l.Path)
		return Result{}
	}
	keys := make(map[string]bool, len(measured))
	for k, n := range measured {
		if n > 0 {
			keys[k] = true
		}
	}
	res := CheckMode(r, l, keys, false)
	for _, row := range l.rows {
		listed, n := l.counts[row.Key], measured[row.Key]
		switch {
		case n == 0:
		case n > listed:
			res.Over = append(res.Over, CountRow{Key: row.Key, Listed: listed, Measured: n})
		case n < listed:
			res.Lowered = append(res.Lowered, CountRow{Key: row.Key, Listed: listed, Measured: n})
		}
	}
	if !update {
		return res
	}
	for _, k := range res.Unlisted {
		r.Errorf("%s is ceiling-only and refuses to grow under %s=1: %s is not listed, and the update adds no row for it (fix the finding; the list only shrinks)", l.Path, UpdateEnv, k)
	}
	for _, c := range res.Over {
		r.Errorf("%s refuses to raise a count under %s=1: %s is listed at %d sites and measured at %d (fix the new site; the list only shrinks)", l.Path, UpdateEnv, c.Key, c.Listed, c.Measured)
	}
	drop := map[int]bool{}
	for _, row := range res.Stale {
		drop[row.Line-1] = true
	}
	lower := map[int]int{}
	for _, row := range l.rows {
		for _, c := range res.Lowered {
			if c.Key == row.Key {
				lower[row.Line-1] = c.Measured
			}
		}
	}
	kept := len(l.rows) - len(res.Stale)
	out := l.render(drop, lower, nil, kept)
	if n, ok := l.Ceiling(); ok && kept > n {
		r.Errorf("%s would hold %d rows, over its ceiling of %d; an update never raises a ceiling", l.Path, kept, n)
	}
	stale, lowered := len(res.Stale), len(res.Lowered)
	res.Stale, res.Lowered = nil, nil
	if out == l.Text() {
		return res
	}
	if err := WriteAtomic(l.Path, out); err != nil {
		r.Errorf("%s: the update could not write the list: %v", l.Path, err)
		return res
	}
	res.Updated = true
	r.Errorf("%s: %s (%d stale rows dropped, %d counts lowered, %d rows now)", l.Path, UpdatedRerun, stale, lowered, kept)
	return res
}

// withCount replaces only the second field in a validated counted row. Keep
// every separator and the trailing reason exactly as the author wrote them.
func withCount(line string, n int) string {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return line // Parse refuses a counted row without a positive count.
	}
	afterKey := strings.Index(line, fields[0]) + len(fields[0])
	start := afterKey + strings.Index(line[afterKey:], fields[1])
	return line[:start] + strconv.Itoa(n) + line[start+len(fields[1]):]
}

// render is the file with the dropped lines gone, the grown rows appended and a
// ceiling lowered to the kept count (never raised).
func (l *List) render(drop map[int]bool, lower map[int]int, grow []string, kept int) string {
	var out []string
	for i, line := range l.lines {
		switch {
		case drop[i]:
			continue
		case lower[i] > 0:
			out = append(out, withCount(line, lower[i]))
		case i == l.ceilAt && kept < l.ceiling:
			out = append(out, fmt.Sprintf("%s %d", ceilingPrefix, kept))
		default:
			out = append(out, line)
		}
	}
	if len(grow) == 0 {
		return strings.Join(out, "\n")
	}
	trailing := len(out) > 0 && out[len(out)-1] == ""
	if trailing {
		out = out[:len(out)-1]
	}
	for _, k := range grow {
		row := k
		if l.opt.NewRow != nil {
			row = l.opt.NewRow(k)
		}
		out = append(out, row)
	}
	return strings.Join(out, "\n") + "\n"
}

// WriteAtomic replaces path through a temp file in its own directory, so a reader
// never sees half a list.
func WriteAtomic(path, text string) error {
	return atomicfile.Write(filepath.Clean(path), []byte(text), 0o644, atomicfile.ExactMode())
}
