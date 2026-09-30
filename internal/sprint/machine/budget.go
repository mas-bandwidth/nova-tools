package machine

import (
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
	"github.com/mas-bandwidth/nova-tools/internal/tset"
)

// Budget is what one tick may do (1.0, "The tick budget" and "Bytes"; 1.4.2;
// SprintEvents.tla leaves the step budget out, "a tick applies every request
// it planned", so the budget is the design's alone).
type Budget struct {
	// Changes, EntriesNotes, RequestBytes and Steps bound RT3: at most 10,000
	// changed members in at most 32 steps, which write at most 2,500 entries
	// and notes in all and carry at most 2 MiB of requests (1.0; 1.4.2).
	Changes, EntriesNotes, RequestBytes, Steps int
	// StepBytes is the most one tick step carries: the builder cuts a tick's
	// steps at 2 MiB of request, where a verb's keep Layer 1's 4 MiB (1.0).
	StepBytes int
	// AgendaHead and HeldHead are the keys RT1 reads of the agenda's head and
	// the held queue's (2,000 each, 1.4.2; the held rule's own budget of 2,000
	// cards). Layer 1 bounds one range at 2,000 (L1 7).
	AgendaHead, HeldHead int
	// PageLines, PageIDs and PageBytes bound the ingest's page: 5,000 lines,
	// 20,000 ids, and a line limit set to expect at most 2 MiB (1.0, E5).
	PageLines, PageIDs, PageBytes int
	// Pop is the most due and cut entries RT1 pops (1.2: 1,000).
	Pop int
	// Read is the bounds one rule's read is planned within (L1 6, 7; 1.4.2).
	Read sprint.ReadBounds
}

// DefaultBudget is the design's budget.
func DefaultBudget() Budget {
	return Budget{
		Changes: 10000, EntriesNotes: 2500, RequestBytes: 2 << 20, Steps: 32, StepBytes: 2 << 20,
		AgendaHead: sprint.MaxRangeLimit, HeldHead: sprint.MaxRangeLimit,
		PageLines: 5000, PageIDs: 20000, PageBytes: 2 << 20,
		Pop:  sprintfn.PopMax,
		Read: sprint.L1ReadBounds(),
	}
}

// spend is what the steps dealt so far cost.
type spend struct{ changes, entriesNotes, bytes, steps int }

// stepCost is one step's cost against the budget: the members its entries
// change (candidates, no-ops included, 1.0), its entries and notes, and its
// encoded bytes.
type stepCost struct{ changes, entriesNotes, bytes int }

// costOf is a body's cost as the request the tick sends.
func costOf(prefix string, req *sprintfn.Request) (stepCost, *sprintfn.Refusal) {
	n, ref := sprintfn.EncodedSize(prefix, req)
	if ref != nil {
		return stepCost{}, ref
	}
	c := stepCost{bytes: n, entriesNotes: len(req.Body.Entries) + len(req.Body.Notes)}
	for _, e := range req.Body.Entries {
		if changesMembers(e) {
			c.changes += len(e.IDs)
		}
	}
	return c, nil
}

// changesMembers says an entry changes the members it names (create, move,
// remove); guards, counts, rows and advance do not.
func changesMembers(e tset.Entry) bool {
	return e.Kind == "create" || e.Kind == "move" || e.Kind == "remove"
}

// fits says a step of cost c can still be dealt.
func (b Budget) fits(s spend, c stepCost) bool {
	return s.steps < b.Steps && s.changes+c.changes <= b.Changes && s.entriesNotes+c.entriesNotes <= b.EntriesNotes &&
		s.bytes+c.bytes <= b.RequestBytes
}

// add is the spend with one more step.
func (s spend) add(c stepCost) spend {
	return spend{changes: s.changes + c.changes, entriesNotes: s.entriesNotes + c.entriesNotes, bytes: s.bytes + c.bytes, steps: s.steps + 1}
}

// planned is one rule's plan of this tick, cut into requests, ready to deal.
type planned struct {
	rule     sprint.Rule
	batch    Batch
	plan     sprint.RulePlan
	reqs     []*sprintfn.Request
	costs    []stepCost
	whole    bool // the plan fit one request: it removes the keys it finished (1.3.6)
	dealt    int
	blocked  bool // a request of it did not fit: the rest of it waits for a later tick
	refusals map[string]int
}

// deal deals the planned requests round robin in priority order (1.4.2:
// "Every rule with keys gets one step before any rule gets a second, so a long
// release never starves a deal"; SprintEvents.tla, Rounds and Order): round i
// takes the i-th request of every rule that has one, until the budget is
// spent. A rule's MaxSteps caps its requests (R19: one a tick). A request that
// does not fit blocks the rest of its rule's plan for this tick (its keys stay
// queued, T3), and dealing goes on with the others. reserve is the spend of
// the steps the tick sends besides the rules' (the quarantine step), dealt
// first.
func deal(ps []*planned, b Budget, reserve spend) ([]*planned, []int, spend) {
	var order []*planned
	var index []int
	s := reserve
	for round := 0; ; round++ {
		any := false
		for _, p := range ps {
			if p.blocked || round >= len(p.reqs) || (p.rule.MaxSteps > 0 && round >= p.rule.MaxSteps) {
				continue
			}
			any = true
			if !b.fits(s, p.costs[round]) {
				p.blocked = true
				continue
			}
			s = s.add(p.costs[round])
			p.dealt++
			order, index = append(order, p), append(index, round)
		}
		if !any || s.steps >= b.Steps {
			return order, index, s
		}
	}
}
