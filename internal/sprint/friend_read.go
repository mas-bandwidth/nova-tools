package sprint

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// A read is a card on the fleet table (read_cards.go): a friend's read card is on her
// fleet row and closed from her outbox report or the read verb, as a member's is.

// ParseFriendReadReport reads a friend's read report. LAND closes the read
// ok. HOLD closes it broken when a line names a file, a line or a rule
// (typedrec.NamesADefect, the rule a reader's broken read is held to).
// why is set when the report does not close the read.
func ParseFriendReadReport(report string) (verdict, finding, why string) {
	word, defect := "", ""
	for _, l := range strings.Split(report, "\n") {
		t := strings.TrimSpace(l)
		if word == "" {
			if rest, ok := strings.CutPrefix(t, "Verdict:"); ok {
				fields := strings.Fields(rest)
				if len(fields) > 0 {
					word = strings.ToUpper(strings.Trim(fields[0], "*_.,;:"))
				}
			}
		}
		if defect == "" && typedrec.NamesADefect(l) {
			defect = strings.TrimSpace(l)
		}
	}
	switch word {
	case "LAND":
		return "ok", "", ""
	case "HOLD":
		if defect == "" {
			return "", "", "a HOLD closes a read broken only when a finding names a file, a line or a rule"
		}
		return "broken", defect, ""
	default:
		return "", "", "a friend's read report says Verdict: LAND or Verdict: HOLD"
	}
}

// friendReadTier is the tier a primary's read would be drawn from before
// readTierOf collapses a frontier card onto the tier a route serves. A heavy
// card whose read tier is set to the one above (frontier) is frontier here;
// readTierOf's own return is left as it is.
func friendReadTier(s *Snapshot, pr *Card) string {
	m, _ := cardhdr.ReadModel(pr.F("brief"))
	t := cardTier(pr, m)
	set := s.readTierSetting(pr.Row)
	if set == "" {
		return t
	}
	ladder := []string{cardhdr.RouteFlash, cardhdr.RoutePro, "heavy", cardhdr.RouteFrontier}
	if slices.Index(ladder, set) > slices.Index(ladder, t) {
		return set
	}
	return t
}

// friendAtOrAbove says the friend may be asked a read of tier: one of her tiers
// (friendTiers) is tier or above it on capLadder (docs/SPEC-SPRINT.md, a read
// asked of any unit with room at or above the read tier). A frontier friend
// takes a flash, pro, heavy or frontier read; a flash friend takes a flash read.
func friendAtOrAbove(f FriendSeat, tier string) bool {
	want := slices.Index(capLadder, tier)
	if want < 0 {
		return false
	}
	for _, t := range friendTiers(f) {
		if i := slices.Index(capLadder, t); i >= want {
			return true
		}
	}
	return false
}

// readHeadMatches says the read's head is the primary's head or the attempt's
// work head. A friend's read is asked with the work card's head (attemptEnds);
// a machine read is asked with the primary's. Finish writes both the same.
func readHeadMatches(s *Snapshot, pr, rc *Card) bool {
	h := rc.F("head")
	if h == "" || pr == nil {
		return false
	}
	if h == pr.F("head") {
		return true
	}
	attempt := pr.Int("attempt")
	if attempt == 0 {
		attempt = 1
	}
	_, _, work := attemptEnds(s, pr.ID, attempt)
	return work != "" && h == work
}

// friendReadAgrees says a read card on the fleet table is its reader's: the reader
// its id names and its reader field are one, and its row is that reader's fleet row,
// a friend's (friend.<name>) or a member's (read cards, read_cards.go). A retired read
// has been taken off that row (its row is empty) and the id still names the reader.
func friendReadAgrees(c *Card) bool {
	_, _, idReader, ok := ParseReadCard(c.ID)
	if !ok || c.F("reader") != idReader {
		return false
	}
	if c.Row == "" || c.Row == idReader {
		return true
	}
	name, rowOK := FriendOfRow(c.Row)
	return rowOK && name == idReader
}

// friendReadLive is the friend's reads of the attempt that stand
// (docs/SPEC-SPRINT.md, a read asked of any unit with room at or above the
// read tier). A placed read stands as itself. A retired read whose verdict is
// ok stands as OK when its head matches (readHeadMatches), and one whose
// verdict is broken stands as broken, each a copy so the fleet card is left
// as it is. A read taken back with no verdict, retired or withdrawn on her row,
// does not stand: the attempt is asked of another reader, and the withdrawn
// record stays as history. The ok and broken copies are not placed on a table.
//
// A read card a member is dealt (read_cards.go) is on the fleet table as hers is,
// on the member's row, and stands the same way (fleetReadLive is this, by its
// other name).
func friendReadLive(s *Snapshot, pr *Card) (placed, okCards, broken []*Card) {
	if s == nil || s.Fleet == nil || pr == nil {
		return nil, nil, nil
	}
	return fleetReadLiveOf(s, pr, s.Fleet.Cards())
}

// fleetReadLiveOf is friendReadLive over the fleet cards given (every card of the table,
// or those of the primary a caller indexed once: fleetReadIndex).
func fleetReadLiveOf(s *Snapshot, pr *Card, cards []*Card) (placed, okCards, broken []*Card) {
	attempt := pr.Int("attempt")
	if attempt == 0 {
		attempt = 1
	}
	prefix := pr.ID + ".r" + itoa(attempt) + "."
	for _, c := range cards {
		if c == nil || c.F("kind") != "read" || !strings.HasPrefix(c.ID, prefix) || c.Col == Withdrawn {
			continue
		}
		if c.Placed() {
			if !friendReadAgrees(c) {
				continue
			}
			placed = append(placed, c)
			continue
		}
		if !friendReadAgrees(c) {
			continue
		}
		switch c.F("verdict") {
		case "ok":
			if !readHeadMatches(s, pr, c) {
				continue
			}
			syn := *c
			syn.Col = OK
			okCards = append(okCards, &syn)
		case "broken":
			syn := *c
			syn.Col = Broken
			broken = append(broken, &syn)
		}
	}
	return placed, okCards, broken
}

// fleetReadIndex is the fleet table's read cards, placed or kept, by their primary field,
// in id order: read once for a step that asks of many primaries.
func fleetReadIndex(s *Snapshot) map[string][]*Card {
	out := map[string][]*Card{}
	if s == nil || s.Fleet == nil {
		return out
	}
	for _, c := range s.Fleet.Cards() {
		if c != nil && c.F("kind") == "read" {
			out[c.F("primary")] = append(out[c.F("primary")], c)
		}
	}
	return out
}

// attemptEnds is the work card's branch and head, and the commit the attempt
// started from (the last earlier attempt that finished ok). Finish writes the
// branch on the work card, not the primary (steps_work.go).
func attemptEnds(s *Snapshot, primary string, attempt int) (branch, start, head string) {
	if s.Fleet == nil {
		return "", "", ""
	}
	if wc := s.Fleet.Card(WorkCardID(primary, attempt)); wc != nil {
		branch, head = wc.F("branch"), wc.F("head")
	}
	var earlier []*Card
	for a := 1; a < attempt; a++ {
		if w := s.Fleet.Card(WorkCardID(primary, a)); w != nil {
			earlier = append(earlier, w)
		}
	}
	return branch, BaseOf(earlier).Head, head
}

// attemptWorker is the friend who worked on this attempt of the primary, if any.
// A friend does not read her own work.
func attemptWorker(s *Snapshot, primary string, attempt int) string {
	if s == nil {
		return ""
	}
	if s.Fleet != nil {
		if wc := s.Fleet.Card(WorkCardID(primary, attempt)); wc != nil {
			if name, ok := FriendOfRow(wc.Row); ok {
				return name
			}
			if name, ok := FriendOfRow(wc.F("member")); ok {
				return name
			}
		}
	}
	if s.Work != nil {
		if pr := s.Work.Card(primary); pr != nil && pr.Int("attempt") == attempt {
			if name, ok := FriendOfRow(pr.F("member")); ok {
				return name
			}
		}
	}
	return ""
}

// FriendReadClose applies one friend's read report to the read card on her
// fleet row. LAND and HOLD retire that card (it is not moved to a fleet
// column the fleet does not use, and not onto a readers column). HOLD with a
// finding raises the same broken-read judgment Read raises. The readers table
// gains no row.
func FriendReadClose(s *Snapshot, name, primary, report string) Plan {
	var p Plan
	pr := s.Work.Card(primary)
	if pr == nil {
		p.refuse(primary, "no such primary")
		return p
	}
	attempt := pr.Int("attempt")
	if attempt == 0 {
		attempt = 1
	}
	id := ReadCardID(primary, attempt, name)
	rc := s.Fleet.Card(id)
	if rc == nil || !rc.Placed() {
		p.refuse(primary, "no read asked of "+name)
		return p
	}
	verdict, finding, why := ParseFriendReadReport(report)
	if why != "" {
		p.refuse(primary, why)
		return p
	}
	p.Units = append(p.Units, friendReadCloseUnit(s, name, pr, rc, verdict, finding))
	return p
}

// friendReadCloseUnit is the one close of a friend's read card on her fleet row,
// whether her outbox report (FriendReadClose) or the read verb (friendReadVerb)
// carried the verdict: the card retired with its verdict, a broken verdict's
// judgment, and the primary's review judgment after it.
func friendReadCloseUnit(s *Snapshot, name string, pr, rc *Card, verdict, finding string) Unit {
	attempt := rc.Int("attempt")
	if attempt == 0 {
		attempt = 1
	}
	set := map[string]string{"verdict": verdict, "read": stamp(s.Now), "retired": stamp(s.Now), "retired_by": "read"}
	var notes []Note
	if verdict == "broken" {
		set["finding"] = finding
		n := judgment(NReadBroken, pr.Row, s.Now, pr.Int("broken_reads"), pr.ID)
		n.Who, n.Attempt, n.What = name, attempt, finding
		notes = append(notes, n)
	}
	// the card is still placed when the judgment is read; the verdict is the column it stands as
	stood := OK
	if verdict == "broken" {
		stood = Broken
	}
	if j, ok := reviewJudgment(s, pr, reviewStep{moved: map[string]string{rc.ID: stood}, writes: notes, who: name}); ok {
		notes = append(notes, j)
	}
	return Unit{Key: pr.ID, Stream: pr.Row, Changes: []Change{
		change(Fleet, removeEntry(rc, set)),
	}, Moved: rc.ID + " retired " + verdict, Notes: notes}
}

// FriendReadOutboxLine is how a read on a friend's row is returned: her
// outbox report, which friend sync reads (FriendReadClose), or the read verb
// her packet prints, which writes the same close (friendReadVerb).
func FriendReadOutboxLine(row, id string, epoch uint64) string {
	return fmt.Sprintf("write outbox/%s/REPORT.md in your working directory with 'Verdict: LAND', or 'Verdict: HOLD' and a line naming the file:line or rule and what to change (friend sync reads it); or run: nova-sprint read --as %s (--ok | --broken) %s --epoch %d --finding '<file:line, and what to change>'", id, row, id, epoch)
}

// friendReadVerb is the read verb on a friend's row (docs/SPEC-SPRINT.md
// section 5): her read cards are on her fleet row, not the readers table, and
// a verdict there is the close her outbox report makes (friendReadCloseUnit).
// A read named at any generation, 0 included (the reads asked before the ask
// wrote one), is hers to report. A read on her row has no begin and is handed
// back by friend take, not by --return.
func friendReadVerb(s *Snapshot, r ReadReq, name string) Plan {
	return readCardVerb(s, r, FriendRow(name), name)
}

// readCardVerb is the read verb on a fleet row, a friend's or a member's (read_cards.go):
// a verdict closes its read card as her outbox report does (friendReadCloseUnit); a return
// (--return, with the reason) hands it back with no verdict, retired by returned, which
// spends the reader's read of the attempt, and the read-card deal deals it to another
// reader. A read card has no begin: the take moves it to working.
func readCardVerb(s *Snapshot, r ReadReq, row, name string) Plan {
	var p Plan
	if r.Begin {
		p.refuse("read", "a read card has no begin: the take moves it to working; report it with "+
			"read --as "+row+" (--ok | --broken) <read> --finding <text>, hand it back with read --as "+row+" --return <read> --reason <text>, or write outbox/<read>/REPORT.md")
		return p
	}
	if s.Fleet == nil {
		p.refuse("read", "the fleet table was not read")
		return p
	}
	sel := r.Sel
	if len(sel.IDs) == 0 && sel.Only == nil && sel.Limit == 0 {
		sel.Limit = 1
	}
	var all []*Card
	for _, col := range []string{Working, Ready} {
		for _, c := range s.Fleet.Cell(row, col) {
			if c.F("kind") == "read" {
				all = append(all, c)
			}
		}
	}
	SortCards(all)
	chosen := pick(&p, sel, all, fieldStream, func(c *Card) string {
		switch {
		case c.F("kind") != "read":
			return "not a read (it is " + orDash(c.F("kind")) + "): work is reported with finish"
		case !c.Placed():
			return "retired at " + orDash(c.F("retired")) + " by " + orDash(c.F("retired_by")) + ": nothing to report"
		case c.Row != row:
			return "not " + r.As + "'s to read (it is " + placeWord(c) + ")"
		case s.Work.Card(c.F("primary")) == nil:
			return "its primary " + c.F("primary") + " is not on the table"
		}
		return ""
	}, s.Fleet.Card)
	namePrimarysReads(&p, all)
	for _, c := range chosen {
		pr := s.Work.Card(c.F("primary"))
		if r.Return {
			set := map[string]string{"retired": stamp(s.Now), "retired_by": RetiredByReturned, "reason": cutText(r.Reason, MaxCardTextBytes)}
			if r.Usage != "" {
				set["usage"] = r.Usage
			}
			n := happened(NReadReturned, pr.Row, s.Now, pr.ID)
			n.Who, n.Attempt, n.What = name, c.Int("attempt"), name+" returned "+c.ID+": "+r.Reason
			p.Units = append(p.Units, Unit{Key: pr.ID, Stream: pr.Row, Changes: []Change{change(Fleet, removeEntry(c, set))},
				Moved: c.ID + " " + c.Col + " -> returned (retired: " + name + " gave no verdict)", Notes: []Note{n}})
			continue
		}
		u := friendReadCloseUnit(s, name, pr, c, r.Verdict, r.Finding)
		if r.Usage != "" {
			u.Changes[0].Entry.Set["usage"] = r.Usage
		}
		u.Moved = c.ID + " " + c.Col + " -> " + r.Verdict + " (retired: read by " + name + ")"
		p.Units = append(p.Units, u)
	}
	return p
}

// markWaiting writes the waiting mark and its note on each primary in review that waits
// (waits: why), once an attempt, and clears the mark on each primary the plan asks (asked).
func markWaiting(p *Plan, s *Snapshot, asked map[string]int, waits map[string]string) {
	var marks []Unit
	for _, c := range s.Work.Column(Review) {
		attempt := c.Int("attempt")
		if attempt == 0 {
			attempt = 1
		}
		if i, ok := asked[c.ID]; ok {
			if c.F(FieldWaitingReader) != "" {
				p.Units[i].Changes = unsetOn(p.Units[i].Changes, c, FieldWaitingReader)
			}
			continue
		}
		why, ok := waits[c.ID]
		if !ok || waitingAt(c) == attempt {
			continue
		}
		n := happened(NWaitingForReader, c.Row, s.Now, c.ID)
		n.Attempt = attempt
		n.What = c.ID + " waits for a reader at attempt " + itoa(attempt) + ": " + why + "; the tick asks it when one has room (readers: " + readersText(s) + ")"
		marks = append(marks, Unit{Key: c.ID, Stream: c.Row,
			Changes: []Change{change(Work, setEntry(c, map[string]string{FieldWaitingReader: itoa(attempt) + " " + stamp(s.Now)}))},
			Moved:   c.ID + " waiting for a reader (" + why + ")", Notes: []Note{n}})
	}
	p.Units = append(p.Units, marks...)
}

// waitingAt is the attempt the primary's waiting mark was written at, 0 when
// it has none.
func waitingAt(c *Card) int {
	v, _, _ := strings.Cut(c.F(FieldWaitingReader), " ")
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}

// unsetOn unsets the field of the card in the unit's work change of it, or
// adds that change when the unit has none.
func unsetOn(changes []Change, c *Card, field string) []Change {
	for i, ch := range changes {
		if ch.Table == Work && ch.Entry.ID == c.ID {
			if !slices.Contains(ch.Entry.Unset, field) {
				changes[i].Entry.Unset = append(slices.Clone(ch.Entry.Unset), field)
			}
			return changes
		}
	}
	return append(changes, change(Work, setEntry(c, nil, field)))
}
