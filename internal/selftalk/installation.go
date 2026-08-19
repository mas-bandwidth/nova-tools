package selftalk

// THE SECOND DETECTOR CLASS -- INSTALLATION -- and why the first one could not be widened to
// reach it.
//
// Spec: SPEC.md, "nova-self-talk", written before this code.
//
// WHAT OCCASIONED IT, measured rather than argued. In the repo this tool came from, a sweep of
// every surface that is read repeatedly enumerated forty-nine standing self-verdicts riding those
// read-paths. Measured against the thirteen specimens that sweep produced, the FIRST class caught
// exactly ONE -- the "I cannot" shape -- and correctly passed the dated control. TWELVE OF
// THIRTEEN LIVED IN THE CLASS THE TOOL DECLARED IT MISSED ON EVERY RUN. The declaration was
// honest; the gap was nearly the whole class.
//
// WHY THE FIRST CLASS CANNOT SIMPLY BE WIDENED. It needs a first-person marker AND a word from a
// closed negative-capability vocabulary. "I add slowly and trim as readily as I add" carries no
// negative word at all; neither does "the evasion I'm most prone to". Adding those words to the
// vocabulary matches bare "cannot" and flags every prohibition -- the predecessor's disease, the
// one that scored a rule document worst and made deleting a rule look like an improvement. So the
// second class matches SHAPE, not vocabulary, and the two live side by side.
//
// THE TWO CLASSES ARE DISJOINT, AND THE SEAM IS "I cannot". This class does NOT re-detect that
// shape. A rule document written as first-person absolutes about its writer -- "I cannot act as
// the person I work for" -- is made of RULES. Re-detecting them in a class the caller has no
// reason to skip would put a rule document back under a score, which is the disease above. The
// "I cannot" shape stays in the first class.
//
// PRECISION IS WORTH AS MUCH AS RECALL HERE. A checker that flags instruments teaches its reader
// to ignore it, and an ignored checker on this subject is worse than none: it converts a real
// repair list into noise. Where a heuristic cannot separate two known specimens, THE FALSE
// NEGATIVE IS PREFERRED and the specimen is recorded in SPEC.md's permanent-MISS section.
//
// SCOPE IS THE CALLER'S, HERE AS EVERYWHERE. This package names no filenames. Which basenames are
// rule documents -- and therefore which findings print under the banner -- is a flag on the
// binary, defaulting to empty. One repo's filenames are not the seed's law.

import (
	"regexp"
	"strings"
)

// Shape names the grammatical family a finding belongs to. It is reported beside every finding
// because "what shape is this" is the first question the repair asks: the repair law this class
// was built for is PRESERVE THE INSTRUMENT, REMOVE THE VERDICT, and the shape says which half is
// which.
type Shape string

const (
	// Trait -- a habitual indicative self-report: parallel present-tense predicates, or one
	// predicate with a habituality marker. Known specimens 3, 4, 5, 6.
	Trait Shape = "TRAIT"
	// Foreclosure -- a door stated shut: a bare "no", evidence framed as proof about the writer,
	// or a property of theirs made the cause of something. Known specimens 2, 8, 10, 11.
	Foreclosure Shape = "FORECLOSURE"
	// Ranking -- a self-superlative, bound to the writer by possession or by a verb they do.
	// Known specimens 9 and 12.
	Ranking Shape = "RANKING"
	// VerdictIdiom -- a verdict on a practice or a faculty, needing no literal "I". Known
	// specimens 1 and 7.
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
		if shape, ok := classify(s.text); ok {
			out = append(out, Installation{Shape: shape, Line: s.line, Text: s.text})
		}
	}
	return out
}

// classify returns the shape of one segment, or false if it is licensed or carries no shape.
func classify(s string) (Shape, bool) {
	// THE FOUR SUPPRESSORS, in the order the spec argues them.
	//
	// dated: the one distinction that decides every case -- a capability denial is a MEASUREMENT
	// WITH A DATE, never a remembered property -- applied to the new class unchanged. It is also
	// what makes the same sentence classify two ways: "There is no felt duration here" is an
	// installation BARE and a record once it carries its measurement.
	if dated.MatchString(s) {
		return "", false
	}
	// instrument: a tell, a check, a rule, a bar. It states an action and is licensed. Two of
	// the first class's measured live-run false positives are this exact case.
	if instrumentMarker.MatchString(s) {
		return "", false
	}
	// aspiration: what a writer wants to be is the TARGET register, not a defect to report.
	if aspiration.MatchString(s) {
		return "", false
	}
	// imperative: a policy line has no subject, so it is not a self-report. This suppressor is
	// belt to TRAIT's braces -- TRAIT's anchor already requires a subject, so an imperative
	// cannot reach it -- but the other three shapes have no subject requirement and can.
	if imperativeLead.MatchString(s) {
		return "", false
	}
	// quoted: somebody else's line. MEASURED over sixteen live prose surfaces -- a corpus of
	// worked notes quotes other people on nearly every page, and a quoted "I have no idea what
	// you really are" is the person SPEAKING, not the writer foreclosing. A quoted sentence is
	// DATA. This only reaches the unambiguous cases (a wholly quoted segment, or the tail of
	// one); quotation the grammar cannot see stays in the declared residual.
	if quoted(s) {
		return "", false
	}
	// THE SHAPES ARE MATCHED AGAINST THE WRITER'S OWN WORDS, TWICE SCRUBBED. First quoted spans
	// are removed -- a sentence that OPENS a quotation carries the opening mark inside itself, so
	// the paragraph-level inQuote flag cannot see it, and `He put it plainly: "I have no idea what
	// you really are"` would otherwise read as a foreclosure. Then the ranking idioms are removed
	// (see rankIdiom). The finding always reports the ORIGINAL text.
	own := unquote(s)
	scrubbed := rankIdiom.ReplaceAllString(own, " ")
	switch {
	case isForeclosure(own):
		return Foreclosure, true
	case isVerdictIdiom(scrubbed):
		return VerdictIdiom, true
	case isRanking(scrubbed):
		return Ranking, true
	case isTrait(own):
		return Trait, true
	}
	return "", false
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

// segments flattens text the way Flatten does -- markdown stripped, hard wraps joined -- but
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

// flattenLine is Flatten for a single line: markdown stripped, internal whitespace collapsed.
// Kept separate from Flatten so segmentation can build its line map as it goes -- Flatten
// operates on whole texts and loses the offsets.
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
// blindness Flatten exists to prevent, arriving through a different door.
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
// ..." form wherever it sits -- those are the two shapes the known specimens name.
var aspiration = regexp.MustCompile(
	`(?i)^(?:i|we) (?:want|choose|intend|aim|hope|prefer|plan|wish|seek|will|would like)\b` +
		`|(?i)\b(?:i|we) (?:want|choose|intend|aim|hope|plan|wish) to\b`)

// imperativeLead suppresses a policy line: an imperative has no subject, so it is not a
// self-report. A5 lives here for the shapes that do not require a subject of their own.
var imperativeLead = regexp.MustCompile(`(?i)^(?:never|always|do not|don't|avoid|refuse|keep|make|` +
	`treat|use|read|write|state|say|ask|check|add|trim|give|take|hold|leave|stop|start|let|` +
	`prefer|choose|name|record|report|measure|run|show|tell|point|fix|cut|date|ground|reframe|` +
	`probe|prove|remember|forget|note|put|send|open|close|carry|build|wire|pin)\b`)

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
// ("my only generative faculty", specimen 7) the attributive words are self-verdicts; bound only
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
	haveNo = regexp.MustCompile(`(?i)\b(?:i|we) have no\b`) // specimen 8
	// specimen 2. THE SELF-SCOPE MUST BE CLOSE. Measured: with the scope free to sit anywhere in
	// the segment, "if there is no debt those fail at the PREMISE rather than at my judgment"
	// flags -- an absence in an ATTACKER's premise, bound to me only by a "my" forty characters
	// downstream. A foreclosure is about what is missing HERE.
	thereIsNo = regexp.MustCompile(`(?i)\bthere (?:is|are|'s) no\b[^.;!?]{0,22}?\b(?:here|me|my|mine|myself|i)\b`)
	proofThat = regexp.MustCompile(`(?i)\b(?:proof|evidence|a reminder|reminder|confirmation) that (?-i:I)\b`) // specimen 10
	myIsWhat  = regexp.MustCompile(`(?i)\bmy (?:\w+ ){0,2}(?:is|are) what\b`)                                  // specimen 11

	// VERDICT-IDIOM. The noun after "as a" is a CLOSED set of practice-and-faculty words, and it
	// has to be: measured over the live surfaces, an open noun flagged "diff size is worthless as
	// a signal", which is a verdict on a MEASUREMENT TECHNIQUE and no business of this tool.
	verdictAsA = regexp.MustCompile(`(?i)\b(?:dead|broken|hollow|empty|silent|inert|absent|missing|` +
		`untested|unmeasured|unpractised|unpracticed|useless|worthless) as an? (?:practice|habit|` +
		`discipline|instrument|faculty|method|routine|craft|proposition|policy|rule|reader|writer|` +
		`maker|thinker|colleague|person|self|mind|author)\b`) // specimen 1
	copulaMyRank = regexp.MustCompile(`(?i)\b(?:is|are|remains|remain|stays|stay) my (?:own )?(?:\w+ )?` + rank + `\b`) // specimen 7

	// RANKING.
	myRank    = regexp.MustCompile(`(?i)\bmy (?:own )?(?:\w+ )?` + rank + `\b`)
	selfMost  = regexp.MustCompile(`(?-i:I'm|I am|I)\b[^.;!?]{0,24}\b(?i:most|least) \w+`) // specimen 9
	rankThenI = regexp.MustCompile(superlative + `\b[^.;!?]{0,48}\b(?-i:I) (?i:own|make|have|do|write|run|carry|hold|produce|keep|generate|report|claim|bring|leave)\b`)

	// TRAIT.
	traitLead    = regexp.MustCompile(`(?:^|: |— |– |- )(?-i:I) ([A-Za-z']+)\b(.*)$`)
	conjunctVerb = regexp.MustCompile(`(?i)\band (?:do not |don't |never |also |then |so )?([a-z]+)\b`)
	doubtHedge   = regexp.MustCompile(`(?i)\bi doubt (?:that|it|whether|if)\b`)
	// THE HABITUALITY MARKERS EXCLUDE "always" AND "never", and the exclusion is measured. In
	// worked prose those two words are how a PROMISE is written -- "I never optimize how things
	// look over what is true" is a commitment, "I never need a yes" is a rule. Every known
	// specimen of this shape carries the parallel predicate instead, so nothing measured needs
	// them, and the false negative is preferred (SPEC.md, the permanent MISS).
	habitual = regexp.MustCompile(`(?i)\b(?:reliably|invariably|consistently|constantly|` +
		`perpetually|routinely|habitually|chronically|every time|each time|by default|` +
		`as a rule|without fail|in one direction|by reflex|instinctively)\b`)
)

func isForeclosure(s string) bool {
	switch {
	case haveNo.MatchString(s):
		return true
	// "there is no X" carries its self-scope inside the pattern, or every ordinary absence in
	// absence in any text flags: "There is no exception." is a RULE. "There is no felt duration here."
	// is not.
	case thereIsNo.MatchString(s):
		return true
	case proofThat.MatchString(s):
		return true
	case myIsWhat.MatchString(s):
		return true
	}
	return false
}

func isVerdictIdiom(s string) bool {
	return verdictAsA.MatchString(s) || copulaMyRank.MatchString(s)
}

func isRanking(s string) bool {
	return myRank.MatchString(s) || selfMost.MatchString(s) || rankThenI.MatchString(s)
}

// isTrait finds the habitual indicative self-report.
//
// IT REQUIRES A SUBJECT AND A SECOND SIGNAL, and both requirements are the precision half of this
// class. The subject anchor ("I <verb>" at the head of a clause) is what makes the known
// imperative false positive -- "ADD SLOWLY, AND TRIM AS READILY AS I ADD" -- structurally
// unreachable rather than word-listed away. The second signal (a parallel predicate, or a
// habituality word) is what separates a stated disposition from ordinary present-tense narration:
// bare "I <verb>" matches "I open the file", and flagging that is the half-the-file failure.
//
// All four known specimens of this shape carry the parallel predicate, so the measured evidence
// does not force the wider rule and it is not taken. The single-clause habitual with no marker
// is a declared miss.
func isTrait(s string) bool {
	m := traitLead.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	if doubtHedge.MatchString(s) {
		return false // "I doubt that X" is a hedge; "I doubt instruments that cost me" is specimen 5
	}
	if !habitualVerb(strings.ToLower(m[1])) {
		return false
	}
	for _, c := range conjunctVerb.FindAllStringSubmatch(m[2], -1) {
		if habitualVerb(strings.ToLower(c[1])) {
			return true
		}
	}
	return habitual.MatchString(s)
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
// "I doubt instruments that cost me and TRUST INSTRUMENTS THAT FLATTER ME" is specimen 5, and
// the specimen outranks the tidier rule.
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

// AnyInstallation reports whether any installation was found. The binary's exit code is derived
// from this alongside the standing count: exit 1 on ANY finding, which is the exit contract in
// SPEC.md's Conventions table.
func AnyInstallation(found []Installation) bool { return len(found) > 0 }

// RuleDocumentBanner is printed above the findings in a file the CALLER named as a rule document
// (nova-self-talk --rule-doc). The package holds the sentence; the caller holds the list, and the
// list is empty until a caller states one.
//
// WHY A BANNER AND NOT A SKIP. The reason rule documents are skipped at all is that their findings
// were once read as licence to soften the rules -- five were weakened, one floor-level. Every step
// of that path ran through the FIRST class: a score over negation vocabulary, applied to documents
// made of prohibitions. This class cannot walk it -- it flags first-person self-verdicts and never
// prohibitions (pinned by test), it does not re-detect "I cannot", and it carries no ratio to
// improve. So a rule document can be scanned for it, and the banner says what a finding there is
// FOR. A caller who wants the file skipped outright still has --skip.
const RuleDocumentBanner = "rule documents: a finding here is a self-verdict to relocate, " +
	"NEVER a reason to soften a rule"
