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
// Each refusal is one line naming the rule, the line and the remedy.

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

// parentKind is the KIND of a card cut as a parent with children (#4388's
// hierarchy): its children carry the DONE-WHEN, the CLASS-TEST and the
// packages, so the parent is exempt from those rules. It is the KIND value
// alone, so this compiles and holds before the hierarchy lands.
const parentKind = "parent"

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
	// GoFiles is the path of every .go file in the repo at the card's BASE,
	// the tree PATHS is counted against. nil is a tree not read: a path is
	// then counted by its shape (Packages).
	GoFiles []string
}

// Sentences counts the sentences in s: a sentence ends in . ! or ? followed
// by a space or the end, and a `code span` is opaque (a period inside one ends
// nothing). Text after the last end is one more sentence; "" is none.
func Sentences(s string) int {
	s = strings.TrimSpace(s)
	n, open, pending := 0, false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '`':
			open = !open
			pending = true
		case open:
		case (c == '.' || c == '!' || c == '?') && (i+1 == len(s) || s[i+1] == ' ' || s[i+1] == '\t' || s[i+1] == '\n'):
			n++
			pending = false
		case c != ' ' && c != '\t' && c != '\n':
			pending = true
		}
	}
	if pending {
		n++
	}
	return n
}

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
// Entries are separated by commas or spaces. With goFiles (the repo at the
// card's BASE) an entry spans the directory of every .go file it names,
// matches as a glob, or holds below it; an entry ending in .go that names no
// file yet is a new file in its directory. With no tree (goFiles nil) an entry
// is read by its shape: a .go file or glob is its directory, a directory
// (a trailing / or /..., or a path whose last element has no extension) is
// itself, and anything else is not a package.
func Packages(paths string, goFiles []string) []string {
	pkgs := map[string]bool{}
	for _, tok := range strings.FieldsFunc(paths, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == ';' }) {
		tok = strings.Trim(tok, "`'\"")
		tok = strings.TrimPrefix(tok, "./")
		dir := strings.HasSuffix(tok, "/") || strings.HasSuffix(tok, "/...")
		tok = strings.TrimSuffix(strings.TrimSuffix(tok, "..."), "/")
		if tok == "" || tok == "." || !pathTokenRE.MatchString(tok) {
			continue
		}
		isGo := strings.HasSuffix(tok, ".go")
		if goFiles == nil {
			switch {
			case isGo:
				pkgs[path.Dir(tok)] = true
			case dir || strings.Contains(tok, "/") && !strings.Contains(path.Base(tok), "."):
				pkgs[tok] = true
			}
			continue
		}
		found := false
		for _, f := range goFiles {
			ok, _ := path.Match(tok, f)
			if ok || f == tok || strings.HasPrefix(f, tok+"/") {
				pkgs[path.Dir(f)] = true
				found = true
			}
		}
		if !found && isGo {
			pkgs[path.Dir(tok)] = true
		}
	}
	out := make([]string, 0, len(pkgs))
	for p := range pkgs {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// cardLine is one KEY: value line the linter read, as written.
type cardLine struct {
	value, text string
}

// lintCard is the card as the rules read it.
type lintCard struct {
	keys    map[string]cardLine // the first line of each key
	lines   []string            // every line, a quote prefix ("> ") dropped, fences dropped
	kind    string
	goFiles []string
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
	lc := &lintCard{keys: map[string]cardLine{}, goFiles: c.GoFiles}
	fenced := false
	for _, raw := range strings.Split(strings.ReplaceAll(c.Text, "\r\n", "\n"), "\n") {
		line := unquote(strings.TrimRight(raw, " \t\r"))
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		lc.lines = append(lc.lines, line)
		if k, v, ok := KeyValue(line); ok {
			if _, seen := lc.keys[k]; !seen {
				lc.keys[k] = cardLine{value: v, text: line}
			}
		}
	}
	lc.kind = strings.TrimSpace(lc.keys[keyKind].value)
	return lc
}

func (lc *lintCard) parent() bool { return lc.kind == parentKind }

// rule is one check: nil when the card keeps it.
type rule struct {
	name  string
	check func(*lintCard) *Refusal
}

var (
	buildIssueRE = regexp.MustCompile(`(?i)build .*#\d+ .*as written`)
	listItemRE   = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s+\S`)
)

const (
	remedyInvariant = "add INVARIANT: <the one sentence the class test proves>"
	remedyDoneWhen  = "add DONE-WHEN: <one sentence a test can fail>"
	remedyClassTest = "add CLASS-TEST: Test<Name>, the one Go test that proves the invariant"
	remedyPlatforms = "write PLATFORMS: darwin,linux (or darwin, or linux)"
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
		if l, ok := lc.keys[KeyInvariant]; ok && Sentences(l.value) > 1 {
			return &Refusal{Line: l.text, Remedy: RemedyParent}
		}
		return nil
	}},
	{RuleDoneWhenMissing, func(lc *lintCard) *Refusal {
		l, ok := lc.keys[keyDoneWhen]
		if !lc.parent() && (!ok || strings.TrimSpace(l.value) == "") {
			return &Refusal{Line: l.text, Remedy: remedyDoneWhen}
		}
		return nil
	}},
	{RuleDoneWhenSentences, func(lc *lintCard) *Refusal {
		if l, ok := lc.keys[keyDoneWhen]; ok && !lc.parent() && Sentences(l.value) > 1 {
			return &Refusal{Line: l.text, Remedy: RemedyParent}
		}
		return nil
	}},
	{RuleClassTestMissing, func(lc *lintCard) *Refusal {
		l, ok := lc.keys[KeyClassTest]
		if !lc.parent() && (!ok || strings.TrimSpace(l.value) == "") {
			return &Refusal{Line: l.text, Remedy: remedyClassTest}
		}
		return nil
	}},
	{RuleClassTestForm, func(lc *lintCard) *Refusal {
		l, ok := lc.keys[KeyClassTest]
		if !ok || lc.parent() || strings.TrimSpace(l.value) == "" {
			return nil
		}
		if _, err := ParseClassTest(l.value); err != nil {
			return &Refusal{Line: l.text, Remedy: remedyClassTest}
		}
		return nil
	}},
	{RulePathsPackages, func(lc *lintCard) *Refusal {
		l, ok := lc.keys[keyPaths]
		if ok && !lc.parent() && len(Packages(l.value, lc.goFiles)) > MaxPackages {
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
		for i, line := range lc.lines {
			if k, _, ok := KeyValue(line); !ok || k != KeyBuild {
				continue
			}
			items := 0
			for _, next := range lc.lines[i+1:] {
				if listItemRE.MatchString(next) {
					items++
					continue
				}
				if _, _, ok := KeyValue(next); ok || strings.HasPrefix(next, "#") || strings.TrimSpace(next) == "---" {
					break
				}
			}
			if items >= 2 {
				return &Refusal{Line: line, Remedy: RemedyParent}
			}
		}
		return nil
	}},
}

// LintOneInvariant is every rule's refusal for card, in rule order; nil when
// the card is one invariant. Every push path runs it before any write.
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
