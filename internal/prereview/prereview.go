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
	// Checks is the rollup read at Head. A run whose HeadSHA is not Head is
	// not this head's evidence.
	Checks []CheckRun
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
	}
	return c
}

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
	}
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
		return Check{Missing, "the body has no line starting RESULT, so there is no DONE line to read (not a harvested card)"}
	}
	text := resultText(pr, card)
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) < 2 {
		return Check{Missing, "the RESULT has no second line"}
	}
	got := strings.TrimSpace(lines[1])
	if got == "DONE" {
		return Check{Yes, "RESULT line 2 is bare DONE"}
	}
	return Check{No, "RESULT line 2 is \"" + oneline.Escape(oneline.Cap(got, 80)) + "\", not bare DONE"}
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
