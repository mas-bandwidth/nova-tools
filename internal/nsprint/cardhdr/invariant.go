package cardhdr

// ONE CARD, ONE INVARIANT (nova-tools#4396, Glenn 2026-09-26: "It is better
// to cut high quality cards than to waste time building it then throwing it
// away at 4/10"). Over sixteen cold reads every 4/10 or 5/10 was a card whose
// body was a list of items or a pointer to an issue, and every 8 to 10 a card
// of one invariant with a named test. LintOneInvariant is the one check every
// push path runs (card push, card cut, card cut --from, task push): a card
// carries an INVARIANT of one sentence, a DONE-WHEN of one sentence, a
// CLASS-TEST naming one Go test, PATHS over at most three Go packages and, if
// any, PLATFORMS of darwin and linux only; a body that says "build issue #N
// as written", or a BUILD: section that lists two or more items, is refused.
// Each refusal is one line naming the rule, the line and the remedy. A KIND
// plan skips the DONE-WHEN, CLASS-TEST, PATHS and BUILD-list rules (its
// children carry the test and the packages) and must list its children under
// BUILD: (plan-children: a plan with none is refused).
// A KIND stitch is linted like any card: the one stitch no rule reads is the
// row card cut --parent generates, which that writer never lints (its row
// check returns before the lint); a KIND: stitch header is no exemption.
// LintTitleKind is the check every task push runs on its --kind and --title,
// with a card or none: a stitch is refused (card cut --parent is its one
// writer), a plan with no card is refused plan-children, and a title that
// says "build issue #N as written" is refused build-issue.

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The card lines this file reads.
const (
	KeyInvariant = "INVARIANT"
	KeyClassTest = "CLASS-TEST"
	KeyPlatforms = "PLATFORMS"
	KeyBuild     = "BUILD"
	keyDoneWhen  = "DONE-WHEN"
	keyPaths     = "PATHS"
	keyKind      = "KIND"
)

// The hierarchy's KINDs (#4388; taskcard.KindPlan and taskcard.KindStitch
// are these constants). A plan is a card cut as a parent with children: its
// children carry the DONE-WHEN, the CLASS-TEST and the packages, and its
// BUILD: lists them, so a plan is exempt from the done-when-*, class-test-*,
// paths-packages and build-list rules, and refused plan-children when its
// BUILD: lists no child. A stitch is the card card cut --parent
// writes for the plan's second phase; the header grants it nothing, since any
// author can type KIND: stitch (card cut --parent passes the exemption by not
// linting the stitch row it generates).
const (
	KindPlan   = "plan"
	KindStitch = "stitch"
)

// MaxPackages is the most Go packages a card's PATHS may span.
const MaxPackages = 3

// Platforms is every value a PLATFORMS line may name.
var Platforms = []string{"darwin", "linux"}

// RemedyParent is the remedy for a card that is more than one invariant.
const RemedyParent = "cut as a parent with children: card cut --parent"

// The rule names a refusal carries, one per check.
const (
	RuleInvariantMissing   = "invariant-missing"
	RuleInvariantSentences = "invariant-sentences"
	RuleDoneWhenMissing    = "done-when-missing"
	RuleDoneWhenSentences  = "done-when-sentences"
	RuleClassTestMissing   = "class-test-missing"
	RuleClassTestForm      = "class-test-form"
	RulePathsPackages      = "paths-packages"
	RulePlatforms          = "platforms"
	RuleBuildIssue         = "build-issue"
	RuleBuildList          = "build-list"
	RulePlanChildren       = "plan-children"
	RuleStitchWriter       = "stitch-writer"
)

// Refusal is one broken rule: its name, the offending line as written ("" when
// the line is missing) and the remedy.
type Refusal struct {
	Rule   string
	Line   string
	Remedy string
}

// String is the refusal's one line:
//
//	REFUSED card-lint rule=<name> line="<the offending line>" remedy="<exact command>"
func (r Refusal) String() string {
	return fmt.Sprintf("REFUSED card-lint rule=%s line=%s remedy=%s", r.Rule, strconv.Quote(r.Line), strconv.Quote(r.Remedy))
}

// Refusals is a card's refusals as an error: one line each.
type Refusals []Refusal

func (rs Refusals) Error() string {
	lines := make([]string, len(rs))
	for i, r := range rs {
		lines[i] = r.String()
	}
	return strings.Join(lines, "\n")
}

// Rules is the refusals' rule names, comma separated.
func (rs Refusals) Rules() string {
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = r.Rule
	}
	return strings.Join(names, ",")
}

// Card is what the linter reads: the card text (a card body, or an issue text
// a card is cut from) and the repo at the card's BASE.
type Card struct {
	Text string
	// Files is the path of every file in the repo at the card's BASE, the
	// tree PATHS is counted against. nil is a tree not read: a path is then
	// counted by its shape (Packages).
	Files []string
}

// abbreviations are the words whose period ends no sentence (lower case,
// without the last period): "e.g. x", "i.e. x", "a vs. b", "a, b, etc. c".
var abbreviations = map[string]bool{"e.g": true, "i.e": true, "vs": true, "etc": true}

// abbreviationAt reports whether the period at s[i] closes an abbreviation:
// the word before it (back to a space or an opening bracket or quote) is one.
func abbreviationAt(s string, i int) bool {
	j := i
	for j > 0 && !strings.ContainsRune(" \t\n([{\"'", rune(s[j-1])) {
		j--
	}
	return abbreviations[strings.ToLower(s[j:i])]
}

// Sentences counts the sentences in s: a sentence ends in . ! or ? followed,
// after any closing quotes or brackets ("done." or (see x.)), by a space or
// the end. A `code span` is opaque (a period inside one ends nothing), and a
// backtick with no closing backtick on its line is a plain character. The
// period of e.g., i.e., vs. or etc. ends nothing. Text after the last end is
// one more sentence; "" is none.
func Sentences(s string) int {
	s = strings.TrimSpace(s)
	n, pending := 0, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '`':
			pending = true
			line := s[i+1:]
			if nl := strings.IndexByte(line, '\n'); nl >= 0 {
				line = line[:nl]
			}
			if end := strings.IndexByte(line, '`'); end >= 0 {
				i += end + 1 // the span, closing backtick included
			}
		case c == '.' && abbreviationAt(s, i):
			pending = true
		case c == '.' || c == '!' || c == '?':
			j := i + 1
			for j < len(s) && strings.IndexByte(closers, s[j]) >= 0 {
				j++
			}
			if j == len(s) || s[j] == ' ' || s[j] == '\t' || s[j] == '\n' {
				n++
				pending = false
				i = j - 1
			} else {
				pending = true
			}
		case c != ' ' && c != '\t' && c != '\n':
			pending = true
		}
	}
	if pending {
		n++
	}
	return n
}

// closers are the characters that may follow a sentence's end before the
// space: closing quotes and brackets.
const closers = "\"')]}"

// ParseInvariant reads an INVARIANT line's value: one sentence.
func ParseInvariant(value string) (string, error) {
	v := strings.TrimSpace(value)
	switch n := Sentences(v); {
	case n == 0:
		return "", fmt.Errorf("INVARIANT: is empty; the card states the one invariant it makes true")
	case n > 1:
		return "", fmt.Errorf("INVARIANT: is %d sentences, not one", n)
	}
	return v, nil
}

// classTestRE is one Go test's name.
var classTestRE = regexp.MustCompile(`^Test[A-Za-z0-9_]+$`)

// ParseClassTest reads a CLASS-TEST line's value: the one Go test (by name)
// that proves the card's invariant.
func ParseClassTest(value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", fmt.Errorf("CLASS-TEST: is empty; name the one Go test that proves the invariant")
	}
	if !classTestRE.MatchString(v) {
		return "", fmt.Errorf("CLASS-TEST: %q is not one Go test name (Test[A-Za-z0-9_]+)", v)
	}
	return v, nil
}

// ParsePlatforms reads a PLATFORMS line's value: a comma-separated subset of
// Platforms, each named once (PLATFORMS: darwin,linux).
func ParsePlatforms(value string) ([]string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return nil, fmt.Errorf("PLATFORMS: is empty; name darwin, linux or darwin,linux")
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		known := false
		for _, k := range Platforms {
			known = known || k == p
		}
		if !known {
			return nil, fmt.Errorf("PLATFORMS: %q is not a subset of darwin,linux", v)
		}
		if seen[p] {
			return nil, fmt.Errorf("PLATFORMS: %q names %s twice", v, p)
		}
		seen[p] = true
		out = append(out, p)
	}
	return out, nil
}

// pathTokenRE is a PATHS entry that is a path (a word such as "(new)" or
// "and" between paths is not).
var pathTokenRE = regexp.MustCompile(`^[A-Za-z0-9_.*?\[\]/-]+$`)

// Packages is the distinct Go packages (directories) a PATHS value spans.
// Entries are separated by commas or spaces. With files (every file in the
// repo at the card's BASE) an entry that is in the tree (it names a file,
// matches one as a glob, or holds one below it) spans the directory of every
// .go file it covers, so a directory in the tree with no .go file (docs/) is
// no package. An entry absent from the tree (a new file or a new directory)
// is read by its shape, as every entry is with no tree (files nil): a .go
// file or glob is its directory, a directory (a trailing / or /..., or a path
// whose last element has no extension) is itself, and anything else is not a
// package.
func Packages(paths string, files []string) []string {
	pkgs := map[string]bool{}
	for _, tok := range strings.FieldsFunc(paths, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == ';' }) {
		tok = strings.Trim(tok, "`'\"")
		tok = strings.TrimPrefix(tok, "./")
		dir := strings.HasSuffix(tok, "/") || strings.HasSuffix(tok, "/...")
		tok = strings.TrimSuffix(strings.TrimSuffix(tok, "..."), "/")
		if tok == "" || tok == "." || !pathTokenRE.MatchString(tok) {
			continue
		}
		inTree := false
		for _, f := range files {
			if ok, _ := path.Match(tok, f); ok || f == tok || strings.HasPrefix(f, tok+"/") {
				inTree = true
				if strings.HasSuffix(f, ".go") {
					pkgs[path.Dir(f)] = true
				}
			}
		}
		if inTree {
			continue
		}
		switch {
		case strings.HasSuffix(tok, ".go"):
			pkgs[path.Dir(tok)] = true
		case dir || strings.Contains(tok, "/") && !strings.Contains(path.Base(tok), "."):
			pkgs[tok] = true
		}
	}
	out := make([]string, 0, len(pkgs))
	for p := range pkgs {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// cardLine is one KEY: value line the linter read, as written; more is the
// lines that continue it (up to a blank line, a KEY: line, a list item, a
// heading, a rule or a fence), read with the value as its sentences.
type cardLine struct {
	value, text, more string
}

// sentences is the line's sentence count, its continuation lines included.
func (l cardLine) sentences() int {
	return Sentences(l.value + "\n" + l.more)
}

// lintCard is the card as the rules read it.
type lintCard struct {
	keys  map[string]cardLine // the first line of each key
	lines []string            // every line, a quote prefix ("> ") dropped, fences dropped
	all   []string            // every line, fenced lines kept (the fence lines dropped)
	kind  string
	files []string
}

// unquote drops a Markdown quote prefix, so a card cut from an issue (which
// quotes the issue under "> ") is read like the issue.
func unquote(line string) string {
	for strings.HasPrefix(line, ">") {
		line = strings.TrimPrefix(strings.TrimPrefix(line, ">"), " ")
	}
	return line
}

func readCard(c Card) *lintCard {
	lc := &lintCard{keys: map[string]cardLine{}, files: c.Files}
	fenced := false
	cont := "" // the key whose continuation lines are being read
	for _, raw := range strings.Split(strings.ReplaceAll(c.Text, "\r\n", "\n"), "\n") {
		line := unquote(strings.TrimRight(raw, " \t\r"))
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced, cont = !fenced, ""
			continue
		}
		lc.all = append(lc.all, line)
		if fenced {
			continue
		}
		lc.lines = append(lc.lines, line)
		if k, v, ok := KeyValue(line); ok {
			cont = ""
			if _, seen := lc.keys[k]; !seen {
				lc.keys[k] = cardLine{value: v, text: line}
				cont = k
			}
			continue
		}
		t := strings.TrimSpace(line)
		if t == "" || t == "---" || strings.HasPrefix(t, "#") || listItemRE.MatchString(line) {
			cont = ""
		}
		if cont != "" {
			l := lc.keys[cont]
			l.more += line + "\n"
			lc.keys[cont] = l
		}
	}
	lc.kind = strings.TrimSpace(lc.keys[keyKind].value)
	return lc
}

func (lc *lintCard) plan() bool { return lc.kind == KindPlan }

// rule is one check: nil when the card keeps it.
type rule struct {
	name  string
	check func(*lintCard) *Refusal
}

var (
	// buildIssueRE is a body that points at an issue: "Build issue #4352 as
	// written.", "Build nova-tools#4352, as written." (any words, any
	// punctuation, between).
	buildIssueRE = regexp.MustCompile(`(?i)\bbuild\b.*#\d+\b.*\bas written\b`)
	// listItemRE is one list item: - * + markers; and a number, a letter
	// (upper or lower case: the #4352 A-F shape, a. b.) or a roman numeral
	// (i. ii., I. II.) written 1. or 1) or (1).
	listItemRE = regexp.MustCompile(`^\s*(?:[-*+]|(?:\d+|[A-Za-z]|[ivx]+|[IVX]+)[.)]|\((?:\d+|[A-Za-z]|[ivx]+|[IVX]+)\))\s+\S`)
	// inlineItemRE is one numbered item on a BUILD: line itself: 1. 1) (1).
	inlineItemRE = regexp.MustCompile(`\s(?:\d+[.)]|\(\d+\))\s+\S`)
)

const (
	remedyInvariant = "add INVARIANT: <the one sentence the class test proves>"
	remedyDoneWhen  = "add DONE-WHEN: <one sentence a test can fail>"
	remedyClassTest = "add CLASS-TEST: Test<Name>, the one Go test that proves the invariant"
	remedyPlatforms = "write PLATFORMS: darwin,linux (or darwin, or linux)"
	// remedyPlanChildren: a plan names its children, or is not a plan.
	remedyPlanChildren = "list each child under BUILD: (one item per child card), or push it as one card: KIND: fix"
)

// rules is every check, in the order a card's refusals are printed.
var rules = []rule{
	{RuleInvariantMissing, func(lc *lintCard) *Refusal {
		l, ok := lc.keys[KeyInvariant]
		if !ok || strings.TrimSpace(l.value) == "" {
			return &Refusal{Line: l.text, Remedy: remedyInvariant}
		}
		return nil
	}},
	{RuleInvariantSentences, func(lc *lintCard) *Refusal {
		if l, ok := lc.keys[KeyInvariant]; ok && l.sentences() > 1 {
			return &Refusal{Line: l.text, Remedy: RemedyParent}
		}
		return nil
	}},
	{RuleDoneWhenMissing, func(lc *lintCard) *Refusal {
		l, ok := lc.keys[keyDoneWhen]
		if !lc.plan() && (!ok || strings.TrimSpace(l.value) == "") {
			return &Refusal{Line: l.text, Remedy: remedyDoneWhen}
		}
		return nil
	}},
	{RuleDoneWhenSentences, func(lc *lintCard) *Refusal {
		if l, ok := lc.keys[keyDoneWhen]; ok && !lc.plan() && l.sentences() > 1 {
			return &Refusal{Line: l.text, Remedy: RemedyParent}
		}
		return nil
	}},
	{RuleClassTestMissing, func(lc *lintCard) *Refusal {
		l, ok := lc.keys[KeyClassTest]
		if !lc.plan() && (!ok || strings.TrimSpace(l.value) == "") {
			return &Refusal{Line: l.text, Remedy: remedyClassTest}
		}
		return nil
	}},
	{RuleClassTestForm, func(lc *lintCard) *Refusal {
		l, ok := lc.keys[KeyClassTest]
		if !ok || lc.plan() || strings.TrimSpace(l.value) == "" {
			return nil
		}
		if _, err := ParseClassTest(l.value); err != nil {
			return &Refusal{Line: l.text, Remedy: remedyClassTest}
		}
		return nil
	}},
	{RulePathsPackages, func(lc *lintCard) *Refusal {
		l, ok := lc.keys[keyPaths]
		if ok && !lc.plan() && len(Packages(l.value, lc.files)) > MaxPackages {
			return &Refusal{Line: l.text, Remedy: RemedyParent}
		}
		return nil
	}},
	{RulePlatforms, func(lc *lintCard) *Refusal {
		if l, ok := lc.keys[KeyPlatforms]; ok {
			if _, err := ParsePlatforms(l.value); err != nil {
				return &Refusal{Line: l.text, Remedy: remedyPlatforms}
			}
		}
		return nil
	}},
	{RuleBuildIssue, func(lc *lintCard) *Refusal {
		for _, line := range lc.lines {
			if buildIssueRE.MatchString(line) {
				return &Refusal{Line: line, Remedy: RemedyParent}
			}
		}
		return nil
	}},
	{RuleBuildList, func(lc *lintCard) *Refusal {
		if lc.plan() {
			return nil // a plan's BUILD: lists its children (plan-children)
		}
		if line, items := lc.buildList(); items >= 2 {
			return &Refusal{Line: line, Remedy: RemedyParent}
		}
		return nil
	}},
	{RulePlanChildren, func(lc *lintCard) *Refusal {
		if !lc.plan() {
			return nil
		}
		if _, items := lc.buildList(); items == 0 {
			return &Refusal{Line: lc.keys[keyKind].text, Remedy: remedyPlanChildren}
		}
		return nil
	}},
}

// buildList is the card's longest BUILD: list: the BUILD: line (any letter
// case: Build:, build:) and its items, fenced lines included: the items on
// the BUILD: line itself (BUILD: 1. a 2. b, inlineItemRE) and the list items
// (listItemRE) below it, read up to the next KEY: line, heading or rule; ""
// and 0 when no BUILD: line has an item.
func (lc *lintCard) buildList() (string, int) {
	best, most := "", 0
	for i, line := range lc.all {
		k, v, ok := KeyValue(line)
		if !ok || !strings.EqualFold(k, KeyBuild) {
			continue
		}
		items := len(inlineItemRE.FindAllString(" "+v, -1))
		for _, next := range lc.all[i+1:] {
			if listItemRE.MatchString(next) {
				items++
				continue
			}
			if _, _, ok := KeyValue(next); ok || strings.HasPrefix(next, "#") || strings.TrimSpace(next) == "---" {
				break
			}
		}
		if items > most {
			best, most = line, items
		}
	}
	return best, most
}

// LintOneInvariant is every rule's refusal for card, in rule order; nil when
// the card is one invariant. Every push path runs it before any write, on
// every KIND (a KIND: stitch included); the stitch card cut --parent
// generates is the one card no push path lints.
func LintOneInvariant(c Card) Refusals {
	return lintWith(c, rules)
}

func lintWith(c Card, rs []rule) Refusals {
	lc := readCard(c)
	var out Refusals
	for _, r := range rs {
		if ref := r.check(lc); ref != nil {
			ref.Rule = r.name
			out = append(out, *ref)
		}
	}
	return out
}

// remedyStitch: a stitch has one writer, the plan's cut.
const remedyStitch = "a stitch is cut with its plan: card cut --parent <plan id> (no other push writes KIND stitch)"

// LintTitleKind is the refusals every task push runs on its --kind and
// --title (#4396), a card pushed with it (card true) or none: KIND stitch is
// refused stitch-writer (card cut --parent, which pushes no task push, is its
// one writer), KIND plan with no card is refused plan-children (no BUILD:
// names a child), and a title that says "build issue #N as written" is
// refused build-issue. nil when the push keeps them.
func LintTitleKind(kind, title string, card bool) Refusals {
	var out Refusals
	kind = strings.TrimSpace(kind)
	if kind == KindStitch {
		out = append(out, Refusal{Rule: RuleStitchWriter, Line: "--kind " + kind, Remedy: remedyStitch})
	}
	if kind == KindPlan && !card {
		out = append(out, Refusal{Rule: RulePlanChildren, Line: "--kind " + kind, Remedy: remedyPlanChildren})
	}
	if buildIssueRE.MatchString(title) {
		out = append(out, Refusal{Rule: RuleBuildIssue, Line: "--title " + title, Remedy: RemedyParent})
	}
	return out
}

// Merge is rs and then each refusal of more whose rule rs does not name, so
// a rule is one line.
func (rs Refusals) Merge(more Refusals) Refusals {
	for _, m := range more {
		seen := false
		for _, r := range rs {
			seen = seen || r.Rule == m.Rule
		}
		if !seen {
			rs = append(rs, m)
		}
	}
	return rs
}
