// Package prereview is the mechanical first pass over one pull request: five
// yes/no checks that need NO model and no judgement, decided from the card, the
// pull request's RESULT text, the diff, and the check rollup at the exact head.
//
// It exists because the expensive part of a read is a friend's attention, and
// four of the things a friend keeps catching are things a regular expression can
// catch first (nova-tools #2565; rowan-new memory jev-first-pass-not-landing).
// The pass ADVISES and bounces: it never lands anything. The lander's rule is
// unchanged -- one typed NON-author friend line at 8+ at head -- and the account
// this pass writes under is not a friend.
//
// The checks:
//
//   - symbol: the added test exercises generated code, and carries none of the
//     self-check tells the friend-classified corpus names (checks_symbol.go).
//   - paths:  every changed file is inside the card's PATHS globs.
//   - done:   line 2 of the RESULT is the bare word DONE.
//   - claims: every file the RESULT's `files:` line names is in the diff.
//   - ci:     ci-ok at the exact head is success, when checks_enabled names ci.
//     Red or missing is a fail that names the failing jobs (nova-tools #2704).
//     A missing answer is neutral under pass_above, so an absent ci-ok is a
//     fail, not a missing. The check is off by default: a repository with no
//     ci-ok job must not bounce on a default run (nova-tools #2712).
//
// A check with nothing to decide on answers MISSING, never `no`: an absent card
// is not a failed card, and a row that prints `no` for a check it could not run
// is the bad-check shape the 2026-09-21 lander review named. MISSING holds the
// pull request exactly as `no` does -- it just says something different about
// why.
package prereview

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// Result is one check's answer. Yes and No are decided; Missing is the answer
// of a check that had no evidence to decide on, and it is NOT No.
type Result string

// The three answers a check gives.
const (
	Yes     Result = "yes"
	No      Result = "no"
	Missing Result = "missing"
)

// Check is one answer and the one line that says why.
type Check struct {
	Result Result
	Reason string
}

// Checks are the mechanical checks, in the fixed order the line prints them.
type Checks struct {
	Symbol Check
	Paths  Check
	Done   Check
	Claims Check
	CI     Check
	// Base is the rubric's base gate: the pull request targets its
	// repository's trunk and merges into it cleanly.
	Base Check
}

// all is the mechanical checks in print order, so the renderer and the verdict
// read the same list and cannot drift apart.
func (c Checks) all() []struct {
	name string
	c    Check
} {
	return []struct {
		name string
		c    Check
	}{
		{"symbol", c.Symbol},
		{"paths", c.Paths},
		{"done", c.Done},
		{"claims", c.Claims},
		{"ci", c.CI},
		{"base", c.Base},
	}
}

// named is the mechanical checks under the names the JEV line prints them by:
// donewhen (the RESULT's DONE line), selfcheck (the symbol check), paths,
// claims, ci (the rollup at the exact head).
func (c Checks) named() []struct {
	name string
	c    Check
} {
	return []struct {
		name string
		c    Check
	}{
		{"donewhen", c.Done},
		{"selfcheck", c.Symbol},
		{"paths", c.Paths},
		{"claims", c.Claims},
		{"ci", c.CI},
		{"base", c.Base},
	}
}

// Evidence is one line per check, in the JEV line's order: the name, the
// answer and the reason the answer rests on.
func (c Checks) Evidence() []string {
	out := make([]string, 0, 5)
	for _, e := range c.named() {
		r := e.c.Result
		if r == "" {
			r = Missing
		}
		out = append(out, fmt.Sprintf("%s: %s -- %s", e.name, word(r), e.c.Reason))
	}
	return out
}

// Clear reports whether every check answered Yes. A Missing does not clear: a
// check that could not run has not said the pull request is sound.
func (c Checks) Clear() bool {
	for _, e := range c.all() {
		if e.c.Result != Yes {
			return false
		}
	}
	return true
}

// Field renders the checks= value: symbol:yes,paths:no,done:yes,claims:missing.
func (c Checks) Field() string {
	parts := make([]string, 0, 4)
	for _, e := range c.all() {
		r := e.c.Result
		if r == "" {
			r = Missing
		}
		parts = append(parts, e.name+":"+string(r))
	}
	return strings.Join(parts, ",")
}

// Why is the one-line reason: the first check that did not answer Yes, in print
// order, or the clearing sentence when all four did.
func (c Checks) Why() string {
	for _, e := range c.all() {
		if e.c.Result != Yes {
			return e.name + ": " + e.c.Reason
		}
	}
	return "five mechanical checks pass"
}

// PR is the public evidence one pass reads. Diff is the unified diff, Files the
// changed paths, Body the pull request body (which is where a harvested card's
// RESULT lives today).
type PR struct {
	Repo   string
	Number int
	Head   string
	Title  string
	Body   string
	Files  []string
	Diff   string
	// Base is the base gate status: ok, behind, or conflict.
	Base string
	// BaseRef is the branch the pull request targets ("" when unread).
	BaseRef          string
	Mergeable        string
	MergeStateStatus string
	// Checks is the rollup read at Head. A run whose HeadSHA is not Head is
	// not this head's evidence.
	Checks []CheckRun
	// ChecksUnread says the rollup was never read (a cached pull request
	// with no check-runs document): the ci check answers missing, not no.
	ChecksUnread bool
}

// Card is the bound the pass judges against: the PATHS globs the card declared
// and the generated SYMBOL its test was told to call. Origin says where each
// came from, because a bound inferred from the pull request's own body is a
// weaker bound than one the card carried and the line must not pretend
// otherwise ("the card always carries BASE/base-sha/PATHS", Glenn 2026-09-21).
type Card struct {
	Path       string
	Paths      []string
	Symbol     string
	PathsFrom  string // "card", "pr-body-cell", "pr-body-branch" or "none"
	SymbolFrom string // "card" or "none"
	Result     string // the card's own RESULT text, when the card carried one
	// DoneWhen is the DONE-WHEN sentence the pull request body states, when
	// no RESULT carries a DONE line to read.
	DoneWhen string
}

// cardKeyRE is the card header's key line, the grammar internal/swarm's lint
// header and internal/pulse's card gate already read: `KEY: value` at column 0.
var cardKeyRE = regexp.MustCompile(`^([A-Z][A-Z-]*):[ \t]*(.*)$`)

// ParseCard reads a card file's text for the two fields this pass needs. An
// unknown key is read past, exactly as the other two readers of this grammar do.
func ParseCard(path, body string) Card {
	c := Card{Path: path, PathsFrom: "none", SymbolFrom: "none"}
	for _, line := range strings.Split(body, "\n") {
		m := cardKeyRE.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		key, value := m[1], strings.TrimSpace(m[2])
		switch key {
		case "PATHS":
			if value == "" || strings.EqualFold(value, "none") {
				continue
			}
			for _, g := range strings.Split(value, ",") {
				if g = strings.TrimSpace(g); g != "" {
					c.Paths = append(c.Paths, g)
				}
			}
			if len(c.Paths) > 0 {
				c.PathsFrom = "card"
			}
		case "SYMBOL":
			if value != "" {
				c.Symbol, c.SymbolFrom = value, "card"
			}
		}
	}
	if i := strings.Index(body, "RESULT"); i >= 0 {
		c.Result = body[i:]
	}
	return c
}

// cellRE is the `cell: <lang>/<row>` fact a harvested cell's RESULT carries. It
// is matched ANYWHERE in the body and not only at the start of a line: two of
// the 122 cells (#1541, #1548) fold every RESULT fact onto one semicolon-joined
// line, and a start-of-line rule read those two as having no cell at all.
var cellRE = regexp.MustCompile(`(?i)\bcell:\s*([A-Za-z0-9+#]+)\s*/\s*([A-Za-z0-9]+)`)

// branchCellRE is the second source for the same fact: `BRANCH rowan/cell-<lang>-<row>`.
var branchCellRE = regexp.MustCompile(`(?i)^BRANCH\s+\S*?cell-([A-Za-z0-9+#]+)-([A-Za-z0-9]+)\s*$`)

// InferCard is the bound when no card file was given: the conformance leg the
// pull request's own RESULT names. It is a REAL bound and not a tautology -- it
// is derived from the cell's identity, never from the list of files the diff
// happens to touch -- but it is a weaker one than a card's PATHS line, so
// PathsFrom says which it is and the caller prints that.
func InferCard(pr PR) Card {
	result := pr.Body
	if i := strings.Index(result, "RESULT"); i >= 0 {
		result = result[i:]
	}
	c := Card{PathsFrom: "none", SymbolFrom: "none", Result: result}
	lang := ""
	from := ""
	if m := cellRE.FindStringSubmatch(pr.Body); m != nil {
		lang, from = strings.ToLower(m[1]), "pr-body-cell"
	} else {
		for _, line := range strings.Split(pr.Body, "\n") {
			if m := branchCellRE.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				lang, from = strings.ToLower(m[1]), "pr-body-branch"
				break
			}
		}
	}
	if lang != "" {
		c.Paths = []string{"test/conformance/" + lang + "/**"}
		c.PathsFrom = from
	} else if paths := BodyPaths(pr.Body); len(paths) > 0 {
		c.Paths, c.PathsFrom = paths, "pr-body-paths"
	}
	c.DoneWhen = BodyDoneWhen(pr.Body)
	return c
}

// pathsLineRE is a body's PATHS line: `PATHS: a, b` at the start of a line,
// markdown emphasis and a bullet allowed (nova-tools #2536: 163 of 397 bodies
// carry one, and the paths check read none of them).
var pathsLineRE = regexp.MustCompile(`^\s*(?:[-*>]\s*)?(?:\*\*|__)?PATHS(?:\*\*|__)?\s*(?:\([^)]*\))?\s*:(?:\*\*|__)?\s*(.*)$`)

// pathTokenRE is one path-shaped token: a repository-relative path, a
// directory with a trailing slash, a glob, or a file name with an extension.
var pathTokenRE = regexp.MustCompile(`^[A-Za-z0-9_.*{}\[\]-]+(?:/[A-Za-z0-9_.*{}\[\]-]*)*$`)

// BodyPaths reads the PATHS a pull request body declares: the tokens of its
// PATHS line (or of the bullet lines under a bare `PATHS:`) that are shaped
// like paths. A directory `a/b/` means `a/b/**`; a bare file name means that
// file anywhere. Prose on the line is read past; nil when the body has none.
func BodyPaths(body string) []string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	var toks []string
	for i, line := range lines {
		m := pathsLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		rest := strings.TrimSpace(m[1])
		if rest == "" {
			for _, next := range lines[i+1:] {
				t := strings.TrimSpace(next)
				if !strings.HasPrefix(t, "- ") && !strings.HasPrefix(t, "* ") {
					break
				}
				toks = append(toks, pathTokens(t[2:])...)
			}
		} else {
			toks = append(toks, pathTokens(rest)...)
		}
		break
	}
	return toks
}

// pathTokens is the path-shaped words of one PATHS value.
func pathTokens(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == ',' || r == ';' || r == '(' || r == ')' || r == '`' || r == '\t'
	}) {
		w = strings.TrimRight(strings.Trim(w, "*_\"'"), ".:")
		if w == "" || strings.EqualFold(w, "none") || !pathTokenRE.MatchString(w) {
			continue
		}
		if !strings.Contains(w, "/") && !strings.Contains(w, ".") {
			continue // a word, not a path
		}
		if strings.Contains(w, "/") && !strings.Contains(strings.SplitN(w, "/", 2)[0], ".") && strings.HasSuffix(w, "/") {
			w += "**"
		} else if !strings.Contains(w, "/") {
			w = "**/" + w
		}
		out = append(out, w)
	}
	return out
}

// doneWhenRE is a body's DONE-WHEN line (a heading or bold allowed).
var doneWhenRE = regexp.MustCompile(`(?i)^\s*(?:#+\s*|[-*>]\s*)?(?:\*\*|__)?DONE-WHEN(?:\*\*|__)?[^:]*:(?:\*\*|__)?\s*(.*)$`)

// BodyDoneWhen is the DONE-WHEN a pull request body states: the rest of the
// line, or the next non-blank line under a bare heading; "" when none.
func BodyDoneWhen(body string) string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	for i, line := range lines {
		m := doneWhenRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if v := strings.TrimSpace(m[1]); v != "" {
			return v
		}
		for _, next := range lines[i+1:] {
			if t := strings.TrimSpace(next); t != "" {
				return t
			}
		}
	}
	return ""
}

// testNameRE is a Go or bats test a DONE-WHEN names.
var testNameRE = regexp.MustCompile(`\bTest[A-Z0-9_][A-Za-z0-9_]*`)

// resultText is the RESULT the checks read: the card's own when a card carried
// one, and the pull request body otherwise (a harvested card's RESULT is the
// body's first lines).
func resultText(pr PR, card Card) string {
	if strings.TrimSpace(card.Result) != "" {
		return card.Result
	}
	return pr.Body
}

// Mechanical runs the checks. It makes no network call and asks no model: the
// caller has already read the rollup onto pr.Checks.
func Mechanical(pr PR, card Card) Checks {
	return Checks{
		Symbol: symbolCheck(pr, card),
		Paths:  pathsCheck(pr, card),
		Done:   doneCheck(pr, card),
		Claims: claimsCheck(pr, card),
		CI:     ciCheck(pr),
		Base:   baseCheck(pr),
	}
}

// Trunks are the branches a pull request may target without being stacked:
// dev (nova-tools, nova-work), main (rowan-tools and most repositories) and
// fixed-table-form (schema), the read rubric's base gate.
var Trunks = []string{"dev", "main", "fixed-table-form"}

// baseCheck is the read rubric's base gate: a pull request stacked on another
// branch, or one that conflicts with its base, is no; one GitHub (or a local
// merge-tree) called mergeable onto a trunk is yes; anything unread is missing.
func baseCheck(pr PR) Check {
	ref := strings.TrimSpace(pr.BaseRef)
	if ref != "" {
		trunk := false
		for _, t := range Trunks {
			if ref == t {
				trunk = true
			}
		}
		if !trunk {
			return Check{No, fmt.Sprintf("stacked: the base is %s, not a trunk (%s)", oneline.Field(ref), strings.Join(Trunks, ", "))}
		}
	}
	switch BaseGateFromGH(pr.Mergeable, pr.MergeStateStatus) {
	case BaseConflict:
		return Check{No, fmt.Sprintf("conflicts with its base (mergeable=%s, mergeStateStatus=%s)", oneline.Field(pr.Mergeable), oneline.Field(pr.MergeStateStatus))}
	}
	if strings.EqualFold(strings.TrimSpace(pr.Mergeable), "MERGEABLE") {
		if ref == "" {
			return Check{Missing, "mergeable, but the base branch was not read"}
		}
		return Check{Yes, "mergeable onto " + ref}
	}
	return Check{Missing, "mergeability unknown (mergeable=" + oneline.Field(pr.Mergeable) + ")"}
}

// pathsCheck: every changed file is inside one of the card's PATHS globs. The
// matcher is internal/hygiene's, the same one the harness wall and `cut` use, so
// a path cannot mean one thing here and another there.
func pathsCheck(pr PR, card Card) Check {
	if len(card.Paths) == 0 {
		return Check{Missing, "no PATHS on the card and no cell leg in the pull request body to infer one from"}
	}
	if len(pr.Files) == 0 {
		return Check{Missing, "the pull request lists no changed files"}
	}
	outside := make([]string, 0)
	for _, f := range pr.Files {
		in := false
		for _, g := range card.Paths {
			if hygiene.MatchGlob(g, f) {
				in = true
				break
			}
		}
		if !in {
			outside = append(outside, f)
		}
	}
	if len(outside) == 0 {
		return Check{Yes, fmt.Sprintf("all %d changed files inside %s", len(pr.Files), strings.Join(card.Paths, " "))}
	}
	sort.Strings(outside)
	if card.PathsFrom == "pr-body-paths" {
		// The read rubric's scope gate: product code outside the PATHS a
		// body declares is red; a test-only file the change needs is a
		// one-point deduction for the reader, not a gate.
		product := make([]string, 0, len(outside))
		for _, f := range outside {
			if !testOnlyPath(f) {
				product = append(product, f)
			}
		}
		if len(product) == 0 {
			return Check{Yes, fmt.Sprintf("every product file inside %s; %d test-only file(s) outside, first %s", strings.Join(card.Paths, " "), len(outside), outside[0])}
		}
		outside = product
	}
	return Check{No, fmt.Sprintf("%d of %d changed files outside %s, first %s",
		len(outside), len(pr.Files), strings.Join(card.Paths, " "), outside[0])}
}

// doneCheck: line 2 of the RESULT is the bare word DONE. Not "DONE." and not
// "DONE (with notes)": the line is a machine's word, and a card that has to
// qualify it has not finished.
func doneCheck(pr PR, card Card) Check {
	// A pull request whose body has no line starting RESULT is not a
	// harvested card -- a friend's hand-written branch, a landing receipt --
	// and it has no DONE line to read. That is missing, not a failure: on the
	// first posted run's window the rule read line 2 of nineteen such bodies
	// and "failed" every one (a heading, a blank, a bullet).
	if card.Path == "" && !hasResultLine(pr.Body) {
		if card.DoneWhen != "" {
			return doneWhenCheck(pr, card.DoneWhen)
		}
		return Check{Missing, "the body has no line starting RESULT, so there is no DONE line to read (not a harvested card)"}
	}
	// A blank line under the RESULT line is formatting, not a missing DONE
	// (16 of 24 bounces on 2026-09-24 were `RESULT line 2 is ""`):
	// typedrec.LineTwo reads the first non-blank line after RESULT.
	got, present, done := typedrec.LineTwo(resultText(pr, card))
	if !present {
		return Check{Missing, "the RESULT has no second line"}
	}
	if done {
		return Check{Yes, "RESULT line 2 is bare DONE"}
	}
	return Check{No, "RESULT line 2 is \"" + oneline.Escape(oneline.Cap(got, 80)) + "\", not bare DONE"}
}

// testOnlyPath is a file that only tests read: a Go _test file, testdata, or a
// bats file under tests/.
func testOnlyPath(f string) bool {
	return strings.HasSuffix(f, "_test.go") || strings.Contains(f, "/testdata/") || strings.HasPrefix(f, "testdata/") ||
		strings.HasSuffix(f, ".bats") || strings.HasPrefix(f, "tests/")
}

// doneWhenCheck reads a body's DONE-WHEN: every Go test it names is a test the
// diff adds or changes (a `func TestX(` on an added line) -- yes; a named test
// the diff does not touch may already exist on the base, so that is missing,
// never no; a DONE-WHEN that names no test is missing.
func doneWhenCheck(pr PR, doneWhen string) Check {
	names := testNameRE.FindAllString(doneWhen, -1)
	if len(names) == 0 {
		return Check{Missing, "the body's DONE-WHEN names no Go test to find in the diff"}
	}
	added := AddedLines(pr.Diff)
	var absent []string
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		if !strings.Contains(added, "func "+n+"(") {
			absent = append(absent, n)
		}
	}
	if len(absent) > 0 {
		return Check{Missing, fmt.Sprintf("the DONE-WHEN names %s, which the diff does not add (it may be on the base)", strings.Join(absent, ", "))}
	}
	return Check{Yes, fmt.Sprintf("the diff adds every test the DONE-WHEN names (%d)", len(seen))}
}

// filesClaimRE is the RESULT's `files:` claim. The value runs to the next
// semicolon or the end of the line, because a folded RESULT joins its facts with
// semicolons on one line.
var filesClaimRE = regexp.MustCompile(`(?im)(^|;)\s*files:\s*([^;\n]*)`)

// pathish keeps the tokens on a files: line that are paths. A token with no
// slash and no dot is prose ("and", "plus"), not a file.
func pathish(tok string) bool {
	tok = strings.Trim(tok, "`\"'()[],")
	if tok == "" {
		return false
	}
	return strings.Contains(tok, "/") || strings.Contains(tok, ".")
}

// claimsCheck: every file the RESULT's `files:` line names is in the diff. A
// RESULT that names a file it did not change is a RESULT written from intent
// rather than from the diff, and it is the cheapest possible tell that the rest
// of the RESULT may be the same.
func claimsCheck(pr PR, card Card) Check {
	text := resultText(pr, card)
	m := filesClaimRE.FindStringSubmatch(text)
	if m == nil {
		return Check{Missing, "the RESULT names no files: line"}
	}
	changed := make(map[string]bool, len(pr.Files))
	for _, f := range pr.Files {
		changed[f] = true
	}
	claimed, absent := 0, make([]string, 0)
	for _, raw := range strings.FieldsFunc(m[2], func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		tok := strings.Trim(strings.TrimSpace(raw), "`\"'()[],")
		if !pathish(tok) {
			continue
		}
		claimed++
		if !changed[tok] {
			absent = append(absent, tok)
		}
	}
	if claimed == 0 {
		return Check{Missing, "the RESULT's files: line names no path"}
	}
	if len(absent) == 0 {
		return Check{Yes, fmt.Sprintf("all %d files the RESULT claims are in the diff", claimed)}
	}
	return Check{No, fmt.Sprintf("%d of %d files the RESULT claims are not in the diff: %s",
		len(absent), claimed, oneline.Field(strings.Join(absent, ",")))}
}

// isCell reports whether the pull request is a conformance cell: its paths were
// inferred from a cell leg, or it changes a file under test/conformance/.
func isCell(pr PR, card Card) bool {
	if card.PathsFrom == "pr-body-cell" || card.PathsFrom == "pr-body-branch" {
		return true
	}
	for _, f := range pr.Files {
		if strings.HasPrefix(f, "test/conformance/") {
			return true
		}
	}
	return false
}

// hasResultLine reports whether any line of the body starts with RESULT (a
// leading quote or backtick allowed: harvest has written both).
func hasResultLine(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimLeft(strings.TrimSpace(line), "\"'`"), "RESULT") {
			return true
		}
	}
	return false
}
