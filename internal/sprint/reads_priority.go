package sprint

import (
	"slices"
)

// Reads are a card priority (docs/SPEC-SPRINT.md sections 1 and 5; the owner, 2026-10-06,
// with 270 cards in review, 76 working and every reader near idle: "I am now convinced that
// reads need to become a type of card priority." "This will help balance reads vs. work from
// now on."). A read card is at reader priority (priority.go), above normal and low work: every
// deal asks and places the friends' reads before it deals any work card, in the same plan,
// and deals work only in the room the reads leave; a friend is dealt a work card only when no
// read she may take waits (friendDealByLadder, friendReadsFirst). A fleet reader's room is its own,
// never a work lane, so a fleet read waits behind no work card; the readers' ask places them
// in the same tick. Inside the reads the order is the modelled one (work order, the ask's
// stream turns). The backup state (Backup) is exposed on where; the judgment at its edge is
// card a-backup-transition-pushes-one-judgment-b's.

// The backup states (Backup): reads when review exceeds working, merges when merging exceeds
// review and working together (it names the further bottleneck when both hold), none else.
const (
	BackupNone   = "none"
	BackupReads  = "reads"
	BackupMerges = "merges"
)

// BackupOf is the backup state of the three counts of the work table (BackupNone,
// BackupReads, BackupMerges): merges when merging exceeds review plus working, reads when
// review exceeds working, else none.
func BackupOf(working, review, merging int) string {
	switch {
	case merging > review+working:
		return BackupMerges
	case review > working:
		return BackupReads
	}
	return BackupNone
}

// readsWaitingCards is every primary in review whose reads are wanted now and not asked
// (ReadsWanted), sentinels and failed work aside, in work order.
func readsWaitingCards(s *Snapshot) []*Card {
	if s == nil || s.Work == nil {
		return nil
	}
	var out []*Card
	for _, pr := range s.Work.Column(Review) {
		if IsSentinel(pr) || pr.F("result") == "failed" || ReadsWanted(s, pr) == 0 {
			continue
		}
		out = append(out, pr)
	}
	return out
}

// ReadsWaiting is the count of reads waiting to be asked: the reads wanted now (ReadsWanted)
// over every primary in review whose work did not fail.
func ReadsWaiting(s *Snapshot) int {
	if s.ReadCardsOn() {
		return readCardsWaitingCount(s)
	}
	n := 0
	for _, pr := range readsWaitingCards(s) {
		n += ReadsWanted(s, pr)
	}
	return n
}

// friendMayRead says the friend may be asked the primary's read at its attempt: she is up,
// one of her tiers is at or above its read tier (friendAtOrAbove), she did not work the
// attempt, and no read card of hers at the attempt exists (a read taken back keeps its id,
// so she is not asked it again). The friends' ask (friendReadAsk) and the deal's reads first
// (friendReadsFirst) ask the same question.
func friendMayRead(s *Snapshot, f FriendSeat, pr *Card, attempt int, tier, worker string) bool {
	return f.Status == Up && f.Name != worker && friendAtOrAbove(s, f, tier) && s.Fleet.Card(ReadCardID(pr.ID, attempt, f.Name)) == nil
}

// readAttempt is the primary's attempt as the reads count it: 1 when it names none.
func readAttempt(pr *Card) int {
	if a := pr.Int("attempt"); a > 0 {
		return a
	}
	return 1
}

// friendReadsFirst is the seats as the deal deals work to them, reads first: each friend's
// ReadsFirst is the room her reads take before any work card, the reads placed on her in
// placed (the deal's own reads) and, while a read she may take still waits (waiting less the
// primaries placed), the whole of her room, so the deal gives her no work card until no read
// she may take waits. The seats given are not changed.
func friendReadsFirst(s *Snapshot, seats []FriendSeat, waiting []*Card, placed Plan) []FriendSeat {
	if len(seats) == 0 || s == nil || s.Fleet == nil {
		return seats
	}
	on := map[string]int{}     // the reads placed on each friend's row in placed
	asked := map[string]bool{} // the primaries placed
	for _, u := range placed.Units {
		asked[u.Key] = true
		for _, ch := range u.Changes {
			if ch.Table != Fleet || ch.Entry.Create == nil {
				continue
			}
			if name, ok := FriendOfRow(ch.Entry.Create.Row); ok {
				on[name]++
			}
		}
	}
	out := slices.Clone(seats)
	for i, f := range out {
		out[i].ReadsFirst += on[f.Name]
		for _, pr := range waiting {
			if asked[pr.ID] {
				continue
			}
			attempt := readAttempt(pr)
			if friendMayRead(s, f, pr, attempt, friendReadTier(s, pr), attemptWorker(s, pr.ID, attempt)) {
				all := f
				all.ReadsFirst = 0
				room, _ := friendRoom(s, all)
				out[i].ReadsFirst += room // a read she may take waits: no work card this deal
				break
			}
		}
	}
	return out
}

// friendDealByLadder is the tick's friend deal by the ladder (TickDeal): level by level,
// highest first, the reads of that level (a read's level is the higher of reader and its
// primary's, readRank) are asked of the friends first, then the work cards of that level are
// dealt in the room the reads leave; at one level a read goes before work, so the reads of a
// high primary come before high work and reader-level reads between high and normal work. A
// friend with a read she may take still waiting at the level or above is dealt no work card of
// the level (friendReadsFirst). The reclaim of the fleet's dealt-ahead cards runs once, in the
// normal pass (friendDealPass). Its plan holds the passes in that order; a start receipt every
// pass plans is the first's alone. out is the seats with the room each friend's reads took,
// all of it while a read she may take still waits (FriendSeat.ReadsFirst), which the level
// after the deal reads beside the work dealt (dealt).
func friendDealByLadder(s *Snapshot, offer []*Card, seats []FriendSeat) (p Plan, out []FriendSeat, dealt, dealtWorking map[string]int) {
	work := map[int][]*Card{}
	for _, c := range offer {
		work[cardRank(c)] = append(work[cardRank(c)], c)
	}
	var waiting []*Card
	if s != nil && s.Work != nil && s.Fleet != nil && len(seats) > 0 && !s.ReadCardsOn() {
		// while read cards are on, the reads are cut by the read-card ask, and the deal's
		// snapshot already holds the room they take (TickDeal, withReadsReserved)
		waiting = readOrder(readsWaitingCards(s))
	}
	cur := slices.Clone(seats)
	asked, readsOn := map[string]bool{}, map[string]int{}
	dealt, dealtWorking = map[string]int{}, map[string]int{}
	var parts []Plan
	normal := priorityRank(PriorityNormal)
	for r := range PriorityLadder {
		// the reads of this level, asked in the room left
		var mine []*Card
		for _, pr := range waiting {
			if readRank(pr) == r && !asked[pr.ID] {
				mine = append(mine, pr)
			}
		}
		if len(mine) > 0 && len(cur) > 0 {
			if rp, _, err := friendReadAskOf(s, cur, "", mine); err == nil {
				parts = append(parts, rp)
				placed := friendReadsFirst(s, cur, nil, rp) // the room the placed reads take
				for i := range cur {
					readsOn[cur[i].Name] += placed[i].ReadsFirst - cur[i].ReadsFirst
					cur[i].ReadsFirst = placed[i].ReadsFirst
				}
				for _, u := range rp.Units {
					asked[u.Key] = true
				}
			}
		}
		if len(work[r]) == 0 && r != normal {
			continue
		}
		// a friend with a read she may take of this level or above still waiting takes no work
		var above []*Card
		for _, pr := range waiting {
			if readRank(pr) <= r && !asked[pr.ID] {
				above = append(above, pr)
			}
		}
		held := friendReadsFirst(s, cur, above, Plan{})
		wp, wd, ww := friendDealPass(s, work[r], held, r == normal)
		parts = append(parts, wp)
		for i := range cur {
			cur[i].ReadsFirst += wd[cur[i].Name]
		}
		for k, v := range wd {
			dealt[k] += v
		}
		for k, v := range ww {
			dealtWorking[k] = max(dealtWorking[k], v) // the start receipts: every pass plans the same
		}
	}
	keys, rows := map[string]bool{}, map[RowAdd]bool{}
	for _, part := range parts {
		for _, r := range part.Rows {
			if !rows[r] {
				rows[r] = true
				p.Rows = append(p.Rows, r)
			}
		}
		for _, u := range part.Units {
			if keys[u.Key] {
				continue // a start receipt every pass planned, or a card already placed
			}
			keys[u.Key] = true
			p.Units = append(p.Units, u)
		}
		p.Refused = append(p.Refused, part.Refused...)
	}
	// the seats the level reads: the room her reads took, and the whole of it while a read she
	// may take still waits; the work dealt is the level's own count (dealt), never twice
	reads := slices.Clone(seats)
	for i := range reads {
		reads[i].ReadsFirst += readsOn[reads[i].Name]
	}
	var left []*Card
	for _, pr := range waiting {
		if !asked[pr.ID] {
			left = append(left, pr)
		}
	}
	return p, friendReadsFirst(s, reads, left, Plan{}), dealt, dealtWorking
}
