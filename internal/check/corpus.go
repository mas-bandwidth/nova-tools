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

// fenceRE captures a fenced-code-block delimiter run. Fences are tracked by
// CommonMark's rule rather than by toggling on any delimiter: an opening fence
// records its character and length, and only a run of the SAME character, at
// least as long and carrying nothing after it, closes it. A toggle looked
// simpler and was a silent bypass — a ledger documenting its own format
// mentions both delimiters, which left the toggle stuck open and dropped every
// row after it while the run printed OK.
var fenceRE = regexp.MustCompile("^(`{3,}|~{3,})(.*)$")

// anchorColumns is the ledger's fixed shape. A row with any other cell count
// is a finding rather than a skipped line: a fragment containing a literal
// "|", or a trailing comment after the last pipe, produces an extra cell, and
// silently dropping that row would remove protection from exactly the
// statement someone took the trouble to list.
const anchorColumns = 4

// ParseLedger reads the anchor rows out of a ledger's markdown.
//
// THE TABLE DECLARES ITS OWN SHAPE; THIS DOES NOT GUESS AT ONE. That is the
// whole parsing rule, and it replaced two earlier attempts that each failed in
// one direction: requiring outer pipes silently dropped rows a renderer
// accepts, and then accepting any pipe-bearing line read ordinary prose and
// unrelated tables as anchors. Both are the same mistake — a heuristic about
// what a table looks like — so the heuristic is gone.
//
// A run is a block of consecutive non-blank lines outside any fence, at least
// one of which bears pipes. A run is an ANCHOR TABLE only when its second line
// is a separator whose cell count matches its first line's, and that count is
// four. Everything else is somebody else's table — a two-column glossary, a
// prose line that happens to contain pipes — and is left alone, because a
// protection check that reddens on a glossary is one people learn to silence.
//
// Header and separator are recognized by SHAPE and POSITION, never by the
// words in them: no column title is special to this tool, and a line may title
// its columns in its own language. A ledger may hold any number of tables.
//
// What IS reported, because each would otherwise be a silent loss inside the
// ledger's own anchor table: a separator in the body, a row whose column count
// is not four, a table whose separator disagrees with its header, a pipe-led
// block with no separator at all (markdown will not render it as a table, so
// none of its rows would ever be checked), a lone four-column row adrift
// outside any table, and an unterminated fence.
//
// AN EMPTY LEDGER IS AN ERROR, NEVER A GREEN. A ledger with no rows guards
// nothing, and "everything present" and "nothing checked" must never print
// the same line.
func ParseLedger(raw []byte) ([]Anchor, []Failure, error) {
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")

	var out []Anchor
	var failures []Failure
	run := []int{} // line indexes of the current run

	fail := func(line int, reason string) {
		failures = append(failures, Failure{fmt.Sprintf("ledger:%d", line+1), reason})
	}

	flush := func() {
		defer func() { run = run[:0] }()
		if len(run) == 0 {
			return
		}
		first := splitRow(lines[run[0]])

		// A lone four-column row outside any table: an anchor row that lost
		// its table, which is the one single-line case worth a word.
		if len(run) == 1 {
			if len(first) == anchorColumns && !allSeparator(first) && strings.HasPrefix(strings.TrimSpace(lines[run[0]]), "|") {
				fail(run[0], "a four-column row standing outside any table; markdown will not render it as a table row, so it is checked by nothing — give it a header and a separator, or fold it into the table above")
			}
			return
		}

		second := splitRow(lines[run[1]])
		if !allSeparator(second) {
			// Not a table at all. Only say so when it is unmistakably meant
			// as one, so ordinary prose carrying pipes stays silent.
			if strings.HasPrefix(strings.TrimSpace(lines[run[0]]), "|") {
				fail(run[0], "a table with no separator row under its header; markdown will not render this as a table, so none of its rows are checked — add the |---|---| line")
			}
			return
		}
		if len(second) != len(first) {
			fail(run[1], fmt.Sprintf("the separator row has %d cells and its header has %d; markdown will not render this as a table, so none of its rows are checked", len(second), len(first)))
			return
		}
		if len(first) != anchorColumns {
			return // another table entirely — a glossary, a key, not ours
		}

		for _, i := range run[2:] {
			cells := splitRow(lines[i])
			switch {
			case allSeparator(cells):
				fail(i, "a separator row in the middle of a table; a separator belongs directly under the header and nowhere else, and one here would quietly take the row above it out of the check")
			case len(cells) != anchorColumns:
				fail(i, fmt.Sprintf("malformed row: %d columns, want %d — a \"|\" inside a cell, or anything trailing the last pipe, splits into another column; pick a fragment without one rather than escaping it", len(cells), anchorColumns))
			default:
				out = append(out, Anchor{
					Fragment: cells[0], Home: cells[1], Given: cells[2], By: cells[3],
					Line: i + 1,
				})
			}
		}
	}

	var fenceChar byte
	var fenceLen, fenceLine int
	fenced := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if m := fenceRE.FindStringSubmatch(trimmed); m != nil {
			delim := m[1]
			if !fenced {
				flush()
				fenced, fenceChar, fenceLen, fenceLine = true, delim[0], len(delim), i
				continue
			}
			// Only the same character, at least as long, with nothing after
			// it, closes the fence. Anything else is content.
			if delim[0] == fenceChar && len(delim) >= fenceLen && strings.TrimSpace(m[2]) == "" {
				fenced = false
			}
			continue
		}
		if fenced {
			continue
		}
		if trimmed == "" {
			flush()
			continue
		}
		if strings.ContainsRune(trimmed, '|') {
			run = append(run, i)
			continue
		}
		flush()
	}
	if fenced {
		// Swallowing the tail of a file is exactly the silent drop this check
		// exists to forbid, so it is named rather than tolerated.
		fail(fenceLine, "unterminated code fence; every line after it was read as illustration and no row below it was checked")
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
	absRoot, resolvedRoot, err := ResolveRoot(root)
	if err != nil {
		return nil, err
	}
	// The ledger's own path, resolved, so a row cannot name the ledger as its
	// own home — which would pass forever, the row being its own evidence.
	// Both sides go through Abs first: comparing an absolute resolution to a
	// relative one silently disarmed this guard whenever --root and --ledger
	// were given in different forms, which is an ordinary invocation.
	absLedger, err := filepath.Abs(ledgerPath)
	if err != nil {
		return nil, fmt.Errorf("ledger %q cannot be resolved: %w", ledgerPath, err)
	}
	resolvedLedger, err := filepath.EvalSymlinks(absLedger)
	if err != nil {
		// The ledger was read a moment ago, so this cannot be routine; and a
		// dropped guard is worse than a refusal.
		return nil, fmt.Errorf("ledger %q cannot be resolved: %w", ledgerPath, err)
	}

	var failures []Failure
	dirs := newDirCache()

	// THE LEDGER GUARDS THE TREE; THIS GUARDS THE LEDGER. Rows can be lost by
	// exactly the events rows exist to catch, and a shrunken ledger otherwise
	// goes green with a smaller number that nothing compares to anything.
	if len(as) < minAnchors {
		failures = append(failures, Failure{
			"ledger",
			fmt.Sprintf("the ledger holds %s, below the stated floor of %d — rows have been lost from the ledger itself, which is the one loss the rows cannot report. Restore them, or lower the floor in the same commit as a visible decision", plural(len(as), "row"), minAnchors),
		})
	}

	// A duplicated row inflates the count the floor is measured against
	// without protecting anything more, which would let the floor be
	// satisfied by copy-paste.
	firstSeen := map[string]int{}
	for _, a := range as {
		if a.Fragment == "" || a.Home == "" {
			continue // reported on its own terms below
		}
		key := a.Fragment + "\x00" + a.Home
		if prev, dup := firstSeen[key]; dup {
			failures = append(failures, Failure{
				fmt.Sprintf("ledger:%d", a.Line),
				fmt.Sprintf("duplicates ledger:%d (same fragment, same home); a repeated row raises the count --min-anchors is measured against while protecting nothing more", prev),
			})
			continue
		}
		firstSeen[key] = a.Line
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

		path := filepath.Join(absRoot, rel)
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
		// knows what it actually holds — and EVERY component is checked,
		// because a renamed directory is as real a move as a renamed file.
		if bad, actual, why := dirs.spellingOf(absRoot, rel); why != "" {
			failures = append(failures, Failure{a.Home, fmt.Sprintf("%s at %q (the directory holds %q) — a case-only rename is a real move, and this filesystem matched a different spelling (ledger:%d)", why, bad, actual, a.Line)})
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

// ResolveRoot validates --root and resolves it once. Exported so a caller can
// refuse a bad root BEFORE printing any finding: a FAIL line emitted by a run
// that then exits 2 tells a scanner about findings from a run that did not
// happen.
// It returns the root twice: made absolute, and additionally symlink-resolved.
// BOTH are needed and mixing them is a real defect — every path this check
// builds must be joined onto the ABSOLUTE root, while the containment
// comparison happens against the RESOLVED one. Joining onto the caller's raw
// spelling and comparing against a resolved root made every anchor under a
// relative --root report as reached through a symlink.
func ResolveRoot(root string) (abs, resolved string, err error) {
	info, err := os.Stat(root)
	if err != nil {
		return "", "", fmt.Errorf("root: %w", err)
	}
	if !info.IsDir() {
		return "", "", fmt.Errorf("root %q is not a directory", root)
	}
	abs, err = filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("root %q cannot be resolved: %w", root, err)
	}
	resolved, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return "", "", fmt.Errorf("root %q cannot be resolved: %w", root, err)
	}
	return abs, resolved, nil
}

// dirCache answers "does this directory hold this exact name" without
// re-reading a directory once per anchor. A case-insensitive filesystem will
// happily Lstat a name it does not hold, so the directory entries are the only
// witness there is.
type dirCache struct{ seen map[string]dirListing }

type dirListing struct {
	names    []string
	readable bool
}

func newDirCache() *dirCache { return &dirCache{seen: map[string]dirListing{}} }

// spellingOf walks every component of rel under root and reports the first one
// the directory does not hold byte-for-byte. why is "" when the whole path is
// spelled as the ledger gives it.
//
// An UNREADABLE directory is a finding rather than a pass. Every other I/O
// error here says "nothing was checked, which is not a pass", and a listing
// failure that quietly returned yes would be that rule broken in the one place
// it is hardest to see.
func (d *dirCache) spellingOf(root, rel string) (bad, actual, why string) {
	parts := strings.Split(rel, string(filepath.Separator))
	dir := root
	sofar := ""
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		sofar = filepath.Join(sofar, part)
		listing := d.list(dir)
		if !listing.readable {
			return sofar, "the directory cannot be listed", "the spelling could not be verified"
		}
		exact, near := false, ""
		for _, n := range listing.names {
			if n == part {
				exact = true
				break
			}
			if strings.EqualFold(n, part) {
				near = n
			}
		}
		if !exact {
			if near == "" {
				near = "no entry of that name"
			}
			return sofar, near, "the path is spelled differently on disk"
		}
		dir = filepath.Join(dir, part)
	}
	return "", "", ""
}

func (d *dirCache) list(dir string) dirListing {
	if l, ok := d.seen[dir]; ok {
		return l
	}
	entries, err := os.ReadDir(dir)
	l := dirListing{readable: err == nil}
	for _, e := range entries {
		l.names = append(l.names, e.Name())
	}
	d.seen[dir] = l
	return l
}

// plural keeps a finding readable rather than saying "1 rows".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
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
