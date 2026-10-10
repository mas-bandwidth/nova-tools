package sprint

import (
	"fmt"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// The brief's bound (docs/SPEC-SPRINT.md, "The brief is wrong, not the worker"; the owner,
// 2026-10-03, after two gating cards were reworked to attempts 262 and 17 with the same reader
// finding every time: "These two gating cards getting rejected, should have been escalated to
// you, the coordinator, way sooner than this"). The same finding twice means the brief is
// wrong, not the worker, and no further attempt is possible without changing the brief: rework
// refuses such a card, whoever answers, and the judgment raised for it offers brief and drop,
// never rework. The decision is one pure function over the primary, AtBriefBound; a changed
// brief (Brief, FieldBriefAttempt) resets it.
//
// The attempt cap (the owner, 2026-10-04, after a night in which only a repeated finding
// bounded a card and one that failed differently each time ran for ever): after the cap's
// attempts on one brief (AttemptsCap: the stream's setting, else the sprint's, else
// AttemptsDefault) the card is not dealt to a machine again. Its default answer is a friend
// card (AttemptCapDeal, the tick's part before the deal; the tier ladder settled 2026-10-04:
// escalation is by attempt cap): a ready primary past the cap is dealt to a frontier or
// heavy-class friend up with room, the plan counting her free width as it deals, one width
// one such card, and a card that no longer fits stays ready. With no such friend up with
// room it goes to the coordinator as one judgment, "brief defect after N attempts, $X
// spent", with the findings of every attempt listed, and the decisions brief and drop.

// FieldFindingAttempt is the attempt whose broken reads' finding the primary's `finding`
// carries (Rework writes both: the attempt it sends back and what its readers found). A
// primary with a finding and no attempt recorded (admitted before this field) was reworked
// from the attempt before its current one.
const FieldFindingAttempt = "finding_attempt"

// FieldBriefAttempt is the primary's attempt when its brief was last replaced (Brief); absent,
// the brief is the one add gave it, at attempt 0.
const FieldBriefAttempt = "brief_attempt"

// FieldFindings is the primary's list of what each attempt sent back found, one line an
// attempt ("attempt <n>: <the finding's first sentence>"), appended by Rework and cut to
// MaxCardTextBytes from the front (the latest kept): what the cap's judgment lists, and
// kept by the card the cap's default answer deals to a friend.
const FieldFindings = "findings"

// PropAttempts is the work table's property holding the sprint's attempt cap (set
// --attempts, init --attempts), FieldAttempts a stream's control card field holding the
// stream's (stream set --attempts), over the sprint's; AttemptsDefault is the cap when
// neither is set, and AttemptsMax the most a cap may be.
const (
	PropAttempts    = "attempts"
	FieldAttempts   = "attempts"
	AttemptsDefault = 4
	AttemptsMax     = 100
)

// AttemptsCap is how many attempts one brief may run in the stream: the stream's
// setting, else the sprint's, else AttemptsDefault.
func (s *Snapshot) AttemptsCap(stream string) int {
	if s.Merge != nil {
		if n := s.StreamCtl(stream).Int(FieldAttempts); n > 0 {
			return n
		}
	}
	if s.Work != nil {
		if v, ok := s.Work.Prop(PropAttempts); ok {
			if n, err := ParseAttempts(v); err == nil && n > 0 {
				return n
			}
		}
	}
	return AttemptsDefault
}

// ParseAttempts is an attempt cap as the verbs take it: a whole number from 1 to
// AttemptsMax, or ReadTierDefault (0: the default).
func ParseAttempts(text string) (int, error) {
	if text == ReadTierDefault {
		return 0, nil
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(text), "%d", &n); err != nil || n < 1 || n > AttemptsMax || fmt.Sprint(n) != strings.TrimSpace(text) {
		return 0, fmt.Errorf("--attempts wants a whole number from 1 to %d, or %s; found %q", AttemptsMax, ReadTierDefault, text)
	}
	return n, nil
}

// BriefBound is why a primary's brief is wrong: two attempts since the brief last changed
// whose readers found the same thing (Attempts and Finding), or the cap's attempts since it
// changed (Since and Cap, with Finding ""): then Spent is what the card has cost (MoneyText,
// "" when nothing of it was priced) and Findings what each attempt found, in order.
type BriefBound struct {
	ID       string
	Attempts [2]int // the two attempts that failed the same way, in order
	Finding  string // the finding's first sentence, as the first of the two said it
	Since    int    // the attempts since the brief last changed, when that is the bound
	Cap      int    // the cap those attempts reached
	Spent    string
	Findings []string
}

// String is the bound said in one line: what repeated and where, or the cap, the spend and
// the findings, then the verdict.
func (b BriefBound) String() string {
	if b.Finding != "" {
		return fmt.Sprintf("%s has failed the same way twice (attempts %d and %d: %s); the brief is wrong, not the worker",
			b.ID, b.Attempts[0], b.Attempts[1], b.Finding)
	}
	spent := "nothing priced"
	if b.Spent != "" {
		spent = b.Spent + " spent"
	}
	line := fmt.Sprintf("%s: brief defect after %d attempts, %s; the brief is wrong, not the worker", b.ID, b.Since, spent)
	if len(b.Findings) > 0 {
		line += "; findings: " + strings.Join(b.Findings, "; ")
	}
	return line
}

// Remedy is what changes the brief: corrected in place, the card's next attempt staged from
// its last pushed head, or the card dropped. A rework's --fix changes the brief not at all, so
// it is never offered.
func (b BriefBound) Remedy() string {
	return "run: nova-sprint brief " + b.ID + " --brief-file <path> (the brief corrected in place, its next attempt from its last pushed head), or nova-sprint drop " + b.ID + " --reason '<why>'"
}

// Why is the rework's refusal of a card at the bound: the line, and the remedy.
func (b BriefBound) Why() string { return b.String() + "; " + b.Remedy() }

// FindingClass is what two findings are compared by: the whole finding, its whitespace
// collapsed and its case folded (two findings that share a first sentence, or a first line,
// and differ after it are two findings); and a finding of files outside the card's PATHS,
// however it is worded ("files outside PATHS", "outside its PATHS"), is the one class `files
// outside paths`. "" for an empty finding.
func FindingClass(finding string) string {
	text := strings.ToLower(strings.Join(strings.Fields(finding), " "))
	if strings.Contains(text, "outside") && strings.Contains(text, "paths") {
		return "files outside paths"
	}
	return text
}

// SameFinding says two findings are the same finding: each has a class and it is the same one.
func SameFinding(a, b string) bool {
	c := FindingClass(a)
	return c != "" && c == FindingClass(b)
}

// firstSentence is a finding's first sentence as said, for the line that names it.
func firstSentence(finding string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(finding), "\n")
	if i := strings.Index(line, ". "); i >= 0 {
		line = line[:i+1]
	}
	return cutText(strings.Join(strings.Fields(line), " "), MaxProviderErrorBytes)
}

// findingsOf is the primary's findings list (FieldFindings) with the finding of attempt
// n added, as the cap's judgment lists them; nothing added for an empty finding.
func findingsOf(c *Card, n int, finding string) []string {
	var out []string
	for _, l := range strings.Split(c.F(FieldFindings), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	if f := firstSentence(finding); f != "" {
		out = append(out, "attempt "+itoa(n)+": "+f)
	}
	return out
}

// findingsLine is the findings list as the primary keeps it, the latest kept under
// MaxCardTextBytes.
func findingsLine(findings []string) string {
	for len(findings) > 0 && len(strings.Join(findings, "\n")) > MaxCardTextBytes {
		findings = findings[1:]
	}
	return strings.Join(findings, "\n")
}

// AtBriefBound is whether the primary c is at its brief's bound, and why. finding is what
// its readers found at its current attempt, or the report of its failed work: the broken
// reads' findings the store holds (brokenFindings), or the one a read step is about to
// write; "" when none is known. cap is the attempts one brief may run (AttemptsCap). The
// finding before is the primary's own (`finding`, at FieldFindingAttempt), counted only
// when that attempt ran on the current brief. The count is the attempts since the brief
// last changed; a card never dealt is at no bound.
func AtBriefBound(c *Card, finding string, cap int) (BriefBound, bool) {
	attempt := c.Int("attempt")
	briefAt := c.Int(FieldBriefAttempt)
	if attempt == 0 {
		return BriefBound{}, false
	}
	prev, prevAt := c.F("finding"), c.Int(FieldFindingAttempt)
	if prev != "" && prevAt == 0 {
		prevAt = attempt - 1
	}
	if prev != "" && prevAt > briefAt && prevAt < attempt && SameFinding(prev, finding) {
		return BriefBound{ID: c.ID, Attempts: [2]int{prevAt, attempt}, Finding: firstSentence(prev)}, true
	}
	if cap <= 0 {
		cap = AttemptsDefault
	}
	if since := attempt - briefAt; since >= cap {
		spent := MoneyText(CardCostOf(c).Total.Charged)
		if spent == "-" {
			spent = ""
		}
		return BriefBound{ID: c.ID, Since: since, Cap: cap, Spent: spent, Findings: findingsOf(c, attempt, finding)}, true
	}
	return BriefBound{}, false
}

// AttemptCapDeal is the attempt cap's default answer, the tick's part before the deal
// (TickCapDeal; the tier ladder settled 2026-10-04: escalation is by attempt cap): every machine's
// primary ready and past its stream's cap on the brief it carries (AtBriefBound with no
// finding, asked with AttemptsCap), in a stream not held, is dealt as a friend card
// (friend_deal.go) to the frontier or heavy-class friend up with room. Room is her free
// width, width less the cards she already holds, counted across this plan the way
// friendDealPass counts free: each deal decrements it and the next card is picked again, the
// most free first and the first by name among equals, so two capped cards cannot both
// land on one friend whose width is 1. The card keeps its work and findings; its brief
// gains the WHO line of the friend chosen by the fields the brief edit writes (FieldWho
// from WhoOfBrief, the brief's text, and FieldBriefAttempt at the attempt it is dealt, so
// the cap count resets as a replaced brief does); and its next attempt's work card is
// created on her row in working, closing a brief-defect judgment open on it. A card it
// does not deal (no such
// friend up with room) is the deal's as before: at its redeal bound the tick raises the
// cap's judgment, the findings of every attempt and the spend, decisions brief and drop.
func AttemptCapDeal(s *Snapshot, r TickReq) Plan {
	var p Plan
	p.on(s)
	declared := map[string]bool{}
	classes := []string{cardhdr.RouteFrontier, cardhdr.RouteHeavy}
	free := map[string]int{}
	for _, f := range r.Friends {
		if friendDealable(s, f) {
			free[f.Name] = f.Width - friendLoad(s, f.Name)
		}
	}
	for _, c := range s.Work.Column(Ready) {
		if _, friend := FriendCard(c); friend || IsSentinel(c) || StreamHeld(s, c.Row) {
			continue // a friend's card is friendDealPass's, a sentinel never moves, a held stream is dealt nothing
		}
		if _, ok := AtBriefBound(c, "", s.AttemptsCap(c.Row)); !ok {
			continue
		}
		name := friendWithFree(r.Friends, free, c, classes...)
		if name == "" {
			continue // no frontier or heavy friend up with room: the deal's, and its judgment
		}
		card := WorkCardID(c.ID, c.Int("attempt")+1)
		if s.Fleet.Card(card) != nil {
			p.refuse(c.ID, "work card "+card+" exists already")
			continue
		}
		free[name]--
		row := FriendRow(name)
		if !s.Fleet.HasRow(row) && !declared[row] {
			p.Rows = append(p.Rows, RowAdd{Fleet, row})
			declared[row] = true
		}
		u := friendDealUnit(s, c, card, row, Working, map[string]string{
			FieldWho:          row,
			"brief":           briefGainsWho(c.F("brief"), name),
			FieldBriefAttempt: c.F("attempt"),
		})
		if prev := s.Fleet.Placed(WorkCardID(c.ID, c.Int("attempt"))); prev != nil && IsWithdrawn(prev.Col) {
			// the capped attempt's withdrawn work card is retired, its record kept, as
			// a rework retires the bound's (steps_review.go): a withdrawn card's
			// primary is ready, and this one is working now
			u.Changes = append([]Change{change(Fleet, removeEntry(prev, map[string]string{"retired": stamp(s.Now), "retired_by": "friend"}))}, u.Changes...)
		}
		u.Closes = capJudgments(s, c.ID)
		u.Moved = fmt.Sprintf("%s work %s -> working card=%s member=%s (the attempt cap's default answer: a friend's card, its brief gained WHO: friend %s and its count reset as a replaced brief)", c.ID, c.Col, card, row, name)
		p.Units = append(p.Units, u)
	}
	return Lawful(p)
}

// capJudgments is the brief-defect judgments open on the primary (NBriefWrong):
// closed by the attempt cap's default answer, which changes the brief.
func capJudgments(s *Snapshot, id string) []Open {
	var out []Open
	for _, o := range s.Open {
		if o.Note.Type == NBriefWrong && o.Subject() == id {
			out = append(out, o)
		}
	}
	return out
}

// briefGainsWho is the brief with the WHO line of the friend named gained by
// its header: the first line kept, the line first under it, the rest as it
// was, where cardhdr reads it (ReadWho) and the brief edit's field is written
// from it (FieldWho, WhoOfBrief).
func briefGainsWho(brief, name string) string {
	line, rest, _ := strings.Cut(brief, "\n")
	return cutText(line+"\nWHO: friend "+name+"\n"+rest, MaxCardTextBytes)
}
