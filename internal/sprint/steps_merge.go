package sprint

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
)

// MergeReq is one mechanical merge step for a stream, given its facts by the
// caller. The step never decides: what merged, what conflicted and the CI
// result are facts.
type MergeReq struct {
	Stream string
	Batch  int
	// Cards, when given, is the batch by name: exactly these cards of the stream's
	// queue, wherever they stand in it, and Batch is not read. A landing reports the
	// cards it pushed, never "the first n": the tick's accepts put cards in the queue
	// by their order of work, often ahead of the ones a landing is building, and a
	// report by place would then record cards that were not pushed (nova-sprint land).
	Cards []string
	// Landed is the record by name and head (docs/SPEC-SPRINT.md section 8, merge --landed): each
	// card the lander pushed with the head it pushed, and the caller's fact that the head is an
	// ancestor of the base branch's tip. The step lands exactly these cards, and refuses all of
	// them, naming each card and why, and writes nothing, unless every one is merging in the
	// stream, at the head given, with that head in the base.
	Landed   []LandedPin `json:",omitempty"`
	Conflict string      // a card of the batch that did not merge
	// ConflictKind and ConflictPaths are what the lander knows of a conflict: "file" when the
	// paths that did not merge are files no generated ledger owns, "ledger" when one is, and
	// the paths; written on the stream's control card with the stop (FieldConflictKind,
	// FieldConflictPaths), which the conflict rule reads (rules.go). "" is not known.
	ConflictKind  string   `json:",omitempty"`
	ConflictPaths []string `json:",omitempty"`
	Cross         string   // "<card>=<other>": a card that needs a card of another stream first
	Red           bool     // the stream branch went red on the batch
	Suspects      []string // with Red: the cards of the batch the caller suspects
	Rejected      bool     // the merge queue rejected the batch
	// BaseRed is the lander's base-gate rule's stop (docs/SPEC-SPRINT.md section 8, answered by
	// rule): the base failed its tree gate at its tip three times, and this is the error. The
	// stream stops with the judgment NBaseRed; no card moves.
	BaseRed string `json:",omitempty"`
	// BaseRefused is a landing refused because the base failed its tree gate at its tip (the
	// lander ran the gate and it was red), this the finding, Base the base's branch: the
	// refusal is counted on the stream's control card per base (FieldBaseGateRefused), across
	// passes and processes, and the BaseGateStops-th stops the stream with the judgment NBaseRed
	// naming the base, the gate and the first refusal. No card moves.
	BaseRefused string `json:",omitempty"`
	Base        string `json:",omitempty"`
	// MissingBase is a land whose base branch is gone: the merge step raises one
	// judgment naming every unlanded card on that base and the rebase line that
	// fixes them (rebase.go). No card moves.
	MissingBase string `json:",omitempty"`
	// DeadBase is the base the batch's cards (Cards) name, which the lander's fetch found
	// not on origin (docs/SPEC-SPRINT.md section 7, a dead base): each card is marked
	// (FieldDeadBase) and one judgment NDeadBase is raised for it; the stream is not
	// stopped and no card moves.
	DeadBase string `json:",omitempty"`
	Note     string
	Who      string
	// Resolved is, by card, what its landing did beyond merging its head (docs/SPEC-SPRINT.md
	// section 7: the generated ledgers regenerated at the merge); written on its merge card
	// as it lands, its note on the card's timeline.
	Resolved map[string]string
}

// LandedPin is one card of a record by name: its id, the head the caller pushed, and the
// caller's fact (one git merge-base --is-ancestor against a fetched tip, never run by the
// tick) that the head is an ancestor of the base branch's tip.
type LandedPin struct {
	ID     string
	Head   string
	InBase bool
}

// landedRefusals is why each pin of a record by name is refused, in the pins' order: a card
// not merging in the stream, named twice, given another head than the card's now (returned,
// re-queued or re-cut since it was pushed), or whose head the base does not hold. Empty when
// the record is lawful (docs/SPEC-SPRINT.md section 8, merge --landed).
func landedRefusals(s *Snapshot, stream string, pins []LandedPin) []Refusal {
	var out []Refusal
	seen := map[string]bool{}
	for _, pin := range pins {
		pr, m := s.Work.Placed(pin.ID), s.Merge.Placed(pin.ID)
		why := ""
		switch {
		case seen[pin.ID]:
			why = "named twice in the record"
		case pr == nil || pr.Row != stream || pr.Col != Merging || m == nil || m.Row != stream || m.Col != Queued:
			why = "not merging in stream " + stream + " (it is " + placeWord(orEmpty(pr, pin.ID)) + ")"
		case pin.Head == "":
			why = "no head given; it wants <id>@<head>"
		case pr.F("head") != pin.Head:
			why = "head " + pin.Head + " given, the card is at head " + orDash(pr.F("head")) + " now (returned, re-queued or re-cut since it was pushed); nothing recorded for it"
		case !pin.InBase:
			why = "head " + pin.Head + " is not an ancestor of the base branch's tip; it was not landed"
		}
		seen[pin.ID] = true
		if why != "" {
			out = append(out, Refusal{pin.ID, why})
		}
	}
	return out
}

// The base-gate count on a stream's control card (docs/SPEC-SPRINT.md section 8,
// land-base-gate-stops-stream): how many landings in a row were refused on the base's tree
// gate, the base they were refused on, and when the first was. A pass that merges, any other
// stop, a stop on the base and a resume clear it.
const (
	FieldBaseGateRefused = "base_gate_refused"
	FieldBaseGateBase    = "base_gate_base"
	FieldBaseGateFirst   = "base_gate_first"
)

var baseGateCount = []string{FieldBaseGateRefused, FieldBaseGateBase, FieldBaseGateFirst}

// BaseGateStops is the refusals on one base that stop its stream: the first failure of its
// tree gate and one at each retry (BaseGateRetries).
var BaseGateStops = len(BaseGateRetries) + 1

// baseGateStep counts a refusal on the base's gate on the stream's control card, and stops
// the stream with the judgment NBaseRed at the BaseGateStops-th, or at once on BaseRed. A
// count on another base starts again at one.
func baseGateStep(p Plan, s *Snapshot, ctl *Card, r MergeReq) Plan {
	n, first := 1, stamp(s.Now)
	if m := ctl.Int(FieldBaseGateRefused); m > 0 && ctl.F(FieldBaseGateBase) == r.Base {
		n = m + 1
		if f := ctl.F(FieldBaseGateFirst); f != "" {
			first = f
		}
	}
	if r.BaseRed == "" && n < BaseGateStops {
		set := map[string]string{FieldBaseGateRefused: itoa(n), FieldBaseGateBase: r.Base, FieldBaseGateFirst: first}
		p.Units = append(p.Units, Unit{Key: ctl.ID, Stream: r.Stream, Changes: []Change{change(Merge, setEntry(ctl, set))},
			Moved: fmt.Sprintf("stream %s: the base %s refused at its tree gate (%d of %d)", r.Stream, r.Base, n, BaseGateStops)})
		return p
	}
	// the base, not a card: the stream stops with the error, every card where it is
	what := r.BaseRed
	if what == "" {
		what = r.BaseRefused
	}
	if r.Base != "" {
		at, _ := time.Parse(time.RFC3339, first)
		what = fmt.Sprintf("the base %s fails its tree gate, refused %d times, first refused at %s: %s", r.Base, n, at.UTC().Format("15:04:05 MST"), what)
	}
	set := map[string]string{"state": StreamStopped, "since": stamp(s.Now), "cause": "base"}
	j := judgment(NBaseRed, r.Stream, s.Now, 0)
	j.StreamLevel, j.Who, j.What = true, r.Who, cutText(what, MaxCardTextBytes)
	p.Units = append(p.Units, Unit{Key: ctl.ID, Stream: r.Stream, Changes: []Change{change(Merge, setEntry(ctl, set, append([]string{"card", "other"}, baseGateCount...)...))}, Notes: []Note{j},
		Moved: "stream " + r.Stream + " stopped: the base fails its tree gate"})
	return p
}

// missingBaseStep raises the one judgment of a land whose base branch is gone:
// every unlanded card on that base, and the rebase line that fixes them
// (rebase.go). No card moves and the stream is not stopped: the cards are still
// landable once their brief names a base that exists.
func missingBaseStep(p Plan, s *Snapshot, r MergeReq) Plan {
	cards := MissingBaseCards(s, r.MissingBase)
	j := MissingBaseJudgment(r.MissingBase, cards)
	j.Who, j.At = r.Who, s.Now
	if len(cards) == 0 {
		return p
	}
	p.Notes = append(p.Notes, j)
	return p
}

// A dead base (docs/SPEC-SPRINT.md section 7, a dead base). On 2026-10-04 a merging
// card named a branch that was then deleted from origin. The lander tried that branch
// 203 times, every 6 seconds, and the stream held behind the card for half an hour. A
// base the lander's fetch finds not on origin is one fact: the card is marked with the
// base (FieldDeadBase) and one judgment NDeadBase is raised for it, its stream is not
// stopped, and the land pass skips the card (DeadBaseHeld) until the judgment is
// answered or the base is re-pointed (CardBase), then tries it once.

// NDeadBase is the judgment of a merging card whose BASE is not on origin.
const NDeadBase = "a merging card names a base not on origin"

// FieldDeadBase is the base a merging card names that the lander found not on origin, on
// the primary; FieldBaseSet is the last re-point of its BASE (CardBase): old -> new, when,
// by whom.
const (
	FieldDeadBase = "dead_base"
	FieldBaseSet  = "base_set"
)

func init() {
	// the re-point is the judgment's words (DeadBaseWhy); ack answers it, and the next land
	// pass tries the card once more
	Decisions[NDeadBase] = []string{"look at the card", "return", "drop", "ack"}
}

// DeadBaseWhy is the one sentence of a dead base, the lander's refusal and the judgment's.
func DeadBaseWhy(id, base string) string {
	return "card " + id + " names BASE " + base + ", which is not on origin"
}

// deadBaseOpen is the open judgments of a dead base on the card.
func deadBaseOpen(s *Snapshot, id string) []Open {
	var out []Open
	for _, o := range s.Open {
		if o.Note.Type == NDeadBase && o.Subject() == id {
			out = append(out, o)
		}
	}
	return out
}

// DeadBaseHeld says the land pass skips the card: it is marked dead on the base it names
// now, and that judgment is open. A re-pointed base or an answered judgment arms it again.
func DeadBaseHeld(s *Snapshot, c *Card, base string) bool {
	return c != nil && base != "" && c.F(FieldDeadBase) == base && len(deadBaseOpen(s, c.ID)) > 0
}

// deadBaseStep marks each card of the batch dead on its base and raises one judgment for
// it. A card held already (a replay, or a second lander) writes nothing: one fact, once.
func deadBaseStep(p Plan, s *Snapshot, r MergeReq) Plan {
	if len(r.Cards) == 0 {
		p.refuse(r.Stream, "a dead base names its cards; nothing was changed")
		return p
	}
	for _, id := range r.Cards {
		pr, m := s.Work.Placed(id), s.Merge.Placed(id)
		switch {
		case pr == nil || pr.Row != r.Stream || pr.Col != Merging || m == nil || m.Row != r.Stream || m.Col != Queued:
			p.refuse(id, "not merging in stream "+r.Stream+" (it is "+placeWord(orEmpty(pr, id))+")")
			continue
		case DeadBaseHeld(s, pr, r.DeadBase):
			continue
		}
		why := DeadBaseWhy(id, r.DeadBase)
		j := judgment(NDeadBase, r.Stream, s.Now, 0, id)
		j.Who, j.Card = r.Who, id
		j.What = cutText(why+"; re-point it: nova-sprint card base "+id+" <a branch on origin>; or ack this and the lander tries it once more", MaxCardTextBytes)
		p.Units = append(p.Units, Unit{Key: id, Stream: r.Stream, Changes: []Change{change(Work, setEntry(pr, map[string]string{FieldDeadBase: r.DeadBase}))},
			Notes: []Note{j}, Moved: why + "; held from landing"})
	}
	return p
}

// baseRefRE is a branch name's shape: what git's check-ref-format accepts for the branches
// a card's BASE may name. Glob and revision characters are refused, so `card base <id> "*"`
// never stands in for the advertised branch it is not (the reader's finding, attempt 4).
var baseRefRE = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]*$`)

// ValidBase says base is a branch name a lander could cut: the shape above and the
// component rules git adds (no "..", "//", a trailing "/", ".lock" or ".").
func ValidBase(base string) bool {
	return len(base) <= 200 && baseRefRE.MatchString(base) && !strings.Contains(base, "..") &&
		!strings.Contains(base, "//") && !strings.HasSuffix(base, "/") &&
		!strings.HasSuffix(base, ".lock") && !strings.HasSuffix(base, ".")
}

// CardBaseReq re-points a merging card's BASE (nova-sprint card base). OnOrigin is the
// caller's fact, one git ls-remote, that the branch is on the card's origin.
type CardBaseReq struct {
	ID, Base string
	OnOrigin bool
	Who      string
}

// CardBase re-points a merging card's BASE: its brief's BASE: line names the new branch,
// its dead-base mark is cleared and its dead-base judgment answered, and one log line names
// the base before and after. The work, its head, its attempt and its reads are kept, and the
// card stays where it is in the merge queue: the next land pass tries it once against the
// new base. A branch not on origin is refused, nothing changed (docs/SPEC-SPRINT.md section
// 7, a dead base).
func CardBase(s *Snapshot, r CardBaseReq) Plan {
	var p Plan
	p.on(s)
	if why := notCoordinator(s, r.Who, "card base"); why != "" {
		p.refuse(r.ID, strings.Replace(why, "answers a judgment, which is", "is", 1))
		return p
	}
	c := s.Work.Placed(r.ID)
	var why string
	switch {
	case c == nil:
		why = r.ID + " is no card on the table"
	case c.Col != Merging:
		why = r.ID + " is " + c.Col + ": card base re-points a merging card; a card before merging takes a new brief (nova-sprint brief)"
	case !ValidBase(r.Base):
		why = "'" + r.Base + "' is not a branch name"
	case !r.OnOrigin:
		why = r.Base + " is not a branch on origin; push the branch or name one origin holds; run: nova-sprint card base " + r.ID + " <a branch on origin>"
	}
	if why != "" {
		p.refuse(r.ID, why+"; nothing was changed")
		return p
	}
	brief, old, ok := repointBase(c.F("brief"), r.Base)
	switch {
	case !ok:
		why = r.ID + "'s brief names no BASE: line; land gives it one (--base)"
	case old == r.Base:
		why = r.ID + " names BASE " + r.Base + " already"
	}
	if why != "" {
		p.refuse(r.ID, why+"; nothing was changed")
		return p
	}
	moved := fmt.Sprintf("%s BASE %s -> %s", c.ID, old, r.Base)
	set := map[string]string{"brief": brief, FieldBaseSet: fmt.Sprintf("%s -> %s %s by %s", old, r.Base, stamp(s.Now), orDash(r.Who))}
	u := Unit{Key: c.ID, Stream: c.Row, Changes: []Change{change(Work, setEntry(c, set, FieldDeadBase))}, Moved: moved}
	if open := deadBaseOpen(s, c.ID); len(open) > 0 {
		u.Closes = open
		u.Notes = append(u.Notes, decided(open[0], "card base: "+moved, r.Who, s.Now, c.ID))
	}
	p.Units = append(p.Units, u)
	return p
}

// repointBase is the brief with its first BASE: line naming base (a pin, @<sha>, of the old
// base dropped with it), and the base it named; false when it names none.
func repointBase(brief, base string) (string, string, bool) {
	lines := strings.SplitAfter(brief, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "BASE:") {
			continue
		}
		old, _, _ := strings.Cut(strings.TrimSpace(strings.TrimPrefix(trimmed, "BASE:")), "@")
		indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
		end := line[len(strings.TrimRight(line, "\r\n")):]
		lines[i] = indent + "BASE: " + base + end
		return strings.Join(lines, ""), strings.TrimSpace(old), true
	}
	return brief, "", false
}

// streamDone says every primary of the stream on the table has landed, given
// the ones about to land, and at least one has.
func streamDone(s *Snapshot, stream string, landing int) bool {
	landed := s.Work.Count(stream, Landed) + landing
	open := 0
	for _, st := range []State{Waiting, Ready, Working, Review, Merging} {
		open += s.Work.Count(stream, st)
	}
	return landed > 0 && open == landing
}

// crossRefusal is why a cross fact "<card>=<other>" is refused, "" when it
// holds: the other card is a primary placed on the table, in another stream
// than the card's, and not landed.
func crossRefusal(s *Snapshot, stream, card, other string) string {
	oc := s.Work.Placed(other)
	switch {
	case other == "":
		return "the cross fact names no other card; it wants <card>=<other>"
	case other == card:
		return "the cross fact names the card itself (" + card + "); the other card is in another stream"
	case oc == nil:
		return "the other card " + other + " is not on the table; the other card is placed, in another stream, not landed"
	case oc.Row == stream:
		return "the other card " + other + " is in the same stream " + stream + "; the other card is in another stream"
	case oc.Col == Landed:
		return "the other card " + other + " (stream " + oc.Row + ") has landed already; nothing to wait for"
	}
	return ""
}

// span is a batch by its first and last card.
func span(ids []string) string {
	switch len(ids) {
	case 0:
		return "empty"
	case 1:
		return ids[0]
	}
	return ids[0] + " .. " + ids[len(ids)-1]
}

// MergeStep merges eligible cards by priority, preserving dependencies, as one
// batch; or, given a fact that stops the stream, stops it and tells the
// coordinator why. A stopped stream moves only after resume. A conflict fact
// on the card's own head (RefusalWay) stops nothing: the card is reworked at
// the tip, or returned for the widen rule, and the stream goes on
// (landRefused).
func MergeStep(s *Snapshot, r MergeReq) Plan { return Lawful(mergeStep(s, r)) }

func mergeStep(s *Snapshot, r MergeReq) Plan {
	var p Plan
	p.on(s)
	ctl := s.StreamCtl(r.Stream)
	if ctl == nil {
		p.refuse(r.Stream, "no such stream")
		return p
	}
	state := ctl.F("state")
	switch state {
	case StreamStopped:
		p.refuse(r.Stream, "stopped ("+ctl.F("cause")+"); run: nova-sprint resume --stream "+r.Stream)
		return p
	case StreamLanded:
		// a landing the stream dealt is still recorded; landed with nothing owed refuses
		if !StreamOwesLanding(s, r.Stream) {
			p.refuse(r.Stream, "landed")
			return p
		}
	}
	if r.DeadBase != "" {
		return deadBaseStep(p, s, r)
	}
	if r.BaseRed != "" || r.BaseRefused != "" {
		return baseGateStep(p, s, ctl, r)
	}
	if r.MissingBase != "" {
		return missingBaseStep(p, s, r)
	}
	// A stuck card is a barrier: the step never passes an earlier stuck card.
	queued := s.Merge.Cell(r.Stream, Queued)
	if stuck := s.Merge.Cell(r.Stream, Stuck); len(stuck) > 0 {
		var before []*Card
		for _, c := range queued {
			if c.Score < stuck[0].Score || (c.Score == stuck[0].Score && c.ID < stuck[0].ID) {
				before = append(before, c)
			}
		}
		queued = before
	}
	beforePriority := len(queued)
	queued = MergePriorityOrder(s, queued)
	now := stamp(s.Now)
	if len(queued) == 0 {
		why := "nothing queued in stream " + r.Stream + "; nothing was changed"
		if beforePriority > 0 {
			why = "no queued card has its prerequisites satisfied in stream " + r.Stream + "; nothing was changed; land its prerequisites first"
		} else if len(s.Merge.Cell(r.Stream, Stuck)) > 0 {
			why = "nothing queued before the stuck card of stream " + r.Stream + "; resume it first; nothing was changed"
		}
		p.refuse(r.Stream, why)
		return p
	}
	if len(r.Landed) > 0 {
		if why := landedRefusals(s, r.Stream, r.Landed); len(why) > 0 {
			p.Refused = append(p.Refused, why...)
			return p
		}
		r.Cards = make([]string, len(r.Landed))
		for i, pin := range r.Landed {
			r.Cards[i] = pin.ID
		}
	}
	n := r.Batch
	if n <= 0 || n > len(queued) {
		n = len(queued)
	}
	batch := queued[:n]
	if len(r.Cards) > 0 {
		// the batch by name: every named card queued here, in the queue's order
		batch = nil
		for _, c := range queued {
			if contains(r.Cards, c.ID) {
				batch = append(batch, c)
			}
		}
		if len(batch) != len(r.Cards) {
			p.refuse(r.Stream, fmt.Sprintf("the batch names %d cards and %d of them are queued in stream %s now; nothing was changed", len(r.Cards), len(batch), r.Stream))
			return p
		}
	}
	var ids []string
	landing := map[string]bool{}
	for _, c := range batch {
		if needs := WaitsFor(s, s.Work.Card(c.ID), landing); len(needs) > 0 {
			p.refuse(c.ID, "the batch omits prerequisites "+strings.Join(needs, ", ")+"; nothing was changed; run: nova-sprint merge --stream "+r.Stream)
			return p
		}
		ids = append(ids, c.ID)
		landing[c.ID] = true
	}
	ctlSet := map[string]string{}
	var notes []Note
	if state == StreamWaiting || state == StreamWorking || state == StreamLanded {
		ctlSet["state"], ctlSet["since"] = StreamMerging, now
		m := happened(NStartedMerging, r.Stream, s.Now)
		m.Who = r.Who
		if state == StreamLanded {
			m.What = "a landing the stream dealt, after its stop"
		}
		notes = append(notes, m)
	}
	// A card named by a fact is a card of the batch: the first n queued, or the
	// cards the batch names.
	notInBatch := func(id string) bool {
		if contains(ids, id) {
			return false
		}
		listed := strings.Join(ids, ", ")
		if len(ids) > MaxLook {
			listed = span(ids) + "; list it: nova-sprint queue --stream " + r.Stream + " --max " + itoa(len(ids))
		}
		p.refuse(id, "not a card of the batch; the batch of "+itoa(len(ids))+" is "+listed)
		return true
	}
	stop := func(cause, typ string, primaries []string, before int, unset ...string) Unit {
		ctlSet["state"], ctlSet["since"], ctlSet["cause"] = StreamStopped, now, cause
		j := judgment(typ, r.Stream, s.Now, before, primaries...)
		j.StreamLevel, j.Who, j.What = true, r.Who, r.Note
		return Unit{Key: ctl.ID, Stream: r.Stream, Changes: []Change{change(Merge, setEntry(ctl, ctlSet, append(unset, baseGateCount...)...))}, Notes: append(notes, j)}
	}
	switch {
	case r.Conflict != "":
		m := s.Merge.Placed(r.Conflict)
		if m == nil || m.Row != r.Stream || m.Col != Queued {
			p.refuse(r.Conflict, "not queued in stream "+r.Stream)
			return p
		}
		if notInBatch(r.Conflict) {
			return p
		}
		pr := s.Work.Placed(r.Conflict)
		if way := RefusalWay(r.ConflictKind, r.Note); way != "" {
			// a refusal of the card's own head never stops the stream: the card is reworked at
			// the tip, or returned for the widen rule, and the stream lands on (redo.go)
			if pr == nil || pr.Col != Merging {
				p.refuse(r.Conflict, "queued in merge but not merging in work ("+placeWord(orEmpty(pr, r.Conflict))+"); run: nova-sprint check")
				return p
			}
			p.Units = append(p.Units, landRefused(s, r, way, state, ctl, ctlSet, notes, pr, m))
			break
		}
		// the lander's own failure (a generated ledger it could not resolve, a head that is no
		// commit or that origin does not hold, a conflict it did not place): a mind's, the
		// stream stopped
		ctlSet["card"] = r.Conflict
		if r.ConflictKind != "" {
			ctlSet[FieldConflictKind] = r.ConflictKind
		}
		if len(r.ConflictPaths) > 0 {
			ctlSet[FieldConflictPaths] = cutText(strings.Join(r.ConflictPaths, ","), MaxProviderErrorBytes)
		}
		u := stop("conflict", NConflict, []string{r.Conflict}, pr.Int("stuck"), "other")
		u.Notes[len(u.Notes)-1].Card = r.Conflict
		// a conflict stop has no cross need: whatever the card once needed
		u.Changes = append(u.Changes, change(Merge, moveEntry(m, r.Stream, Stuck, nil, "need_card", "need_stream")))
		if pr != nil {
			u.Changes = append(u.Changes, change(Work, setEntry(pr, map[string]string{"stuck": itoa(pr.Int("stuck") + 1)})))
		}
		u.Moved = fmt.Sprintf("stream %s stopped: %s queued -> stuck (conflict)", r.Stream, r.Conflict)
		p.Units = append(p.Units, u)
	case r.Cross != "":
		card, other, ok := strings.Cut(r.Cross, "=")
		m := s.Merge.Placed(card)
		if !ok || m == nil || m.Row != r.Stream || m.Col != Queued {
			p.refuse(card, "the cross fact wants <card>=<other> with the card queued in stream "+r.Stream)
			return p
		}
		if notInBatch(card) {
			return p
		}
		if why := crossRefusal(s, r.Stream, card, other); why != "" {
			p.refuse(card, why)
			return p
		}
		otherStream := s.Work.Placed(other).Row
		ctlSet["card"], ctlSet["other"] = card, other
		u := stop("cross", NCross, []string{card, other}, 0)
		j := &u.Notes[len(u.Notes)-1]
		j.What = fmt.Sprintf("%s (stream %s) needs %s (stream %s) landed first", card, r.Stream, other, orDash(otherStream))
		j.Card, j.Other, j.OtherStream = card, other, otherStream
		u.Changes = append(u.Changes, change(Merge, moveEntry(m, r.Stream, Stuck, map[string]string{"need_card": other, "need_stream": otherStream})))
		u.Moved = fmt.Sprintf("stream %s stopped: %s queued -> stuck, needs %s (stream %s) landed first", r.Stream, card, other, orDash(otherStream))
		p.Units = append(p.Units, u)
	case r.Red:
		for _, x := range r.Suspects {
			if !contains(ids, x) {
				p.refuse(x, "a suspect is a card of the batch; the batch of "+itoa(len(ids))+" is "+span(ids)+"; list it: nova-sprint queue --stream "+r.Stream+" --max "+itoa(len(ids)))
			}
		}
		if len(p.Refused) > 0 {
			return p
		}
		ctlSet["ci"] = "red"
		u := stop("red", NRed, ids, 0, "card", "other")
		j := &u.Notes[len(u.Notes)-1]
		j.Suspects = append([]string(nil), r.Suspects...)
		if len(r.Suspects) > 0 {
			ctlSet["suspects"] = strings.Join(r.Suspects, ",")
			j.What = "suspects: " + strings.Join(r.Suspects, ", ") + " (of the batch of " + itoa(len(ids)) + ")"
		} else {
			j.What = "no suspect named; the batch of " + itoa(len(ids)) + " is " + span(ids) + "; list it: nova-sprint queue --stream " + r.Stream + " --max " + itoa(len(ids))
		}
		for _, c := range batch {
			if pr := s.Work.Placed(c.ID); pr != nil {
				u.Changes = append(u.Changes, change(Work, setEntry(pr, map[string]string{"ci": "red", "ci_at": now})))
			}
		}
		u.Moved = fmt.Sprintf("stream %s stopped: branch red on a batch of %d", r.Stream, len(ids))
		p.Units = append(p.Units, u)
	case r.Rejected:
		u := stop("rejected", NRejected, ids, 0, "other", "card")
		u.Moved = fmt.Sprintf("stream %s stopped: the merge queue rejected a batch of %d", r.Stream, len(ids))
		p.Units = append(p.Units, u)
	default:
		// A card queued in merge but not merging in work is refused; the
		// stream's control change and its notes ride on the first card that
		// lands, and the batch note lists only the cards that landed.
		var landing []*Card
		var landed []string
		for _, c := range batch {
			if pr := s.Work.Placed(c.ID); pr == nil || pr.Col != Merging {
				p.refuse(c.ID, "queued in merge but not merging in work ("+placeWord(orEmpty(pr, c.ID))+"); run: nova-sprint check")
				continue
			}
			landing = append(landing, c)
			landed = append(landed, c.ID)
		}
		if len(landing) == 0 {
			return p
		}
		ctlSet["ci"], ctlSet["moved"] = "green", now
		switch {
		case streamDone(s, r.Stream, len(landing)):
			ctlSet["state"], ctlSet["since"] = StreamLanded, now
		case s.Merge.Count(r.Stream, Queued)+s.Merge.Count(r.Stream, Stuck) == len(landing):
			// The last queued card lands and the stream is not done: nothing
			// is queued or stuck, so the stream is waiting. Waiting and working
			// stay as they were. A stream that was landed while it still owed
			// this landing leaves landed.
			if state == StreamWaiting || state == StreamWorking {
				delete(ctlSet, "state")
				delete(ctlSet, "since")
				notes = nil
			} else {
				ctlSet["state"], ctlSet["since"] = StreamWaiting, now
				if state == StreamLanded {
					notes = nil
				}
			}
		}
		// What each card cost, its total's charged figure as the card carries it
		// (cost.go, FieldCostTotal), written on it as it lands (FieldCost), and the
		// stream's sum over every landed card, set on its control card, which the work
		// table's cost column shows (Cost): a sum of the cards, set, never added to, so
		// a replay writes the same and a clear empties it with the tables.
		costs := map[string]string{}
		sum := []string{}
		for _, pr := range s.Work.Cell(r.Stream, Landed) {
			if v := pr.F(FieldCost); v != "" {
				sum = append(sum, v)
			}
		}
		for _, c := range landing {
			if v := cardcost.ParseTotal(s.Work.Placed(c.ID).F(FieldCostTotal)).Charged; v != "" {
				costs[c.ID] = v
				sum = append(sum, v)
			}
		}
		if total, ok := cardcost.Sum(sum...); ok && len(sum) > 0 && total != ctl.F(FieldCost) {
			ctlSet[FieldCost] = total
		}
		for i, c := range landing {
			u := Unit{Key: c.ID, Stream: r.Stream}
			if i == 0 {
				// a pass that merges: the base passed its gate, and its count starts again
				u.Changes = append(u.Changes, change(Merge, setEntry(ctl, ctlSet, baseGateCount...)))
				u.Notes = notes
			}
			merged := map[string]string{"merged": now}
			if v := r.Resolved[c.ID]; v != "" {
				merged["note"] = v
			}
			u.Changes = append(u.Changes, change(Merge, moveEntry(c, r.Stream, Merged, merged)))
			set := map[string]string{"ci": "green", "landed": now}
			if v := costs[c.ID]; v != "" {
				set[FieldCost] = v
			}
			u.Changes = append(u.Changes, change(Work, moveEntry(s.Work.Placed(c.ID), r.Stream, Landed, set)))
			u.Moved = c.ID + " merging -> landed"
			p.Units = append(p.Units, u)
		}
		last := &p.Units[len(p.Units)-1]
		b := happened(NBatchLanded, r.Stream, s.Now, landed...)
		b.Who, b.What = r.Who, "ci green"
		last.Notes = append(last.Notes, b)
		if ctlSet["state"] == StreamLanded {
			l := happened(NStreamLanded, r.Stream, s.Now)
			l.Who = r.Who
			last.Notes = append(last.Notes, l)
		}
		lands := map[string]bool{}
		for _, id := range landed {
			lands[id] = true
		}
		// A sprint this merge finishes is found done by the tick's done part
		// (TickDone), which says so and stops the machine.
		p.Units = append(p.Units, resolveAfter(s, lands, r.Who)...)
	}
	return p
}

// ResumeReq moves a stopped stream again.
type ResumeReq struct {
	Stream  string
	Did     string // what the coordinator did
	Answers []string
	Who     string
}

// Resume moves a stopped stream's stuck cards whose cause is resolved back to
// queued at their unchanged scores and the stream to merging (waiting when
// nothing is queued). It is refused while a cause is unresolved, naming it: a
// stuck card that needs a card of another stream waits until that card has
// landed (ranking it is not landing it). The other causes (a conflict, a red
// branch, a rejected batch) are resolved by the coordinator, who says what
// was done; after a red branch saying it is required. It answers every
// judgment open on the stream.
func Resume(s *Snapshot, r ResumeReq) Plan {
	var p Plan
	ctl := s.StreamCtl(r.Stream)
	if ctl == nil {
		p.refuse(r.Stream, "no such stream")
		return p
	}
	if ctl.F("state") != StreamStopped {
		p.refuse(r.Stream, "not stopped (it is "+orDash(ctl.F("state"))+")")
		return p
	}
	if ctl.F("cause") == "red" && strings.TrimSpace(r.Did) == "" {
		p.refuse(r.Stream, "stopped for a red branch; say what was done: nova-sprint resume --stream "+r.Stream+" --did <text>")
		return p
	}
	stuck := s.Merge.Cell(r.Stream, Stuck)
	for _, c := range stuck {
		if ctl.F("cause") != "cross" {
			break // only a cross stop waits for a need
		}
		if need := c.F("need_card"); need != "" && s.StateOf(need) != Landed {
			p.refuse(r.Stream, fmt.Sprintf("unresolved: %s needs %s (stream %s) landed first, and it is %s", c.ID, need, orDash(c.F("need_stream")), orDash(s.StateOf(need))))
			return p
		}
	}
	// settled as settle settles a stream: merging with cards to merge, landed
	// when every card of it ended with one landed, else waiting
	state := StreamWaiting
	switch {
	case len(stuck)+s.Merge.Count(r.Stream, Queued) > 0:
		state = StreamMerging
	case streamDone(s, r.Stream, 0):
		state = StreamLanded
	}
	set := map[string]string{"state": state, "since": stamp(s.Now)}
	if r.Did != "" {
		set["did"] = r.Did
	}
	u := Unit{Key: ctl.ID, Stream: r.Stream, Changes: []Change{change(Merge, setEntry(ctl, set, append([]string{"cause", "card", "other", FieldConflictKind, FieldConflictPaths}, baseGateCount...)...))},
		Moved: fmt.Sprintf("stream %s stopped -> %s; %d stuck -> queued", r.Stream, state, len(stuck))}
	if state == StreamLanded {
		n := happened(NStreamLanded, r.Stream, s.Now)
		n.Who = r.Who
		u.Notes = append(u.Notes, n)
	}
	for _, o := range s.Open {
		if o.Subject() == StreamSubject(r.Stream) {
			u.Closes = append(u.Closes, o)
		}
	}
	for _, c := range stuck {
		u.Changes = append(u.Changes, change(Merge, moveEntry(c, r.Stream, Queued, nil, "need_card", "need_stream")))
	}
	p.Units = append(p.Units, u)
	answered(&p, s, r.Answers, r.Who)
	return p
}
