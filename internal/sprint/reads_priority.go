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

// PipelineCounts is the work table's primaries working, in review and merging, sentinels
// aside, over the streams on the table.
func PipelineCounts(s *Snapshot) (working, review, merging int) {
	if s == nil || s.Work == nil {
		return 0, 0, 0
	}
	for _, c := range s.Work.Column(Working, Review, Merging) {
		if IsSentinel(c) {
			continue
		}
		switch c.Col {
		case Working:
			working++
		case Review:
			review++
		case Merging:
			merging++
		}
	}
	return working, review, merging
}

// Backup is the snapshot's backup state (BackupOf over PipelineCounts).
func Backup(s *Snapshot) string { return BackupOf(PipelineCounts(s)) }

// ReadsWaiting is the count of reads waiting: the read cards wanted and not yet dealt, and
// those dealt and not started (readCardsWaitingCount).
func ReadsWaiting(s *Snapshot) int { return readCardsWaitingCount(s) }

// readAttempt is the primary's attempt as the reads count it: 1 when it names none.
func readAttempt(pr *Card) int {
	if a := pr.Int("attempt"); a > 0 {
		return a
	}
	return 1
}

// friendDealByLadder is the tick's friend deal by the ladder (TickDeal): level by level,
// highest first, the work cards of that level are dealt to the friends in the room left. A
// read card is dealt before any of them (withReadCards), so her reads already hold their half
// slots of her room. The reclaim of the fleet's dealt-ahead cards runs once, in the normal
// pass (friendDealPass). Its plan holds the passes in that order; a start receipt every pass
// plans is the first's alone. out is the seats the level after the deal reads beside the work
// dealt (dealt).
func friendDealByLadder(s *Snapshot, offer []*Card, seats []FriendSeat) (p Plan, out []FriendSeat, dealt, dealtWorking map[string]int) {
	work := map[int][]*Card{}
	for _, c := range offer {
		work[cardRank(c)] = append(work[cardRank(c)], c)
	}
	cur := slices.Clone(seats)
	dealt, dealtWorking = map[string]int{}, map[string]int{}
	var parts []Plan
	normal := priorityRank(PriorityNormal)
	for r := range PriorityLadder {
		if len(work[r]) == 0 && r != normal {
			continue
		}
		wp, wd, ww := friendDealPass(s, work[r], cur, r == normal)
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
	// the seats the level reads: the work dealt is the level's own count (dealt), never twice;
	// her reads are cards on her row already (read_cards.go), counted by her load
	return p, slices.Clone(seats), dealt, dealtWorking
}
