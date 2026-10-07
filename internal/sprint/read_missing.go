package sprint

import (
	"cmp"
	"fmt"
	"maps"
)

// A read on a branch origin does not hold (docs/SPEC-SPRINT.md section 6; 2026-10-06, 7:46 to
// 8:27 PM ET: about half of one reader's 54 broken verdicts said "the branch this read names
// is not on origin", and each sent its card to rework, a new attempt and two new reads). A
// broken verdict on a read whose branch origin does not hold at its close is the machine's
// fault, never the card's: the server checks the read's branch against origin before the
// close (ReadReq.Missing, the read verb and friend sync), not the finding's words. Such a
// verdict is no verdict: the read is retired by RetiredByMissingBranch, which spends no
// reader (it is not in spentBy, and on the readers table its reader may be asked again under
// the second identity), no broken-read judgment is written, so the card is not reworked and
// its attempt is not spent. When the server found the branch origin holds the work's head on, the primary
// records it (FieldReadBranch), every read of that head names it (ReadBranch), and the ask asks
// the read again on it; when no branch of the work on origin holds the head, the seat is told at
// once (NReadBranchMissing) and the read is not asked again until it closes. The happened
// note says "re-asked: the read named a branch not on origin".

// RetiredByMissingBranch is the retired_by of a read whose broken verdict named a branch
// origin does not hold: retired with no verdict and asked again.
const RetiredByMissingBranch = "branch-missing"

// NReadMissingBranch is the happened note of such a read, re-asked.
const NReadMissingBranch = "a read named a branch not on origin"

// ReaskedMissingBranch begins the note's text and the card's reason.
const ReaskedMissingBranch = "re-asked: the read named a branch not on origin"

// NReadBranchMissing is the judgment of a read whose branch origin does not hold when no
// branch of the work on origin holds its head either: written at the first such close, on the
// primary, naming the read card and the branch. While it is open at the primary's attempt the
// primary wants no read (ReadsWanted, readCardsWanted): the seat pushes the branch and acks it
// (the tick asks the read again), or reworks or drops the card.
const NReadBranchMissing = "a read's branch is not on origin"

// readBranchMissingOpen says an NReadBranchMissing judgment is open on the primary at its
// attempt: it is asked no read until the judgment closes.
func readBranchMissingOpen(s *Snapshot, pr *Card) bool {
	if s == nil || pr == nil {
		return false
	}
	attempt := readAttempt(pr)
	for _, o := range closesFor(s.Open, []string{NReadBranchMissing}, pr.ID) {
		if o.Note.Attempt == 0 || o.Note.Attempt == attempt {
			return true
		}
	}
	return false
}

// MissingBranch is the server's finding on one read's branch at its close: the branch the
// read named, which origin does not hold, and the branch origin holds the read's head on (""
// when it found none).
type MissingBranch struct {
	Named string `json:"named"`
	Holds string `json:"holds,omitempty"`
}

// missingBranch is whether a broken verdict on read card c is a read on a branch origin does
// not hold (r.Missing names it), and the server's finding.
func (r ReadReq) missingBranch(c *Card) (MissingBranch, bool) {
	if r.Begin || r.Return || r.Verdict != "broken" || r.Missing == nil {
		return MissingBranch{}, false
	}
	m, ok := r.Missing[c.ID]
	return m, ok
}

// missingBranchUnit retires read card c on table, whose broken verdict named a branch origin
// does not hold, with no verdict, its cost kept as a returned run's; the primary records the
// branch origin holds the head on when the server found it (fixed); one happened note.
func missingBranchUnit(s *Snapshot, table string, c, pr *Card, m MissingBranch, finding, usage, who string) Unit {
	branch, fixed := m.Named, m.Holds
	reader := cmp.Or(c.F("reader"), c.Row)
	if name, ok := FriendOfRow(c.Row); ok {
		reader = name
	}
	head := c.F("head")
	reason := ReaskedMissingBranch + " (" + orDash(branch) + ")"
	if fixed != "" {
		reason += "; asked again on " + fixed + ", which holds " + orDash(head)
	} else {
		reason += "; no branch of the work on origin holds " + orDash(head) + ": not asked again until it is (" + NReadBranchMissing + ")"
	}
	run := nextTake(c, FieldReadTake)
	rec := readCostRecord(s, c, usage, c.F("asked"), cmp.Or(c.F("begun"), stamp(s.Now)))
	set := map[string]string{"retired": stamp(s.Now), "retired_by": RetiredByMissingBranch, "reason": cutText(reason, MaxCardTextBytes),
		"finding": cutText(finding, MaxCardTextBytes), FieldReadTake + itoa(run): rec}
	if usage != "" {
		maps.Copy(set, readUsageFields(c, usage))
	}
	changes := []Change{change(table, removeEntry(c, set))}
	key, stream := c.ID, c.F("stream")
	if pr != nil {
		key, stream = pr.ID, pr.Row
		prSet := map[string]string{}
		if pr.Placed() {
			addConsumer(pr, prSet, readConsumer(s, c, run, "returned", rec))
		}
		if fixed != "" && head != "" {
			prSet[FieldReadBranch], prSet[FieldReadBranchHead] = fixed, head
		}
		if len(prSet) > 0 && pr.Placed() {
			changes = append(changes, change(Work, setEntry(pr, prSet)))
		}
	}
	n := happened(NReadMissingBranch, stream, s.Now, c.F("primary"))
	n.Who, n.Attempt, n.What = cmp.Or(who, reader), c.Int("attempt"), fmt.Sprintf("%s: %s's read %s; its attempt is not spent", reason, reader, c.ID)
	notes := []Note{n}
	if fixed == "" && pr != nil && pr.Placed() && !readBranchMissingOpen(s, pr) {
		// no branch on origin holds the head: a read asked again could only find the same, so
		// the seat is told at once and the primary waits on the judgment (readBranchMissingOpen)
		j := judgment(NReadBranchMissing, pr.Row, s.Now, 0, pr.ID)
		j.Who, j.Attempt, j.Card = cmp.Or(who, reader), c.Int("attempt"), c.ID
		j.What = fmt.Sprintf("%s's read %s of %s names branch %s, which origin does not hold, and no branch of its work on origin holds its head %s; it is not asked again until the branch is on origin: push it and ack this, or rework, or drop",
			reader, c.ID, pr.ID, orDash(branch), orDash(head))
		notes = append(notes, j)
	}
	return Unit{Key: key, Stream: stream, Changes: changes, Notes: notes,
		Moved: c.ID + " " + c.Col + " -> " + RetiredByMissingBranch + " (retired: " + ReaskedMissingBranch + ")"}
}
