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
// the second identity), no judgment is written, so the card is not reworked and its attempt
// is not spent, and the ask asks the read again. When the server found the branch origin
// holds the work's head on, the primary records it (FieldReadBranch) and every read of that
// head names it (ReadBranch). The happened note says "re-asked: the read named a branch not
// on origin".

// RetiredByMissingBranch is the retired_by of a read whose broken verdict named a branch
// origin does not hold: retired with no verdict and asked again.
const RetiredByMissingBranch = "branch-missing"

// NReadMissingBranch is the happened note of such a read, re-asked.
const NReadMissingBranch = "a read named a branch not on origin"

// ReaskedMissingBranch begins the note's text and the card's reason.
const ReaskedMissingBranch = "re-asked: the read named a branch not on origin"

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
		reason += "; no branch of the work on origin holds " + orDash(head) + ", asked again as it is"
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
	return Unit{Key: key, Stream: stream, Changes: changes, Notes: []Note{n},
		Moved: c.ID + " " + c.Col + " -> " + RetiredByMissingBranch + " (retired: " + ReaskedMissingBranch + ")"}
}
