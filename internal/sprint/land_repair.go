package sprint

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/pkg/diffcheck"
	"github.com/mas-bandwidth/nova-tools/pkg/hygiene"
)

// The lander's document repairs (docs/SPEC-SPRINT.md section 7, the lander's checks): a
// mechanical fault a change leaves in a Markdown or text file is one a formatter fixes,
// so it is fixed the way the ledgers are regenerated, not refused. The faults: a line
// the change ends in CRLF, trailing whitespace on a line the change writes, a missing
// final newline, a code fence the change leaves open, and a backquote the change leaves
// unmatched in a paragraph. A fault is repaired only when there is one repair; when
// there are two ways to put it right that read differently, it is refused with the
// line. A fault of the base's (no line of the change in it) is left alone. A code span
// is judged on the file the change leaves, so a change that only deletes (the line that
// closed a span, or its closing backquote) is repaired or refused as an addition is. The lander
// calls RepairMerge on each merge commit before its E4 check (cmd/nova-sprint/land.go,
// checkCard); a stream's prose globs (FieldProse) are not read for backquotes at all.

// DocFix is one fault of a document: its file, the line (1-based, in the file as
// repaired), and what was done or, for a refusal, why there is no one repair.
type DocFix struct {
	File string
	Line int
	What string
}

func (f DocFix) String() string { return fmt.Sprintf("%s:%d %s", f.File, f.Line, f.What) }

// DocFile says file is a document the repairs read: a Markdown or text file.
func DocFile(file string) bool {
	switch path.Ext(file) {
	case ".md", ".txt":
		return true
	}
	return false
}

// DocProse says file is under one of a stream's prose globs (PATHS globs, `**` across
// directories): a file whose backquotes are its own and are not checked at all.
func DocProse(globs []string, file string) bool {
	return slices.ContainsFunc(globs, func(g string) bool { return g != "" && hygiene.MatchGlob(g, file) })
}

// DocLines is what a change does to one document: the lines it writes (1-based, in the
// file after it) and each run of lines it takes out.
type DocLines struct {
	Added   []int
	Deleted []DocCut
}

// DocCut is one run of lines a change takes out of a document: At is the line of the
// file after it that now stands where they stood (the line after the cut, one past the
// last line at the end of the file), Lines is their text.
type DocCut struct {
	At    int
	Lines []string
}

// DocChanged is what a unified diff does to each file, by the new side's path: the lines
// it writes and the runs of lines it deletes, so that a deletion-only change is judged on
// the file it leaves as an addition is (E4 judges the result, not the added backquotes).
func DocChanged(diff string) map[string]DocLines {
	out := map[string]DocLines{}
	for _, f := range diffcheck.Parse(diff) {
		d := out[f.New]
		for _, h := range f.Hunks {
			line := max(h.NewStart, 1)
			cut := -1 // the index in d.Deleted of the run of '-' lines being read
			for _, l := range h.Lines {
				if l == "" {
					continue
				}
				switch l[0] {
				case '-':
					if cut < 0 {
						d.Deleted = append(d.Deleted, DocCut{At: line})
						cut = len(d.Deleted) - 1
					}
					d.Deleted[cut].Lines = append(d.Deleted[cut].Lines, l[1:])
					continue
				case '+':
					d.Added = append(d.Added, line)
					line++
				case ' ':
					line++
				}
				cut = -1
			}
		}
		out[f.New] = d
	}
	return out
}

// joinedWhat opens a fix's words for the code spans joined in a file: "E4 repaired: N
// spans joined in <file>", one fix a file, said on the note as it is.
const joinedWhat = "E4 repaired: "

// RepairNote is the landing note's words for fixes: "E4 repaired: N spans joined in
// <file>" for each file whose wrapped code spans were joined, then "the documents were
// repaired at the merge: <file>:<line> <what>; ..." for the rest, "" for none.
func RepairNote(fixes []DocFix) string {
	var s, rest []string
	for _, f := range fixes {
		if strings.HasPrefix(f.What, joinedWhat) {
			s = append(s, f.What)
		} else {
			rest = append(rest, f.String())
		}
	}
	if len(rest) > 0 {
		s = append(s, "the documents were repaired at the merge: "+strings.Join(rest, "; "))
	}
	return strings.Join(s, "; ")
}

// StreamProse is a stream's prose globs (FieldProse, written by `stream set --prose`):
// the files whose backquotes are their own, nil when it names none.
func StreamProse(s *Snapshot, stream string) []string {
	ctl := s.StreamCtl(stream)
	if ctl == nil {
		return nil
	}
	var out []string
	for _, g := range strings.Split(ctl.F(FieldProse), ",") {
		if g = strings.TrimSpace(g); g != "" {
			out = append(out, g)
		}
	}
	return out
}

// RepairMerge repairs the documents a merge commit at HEAD of the clone dir changes
// against before (the batch branch's tip before the merge), the lander's fix on the way
// in as the ledgers' regeneration is: each Markdown or text file the merge writes or
// deletes from is read from the tree, repaired on the merge's own lines (RepairDoc, its
// backquotes unread under
// a prose glob), written back, and the merge commit amended with the repair in its body,
// its parents and subject kept. note is the landing note (RepairNote), "" when nothing
// was repaired; refused is each fault with no one repair, and each case of an edited model
// without a current run record (recordsRefusals, land_records.go), and then nothing is written. A
// symlink is not written through. git runs git in dir and returns its trimmed stdout.
func RepairMerge(dir string, git func(args ...string) (string, error), before string, prose []string) (note string, refused []DocFix, err error) {
	diff, err := git("diff", "-M", "--no-color", before, "HEAD")
	if err != nil {
		return "", nil, err
	}
	changed := DocChanged(diff)
	files := make([]string, 0, len(changed))
	for f := range changed {
		if DocFile(f) {
			files = append(files, f)
		}
	}
	slices.Sort(files)
	type write struct {
		file, text string
		mode       os.FileMode
	}
	var writes []write
	var fixes []DocFix
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		fi, err := os.Lstat(p)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return "", nil, err
		}
		text, fx, rf := RepairDoc(f, string(b), changed[f], DocProse(prose, f))
		fixes, refused = append(fixes, fx...), append(refused, rf...)
		if text != string(b) {
			writes = append(writes, write{f, text, fi.Mode().Perm()})
		}
	}
	// a merge that edits a model is refused without its run records (land_records.go)
	owed, err := recordsRefusals(dir, diff)
	if err != nil {
		return "", nil, err
	}
	refused = append(refused, owed...)
	if len(refused) > 0 || len(writes) == 0 {
		return "", refused, nil
	}
	for _, w := range writes {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(w.file)), []byte(w.text), w.mode); err != nil {
			return "", nil, err
		}
		if _, err := git("add", "--", w.file); err != nil {
			return "", nil, err
		}
	}
	msg, err := git("log", "-1", "--format=%B")
	if err != nil {
		return "", nil, err
	}
	note = RepairNote(fixes)
	body := strings.ToUpper(note[:1]) + note[1:] + "."
	if _, err := git("commit", "-q", "--amend", "-m", msg, "-m", body); err != nil {
		return "", nil, err
	}
	return note, nil, nil
}

// RepairDoc repairs the faults of text, a document's content after a merge, that are the
// change's: the formatter's on the lines it writes (changed.Added, 1-based), a code span's
// in a paragraph it writes a backquote on or takes an odd count of backquotes from
// (changed.Deleted). prose skips the backquote check. fixed is text repaired; fixes is
// each repair made; refused is each fault with no one repair, which the lander refuses
// with its line. A file that is not a document is returned as it is.
func RepairDoc(file, text string, changed DocLines, prose bool) (fixed string, fixes, refused []DocFix) {
	if !DocFile(file) || text == "" {
		return text, nil, nil
	}
	ch := map[int]bool{}
	for _, n := range changed.Added {
		ch[n] = true
	}
	finalNL := strings.HasSuffix(text, "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	fix := func(n int, what string) { fixes = append(fixes, DocFix{file, n, what}) }

	// a code span the change wraps across a line break is joined onto one line first, and
	// the change's lines and cuts are read by the joined file's lines from here on
	if !prose {
		joined, to, joins := joinSpans(lines, fences(lines), ch)
		if len(joins) > 0 {
			lines, ch = joined, map[int]bool{}
			for _, n := range changed.Added {
				if n >= 1 && n <= len(to) {
					ch[to[n-1]+1] = true
				}
			}
			cuts := make([]DocCut, len(changed.Deleted))
			for k, c := range changed.Deleted {
				cuts[k] = c
				if c.At >= 1 && c.At <= len(to) {
					cuts[k].At = to[c.At-1] + 1
				} else if c.At > len(to) {
					cuts[k].At = len(lines) + 1
				}
			}
			changed.Deleted = cuts
			fix(joins[0]+1, fmt.Sprintf("%s%d spans joined in %s", joinedWhat, len(joins), file))
		}
	}

	// CRLF: a line the change writes loses its CR, unless the file's own lines are CRLF.
	crlf := false
	for i, l := range lines {
		if !ch[i+1] && strings.HasSuffix(l, "\r") {
			crlf = true
		}
	}
	for i, l := range lines {
		if !crlf && ch[i+1] && strings.HasSuffix(l, "\r") {
			lines[i] = strings.TrimSuffix(l, "\r")
			fix(i+1, "a CRLF line ending made LF")
		}
	}

	fence := fences(lines)
	md := path.Ext(file) == ".md"
	for i, l := range lines {
		if !ch[i+1] || fence[i] != inProse {
			continue
		}
		t := strings.TrimRight(l, " \t")
		if t == l {
			continue
		}
		// a Markdown line ending in two spaces before more of its paragraph is a hard break
		if md && strings.HasSuffix(l, "  ") && strings.TrimSpace(t) != "" && i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" &&
			strings.TrimRight(l, " ") == t {
			continue
		}
		lines[i] = t
		fix(i+1, "trailing whitespace trimmed")
	}

	if !prose {
		cuts := cutTicks(lines, fence, changed.Deleted)
		for _, p := range paragraphs(lines, fence) {
			f, r := repairSpans(file, lines, p, ch, cuts)
			fixes, refused = append(fixes, f...), append(refused, r...)
		}
	}

	if open, run := openFence(lines, fence); open >= 0 {
		touched := false
		for n := open + 1; n <= len(lines); n++ {
			touched = touched || ch[n]
		}
		switch {
		case !touched:
		case blankThenText(lines[open+1:]):
			refused = append(refused, DocFix{file, open + 1, "leaves a code fence open: the block it opens has no one end (a blank line and more text follow it)"})
		default:
			lines = append(lines, run)
			finalNL = true
			fix(len(lines), "the code fence opened at line "+fmt.Sprint(open+1)+" closed at the end of the file")
		}
	}

	if !finalNL && ch[len(lines)] {
		finalNL = true
		fix(len(lines), "a final newline added")
	}
	fixed = strings.Join(lines, "\n")
	if finalNL {
		fixed += "\n"
	}
	return fixed, fixes, refused
}

// A line's place against the code fences: in prose, a fence line, or inside a block.
const (
	inProse = iota
	onFence
	inBlock
)

// fenceRun is a line's fence (three or more backquotes or tildes after up to three
// spaces) and its rest, ok false for a line that is no fence.
func fenceRun(l string) (run, rest string, ok bool) {
	t := strings.TrimLeft(l, " ")
	if len(l)-len(t) > 3 || len(t) < 3 || (t[0] != '`' && t[0] != '~') {
		return "", "", false
	}
	n := len(t) - len(strings.TrimLeft(t, t[:1]))
	if n < 3 {
		return "", "", false
	}
	return t[:n], t[n:], true
}

// fences is each line's place against the code fences.
func fences(lines []string) []int {
	out := make([]int, len(lines))
	open := ""
	for i, l := range lines {
		run, rest, ok := fenceRun(strings.TrimSuffix(l, "\r"))
		switch {
		case open == "" && ok:
			open, out[i] = run, onFence
		case open != "" && ok && run[0] == open[0] && len(run) >= len(open) && strings.TrimSpace(rest) == "":
			open, out[i] = "", onFence
		case open != "":
			out[i] = inBlock
		}
	}
	return out
}

// openFence is the 0-based line of the fence left open at the end of lines and the run
// that closes it, -1 when every fence is closed.
func openFence(lines []string, fence []int) (int, string) {
	open := -1
	for i := range lines {
		if fence[i] != onFence {
			continue
		}
		if open < 0 {
			open = i
		} else {
			open = -1
		}
	}
	if open < 0 {
		return -1, ""
	}
	run, _, _ := fenceRun(strings.TrimSuffix(lines[open], "\r"))
	return open, run
}

// blankThenText says lines hold a blank line with a line of text after it.
func blankThenText(lines []string) bool {
	blank := false
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			blank = true
		} else if blank {
			return true
		}
	}
	return false
}

// joinSpans joins each code span the change wraps across a line break: a line of prose it
// writes that ends inside a span its own last backquote opens (the paragraph's count odd
// at its end, the line holding a backquote) is joined to the lines of the paragraph it
// writes after it up to the first that brings the count even, with a blank where each
// line broke. A line that starts a block (a heading, a quote, a list item, a table row)
// is not joined to the one above it and opens its count afresh, and a line of the base's
// is never touched; a wrap with no line of the change's to close it is left to the span
// check (repairSpans), which drops a stray backquote or refuses with the line. Inside a
// paragraph a line break reads as a blank, so the join changes the lines and not the
// text. joined is the lines after it, to each line's (0-based) line in joined, and joins
// the line of joined each span opens on.
func joinSpans(lines []string, fence []int, ch map[int]bool) (joined []string, to, joins []int) {
	to = make([]int, len(lines))
	text := func(i int) bool { return fence[i] == inProse && strings.TrimSpace(lines[i]) != "" }
	odd := false // the paragraph's count so far is odd: a span opened above is still open
	for i := 0; i < len(lines); {
		to[i] = len(joined)
		if !text(i) {
			odd = false
			joined = append(joined, lines[i])
			i++
			continue
		}
		if startsBlock(lines[i]) {
			odd = false
		}
		n, end := strings.Count(lines[i], "`"), i
		open := odd != (n%2 == 1)
		if open && n > 0 && ch[i+1] && !strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
			for k := i + 1; k < len(lines) && text(k) && ch[k+1] && !startsBlock(lines[k]); k++ {
				if open = open != (strings.Count(lines[k], "`")%2 == 1); !open {
					end = k
					break
				}
			}
		}
		if end == i {
			odd = odd != (n%2 == 1)
			joined = append(joined, lines[i])
			i++
			continue
		}
		l := lines[i]
		for k := i + 1; k <= end; k++ {
			to[k] = len(joined)
			l = strings.TrimRight(strings.TrimSuffix(l, "\r"), " \t") + " " + strings.TrimLeft(lines[k], " \t")
		}
		joins = append(joins, len(joined))
		joined = append(joined, l)
		i, odd = end+1, false
	}
	return joined, to, joins
}

// startsBlock says a line opens a block of its own rather than going on with the
// paragraph above it: a heading, a quote, a list item or a table row.
func startsBlock(l string) bool {
	t := strings.TrimLeft(l, " ")
	if t == "" || len(l)-len(t) > 3 {
		return false
	}
	switch t[0] {
	case '#', '>', '|':
		return true
	case '-', '*', '+':
		return len(t) == 1 || t[1] == ' ' || t[1] == '\t'
	}
	d := len(t) - len(strings.TrimLeft(t, "0123456789"))
	return d > 0 && d <= 9 && d < len(t) && (t[d] == '.' || t[d] == ')') && (d+1 == len(t) || t[d+1] == ' ' || t[d+1] == '\t')
}

// paragraphs is each run of non-blank prose lines, as 0-based line indexes.
func paragraphs(lines []string, fence []int) [][]int {
	var out [][]int
	var p []int
	for i, l := range lines {
		if fence[i] != inProse || strings.TrimSpace(l) == "" {
			if len(p) > 0 {
				out = append(out, p)
			}
			p = nil
			continue
		}
		p = append(p, i)
	}
	if len(p) > 0 {
		out = append(out, p)
	}
	return out
}

// tick is one run of backquotes in a paragraph: its line, its column (byte), its length,
// and the characters either side of it (a line's start or end is blank).
type tick struct {
	line, col, n int
	prev, next   rune
}

// ticksOf is every run of backquotes in the lines of a paragraph.
func ticksOf(lines []string, p []int) []tick {
	var out []tick
	for _, i := range p {
		l := lines[i]
		for c := 0; c < len(l); {
			if l[c] != '`' {
				c++
				continue
			}
			e := c
			for e < len(l) && l[e] == '`' {
				e++
			}
			t := tick{line: i, col: c, n: e - c, prev: ' ', next: ' '}
			if c > 0 {
				t.prev = rune(l[c-1])
			}
			if e < len(l) {
				t.next = rune(l[e])
			}
			out = append(out, t)
			c = e
		}
	}
	return out
}

// cut is a deletion's backquotes as one paragraph of the result holds them: the count it
// took from the paragraph and the lines of the paragraph beside it (0-based).
type cut struct {
	ticks  int
	beside []int
}

// cutTicks is each deletion's backquotes by the result's line (0-based) of the paragraph
// they were taken from. A cut's lines up to its first blank or fence line were the end of
// the paragraph above it, the lines after its last the start of the one below; a cut
// with neither is all of the paragraph it sits in; the paragraphs wholly inside it are
// gone and judged by no one. A line beside a cut is a line of the change's for the span
// check.
func cutTicks(lines []string, fence []int, cuts []DocCut) map[int]*cut {
	out := map[int]*cut{}
	prose := func(i int) bool {
		return i >= 0 && i < len(lines) && fence[i] == inProse && strings.TrimSpace(lines[i]) != ""
	}
	add := func(i, n int) {
		if n == 0 || !prose(i) {
			return
		}
		if out[i] == nil {
			out[i] = &cut{}
		}
		out[i].ticks += n
		out[i].beside = append(out[i].beside, i)
	}
	for _, c := range cuts {
		above, below := c.At-2, c.At-1
		first, last := -1, -1
		for k, l := range c.Lines {
			if _, _, ok := fenceRun(strings.TrimSuffix(l, "\r")); ok || strings.TrimSpace(l) == "" {
				if first < 0 {
					first = k
				}
				last = k
			}
		}
		if first < 0 {
			n := backquotes(c.Lines)
			if prose(above) {
				add(above, n)
				if out[above] != nil && prose(below) {
					out[above].beside = append(out[above].beside, below)
				}
			} else {
				add(below, n)
			}
			continue
		}
		add(above, backquotes(c.Lines[:first]))
		add(below, backquotes(c.Lines[last+1:]))
	}
	return out
}

// backquotes is the count of backquotes in lines.
func backquotes(lines []string) int {
	n := 0
	for _, l := range lines {
		n += strings.Count(l, "`")
	}
	return n
}

// repairSpans is a paragraph's backquote fault: an odd count of backquotes on the
// paragraph with a run on a line the change writes, or with an odd count of backquotes
// taken from it by the change's deletions (cuts, by line). It is repaired by dropping the
// one run, on a line the change writes or beside one of its deletions, whose loss leaves
// every other run closed in a span that reads as one (an opening run not after a word
// and before a blank, a closing run not after a blank and before a word); when no run or
// more than one does, the paragraph is refused at its first such line with a run, or at
// the first line beside a deletion when none holds one.
func repairSpans(file string, lines []string, p []int, ch map[int]bool, cuts map[int]*cut) (fixes, refused []DocFix) {
	ts := ticksOf(lines, p)
	total := 0
	for _, t := range ts {
		total += t.n
	}
	if total%2 == 0 {
		return nil, nil
	}
	ours := map[int]bool{}
	taken, beside := 0, -1
	for _, i := range p {
		if ch[i+1] {
			ours[i] = true
		}
		if c := cuts[i]; c != nil {
			taken += c.ticks
			for _, b := range c.beside {
				ours[b] = true
				if beside < 0 || b < beside {
					beside = b
				}
			}
		}
	}
	var cands []int
	first := -1
	for k, t := range ts {
		if !ours[t.line] {
			continue
		}
		if first < 0 {
			first = k
		}
		if spansClose(slices.Delete(slices.Clone(ts), k, k+1)) {
			cands = append(cands, k)
		}
	}
	written := slices.ContainsFunc(ts, func(t tick) bool { return ch[t.line+1] })
	switch {
	case !written && taken%2 == 0:
		return nil, nil
	case first < 0:
		return nil, []DocFix{{file, beside + 1, "leaves a code span unmatched: the lines it deletes take an odd count of backquotes and no line beside them holds one to drop: " + strings.TrimSpace(lines[beside])}}
	case len(cands) != 1:
		t := ts[first]
		why := "no backquote's loss closes every span"
		if len(cands) > 1 {
			why = fmt.Sprintf("%d backquotes could be the stray one", len(cands))
		}
		return nil, []DocFix{{file, t.line + 1, "leaves a code span unmatched and the repair is ambiguous: " + why + ": " + strings.TrimSpace(lines[t.line])}}
	}
	t := ts[cands[0]]
	l := lines[t.line]
	lines[t.line] = l[:t.col] + l[t.col+t.n:]
	return []DocFix{{file, t.line + 1, fmt.Sprintf("a stray backquote dropped at column %d", t.col+1)}}, nil
}

// spansClose says runs pair into code spans as CommonMark pairs them (a run closes at
// the next run of its length) with none left over, each span reading as one.
func spansClose(ts []tick) bool {
	for i := 0; i < len(ts); {
		j := i + 1
		for j < len(ts) && ts[j].n != ts[i].n {
			j++
		}
		if j == len(ts) || looksClosing(ts[i]) || looksOpening(ts[j]) {
			return false
		}
		i = j + 1
	}
	return true
}

// looksClosing says a run reads only as a span's end: after a word, before a blank.
func looksClosing(t tick) bool { return !unicode.IsSpace(t.prev) && unicode.IsSpace(t.next) }

// looksOpening says a run reads only as a span's start: after a blank, before a word.
func looksOpening(t tick) bool { return unicode.IsSpace(t.prev) && !unicode.IsSpace(t.next) }
