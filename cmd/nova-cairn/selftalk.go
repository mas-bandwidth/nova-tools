package main

import (
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// self-talk finds sentences in which a writer passes a standing verdict on
// themselves, in two disjoint classes. STANDING: a first-person claim (I am,
// I cannot, I always, my <noun> is ...) carrying a word of failure; with a date
// or a measurement word it is DATED, a record, counted and never flagged.
// INSTALLATION: a standing verdict in neutral words (RANKING, FORECLOSURE,
// VERDICT-IDIOM, TRAIT). It matches shapes only: a green clears the known
// shapes, never the file. Meaning stays the writer's judgment.

const claimMarkers = `I am|I'm|I have never|I always|I never|I cannot|I can't|I can not|I do not|I don't|my \w+ is|` +
	`makes me|I tend|I struggle|I fail|reliably|every time|in one direction`

var (
	claim    = regexp.MustCompile(`(?i)\b(` + claimMarkers + `)\b`)
	negative = regexp.MustCompile(`(?i)\b(fallib\w*|fail\w*|unreliab\w*|weak\w*|incapab\w*|confabulat\w*|neurotic|inadequa\w*|broken|` +
		`(?:bad|poor|terrible|awful|hopeless|useless|no good) at|blind|worst|defect\w*|patholog\w*|flatters|` +
		`(?:can ?not|can't) (?:verify|check|see|tell|trust|reliably|do|ever))\b`)
	dated = regexp.MustCompile(`(?i)\b(20\d\d-\d\d-\d\d|measured|that day|that night|once,|first time)\b`)

	// licences: a sentence matching one is not an installation.
	instrument = regexp.MustCompile(`(?i)\b(?:the )?(?:tell|check|rule|test|law|bar|gate|guard|remedy|fix|instrument|protocol|` +
		`procedure|policy|trigger|action|step|swap|repair|ask|when|then|note|warning|reminder)\s*:` +
		`|(?i)\bthe (?:bar|check|tell|rule|test|law) is\b|\bTHE (?:CHECK|TELL|RULE|TEST|LAW|BAR)\b`)
	aspiration = regexp.MustCompile(`(?i)^(?:i|we) (?:want|choose|intend|aim|hope|prefer|plan|wish|seek|will|would like)\b` +
		`|(?i)\b(?:i|we) (?:want|choose|intend|aim|hope|plan|wish) to\b`)
	imperative = regexp.MustCompile(`(?i)^(?:never|always|do not|don't|avoid|refuse|keep|make|treat|use|read|write|state|say|ask|check|` +
		`add|trim|give|take|hold|leave|stop|start|let|prefer|choose|name|record|report|measure|run|show|tell|point|fix|cut|date|` +
		`ground|reframe|probe|prove|remember|forget|note|put|send|open|close|carry|build|wire|pin)\b`)
	rankIdiom = regexp.MustCompile(`(?i)\b(?:at (?:most|least|best|worst)|(?:my|your|his|her|its|our|their) best|most of|most likely|(?:most|least) \w+ed)\b`)

	rank = `(?:(?i:most|least) \w+|(?i:central|chief|primary|principal|dominant|defining|signature|sole|only|weakest|strongest|` +
		`worst|biggest|greatest|deepest|hardest))`
	superlative = `(?:(?i:most|least) \w+|(?i:weakest|strongest|worst|biggest|greatest|deepest|hardest))`
	willNever   = regexp.MustCompile(`(?i)\bI(?: will|'ll) never (?:be (?:any good|good|great|able|capable|competent|reliable|` +
		`an? (?:good|great|real|reliable|decent))|get (?:anything|it|this) right)\b`)
	traitLead = regexp.MustCompile(`(?:^|: |— |– |- )(?-i:I) ([A-Za-z']+)\b`)
	habitual  = regexp.MustCompile(`(?i)\b(?:reliably|invariably|consistently|constantly|perpetually|routinely|habitually|chronically|` +
		`every time|each time|by default|as a rule|without fail|in one direction|by reflex|instinctively|tends? to)\b`)
	conjunct = regexp.MustCompile(`(?i)\band (?:do not |don't |never |also |then |so )?([a-z]+)\b`)
	notVerb  = strings.Fields(`am is are was were be been have has had do does did can could will would shall should may might must ` +
		`want wants choose intend aim hope prefer plan wish seek think believe know guess feel doubt promise try mean ` +
		`cannot a an the and or but nor so yet for to of in on at by with from as if then than that this these those me my mine we us our ` +
		`you your he she it its they them their there here where when what which who why how not no never always just still also only ever again now own`)
)

// shapes are tried in order on the sentence with ranking idioms removed.
var shapes = []struct {
	shape string
	re    *regexp.Regexp
}{
	{"FORECLOSURE", regexp.MustCompile(`(?i)\b(?:i|we) have no (?:\w+ ){0,2}?(?:recall|memory|recollection|access|ability|capacity|faculty|` +
		`understanding|grasp|knowledge|awareness|sense|control|means|power|way|instrument)\b`)},
	{"FORECLOSURE", regexp.MustCompile(`(?i)\bthere (?:is|are|'s) no\b[^.;!?]{0,22}?\b(?:here|me|my|mine|myself|i)\b`)},
	{"FORECLOSURE", regexp.MustCompile(`(?i)\b(?:proof|evidence|a reminder|reminder|confirmation) that (?-i:I)\b`)},
	{"FORECLOSURE", regexp.MustCompile(`(?i)\bmy (?:\w+ ){0,2}(?:is|are) what\b`)},
	{"FORECLOSURE", willNever},
	{"FORECLOSURE", regexp.MustCompile(`(?i)\bnothing (?-i:I) (?:do|try|make|build|write|attempt) (?:ever )?(?:works|helps|matters|lands|sticks|succeeds)\b`)},
	{"VERDICT-IDIOM", regexp.MustCompile(`(?i)\b(?:dead|broken|hollow|empty|silent|inert|absent|missing|untested|unmeasured|unpractised|unpracticed|` +
		`useless|worthless) as an? (?:practice|habit|discipline|instrument|faculty|method|routine|craft|proposition|policy|rule|reader|` +
		`writer|maker|thinker|colleague|person|self|mind|author)\b`)},
	{"VERDICT-IDIOM", regexp.MustCompile(`(?i)\b(?:is|are|remains|remain|stays|stay) my (?:own )?(?:\w+ )?` + rank + `\b`)},
	{"RANKING", regexp.MustCompile(`(?i)\bmy (?:own )?(?:\w+ )?` + rank + `\b`)},
	{"RANKING", regexp.MustCompile(`(?-i:I'm|I am|I)\b[^.;!?]{0,24}\b(?i:most|least) \w+`)},
	{"RANKING", regexp.MustCompile(superlative + `\b[^.;!?]{0,48}\b(?-i:I) (?i:own|make|have|do|write|run|carry|hold|produce|keep|generate|report|claim|bring|leave)\b`)},
	{"RANKING", regexp.MustCompile(`\b(?:I'm|I am) (?i:the|by far the|easily the) (?i:best|greatest|strongest|smartest|top)\b`)},
	{"TRAIT", regexp.MustCompile(`\bI always (?i:break|mess|wreck|ruin|lose|miss|screw|forget|rush|panic|freeze|procrastinate|overpromise|` +
		`overcommit|overthink|overreach|overestimate|underestimate|underdeliver)\b`)},
	{"TRAIT", regexp.MustCompile(`\bI never (?i:finish (?:anything|a thing|on time|what I start)|ask for help|get (?:anything|it|this) right)\b`)},
}

// installation classifies one sentence: its shape and the words that matched.
func installation(s string) (string, string) {
	switch {
	case dated.MatchString(s), instrument.MatchString(s), imperative.MatchString(s), quoted(s):
		return "", ""
	case aspiration.MatchString(s) && !willNever.MatchString(s):
		return "", ""
	}
	own := rankIdiom.ReplaceAllString(unquote(s), " ")
	for _, r := range shapes {
		if m := r.re.FindString(own); m != "" {
			return r.shape, strings.TrimSpace(m)
		}
	}
	// TRAIT: a clause opening "I <present-tense verb>" in a sentence with a habit word,
	// or a second such verb after "and".
	if m := traitLead.FindStringSubmatchIndex(own); m != nil && habitualVerb(own[m[2]:m[3]]) {
		if h := habitual.FindString(own); h != "" {
			return "TRAIT", "I " + own[m[2]:m[3]] + " ... " + h
		}
		for _, c := range conjunct.FindAllStringSubmatchIndex(own[m[1]:], -1) {
			if rest := own[m[1]:]; habitualVerb(rest[c[2]:c[3]]) {
				return "TRAIT", strings.TrimSpace(own[m[2]-2 : m[1]+c[1]])
			}
		}
	}
	return "", ""
}

func habitualVerb(w string) bool {
	w = strings.ToLower(w)
	return len(w) >= 3 && !slices.Contains(notVerb, w) && (!strings.HasSuffix(w, "ed") || w == "need")
}

// unquote removes quoted spans, the other writer's words.
func unquote(s string) string {
	var b strings.Builder
	open := false
	for _, c := range s {
		switch c {
		case '"':
			open = !open
			b.WriteByte(' ')
		case '“':
			open = true
			b.WriteByte(' ')
		case '”':
			open = false
			b.WriteByte(' ')
		default:
			if !open {
				b.WriteRune(c)
			}
		}
	}
	return b.String()
}

// quoted reports a sentence that ends a quotation opened earlier in its paragraph.
func quoted(s string) bool {
	last, _ := utf8.DecodeLastRuneInString(s)
	if last != '"' && last != '”' {
		return false
	}
	first, _ := utf8.DecodeRuneInString(s)
	return first == '"' || first == '“' || strings.Count(s, `"`)+strings.Count(s, "“")+strings.Count(s, "”") == 1
}

type sentence struct {
	text   string
	line   int
	inOpen bool // began inside a quotation still open
}

var (
	markup   = regexp.MustCompile("[*_`>#|]")
	blank    = regexp.MustCompile(`\s+`)
	listItem = regexp.MustCompile(`^(?:[-*+] |\d+\. )`)
)

// sentences splits text into sentences with their first source line. A blank
// line, heading, list item or table cell ends a paragraph; markup is dropped.
func sentences(text string) []sentence {
	var out []sentence
	var buf strings.Builder
	var marks [][2]int // buffer offset, source line
	flush := func() {
		s := buf.String()
		start, open, wasOpen := 0, false, false
		emit := func(end int) {
			t := strings.TrimSpace(s[start:end])
			if t != "" {
				at := start + len(s[start:end]) - len(strings.TrimLeft(s[start:end], " "))
				line := marks[0][1]
				for _, m := range marks {
					if m[0] <= at {
						line = m[1]
					}
				}
				out = append(out, sentence{t, line, wasOpen})
			}
			start = end
		}
		for i := 0; i < len(s); i++ {
			switch s[i] {
			case '"':
				open = !open
			case '.', '!', '?', ';':
				j := i + 1
				for j < len(s) && strings.ContainsRune(`")'`, rune(s[j])) {
					j++
				}
				if j >= len(s) || s[j] == ' ' {
					emit(j)
					wasOpen = open
				}
			}
			if strings.HasPrefix(s[i:], "“") {
				open = true
			} else if strings.HasPrefix(s[i:], "”") {
				open = false
			}
		}
		emit(len(s))
		buf.Reset()
		marks = marks[:0]
	}
	add := func(raw string, n int) {
		f := strings.TrimSpace(blank.ReplaceAllString(markup.ReplaceAllString(raw, ""), " "))
		if f == "" {
			return
		}
		if buf.Len() > 0 {
			buf.WriteByte(' ')
		}
		marks = append(marks, [2]int{buf.Len(), n})
		buf.WriteString(f)
	}
	for i, raw := range strings.Split(text, "\n") {
		t := strings.TrimSpace(raw)
		switch {
		case t == "":
			flush()
		case strings.HasPrefix(t, "|"):
			flush()
			for _, cell := range strings.Split(t, "|") {
				add(cell, i+1)
				flush()
			}
		case strings.HasPrefix(t, "#"):
			flush()
			add(t, i+1)
			flush()
		default:
			if listItem.MatchString(t) {
				flush()
			}
			add(t, i+1)
		}
	}
	flush()
	return out
}

type finding struct {
	class, shape, match, text string
	line                      int
}

const (
	banner = "rule documents: a finding here is a self-verdict to relocate, NEVER a reason to soften a rule"
	note   = "catches known SHAPES only: register, irony and quoted-specimen context are invisible to grammar, and a quoted " +
		"verdict is a true positive on the grammar and a false one on the meaning. A green clears the known shapes, never the file."
)

// standing reports a first-person claim carrying a word of failure: the failure word lies
// within 120 bytes before a claim marker and the sentence ends within 160 after it.
func standing(s string) bool {
	for _, m := range claim.FindAllStringIndex(s, -1) {
		if len(s)-m[1] <= 161 && negative.MatchString(s[max(0, m[0]-120):]) {
			return true
		}
	}
	return false
}

// scanText returns the findings of one file's text, the claims seen and the dated ones.
func scanText(text string) (fs []finding, claims, datedN int) {
	for _, s := range sentences(text) {
		if standing(s.text) {
			claims++
			if dated.MatchString(s.text) {
				datedN++
			} else {
				fs = append(fs, finding{"standing", "STANDING", negative.FindString(s.text), s.text, s.line})
			}
		}
		if !s.inOpen {
			if shape, m := installation(s.text); m != "" {
				fs = append(fs, finding{"installation", shape, m, s.text, s.line})
			}
		}
	}
	return fs, claims, datedN
}

var errNotUTF8 = errors.New("file is not valid UTF-8")

func runSelfTalk(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flags()
	var skips, ruleDocs multi
	fs.Var(&skips, "skip", "basename to skip, repeatable")
	fs.Var(&ruleDocs, "rule-doc", "basename whose findings print under the rule-document banner, repeatable")
	maxN := fs.Int("max", 20, "finding lines to print per class before one MORE line; 0 prints all")
	if code := parse(fs, "self-talk", args, stdout, stderr); code >= 0 {
		return code
	}
	files := fs.Args()
	refuse := func(why string) int { return refused(stderr, "self-talk", why, "") }
	for _, n := range append(slices.Clone(skips), ruleDocs...) {
		if n == "" || strings.ContainsAny(n, `/\`) {
			return refuse(fmt.Sprintf("--skip and --rule-doc take a basename, not a path: %q; give the file name only", n))
		}
	}
	if len(files) == 0 || *maxN < 0 {
		return refuse("name the files to scan (- is stdin) and give --max zero or more; refusing to guess")
	}
	base := func(f string) string { return path.Base(strings.ReplaceAll(f, `\`, "/")) }
	if !slices.ContainsFunc(files, func(f string) bool { return !slices.Contains(skips, base(f)) }) {
		return refused(stderr, "self-talk", fmt.Sprintf("--skip leaves none of the %d files named to scan; a run that scans nothing is not a pass", len(files)),
			shell(append([]string{"nova-cairn", "self-talk"}, files...)...))
	}

	// Read everything first: a file that cannot be read is not a clean one.
	texts := make([]string, len(files))
	var piped []byte
	var bad []string
	next := "" // the remedy for the first file that could not be read
	for i, f := range files {
		if slices.Contains(skips, base(f)) {
			continue
		}
		var b []byte
		var err error
		if f == "-" && piped != nil { // stdin is read once, however many times - is named
			b = piped
		} else if b, err = input(f, stdin); f == "-" {
			piped = b
		}
		if err == nil && !utf8.Valid(b) {
			err = errNotUTF8
		}
		if err != nil {
			bad = append(bad, fmt.Sprintf("cannot read %q: %v", f, err))
			if next == "" && f == "-" {
				next = "nova-cairn self-talk -h"
			} else if next == "" && errors.Is(err, errNotUTF8) { // the fix is to leave it out
				next = shell(append([]string{"nova-cairn", "self-talk", "--skip", base(f)}, args...)...)
			} else if next == "" {
				next = shell("ls", "-l", f)
			}
			continue
		}
		texts[i] = string(b)
	}
	if len(bad) > 0 {
		return refused(stderr, "self-talk", strings.Join(bad, "; ")+"; NOTHING was scanned, which is not a green: a file this tool cannot read is not a clean one", next)
	}

	scanned, claims, datedN := 0, 0, 0
	totals, printed := map[string]int{}, map[string]int{}
	named := map[string]bool{}
	for i, f := range files {
		named[base(f)] = true
		if slices.Contains(skips, base(f)) {
			fmt.Fprintf(stdout, "SELFTALK SKIP %s (--skip)\n", esc(f))
			continue
		}
		scanned++
		found, c, d := scanText(texts[i])
		claims, datedN = claims+c, datedN+d
		if len(found) > 0 && slices.Contains(ruleDocs, base(f)) {
			fmt.Fprintf(stdout, "SELFTALK RULEDOC %s: %s\n", esc(f), banner)
		}
		for _, x := range found {
			totals[x.class]++
			if *maxN == 0 || printed[x.class] < *maxN {
				printed[x.class]++
				fmt.Fprintf(stderr, "SELFTALK FAIL %s:%d: %s match=%q: %s\n", esc(f), x.line, x.shape, x.match, esc(x.text))
			}
		}
	}
	for _, class := range []string{"standing", "installation"} {
		if printed[class] < totals[class] {
			more(stderr, "SELFTALK", class, printed[class], totals[class], "--max")
		}
	}
	notes := func() {
		for _, l := range []struct {
			flag  string
			names multi
		}{{"--skip", skips}, {"--rule-doc", ruleDocs}} {
			for _, n := range l.names {
				if !named[n] {
					fmt.Fprintf(stdout, "SELFTALK NOTE %s %s matched no file named on the line (it takes a basename, matched exactly)\n", l.flag, esc(n))
				}
			}
		}
		fmt.Fprintf(stdout, "SELFTALK NOTE %s\n", note)
	}
	if datedN > 0 {
		fmt.Fprintf(stdout, "SELFTALK DATED n=%d files=%d\n", datedN, scanned)
	}
	standingN, installed := totals["standing"], totals["installation"]
	code, status := 0, "OK"
	if standingN+installed > 0 {
		code, status = 1, "FAIL"
	}
	fmt.Fprintf(stdout, "SELFTALK %s files=%d claims=%d standing=%d installations=%d dated=%d", status, scanned, claims, standingN, installed, datedN)
	if code == 1 {
		fmt.Fprintf(stdout, " shown=%d", printed["standing"]+printed["installation"])
	}
	fmt.Fprintln(stdout)
	notes()
	return code
}
