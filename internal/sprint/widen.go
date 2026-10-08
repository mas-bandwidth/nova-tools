package sprint

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// Widened in place by rule (docs/SPEC-SPRINT.md section 8, the rules table's row widen). On
// 2026-10-05 the coordinator twinned ten cards by hand whose work was done and green but held
// for files outside PATHS: each file read from the HOLD or the finding, appended to PATHS and
// SHARED, the twin pointed at the finished head. The tick does it, on the card itself (the
// owner, 2026-10-06: "We gotta stop doing this twin shit. it's waste."): a card in review whose
// worker's HOLD (work failed, a bound, a brief at its bound) names files outside its PATHS,
// or whose head the lander refused for them (E12, returned by the merge step or the conflict
// rule), has its brief edited in place (Brief, as brief --widen edits it) with PATHS and
// SHARED widened by exactly those files when each is adjacent to the change (WidenAdjacent)
// and a CARRY: line at the finished head: the same id, its next attempt staged from that head,
// its brief's bound counted again from it, and the coordinator gets one note naming the
// files. A HOLD naming a file that is not adjacent stays a judgment, its
// text naming what blocks it, and no other rule reworks it; an E12 refusal naming one is
// redone by the conflict rule. A reader's finding is the read-broken rule's (rules_read.go).

// RuleWiden is the widen rule's name; the sprint's answer_rules_off naming it turns it off
// alone (RuleOff), and run --answer-rules=false with every rule.
const RuleWiden = "widen"

// ActWiden is the widen rule's act.
const ActWiden = "widen PATHS in place"

// PartRuleWiden is the tick part that widens the cards the widen rule answers.
const PartRuleWiden = "rule widen"

// NPathsWidened is the happened note, to the coordinator, naming the files a card's PATHS
// were widened by.
const NPathsWidened = "PATHS widened by rule"

// WidenBlocked prefixes the text of a judgment the widen rule leaves: the files outside
// PATHS that are not adjacent to the change.
const WidenBlocked = "outside PATHS and not adjacent: "

// What makes a file outside PATHS adjacent to the change (WidenAdjacent).
const (
	AdjTests    = "the same package's tests"
	AdjTestdata = "the same package's testdata"
	AdjLedger   = "a ledger"
	AdjDocs     = "docs a class test reads"
	AdjNamed    = "the HOLD names it with its reason"
)

// ledgerDir holds the shrink-only ledgers the class tests read (docs/STANDARD.md).
const ledgerDir = "internal/ci/testdata/"

// WidenFile is one file outside a brief's PATHS and why it is adjacent to the change; Adjacent
// is "" when it is not.
type WidenFile struct {
	File     string `json:"file"`
	Adjacent string `json:"adjacent,omitempty"`
}

// WidenFiles is every file text names outside the brief's PATHS (FilesOutsidePaths), each
// classified by WidenAdjacent; hold says text is the worker's HOLD, whose reasons count.
func WidenFiles(brief, text string, hold bool) []WidenFile {
	out := FilesOutsidePaths(brief, text)
	files := make([]WidenFile, 0, len(out))
	for _, f := range out {
		files = append(files, WidenFile{File: f, Adjacent: WidenAdjacent(decide.CardPaths(brief), f, text, hold)})
	}
	return files
}

// WidenAdjacent is why file f is adjacent to a change inside paths: a test file of a package
// PATHS name, a file under that package's testdata, a ledger under internal/ci/testdata, a
// markdown file under docs/ or an AGENTS.md map (the docs the class tests read), or, in a
// HOLD, a file named with its reason (namedWithReason); "" when none holds.
func WidenAdjacent(paths []string, f, text string, hold bool) string {
	dir := path.Dir(f)
	pkg := func(d string) bool {
		return slices.ContainsFunc(paths, func(g string) bool {
			gd := path.Dir(g)
			if strings.HasSuffix(g, "/") {
				gd = strings.TrimSuffix(g, "/")
			}
			return gd == d || hygiene.MatchGlob(gd, d)
		})
	}
	switch {
	case strings.HasSuffix(f, "_test.go") && pkg(dir):
		return AdjTests
	case strings.Contains(f, "/testdata/") && pkg(strings.SplitN(f, "/testdata/", 2)[0]):
		return AdjTestdata
	case strings.HasPrefix(f, ledgerDir):
		return AdjLedger
	case strings.HasPrefix(f, "docs/") && path.Ext(f) == ".md", path.Base(f) == "AGENTS.md":
		return AdjDocs
	case hold && namedWithReason(text, f):
		return AdjNamed
	}
	return ""
}

// namedWithReason says a clause of text (a line, or a sentence of it) names f and gives a
// reason after it: past the file, its line number and any files listed beside it, three words
// or more.
func namedWithReason(text, f string) bool {
	for _, clause := range strings.FieldsFunc(strings.NewReplacer(". ", "\n", "; ", "\n").Replace(text), func(r rune) bool { return r == '\n' }) {
		for rest := clause; ; {
			i := strings.Index(rest, f)
			if i < 0 {
				break
			}
			rest = rest[i+len(f):]
			words := strings.FieldsFunc(rest, func(r rune) bool {
				return r == ' ' || r == '\t' || strings.ContainsRune(":,()`'\"", r) || r == '—' || r == '–'
			})
			for len(words) > 0 && (words[0] == "-" || words[0] == "and" || isNumber(words[0]) || findingFileRE.MatchString(strings.TrimRight(words[0], ".!?"))) {
				words = words[1:]
			}
			if len(words) >= 3 {
				return true
			}
		}
	}
	return false
}

func isNumber(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

// isHold says a work card's report is a HOLD: its Verdict line, or a HOLD word as the collect
// carries a friend's report on one line ("friend <name> HOLD: ...").
func isHold(report string) bool {
	if v, _ := CollectVerdict(report); v == "HOLD" {
		return true
	}
	return slices.ContainsFunc(strings.Fields(report), func(w string) bool { return strings.Trim(w, "*_:.,;") == "HOLD" })
}

// widenAnswer is the widen rule's answer for one card: widen it (widen) or leave it (far set).
type widenAnswer struct {
	pr    *Card
	opens []Open
	files []WidenFile
	far   []string
	widen bool
	text  string
	from  string
}

// widenTypes are the judgments the widen rule reads.
var widenTypes = []string{NWorkFailed, NBound, NBriefWrong, NReturned}

// widenAnswers is the widen rule's answers, one per card, in the order its judgments are open:
// a card in review at a full sha head (no friend's, no brief defect, no pinned model:
// mindCard) whose HOLD names files outside PATHS and says PATHS, or that the conflict rule
// returned on an E12 refusal at this attempt. A HOLD with a PATHS-PROPOSED line is the paths
// rule's (paths_proposed.go), and one naming a card that has not landed the hold-need rule's
// (judgment_rules.go). Every file adjacent: widened in place (widen). A HOLD naming a file
// not adjacent: left (far). An
// E12 refusal naming one is no answer: the conflict rule redoes it inside its PATHS.
func widenAnswers(s *Snapshot, r TickReq) []widenAnswer {
	if !r.AnswerRules || s.RuleOff(RuleWiden) {
		return nil
	}
	var out []widenAnswer
	at := map[string]int{}
	for _, o := range s.Open {
		if o.Note.Kind != Judgment || !slices.Contains(widenTypes, o.Note.Type) {
			continue
		}
		pr := s.Work.Placed(o.Subject())
		if pr == nil || pr.Col != Review || mindCard(pr) != "" || !typedrec.IsFullSha(pr.F("head")) {
			continue
		}
		if i, ok := at[pr.ID]; ok {
			out[i].opens = append(out[i].opens, o)
			continue
		}
		a := widenAnswer{pr: pr, opens: []Open{o}}
		hold := o.Note.Type != NReturned
		if hold {
			wc := s.Fleet.Card(pr.F("work"))
			if pr.F("result") != "failed" || wc == nil || !isHold(wc.F("report")) || !strings.Contains(wc.F("report"), cardhdr.KeyPaths) {
				continue
			}
			if _, ok := heldProposal(s, pr); ok || holdsFor(s, pr) != "" || strings.HasPrefix(o.Note.What, holdFixRefused) {
				continue // a PATHS-PROPOSED line is the paths rule's or finish's (hold_fix.go), a HOLD for a card the hold-need rule's; a refused hold fix stands
			}
			a.text, a.from = wc.F("report"), "its HOLD"
		} else {
			if pr.F(FieldRuleRefused) != RefusedPaths || pr.F(FieldRuleRedo) != pr.F("attempt") {
				continue
			}
			a.text, a.from = pr.F(FieldRuleRefusal), "the lander's E12 refusal"
		}
		a.files = WidenFiles(pr.F("brief"), a.text, hold)
		for _, f := range a.files {
			if f.Adjacent == "" {
				a.far = append(a.far, f.File)
			}
		}
		switch {
		case len(a.files) == 0, len(a.far) > 0 && !hold:
			continue
		case len(a.far) == 0:
			a.widen = true
		}
		at[pr.ID] = len(out)
		out = append(out, a)
	}
	return out
}

// widenSaid is the widen's words: the files, each with why it is adjacent, and the head.
func (a widenAnswer) widenSaid() string {
	var each []string
	for _, f := range a.files {
		each = append(each, f.File+" ("+f.Adjacent+")")
	}
	return fmt.Sprintf("%s attempt %s held only for files outside its PATHS (%s), each adjacent: PATHS widened in place by %s, its next attempt from head %s",
		a.pr.ID, a.pr.F("attempt"), a.from, strings.Join(each, ", "), a.pr.F("head"))
}

// fileNames is the files' names.
func fileNames(files []WidenFile) []string {
	var out []string
	for _, f := range files {
		out = append(out, f.File)
	}
	return out
}

// SharedWidened is brief with files added to every SHARED: line it has, each once, the old
// first; a brief with no SHARED: line is returned as it is.
func SharedWidened(brief string, files []string) string {
	lines := strings.Split(brief, "\n")
	for i, l := range lines {
		if k, v, ok := cardhdr.KeyValue(strings.TrimSpace(l)); ok && k == "SHARED" {
			var all []string
			for _, g := range append(strings.Split(v, ","), files...) {
				if g = strings.TrimSpace(g); g != "" && g != "none" && !slices.Contains(all, g) {
					all = append(all, g)
				}
			}
			lines[i] = l[:len(l)-len(strings.TrimLeft(l, " \t"))] + k + ": " + strings.Join(all, ",")
		}
	}
	return strings.Join(lines, "\n")
}

// TickRuleWiden is the widen rule's part (docs/SPEC-SPRINT.md section 8, the row widen): the
// first card it widens, by Brief in place with PATHS and SHARED widened by exactly the files
// and a CARRY: line at the finished head (the same id, review -> ready, its next attempt from
// that head and its brief's bound counted from it), its fix naming the head to start from and
// the files, its judgments closed by the decided note, and one note to the coordinator naming
// the files; one card a tick. Every card it leaves has its judgments' text prefixed with
// WidenBlocked and the files, once. A brief edit refused leaves the judgment open, the
// coordinator's.
func TickRuleWiden(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	var updates []Note
	for _, a := range widenAnswers(s, r) {
		if len(a.far) > 0 {
			for _, o := range a.opens {
				if n := o.Note; !strings.HasPrefix(n.What, WidenBlocked) && !slices.ContainsFunc(updates, func(u Note) bool { return u.ID == n.ID }) {
					n.What = WidenBlocked + strings.Join(a.far, ",") + " (" + a.from + "): " + n.What
					updates = append(updates, n)
				}
			}
			continue
		}
		if len(p.Units) == 0 && a.widen {
			p = widenInPlace(s, r, a)
		}
	}
	p.Updates = append(p.Updates, updates...)
	return p, 0
}

// widenInPlace is one card the widen rule answers, its brief widened in place (Brief, as brief
// --widen edits it: no twin); an empty plan when the edit is refused.
func widenInPlace(s *Snapshot, r TickReq, a widenAnswer) Plan {
	files := fileNames(a.files)
	pr := a.pr
	carry := fmt.Sprintf("CARRY: %s attempt %d head=%s", pr.ID, pr.Int("attempt"), pr.F("head"))
	p := Brief(s, BriefReq{ID: pr.ID, Brief: SharedWidened(PathsWidened(pr.F("brief"), files, carry), files), Rules: pr.F(FieldRules), Who: r.who()})
	if len(p.Refused) > 0 {
		return Plan{}
	}
	why := a.widenSaid()
	said := cutText(RuleSaid(RuleWiden, ActWiden+": "+why), MaxCardTextBytes)
	fix := cutText(fmt.Sprintf("start from head %s (%s attempt %s): its change is done; PATHS now name %s; %s: %s",
		pr.F("head"), pr.ID, pr.F("attempt"), strings.Join(files, ","), a.from, a.text), MaxCardTextBytes)
	for i := range p.Units {
		u := &p.Units[i]
		if u.Key != pr.ID {
			continue
		}
		for j, ch := range u.Changes {
			if ch.Table != Work || ch.Entry.ID != pr.ID {
				continue
			}
			e := &u.Changes[j].Entry
			if e.Set == nil {
				e.Set = map[string]string{}
			}
			e.Set["fix"] = fix
			e.Set[FieldNote], e.Set[FieldRuleAnswer] = said, RuleWiden+": "+ActWiden+" at "+stamp(s.Now)
			keep := []string{"fix"}
			if w := pr.F(FieldWho); w != "" && e.Set[FieldWho] == "" {
				e.Set[FieldWho] = w // whoever held the work keeps the card
				keep = append(keep, FieldWho)
			}
			e.Unset = slices.DeleteFunc(e.Unset, func(f string) bool { return slices.Contains(keep, f) })
		}
		u.Moved += "; answered by rule " + RuleWiden
		for _, o := range u.Closes {
			if o.Note.Kind == Judgment && !answeredIn(u.Notes, o.Note.ID) {
				u.Notes = append(u.Notes, decided(o, said, r.who(), s.Now, pr.ID))
			}
		}
		u.Notes = append(u.Notes, Note{Kind: Happened, Type: NPathsWidened, Stream: pr.Row, Primaries: []string{pr.ID}, Count: 1,
			Who: r.who(), To: s.Coordinator, At: s.Now, What: cutText(why, MaxCardTextBytes)})
	}
	return p
}

// TickRuleReworkUnwidened is the rule rework part with the judgments the widen rule leaves
// taken out of the open set: a HOLD for a file outside PATHS that is not adjacent is the
// coordinator's, and no failed or bound rule redeals it.
func TickRuleReworkUnwidened(s *Snapshot, r TickReq) (Plan, int) {
	var left []string
	for _, a := range widenAnswers(s, r) {
		if len(a.far) > 0 {
			for _, o := range a.opens {
				left = append(left, o.Key)
			}
		}
	}
	if len(left) == 0 {
		return TickRuleRework(s, r)
	}
	t := *s
	t.Open = slices.DeleteFunc(slices.Clone(s.Open), func(o Open) bool { return slices.Contains(left, o.Key) })
	return TickRuleRework(&t, r)
}
