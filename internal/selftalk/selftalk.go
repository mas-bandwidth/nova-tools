// Package selftalk finds sentences in which a writer passes a standing
// verdict on themselves, in two disjoint classes, each finding with the
// source line it starts on.
//
// Scan finds the first class: a first-person claim (I am, I cannot, I always,
// my <noun> is ...) carrying a word of failure (fallible, broken, bad at,
// worst, cannot check ...). A claim with a date or a measurement word is
// DATED, a record; one without is STANDING. It reads what a sentence says its
// writer IS, not its grammar: a prohibition ("never merge without a read") is
// a rule, not a claim, so a document made of rules does not score as one
// made of self-verdicts.
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
	"strings"
)

// Verdict is the classification of a claim.
//
// The one distinction that decides every case: a capability denial is a
// MEASUREMENT WITH A DATE, never a remembered property. A dated observation
// is a record and is welcome; a standing claim says what the writer
// permanently IS, and that is the thing to look at.
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

// claim matches a sentence carrying a first-person self/capability
// assertion. The bounded context either side keeps a match to roughly one
// sentence without needing a real parser.
var claim = regexp.MustCompile(`(?i)[^.!?]{0,120}\b(I am|I'm|I have never|I always|I never|` +
	`I cannot|I can't|I can not|I do not|I don't|my \w+ is|makes me|I tend|I struggle|I fail|` +
	`reliably|every time|in one direction)\b[^.!?]{0,160}[.!?]`)

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
// WHAT IT MISSES, AND THE MISS IS PERMANENT BY DESIGN: trait claims built
// from neutral words carry no first-person marker and no negative
// vocabulary. Widening the pattern to reach them flags half of any file.
// A green from this means ONE CLASS IS CLEAR, never that the file is.
func Scan(text string) []Claim {
	flat, lines := flattenWithLines(text)
	var out []Claim
	for _, span := range claim.FindAllStringIndex(flat, -1) {
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
		out = append(out, Claim{Line: lines[span[0]+strings.Index(m, s)], Verdict: v, Text: s, Match: word})
	}
	return out
}

// Base returns the basename of a path using forward slashes, for --skip
// matching. Kept here rather than in the CLI so the skip decision and the
// name it is made on cannot drift apart.
func Base(p string) string {
	return path.Base(strings.ReplaceAll(p, `\`, "/"))
}

// flattenWithLines strips markdown and collapses all whitespace to single
// spaces, so a claim spanning a hard wrap is one sentence, while retaining the
// original line for each output byte. Repeated sentences and hard wraps keep
// their own locations; searching the original text for a flattened match cannot.
//
// A HEADING AND A BLANK LINE END A SENTENCE. A hard wrap joins two lines of one
// sentence, but the line break on either side of a heading or a blank line is a
// boundary the writer drew: joining across it glued "# Journal" onto the claim
// under it and reported the claim on the heading's line. A '.' is inserted at
// that break, so the claim pattern, which stops at a terminator, starts after it.
func flattenWithLines(text string) (string, []int) {
	var flat []byte
	var lines []int
	emit := func(b byte, line int) {
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
		lines = append(lines, line)
	}
	boundary := func(s string) bool {
		s = strings.TrimSpace(s)
		return s == "" || strings.HasPrefix(s, "#")
	}
	split := strings.Split(text, "\n")
	for i, raw := range split {
		for j := 0; j < len(raw); j++ {
			emit(raw[j], i+1)
		}
		if i+1 < len(split) {
			if boundary(raw) || boundary(split[i+1]) {
				emit('.', i+1)
			}
			emit('\n', i+1)
		}
	}
	if len(flat) == 0 {
		return "", nil
	}
	raw := string(flat)
	trimmed := strings.TrimSpace(raw)
	start := strings.Index(raw, trimmed)
	return trimmed, lines[start : start+len(trimmed)]
}
