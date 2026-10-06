package sprint

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/diffcheck"
	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
)

// The lander's document repairs (docs/SPEC-SPRINT.md section 7, the lander's checks): a
// mechanical fault a change leaves in a Markdown or text file is one a formatter fixes,
// so it is fixed the way the ledgers are regenerated, not refused. The faults: a line
// the change ends in CRLF, trailing whitespace on a line the change writes, a missing
// final newline, a code fence the change leaves open, and a backquote the change leaves
// unmatched in a paragraph. A fault is repaired only when there is one repair; when
// there are two ways to put it right that read differently, it is refused with the
// line. A fault of the base's (no line of the change in it) is left alone. Only the
// repair is written here; cmd/nova-sprint/land.go does not call it yet.

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

// DocChanged is the lines each file of a unified diff writes, by the new side's path,
// 1-based; a file the diff only deletes from has none.
func DocChanged(diff string) map[string][]int {
	out := map[string][]int{}
	for _, f := range diffcheck.Parse(diff) {
		for _, h := range f.Hunks {
			line := h.NewStart
			for _, l := range h.Lines {
				switch l[0] {
				case '+':
					out[f.New] = append(out[f.New], line)
					line++
				case ' ':
					line++
				}
			}
		}
	}
	return out
}

// RepairNote is the landing note's words for fixes: "the documents were repaired at the
// merge: <file>:<line> <what>; ...", "" for none.
func RepairNote(fixes []DocFix) string {
	if len(fixes) == 0 {
		return ""
	}
	s := make([]string, len(fixes))
	for i, f := range fixes {
		s[i] = f.String()
	}
	return "the documents were repaired at the merge: " + strings.Join(s, "; ")
}

// RepairDoc repairs the faults of text, a document's content after a merge, that lie on
// changed (the lines the change writes, 1-based). prose skips the backquote check. fixed
// is text repaired; fixes is each repair made; refused is each fault with no one repair,
// which the lander refuses with its line. A file that is not a document is returned as
// it is.
func RepairDoc(file, text string, changed []int, prose bool) (fixed string, fixes, refused []DocFix) {
	if !DocFile(file) || text == "" {
		return text, nil, nil
	}
	ch := map[int]bool{}
	for _, n := range changed {
		ch[n] = true
	}
	finalNL := strings.HasSuffix(text, "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	fix := func(n int, what string) { fixes = append(fixes, DocFix{file, n, what}) }

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
		for _, p := range paragraphs(lines, fence) {
			f, r := repairSpans(file, lines, p, ch)
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

// repairSpans is a paragraph's backquote fault: an odd count of backquotes on the
// paragraph with a run on a line the change writes. It is repaired by dropping the one
// run whose loss leaves every other run closed in a span that reads as one (an opening
// run not after a word and before a blank, a closing run not after a blank and before a
// word); when no run or more than one does, the paragraph is refused at its first changed
// line with a run.
func repairSpans(file string, lines []string, p []int, ch map[int]bool) (fixes, refused []DocFix) {
	ts := ticksOf(lines, p)
	total := 0
	for _, t := range ts {
		total += t.n
	}
	if total%2 == 0 {
		return nil, nil
	}
	var cands []int
	first := -1
	for k, t := range ts {
		if !ch[t.line+1] {
			continue
		}
		if first < 0 {
			first = k
		}
		if spansClose(slices.Delete(slices.Clone(ts), k, k+1)) {
			cands = append(cands, k)
		}
	}
	switch {
	case first < 0:
		return nil, nil
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
