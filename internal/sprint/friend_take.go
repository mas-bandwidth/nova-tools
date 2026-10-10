package sprint

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/typedrec"
)

// A friend's card taken back (docs/SPEC-SPRINT.md section 1, a friend's card taken back;
// the owner, 2026-10-04, on cards dealt to a friend who would not start them, which could
// only be dropped and added again: "sounds bad, we should fix this"). The coordinator takes
// back a card dealt to a friend that she has not started (friend take), and the hold of a
// friend (friend down) takes back every one of hers she has not started, as a held machine's
// cards are withdrawn: the work card is withdrawn on her row (never a failure: no redeal of
// its bound is spent, FieldTakeEnded is not set), its primary goes back to ready, and the
// friends' deal (friendDealPass) places the same card again at its next generation, on its own
// branch and job. A card she has started stays with her and finishes under the coordinator's
// take: one she pushed to, one her beat names running (the caller reads both: Started), and
// one she finished (in review or later, so no longer ready or working on her row). With cards
// named, each one she may give up is taken and each one she keeps is refused, one line each
// (section 1, the card friend-take-partial.w1); AllOrNothing takes none when any is refused,
// as the take did before. A hold keeps no card (the owner, 2026-10-04, on a held friend still
// showing two working cards: "nonono"): it takes her started cards too, and a started card
// with a push carries its pushed head and the generation whose branch holds it
// (FieldCarryHead, FieldCarryGen), so the next taker starts from that work and none is lost.

const (
	// FieldTakenBack is why a friend's work card was taken back, on the withdrawn card until
	// the deal places it again: "taken back by the coordinator: <reason>", or the hold's words.
	FieldTakenBack = "taken_back"
	// FieldTakenFrom is the friend's row the coordinator took the card back from (friend
	// take, not the hold): the deal that places it again never gives it back to her.
	FieldTakenFrom = "taken_from"
	// NTakenBack is the happened note of a friend's card taken back to ready.
	NTakenBack = "a friend's card taken back to ready"
	// FieldCarryHead is the head a started card had pushed when a hold took it back, and
	// FieldCarryGen the generation whose branch holds it (BranchOf at that generation): the
	// next generation starts from it. Both stay on the card through its next deal.
	FieldCarryHead = "carry_head"
	FieldCarryGen  = "carry_gen"
)

// pushPrefix opens the words friend take and friend down give a started card with a push
// (cmd/nova-sprint friendStarted): "a push on its branch <branch> at <tip>".
const pushPrefix = "a push on its branch "

// PushedTip is the tip a started card's words name ("a push on its branch <branch> at
// <tip>"), "" when they name no push at a full sha (her beat names it running, or the tip
// could not be read): only a push read at its sha is carried.
func PushedTip(why string) string {
	if !strings.HasPrefix(why, pushPrefix) {
		return ""
	}
	_, tip, ok := strings.Cut(strings.TrimPrefix(why, pushPrefix), " at ")
	if !ok || !typedrec.IsFullSha(tip) {
		return ""
	}
	return tip
}

// FriendTakeReq is friend take (IDs or All) or the hold's withdrawal (Hold, All): the
// friend, the cards named (each a primary or its work card), every card of hers she has
// not started (All), why, and Started, the work cards she has started as the caller read
// them (a push on the card's branch, her beat naming it running), each with its why.
// AllOrNothing takes none of the cards named when any one is refused. Begun, with Hold and
// All, takes only the cards she has begun (working, or read as started), leaving her ready
// cards not begun on her row; a friend's hold no longer asks for it, since a held friend
// keeps no card at all (the owner, 2026-10-09).
type FriendTakeReq struct {
	Friend       string
	IDs          []string
	All          bool
	Hold         bool
	Begun        bool
	AllOrNothing bool
	Reason       string
	Started      map[string]string
	Who          string
	// Spends is a take the seat or her runner asked for (friend take): a read card it takes
	// spends its reader (retired_by returned, readSpent), as read --return does; the
	// machine's take-backs (a hold, a stall) spend nothing.
	Spends bool
}

// takenBackWhy is the words a taken card carries (FieldTakenBack).
func (r FriendTakeReq) takenBackWhy() string {
	if r.Hold {
		return "taken back by the hold of friend " + r.Friend
	}
	if r.Reason == "" {
		return "taken back by the coordinator"
	}
	return "taken back by the coordinator: " + r.Reason
}

// FriendTake takes back the friend's cards she has not started: each one withdrawn on her
// row, its primary ready for the friends' deal. A card named that is not dealt to her, or
// that she has started, is refused, one refusal each, and the rest are taken; with
// AllOrNothing one refusal takes none, each card that would have been taken refused too;
// with All a started card stays (the caller, which read Started, says so). A working card taken frees her lane: her oldest ready card not taken
// is taken into working in the same unit, as her finish takes it (friendNext).
func FriendTake(s *Snapshot, r FriendTakeReq) Plan {
	var p Plan
	row := FriendRow(r.Friend)
	mine := append(append([]*Card(nil), s.Fleet.Cell(row, Ready)...), s.Fleet.Cell(row, Working)...)
	SortCards(mine)
	var take []*Card
	if r.All {
		for _, c := range mine {
			// a hold takes started cards too: a card sitting on a held friend blocks every card
			// that needs it (the owner, 2026-10-04: "Held friends cards need to be
			// redistributed automatically"); the coordinator's own take keeps a started one
			switch {
			case r.Hold && r.Begun:
				if c.Col == Working || r.Started[c.ID] != "" {
					take = append(take, c)
				}
			case r.Started[c.ID] == "" || r.Hold:
				take = append(take, c)
			}
		}
	}
	for _, id := range r.IDs {
		c := s.Fleet.Placed(id)
		if c == nil || c.F("kind") != "work" {
			if pr := s.Work.Placed(id); pr != nil {
				c = s.Fleet.Placed(WorkCardID(pr.ID, pr.Int("attempt")))
			}
		}
		switch {
		case c == nil:
			p.refuse(id, "no card "+id+" is dealt to friend "+r.Friend)
		case c.Row != row:
			p.refuse(id, fmt.Sprintf("%s is not dealt to friend %s: it is at %s", c.ID, r.Friend, placeOf(c)))
		case c.Col != Ready && c.Col != Working:
			p.refuse(id, fmt.Sprintf("%s has started: friend %s finished it (%s), and its primary %s is %s", c.ID, r.Friend, c.Col, c.F("primary"), orDash(s.StateOf(c.F("primary")))))
		case r.Started[c.ID] != "":
			p.refuse(id, fmt.Sprintf("%s has started: %s; it stays with friend %s and finishes", c.ID, r.Started[c.ID], r.Friend))
		case !slices.Contains(take, c):
			take = append(take, c)
		}
	}
	if r.AllOrNothing && len(p.Refused) > 0 {
		n := len(p.Refused)
		for _, c := range take {
			p.refuse(c.ID, fmt.Sprintf("not taken: --all-or-nothing, and %d of the cards named %s refused", n, map[bool]string{true: "was", false: "were"}[n == 1]))
		}
		return p
	}
	// her ready cards not taken, oldest first: each working card taken frees a lane one fills
	var next []*Card
	for _, c := range mine {
		// a held friend takes nothing into a lane her hold frees
		if c.Col == Ready && !slices.Contains(take, c) && !(r.Hold && r.Begun) {
			next = append(next, c)
		}
	}
	why := r.takenBackWhy()
	for _, c := range take {
		// the take, not the hold, keeps her from it; a card never taken from her has its whole
		// dealt bound again from now (untaken_since), its first take unset
		set, unset := map[string]string{FieldTakenBack: why, "untaken_since": stamp(s.Now)}, []string{"first_taken"}
		if r.Spends && isRead(c) {
			set["retired_by"] = RetiredByReturned // handed back: it spends her (readSpent)
		}
		if r.Hold {
			unset = append(unset, FieldTakenFrom)
		} else {
			set[FieldTakenFrom] = row
		}
		what := why + " from friend " + r.Friend
		if started := r.Started[c.ID]; started != "" {
			// only a hold takes a started card: its pushed head is carried to the next taker
			what += "; started (" + started + ")"
			if tip := PushedTip(started); tip != "" {
				set[FieldCarryHead], set[FieldCarryGen] = tip, strconv.Itoa(c.Int("gen"))
				what += fmt.Sprintf(": the next generation starts from its pushed head %s (gen %d)", tip, c.Int("gen"))
			} else {
				what += ": no push to carry"
			}
		}
		u := withdrawUnit(s, c, set, unset, NTakenBack, r.Who, what)
		if c.Col == Working && len(next) > 0 {
			n := next[0]
			next = next[1:]
			set, unset := friendTaken(s, n, r.Friend)
			u.Changes = append(u.Changes, change(Fleet, moveEntry(n, row, Working, set, unset...)))
			u.Moved += fmt.Sprintf("; %s ready -> working (her next, taken now)", n.ID)
		}
		p.Units = append(p.Units, u)
	}
	return p
}

// FriendReadyMax is how long a work card may sit ready on a friend's row while she has a
// lane free before it is a judgment (a work card past its deadline, dealt and never
// taken): the deal takes it into the lane (in batch and one-shot mode alike), or her
// daemon or her session takes it (take --as friend.<name>), and a card none took is no one's
// (tla/FriendReadyTake.tla, NeverStrandedSilently).
const FriendReadyMax = 10 * time.Minute

// friendLaneIdle says the card is ready on a friend's row while she has a lane free (her
// working cards fewer than her lanes, friendRoom; seats is the tick's read of the friends),
// and names her. A card ready behind her lanes, all of them working, is her queue, held to
// the dealt bound as a member's is.
func friendLaneIdle(s *Snapshot, seats []FriendSeat, c *Card) (friend string, idle bool) {
	friend, ok := FriendOfRow(c.Row)
	if !ok || c.Col != Ready {
		return friend, false
	}
	for _, f := range seats {
		if f.Name == friend {
			_, lanes := friendRoom(f)
			return friend, s.Fleet.Count(c.Row, Working) < lanes
		}
	}
	return friend, false
}

// NGivenBack is the happened note of a card taken back from a friend given back to her.
const NGivenBack = "a card taken back from a friend given back"

// FriendGiveReq is friend give: the friend, the cards named (each a primary or its work
// card), and why.
type FriendGiveReq struct {
	Friend string
	IDs    []string
	Reason string
	Who    string
}

// FriendGive is the coordinator's undo of a take-back (friend take): on each card named,
// ready or waiting, its current attempt's work card loses the mark of the friend it was
// taken from (FieldTakenFrom), so the deal may deal it to her again; a pinned card taken
// from its friend waits for no one else and is dealt to her. A card not ready or waiting,
// or never taken back from her, is refused, one refusal each, and the rest are given.
func FriendGive(s *Snapshot, r FriendGiveReq) Plan {
	var p Plan
	row := FriendRow(r.Friend)
	reason := r.Reason
	if reason == "" {
		reason = "given back by the coordinator"
	}
	seen := map[string]bool{}
	for _, id := range r.IDs {
		pr := s.Work.Placed(id)
		if c := s.Fleet.Placed(id); pr == nil && c != nil && c.F("kind") == "work" {
			pr = s.Work.Placed(c.F("primary"))
		}
		if pr == nil {
			p.refuse(id, "no card "+id)
			continue
		}
		if seen[pr.ID] {
			continue
		}
		seen[pr.ID] = true
		card := WorkCardID(pr.ID, pr.Int("attempt"))
		wc := s.Fleet.Placed(card)
		from := "no friend"
		if wc != nil && wc.F(FieldTakenFrom) != "" {
			from = "friend " + strings.TrimPrefix(wc.F(FieldTakenFrom), friendRowPrefix)
		}
		switch {
		case pr.Col != Ready && pr.Col != Waiting:
			p.refuse(id, fmt.Sprintf("%s is %s, not ready or waiting", pr.ID, pr.Col))
		case wc == nil || wc.F(FieldTakenFrom) != row:
			p.refuse(id, fmt.Sprintf("%s was never taken back from friend %s (taken from %s)", card, r.Friend, from))
		default:
			n := happened(NGivenBack, pr.Row, s.Now, pr.ID)
			n.Who, n.What = r.Who, fmt.Sprintf("%s may be dealt to %s again (%s)", pr.ID, r.Friend, reason)
			p.Units = append(p.Units, Unit{Key: pr.ID, Stream: pr.Row,
				Changes: []Change{change(Fleet, setEntry(wc, nil, FieldTakenFrom))}, Notes: []Note{n}, Moved: n.What})
		}
	}
	return p
}
