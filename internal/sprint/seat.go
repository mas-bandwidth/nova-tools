package sprint

import (
	"strings"
	"time"
)

// The coordinator's seat (docs/SPEC-SPRINT.md, "Handing over the seat"; the owner,
// 2026-10-02: "We need to make handover MORE SMOOTH."; "it needs to be able to
// be done, even if the old coordinator is asleep or out of credits"; "you can
// be given coordinator status, or you can take it (with my permission only)").
// The seat is given by its holder or by the sprint's owner, or taken by the one
// taking it with the owner's name; each is a happened note and its log line,
// written with the change of the coordinator in one commit.

// SeatChange is one change of the seat, as the seat's record keeps it. Generation
// is the seat's generation after the change (FirstSeatGeneration is the seat
// no change has moved; every accepted change takes the next): the fence of
// the coordinator's observations of the friends (ObserveFriend).
type SeatChange struct {
	Holder     string    `json:"holder"`
	Generation uint64    `json:"generation"`
	From       string    `json:"from"`
	At         time.Time `json:"at"`
	By         string    `json:"by"`
	Taken      bool      `json:"taken,omitempty"`
	ApprovedBy string    `json:"approved_by,omitempty"`
	Reason     string    `json:"reason"`
}

// SeatReq asks for the seat to move to To. Owner is the sprint's owner ("" when
// it names none); Take with ApprovedBy is a take, else the seat is given.
type SeatReq struct {
	To, Who, Reason   string
	Take              bool
	ApprovedBy, Owner string
}

// NotSeat is why who may not move the seat to the request's name, "" is may:
// the holder or the owner gives it; the one taking it takes it, with the
// owner's name. It reads nothing but its arguments, so the command refuses
// before it writes, and the step refuses again on what it read.
func NotSeat(holder string, r SeatReq) string {
	switch {
	case !ValidID(r.To):
		return "a name wants letters, digits, _ and -: " + orDash(r.To)
	case strings.TrimSpace(r.Reason) == "":
		return "a seat change wants --reason <text>: it is recorded in the log"
	case holder == "":
		return "the sprint has no coordinator; run: nova-sprint init --coordinator <name>"
	case r.To == holder:
		return r.To + " holds the seat already; nothing was changed"
	case !r.Take && r.ApprovedBy != "":
		return "--approved-by goes with --take; the holder or the owner gives the seat without it"
	case !r.Take && r.Who != holder && (r.Owner == "" || r.Who != r.Owner):
		why := "the seat is the holder's to give: " + holder + ", not " + orDash(r.Who)
		if r.Owner != "" {
			why += " (or the owner's: " + r.Owner + ")"
		}
		return why + "; to take it: nova-sprint coordinator " + r.To + " --take --approved-by <owner> --reason <text>; nothing was changed"
	case r.Take && r.ApprovedBy == "":
		return "--take wants --approved-by <owner>: a seat is taken only with the owner's name in the record; nothing was changed"
	case r.Take && r.Owner == "":
		return "the sprint names no owner, whose name a take carries: init --owner <name>, or NOVA_SPRINT_OWNER; nothing was changed"
	case r.Take && r.ApprovedBy != r.Owner:
		return "the sprint's owner is " + r.Owner + ", not " + r.ApprovedBy + "; nothing was changed"
	case r.Take && r.Who != r.To:
		return "a seat is taken by the one taking it: " + r.To + ", not " + orDash(r.Who) + "; nothing was changed"
	}
	return ""
}

// MoveSeat is the seat moved to r.To: a happened note, which is the log's line
// of it, and the change the commit writes. A take's note is addressed to the
// holder it was taken from, so they see it when they wake; a given seat's to
// the new holder.
func MoveSeat(s *Snapshot, r SeatReq) Plan {
	var p Plan
	if why := NotSeat(s.Coordinator, r); why != "" {
		p.refuse(r.To, why)
		return p
	}
	c := &SeatChange{Holder: r.To, Generation: max(s.SeatGeneration, FirstSeatGeneration) + 1, From: s.Coordinator, At: s.Now, By: r.Who, Taken: r.Take, ApprovedBy: r.ApprovedBy, Reason: strings.TrimSpace(r.Reason)}
	n := Note{Kind: Happened, Type: NSeat, At: s.Now, Who: r.Who, To: c.Holder,
		What: c.From + " -> " + c.Holder + ": " + c.Reason + ", by " + c.By}
	if c.Taken {
		n.Type, n.To = NSeatTaken, c.From
		n.What = c.From + " -> " + c.Holder + ", approved by " + c.ApprovedBy + ": " + c.Reason
		n.Hint = "the seat is " + c.Holder + "'s now; your coordinator verbs are refused"
	}
	p.Notes = []Note{n}
	p.Seat = c
	return p
}

// The seat key follows the seat record (docs/SPEC-SPRINT.md, "Handing over the
// seat", seat-key-follows-record.w2): a server whose unit names another actor
// than the seat's holder once left the key naming that actor after each
// restart, and every coordinator verb of the holder was refused until the key
// was set by hand.
// Only init and the seat's own steps write the key, and from the record: the
// run loop never does, whatever actor it runs as. seat shows the key, the
// record's holder and the server's actor, and a drift between them; seat
// --repair writes the key from the record.

// SeatDrift is how the key, the record's holder and the server's actor
// disagree, "" when they do not: the key names someone the record does not, or
// the server runs as a named actor who is not the holder. record is "" when the
// seat has no record (init's key is the record then); server is "" when no
// server is recorded, and MachineActor is the server acting as no one.
func SeatDrift(key, record, server string) string {
	holder := record
	if holder == "" {
		holder = key
	}
	var why []string
	if record != "" && key != record {
		why = append(why, "the key says "+orDash(key)+" and the record "+record+": nova-sprint seat --repair --reason <text> (the holder or the owner)")
	}
	if server != "" && server != MachineActor && server != holder {
		why = append(why, "the server runs as "+server+" and the seat is "+orDash(holder)+"'s: change the server's NOVA_SPRINT_ACTOR="+server+" to NOVA_SPRINT_ACTOR="+orDash(holder)+" and restart it")
	}
	return strings.Join(why, "; ")
}

// SeatRepairReq asks for the coordinator key to be written from the seat's
// record. Owner is the sprint's owner ("" when it names none).
type SeatRepairReq struct {
	Who, Reason, Owner string
}

// NotSeatRepair is why who may not repair the key, "" is may: the record's
// holder or the owner, with a reason, while the key says someone else. ok is
// whether the seat has a record (rec its last change).
func NotSeatRepair(key string, rec SeatChange, ok bool, r SeatRepairReq) string {
	switch {
	case strings.TrimSpace(r.Reason) == "":
		return "a repair of the seat's key wants --reason <text>: it is recorded in the log"
	case !ok:
		return "the seat has not moved since init, and init's key is its record: there is nothing to repair it from; nothing was changed"
	case key == rec.Holder:
		return "the key says " + key + " as the record does; nothing was changed"
	case r.Who != rec.Holder && (r.Owner == "" || r.Who != r.Owner):
		why := "the seat's key is repaired by the record's holder: " + rec.Holder + ", not " + orDash(r.Who)
		if r.Owner != "" {
			why += " (or the owner: " + r.Owner + ")"
		}
		return why + "; nothing was changed"
	}
	return ""
}

// RepairSeat is the key written from the record rec, read before the step: the
// commit writes the record back unchanged with its holder as the key, and a
// happened note, the log's line of it, says who and why. A seat that moved since
// rec was read (its generation is not rec's) is refused: the step reads again.
func RepairSeat(s *Snapshot, rec SeatChange, r SeatRepairReq) Plan {
	var p Plan
	if s.SeatGeneration != max(rec.Generation, FirstSeatGeneration) {
		p.refuse(rec.Holder, "the seat moved since its record was read; run seat again; nothing was changed")
		return p
	}
	if why := NotSeatRepair(s.Coordinator, rec, true, r); why != "" {
		p.refuse(rec.Holder, why)
		return p
	}
	c := rec
	p.Notes = []Note{{Kind: Happened, Type: NSeat, At: s.Now, Who: r.Who, To: rec.Holder,
		What: "repaired: the key said " + orDash(s.Coordinator) + ", the record " + rec.Holder + ": " + strings.TrimSpace(r.Reason) + ", by " + r.Who}}
	p.Seat = &c
	return p
}
