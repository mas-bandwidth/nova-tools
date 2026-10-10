package sprint

import (
	"fmt"
	"log"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"
)

// The friend stall ladder (docs/SPEC-SPRINT.md section friend-stall-ladder-r.w1).
// The model is tla/StallLadder.tla (with invariants NoCardHeldPastBound, NoStartedRedealt,
// ReleasedOnlyByActivity).
//
// A friend holding dealt cards is stalled when no activity of hers and no card progress stamp
// (FieldProgress) is newer than friend_stall_after (default 20m). Her activity is any of: her
// session activity (FriendReport.Active); a beat whose running list is not empty (friend beat
// --running: a one-shot lane, or cards worked in child agents, move no session), at the beat's
// time; her session's proof (a check answered, or a bus message of hers) and its answer to the
// coordinator's wake ping; and a finish or a report of hers: the newest of them (FriendWorked).
// While stalled, the ladder climbs one rung per friend_stall_step (default 5m):
//   (1) Wake turn 1: bus message to her pushed into daemon as a turn.
//   (2) Wake turn 2: second wake bus message.
//   (3) Coordinator note: pushed judgment ("friend <f> stalled <d>: two wakes unanswered").
//   (4) Unstarted cards taken back: FriendTake with All: true (started cards stay and finish).
//   (5) Friend marked down with reason "stalled", released to up by the tick itself at her
//       first activity after it. The release clears the stall props and removes the
//       coordinator's observation of her (Plan.HealthClear), writing none: an observation
//       written by the tick would stand for FriendPongWindow and read as evidence her
//       session never gave (ObservedStatus); with it
//       removed her status is her session's evidence alone (FriendStatus).
//
// Every rung emits a happened note (Kind: Happened), which says what the rung did: the
// part's units are only the cards and rows it changes. Any activity or progress resets her
// to rung 0.

// NFriendStall is the happened notification type for stall ladder climbing and clearing.
const NFriendStall = "friend stall"

// PropFriendStallRung is the fleet property recording the friend's current stall ladder rung
// (0..5). A property name is letters, digits, _ . and - (the table store refuses any other),
// so the friend's name follows a dot.
func PropFriendStallRung(friend string) string { return "friend_stall_rung." + friend }

// PropFriendStallDown is the fleet property recording when the friend was marked down for stall.
func PropFriendStallDown(friend string) string { return "friend_stall_down." + friend }

// TickFriendStall is the friend stall part of the tick (PartFriendStall): it runs in the
// fleet update pass, checks each friend holding cards against the stall bounds, climbs
// the ladder when stalled, takes back unstarted cards at rung 4, marks her down at rung 5,
// and releases her to up at her first activity after going down.
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

		// 1. Activity signal: the newest evidence of hers (FriendWorked): her session's
		// writes, her beat naming running cards, her session's proof (a check answered or a
		// bus message of hers), and her finishes and reports, never one field alone
		activity, _ := FriendWorked(s, f, friendWorkOf(r, f))
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
		// only when fresh activity of hers is observed (never card progress alone). The
		// release writes no observation: it removes the one rung 5 wrote, so her status
		// is her session's evidence alone (FriendStatus).
		if isStallDown && !activity.IsZero() && (downTime.IsZero() || !activity.Before(downTime)) {
			write(PropFriendStallDown(f), "")
			write(PropFriendStallRung(f), "")
			curRung = 0
			p.HealthClear = append(p.HealthClear, f)
			if ctl := s.MemberCtl(row); ctl != nil {
				p.Units = append(p.Units, Unit{
					Key: ctl.ID,
					Changes: []Change{
						change(Fleet, setEntry(ctl, map[string]string{
							"status": Up,
							"since":  stamp(s.Now),
						})),
					},
					Moved: fmt.Sprintf("friend %s up: released by her activity", f),
				})
			}
			hn := happened(NFriendStall, "", s.Now)
			hn.Who, hn.To = r.who(), s.Coordinator
			hn.What = fmt.Sprintf("friend %s released to up: activity at %s", f, activity.UTC().Format(time.RFC3339))
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

		lastSignOfLife := activity
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
					if r.WakeFriend != nil {
						_ = r.WakeFriend(f, 1, idleDuration) // ignored: the wake message is best effort; the rung climbs regardless
					}
					hn := happened(NFriendStall, "", s.Now)
					hn.Who, hn.To = r.who(), s.Coordinator
					hn.What = fmt.Sprintf("friend %s stalled %s: wake turn 1", f, idleDuration.Round(time.Second))
					p.Notes = append(p.Notes, hn)
				case 2:
					if r.WakeFriend != nil {
						_ = r.WakeFriend(f, 2, idleDuration) // ignored: the wake message is best effort; the rung climbs regardless
					}
					hn := happened(NFriendStall, "", s.Now)
					hn.Who, hn.To = r.who(), s.Coordinator
					hn.What = fmt.Sprintf("friend %s stalled %s: wake turn 2", f, idleDuration.Round(time.Second))
					p.Notes = append(p.Notes, hn)
				case 3:
					// its own type, kept by the coordinator's pass while her rung stands
					// (stops.go): as NStalled the check closed it in the tick that raised it
					jn := judgment(NFriendStalled, "", s.Now, 0, f)
					jn.Who, jn.To = r.who(), s.Coordinator
					jn.What = fmt.Sprintf("friend %s stalled %s: two wakes unanswered", f, idleDuration.Round(time.Second))
					jn.Decisions = []string{
						"friend take " + f + " --all-unstarted",
						"friend down " + f + " --reason 'stalled'",
						"ack", "wait",
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
						switch {
						case c.F(FieldProgress) != "":
							started[c.ID] = "progress was stamped on it"
						case c.F(FieldReported) != "":
							started[c.ID] = "a report of hers was written on it"
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

// FriendWork is the evidence of a friend's work the tables do not hold: what her daemon's
// beat carries and the store's record of her last finish. Each is zero when there is none.
type FriendWork struct {
	// Active is the newest write under her working directory and outbox as her daemon's
	// last walk found it (FriendReport.Active). The walk is bounded in files and time, so
	// it can answer with an old file while her outbox holds new ones: never the only field.
	Active time.Time
	// Running is her beat's time while it names cards running (friend beat --running).
	Running time.Time
	// Proof is her session's last proof: a SESSION CHECK it answered, or a bus message of
	// its own (Beat.Proof, FriendSession.Pong).
	Proof time.Time
	// Finished is when a card of hers last finished, working to done (the store's
	// friend-finish record, FriendSeat.Finished), whatever row the card is on now.
	Finished time.Time
	// Answered is when her session last answered the coordinator's wake ping (her
	// FriendHealth observation, up), FriendSeat.Answered.
	Answered time.Time
}

// friendWorkOf is what the tick was given of the friend's work beside the tables: her
// beat (by her name, else by her row), her session's proof, and her seat's.
func friendWorkOf(r TickReq, f string) FriendWork {
	var w FriendWork
	later := func(at *time.Time, t time.Time) {
		if t.After(*at) {
			*at = t
		}
	}
	b, ok := r.Beats[f]
	if !ok || b.Friend == nil {
		if br, okr := r.Beats[FriendRow(f)]; okr {
			b, ok = br, okr
		}
	}
	if ok {
		if b.Friend != nil {
			later(&w.Active, b.Friend.Active)
			if len(b.Friend.Running) > 0 {
				later(&w.Running, b.At)
			}
		}
		later(&w.Proof, b.Proof)
	}
	later(&w.Proof, r.Sessions[f].Pong)
	for _, seat := range r.Friends {
		if seat.Name == f {
			later(&w.Active, seat.Active)
			later(&w.Proof, seat.Proof)
			later(&w.Finished, seat.Finished)
			later(&w.Answered, seat.Answered)
		}
	}
	return w
}

// FriendWorked is the newest evidence that friend f is at work, and what it is (docs/SPEC-SPRINT.md
// section friend-stall-ladder-r.w1): her session's writes, her beat naming running cards,
// her session's proof (a check answered, or a bus message of hers), her session's answer to
// the coordinator's wake ping, a finish of hers (the
// store's record, or a card done on her row), a report of hers (FieldReported on a card of
// her row), and the coordinator's view of her row ("active"). Card progress and takes are
// the cards', not hers (FriendCardMoved): they hold the stall ladder and never release her.
// Zero and "" when there is none. On 2026-10-06 one field, her daemon's walk of her
// working directory, read three days old while her outbox had reports that hour and her bus
// notes came every few minutes, and the ladder took two working cards back from her.
func FriendWorked(s *Snapshot, f string, w FriendWork) (time.Time, string) {
	var at time.Time
	what := ""
	see := func(t time.Time, word string) {
		// A stamp after the server's clock is that clock. Zero now is not a
		// clock, and clamping to it would drop the evidence (see uses After).
		if s != nil && !s.Now.IsZero() && t.After(s.Now) {
			noteFutureEvidence(f, t, s.Now, word)
			t = s.Now
		}
		if t.After(at) {
			at, what = t, word
		}
	}
	see(w.Active, "session write")
	see(w.Running, "beat naming running cards")
	see(w.Proof, "session proof")
	see(w.Finished, "finish")
	see(w.Answered, "session answer")
	if s == nil || s.Fleet == nil {
		return at, what
	}
	row := FriendRow(f)
	for _, c := range append(append([]*Card(nil), s.Fleet.Cell(row, DoneOK)...), s.Fleet.Cell(row, DoneFailed)...) {
		see(stampAt(c, "finished"), "finish")
	}
	for _, col := range []string{Ready, Working, DoneOK, DoneFailed} {
		for _, c := range s.Fleet.Cell(row, col) {
			see(stampAt(c, FieldReported), "report")
		}
	}
	if ctl := s.MemberCtl(row); ctl != nil {
		see(stampAt(ctl, "active"), "session write")
	}
	if texts, ok := s.Fleet.Texts[row]; ok {
		if t, err := time.Parse(time.RFC3339, texts[Active]); err == nil {
			see(t, "session write")
		}
	}
	return at, what
}

// FriendCardMoved is the newest move of a card of hers that she or her daemon made: a
// progress stamp, or a take, on a card ready or working on her row; zero when none.
func FriendCardMoved(s *Snapshot, f string) time.Time {
	var at time.Time
	if s == nil || s.Fleet == nil {
		return at
	}
	row := FriendRow(f)
	for _, c := range append(append([]*Card(nil), s.Fleet.Cell(row, Ready)...), s.Fleet.Cell(row, Working)...) {
		for _, k := range []string{FieldProgress, "taken"} {
			if t := stampAt(c, k); t.After(at) {
				at = t
			}
		}
	}
	return at
}

// futureEvidenceOnce is the log of a stamp dated after the server's clock,
// once per friend and stamp. The tick calls FriendWorked every pass, and a
// parallel test must not swap a process logger to count the line.
var futureEvidenceOnce sync.Map

func futureEvidenceKey(friend string, stamped time.Time) string {
	return friend + "\x00" + stamped.UTC().Format(time.RFC3339Nano)
}

// noteFutureEvidence counts a future stamp as the server's now and logs that
// once. stamped is the evidence time before the clamp.
func noteFutureEvidence(friend string, stamped, now time.Time, word string) {
	if _, loaded := futureEvidenceOnce.LoadOrStore(futureEvidenceKey(friend, stamped), 1); loaded {
		return
	}
	log.Printf("friend %s evidence %s dated %s, after the server's clock %s: counted as now",
		friend, word, stamped.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
}
