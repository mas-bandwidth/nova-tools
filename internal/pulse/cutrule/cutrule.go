// Package cutrule is the rule kind of nova-pulse cut: one read card per numbered
// rule of a spec file.
//
// It replaces the hand loop's tools22/mkrule.sh and its RULES.tsv bookkeeping.
// The numbered rules of docs/SPEC-<X>.md — the lines `grep -cE "^[0-9]+\. "`
// counts — are cut mechanically, one read card each, and every card names the
// rule's own number, its line in the spec file, and the package the rule's text
// names, so a read is bounded to the code it holds the rule against. A rule
// whose text names no package is still cut, with `-` in its place: the card
// names what the rule itself names, and the worker says the package in the
// verdict line.
//
// The cards are read cards: text-only, the no-build line, one verdict line back.
// The number on every card comes only from the queue state file under its lock
// (internal/pulse number.go) — this package numbers nothing itself, and there is
// no flag a number can be passed in by.
//
// Flag parsing lives in cmd/nova-pulse, the same place the other kinds' parsing
// lives; this package is the verb apart from the flags, so a test can drive it
// with a fake spec and a fake queue.
package cutrule

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/lanes"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// ruleLine matches a numbered rule — the same lines `grep -cE "^[0-9]+\. "`
// counts on the spec file, so the cut's card count is checkable by hand against
// the file it was cut from.
var ruleLine = regexp.MustCompile(`^([0-9]+)\. `)

// pkgPath is a repository package path (internal, cmd or lisp) in a rule's
// text: the package the card holds the rule against.
var pkgPath = regexp.MustCompile(`(?:internal|cmd|lisp)/[A-Za-z0-9_./-]+`)

// ruleTextMax is the ceiling on the rule text a card quotes, the packet law's
// 400-byte evidence line: a rule longer than this is quoted with its head and
// the dropped count, and the spec line on the card is the whole truth.
const ruleTextMax = 400

// readSteps is the read family's step budget, the eight turns cutkind.go's
// cutKindSteps gives a read.
const readSteps = 8

// CutRuleInput is everything the rule cutter takes, held apart from flag parsing
// so a test can drive it with a fake spec and a fake queue.
type CutRuleInput struct {
	Spec   string // path to the spec file, docs/SPEC-<X>.md, whose numbered rules are cut
	Repo   string // owner/name; line 1 of every card names the repo
	Out    string // the directory the card files go into
	Queue  string // the queue directory holding the state file the numbers come from
	Stdout io.Writer
	Stderr io.Writer
}

// specRule is one numbered rule of the spec: the number on its own first line,
// that line's 1-based position in the file, its text (the numbered line and its
// continuations), and the package the text names, `-` when it names none.
type specRule struct {
	Number  int
	Line    int
	Text    string
	Package string
}

// CutRule writes one read card per numbered rule of the spec file and returns
// the exit code: 0 when every rule was cut, 2 when the invocation was refused.
// The cards go into --out numbered from the queue state file, one line per card
// on stdout plus one summary line, and land in the queue's green lane — a read
// is an already-approved read.
func CutRule(in CutRuleInput) int {
	if problem := cutRuleProblem(in); problem != "" {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: %s\n", problem)
		return 2
	}
	raw, err := os.ReadFile(in.Spec)
	if err != nil {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: --spec %s: %s (pass a readable docs/SPEC-<X>.md of numbered rules)\n", oneline.Field(in.Spec), oneline.Err(err))
		return 2
	}
	rules := parseSpecRules(string(raw))
	if len(rules) == 0 {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: --spec %s: no numbered rules (pass a docs/SPEC-<X>.md whose rules are numbered `1. ` lines)\n", oneline.Field(in.Spec))
		return 2
	}
	if err := os.MkdirAll(in.Out, 0o755); err != nil {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: --out %s: %s (pass a directory cut may create)\n", oneline.Field(in.Out), oneline.Err(err))
		return 2
	}
	specBase := filepath.Base(in.Spec)
	var written []lanes.Card
	for _, r := range rules {
		n, err := pulse.NextCardNumber(in.Queue)
		if err != nil {
			fmt.Fprintf(in.Stderr, "CUT REFUSED: %s (the number comes only from the state file under %s)\n", oneline.Err(err), oneline.Field(in.Queue))
			return 2
		}
		card := renderRuleCard(in.Repo, specBase, n, r)
		name := fmt.Sprintf("card-%d.md", n)
		if err := os.WriteFile(filepath.Join(in.Out, name), []byte(card), 0o644); err != nil {
			fmt.Fprintf(in.Stderr, "CUT REFUSED: card %s: %s (pass a writable --out directory)\n", oneline.Field(name), oneline.Err(err))
			return 2
		}
		// The lane card's kind is read, because the card IS a read card: `rule`
		// is the verb that cut it, not a class the drain knows.
		written = append(written, lanes.Card{ID: fmt.Sprintf("card-%d", n), Kind: "read", Approved: true, Steps: readSteps, Body: card})
		fmt.Fprintf(in.Stdout, "CUT CARD card=%s kind=rule rule=%d line=%d package=%s\n", oneline.Field(name), r.Number, r.Line, oneline.Field(r.Package))
	}
	if _, err := lanes.Write(in.Queue, written); err != nil {
		fmt.Fprintf(in.Stderr, "CUT REFUSED: lanes under %s: %s (pass a writable --queue)\n", oneline.Field(in.Queue), oneline.Err(err))
		return 2
	}
	fmt.Fprintf(in.Stdout, "CUT RULE cards=%d spec=%s out=%s\n", len(written), oneline.Field(in.Spec), oneline.Field(in.Out))
	return 0
}

// cutRuleProblem is every refusal this cutter has, each naming its remedy. The
// spec file itself is read (and its rules counted) by CutRule, so a missing or
// rule-less file is refused there, where the error names the path.
func cutRuleProblem(in CutRuleInput) string {
	switch {
	case strings.TrimSpace(in.Repo) == "":
		return "--repo is required; line 1 of every card names the repo (pass --repo <owner>/<name>)"
	case strings.TrimSpace(in.Queue) == "":
		return "--queue is required; the card number comes only from its state file (pass --queue <dir>)"
	case strings.TrimSpace(in.Out) == "":
		return "--out is required (pass the directory the cards are written into, usually <queue>/pending)"
	case strings.TrimSpace(in.Spec) == "":
		return "--spec is required (pass the docs/SPEC-<X>.md whose numbered rules are cut one read card each)"
	}
	return ""
}

// parseSpecRules returns the spec's numbered rules in document order. A rule is
// a line the ruleLine pattern counts; its text is that line plus the lines that
// follow it up to a blank line, the next rule or the next heading, so a rule
// wrapped over several lines is quoted whole and its package read whole.
func parseSpecRules(text string) []specRule {
	var rules []specRule
	inRule := false
	for i, l := range strings.Split(text, "\n") {
		if m := ruleLine.FindStringSubmatch(l); m != nil {
			n, _ := strconv.Atoi(m[1])
			rules = append(rules, specRule{Number: n, Line: i + 1, Text: l})
			inRule = true
			continue
		}
		t := strings.TrimSpace(l)
		// Blank lines and headings end the active rule
		if t == "" || strings.HasPrefix(t, "#") {
			inRule = false
			continue
		}
		if inRule {
			last := &rules[len(rules)-1]
			last.Text += "\n" + l
		}
	}
	for i := range rules {
		rules[i].Package = rulePackage(rules[i].Text)
	}
	return rules
}

// rulePackage is the first repository package path (internal, cmd or lisp) the
// rule's text names, with a file's last segment dropped — `internal/pulse/wire.go`
// is the package internal/pulse — and `-` when the text names none.
func rulePackage(text string) string {
	m := pkgPath.FindString(text)
	if m == "" {
		return "-"
	}
	if i := strings.LastIndex(m, "/"); strings.Contains(m[i+1:], ".") {
		m = m[:i]
	}
	return strings.TrimRight(m, ".,;:)")
}

// renderRuleCard renders one rule's read card: line 1 the read contract naming
// the rule's number, its line in the spec and its package; the SOURCE line; the
// read instruction with the rule's own text; and the verdict line the card is
// answered by, which names the rule's line and package again.
func renderRuleCard(repo, specBase string, n int, r specRule) string {
	var b strings.Builder
	fmt.Fprintf(&b, "RESULT: CARD-%d read of %s %s rule %d at line %d (%s)\n", n, repoShort(repo), specBase, r.Number, r.Line, r.Package)
	fmt.Fprintf(&b, "SOURCE: %s %s rule %d at line %d\n", repo, specBase, r.Number, r.Line)
	fmt.Fprintf(&b, "Read rule %d of %s at line %d and hold the code it governs against it. Quote the rule beside every line you hold.\n", r.Number, specBase, r.Line)
	fmt.Fprintf(&b, "The rule's text: %s\n", oneline.Cap(oneline.Escape(oneLine(r.Text)), ruleTextMax))
	if r.Package == "-" {
		b.WriteString("The rule names no package; say the package it governs in the verdict line.\n")
	}
	b.WriteString("Do not run go build, go test or any toolchain; read and write only.\n")
	b.WriteString("Write RESULT.md: line 1 exactly the line 1 of this card, line 2 DONE, then exactly one verdict line:\n")
	fmt.Fprintf(&b, "rule%d: APPROVE|HOLD line=%d package=%s\n", r.Number, r.Line, r.Package)
	return b.String()
}

// oneLine flattens a rule's text to the one line a card quotes: runs of blank
// become single spaces.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// repoShort is owner/name as line 1 says it: the name alone, the same shortening
// cutkind.go's line 1 uses.
func repoShort(repo string) string {
	if _, name, ok := strings.Cut(repo, "/"); ok && name != "" {
		return name
	}
	return repo
}
