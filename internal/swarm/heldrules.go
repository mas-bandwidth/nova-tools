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
// Which file: the one the card's stream records (the packet's rules), and none when it records
// none: such a card carries its own rules, as every card did before. `nova-sprint add` records
// the file of the card's repository (OwnRulesName): fleet/child-rules.txt for this repository,
// fleet/child-rules.<repo>.txt for another that has one, and nothing for a repository with
// none, whose cards carry their own.

// DefaultRulesName is this repository's own rules file, fleet/child-rules.txt.
const DefaultRulesName = "child-rules.txt"

// HomeRepo is the repository whose rules file is DefaultRulesName: the one these files live in.
const HomeRepo = "nova-tools"

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

// OwnRulesName is the held rules file of the repository the brief's REPO: (or base-repo:)
// names: DefaultRulesName for HomeRepo, child-rules.<repo>.txt when this build holds one,
// and "" for a repository with none (its cards carry their own rules). A brief that names no
// repository has none of its own: it is unnamed, the add's held set.
func OwnRulesName(brief, unnamed string) string {
	return ownRulesIn(brief, unnamed, func(name string) bool { _, ok := HeldRulesText(name); return ok })
}

// ownRulesIn is OwnRulesName over held, which says whether a rules file is held.
func ownRulesIn(brief, unnamed string, held func(name string) bool) string {
	repo := ReadCardBase([]byte(brief)).Named
	repo = strings.TrimSuffix(path.Base(strings.TrimRight(repo, "/")), ".git")
	switch name := "child-rules." + repo + ".txt"; {
	case repo == "" || repo == ".":
		return unnamed
	case repo == HomeRepo:
		return DefaultRulesName
	case held(name):
		return name
	}
	return ""
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
