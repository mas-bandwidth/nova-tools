// Package diffcheck is the lander's mechanical checks of one card's diff, the two the
// calibration of the decide read found a model read does not make (docs/SPEC-SPRINT.md
// section 7, the lander's checks): every file the diff changes is one the card's PATHS
// names (E12), and no changed line leaves a stranded sentence fragment or an unmatched
// backquote beside it (E4). Both read the unified diff alone, as git prints it; no model,
// no repository, no network.
package diffcheck

import (
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
)

// File is one file of a diff: its path before and after (equal unless renamed; a
// created or deleted file names the side it has on both) and its hunks.
type File struct {
	Old, New string
	Hunks    []Hunk
}

// Hunk is one @@ section: the new side's first line number and its lines, each with
// its marker (' ', '-' or '+').
type Hunk struct {
	NewStart int
	Lines    []string
}

// Parse reads a unified git diff (git diff, with or without -M).
func Parse(diff string) []File {
	var out []File
	var f *File
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			a, b, _ := strings.Cut(strings.TrimPrefix(l, "diff --git "), " b/")
			out = append(out, File{Old: strings.TrimPrefix(a, "a/"), New: b})
			f = &out[len(out)-1]
		case f == nil:
		case len(f.Hunks) == 0 && strings.HasPrefix(l, "rename from "):
			f.Old = strings.TrimPrefix(l, "rename from ")
		case len(f.Hunks) == 0 && strings.HasPrefix(l, "rename to "):
			f.New = strings.TrimPrefix(l, "rename to ")
		case strings.HasPrefix(l, "@@ "):
			f.Hunks = append(f.Hunks, Hunk{NewStart: newStart(l)})
		case len(f.Hunks) > 0 && l != "" && strings.ContainsRune(" -+", rune(l[0])):
			h := &f.Hunks[len(f.Hunks)-1]
			h.Lines = append(h.Lines, l)
		}
	}
	return out
}

// newStart is the new side's first line of a hunk header, @@ -a,b +c,d @@.
func newStart(header string) int {
	_, plus, _ := strings.Cut(header, " +")
	n, _ := strconv.Atoi(strings.FieldsFunc(plus, func(r rune) bool { return r == ',' || r == ' ' })[0])
	return n
}

// The class ledgers a card may update beyond its PATHS (the cards' own words: "Those
// ledger files are the only files beyond PATHS you may change"): under LedgerDir, a list
// file named as internal/ci spells its lists (allowlist_update_test.go, listFilePatterns,
// and the deleted-tests ledger) or a .txt shard of one of its counted-ledger directories
// (countedShardDirectories). The class tests' fixtures beside them are not ledgers.
const LedgerDir = "internal/ci/testdata/"

var (
	ledgerLists  = []string{"*allowlist*.txt", "*.allow", "*_examples.txt", "deleted-tests.txt"}
	ledgerShards = []string{"discarded", "scripthide", "okonfailure", "remedy", "generality", "generality-text", "testify"}
)

// Ledger says p is a class ledger (LedgerDir).
func Ledger(p string) bool {
	rest, ok := strings.CutPrefix(p, LedgerDir)
	if !ok {
		return false
	}
	dir, file, nested := strings.Cut(rest, "/")
	if !nested {
		return slices.ContainsFunc(ledgerLists, func(g string) bool { ok, _ := path.Match(g, rest); return ok })
	}
	return slices.Contains(ledgerShards, dir) && path.Ext(file) == ".txt"
}

// Outside is every file the diff changes that the card may not (E12); nil when paths is
// empty (a card that names no PATHS is held to none). A file is the card's when its
// PATHS globs name it or it is a ledger (Ledger). A rename holds both sides: the file it
// moves from is the card's, and the file it moves to is the card's too or stays in the
// directory it was in (a name card renames a file in place). A rename out of a PATHS
// file to anywhere else, and a file moved into the ledgers' directory, are outside.
func Outside(paths []string, diff string) []string {
	if len(paths) == 0 {
		return nil
	}
	named := func(p string) bool {
		return slices.ContainsFunc(paths, func(g string) bool { return hygiene.MatchGlob(g, p) })
	}
	mine := func(p string) bool { return named(p) || Ledger(p) }
	var out []string
	for _, f := range Parse(diff) {
		from := mine(f.Old)
		to := mine(f.New) || f.Old != f.New && named(f.Old) && path.Dir(f.Old) == path.Dir(f.New)
		if !from || !to {
			out = append(out, f.New)
		}
	}
	return out
}

// Finding is one place a changed line breaks the text beside it.
type Finding struct {
	File string
	Line int // the new side's line
	Why  string
}

func (f Finding) String() string { return fmt.Sprintf("%s:%d %s", f.File, f.Line, f.Why) }

// Fragments is every stranded fragment and unmatched backquote the diff leaves in prose
// (E4): a Go comment, or a line of a Markdown or text file. A block is a run of changed
// lines in a hunk, and it is read against the line before it:
//
//   - the block takes away a number of backquotes of one parity and puts back the
//     other, so a code span beside it is left open;
//   - the line before it ends mid-sentence (on a letter, a digit or a comma, and not on
//     a word that opens a sentence, as "The" does), the old
//     text went on with a word that does not open a sentence, and the new text goes on
//     with one that does (a capital and lower case after it: The, An, A): the sentence
//     before the block is left without its end.
func Fragments(diff string) []Finding {
	var out []Finding
	for _, f := range Parse(diff) {
		for _, h := range f.Hunks {
			out = append(out, hunkFragments(f.New, h)...)
		}
	}
	return out
}

func hunkFragments(file string, h Hunk) []Finding {
	var out []Finding
	line := h.NewStart
	for i := 0; i < len(h.Lines); {
		if h.Lines[i][0] == ' ' {
			i, line = i+1, line+1
			continue
		}
		j := i
		var removed, added []string
		for ; j < len(h.Lines) && h.Lines[j][0] != ' '; j++ {
			if t, ok := prose(file, h.Lines[j][1:]); ok && h.Lines[j][0] == '-' {
				removed = append(removed, t)
			} else if ok {
				added = append(added, t)
			}
		}
		if took, gave := ticks(removed), ticks(added); took%2 != gave%2 {
			out = append(out, Finding{file, line, fmt.Sprintf("leaves a code span unmatched: the change takes %d backquotes and puts back %d", took, gave)})
		}
		var after string
		if j < len(h.Lines) {
			after, _ = prose(file, h.Lines[j][1:])
		}
		if i > 0 && h.Lines[i-1][0] == ' ' {
			if before, ok := prose(file, h.Lines[i-1][1:]); ok && midSentence(before) && !opens(lastWord(before)) &&
				!opens(first(removed, after)) && opens(first(added, after)) && first(removed, after) != first(added, after) {
				out = append(out, Finding{file, line, fmt.Sprintf("leaves a sentence fragment: %q is followed by a new sentence, %q", tail(before), head(first(added, after)))})
			}
		}
		for k := i; k < j; k++ {
			if h.Lines[k][0] == '+' {
				line++
			}
		}
		i = j
	}
	return out
}

// prose is a line's words when a reader reads it as a sentence: a Go comment's after
// its //, a Markdown or text line's; false for code, a directive, an indented block, a
// fence, a heading, a list item, a table row and a blank. A Markdown line indented
// under a list item is prose too.
func prose(file, l string) (string, bool) {
	t := strings.TrimSpace(l)
	switch path.Ext(file) {
	case ".go":
		rest, ok := strings.CutPrefix(t, "//")
		if !ok || strings.HasPrefix(rest, "go:") || strings.HasPrefix(rest, "\t") || strings.HasPrefix(rest, "  ") {
			return "", false
		}
		t = strings.TrimSpace(rest)
	case ".md", ".txt":
	default:
		return "", false
	}
	for _, opener := range []string{"```", "#", "|", "- ", "* ", "+ ", ">"} {
		if strings.HasPrefix(t, opener) {
			return "", false
		}
	}
	return t, t != ""
}

// ticks counts the backquotes of lines, a fence's three not counted.
func ticks(lines []string) int {
	n := 0
	for _, l := range lines {
		n += strings.Count(strings.ReplaceAll(l, "```", ""), "`")
	}
	return n
}

// first is the first of lines, else or.
func first(lines []string, or string) string {
	if len(lines) > 0 {
		return lines[0]
	}
	return or
}

// midSentence says a line ends where a sentence goes on: on a letter, a digit or a comma.
func midSentence(s string) bool {
	r, _ := utf8.DecodeLastRuneInString(s)
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == ','
}

// opens says a line begins as a sentence begins: its first word a capital with lower
// case after it, or a capital alone (A, I). An all-capitals word (MORE, THE COUNT) and
// a name in camel case are words of the sentence before, not its end.
func opens(s string) bool {
	w, _, _ := strings.Cut(s, " ")
	w = strings.TrimRight(w, ",.;:")
	r, size := utf8.DecodeRuneInString(w)
	if !unicode.IsUpper(r) {
		return false
	}
	for _, c := range w[size:] {
		if !unicode.IsLower(c) {
			return false
		}
	}
	return true
}

// lastWord is a line's last word.
func lastWord(s string) string {
	w := strings.Fields(s)
	if len(w) == 0 {
		return ""
	}
	return w[len(w)-1]
}

// tail and head are a line's last and first few words, for a finding.
func tail(s string) string {
	w := strings.Fields(s)
	if len(w) > 4 {
		w = w[len(w)-4:]
	}
	return strings.Join(w, " ")
}

func head(s string) string {
	w := strings.Fields(s)
	if len(w) > 4 {
		w = w[:4]
	}
	return strings.Join(w, " ")
}
