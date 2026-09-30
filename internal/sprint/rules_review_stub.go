package sprint

import "sort"

// A stub for IT05's rule.go and readplan.go (the upper design, version 2.1,
// sections 8.0 and 8.1): the rule registry and the read plan, in the smallest
// shape rules_review.go builds against. It is deleted when IT05 merges; what
// rules_review.go reads of it is in the list below, so that the edit at that
// merge is the names this file gives and no more.
//
//	Rule, RegisterRule, RuleTable    8.0, complete
//	ReadPlan.Sprint                  8.1 IT05: the one field of ReadPlan the review rules fill
//	SprintQ                          8.1 IT05: only Kind, Table, IDs, Line, Offset, Head, Limit, Fields and
//	                                 Follow, the words 1.0's composite queries are asked in
//	QueryCost                        8.0, for the queries the review rules ask (1.0's table)
//
// IT06's tables are not stood in for: the review rules name the types of
// their notes as strings (rules_review.go) and leave the decisions a type
// offers to IT06's Judgments.

// The composite queries the review rules ask (1.0).
const (
	// qRelated is `related`: each id's record and the records its follows name.
	qRelated = "related"
	// qFleet and qReaders are `fleet` and `readers`: every member's (reader's)
	// control card and the counts of its cells.
	qFleet   = "fleet"
	qReaders = "readers"
)

// SprintQ is one composite query of 1.0, with its id source: ids, a line of
// the log by seq (from an offset), or the head of an index or a cell.
type SprintQ struct {
	// Kind is the query: related, fleet or readers.
	Kind string
	// Table is the table of the ids a related query is over.
	Table string
	// IDs, Line and Offset, or Head and Limit, are the id source: named ids; a
	// line by seq and the id of it to start at; or the first Limit ids of a
	// head.
	IDs    []string
	Line   uint64
	Offset int
	Head   string
	Limit  int
	// Fields is the projection of each record (no query returns a whole record
	// unless asked); Follow is the records a related query follows to.
	Fields []string
	Follow []string
}

// ReadPlan is what a rule's read asks of the store in one snapshot. IT05's
// has more fields (ids, ranges, counts, lines); the review rules fill this
// one.
type ReadPlan struct {
	// Sprint is the composite queries.
	Sprint []SprintQ
}

// stubFollowCost is the records a follow of `related` costs an id, at most
// (1.0's table): rcards holds at most 15 read cards, needs at most 64, and the
// other follows are one record each.
var stubFollowCost = map[string]int{"rcards": 15, "needs": 64}

// The most a query without ids names: a sprint has at most 250 members and 1,024
// members, readers and streams in all (section 3, the size of the sprint).
const (
	stubMaxMembers = 250
	stubMaxReaders = 1024
)

// QueryCost is the records a composite query may cost the store, for the
// queries the review rules ask: a related query costs each of its ids one
// record and its follows' records; fleet and readers cost a record a member
// or a reader.
func QueryCost(q SprintQ) Cost {
	switch q.Kind {
	case qRelated:
		per := 1
		for _, f := range q.Follow {
			if n, ok := stubFollowCost[f]; ok {
				per += n
			} else {
				per++
			}
		}
		ids := len(q.IDs)
		if q.Limit > ids {
			ids = q.Limit
		}
		return Cost{Records: per * ids}
	case qFleet:
		return Cost{Records: stubMaxMembers}
	case qReaders:
		return Cost{Records: stubMaxReaders}
	}
	return Cost{}
}

// Rule is a rule of the tick as the loop takes it (8.0): the plan it reads
// its keys with, and the plan it makes from what it read.
type Rule struct {
	Name     string
	Priority int
	MaxSteps int // 0 is no cap; R19 is 1
	// the plan, and the keys it left for later; halvings > 0 after a BUDGET or LIMIT (1.3.5)
	Read func(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey)
	Plan func(s *Snapshot, keys []AgendaKey, now Now) RulePlan
}

var stubRules []Rule

// RegisterRule adds a rule to the table; each rules file calls it from init.
func RegisterRule(r Rule) { stubRules = append(stubRules, r) }

// RuleTable is every rule registered, in priority order.
func RuleTable() []Rule {
	out := append([]Rule(nil), stubRules...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out
}
