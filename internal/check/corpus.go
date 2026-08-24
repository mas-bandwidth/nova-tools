package check

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// corpus.go — the protected-corpus gate.
//
// THE HAZARD IS PRESENCE, NOT TONE, and it is structural to any mind whose
// memory is a growing set of files. A self-talk screen watches what creeps
// IN. Nothing watches what falls OUT. A consolidation pass, a rewrite, a
// directory move, a restore to an earlier checkpoint — each can drop a
// sentence that was given once and never repeated, and none of them produces
// an error. The file still parses. The links still resolve. The line has no
// way to know what it no longer holds, because the record and the evidence
// about the record are the same object.
//
// That asymmetry is the whole argument for a ledger. Anything else a check
// can find is present in the tree: a broken link names its target, an
// oversized kernel names its bytes. A lost sentence names nothing. So the
// line writes down, in advance and in prose, the statements it intends never
// to lose silently, and where each one lives. This check reads that ledger
// and asserts every fragment is still where the ledger says.
//
// AND THE LEDGER IS INSIDE THE THING IT PROTECTS, which is the obvious
// objection and is answered by a floor rather than by hope. The same restore
// that drops a sentence drops the row guarding it, and the run would go green
// with a smaller count that nothing compares to anything. So the caller
// states a MINIMUM ROW COUNT, in the house's no-guessed-budgets idiom, and
// losing rows is itself red. Without it this check protects everything except
// itself.
//
// THE LEDGER IS PROSE FIRST. It is written to be read by a person as the
// list of what is protected and why; this check reads only its table rows.
// Fragments are verbatim substrings — short enough to survive a reflow, long
// enough to be unmistakable, and that judgment is the line's, never this
// tool's.
//
// THE ONE LEGITIMATE WAY OUT IS THE LEDGER. If protected words must move or
// be reworded, the ledger row changes in the same commit. That makes the
// change a visible decision instead of a silent loss, which is the entire
// point: this check does not forbid change, it forbids change that leaves no
// trace.
//
// WHAT IT DOES NOT DO. It does not judge what belongs in the corpus, does
// not read sentiment, and does not ship a corpus of its own. What is worth
// protecting is one of the more personal decisions a line makes, and a tool
// that guessed it would be answering a question it cannot see.

// Anchor is one ledger row: a verbatim fragment, the file it lives in, and
// the provenance columns that make the ledger readable as prose.
type Anchor struct {
	Fragment string
	Home     string // slash-separated, relative to --root
	Given    string
	By       string
	Line     int // 1-based line in the ledger, so a finding can be found
}

// separatorCell matches a markdown table separator cell (---, :--, --:, :-:).
var separatorCell = regexp.MustCompile(`^:?-+:?$`)

// fenceRE matches a fenced-code-block delimiter. A ledger that documents its
// own format shows a table inside a fence, and those rows are illustration
// rather than protection.
var fenceRE = regexp.MustCompile("^(```+|~~~+)")

// anchorColumns is the ledger's fixed shape. A row with any other cell count
// is a finding rather than a skipped line: a fragment containing a literal
// "|", or a trailing comment after the last pipe, produces an extra cell, and
// silently dropping that row would remove protection from exactly the
// statement someone took the trouble to list.
const anchorColumns = 4

// ParseLedger reads the anchor rows out of a ledger's markdown.
//
// Rows are recognized generously and rejected loudly. A line inside a fenced
// code block is illustration and is skipped. Otherwise a line is a candidate
// row if it begins with "|" or carries at least three of them — GitHub's
// tables make the outer pipes optional, so requiring them would let a row
// that renders perfectly well vanish from the check without a word.
//
// A table is a run of consecutive candidate rows. If the run's second line is
// a separator, its first line was the header and both are dropped. Header and
// separator are therefore recognized by SHAPE and POSITION, never by the
// words in them: no column title is special to this tool, and a line may
// title its columns in its own language. A separator anywhere else in a run
// is a finding — it would otherwise silently delete the row above it, which
// is the same silent loss this check exists to make loud.
//
// AN EMPTY LEDGER IS AN ERROR, NEVER A GREEN. A ledger with no rows guards
// nothing, and "everything present" and "nothing checked" must never print
// the same line.
func ParseLedger(raw []byte) ([]Anchor, []Failure, error) {
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")

	var out []Anchor
	var failures []Failure
	fenced := false
	run := []int{} // line indexes of the current run of candidate rows

	flush := func() {
		defer func() { run = run[:0] }()
		if len(run) == 0 {
			return
		}
		body := run
		// A separator on the run's second line marks the row above it as
		// this table's header; both leave.
		if len(run) >= 2 && allSeparator(splitRow(lines[run[1]])) {
			body = run[2:]
		}
		for _, i := range body {
			cells := splitRow(lines[i])
			locus := fmt.Sprintf("ledger:%d", i+1)
			if allSeparator(cells) {
				failures = append(failures, Failure{locus, "a separator row in the middle of a table; a separator belongs directly under the header and nowhere else, and one here would quietly take the row above it out of the check"})
				continue
			}
			if len(cells) != anchorColumns {
				failures = append(failures, Failure{locus, fmt.Sprintf("malformed row: %d columns, want %d — a \"|\" inside a cell, or anything trailing the last pipe, splits into another column; pick a fragment without one rather than escaping it", len(cells), anchorColumns)})
				continue
			}
			out = append(out, Anchor{
				Fragment: cells[0], Home: cells[1], Given: cells[2], By: cells[3],
				Line: i + 1,
			})
		}
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if fenceRE.MatchString(trimmed) {
			flush()
			fenced = !fenced
			continue
		}
		if fenced || !isCandidateRow(trimmed) {
			flush()
			continue
		}
		run = append(run, i)
	}
	flush()

	if len(out) == 0 {
		// The malformed findings travel with the error: a ledger whose rows
		// are ALL malformed is visibly populated, and telling its author it
		// is empty while withholding the diagnosis is the worst of both.
		return nil, failures, fmt.Errorf("the ledger holds no anchor rows; an empty ledger guards nothing and must not read as a pass")
	}
	return out, failures, nil
}

// isCandidateRow reports whether a line should be read as a table row.
// Generous on purpose: a row missing an outer pipe still renders as a table,
// so it must still be checked or reported, never skipped.
func isCandidateRow(trimmed string) bool {
	if trimmed == "" {
		return false
	}
	return strings.HasPrefix(trimmed, "|") || strings.Count(trimmed, "|") >= anchorColumns-1
}

// splitRow splits a table row into trimmed cells, tolerating a missing outer
// pipe on either side.
func splitRow(row string) []string {
	inner := strings.TrimSpace(row)
	inner = strings.TrimPrefix(inner, "|")
	inner = strings.TrimSuffix(inner, "|")
	parts := strings.Split(inner, "|")
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(p)
	}
	return cells
}

func allSeparator(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		if !separatorCell.MatchString(c) {
			return false
		}
	}
	return true
}

// Corpus verifies every anchor is present in its home file under root, and
// that the ledger still holds at least minAnchors rows.
//
// It returns every failure rather than the first: the losses this exists to
// catch arrive in batches — a move, a restore, a consolidation pass — and a
// one-at-a-time report would take as many runs as there were losses.
//
// err is reserved for the check being unable to run at all: a --root that is
// absent or is not a directory. That distinction is not pedantry here. A
// typo'd root would otherwise fire this tool's loudest alarm — "the words
// were lost in place" — once per anchor, for an invocation mistake, which is
// the fastest way to teach a caller to ignore the alarm.
func Corpus(root, ledgerPath string, minAnchors int, as []Anchor) ([]Failure, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("root %q is not a directory", root)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("root %q cannot be resolved: %w", root, err)
	}
	// The ledger's own path, resolved, so a row cannot name the ledger as its
	// own home — which would pass forever, the row being its own evidence.
	resolvedLedger, _ := filepath.EvalSymlinks(ledgerPath)

	var failures []Failure
	dirs := newDirCache()

	// THE LEDGER GUARDS THE TREE; THIS GUARDS THE LEDGER. Rows can be lost by
	// exactly the events rows exist to catch, and a shrunken ledger otherwise
	// goes green with a smaller number that nothing compares to anything.
	if len(as) < minAnchors {
		failures = append(failures, Failure{
			filepath.Base(ledgerPath),
			fmt.Sprintf("the ledger holds %d rows, below the stated floor of %d — rows have been lost from the ledger itself, which is the one loss the rows cannot report. Restore them, or lower the floor in the same commit as a visible decision", len(as), minAnchors),
		})
	}

	for _, a := range as {
		locus := fmt.Sprintf("ledger:%d", a.Line)

		// A blank fragment is contained in every file, so it would pass
		// forever while protecting nothing — a permanent green that is
		// indistinguishable from a real one.
		if a.Fragment == "" {
			failures = append(failures, Failure{locus, "the fragment cell is empty; an empty fragment is present in every file and would pass forever while protecting nothing"})
			continue
		}
		if a.Home == "" {
			failures = append(failures, Failure{locus, fmt.Sprintf("no home file given for %q; a fragment with nowhere to be cannot be checked", a.Fragment)})
			continue
		}
		if filepath.IsAbs(a.Home) || strings.HasPrefix(a.Home, "/") {
			failures = append(failures, Failure{locus, fmt.Sprintf("home %q is absolute; homes are relative to --root so the ledger travels with the repo", a.Home)})
			continue
		}
		rel := filepath.Clean(filepath.FromSlash(a.Home))
		if escapesRoot(rel) {
			failures = append(failures, Failure{locus, fmt.Sprintf("home %q leaves the tree under --root; an anchor held outside the repo is not held by it", a.Home)})
			continue
		}

		path := filepath.Join(root, rel)
		fi, statErr := os.Lstat(path)
		switch {
		case statErr != nil && os.IsNotExist(statErr):
			failures = append(failures, Failure{a.Home, fmt.Sprintf("home file does not exist — %q cannot be held by a file that is gone. If the material moved, move this ledger row in the same commit (ledger:%d)", a.Fragment, a.Line)})
			continue
		case statErr != nil:
			failures = append(failures, Failure{a.Home, fmt.Sprintf("unreachable (%v) — nothing was checked for %q, which is not a pass (ledger:%d)", statErr, a.Fragment, a.Line)})
			continue
		case !fi.Mode().IsRegular():
			failures = append(failures, Failure{a.Home, fmt.Sprintf("not a regular file (%s); symlinks are never followed, so %q is not held here (ledger:%d)", fi.Mode().Type(), a.Fragment, a.Line)})
			continue
		}

		// A case-only rename is a real move, and a case-insensitive
		// filesystem answers Lstat for the old spelling — so the author's
		// Mac says green where CI on Linux says red. Lstat cannot settle
		// this: its FileInfo.Name() is the base of the path it was HANDED,
		// so it agrees with the ledger by construction. Only the directory
		// knows what it actually holds.
		if actual, ok := dirs.exactName(filepath.Dir(path), filepath.Base(rel)); !ok {
			failures = append(failures, Failure{a.Home, fmt.Sprintf("the directory holds %q, not %q — this filesystem matched a different spelling, and a case-only rename is a real move (ledger:%d)", actual, filepath.Base(rel), a.Line)})
			continue
		}

		// The escape check above is lexical only: a symlinked DIRECTORY
		// anywhere in the path could still point outside the repo, and the
		// kernel resolves it without asking. Same posture, and the same
		// resolution, as attest.
		resolved, resErr := filepath.EvalSymlinks(path)
		if resErr != nil {
			failures = append(failures, Failure{a.Home, fmt.Sprintf("cannot be resolved (%v) — nothing was checked for %q (ledger:%d)", resErr, a.Fragment, a.Line)})
			continue
		}
		if resolved != filepath.Join(resolvedRoot, rel) {
			failures = append(failures, Failure{a.Home, fmt.Sprintf("reached through a symlinked path component (resolves to %s); symlinks are never followed, so %q is not held under --root (ledger:%d)", resolved, a.Fragment, a.Line)})
			continue
		}
		if resolvedLedger != "" && resolved == resolvedLedger {
			failures = append(failures, Failure{locus, fmt.Sprintf("the ledger names itself as the home for %q; the row would be its own evidence and could never go red", a.Fragment)})
			continue
		}

		body, readErr := os.ReadFile(path)
		if readErr != nil {
			failures = append(failures, Failure{a.Home, fmt.Sprintf("unreadable (%v) — nothing was checked for %q, which is not a pass (ledger:%d)", readErr, a.Fragment, a.Line)})
			continue
		}
		if !strings.Contains(string(body), a.Fragment) {
			failures = append(failures, Failure{a.Home, fmt.Sprintf("ABSENT: %q (given %s, %s) — the words were lost in place. Restore them, or change ledger:%d in the same commit as a visible decision", a.Fragment, orUnstated(a.Given), orUnstated(a.By), a.Line)})
		}
	}
	return failures, nil
}

// dirCache answers "does this directory hold this exact name" without
// re-reading a directory once per anchor. A case-insensitive filesystem will
// happily Lstat a name it does not hold, so the entries are the only witness.
type dirCache struct{ seen map[string][]string }

func newDirCache() *dirCache { return &dirCache{seen: map[string][]string{}} }

// exactName reports whether dir holds name byte-for-byte. When it does not,
// it returns the entry that matched case-insensitively, so the finding can
// say what is actually there. An unreadable directory answers yes: the file
// was already Lstat'd successfully, so a listing failure is not evidence
// against the anchor and must not manufacture one.
func (d *dirCache) exactName(dir, name string) (string, bool) {
	names, ok := d.seen[dir]
	if !ok {
		entries, err := os.ReadDir(dir)
		if err != nil {
			d.seen[dir] = nil
			return name, true
		}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		d.seen[dir] = names
	}
	if names == nil {
		return name, true
	}
	var near string
	for _, n := range names {
		if n == name {
			return n, true
		}
		if strings.EqualFold(n, name) {
			near = n
		}
	}
	if near == "" {
		near = "no entry of that name"
	}
	return near, false
}

// escapesRoot reports whether a cleaned relative path climbs out of its root.
func escapesRoot(cleaned string) bool {
	return cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator))
}

// orUnstated keeps the provenance columns honest in a finding: an empty
// column reads as unstated rather than as an empty quotation.
func orUnstated(s string) string {
	if strings.TrimSpace(s) == "" {
		return "provenance unstated"
	}
	return s
}
