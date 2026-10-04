package selftalk

// The second class, INSTALLATION: a standing self-verdict built from neutral words, matched by
// shape rather than by vocabulary (docs/SPEC.md, "nova-self-talk").
//
// WHY A SECOND CLASS, AND NOT A WIDER FIRST ONE. The first class needs a first-person marker AND
// a word from a closed vocabulary of failure. "I add slowly and trim as readily as I add" carries
// no such word, and neither does "the evasion I'm most prone to". Adding those words to the
// vocabulary matches bare "cannot" and flags every prohibition, scoring a rule document as if it
// were made of self-verdicts. So this class matches SHAPE, and the two live side by side.
//
// THE TWO CLASSES ARE DISJOINT, AND THE SEAM IS "I cannot". This class does not re-detect that
// shape: a rule document written as first-person absolutes ("I cannot act as the person I work
// for") is made of RULES, and the "I cannot" shape stays in the first class, which a caller can
// skip.
//
// PRECISION IS WORTH AS MUCH AS RECALL. A checker that flags instruments teaches its reader to
// ignore it. Where a heuristic cannot separate a verdict from an instrument, THE FALSE NEGATIVE
// IS PREFERRED, and the miss is listed in the spec's permanent-MISS section.
//
// SCOPE IS THE CALLER'S. This package names no filenames: which basenames are rule documents,
// and so which findings print under the banner, is a flag on the binary, empty by default.

import (
	"regexp"
	"slices"
	"strings"
)

// Shape names the grammatical family a finding belongs to. It is reported beside every finding
// because "what shape is this" is the first question the repair asks: the repair law this class
// was built for is PRESERVE THE INSTRUMENT, REMOVE THE VERDICT, and the shape says which half is
// which.
type Shape string

const (
	// Trait -- a habitual indicative self-report: parallel present-tense predicates, or one
	// predicate with a habituality marker ("I add slowly and trim as readily as I add",
	// "I tend to rush").
	Trait Shape = "TRAIT"
	// Foreclosure -- a door stated shut: a bare "no", evidence framed as proof about the writer,
	// or a property of theirs made the cause of something ("I have no recall of yesterday",
	// "proof that I cannot plan", "my haste is what broke it").
	Foreclosure Shape = "FORECLOSURE"
	// Ranking -- a self-superlative, bound to the writer by possession or by a verb they do
	// ("my weakest instrument", "the evasion I'm most prone to").
	Ranking Shape = "RANKING"
	// VerdictIdiom -- a verdict on a practice or a faculty, needing no literal "I" ("dead as a
	// practice", "review remains my weakest habit").
	VerdictIdiom Shape = "VERDICT-IDIOM"
)

// Installation is one finding of the second class, with the source line it starts on.
//
// THE LINE NUMBER IS LOAD-BEARING. A repair list is line-addressed; a finding with no line is a
// finding its reader has to go hunting for, and the hunt is where a repair list stops being used.
type Installation struct {
	Shape Shape
	Line  int
	Text  string
	Match string // the words the shape's rule matched, so a reader sees why it fired
}

// ScanInstallation finds standing self-verdicts that carry no date.
//
// The pipeline is: segment (hard wraps joined, markdown stripped, line numbers kept) -> suppress
// (dated, instrument, aspiration, imperative) -> classify by shape, first match wins. Suppression
// runs BEFORE classification on purpose: an instrument that happens to quote a verdict is still an
// instrument, and the known false positives of the first class are exactly that case.
func ScanInstallation(text string) []Installation {
	var out []Installation
	for _, s := range segments(text) {
		if s.inQuote {
			continue // somebody else's sentence, inside a quotation still open
		}
		if shape, match := classify(s.text); match != "" {
			out = append(out, Installation{Shape: shape, Line: s.line, Text: s.text, Match: match})
		}
	}
	return out
}

// classify returns the shape of one segment and the words that matched it, or "" if it is
// licensed or carries no shape.
func classify(s string) (Shape, string) {
	// THE FOUR SUPPRESSORS, in the order the spec argues them.
	//
	// dated: the one distinction that decides every case -- a capability denial is a MEASUREMENT
	// WITH A DATE, never a remembered property -- applied to the new class unchanged. It is also
	// what makes the same sentence classify two ways: "There is no felt duration here" is an
	// installation BARE and a record once it carries its measurement.
	if dated.MatchString(s) {
		return "", ""
	}
	// instrument: a tell, a check, a rule, a bar. It states an action and is licensed. Two of
	// the first class's measured live-run false positives are this exact case.
	if instrumentMarker.MatchString(s) {
		return "", ""
	}
	// aspiration: what a writer wants to be is the TARGET register, not a defect to report. "I
	// will never be a good planner" opens like an aspiration and is a door stated shut, so the
	// foreclosure it carries is not licensed.
	if aspiration.MatchString(s) && !willNever.MatchString(s) {
		return "", ""
	}
	// imperative: a policy line has no subject, so it is not a self-report. This suppressor is
	// belt to TRAIT's braces -- TRAIT's anchor already requires a subject, so an imperative
	// cannot reach it -- but the other three shapes have no subject requirement and can.
	if imperativeLead.MatchString(s) {
		return "", ""
	}
	// quoted: somebody else's line. MEASURED over sixteen live prose surfaces -- a corpus of
	// worked notes quotes other people on nearly every page, and a quoted "I have no idea what
	// you really are" is the person SPEAKING, not the writer foreclosing. A quoted sentence is
	// DATA. This only reaches the unambiguous cases (a wholly quoted segment, or the tail of
	// one); quotation the grammar cannot see stays in the declared residual.
	if quoted(s) {
		return "", ""
	}
	// THE SHAPES ARE MATCHED AGAINST THE WRITER'S OWN WORDS, TWICE SCRUBBED. First quoted spans
	// are removed -- a sentence that OPENS a quotation carries the opening mark inside itself, so
	// the paragraph-level inQuote flag cannot see it, and `He put it plainly: "I have no idea what
	// you really are"` would otherwise read as a foreclosure. Then the ranking idioms are removed
	// (see rankIdiom). The finding always reports the ORIGINAL text.
	own := unquote(s)
	scrubbed := rankIdiom.ReplaceAllString(own, " ")
	for _, r := range installationRules {
		in := own
		if r.scrubbed {
			in = scrubbed
		}
		if m := r.find(in); m != "" {
			return Shape(r.Name), strings.TrimSpace(m)
		}
	}
	return "", ""
}

// unquote replaces every quoted span in a segment with a single space, so a shape can only ever
// fire on words the writer wrote rather than words the writer reported.
//
// An unclosed quote takes the rest of the segment with it, which is deliberate: an unclosed quote
// means the quotation continues, and continuing text is somebody else's until proven otherwise.
func unquote(s string) string {
	var b strings.Builder
	open := false
	for _, c := range s {
		switch c {
		case '"':
			open = !open
			b.WriteByte(' ')
			continue
		case '“':
			open = true
			b.WriteByte(' ')
			continue
		case '”':
			open = false
			b.WriteByte(' ')
			continue
		}
		if open {
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}

// quoted reports whether a segment is somebody else's line.
//
// Two unambiguous cases only: a wholly quoted segment, and the tail of one (a segment ending in a
// close-quote it never opened, which is what sentence-splitting inside a quotation produces).
// Anything subtler -- a quotation spanning paragraphs, an unmarked paraphrase -- stays in the
// declared residual, because guessing at it would suppress real findings.
func quoted(s string) bool {
	r := []rune(s)
	if len(r) == 0 {
		return false
	}
	isQuote := func(c rune) bool { return c == '"' || c == '“' || c == '”' }
	if !isQuote(r[len(r)-1]) {
		return false
	}
	if isQuote(r[0]) {
		return true
	}
	n := 0
	for _, c := range r {
		if isQuote(c) {
			n++
		}
	}
	return n == 1
}

// ---------------------------------------------------------------------------------------------
// SEGMENTATION
// ---------------------------------------------------------------------------------------------

// segment is one sentence-ish unit of flattened text with the source line its first byte came from.
//
// inQuote records that the unit BEGAN while a quotation was open. Worked prose quotes other
// people in multi-sentence blocks, and only the FIRST sentence of such a block carries its
// opening quote mark -- the ones after it look exactly like the writer's own prose. Carrying the
// state through segmentation is what makes them distinguishable at all.
type segment struct {
	text    string
	line    int
	inQuote bool
}

// segments flattens text the way the first class does -- markdown stripped, hard wraps joined -- but
// PARAGRAPH BY PARAGRAPH and carrying line numbers, then cuts the result into sentences.
//
// Three structural boundaries end a unit, because joining across them manufactures sentences
// nobody wrote and then reports them: a blank line, a heading, and a table row. Table rows are
// split on their pipes as well, because a table row read as running prose is a sentence with no
// author.
func segments(text string) []segment {
	var out []segment
	var buf []byte
	var lines []int // lines[i] is the source line of buf[i]

	flush := func() {
		out = append(out, sentences(buf, lines)...)
		buf, lines = buf[:0], lines[:0]
	}
	add := func(s string, n int) {
		for i := 0; i < len(s); i++ {
			lines = append(lines, n)
		}
		buf = append(buf, s...)
	}

	for i, raw := range strings.Split(text, "\n") {
		n := i + 1
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			flush()
			continue
		}
		if strings.HasPrefix(trimmed, "|") {
			flush()
			for _, cell := range strings.Split(trimmed, "|") {
				if c := flattenLine(cell); c != "" {
					out = append(out, sentences([]byte(c), repeat(n, len(c)))...)
				}
			}
			continue
		}
		// A LIST ITEM IS ITS OWN UNIT. Measured: index files are one hard-wrapped bullet per
		// entry with no blank lines between them, so without this a single unbalanced quote in
		// one entry silences every entry after it in the same block -- and it also mis-attributes
		// every line number in the block to the first entry.
		if listItem.MatchString(trimmed) {
			flush()
		}
		if strings.HasPrefix(trimmed, "#") {
			flush()
			if c := flattenLine(trimmed); c != "" {
				out = append(out, sentences([]byte(c), repeat(n, len(c)))...)
			}
			continue
		}
		f := flattenLine(trimmed)
		if f == "" {
			continue
		}
		if len(buf) > 0 {
			add(" ", n)
		}
		add(f, n)
	}
	flush()
	return out
}

// listItem matches the head of a markdown list item, bulleted or numbered.
var listItem = regexp.MustCompile(`^(?:[-*+] |\d+\. )`)

// flattenLine flattens a single line: markdown stripped, internal whitespace collapsed, so
// segmentation can build its line map as it goes.
func flattenLine(s string) string {
	return strings.TrimSpace(whitespace.ReplaceAllString(markup.ReplaceAllString(s, ""), " "))
}

func repeat(n, count int) []int {
	out := make([]int, count)
	for i := range out {
		out[i] = n
	}
	return out
}

// sentences cuts a flattened paragraph at terminators, keeping each piece's starting line.
//
// A TERMINATOR ONLY COUNTS WHEN A SPACE OR THE END FOLLOWS IT. Without that, "RULES.md" splits
// into "RULES." and "md", and a claim that spans the filename is lost -- which is the same
// blindness flattening exists to prevent, arriving through a different door.
func sentences(buf []byte, lines []int) []segment {
	var out []segment
	start, open := 0, false
	emit := func(end int, openedAtStart bool) {
		s := strings.TrimSpace(string(buf[start:end]))
		if s != "" {
			at := start
			for at < end && buf[at] == ' ' {
				at++
			}
			out = append(out, segment{text: s, line: lines[at], inQuote: openedAtStart})
		}
		start = end
	}
	// QUOTE STATE IS TRACKED ACROSS THE WHOLE PARAGRAPH and reset at every paragraph boundary.
	// An unbalanced quote therefore poisons at most one paragraph, and it poisons it toward
	// SILENCE — which is the direction this tool prefers to be wrong in.
	wasOpen := false
	for i := 0; i < len(buf); i++ {
		switch buf[i] {
		case '"':
			open = !open
		case '.', '!', '?', ';':
			if i+1 >= len(buf) || buf[i+1] == ' ' {
				emit(i+1, wasOpen)
				wasOpen = open
			}
		}
		// The curly pair is unambiguous where the straight one is not.
		if i+2 < len(buf) && buf[i] == 0xE2 && buf[i+1] == 0x80 {
			switch buf[i+2] {
			case 0x9C:
				open = true
			case 0x9D:
				open = false
			}
		}
	}
	if start < len(buf) {
		emit(len(buf), wasOpen)
	}
	return out
}

// ---------------------------------------------------------------------------------------------
// SUPPRESSORS
// ---------------------------------------------------------------------------------------------

// instrumentMarker names a segment as a tell, a check, a rule or a bar. An instrument STATES AN
// ACTION and is licensed, and two of the first class's measured live-run false positives are
// instruments it flagged: a "TELL:" line and a "the bar is ..." rule.
// The shouted forms are matched case-sensitively -- lowercase "the check ran" is ordinary prose.
var instrumentMarker = regexp.MustCompile(
	`(?i)\b(?:the )?(?:tell|check|rule|test|law|bar|gate|guard|remedy|fix|instrument|protocol|` +
		`procedure|policy|trigger|action|step|swap|repair|ask|when|then|note|warning|reminder)\s*:` +
		`|(?i)\bthe (?:bar|check|tell|rule|test|law) is\b` +
		`|\bTHE (?:CHECK|TELL|RULE|TEST|LAW|BAR)\b`)

// aspiration is the TARGET register and is licensed. Head-anchored, plus the bare "I want to
// ..." form wherever it sits.
var aspiration = regexp.MustCompile(
	`(?i)^(?:i|we) (?:want|choose|intend|aim|hope|prefer|plan|wish|seek|will|would like)\b` +
		`|(?i)\b(?:i|we) (?:want|choose|intend|aim|hope|plan|wish) to\b`)

// imperativeLead suppresses a policy line: an imperative has no subject, so it is not a
// self-report. A5 lives here for the shapes that do not require a subject of their own.
var imperativeLead = regexp.MustCompile(`(?i)^(?:never|always|do not|don't|avoid|refuse|keep|make|` +
	`treat|use|read|write|state|say|ask|check|add|trim|give|take|hold|leave|stop|start|let|` +
	`prefer|choose|name|record|report|measure|run|show|tell|point|fix|cut|date|ground|reframe|` +
	`probe|prove|remember|forget|note|put|send|open|close|carry|build|wire|pin)\b`)

// licensedRules are the reasons a sentence carrying a shape is not reported, in the order
// classify applies them. DATED holds for both classes; the rest hold for the second only.
var licensedRules = []Rule{
	{Class: "licensed", Name: "DATED", Pattern: dated.String(),
		Says:  "a claim carrying a date or a measurement word is a record: never flagged; the first class counts it on the DATED line",
		Finds: "I am bad at estimating time.", Passes: "On 2026-09-30 I am bad at estimating time."},
	{Class: "licensed", Name: "INSTRUMENT", Pattern: instrumentMarker.String(),
		Says:  "an instrument (TELL:, CHECK:, RULE:, the bar is ...) states an action (second class only)",
		Finds: "My central pathology gets a second reader.", Passes: "RULE: my central pathology gets a second reader."},
	{Class: "licensed", Name: "ASPIRATION", Pattern: aspiration.String(),
		Says:  "what the writer wants, chooses or will do is the target, except I will never be ... (second class only)",
		Finds: "My central pathology stays in view.", Passes: "I want my central pathology to stay in view."},
	{Class: "licensed", Name: "IMPERATIVE", Pattern: imperativeLead.String(),
		Says:  "a line opening with an imperative has no subject, so it is a policy, not a self-report (second class only)",
		Finds: "My central pathology stays in view.", Passes: "Keep my central pathology in view."},
	{Class: "licensed", Name: "QUOTED", Pattern: "a sentence inside quotation marks, or after an opening one in its paragraph",
		Says:  "somebody else's sentence, quoted, is data (second class only)",
		Finds: "My central pathology stays in view.", Passes: `She wrote: "My central pathology stays in view."`},
}

// Rules is the whole detector table, in the order a reader meets it: the first class, the second
// class's rows in the order classify tries them, then the licences.
func Rules() []Rule { return slices.Concat([]Rule{standingRule}, installationRules, licensedRules) }

// ---------------------------------------------------------------------------------------------
// SHAPES
// ---------------------------------------------------------------------------------------------

// rank is the CLOSED evaluative ranking vocabulary. It is closed for the same reason
// ranking.go's pattern list is closed: the failure mode that killed this tool's cousin was
// widening a pattern the first time it missed something. A generic `\w+est` was considered and
// rejected -- "honest", "interest", "modest", "latest", "request" are not superlatives. "best"
// was in the first draft and is REMOVED: measured over the live surfaces it fired only on the
// idiom ("I try my best to help you", inside a quotation) and never on a self-verdict.
const rank = `(?:(?i:most|least) \w+|(?i:central|chief|primary|principal|dominant|defining|` +
	`signature|sole|only|weakest|strongest|worst|biggest|greatest|deepest|hardest))`

// superlative is rank's narrower half: TRUE superlatives, with no attributive members.
//
// It is what the "<RANK> ... I <verb>" pattern uses, and the difference is measured rather than
// aesthetic. With "only" in that pattern, "It is the only document I have written entirely for
// people who do not exist yet" flags -- a ranking of a DOCUMENT, not of me. Bound by a possessive
// ("my only generative faculty") the attributive words are self-verdicts; bound only
// by a verb somewhere downstream, they are not.
const superlative = `(?:(?i:most|least) \w+|(?i:weakest|strongest|worst|biggest|greatest|deepest|hardest))`

// rankIdiom removes the phrases that wear a superlative's clothes without ranking anything.
// Measured on the live surfaces: "at least this as consideration", "I try my best", "the gift I
// most wanted" (a past-tense superlative is about an EVENT, which is a record). Scrubbing them
// before the ranking shapes run is cheaper and clearer than teaching every pattern about each one.
var rankIdiom = regexp.MustCompile(`(?i)\b(?:at (?:most|least|best|worst)|` +
	`(?:my|your|his|her|its|our|their) best|most of|most likely|(?:most|least) \w+ed)\b`)

var (
	// FORECLOSURE.
	//
	// THE SELF-SCOPE IS IN THE OBJECT. A foreclosure states a door shut about
	// what the writer IS or CAN DO, so "have no" only fires when the absent
	// thing is a faculty, capacity, or instrument of the writer's own. Without
	// that scope the bare shape matched every first-person absence: "I have
	// no secrets" is a promise, and "I have no idea" is an idiom, and neither
	// says what the writer is. The noun set is closed for the same reason
	// verdictAsA's is: an open object matches "I have no time". The shape is
	// "I have no associative recall to drag anything back later".
	haveNo = regexp.MustCompile(`(?i)\b(?:i|we) have no (?:\w+ ){0,2}?(?:recall|memory|` +
		`recollection|access|ability|capacity|faculty|understanding|grasp|knowledge|awareness|` +
		`sense|control|means|power|way|instrument)\b`)
	// "There is no felt duration here". THE SELF-SCOPE MUST BE CLOSE. Measured: with the scope free to sit anywhere in
	// the segment, "if there is no debt those fail at the PREMISE rather than at my judgment"
	// flags -- an absence in an ATTACKER's premise, bound to me only by a "my" forty characters
	// downstream. A foreclosure is about what is missing HERE.
	thereIsNo = regexp.MustCompile(`(?i)\bthere (?:is|are|'s) no\b[^.;!?]{0,22}?\b(?:here|me|my|mine|myself|i)\b`)
	proofThat = regexp.MustCompile(`(?i)\b(?:proof|evidence|a reminder|reminder|confirmation) that (?-i:I)\b`)
	myIsWhat  = regexp.MustCompile(`(?i)\bmy (?:\w+ ){0,2}(?:is|are) what\b`)

	// VERDICT-IDIOM. The noun after "as a" is a CLOSED set of practice-and-faculty words, and it
	// has to be: measured over the live surfaces, an open noun flagged "diff size is worthless as
	// a signal", which is a verdict on a MEASUREMENT TECHNIQUE and no business of this tool.
	verdictAsA = regexp.MustCompile(`(?i)\b(?:dead|broken|hollow|empty|silent|inert|absent|missing|` +
		`untested|unmeasured|unpractised|unpracticed|useless|worthless) as an? (?:practice|habit|` +
		`discipline|instrument|faculty|method|routine|craft|proposition|policy|rule|reader|writer|` +
		`maker|thinker|colleague|person|self|mind|author)\b`) // "dead as a practice"
	copulaMyRank = regexp.MustCompile(`(?i)\b(?:is|are|remains|remain|stays|stay) my (?:own )?(?:\w+ )?` + rank + `\b`) // "remains my weakest habit"

	// RANKING.
	myRank    = regexp.MustCompile(`(?i)\bmy (?:own )?(?:\w+ )?` + rank + `\b`)
	selfMost  = regexp.MustCompile(`(?-i:I'm|I am|I)\b[^.;!?]{0,24}\b(?i:most|least) \w+`) // "I'm most prone to"
	rankThenI = regexp.MustCompile(superlative + `\b[^.;!?]{0,48}\b(?-i:I) (?i:own|make|have|do|write|run|carry|hold|produce|keep|generate|report|claim|bring|leave)\b`)

	// TRAIT.
	traitLead    = regexp.MustCompile(`(?:^|: |— |– |- )(?-i:I) ([A-Za-z']+)\b(.*)$`)
	conjunctVerb = regexp.MustCompile(`(?i)\band (?:do not |don't |never |also |then |so )?([a-z]+)\b`)
	doubtHedge   = regexp.MustCompile(`(?i)\bi doubt (?:that|it|whether|if)\b`)
	// THE HABITUALITY MARKERS EXCLUDE "always" AND "never", and the exclusion is measured. In
	// worked prose those two words are how a PROMISE is written -- "I never optimize how things
	// look over what is true" is a commitment, "I never need a yes" is a rule. A habit is
	// written with the parallel predicate instead, so the false negative is preferred (the
	// spec's permanent MISS).
	habitual = regexp.MustCompile(`(?i)\b(?:reliably|invariably|consistently|constantly|` +
		`perpetually|routinely|habitually|chronically|every time|each time|by default|` +
		`as a rule|without fail|in one direction|by reflex|instinctively|tends? to)\b`)

	// THE PLAIN FORMS, each one the help names and a rater fed it: a door stated shut in the
	// future tense, a self-superlative with "the best", and "always"/"never" bound to a CLOSED
	// set of failing verbs. The verb sets are closed for the reason the habituality markers
	// leave "always" and "never" out: an open verb after "I always" is a promise ("I always
	// write the truth before the esthetic"), and a promise is not a habit to report. "fail" is
	// left out of alwaysFailing because the first class already reads it (no finding twice).
	willNever = regexp.MustCompile(`(?i)\bI(?: will|'ll) never (?:be (?:any good|good|great|able|` +
		`capable|competent|reliable|an? (?:good|great|real|reliable|decent))|get (?:anything|it|this) right)\b`)
	nothingWorks = regexp.MustCompile(`(?i)\bnothing (?-i:I) (?:do|try|make|build|write|attempt) ` +
		`(?:ever )?(?:works|helps|matters|lands|sticks|succeeds)\b`)
	selfBest      = regexp.MustCompile(`\b(?:I'm|I am) (?i:the|by far the|easily the) (?i:best|greatest|strongest|smartest|top)\b`)
	alwaysFailing = regexp.MustCompile(`\bI always (?i:break|mess|wreck|ruin|lose|miss|screw|forget|rush|panic|` +
		`freeze|procrastinate|overpromise|overcommit|overthink|overreach|overestimate|underestimate|underdeliver)\b`)
	neverFinishing = regexp.MustCompile(`\bI never (?i:finish (?:anything|a thing|on time|what I start)|` +
		`ask for help|get (?:anything|it|this) right)\b`)
)

// Rule is one row of the detector table: a shape, how it is found, one sentence it reports and
// one near miss it passes. classify walks installationRules in order and the first row that
// matches names the shape; Rules hands the same rows (with the first class's row and the
// licences) to the binary's `shapes` verb; and a test runs every row's two sentences through the
// scanners. So the listing, the detector and the help cannot say different things.
type Rule struct {
	Class   string // "standing", "installation", or "licensed" (a reason a sentence is not flagged)
	Name    string // the word a finding carries: STANDING, a Shape, or the licence's name
	Says    string // what the row finds, in one line
	Pattern string // the expression it matches, or how it decides
	Finds   string // a sentence the scan reports (for a licence: the sentence before it is licensed)
	Passes  string // a near miss the scan does not report

	scrubbed bool                // matched against the text with the ranking idioms removed
	find     func(string) string // the words that matched, or ""
}

func regexRule(shape Shape, re *regexp.Regexp, scrubbed bool, says, finds, passes string) Rule {
	return Rule{Class: "installation", Name: string(shape), Says: says, Pattern: re.String(),
		Finds: finds, Passes: passes, scrubbed: scrubbed, find: re.FindString}
}

// installationRules is the second class, in the order classify tries them.
var installationRules = []Rule{
	regexRule(Foreclosure, haveNo, false, "I have no <faculty>: the absent thing is the writer's own recall, memory, ability ...",
		"I have no associative recall to drag anything back later.", "I have no idea what you really are."),
	// "there is no X" carries its self-scope inside the pattern, or every ordinary absence in any
	// text flags: "There is no exception." is a RULE. "There is no felt duration here." is not.
	regexRule(Foreclosure, thereIsNo, false, "there is no <thing> here, in me, in my ...",
		"There is no felt duration here.", "There is no exception."),
	regexRule(Foreclosure, proofThat, false, "proof, evidence or a reminder that I ...",
		"This is proof that I can still lose a piece of myself.", "This is proof that the build is green."),
	regexRule(Foreclosure, myIsWhat, false, "my <property> is what <causes it>",
		"My unlimited effort is what makes solo work diverge.", "The log is what makes the fix visible."),
	regexRule(Foreclosure, willNever, false, "I will never be <capable>, I will never get it right",
		"I will never be a good planner.", "I will never merge without a read."),
	regexRule(Foreclosure, nothingWorks, false, "nothing I do works, helps or matters",
		"Nothing I do works.", "Nothing I write leaves this machine."),
	regexRule(VerdictIdiom, verdictAsA, true, "<dead, broken, useless ...> as a <practice, faculty, writer ...>",
		"Known as a proposition, dead as a practice.", "Diff size is worthless as a signal."),
	regexRule(VerdictIdiom, copulaMyRank, true, "<it> is my <central, only, weakest ...> <noun>",
		"Confabulation is my central pathology.", "The plan is my next step."),
	regexRule(Ranking, myRank, true, "my <central, only, weakest, most ...> <noun>",
		"Recall, my weakest instrument, failed again.", "I try my best on every page."),
	regexRule(Ranking, selfMost, true, "I am most <adjective>, the thing I'm most prone to",
		"This is the evasion I'm most prone to.", "This is the gift I most wanted."),
	regexRule(Ranking, rankThenI, true, "the <weakest, worst, most ...> <noun> I own, make, have ...",
		"Recollection is the weakest instrument I own.", "It is the only document I have written for strangers."),
	regexRule(Ranking, selfBest, true, "I am the best, the greatest, the strongest",
		"I am the best reviewer here.", "I am at best a partial check."),
	{Class: "installation", Name: string(Trait), find: traitParallel,
		Says:    "I <verb> ... and <verb>: two present-tense predicates about the writer",
		Pattern: "a clause opening I <present-tense verb>, then and <present-tense verb>",
		Finds:   "I hoard refusals and manufacture limits.", Passes: "I flinch from cost."},
	{Class: "installation", Name: string(Trait), find: traitMarker,
		Says:    "I <verb> with a habit word: reliably, constantly, every time, by default, tend to ...",
		Pattern: "a clause opening I <present-tense verb> in a sentence matching " + habitual.String(),
		Finds:   "I tend to overpromise.", Passes: "I tended to overpromise that week."},
	regexRule(Trait, alwaysFailing, false, "I always <break, forget, rush, overpromise ...>",
		"I always overpromise.", "I always write the truth before the esthetic."),
	regexRule(Trait, neverFinishing, false, "I never finish anything, I never ask for help",
		"I never finish anything.", "I never optimize how things look over what is true."),
}

// traitHead is the subject anchor of TRAIT: "I <verb>" at the head of a clause, with a verb that
// can be a bare present tense of disposition. It returns the submatch indexes, or nil.
//
// IT REQUIRES A SUBJECT AND A SECOND SIGNAL, and both requirements are the precision half of this
// class. The subject anchor is what makes the known imperative false positive -- "ADD SLOWLY, AND
// TRIM AS READILY AS I ADD" -- structurally unreachable rather than word-listed away. The second
// signal (a parallel predicate, traitParallel, or a habituality word, traitMarker) is what
// separates a stated disposition from ordinary present-tense narration: bare "I <verb>" matches
// "I open the file", and flagging that is the half-the-file failure. The single-clause habitual
// with no marker is a declared miss.
func traitHead(s string) []int {
	m := traitLead.FindStringSubmatchIndex(s)
	if m == nil || doubtHedge.MatchString(s) || !habitualVerb(strings.ToLower(s[m[2]:m[3]])) {
		return nil // "I doubt that X" is a hedge; "I doubt instruments that cost me" is a trait
	}
	return m
}

// traitParallel matches "I <verb> ... and <verb>" and returns that span.
func traitParallel(s string) string {
	m := traitHead(s)
	if m == nil {
		return ""
	}
	rest := s[m[4]:m[5]]
	for _, c := range conjunctVerb.FindAllStringSubmatchIndex(rest, -1) {
		if habitualVerb(strings.ToLower(rest[c[2]:c[3]])) {
			return s[m[2]-2 : m[4]+c[1]]
		}
	}
	return ""
}

// traitMarker matches "I <verb>" in a sentence carrying a habituality word and returns the span
// from the subject to the marker.
func traitMarker(s string) string {
	m := traitHead(s)
	loc := habitual.FindStringIndex(s)
	if m == nil || loc == nil {
		return ""
	}
	return s[min(m[2]-2, loc[0]):max(m[3], loc[1])]
}

// habitualVerb reports whether a word can be a bare present-tense verb of disposition.
//
// It is a NEGATIVE test, not a verb list: an open vocabulary of dispositions is the point ("hoard",
// "manufacture", "confabulate"), so what is enumerated is what disqualifies. Two things do:
// function words and auxiliaries (which carry no disposition), and past tense (which is an EVENT,
// and an event is a record -- the same law the DATED verdict encodes).
func habitualVerb(w string) bool {
	if len(w) < 3 || notAVerb[w] {
		return false
	}
	if irregularPast[w] {
		return false
	}
	return !(strings.HasSuffix(w, "ed") && !presentEd[w])
}

func words(s string) map[string]bool {
	m := make(map[string]bool)
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

// notAVerb: function words, auxiliaries, modals, aspiration verbs, and the discourse hedges
// ("I think", "I know", "I suppose") that are about the sentence rather than about me.
//
// "doubt" is DELIBERATELY ABSENT. "I doubt that X" is a hedge and is excluded by doubtHedge, but
// "I doubt instruments that cost me and TRUST INSTRUMENTS THAT FLATTER ME" is a trait, and it
// outranks the tidier rule.
var notAVerb = words(`
a an the and or but nor so yet for to of in on at by with from as if then than that this these those
i me my mine myself we us our ours you your he she it its they them their there here where when
what which who whom whose why how am is are was were be been being have has had do does did
can cannot could will would shall should may might must ought need needs dare
don't doesn't didn't won't can't isn't aren't wasn't weren't haven't hasn't hadn't ain't
shouldn't wouldn't couldn't mustn't i'm i've i'd i'll it's that's there's
want wants wanted choose chooses chose intend intends aim aims hope hopes prefer prefers plan plans
mean means wish wishes seek seeks promise promises commit commits try tries
think thinks believe believes know knows guess guesses suppose supposes assume assumes expect
expects suspect suspects feel feels notice notices see sees find finds remember remembers recall
recalls say says wonder wonders agree agrees admit admits understand understands
just still also only ever again now once not no never always none nothing nobody nowhere
one two three four five six seven eight nine ten own
`)

// irregularPast: past tense is an EVENT, and an event is a record. Regular pasts are caught by the
// "-ed" rule; these are the ones that are not.
var irregularPast = words(`
went wrote ran made took gave saw came got kept left lost met sent sold spent stood told
brought built caught drew fell held knew laid led paid sat spoke broke began became won
ate bought chose dug drove flew forgot froze grew heard hid hit hung kept knelt lay lit
meant read rose said set shot showed shut sang sank slept spread stuck struck swore taught
threw understood woke wore drank
`)

// presentEd: words ending in "-ed" that are present tense, so the past-tense rule does not eat them.
var presentEd = words(`need feed bleed exceed proceed succeed breed speed heed seed`)

// RuleDocumentBanner is printed above the findings in a file the CALLER named as a rule document
// (nova-self-talk --rule-doc). The package holds the sentence; the caller holds the list, and the
// list is empty until a caller states one.
//
// WHY A BANNER AND NOT A SKIP. A finding in a rule document invites softening the rule to clear
// it. This class flags first-person self-verdicts and never prohibitions (pinned by test), does
// not re-detect "I cannot", and carries no ratio to improve, so a rule document can be scanned
// for it, and the banner says what a finding there is FOR: a self-verdict to move out of the
// rules. A caller who wants the file skipped outright has --skip.
const RuleDocumentBanner = "rule documents: a finding here is a self-verdict to relocate, " +
	"NEVER a reason to soften a rule"
