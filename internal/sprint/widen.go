package sprint

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/diffcheck"
	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// Widened by rule (docs/SPEC-SPRINT.md section 8, "Answered by rule", the row widen; the
// owner, 2026-10-05, at 12:10 PM: "now all the things you are doing manually now in LLM
// space, make sure there are cards for this sprint to have them automated by the machine").
// That day the coordinator twinned ten cards by hand whose work was done and green but held
// for files outside PATHS: each file read from the finding, appended to PATHS and SHARED, and
// the twin pointed at the finished head. The tick does it by rule: a card in review whose open
// judgment (a worker's HOLD, a reader's finding, the lander's E12 refusal the conflict rule
// returned, a bound) says its change needs files outside its PATHS, and names them, is
// twinned (add --replaces, Recut) with its PATHS and SHARED widened to exactly those files
// when every one is adjacent to the change, the twin starting from the finished head (its
// CARRY: line), and the coordinator gets one note naming the files. A file that is not
// adjacent leaves the judgment to a mind, and WidenAnswers says which file and why. One pure
// function decides (WidenAnswers); the tick part applies it (TickRuleWiden).

// RuleWiden is the widen rule's name. run --answer-rules=false turns it off with every rule;
// the sprint's answer_rules_off naming it turns it off alone (RuleOff), though RuleNames and
// nova-config's enum (config.AnswerRules) do not list it yet, so nova-config cannot set it.
const RuleWiden = "widen"

// PartRuleWiden is the widen rule's tick part: after the conflict rule's return and resume,
// so a head the lander refused for its PATHS is in review when it runs, and before the rule
// rework, so the card it twins is not reworked as well (TickEndWith).
const PartRuleWiden = "rule widen"

// ActTwin is the widen rule's act: the card twinned with its PATHS widened.
const ActTwin = "twin wider"

// FieldWidened is a twin's record of the files the widen rule added to its PATHS, and where it
// started: "<file,...> from <old> attempt <n> head=<sha>".
const FieldWidened = "widened"

// WidenTypes are the judgments the widen rule reads: work held or failed, a reader's finding,
// the conflict rule's return, a bound, a brief at its bound.
var WidenTypes = []string{NWorkFailed, NReadBroken, NReturned, NBound, NBriefWrong}

// WidenFile is one file a held card's judgment names outside its PATHS, and whether it is
// adjacent to the change, with why.
type WidenFile struct {
	File     string `json:"file"`
	Adjacent bool   `json:"adjacent"`
	Why      string `json:"why"`
}

// WidenAnswer is what the widen rule does with one held card: twin it wider, or leave it.
type WidenAnswer struct {
	Card      string      `json:"card"`
	Judgments []string    `json:"judgments"`
	Act       string      `json:"act"`
	Why       string      `json:"why"`
	Twin      string      `json:"twin,omitempty"`
	Head      string      `json:"head,omitempty"`
	Attempt   int         `json:"attempt,omitempty"`
	Files     []WidenFile `json:"files,omitempty"`
}

// Twins says the answer twins its card.
func (a WidenAnswer) Twins() bool { return a.Act == ActTwin }

// heldForPathsRE says a text is about files outside a card's PATHS: the worker's HOLD, the
// reader's finding, the lander's E12, a PATHS-PROPOSED line.
var heldForPathsRE = regexp.MustCompile(`(?i)\b(outside|beyond|not in|too narrow|widen)\b[^.\n]{0,40}PATHS|PATHS[^.\n]{0,40}\b(outside|too narrow)\b|\(E12\)|` + regexp.QuoteMeta("PATHS-PROPOSED:"))

// HeldForPaths says text names files outside a card's PATHS as what holds it.
func HeldForPaths(text string) bool { return heldForPathsRE.MatchString(text) }

// widenFileRE is a repository path a text names: a relative path of two or more parts whose
// last has an extension of letters (internal/x/a_test.go, docs/SPEC.md), or a glob of one.
var widenFileRE = regexp.MustCompile(`^[A-Za-z0-9_.*][A-Za-z0-9_.*+-]*(/[A-Za-z0-9_.*+-]+)+$`)

// widenExtRE is the extension a file's last part ends in: letters only, so a branch
// (sprint/c.w1.g3.e15) or a version is never read as a file.
var widenExtRE = regexp.MustCompile(`\.[A-Za-z]{1,8}$`)

// WidenFiles is every repository file (or glob) text names, each once, in the order named: a
// relative path with a directory and an extension of letters, read through backticks,
// brackets, quotes and a trailing line number (internal/x/a.go:12). Absolute paths, home
// paths, paths that climb with .., URLs, directories, go package patterns and branch names
// are not files.
func WidenFiles(text string) []string {
	var out []string
	for _, tok := range strings.FieldsFunc(text, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || strings.ContainsRune(",;()[]{}<>\"'`=|", r)
	}) {
		if strings.Contains(tok, "://") || strings.HasPrefix(tok, "/") || strings.HasPrefix(tok, "~") {
			continue
		}
		tok, _, _ = strings.Cut(tok, ":")
		tok = strings.TrimPrefix(strings.TrimLeft(strings.TrimRight(tok, "*.!?"), "*"), "./")
		if !widenFileRE.MatchString(tok) || slices.Contains(strings.Split(tok, "/"), "..") {
			continue
		}
		if !widenExtRE.MatchString(path.Base(tok)) || slices.Contains(out, tok) {
			continue
		}
		out = append(out, tok)
	}
	return out
}

// WidenAdjacent is whether file f, outside a card's PATHS, is adjacent to its change, with
// why: the test or testdata of a package its PATHS name, a generated ledger, a doc under docs/
// (the docs a class test reads), or, when hold (a worker's HOLD names it with its reason), a
// file of a package its PATHS name. Anything else, another package's code above all, is not.
func WidenAdjacent(f string, paths []string, hold bool) (bool, string) {
	dirs := map[string]bool{}
	for _, g := range paths {
		if d, ok := strings.CutSuffix(g, "/"); ok {
			dirs[d] = true // a directory the PATHS name whole
			continue
		}
		dirs[path.Dir(g)] = true
	}
	dir := path.Dir(f)
	testdata := false
	for d := range dirs {
		testdata = testdata || strings.HasPrefix(f, path.Join(d, "testdata")+"/")
	}
	switch {
	case strings.HasSuffix(f, "_test.go") && dirs[dir]:
		return true, "the test of " + dir + ", a package its PATHS name"
	case testdata:
		return true, "testdata of a package its PATHS name"
	case diffcheck.Ledger(f) || diffcheck.AgentsMap(f):
		return true, "a generated ledger the change touches"
	case strings.HasPrefix(f, "docs/") && strings.HasSuffix(f, ".md"):
		return true, "a doc under docs/ a class test reads"
	case hold && dirs[dir]:
		return true, "in " + dir + ", a package its PATHS name, named by the HOLD with its reason"
	}
	return false, "outside the packages its PATHS name: another package's change is a mind's"
}

// widenText is everything that says why a card in review is held: its open judgments' words,
// the lander's refusal the conflict rule returned it on, its live attempt's report.
func widenText(s *Snapshot, pr *Card, opens []Open) string {
	var b strings.Builder
	for _, o := range opens {
		b.WriteString(o.Note.What + "\n")
	}
	if pr.F(FieldRuleRefused) == RefusedPaths {
		b.WriteString(pr.F(FieldRuleRefusal) + "\n")
	}
	if wc := s.Fleet.Card(pr.F("work")); wc != nil && wc.F("primary") == pr.ID {
		b.WriteString(wc.F("report") + "\n")
	}
	return b.String()
}

// WidenAnswers is what the widen rule does with every card in review that an open judgment of
// WidenTypes holds for files outside its PATHS, one answer per card, in the order its first
// such judgment is open: twin wider, or left with why (no file outside its PATHS, no pushed
// head, a file not adjacent, no twin id left, the rule turned off). It is pure.
func WidenAnswers(s *Snapshot, r TickReq) []WidenAnswer {
	var order []string
	opens := map[string][]Open{}
	for _, o := range s.Open {
		if o.Note.Kind != Judgment || !slices.Contains(WidenTypes, o.Note.Type) {
			continue
		}
		id := o.Subject()
		if _, ok := opens[id]; !ok {
			order = append(order, id)
		}
		opens[id] = append(opens[id], o)
	}
	var out []WidenAnswer
	for _, id := range order {
		pr := s.Work.Placed(id)
		if pr == nil || IsSentinel(pr) || pr.Col != Review {
			continue
		}
		text := widenText(s, pr, opens[id])
		if !HeldForPaths(text) {
			continue
		}
		a := WidenAnswer{Card: id, Act: ActLeft, Attempt: pr.Int("attempt"), Head: pr.F("head")}
		for _, o := range opens[id] {
			a.Judgments = append(a.Judgments, o.Note.ID)
		}
		out = append(out, widenAnswer(s, r, pr, text, a))
	}
	return out
}

// widenAnswer is the widen rule's answer for one held card.
func widenAnswer(s *Snapshot, r TickReq, pr *Card, text string, a WidenAnswer) WidenAnswer {
	brief := pr.F("brief")
	paths := decide.CardPaths(brief)
	hold := strings.Contains(text, "HOLD")
	for _, f := range WidenFiles(text) {
		if len(paths) > 0 && slices.ContainsFunc(paths, func(g string) bool { return g == f || hygiene.MatchGlob(g, f) }) {
			continue
		}
		adj, why := WidenAdjacent(f, paths, hold)
		a.Files = append(a.Files, WidenFile{File: f, Adjacent: adj, Why: why})
	}
	var far []string
	for _, f := range a.Files {
		if !f.Adjacent {
			far = append(far, f.File+" ("+f.Why+")")
		}
	}
	switch {
	case len(paths) == 0:
		a.Why = "its brief names no PATHS to widen"
	case len(a.Files) == 0:
		a.Why = "held for its PATHS, but names no file outside them: a mind's"
	case len(far) > 0:
		a.Why = "not adjacent: " + strings.Join(far, "; ")
	case !typedrec.IsFullSha(a.Head):
		a.Why = "attempt " + itoa(a.Attempt) + " pushed no head to start the twin from"
	case TwinID(s, pr) == "":
		a.Why = "every twin id of " + pr.ID + " is taken"
	case !r.AnswerRules:
		a.Why = "the rules are off (run --answer-rules=false)"
	case s.RuleOff(RuleWiden):
		a.Act, a.Why = ActOff, "nova-config's sprint row answer_rules_off turns the rule "+RuleWiden+" off"
	default:
		a.Act, a.Twin = ActTwin, TwinID(s, pr)
		a.Why = "every file outside its PATHS is adjacent: twinned as " + a.Twin + " with PATHS and SHARED widened, starting from attempt " + itoa(a.Attempt) + " head=" + a.Head
	}
	return a
}

// widenNames is the files of an answer, comma joined.
func widenNames(fs []WidenFile) string {
	var out []string
	for _, f := range fs {
		out = append(out, f.File)
	}
	return strings.Join(out, ",")
}

// WidenCarry is the CARRY: line of a twin that starts from the held card's attempt and head,
// as the member reads it (internal/member, CarryOf).
func WidenCarry(card string, attempt int, head string) string {
	return fmt.Sprintf("CARRY: %s attempt %d head=%s", card, attempt, head)
}

// WidenBrief is brief with files added to every PATHS: line and its SHARED: line (one added
// after the first PATHS: line when it has none), each glob once, the old first, and carry as
// its CARRY: line, in place of one in its header or after line 1.
func WidenBrief(brief string, files []string, carry string) string {
	lines := strings.Split(brief, "\n")
	union := func(old string) string {
		var out []string
		for _, g := range append(strings.Split(old, ","), files...) {
			if g = strings.TrimSpace(g); g != "" && g != "none" && !slices.Contains(out, g) {
				out = append(out, g)
			}
		}
		return strings.Join(out, ",")
	}
	firstPaths, shared, carried := -1, false, false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		indent := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		k, v, ok := cardhdr.KeyValue(t)
		switch {
		case ok && k == cardhdr.KeyPaths:
			lines[i] = indent + k + ": " + union(v)
			if firstPaths < 0 {
				firstPaths = i
			}
		case ok && k == "SHARED" && !shared:
			lines[i], shared = indent+k+": "+union(v), true
		case strings.HasPrefix(t, "CARRY:") && !carried:
			lines[i], carried = carry, true
		}
	}
	if !shared && firstPaths >= 0 {
		lines = slices.Insert(lines, firstPaths+1, "SHARED: "+union(""))
	}
	if !carried {
		lines = slices.Insert(lines, min(1, len(lines)), carry)
	}
	return strings.Join(lines, "\n")
}

// TickRuleWiden twins the first held card the widen rule answers (WidenAnswers): Recut with
// the widened brief, the held card dropped "replaced by <twin>", its judgments closed with the
// decided note "answered by rule widen: ...", and one note to the coordinator naming the
// files. One card a tick: each twin's step re-weighs the stream, so two planned on one read
// would write one card's weight twice; the next tick twins the next.
func TickRuleWiden(s *Snapshot, r TickReq) (Plan, int) {
	for _, a := range WidenAnswers(s, r) {
		if !a.Twins() {
			continue
		}
		pr := s.Work.Placed(a.Card)
		files := strings.Split(widenNames(a.Files), ",")
		brief := WidenBrief(pr.F("brief"), files, WidenCarry(pr.ID, a.Attempt, a.Head))
		p := Recut(s, RecutReq{ID: pr.ID, New: a.Twin, Brief: brief, Rules: pr.F(FieldRules), Who: r.who()})
		if len(p.Refused) > 0 {
			continue
		}
		said := RuleSaid(RuleWiden, ActTwin+": "+a.Why+"; PATHS widened by "+widenNames(a.Files))
		for i := range p.Units {
			u := &p.Units[i]
			for j, ch := range u.Changes {
				if ch.Table != Work || ch.Entry.ID != a.Twin || ch.Entry.Create == nil {
					continue
				}
				set := u.Changes[j].Entry.Set
				set[FieldWidened] = fmt.Sprintf("%s from %s attempt %d head=%s", widenNames(a.Files), pr.ID, a.Attempt, a.Head)
				set[FieldRuleAnswer] = RuleWiden + ": " + ActTwin + " at " + stamp(s.Now)
				if w := pr.F(FieldWho); w != "" && set[FieldWho] == "" {
					set[FieldWho] = w // whoever held the work keeps the twin
				}
			}
			if u.Key != pr.ID {
				continue
			}
			for _, o := range u.Closes {
				if o.Note.Kind == Judgment {
					u.Notes = append(u.Notes, decided(o, cutText(said, MaxCardTextBytes), r.who(), s.Now, pr.ID))
				}
			}
		}
		n := happened(NRuleAnswered, pr.Row, s.Now, a.Twin)
		n.Who, n.To, n.Card, n.Attempt = r.who(), s.Coordinator, a.Twin, a.Attempt
		var why []string
		for _, f := range a.Files {
			why = append(why, f.File+" ("+f.Why+")")
		}
		n.What = cutText(fmt.Sprintf("%s twinned as %s with PATHS and SHARED widened by %s, starting from attempt %d head=%s",
			pr.ID, a.Twin, strings.Join(why, ", "), a.Attempt, a.Head), MaxCardTextBytes)
		n.Hint = "nothing to do: the twin is dealt as any card; drop " + a.Twin + " if a file should not be its"
		p.Notes = append(p.Notes, n)
		return p, 0
	}
	return Plan{}, 0
}
