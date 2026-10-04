package sprint

import "fmt"

// The fleet's and the friends' ok% is the readers' (docs/SPEC-SPRINT.md section 1;
// the owner, 2026-10-04: "trust but VERIFY"; "I want to trust the ok%"; "Are they
// actually doing the work that is shown in the friend table? Really?"). A worker's
// finish, ok or failed, moves its work card to the member's hidden finished cell,
// where it counts in no ok%; a reader's verdict on that attempt moves it on, to ok when
// as many different readers as the primary needs (ReadsNeeded) read it ok, to failed
// when a reader read it broken. Work no reader reads (a failed finish) stays finished.

// readVerdict is the readers' verdict on an attempt of primary pr: DoneFailed when a
// reader read the attempt broken, DoneOK when ReadsNeeded different readers read
// it ok, "" while neither. reads are the attempt's read cards (readsAt).
func readVerdict(pr *Card, reads []*Card) string {
	ok := map[string]bool{}
	for _, rc := range reads {
		switch rc.F("verdict") {
		case "broken":
			return DoneFailed
		case "ok":
			ok[rc.Row] = true
		}
	}
	if pr != nil && len(ok) >= ReadsNeeded(pr) {
		return DoneOK
	}
	return ""
}

// verdictChange is the move of a work card waiting in finished to the cell of the
// readers' verdict on it (readVerdict), false when there is none yet.
func verdictChange(pr, wc *Card, reads []*Card) (Change, string, bool) {
	if wc == nil || !wc.Placed() || wc.Col != Finished {
		return Change{}, "", false
	}
	col := readVerdict(pr, reads)
	if col == "" {
		return Change{}, "", false
	}
	return change(Fleet, moveEntry(wc, wc.Row, col, nil)), col, true
}

// TickVerdicts is the fleet update's part that counts the readers' verdicts: each work
// card in a member's finished cell whose attempt the readers have read (readVerdict)
// moves to its ok or failed cell, up to TickMaxMoves a tick; the rest are due. A rework
// that retires the read cards moves its work card in the same step (Rework).
func TickVerdicts(s *Snapshot, _ TickReq) (Plan, int) {
	var p Plan
	due := 0
	for _, wc := range s.Fleet.Column(Finished) {
		pr := s.Work.Placed(wc.F("primary"))
		if pr == nil {
			continue
		}
		ch, col, ok := verdictChange(pr, wc, readsAt(s, pr, wc.Int("attempt")))
		if !ok {
			continue
		}
		if len(p.Units) >= TickMaxMoves {
			due++
			continue
		}
		p.Units = append(p.Units, Unit{Key: wc.ID, Stream: wc.F("stream"), Changes: []Change{ch},
			Moved: fmt.Sprintf("%s finished -> %s (the readers' verdict)", wc.ID, col)})
	}
	return p, due
}

// TickFriendRedeal is the fleet update's part that redeals a friend's card past its
// deadline unfinished (the deadline WorkDeadline holds every work card to): never a
// failure and never a late judgment. Its work card goes to her redealt cell, where the
// friends table counts it, at its next generation, so her late report finishes nothing;
// its primary goes back to ready, and the next deal (FriendDeal) deals its next attempt
// to a friend up with room. Up to TickMaxMoves a tick; the rest are due.
func TickFriendRedeal(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	due := 0
	for _, wc := range s.Fleet.Column(Ready, Working) {
		if !friendLate(s, r, wc) {
			continue
		}
		if len(p.Units) >= TickMaxMoves {
			due++
			continue
		}
		field, limit, word, _ := WorkDeadline(s, wc)
		set := nextGen(wc, "", s.Now)
		set[Redealt] = stamp(s.Now)
		what := fmt.Sprintf("%s %s at %s, %s past %s: redealt", wc.ID, field, wc.F(field), word, limit)
		u := Unit{Key: wc.ID, Stream: wc.F("stream"), Changes: []Change{change(Fleet, moveEntry(wc, wc.Row, Redealt, set))},
			Moved: fmt.Sprintf("%s %s -> redealt gen=%d, past its deadline", wc.ID, wc.Col, wc.Int("gen")+1)}
		if pr := s.Work.Placed(wc.F("primary")); pr != nil && pr.Col == Working && pr.F("work") == wc.ID {
			u.Changes = append(u.Changes, change(Work, moveEntry(pr, pr.Row, Ready, nil, "work")))
			u.Moved += "; " + pr.ID + " working -> ready"
			n := happened(NFriendRedealt, pr.Row, s.Now, pr.ID)
			n.Who, n.What, n.Card = r.who(), what, wc.ID
			u.Notes = append(u.Notes, n)
		}
		p.Units = append(p.Units, u)
	}
	return p, due
}

// friendLate says wc is a friend's card past its deadline (WorkDeadline) in running time.
func friendLate(s *Snapshot, r TickReq, wc *Card) bool {
	if !IsFriendRow(wc.Row) {
		return false
	}
	field, limit, _, _ := WorkDeadline(s, wc)
	d, ok := r.running(s.Now, wc.F(field))
	return ok && d > limit
}
