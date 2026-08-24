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
// way to know what it no longer holds, because the evidence and the thing
// are the same object.
//
// That asymmetry is the whole argument for a ledger. Anything else a check
// can find is present in the tree: a broken link names its target, an
// oversized kernel names its bytes. A lost sentence names nothing. So the
// line writes down, in advance and in prose, the statements it intends never
// to lose silently, and where each one lives. This check reads that ledger
// and asserts every fragment is still where the ledger says.
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

// anchorColumns is the ledger's fixed shape. A row with any other cell count
// is a finding rather than a skipped line: a fragment containing a literal
// "|" splits into an extra cell, and silently dropping that row would remove
// protection from exactly the statement someone took the trouble to list.
const anchorColumns = 4

// ParseLedger reads the anchor rows out of a ledger's markdown.
//
// Header and separator rows are recognized by SHAPE, never by the words in
// them: a row whose cells are all dashes is a separator, and the row
// immediately above it is its header. No column name is special to this
// tool, so a line may title its columns in its own language.
//
// AN EMPTY LEDGER IS AN ERROR, NEVER A GREEN. A ledger with no rows guards
// nothing, and "everything present" and "nothing checked" must never print
// the same line.
func ParseLedger(raw []byte) ([]Anchor, []Failure, error) {
	var out []Anchor
	var failures []Failure
	for i, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) < 2 || !strings.HasPrefix(trimmed, "|") || !strings.HasSuffix(trimmed, "|") {
			continue
		}
		cells := splitRow(trimmed)
		if allSeparator(cells) {
			// The row above a separator was this table's header. Drop it
			// rather than checking a column title as if it were a fragment.
			if n := len(out); n > 0 && out[n-1].Line == i {
				out = out[:n-1]
			}
			continue
		}
		if len(cells) != anchorColumns {
			failures = append(failures, Failure{
				fmt.Sprintf("ledger:%d", i+1),
				fmt.Sprintf("malformed row: %d columns, want %d — a fragment containing \"|\" splits into an extra cell; pick a fragment without one rather than escaping it", len(cells), anchorColumns),
			})
			continue
		}
		out = append(out, Anchor{
			Fragment: cells[0], Home: cells[1], Given: cells[2], By: cells[3],
			Line: i + 1,
		})
	}
	if len(out) == 0 {
		return nil, failures, fmt.Errorf("the ledger holds no anchor rows; an empty ledger guards nothing and must not read as a pass")
	}
	return out, failures, nil
}

// splitRow splits a `| a | b |` row into trimmed cells.
func splitRow(row string) []string {
	inner := strings.TrimSuffix(strings.TrimPrefix(row, "|"), "|")
	parts := strings.Split(inner, "|")
	cells := make([]string, len(parts))
	for i, p := range parts {
		cells[i] = strings.TrimSpace(p)
	}
	return cells
}

func allSeparator(cells []string) bool {
	for _, c := range cells {
		if !separatorCell.MatchString(c) {
			return false
		}
	}
	return len(cells) > 0
}

// Corpus verifies every anchor is present in its home file under root.
//
// It returns every failure rather than the first: the losses this exists to
// catch arrive in batches — a move, a restore, a consolidation pass — and a
// one-at-a-time report would take as many runs as there were losses.
func Corpus(root string, as []Anchor) []Failure {
	var failures []Failure
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
		rel := filepath.FromSlash(a.Home)
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
			// Symlinks are never followed, as everywhere else here: an
			// anchor "present" through a link points at a file this repo
			// does not govern, which is not the protection claimed.
			failures = append(failures, Failure{a.Home, fmt.Sprintf("not a regular file (%s); symlinks are never followed, so %q is not held here (ledger:%d)", fi.Mode().Type(), a.Fragment, a.Line)})
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
	return failures
}

// escapesRoot reports whether a cleaned relative path climbs out of its root.
func escapesRoot(rel string) bool {
	cleaned := filepath.Clean(rel)
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
