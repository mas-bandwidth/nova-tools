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

// SeatChange is one change of the seat, as the seat's record keeps it.
type SeatChange struct {
	Holder     string    `json:"holder"`
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
	c := &SeatChange{Holder: r.To, From: s.Coordinator, At: s.Now, By: r.Who, Taken: r.Take, ApprovedBy: r.ApprovedBy, Reason: strings.TrimSpace(r.Reason)}
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

// FriendSpec is what friend sync knows of one friend: her name (a friend row
// of nova-config), her width, and her tiers.
type FriendSpec struct {
	Name  string   `json:"name"`
	Width int      `json:"width"`
	Tiers []string `json:"tiers,omitempty"`
}

// FriendHoldChange is the coordinator holding or releasing a friend.
type FriendHoldChange struct {
	Name string    `json:"name"`
	Held bool      `json:"held"`
	At   time.Time `json:"at"`
	By   string    `json:"by"`
}

// FriendSpecSync is the full set of specs synchronized into the roster.
type FriendSpecSync struct {
	Specs []FriendSpec `json:"specs"`
}

// FriendRosterChange is one durable mutation of the friends roster, committed
// atomically with table changes in Store.Run.
type FriendRosterChange struct {
	Hold *FriendHoldChange `json:"hold,omitempty"`
	Sync *FriendSpecSync   `json:"sync,omitempty"`
}
