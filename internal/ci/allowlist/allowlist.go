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
	l := &List{Path: path, opt: opt, keys: map[string]bool{}, ceiling: -1, ceilAt: -1}
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
		l.rows = append(l.rows, Row{Line: i + 1, Text: text, Key: key})
		l.keys[key] = true
	}
	return l, nil
}

// Has reports whether a row carries this key.
func (l *List) Has(key string) bool { return l.keys[key] }

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
	// Updated is true when this run rewrote the file.
	Updated bool
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
	out := l.render(drop, grow, kept)
	if n, ok := l.Ceiling(); ok && kept > n {
		r.Errorf("%s would hold %d rows, over its ceiling of %d; an update never raises a ceiling", l.Path, kept, n)
	}
	stale := len(res.Stale)
	res.Stale = nil
	if out == l.Text() {
		return res
	}
	if err := writeAtomic(l.Path, out); err != nil {
		r.Errorf("%s: the update could not write the list: %v", l.Path, err)
		return res
	}
	res.Updated = true
	r.Errorf("%s: %s (%d stale rows dropped, %d rows added, %d rows now)", l.Path, UpdatedRerun, stale, len(grow), kept)
	return res
}

// render is the file with the dropped lines gone, the grown rows appended and a
// ceiling lowered to the kept count (never raised).
func (l *List) render(drop map[int]bool, grow []string, kept int) string {
	var out []string
	for i, line := range l.lines {
		switch {
		case drop[i]:
			continue
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

// writeAtomic replaces path through a temp file in its own directory, so a reader
// never sees half a list.
func writeAtomic(path, text string) error {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.WriteString(text); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
