package sprint

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardgen"
	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/hygiene"
	"github.com/mas-bandwidth/nova-tools/pkg/typedrec"
)

// Answered by rule: a reader found it broken (docs/SPEC-SPRINT.md section 8, the rules table's
// row read-broken; tla/SprintRules.tla, Part "reads": ReadAnswersBounded, TwinsWiden,
// ReadAnswered). The night of 2026-10-05 the coordinator answered this judgment about forty
// times, every time with `rework <card> --answers <id>`: the finding became the fix. The tick
// answers it the same way: the finding is the fix of a rework on the same tier; a finding
// that names a file outside the card's PATHS twins it (add --replaces) with PATHS
// widened by exactly those files, starting from the broken attempt's head; a card at its
// brief's bound (the same finding twice, or its attempts cap) is left to the coordinator,
// as is a brief defect and a broken verdict with no finding. A friend's card is answered the
// same way, its next attempt hers (ReworkPinned): the finding rides on the card whoever
// worked it. Every answer is a note on the card naming the rule ("note", logged with the
// move) and the decided note of the judgment.

// RuleReadBroken is the read-broken rule's name. run --answer-rules=false turns it off with
// every rule; the sprint's answer_rules_off naming it turns it off alone (RuleOff), though
// RuleNames and nova-config's enum (config.AnswerRules) do not list it yet.
const RuleReadBroken = "read-broken"

// ActTwinWider is the read-broken rule's act on a finding outside PATHS: the card twinned with
// its PATHS widened.
const ActTwinWider = "twin with PATHS widened"

// PartRuleTwin is the tick part that twins the cards a rule answers ActTwinWider.
const PartRuleTwin = "rule twin"

// FieldNote is a card's note: the last rule answer that moved it, in the rule's words.
const FieldNote = "note"

// ruleReadBroken: a reader found the primary's attempt broken. Below its brief's bound it
// is reworked with the findings as the fix, on its tier, or twinned with PATHS widened when
// the findings name files outside them; a friend's card as a machine's.
func ruleReadBroken(s *Snapshot, a *RuleAnswer) {
	a.Rule = RuleReadBroken
	pr := s.Work.Placed(a.Subject)
	if pr == nil || pr.Col != Review {
		left(a, "not in review")
		return
	}
	if pr.F(FieldBriefDefect) != "" {
		left(a, mindCard(pr))
		return
	}
	finding := brokenFindings(s, pr)
	if finding == "" {
		left(a, "no broken read of attempt "+pr.F("attempt")+" with a finding stands: a mind's")
		return
	}
	if bb, ok := AtBriefBound(pr, finding, s.AttemptsCap(pr.Row)); ok {
		left(a, bb.String())
		return
	}
	a.Card, a.fix = pr.ID, cutText(finding, MaxCardTextBytes)
	if out := FilesOutsidePaths(pr.F("brief"), finding); len(out) > 0 {
		a.files, a.twin = out, TwinID(s, pr)
		if a.twin == "" {
			left(a, "its finding names "+strings.Join(out, ",")+" outside its PATHS, and every twin id of "+pr.ID+" is taken")
			return
		}
		a.Act = ActTwinWider
		a.Why = fmt.Sprintf("a reader found attempt %s broken naming %s outside its PATHS: twinned as %s with PATHS widened, its finding the fix",
			pr.F("attempt"), strings.Join(out, ","), a.twin)
		return
	}
	a.Act = ActRework
	a.Why = fmt.Sprintf("a reader found attempt %s broken: its finding is the fix, on the same tier", pr.F("attempt"))
	a.set = map[string]string{FieldNote: cutText(RuleSaid(RuleReadBroken, a.Act+": "+a.Why), MaxCardTextBytes)}
}

// findingFileRE is a repository path a finding names: a relative path of two or more parts.
var findingFileRE = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.+-]*(/[A-Za-z0-9_.+-]+)+$`)

// findingExtRE is the extension a file's last part ends in: letters only, so a branch
// (sprint/c.w1.g3.e15) or a version is never read as a file.
var findingExtRE = regexp.MustCompile(`\.[A-Za-z]{1,8}$`)

// FilesOutsidePaths is every repository file the finding names that the brief's PATHS do not
// cover, each once, in the order named; nil when the brief names no PATHS. A file is a
// relative path with a directory and an extension of letters, read through backticks,
// brackets, quotes and a trailing line number; absolute paths, paths that climb with ..,
// URLs and branch names are not files. PATHS cover a file by its name, a glob
// (hygiene.MatchGlob) or a directory ending in /, and every PATHS covers a file a change
// must touch to keep the tree green (cardgen.AlwaysInPaths): such a finding is a rework,
// never a twin.
func FilesOutsidePaths(brief, finding string) []string {
	paths := decide.CardPaths(brief)
	if len(paths) == 0 {
		return nil
	}
	covered := func(f string) bool {
		return cardgen.AlwaysInPaths(f) || slices.ContainsFunc(paths, func(g string) bool {
			return g == f || hygiene.MatchGlob(g, f) || strings.HasSuffix(g, "/") && strings.HasPrefix(f, g)
		})
	}
	var out []string
	for _, tok := range strings.FieldsFunc(finding, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || strings.ContainsRune(",;()[]{}<>\"'`=|", r)
	}) {
		if strings.Contains(tok, "://") || strings.HasPrefix(tok, "/") || strings.HasPrefix(tok, "~") {
			continue
		}
		tok, _, _ = strings.Cut(tok, ":")
		tok = strings.TrimPrefix(strings.TrimRight(tok, ".!?"), "./")
		if !findingFileRE.MatchString(tok) || slices.Contains(strings.Split(tok, "/"), "..") || !findingExtRE.MatchString(path.Base(tok)) {
			continue
		}
		if !covered(tok) && !slices.Contains(out, tok) {
			out = append(out, tok)
		}
	}
	return out
}

// PathsWidened is brief with files added to every PATHS: line, each once, the old first, and
// carry (when given) as its CARRY: line (member.CarryLine's shape), in place of one in its
// header or after line 1.
func PathsWidened(brief string, files []string, carry string) string {
	lines := strings.Split(brief, "\n")
	carried := carry == ""
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if k, v, ok := cardhdr.KeyValue(t); ok && k == cardhdr.KeyPaths {
			var globs []string
			for _, g := range append(strings.Split(v, ","), files...) {
				if g = strings.TrimSpace(g); g != "" && g != "none" && !slices.Contains(globs, g) {
					globs = append(globs, g)
				}
			}
			lines[i] = l[:len(l)-len(strings.TrimLeft(l, " \t"))] + k + ": " + strings.Join(globs, ",")
		} else if strings.HasPrefix(t, "CARRY:") && !carried {
			lines[i], carried = carry, true
		}
	}
	if !carried {
		lines = slices.Insert(lines, min(1, len(lines)), carry)
	}
	return strings.Join(lines, "\n")
}

// TickRuleTwin twins the first card a rule answers ActTwinWider: Recut with its PATHS
// widened by the files its finding names and a CARRY: line at the broken attempt's head when
// it pushed one, the twin carrying the finding as its fix and the rule's note, the old card
// dropped "replaced by <twin>" with its judgments closed by the decided note. One card a
// tick: each twin's step re-weighs the stream, so two planned on one read would write one
// card's weight twice; the next tick twins the next. A recut refused leaves the judgment
// open, the coordinator's.
func TickRuleTwin(s *Snapshot, r TickReq) (Plan, int) {
	for _, a := range acting(s, r, ActTwinWider) {
		pr := s.Work.Placed(a.Card)
		carry := ""
		if head := pr.F("head"); typedrec.IsFullSha(head) {
			carry = fmt.Sprintf("CARRY: %s attempt %d head=%s", pr.ID, pr.Int("attempt"), head)
		}
		p := Recut(s, RecutReq{ID: pr.ID, New: a.twin, Brief: PathsWidened(pr.F("brief"), a.files, carry), Rules: pr.F(FieldRules), Who: r.who()})
		if len(p.Refused) > 0 {
			continue
		}
		said := cutText(RuleSaid(a.Rule, a.Act+": "+a.Why), MaxCardTextBytes)
		for i := range p.Units {
			u := &p.Units[i]
			for j, ch := range u.Changes {
				if ch.Table == Work && ch.Entry.ID == a.twin && ch.Entry.Create != nil {
					set := u.Changes[j].Entry.Set
					set["fix"], set["finding"] = a.fix, a.fix
					set["why"] = fmt.Sprintf("%s attempt %s finished and a reader found it broken", pr.ID, pr.F("attempt"))
					set[FieldNote], set[FieldRuleAnswer] = said, a.Rule+": "+a.Act+" at "+stamp(s.Now)
					if w := pr.F(FieldWho); w != "" && set[FieldWho] == "" {
						set[FieldWho] = w // whoever held the work keeps the twin
					}
				}
			}
			if u.Key != pr.ID {
				continue
			}
			u.Moved += "; answered by rule " + a.Rule
			for _, o := range u.Closes {
				if o.Note.Kind == Judgment && !answeredIn(u.Notes, o.Note.ID) {
					u.Notes = append(u.Notes, decided(o, said, r.who(), s.Now, pr.ID))
				}
			}
		}
		return p, 0
	}
	return Plan{}, 0
}
