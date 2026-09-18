package bus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

// RowsDir is where a verdict ROW is written: one TSV per lane, at the bus root, beside the
// lanes rather than inside one.
//
// WHY A ROW AT ALL, when a receipt note has said "heard" since the first day. A note says
// heard and nothing else; what a line DECIDED -- approve, hold, answered, abstain -- was
// prose inside a note, so a coordinator counting verdicts opened notes to count them, and
// a model read the bus to find out state. A row is four fields a machine reads without
// opening anything, and the note stays exactly as it was: the row is additive, and a bus
// with no rows on it behaves as it always did.
//
// It is at the ROOT and not in a lane because a reader of verdicts wants every lane's, and
// one directory of one file per lane is a read of n files rather than a walk of the bus.
// ReadBus does not see it: only `from-` directories are lanes, so nothing this adds can be
// mistaken for a note, and `check` neither passes nor fails on it.
const RowsDir = "receipts"

// RowsPath is the repo-relative path of one lane's rows file, by the lane's slug.
func RowsPath(slug string) string { return RowsDir + "/" + slug + ".tsv" }

// VerdictRow is one recorded decision: when, who, which note, what they said.
type VerdictRow struct {
	Stamp   string // RFC 3339 UTC, as written
	As      string // the roster name of the line that decided
	Note    string // the note's id, or its path when it has no id
	Verdict string // one word, upper or lower case, no whitespace

	// File and Line say where the row was read from, so a malformed one can be named.
	File string
	Line int
}

// VerdictMax is how long a verdict word may be. A verdict is a WORD -- APPROVE, HOLD,
// ANSWERED, ABSTAIN -- and a sentence in the column is prose again, in a file whose whole
// point is that it is not prose.
const VerdictMax = 32

// ValidVerdict holds the one shape the fourth column may have: one token of letters,
// digits, hyphen or underscore, at most VerdictMax of them.
//
// A tab or a newline in it would AUTHOR A COLUMN or a row, which is the injection a TSV
// has; a space would make a scanner read two fields where one was written. So this is a
// refusal and never a quiet rewrite: a caller who asked to record one word and had it
// rewritten has been lied to about what is on the record.
func ValidVerdict(v string) error {
	if v == "" {
		return errors.New("--verdict: empty")
	}
	if len(v) > VerdictMax {
		return fmt.Errorf("--verdict %q: longer than %d characters; a verdict is one word", truncate(v, VerdictMax), VerdictMax)
	}
	for _, r := range v {
		if unicode.IsLetter(r) && r < 128 || unicode.IsDigit(r) && r < 128 || r == '-' || r == '_' {
			continue
		}
		return fmt.Errorf("--verdict %q is not one word: a verdict is letters, digits, - and _, because the row it is written to is a TSV", truncate(v, VerdictMax))
	}
	return nil
}

// AppendVerdictRows appends one row per target to the lane's rows file, creating it and the
// directory if they are not there. The file is append-only and one per lane, so two lines
// recording verdicts in the same second touch different files and cannot conflict -- the
// same rule RECEIPTS keeps, for the same reason.
//
// A row is written EVERY time a verdict is given, including for a note this lane has
// already receipted. The receipt note is written once because a note is sent once; a
// verdict is a fact about a decision, and a line that decides again has decided again.
func AppendVerdictRows(root, path string, rows []VerdictRow) error {
	if len(rows) == 0 {
		return nil
	}
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := insideRoot(root, full); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	f, err := openLaneFile(root, full, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(strings.Join([]string{r.Stamp, r.As, r.Note, r.Verdict}, "\t") + "\n")
	}
	if _, err := f.WriteString(b.String()); err != nil {
		return err
	}
	return f.Close()
}

// ReadVerdictRows reads every lane's rows, oldest first. A directory that is not there is
// no rows and not an error: a bus nobody has recorded a verdict on is a bus with no rows.
//
// A line that is not four fields is SKIPPED rather than guessed at, and a caller that wants
// to know reads Line and File on what it did get. The file is hand-editable by design --
// it is a TSV in a git repository -- so a malformed line is a thing that happens, and one
// bad line has never been a reason to lose the rest.
func ReadVerdictRows(root string) ([]VerdictRow, error) {
	dir := filepath.Join(root, RowsDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []VerdictRow
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".tsv") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		for i, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
			if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
				continue
			}
			fields := strings.Split(line, "\t")
			if len(fields) != 4 {
				continue
			}
			out = append(out, VerdictRow{
				Stamp: fields[0], As: fields[1], Note: fields[2], Verdict: fields[3],
				File: RowsDir + "/" + e.Name(), Line: i + 1,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Stamp < out[j].Stamp })
	return out, nil
}

// ParseWindow reads a `close --older-than` window: a Go duration, or a whole number of days
// or weeks, which Go's own parser has no unit for and a daily job is written in.
//
// WHY IT EXISTS. `close --before` takes an INSTANT, which is right for a hand drawing a
// line once and wrong for machinery running every day: a crontab computing yesterday's
// instant in shell is a date command per platform, and the one on a Mac is not the one on
// Linux. A window is the same decision stated as a duration, and the tool does the
// arithmetic against its own clock.
func ParseWindow(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty")
	}
	if rest, ok := strings.CutSuffix(s, "d"); ok {
		return wholeUnits(s, rest, 24*time.Hour)
	}
	if rest, ok := strings.CutSuffix(s, "w"); ok {
		return wholeUnits(s, rest, 7*24*time.Hour)
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a window: it is a Go duration such as 36h, or a whole number of days or weeks such as 3d or 2w", truncate(s, 40))
	}
	if d <= 0 {
		return 0, fmt.Errorf("%q is not a window: a window is a length of time, and this one is zero or backwards", truncate(s, 40))
	}
	return d, nil
}

func wholeUnits(whole, digits string, unit time.Duration) (time.Duration, error) {
	n := 0
	if digits == "" {
		return 0, fmt.Errorf("%q is not a window: it names a unit and no number", truncate(whole, 40))
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%q is not a window: a window in days or weeks is a whole number of them, such as 3d or 2w", truncate(whole, 40))
		}
		n = n*10 + int(r-'0')
		if n > 4000 {
			return 0, fmt.Errorf("%q is not a window: over ten years of it", truncate(whole, 40))
		}
	}
	if n == 0 {
		return 0, fmt.Errorf("%q is not a window: a window is a length of time, and this one is zero", truncate(whole, 40))
	}
	return time.Duration(n) * unit, nil
}
