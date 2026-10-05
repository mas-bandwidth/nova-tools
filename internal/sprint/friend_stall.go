package sprint

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"
)

// The friend stall ladder (docs/SPEC-SPRINT.md section friend-stall-ladder-r.w1).
// The model is tla/StallLadder.tla (with invariants NoCardHeldPastBound, NoStartedRedealt,
// ReleasedOnlyByActivity, WokenAtEveryWakeRung, NoWakeWithoutRung).
//
// A friend holding dealt cards is stalled when neither session activity (FriendReport.Active)
// nor any card progress stamp (FieldProgress) is newer than friend_stall_after (default 20m).
// While stalled, the ladder climbs one rung per friend_stall_step (default 5m):
//   (1) Wake turn 1: bus message to her pushed into daemon as a turn (Plan.Wakes, sent by
//       the binding once the step commits: cmd/nova-sprint wakeFriendStall).
//   (2) Wake turn 2: second wake bus message.
//   (3) Coordinator note: pushed judgment ("friend <f> stalled <d>: two wakes unanswered").
//   (4) Unstarted cards taken back: FriendTake with All: true (started cards stay and finish).
//   (5) Friend marked down with reason "stalled", released to up by the tick itself at her
//       first session activity after it.
//
// Every rung emits a happened note (Kind: Happened), which is its record: a rung that
// changes no card is no unit of the plan. Any session activity or progress resets her
// to rung 0.

// NFriendStall is the happened notification type for stall ladder climbing and clearing.
const NFriendStall = "friend stall"

// PropFriendStallRung is the fleet property recording the friend's current stall ladder rung (0..5).
// A property name is letters, digits, _ . and - (the store refuses any other: a ':' here once
// refused the whole step, and the ladder never climbed), and a friend's name is too.
func PropFriendStallRung(friend string) string { return "friend_stall_rung." + friend }

// PropFriendStallDown is the fleet property recording when the friend was marked down for stall.
func PropFriendStallDown(friend string) string { return "friend_stall_down." + friend }

// FriendWake is one wake of the stall ladder (rung 1 or 2): the friend, the rung, and how
// long she has been idle. The plan carries it (Plan.Wakes) and the binding sends it after
// the step's commit, so the planner stays free of effects: a part planned to see whether
// it has work (store's tick), or planned again after a lost commit, wakes no one.
type FriendWake struct {
	Friend string
	Rung   int
	Idle   time.Duration
}

// TickFriendStall is the friend stall part of the tick (PartFriendStall): it runs in the
// fleet update pass, checks each friend holding cards against the stall bounds, climbs
// the ladder when stalled, wakes her at rungs 1 and 2 (Plan.Wakes), takes back unstarted
// cards at rung 4, marks her down at rung 5, and releases her to up at her first session
// activity after going down. It writes nothing it reads: the snapshot and the request are
// left as they were given.
func TickFriendStall(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	if s.Fleet == nil || s.Work == nil {
		return p, 0
	}

	propsWritten := map[string]bool{}
	write := func(name, value string) {
		if propsWritten[name] {
			return
		}
		was, had := s.Fleet.Prop(name)
		if (!had && value == "") || (had && was == value) {
			return
		}
		propsWritten[name] = true
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: name, Value: value, Was: was, WasAbsent: !had})
	}

	friendsSet := map[string]bool{}
	for _, f := range s.Friends {
		friendsSet[f.Name] = true
	}
	for _, f := range r.Friends {
		friendsSet[f.Name] = true
	}
	for _, row := range s.Fleet.Rows() {
		if f, ok := FriendOfRow(row); ok {
			friendsSet[f] = true
		}
	}
	friends := slices.Sorted(maps.Keys(friendsSet))

	stallAfter := s.FriendStallAfter()
	stallStep := s.FriendStallStep()

	for _, f := range friends {
		row := FriendRow(f)
		mine := append(append([]*Card(nil), s.Fleet.Cell(row, Ready)...), s.Fleet.Cell(row, Working)...)

		// 1. Session activity signal
		var sessionActive time.Time
		if r.Beats != nil {
			if b, ok := r.Beats[f]; ok && b.Friend != nil {
				sessionActive = b.Friend.Active
			} else if b, ok := r.Beats[row]; ok && b.Friend != nil {
				sessionActive = b.Friend.Active
			}
		}
		if ctl := s.MemberCtl(row); ctl != nil {
			if actStr := ctl.F("active"); actStr != "" {
				if t, err := time.Parse(time.RFC3339, actStr); err == nil && t.After(sessionActive) {
					sessionActive = t
				}
			}
		}
		if s.Fleet.Texts != nil {
			if texts, ok := s.Fleet.Texts[row]; ok {
				if actStr, ok := texts[Active]; ok && actStr != "" {
					if t, err := time.Parse(time.RFC3339, actStr); err == nil && t.After(sessionActive) {
						sessionActive = t
					}
				}
			}
		}

		// Check if she is marked stall down
		downStamp, _ := s.Fleet.Prop(PropFriendStallDown(f))
		isStallDown := downStamp != ""
		var downTime time.Time
		if isStallDown {
			downTime, _ = time.Parse(time.RFC3339, downStamp)
		}

		curRungStr, _ := s.Fleet.Prop(PropFriendStallRung(f))
		curRung, _ := strconv.Atoi(curRungStr)

		// ReleasedOnlyByActivity (tla/StallLadder.tla): release from stall down occurs
		// only when fresh session activity is observed.
		if isStallDown && !sessionActive.IsZero() && (downTime.IsZero() || !sessionActive.Before(downTime)) {
			write(PropFriendStallDown(f), "")
			write(PropFriendStallRung(f), "")
			curRung = 0
			gen := max(s.SeatGeneration, FirstSeatGeneration)
			if p.Health == nil {
				p.Health = &FriendHealthWrite{
					Friend: f,
					Health: FriendHealth{
						State:      Up,
						Seen:       s.Now,
						Generation: gen,
					},
				}
			}
			if ctl := s.MemberCtl(row); ctl != nil {
				p.Units = append(p.Units, Unit{
					Key: ctl.ID,
					Changes: []Change{
						change(Fleet, setEntry(ctl, map[string]string{
							"status": Up,
							"since":  stamp(s.Now),
						})),
					},
					Moved: fmt.Sprintf("friend %s up: released by session activity", f),
				})
			}
			hn := happened(NFriendStall, "", s.Now)
			hn.Who, hn.To = r.who(), s.Coordinator
			hn.What = fmt.Sprintf("friend %s released to up: session activity at %s", f, sessionActive.UTC().Format(time.RFC3339))
			p.Notes = append(p.Notes, hn)
			isStallDown = false
		}

		// A friend holding no cards cannot be stalled
		if len(mine) == 0 {
			if curRung > 0 {
				write(PropFriendStallRung(f), "")
			}
			continue
		}

		// 2. Card progress stamp signal
		var cardProgress time.Time
		for _, c := range mine {
			if pStr := c.F(FieldProgress); pStr != "" {
				if t, err := time.Parse(time.RFC3339, pStr); err == nil && t.After(cardProgress) {
					cardProgress = t
				}
			}
		}

		// 3. Card dealt / taken timestamp
		var cardDealt time.Time
		for _, c := range mine {
			for _, k := range []string{"dealt", "first_dealt", "taken", "first_taken"} {
				if dStr := c.F(k); dStr != "" {
					if t, err := time.Parse(time.RFC3339, dStr); err == nil && t.After(cardDealt) {
						cardDealt = t
					}
				}
			}
		}

		lastSignOfLife := sessionActive
		if cardProgress.After(lastSignOfLife) {
			lastSignOfLife = cardProgress
		}
		if cardDealt.After(lastSignOfLife) {
			lastSignOfLife = cardDealt
		}
		if lastSignOfLife.IsZero() {
			lastSignOfLife = s.Now
		}

		idleDuration := s.Now.Sub(lastSignOfLife)
		if r.Stopped != nil && !lastSignOfLife.IsZero() {
			idleDuration -= r.Stopped(lastSignOfLife, s.Now)
		}
		if idleDuration < 0 {
			idleDuration = 0
		}

		var targetRung int
		switch {
		case idleDuration < stallAfter:
			targetRung = 0
		case idleDuration < stallAfter+stallStep:
			targetRung = 1
		case idleDuration < stallAfter+2*stallStep:
			targetRung = 2
		case idleDuration < stallAfter+3*stallStep:
			targetRung = 3
		case idleDuration < stallAfter+4*stallStep:
			targetRung = 4
		default:
			targetRung = 5
		}

		switch {
		case targetRung == 0:
			if curRung > 0 {
				write(PropFriendStallRung(f), "")
				hn := happened(NFriendStall, "", s.Now)
				hn.Who, hn.To = r.who(), s.Coordinator
				hn.What = fmt.Sprintf("friend %s stall reset to rung 0", f)
				p.Notes = append(p.Notes, hn)
			}
		case targetRung > curRung:
			for nextRung := curRung + 1; nextRung <= targetRung; nextRung++ {
				switch nextRung {
				case 1:
					p.Wakes = append(p.Wakes, FriendWake{Friend: f, Rung: 1, Idle: idleDuration})
					hn := happened(NFriendStall, "", s.Now)
					hn.Who, hn.To = r.who(), s.Coordinator
					hn.What = fmt.Sprintf("friend %s stalled %s: wake turn 1", f, idleDuration.Round(time.Second))
					p.Notes = append(p.Notes, hn)
				case 2:
					p.Wakes = append(p.Wakes, FriendWake{Friend: f, Rung: 2, Idle: idleDuration})
					hn := happened(NFriendStall, "", s.Now)
					hn.Who, hn.To = r.who(), s.Coordinator
					hn.What = fmt.Sprintf("friend %s stalled %s: wake turn 2", f, idleDuration.Round(time.Second))
					p.Notes = append(p.Notes, hn)
				case 3:
					jn := judgment(NStalled, "", s.Now, 0, f)
					jn.Who, jn.To = r.who(), s.Coordinator
					jn.What = fmt.Sprintf("friend %s stalled %s: two wakes unanswered", f, idleDuration.Round(time.Second))
					jn.Decisions = []string{
						"friend take " + f + " --all-unstarted",
						"friend down " + f + " --reason 'stalled'",
					}
					p.Notes = append(p.Notes, jn)
					hn := happened(NFriendStall, "", s.Now)
					hn.Who, hn.To = r.who(), s.Coordinator
					hn.What = fmt.Sprintf("friend %s stalled %s: coordinator note", f, idleDuration.Round(time.Second))
					p.Notes = append(p.Notes, hn)
				case 4:
					started := map[string]string{}
					if r.Beats != nil {
						if b, ok := r.Beats[f]; ok && b.Friend != nil {
							for _, run := range b.Friend.Running {
								started[run] = "her beat names it running"
							}
						} else if b, ok := r.Beats[row]; ok && b.Friend != nil {
							for _, run := range b.Friend.Running {
								started[run] = "her beat names it running"
							}
						}
					}
					for _, c := range mine {
						if c.F(FieldProgress) != "" {
							started[c.ID] = "progress was stamped on it"
						}
					}
					takePlan := FriendTake(s, FriendTakeReq{
						Friend:  f,
						All:     true,
						Reason:  "stalled",
						Who:     r.who(),
						Started: started,
					})
					p.Units = append(p.Units, takePlan.Units...)
					p.Refused = append(p.Refused, takePlan.Refused...)
					hn := happened(NFriendStall, "", s.Now)
					hn.Who, hn.To = r.who(), s.Coordinator
					hn.What = fmt.Sprintf("friend %s stalled %s: unstarted cards taken back", f, idleDuration.Round(time.Second))
					p.Notes = append(p.Notes, hn)
				case 5:
					gen := max(s.SeatGeneration, FirstSeatGeneration)
					if p.Health == nil {
						p.Health = &FriendHealthWrite{
							Friend: f,
							Health: FriendHealth{
								State:      Down,
								Reason:     "stalled",
								Seen:       s.Now,
								Generation: gen,
							},
						}
					}
					write(PropFriendStallDown(f), stamp(s.Now))
					if ctl := s.MemberCtl(row); ctl != nil {
						p.Units = append(p.Units, Unit{
							Key: ctl.ID,
							Changes: []Change{
								change(Fleet, setEntry(ctl, map[string]string{
									"status": Down,
									"since":  stamp(s.Now),
								})),
							},
							Moved: fmt.Sprintf("friend %s down: stalled", f),
						})
					}
					hn := happened(NFriendStall, "", s.Now)
					hn.Who, hn.To = r.who(), s.Coordinator
					hn.What = fmt.Sprintf("friend %s stalled %s: marked down (reason stalled)", f, idleDuration.Round(time.Second))
					p.Notes = append(p.Notes, hn)
				}
			}
			write(PropFriendStallRung(f), strconv.Itoa(targetRung))
		}
	}

	return p, 0
}
