package machine

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// TestEveryRuleReadGoesOnTheWire: the read plan of every registered rule, for
// a key naming a subject, a key naming a line and the rule's bare key, is sent
// by readRequest: every query it holds is one the wire carries (ValidateSprintQ
// through EncodeSprintQ). R2's read of a member's cells with each card's
// primary was refused REQUEST on every tick, a follow the wire did not know,
// and a silent member was never taken down (the 4806 read, M2). The rules are
// those of the fleet, the position, the review and the held table (R1 to R10,
// R15, R16); the time rules read the sprint's keys through kinds of their own
// (tick, cut, dropping, goal), which are not composite queries.
func TestEveryRuleReadGoesOnTheWire(t *testing.T) {
	t.Parallel()
	wired := map[string]bool{"seen": true, "down": true, "needs": true, "resolve": true, "cross": true, "deal": true, "level": true,
		"ask": true, "accept": true, "rework": true, "done": true, "held": true}
	for _, r := range sprint.RuleTable() {
		if !wired[r.Name] {
			continue
		}
		for _, keys := range [][]sprint.AgendaKey{
			{{Key: r.Name + ":m1", Seq: 1}},
			{{Key: r.Name + ":s1-1", Seq: 2}},
			{{Key: r.Name + "@7", Seq: 3}},
			{{Key: r.Name, Seq: 4}},
			{{Key: r.Name + ":s1", Seq: 5}, {Key: r.Name + ":m2", Seq: 6}, {Key: r.Name + "@9", Seq: 7}},
		} {
			if r.Name == "needs" && keys[0].Key == "needs" {
				continue // needs has no fixed key: ingest queues needs:<p> and needs@<seq>
			}
			rp, _ := r.Read(keys, sprint.L1ReadBounds(), 0)
			if len(rp.Sprint) == 0 && len(rp.IDs) == 0 && len(rp.Ranges) == 0 && len(rp.Counts) == 0 && len(rp.Lines) == 0 {
				continue
			}
			if _, err := readRequest(testNames, "0", rp); err != nil {
				t.Errorf("rule %s, keys %v: its read cannot be sent: %v", r.Name, keys, err)
			}
		}
	}
}
