package swarm

import (
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/mas-bandwidth/nova-tools/fleet"
)

// RULES BY REFERENCE: THE MEMBER INJECTS THE RULES ONCE; THE CARD DOES NOT CARRY THEM.
//
// The owner, 2026-10-02 (nova-tools#5174 rule 6): "Rules by reference: the member injects
// fleet/child-rules.txt once; the card does not carry it; a per-repo rules file for second
// repos." A card's stored brief is the card's own text; the member, at stage time, appends
// the RULES paragraph of the rules file it holds (the fleet/child-rules*.txt of the build it
// runs, package fleet), so the child reads card text + rules, the shape it read when the card
// carried them (docs/SPEC-SPRINT.md, rules by reference; docs/SPEC-CARD-CONTRACT.md).
//
// Which file: the one the card's stream records (`nova-sprint add --rules`, the packet's
// rules), else fleet/child-rules.<repo>.txt when the card's REPO: names a repository that has
// one, else fleet/child-rules.txt (RulesNameFor).

// DefaultRulesName is the held rules file a card is staged with when its stream records none
// and its repository has none of its own.
const DefaultRulesName = "child-rules.txt"

// HeldRulesText is the text of the held rules file name (a base name under fleet/), and
// whether this build holds it.
func HeldRulesText(name string) ([]byte, bool) {
	if name == "" || name != path.Base(name) || !strings.HasPrefix(name, "child-rules") {
		return nil, false
	}
	raw, err := fs.ReadFile(fleet.Rules, name)
	return raw, err == nil
}

// HeldRules is the rule set of the held rules file name, its rules' source fleet/<name>.
func HeldRules(name string) ([]ChildRule, error) {
	raw, ok := HeldRulesText(name)
	if !ok {
		return nil, fmt.Errorf("this build holds no rules file %s (it holds %s)", name, strings.Join(HeldRulesNames(), ", "))
	}
	return ParseChildRules(string(raw), "fleet/"+name)
}

// HeldRulesNames is every rules file this build holds, by base name.
func HeldRulesNames() []string {
	names, _ := fs.Glob(fleet.Rules, "child-rules*.txt") // the pattern is well formed
	return names
}

// RulesNameFor is the held rules file a member injects into a card: the stream's (stream,
// the name its packet carries), else child-rules.<repo>.txt when the brief's REPO: (or
// base-repo:) names a repository this build holds a file for, else DefaultRulesName.
func RulesNameFor(stream, brief string) string {
	return rulesNameIn(stream, brief, func(name string) bool { _, ok := HeldRulesText(name); return ok })
}

// rulesNameIn is RulesNameFor over held, which says whether a rules file is held.
func rulesNameIn(stream, brief string, held func(name string) bool) string {
	if stream != "" {
		return stream
	}
	repo := ReadCardBase([]byte(brief)).Named
	repo = strings.TrimSuffix(path.Base(strings.TrimRight(repo, "/")), ".git")
	if name := "child-rules." + repo + ".txt"; repo != "" && repo != "." && held(name) {
		return name
	}
	return DefaultRulesName
}

// StagedBrief is the brief a child is handed: the card's text, then its rule set's RULES
// paragraph (RulesParagraph), after one blank line. A brief that already quotes every
// sentence of the set (a card written before rules by reference) is handed as it is, and an
// empty brief stays empty: a card with no brief has no task to hold to rules.
func StagedBrief(brief string, rules []ChildRule) string {
	if strings.TrimSpace(brief) == "" || len(rules) == 0 || quotesEvery(brief, rules) {
		return brief
	}
	return strings.TrimRight(brief, "\n") + "\n\n" + strings.TrimSuffix(RulesParagraph(rules), "\n")
}

// quotesEvery reports whether the brief quotes every sentence of rules, blanks folded.
func quotesEvery(brief string, rules []ChildRule) bool {
	text := foldBlanks(brief)
	for _, r := range rules {
		if !strings.Contains(text, foldBlanks(r.Sentence)) {
			return false
		}
	}
	return true
}

// LintCardChildByReference is the child-rule findings for a card whose rules the member
// injects at stage time: the card is linted as the child is handed it (StagedBrief), so a
// card that does not carry the rules passes their presence, and a line of the card that
// contradicts them (a forbidden command, an unfilled Libraries considered line) is still
// refused. An empty card is still the one finding EmptyCardCheck.
func LintCardChildByReference(raw []byte, rules []ChildRule) []CardHeaderFinding {
	return LintCardChildWith([]byte(StagedBrief(string(raw), rules)), rules)
}
