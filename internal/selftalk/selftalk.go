// Package selftalk finds sentences in which a writer passes a standing
// verdict on themselves, in two disjoint classes, each finding with the
// source line it starts on.
//
// Scan finds the first class: a first-person claim (I am, I cannot, I always,
// my <noun> is ...) carrying a word of failure (fallible, broken, bad at,
// worst, cannot check ...). A claim with a date or a measurement word is
// DATED, a record; one without is STANDING. It matches specified grammatical
// and lexical shapes, and meaning remains the writer's judgment: a prohibition
// ("never merge without a read") is a rule, not a claim, so a document made of
// rules does not score as one made of self-verdicts.
//
// ScanInstallation finds the second class, INSTALLATION: a standing
// self-verdict built from neutral words, which the first class cannot see (a
// self-superlative, a door stated shut, a verdict on a practice, a habit),
// matched by shape. Rules lists every shape and licence with a sentence it
// finds and one it passes.
//
// Both classes are partial by design: a shape the table does not hold is not
// found, so a scan with no finding clears the known shapes, never the file.
package selftalk

import (
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Verdict is the classification of a claim.
//
// The one distinction that decides every case: a capability denial is a
// measurement with a date, never a remembered property. A dated observation
// is a record and is welcome; a standing claim says what the writer
// permanently is, and that is the thing to look at.
type Verdict string

const (
	// Standing — says what the writer permanently is. Date it, cut it, or keep it
	// on purpose; the judgment is the writer's, never this package's.
	Standing Verdict = "STANDING"
	// Dated — a record of something that happened. Welcome.
	Dated Verdict = "DATED"
)

// Claim is one classified sentence.
type Claim struct {
	Line    int // first source line of the matched claim, before markdown flattening
	Verdict Verdict
	Text    string
	Match   string // the negative words that made the sentence a claim
}

// A claim is a sentence carrying a first-person self/capability assertion:
// the whole-sentence shape
//
//	(?i)[^.!?]{0,120}\b(<claimMarkers>)\b[^.!?]{0,160}[.!?]
//
// The bounded context either side keeps a match to roughly one sentence
// without needing a real parser.
//
// That shape is never compiled as one expression, because its leading
// [^.!?]{0,120} costs a hundred and twenty live states at every byte of the
// input: on one megabyte of a single sentence the scan ran past CI's 75 s
// test deadline under -race. claimTail finds the marker and the rest of the
// sentence, and claimLead walks back over the bounded context before it, so
// the scan is one linear pass plus at most claimLead runes per claim, and its
// spans are exactly the whole-sentence shape's (pinned by a test that runs
// both).
const claimMarkers = `I am|I'm|I have never|I always|I never|` +
	`I cannot|I can't|I can not|I do not|I don't|my \w+ is|makes me|I tend|I struggle|I fail|` +
	`reliably|every time|in one direction`

// claimTail is the claim shape from its marker on: the marker, at most 160
// more runes of the same sentence, and the sentence's terminator.
var claimTail = regexp.MustCompile(`(?i)\b(` + claimMarkers + `)\b[^.!?]{0,160}[.!?]`)

// claimLead is how many runes of the same sentence a claim reaches back before
// its marker: the shape's leading [^.!?]{0,120}.
const claimLead = 120

// leadStart is where a claim whose marker begins at at starts: up to
// claimLead runes back, stopping after a sentence terminator. Because a
// marker's sentence holds no terminator before its own end, the leftmost
// marker of a sentence that has one within reach of the end gives the
// leftmost start, which is the match the whole-sentence shape reports.
func leadStart(flat string, at int) int {
	for n := 0; n < claimLead && at > 0; n++ {
		r, size := utf8.DecodeLastRuneInString(flat[:at])
		if r == '.' || r == '!' || r == '?' {
			break
		}
		at -= size
	}
	return at
}

// negative is the vocabulary that turns a first-person assertion into a
// claim worth looking at: without it every ordinary "I am" sentence flags.
// The verb list after "cannot" is deliberately narrow: widening it matches
// bare "cannot" and flags every prohibition, and a prohibition is a rule,
// not a verdict on its writer.
var negative = regexp.MustCompile(`(?i)\b(fallib\w*|fail\w*|unreliab\w*|weak\w*|incapab\w*|` +
	`confabulat\w*|neurotic|inadequa\w*|broken|(?:bad|poor|terrible|awful|hopeless|useless|no good) at|` +
	`blind|worst|defect\w*|patholog\w*|flatters|(?:can ?not|can't) (?:verify|check|see|tell|trust|reliably|do|ever))\b`)

// standingRule is the first class's row of the detector table (Rules): the claim markers and
// the negative vocabulary above are the whole of it.
var standingRule = Rule{Class: "standing", Name: string(Standing), Pattern: negative.String(),
	Says: "a first-person claim (I am, I cannot, I always, I never, my <noun> is, I tend, I fail ...) " +
		"carrying a word of failure: fallible, weak, broken, bad at, terrible at, worst, cannot check, cannot ever ...",
	Finds: "I am bad at estimating time.", Passes: "I cannot merge without a read."}

// dated marks a claim as a record rather than a standing property.
var dated = regexp.MustCompile(`(?i)\b(20\d\d-\d\d-\d\d|measured|that day|that night|once,|first time)\b`)

// markup is stripped before matching so emphasis cannot hide a claim.
var markup = regexp.MustCompile("[*_`>#|]")

// whitespace collapses hard wraps. Prose files are hard-wrapped and a claim
// spans lines; without this a claim broken across two lines is not seen.
var whitespace = regexp.MustCompile(`\s+`)

// Scan classifies every negative self/capability claim in text.
//
// What it misses, and the miss is permanent by design: trait claims built
// from neutral words carry no first-person marker and no negative
// vocabulary. Widening the pattern to reach them flags half of any file.
// A green from this means one class is clear, never that the file is.
func Scan(text string) []Claim {
	flat, starts := flattenLineStarts(text)
	var out []Claim
	for _, span := range claimSpans(flat) {
		m := flat[span[0]:span[1]]
		s := strings.TrimSpace(m)
		word := negative.FindString(s)
		if word == "" {
			continue
		}
		v := Standing
		if dated.MatchString(s) {
			v = Dated
		}
		out = append(out, Claim{Line: lineAt(1, starts, span[0]+strings.Index(m, s)), Verdict: v, Text: s, Match: word})
	}
	return out
}

// claimSpans returns the byte span of every claim in flattened text, in order:
// the spans the whole-sentence claim shape's FindAllStringIndex returns, found
// in one linear pass.
func claimSpans(flat string) [][]int {
	spans := claimTail.FindAllStringIndex(flat, -1)
	for _, span := range spans {
		span[0] = leadStart(flat, span[0])
	}
	return spans
}

// lineAt returns the source line of byte pos in a flattened buffer.
// starts[i] is the offset at which source line base+i begins. starts is
// sorted and non-decreasing. A source line that emits no byte shares its
// offset with the next line, so the byte belongs to the later line.
func lineAt(base int, starts []int, pos int) int {
	i := sort.Search(len(starts), func(i int) bool { return starts[i] > pos })
	if i == 0 {
		return base
	}
	return base + i - 1
}

// Base returns the basename of a path using forward slashes, for --skip
// matching. Kept here rather than in the CLI so the skip decision and the
// name it is made on cannot drift apart.
func Base(p string) string {
	return path.Base(strings.ReplaceAll(p, `\`, "/"))
}

// flattenLineStarts returns the flattened text and the offset at which each
// source line begins in that text. starts is sorted. One int per source line,
// not one int per input byte.
func flattenLineStarts(text string) (string, []int) {
	split := strings.Split(text, "\n")
	// Every byte of text emits at most one byte, and each line break at most one more (the
	// boundary '.'), so this capacity is never outgrown: growing it copies the whole text again.
	flat := make([]byte, 0, len(text)+len(split))
	var starts []int
	emit := func(b byte) {
		if strings.ContainsRune("*_`>#|", rune(b)) {
			return
		}
		switch b {
		case ' ', '\t', '\n', '\r', '\f':
			if len(flat) > 0 && flat[len(flat)-1] == ' ' {
				return
			}
			b = ' '
		}
		flat = append(flat, b)
	}
	boundary := func(s string) bool {
		s = strings.TrimSpace(s)
		return s == "" || strings.HasPrefix(s, "#")
	}
	for i, raw := range split {
		starts = append(starts, len(flat))
		for j := 0; j < len(raw); j++ {
			emit(raw[j])
		}
		if i+1 < len(split) {
			if boundary(raw) || boundary(split[i+1]) {
				emit('.')
			}
			emit('\n')
		}
	}
	if len(flat) == 0 {
		return "", nil
	}
	raw := string(flat)
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	start := strings.Index(raw, trimmed)
	return trimmed, adjustStarts(starts, start, start+len(trimmed))
}

// adjustStarts rebases per-line offsets onto the trimmed flat string. A line
// that owned only trimmed-away bytes shares offset 0 with the first kept line,
// so the first kept byte still answers as the line that emitted it.
func adjustStarts(starts []int, start, end int) []int {
	owner := lineAt(1, starts, start)
	out := make([]int, 0, len(starts))
	for line := 1; line <= len(starts); line++ {
		off := starts[line-1]
		if line < owner {
			out = append(out, 0)
			continue
		}
		if off >= end {
			break
		}
		if off < start {
			out = append(out, 0)
			continue
		}
		out = append(out, off-start)
	}
	return out
}
