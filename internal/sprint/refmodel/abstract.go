package refmodel

import (
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Observed is what the engine's store holds, as read: the four tables (with
// the unplaced records of every card to be compared), the open judgments and
// the acknowledged conditions, the verb of the operation the fence holds, and
// the machine's state.
type Observed struct {
	Snap    *sprint.Snapshot
	Pending string
	Machine string
}

// Abstract maps what the engine's store holds to the model's state, and
// nothing more: each field of State from the one record that holds it.
//
// The mapping's conventions: a primary's attempt is its work card's attempt,
// 1 before its first card is cut (the model's attempt starts at 1), and the
// next one while it is ready or waiting after its card was finished (rework
// with no member up); its head
// is the attempt of the work card its head names (the differential test
// finishes with no head of its own, so the head is the card); an unplaced
// record is a primary off the table, a work card gone, a read card retired, a
// merge place gone; a stream's cause is read only while it is stopped; the
// tick's no-member judgment is on the subject "fleet"; overdue holds are the
// clock's and are left out.
func Abstract(o Observed) State {
	s := o.Snap
	a := New(s.Readers.Rows(), s.Fleet.Rows(), "")
	a.Epoch = s.Epoch
	a.Pending = o.Pending
	a.Machine = o.Machine
	for _, m := range s.Fleet.Rows() {
		a.Members[m] = Down
		if ctl := s.MemberCtl(m); ctl != nil && ctl.F("status") == sprint.Up {
			a.Members[m] = Up
		}
	}
	a.DealLast, a.AskLast = roundLast(s, sprint.FieldDealSeq, sprint.FieldDealLast), roundLast(s, sprint.FieldAskSeq, sprint.FieldAskLast)
	for _, st := range s.Work.Rows() {
		x := Stream{State: SWaiting}
		if ctl := s.StreamCtl(st); ctl != nil {
			x.State = ctl.F("state")
			if x.State == SStopped {
				x.Cause = ctl.F("cause")
			}
		}
		a.Streams[st] = x
	}
	for _, c := range s.Work.Cards() {
		id := c.ID
		k := c.F("kind")
		if k != KindPrimary && k != KindSentinel {
			continue
		}
		p := Primary{Stream: c.F("stream"), Kind: k, State: Off, Score: c.Score,
			Needs: sorted(sprint.Split(c.F("needs"))), Waived: sorted(sprint.Split(c.F("waived"))),
			Attempt: c.Int("attempt"), Pair: sorted(sprint.Split(c.F("asked"))), Reached: c.F("reached") != ""}
		if c.Placed() {
			p.State = c.Col
		}
		if p.Attempt < 1 {
			p.Attempt = 1
		}
		if h := c.F("head"); h != "" {
			if _, n, ok := sprint.ParseWorkCard(h); ok {
				p.Head = n
			} else {
				p.Head = -1
			}
		}
		a.Primaries[id] = p
	}
	// A primary ready or waiting whose card at its attempt field is done
	// has that attempt behind it (a rework with no member up): its next
	// card is the next attempt (the engine's attempt field names the last
	// card cut).
	for id, p := range a.Primaries {
		if p.State != Ready && p.State != Waiting {
			continue
		}
		c := s.Fleet.Card(WC(id, p.Attempt))
		if c != nil && c.Placed() && (c.Col == sprint.DoneOK || c.Col == sprint.DoneFailed) {
			p.Attempt++
			a.Primaries[id] = p
		}
	}
	for _, c := range s.Fleet.Cards() {
		id := c.ID
		if c.F("kind") != "work" {
			continue
		}
		w := WorkCard{Primary: c.F("primary"), Attempt: c.Int("attempt"), Member: c.F("member"), Place: Gone, Gen: c.Int("gen")}
		if c.Placed() {
			w.Place, w.Member = c.Col, c.Row
			if c.Col == sprint.DoneOK || c.Col == sprint.DoneFailed {
				w.Place = sprint.Done // the member's ok and failed cells are the done column's parts
			}
		}
		switch c.F("ok") {
		case "":
		case "yes":
			w.OK = "ok"
		case "no":
			w.OK = "failed"
		default:
			w.OK = c.F("ok")
		}
		a.Work[id] = w
	}
	for _, c := range s.Readers.Cards() {
		id := c.ID
		if c.F("kind") != "read" {
			continue
		}
		r := ReadCard{Primary: c.F("primary"), Attempt: c.Int("attempt"), Reader: c.F("reader"), Place: Retired, Verdict: c.F("verdict")}
		if c.Placed() {
			r.Place = c.Col
		}
		a.Reads[id] = r
	}
	for _, c := range s.Merge.Cards() {
		id := c.ID
		if c.F("kind") != "merge" {
			continue
		}
		m := MergeCard{Place: Gone, Need: c.F("need_card")}
		if c.Placed() {
			m.Place = c.Col
		}
		a.Merge[id] = m
	}
	for _, o := range s.Open {
		if j, ok := judgment(o); ok {
			a.Open[j] = true
		}
	}
	for _, o := range s.Acked {
		if j, ok := judgment(o); ok {
			a.Acked[j] = true
		}
	}
	return a
}

// judgment is one open key as the model's judgment: its type, and its
// subject.
func judgment(o sprint.Open) (Judgment, bool) {
	t := strings.TrimSuffix(o.Note.Type, sprint.NRepeatSuffix)
	if t == sprint.NOverdue {
		return Judgment{}, false
	}
	j := Judgment{Type: JudgmentType(t), Subject: o.Subject()}
	if j.Type == JNoMember {
		j.Subject = "fleet"
	}
	return j, true
}

var types = map[string]string{
	sprint.NWorkFailed:      JFailed,
	sprint.NReadBroken:      JBroken,
	sprint.NReadsExhausted:  JReads,
	sprint.NStranded:        JStranded,
	sprint.NReadyToAccept:   JAccept,
	sprint.NReturned:        JReturned,
	sprint.NBlocked:         JBlocked,
	sprint.NCIRed:           JCI,
	sprint.NRepairSkipped:   JSkipped,
	sprint.NConflict:        JConflict,
	sprint.NRed:             JRed,
	sprint.NCross:           JCross,
	sprint.NRejected:        JRejected,
	sprint.NSprintDone:      JDone,
	sprint.NSentinelReached: JReached,
	sprint.NNoMember:        JNoMember,
	sprint.NCannotAsk:       JCannotAsk,
	sprint.NBound:           JBound,
}

// JudgmentType is the model's name of an engine judgment type; a type the
// model has no name for is "other:" and the engine's words.
func JudgmentType(t string) string {
	if m, ok := types[t]; ok {
		return m
	}
	return "other:" + t
}

// roundLast is where a rolling index stands (errata 3, amendment 5): the last
// name on the streams' control card with the highest sequence, the first
// stream in name order on a tie; "" when none has one.
func roundLast(s *sprint.Snapshot, seqField, lastField string) string {
	last, seq := "", -1
	for _, st := range sorted(s.Work.Rows()) {
		ctl := s.StreamCtl(st)
		if ctl == nil || ctl.F(seqField) == "" {
			continue
		}
		if n := ctl.Int(seqField); n > seq {
			last, seq = ctl.F(lastField), n
		}
	}
	return last
}

func sorted(xs []string) []string {
	if len(xs) == 0 {
		return nil
	}
	out := append([]string(nil), xs...)
	sort.Strings(out)
	return out
}
