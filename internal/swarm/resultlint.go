package swarm

import (
	"bytes"
	"regexp"
	"strings"
)

// RESULT LINE 2 IS A GRAMMAR, AND A TEMPLATE COPIED INTO IT IS NOT A DONE (issue #2917).
//
// Line 2 of a card's RESULT.md is its disposition: one of `DONE`, `ABSTAIN <why>`,
// `BLOCKED <why>` (docs/spec-pulse/10-the-card-as-cut-writes-it.md:8), and on the kinds
// that read a rule, `RED <line>`, `GAP <why>` or `CONFORMS <where>`. The harvest counted a
// DONE by asking whether line 2 BEGAN with the word, and on 2026-09-22 eight cards copied
// the card's own template line
//
//	DONE            <- or: ABSTAIN <why> | BLOCKED <why>
//
// verbatim and were counted as done work (rowan-new reports/failed-cards-2026-09-22.md,
// class 10: nx-g39 on space, nx-c37 on vision, rule3-toolwork-158 and rule3-test-168 on
// hulk, rule3-bus-reply-871 on vision, and three more after the snapshot). The `<-` is the
// template's arrow and nothing a disposition ever says, so a line 2 holding it is refused
// whatever word it starts with.
//
// BLOCKED head=<the base-sha> WITH NOTHING ELSE SAID IS A MODEL ERROR, NOT A BLOCK. Five
// cards the same night (class 19: rule3-decide-1996, read3-nova-tools-2522 and 2668,
// schema-cell-dart-hostile-input, schema-read-spec-l1937-java) wrote exactly
// `BLOCKED head=<sha>` where <sha> is the sha= their own line 1 pins. A read card's head IS
// its base; that is the expected state, not an obstacle, and the card is recut on another
// route. A BLOCKED that names head==base AND a reason (`head=205a85684bc0 (javac not
// found)`) is a real block whose reason is the rest of the line, and is not flagged here.
//
// The base is the `sha=` on line 1 (the contract line every card carries), or the
// --base-sha a caller hands over when line 1 lost it. Abbreviations compare by prefix, down
// to seven hex digits, the shortest a card pins.
//
// This is a lint: it reads one file, no model, no repository, and names each defect by
// check. What to DO with a flagged result is the caller's: the harvest does not count it
// as DONE.

// ResultLintRemedies is every check this lint makes, with what the writer should have
// written. One table: a check without a remedy is caught by the test.
var ResultLintRemedies = map[string]string{
	"line2-grammar":        "line 2 is exactly one of `DONE`, `ABSTAIN <why>`, `BLOCKED <why>`, `RED <line>`, `GAP <why>` or `CONFORMS <where>`, at column 0, on its own line",
	"template-copied":      "line 2 holds the template's `<-`: write the ONE disposition you reached, not the template line that lists them; a copied `DONE <- or: ...` is never counted as DONE",
	"blocked-head-is-base": "`BLOCKED head=<base-sha>` says only that head is the base, which is the expected state of a card that has not committed yet; do the card's work, or name the actual obstacle after BLOCKED",
}

// ResultLintFinding is one defect of one RESULT: the check and the line-2 text it saw.
type ResultLintFinding struct {
	Check   string
	Excerpt string
}

// ResultLint is what LintResult found. Disposition is line 2's first word when it is one
// of the grammar's, "" otherwise.
type ResultLint struct {
	Disposition string
	Findings    []ResultLintFinding
}

// CountsAsDone is the one question the harvest asks: is this a DONE it may count?
func (r ResultLint) CountsAsDone() bool {
	return r.Disposition == "DONE" && len(r.Findings) == 0
}

var (
	// resultLine2RE is the grammar. DONE stands alone or is followed by whitespace; every
	// other disposition needs its reason, one non-space character at least.
	resultLine2RE = regexp.MustCompile(`^(?:(DONE)(?:\s|$)|(ABSTAIN|BLOCKED) \S|(RED|GAP|CONFORMS) \S)`)
	// resultLine1ShaRE is the base pin on the contract line: `sha=<hex>`.
	resultLine1ShaRE = regexp.MustCompile(`(?:^|[\s"(])sha=([0-9a-fA-F]{7,40})\b`)
	// resultHeadOnlyRE is a BLOCKED whose whole reason is one head= sha.
	resultHeadOnlyRE = regexp.MustCompile(`^BLOCKED\s+head=([0-9a-fA-F]{7,40})\s*$`)
)

// LintResult lints one RESULT.md's text. baseSHA is the caller's base pin, used when
// line 1 carries no sha=; "" means none was handed over.
func LintResult(raw []byte, baseSHA string) ResultLint {
	lines := bytes.SplitN(raw, []byte("\n"), 3)
	line1 := strings.TrimRight(string(lines[0]), "\r")
	line2 := ""
	if len(lines) > 1 {
		line2 = strings.TrimRight(string(lines[1]), "\r")
	}

	var r ResultLint
	add := func(check string) {
		r.Findings = append(r.Findings, ResultLintFinding{Check: check, Excerpt: line2})
	}

	if m := resultLine2RE.FindStringSubmatch(line2); m != nil {
		for _, w := range m[1:] {
			if w != "" {
				r.Disposition = w
				break
			}
		}
	} else {
		add("line2-grammar")
	}
	if strings.Contains(line2, "<-") {
		add("template-copied")
	}

	if m := resultHeadOnlyRE.FindStringSubmatch(line2); m != nil {
		base := baseSHA
		if s := resultLine1ShaRE.FindStringSubmatch(line1); s != nil {
			base = s[1]
		}
		if sameSHA(m[1], base) {
			add("blocked-head-is-base")
		}
	}
	return r
}

// sameSHA is prefix equality of two hex object names, each at least seven digits.
func sameSHA(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	if len(a) < 7 || len(b) < 7 {
		return false
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	return strings.HasPrefix(b, a)
}
