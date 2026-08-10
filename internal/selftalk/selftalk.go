// Package selftalk finds first-person claims about what the writer of a text
// permanently IS or permanently CANNOT do, and classifies each as a dated
// record or a standing claim.
//
// WHY IT MEASURES A CONSTRUCT AND NOT GRAMMAR. Its predecessor counted
// negation words and called the ratio "negative self talk". That measured
// SYNTAX: a rule document is a list of things that must not happen, so it
// scored worst of anything in the repo it was written for, and improving its
// score meant deleting a prohibition. That output was acted on: five rules
// were weakened, one of them floor-level, before a cold reader caught every
// one. Restoring them made the score worse.
//
//	THE KERNEL GOT STRONGER AND THE TOOL GOT REDDER.
//	"Never" is not negative self talk. "I am fallible" is.
//
// So this measures the construct instead: a prohibition is only a rule; the
// sentences worth looking at are the ones that say what their writer IS.
//
// WHAT IT MISSES, AND THE MISS IS PERMANENT BY DESIGN: trait claims built
// from neutral words carry no first-person marker and no negative
// vocabulary. Widening the pattern to reach them flags half of any file, so
// the two classes cannot be one tool. A green from this package means ONE
// CLASS IS CLEAR, never that the file is — and the CLI says so on every run.
//
// It is NOT obsoleted by the register improving. A falling score means the
// input got better, which is the tool working, not the tool finishing.
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
	Verdict Verdict
	Text    string
}

// claim matches a sentence carrying a first-person self/capability
// assertion. The bounded context either side keeps a match to roughly one
// sentence without needing a real parser.
var claim = regexp.MustCompile(`(?i)[^.!?]{0,120}\b(I am|I'm|I have never|I always|I never|` +
	`I cannot|I can't|I do not|I don't|my \w+ is|makes me|I tend|I struggle|I fail|` +
	`reliably|every time|in one direction)\b[^.!?]{0,160}[.!?]`)

// negative is the vocabulary that turns a first-person assertion into a
// claim worth looking at. Without this filter every ordinary "I am" sentence
// flags — which is the predecessor's disease. The verb list after "cannot"
// is deliberately narrow: widening it matches bare "cannot" and flags every
// prohibition, and scoring prohibitions is exactly what got rules weakened.
var negative = regexp.MustCompile(`(?i)\b(fallib\w*|fail\w*|unreliab\w*|weak\w*|incapab\w*|` +
	`confabulat\w*|neurotic|inadequa\w*|broken|bad at|poor at|blind|worst|defect\w*|` +
	`patholog\w*|flatters|cannot (?:verify|check|see|tell|trust|reliably|do))\b`)

// dated marks a claim as a record rather than a standing property.
var dated = regexp.MustCompile(`(?i)\b(20\d\d-\d\d-\d\d|measured|that day|that night|once,|first time)\b`)

// markup is stripped before matching so emphasis cannot hide a claim.
var markup = regexp.MustCompile("[*_`>#|]")

// whitespace collapses hard wraps. Prose files are hard-wrapped and a claim
// spans lines; without this the tool is blind to both regression cases that
// occasioned it.
var whitespace = regexp.MustCompile(`\s+`)

// Flatten strips markdown and collapses all whitespace to single spaces.
// Exported because a text check that matches against unflattened text is
// blind to any claim spanning a hard wrap, and a shared implementation is
// one true source for that fix.
func Flatten(text string) string {
	return strings.TrimSpace(whitespace.ReplaceAllString(markup.ReplaceAllString(text, ""), " "))
}

// Scan classifies every negative self/capability claim in text.
//
// WHAT IT MISSES, AND THE MISS IS PERMANENT BY DESIGN: trait claims built
// from neutral words carry no first-person marker and no negative
// vocabulary. Widening the pattern to reach them flags half of any file.
// A green from this means ONE CLASS IS CLEAR, never that the file is.
func Scan(text string) []Claim {
	flat := Flatten(text)
	var out []Claim
	for _, m := range claim.FindAllString(flat, -1) {
		s := strings.TrimSpace(m)
		if !negative.MatchString(s) {
			continue
		}
		v := Standing
		if dated.MatchString(s) {
			v = Dated
		}
		out = append(out, Claim{Verdict: v, Text: s})
	}
	return out
}

// Base returns the basename of a path using forward slashes, for --skip
// matching. Kept here rather than in the CLI so the skip decision and the
// name it is made on cannot drift apart.
func Base(p string) string {
	return path.Base(strings.ReplaceAll(p, `\`, "/"))
}
