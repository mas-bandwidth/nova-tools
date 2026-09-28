package prereview

import (
	"fmt"
	"regexp"
	"strings"
)

// The symbol check is the one Johnny called "the one that pays": does the added
// test EXERCISE generated code, or does it check its own arithmetic?
//
// It is two-sided on purpose, because one side alone gets it wrong on the
// friend-classified corpus. "Does the file mention a generated symbol" says yes
// to schema#1459, which calls the real tableFixedSelect for half its assertions
// and then writes `// Simulate exactly what FixedLoad does` for the other half;
// it says yes to #1497, which includes the real C header and then compares it
// against a byte array pasted in from a comment. So:
//
//	symbol = yes  iff  a generated symbol is exercised AND no self-check tell is present
//
// Both lists are DERIVED, not invented: every tell below cites the pull request
// in reports/false-confidence-cells-2026-09-22.md that Johnny or Emma read
// line-by-line and named it from. That is also the honest limit of this check --
// it is calibrated on 122 cells of one repository's conformance legs, and a tell
// is a string a future card can avoid writing while doing the same thing. It
// bounces a card to a recut; it lands nothing.

// tell is one self-check signal: a pattern, the sentence that says what it
// means, and the pull request it was read from.
type tell struct {
	re   *regexp.Regexp
	says string
	from string
}

// generated is the positive side: the test reaches real generated code.
//
// The families are the ones the schema legs actually emit -- CamelCase
// `<Unit>Fixed<Verb>`, lowerCamel `tableFixed<Verb>`, rust/C snake_case
// `_fixed_<verb>` -- plus the two ways a test can reach generated code without
// naming a function: including or importing a generated artifact, and running
// the generator itself as a subprocess (schema#1488 does exactly that).
var generated = []struct {
	re   *regexp.Regexp
	says string
}{
	{regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*Fixed(Save|Load|Digest|Select|Measure|Open|Compile|Refuse[A-Za-z]*|LineagePlans)\s*\(`),
		"calls a generated fixed-form entry point"},
	{regexp.MustCompile(`\b[a-z0-9_]*_fixed_(save|load|digest|select|measure|open)\s*\(`),
		"calls a generated snake_case fixed-form entry point"},
	{regexp.MustCompile(`\bRow[A-Za-z0-9_]*(Load|Save)\s*\(`),
		"calls a generated row entry point"},
	{regexp.MustCompile(`\bTableFixed\b|\b[A-Za-z_][A-Za-z0-9_]*Fixed(BodyBytes|HeaderBytes|LayoutBytes|Plan|Entry|Entries|Report|Reason)\b`),
		"names a generated fixed-form type or layout constant"},
	{regexp.MustCompile(`(?i)#include\s*[<"][^">]*(generated|Table\.h|Fixed\.h|Fixed\.hpp)[^">]*[">]`),
		"includes a generated header"},
	{regexp.MustCompile(`build/[A-Za-z0-9._-]*(generated|fixed|tables|ebin)[A-Za-z0-9._-]*[/"'\s]|tables-generated|tables-ebin|/generated/|\bgenerated/`),
		"reaches into the generated artifact tree"},
	{regexp.MustCompile(`--extern\s+\w+=|(-L|-I)\s*build/`),
		"links against a generated unit"},
	{regexp.MustCompile(`(?i)\bbin/schema\s+generate\b|\bbin/schema\b|"schema"\s*,\s*"generate"|'schema'\s*,\s*'generate'`),
		"runs the generator as a subprocess and inspects its output"},
}

// goStdRoots are the single-segment Go import paths that are NOT a generated
// unit. A conformance cell's Go test imports its generated unit by a bare path
// (`import "streamdemo"`), which is how the leg is wired; everything else it
// imports is the standard library. So "a single-segment import that is not one
// of these" is a generated unit, and the check does not need to know the unit's
// name in advance.
var goStdRoots = map[string]bool{
	"bufio": true, "bytes": true, "cmp": true, "container": true, "context": true,
	"crypto": true, "embed": true, "encoding": true, "errors": true, "expvar": true,
	"flag": true, "fmt": true, "hash": true, "html": true, "image": true, "io": true,
	"log": true, "maps": true, "math": true, "mime": true, "net": true, "os": true,
	"path": true, "plugin": true, "reflect": true, "regexp": true, "runtime": true,
	"slices": true, "sort": true, "strconv": true, "strings": true, "sync": true,
	"syscall": true, "testing": true, "time": true, "unicode": true, "unsafe": true,
}

// importRootJavaRE is a Java- or Dart-style import's ROOT package.
var importRootJavaRE = regexp.MustCompile(`(?m)^\s*import\s+([a-z][A-Za-z0-9_]*)\.`)

// importRootGoRE is a bare single-segment Go import path inside an import block.
var importRootGoRE = regexp.MustCompile(`(?m)^\s*"([a-z][a-z0-9_]*)"\s*$`)

// platformRoots are the Java/Dart import roots that are the platform, not a
// generated unit.
var platformRoots = map[string]bool{"java": true, "javax": true, "jdk": true, "dart": true}

// importsGeneratedUnit answers whether the text imports a generated unit by
// name. It is a function and not another pattern in the list because the rule
// is a negative one -- an import root that is NOT the platform -- and Go's
// regexp has no lookahead to say that.
func importsGeneratedUnit(text string) bool {
	for _, m := range importRootJavaRE.FindAllStringSubmatch(text, -1) {
		if !platformRoots[m[1]] {
			return true
		}
	}
	for _, m := range importRootGoRE.FindAllStringSubmatch(text, -1) {
		if !goStdRoots[m[1]] {
			return true
		}
	}
	return false
}

// runLineRE is the RESULT's `run:` fact: the command that compiles, links and
// runs the added test. It is part of the symbol evidence because several legs
// reach their generated unit through the LINK and not through a name in the
// source (`rustc --extern vnew_x=build/tables-generated-rust/...`), and a check
// that read only the source would call those tests self-checks.
var runLineRE = regexp.MustCompile(`(?im)(^|;)\s*run:\s*([^;\n]*)`)

// RunLine is the RESULT's run: command, or "".
func RunLine(result string) string {
	if m := runLineRE.FindStringSubmatch(result); m != nil {
		return strings.TrimSpace(m[2])
	}
	return ""
}

// tells is the negative side: the shapes a self-check takes. Each was read out
// of a pull request a friend classified, and the citation is kept beside the
// pattern so that a future widening argues with the evidence and not with me.
var tells = []tell{
	{regexp.MustCompile(`(?i)\b(check|assert|assertTrue|expect|require)\s*\(\s*true\s*[,)]`),
		"asserts a literal true, which can never go red", "schema#1507"},
	{regexp.MustCompile(`(?i)//\s*simulate\b|#\s*simulate\s+(exactly\s+)?what\b`),
		"simulates what the generated code does instead of calling it", "schema#1459, #1482"},
	{regexp.MustCompile(`(?i)verified by (code )?inspection|by inspection[,.]|manually verified`),
		"claims an assertion that is a comment, not code", "schema#1493"},
	{regexp.MustCompile(`(?i)import nothing from generated|standalone:\s*import nothing|no generated (code|headers?) (is |are )?(needed|imported|used)`),
		"says outright that it touches no generated code", "schema#1561"},
	{regexp.MustCompile(`(?i)//\s*-*\s*runtime types \(from|re-?declare[sd]? (the )?(generated|runtime) type|// (the )?generated type, copied`),
		"re-declares the type it claims to test", "schema#1539"},
	{regexp.MustCompile(`(?i)\b(LAYOUT_AS_WRITTEN|HASH_CONSTANT|REF_HASH_(LO|HI)|[a-z0-9_]*_(layout|hash)\s*\[\s*\]\s*=)`),
		"compares against a reference pasted into the test, not read live", "schema#1497, #1561, #1555"},
	{regexp.MustCompile(`(?i)(pasted|copied|hand-?typed|transcribed) from (the )?(comment|header|dump|spec)`),
		"names its own reference as pasted", "schema#1497"},
	{regexp.MustCompile(`(?i)\b(MatchString|regexp\.Must|re\.search|\.match\()\b[^\n]*\b(codeStr|generated|source|src)\b|\b(codeStr|genSrc)\b`),
		"reads the generated file as text and regexes it instead of running it", "schema#1486"},
	{regexp.MustCompile(`(?i)\bfn\s+[a-z0-9_]*_fixed_(hash|load|save)\s*\(|\bfunc\s+[a-z][A-Za-z0-9]*(Wire|Report)\s*\(\s*\)\s*[A-Za-z]|\b(fnv1a64|leb128|read_leb|readLEB)\b[^\n]*\{`),
		"reimplements the generated routine in the test", "schema#1469, #1506, #1539, #1561"},
}

// addedLineRE strips a unified diff to the lines the pull request ADDS. The
// file header (`+++ b/...`) is not an added line.
var addedLineRE = regexp.MustCompile(`(?m)^\+(?:[^+].*|)$`)

// AddedLines is the text of the diff's added lines, one per line.
func AddedLines(diff string) string {
	var b strings.Builder
	for _, m := range addedLineRE.FindAllString(diff, -1) {
		b.WriteString(strings.TrimPrefix(m, "+"))
		b.WriteByte('\n')
	}
	return b.String()
}

// AddedLinesOutside is AddedLines over only the files whose path does not
// contain skip. The tells are read out of what a card's test DOES, and a
// testdata fixture that quotes a self-check (this package's own cells, for
// one) is evidence about somebody else's test, not this one's (#2621: the pass
// self-convicted on its own testdata).
func AddedLinesOutside(diff, skip string) string {
	if skip == "" {
		return AddedLines(diff)
	}
	var b strings.Builder
	keep := true
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			keep = !strings.Contains(line, skip)
			continue
		}
		if strings.HasPrefix(line, "+++ ") {
			keep = keep && !strings.Contains(line, skip)
			continue
		}
		if keep && strings.HasPrefix(line, "+") {
			b.WriteString(strings.TrimPrefix(line, "+"))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// tellSkipped is a file the tells are not read in: a fixture under any
// testdata/ directory, or a page under docs/. Both quote the self-check shapes
// because they ARE the specimens -- this package's own cells and its docs table
// carry check(true, ...) -- and a tell read there convicts the pass of its
// evidence corpus (#2621: #2594 flagged itself on both).
func tellSkipped(path string) bool {
	return strings.HasPrefix(path, "testdata/") || strings.Contains(path, "/testdata/") || strings.HasPrefix(path, "docs/")
}

// TellText is the added lines the tells and the generated-symbol families are
// read in: AddedLines over every file tellSkipped does not skip.
func TellText(diff string) string {
	var b strings.Builder
	keep := true
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			keep = !tellSkipped(diffGitPath(line))
			continue
		}
		// The file header; AddedLines drops every "++" line the same way.
		if strings.HasPrefix(line, "+++ ") {
			if p := strings.TrimPrefix(line, "+++ "); p != "/dev/null" {
				keep = !tellSkipped(strings.TrimPrefix(p, "b/"))
			}
			continue
		}
		if keep && strings.HasPrefix(line, "+") {
			b.WriteString(strings.TrimPrefix(line, "+"))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// diffGitPath is the new-side path of a `diff --git a/<p> b/<p>` header.
func diffGitPath(line string) string {
	if i := strings.LastIndex(line, " b/"); i >= 0 {
		return strings.TrimSpace(line[i+len(" b/"):])
	}
	return ""
}

// DiffFiles is the changed paths a unified diff names, for a caller that has a
// diff and no file list.
var diffFileRE = regexp.MustCompile(`(?m)^\+\+\+ b/(.+)$`)

// DiffFiles reads the changed paths out of a unified diff.
func DiffFiles(diff string) []string {
	out := make([]string, 0)
	for _, m := range diffFileRE.FindAllStringSubmatch(diff, -1) {
		if p := strings.TrimSpace(m[1]); p != "" && p != "/dev/null" {
			out = append(out, p)
		}
	}
	return out
}

// symbolCheck is the two-sided answer. When the card named a SYMBOL, that exact
// symbol must appear in the added lines -- the card's word beats the family --
// and the tells still apply on top of it.
func symbolCheck(pr PR, card Card) Check {
	// The check is calibrated on conformance cells and nothing else. A pull
	// request that is not a cell -- no SYMBOL on a card, no cell leg in its
	// body, no file under test/conformance/ -- has nothing for it to decide,
	// and it answers missing rather than no (#2621: on tool pull requests it
	// said no to every one).
	if card.Symbol == "" && !isCell(pr, card) {
		if c, ok := selfCheckLine(pr.Body); ok {
			return c
		}
		return Check{Missing, "not a conformance cell: no SYMBOL on a card, no cell leg in the body, no file under test/conformance/"}
	}
	added := TellText(pr.Diff)
	if strings.TrimSpace(added) == "" {
		return Check{Missing, "the diff adds no lines outside testdata and docs"}
	}
	if t, ok := firstTell(added); ok {
		return Check{No, fmt.Sprintf("the added test %s (%s)", t.says, t.from)}
	}
	if card.Symbol != "" {
		if strings.Contains(added, card.Symbol) {
			return Check{Yes, "the added test names the card's SYMBOL " + card.Symbol}
		}
		return Check{No, "the added test never names the card's SYMBOL " + card.Symbol}
	}
	evidence := added + "\n" + RunLine(resultText(pr, card))
	for _, g := range generated {
		if g.re.MatchString(evidence) {
			return Check{Yes, "the added test " + g.says}
		}
	}
	if importsGeneratedUnit(evidence) {
		return Check{Yes, "the added test imports a generated unit by name"}
	}
	return Check{No, "the added test reaches no generated symbol: no fixed-form entry point, no generated include, import or artifact path, no generator subprocess"}
}

// selfCheckRE is the SELF-CHECK line a harvested card's body carries (#3712):
// `SELF-CHECK: <pass|fail|not-run> (<the card's TEST or none>)`, the result of
// the card wrapper's own run of the card's TEST line.
var selfCheckRE = regexp.MustCompile(`(?m)^\s*SELF-CHECK:[ \t]*([A-Za-z-]+)[ \t]*(.*)$`)

// selfCheckLine reads a non-cell pull request's SELF-CHECK line: pass is
// yes, fail is no, anything else (not-run) is missing; ok is false when the
// body has no such line.
func selfCheckLine(body string) (Check, bool) {
	m := selfCheckRE.FindStringSubmatch(strings.ReplaceAll(body, "\r\n", "\n"))
	if m == nil {
		return Check{}, false
	}
	test := strings.TrimSpace(m[2])
	switch strings.ToLower(m[1]) {
	case "pass":
		return Check{Yes, "SELF-CHECK: the card wrapper ran the card's TEST " + test + " at head: pass"}, true
	case "fail":
		return Check{No, "SELF-CHECK: the card wrapper ran the card's TEST " + test + " at head: fail"}, true
	}
	return Check{Missing, "SELF-CHECK: " + m[1] + " " + test + ": the card's TEST did not run"}, true
}

// firstTell is the first self-check tell present in the added lines, in list
// order, so the reason is stable across runs.
func firstTell(added string) (tell, bool) {
	for _, t := range tells {
		if t.re.MatchString(added) {
			return t, true
		}
	}
	return tell{}, false
}
