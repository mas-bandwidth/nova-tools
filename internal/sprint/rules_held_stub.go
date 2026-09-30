package sprint

import "sort"

// rules_held_stub.go stands in for the items IT11 needs and that are not
// merged (the upper design, version 2.1, 8.0 and 8.1). Each stand-in says
// which item it is for, so that it is deleted, or rewritten on the real types,
// when they merge, and holder.go and rules_held.go compile against the real ones
// with no other change.
//
//	IT05 (rule.go)      Rule, RegisterRule, RuleTable: deleted.
//	IT05 (readplan.go)  ReadPlan, its query shapes, QueryCost, Cost: deleted.
//	IT05 (partial.go)   FirstSentinel, OpenBefore: deleted.
//	IT05 (the partial snapshot) heldFactsOf, the one place that reads the
//	                    sprint keys a read carries beside the tables: rewritten
//	                    on what the snapshot carries.
//	IT06 (judgments.go) typeVerbStopped, a judgment type the tree has no name
//	                    for: deleted for IT06's.
//
// rules_held.go touches no field of SprintQ: it builds its queries with
// relatedOf, relatedLine, frontOfEveryStream and fleetOf, at the end of this
// file, and these four are what is rewritten when ReadPlan takes the shape of
// 8.0. The tests that read a SprintQ by its fields change with them.

// Rule is one rule of the tick as 8.0 fixes it: its name, its priority, the
// most steps a tick may give it, the read that sizes what it needs, and the
// plan it makes over that read. IT05 owns it.
type Rule struct {
	Name     string
	Priority int
	MaxSteps int // 0 is no cap; R19 is 1
	// Read plans the read for the keys and returns the keys it left for
	// later; halvings > 0 after a BUDGET or LIMIT (1.3.5).
	Read func(keys []AgendaKey, b ReadBounds, halvings int) (ReadPlan, []AgendaKey)
	Plan func(s *Snapshot, keys []AgendaKey, now Now) RulePlan
}

var ruleTable []Rule

// RegisterRule adds a rule to the table, called from each rules file's init.
func RegisterRule(r Rule) {
	ruleTable = append(ruleTable, r)
	sort.SliceStable(ruleTable, func(i, j int) bool { return ruleTable[i].Priority < ruleTable[j].Priority })
}

// RuleTable is the rules in priority order.
func RuleTable() []Rule { return append([]Rule(nil), ruleTable...) }

// The query shapes of layer 1 (L1 7): the tree has no tset package yet, and
// the held rule asks none of them, so they are empty.
type (
	// RangeQ is a range read of layer 1.
	RangeQ struct{}
	// CountQ is a count read of layer 1.
	CountQ struct{}
	// RCountQ is a range count read of layer 1.
	RCountQ struct{}
	// LinesQ is a log page read of layer 2.
	LinesQ struct{}
)

// SprintQ is one of the composite queries of 1.0 with its id source: ids, or
// a line by seq (its ids from Offset, at most Limit). Only the fields the held
// rule's queries use are here.
type SprintQ struct {
	Kind   string // related, front, fleet
	Table  string
	IDs    []string
	Line   uint64 // the id source, when it is a line by seq
	Offset int
	Limit  int
	Stream string // for front: a stream, or AllStreams
	Fields []string
	Follow []string
}

// AllStreams names, for front, every stream of the sprint. The design gives a
// read plan no way to name the streams of ids it has not read (open question
// of IT11); this is the stand-in.
const AllStreams = "*"

// ReadPlan is what one rule's read asks of the store in one snapshot (1.0).
type ReadPlan struct {
	IDs     map[string][]string
	Ranges  []RangeQ
	Counts  []CountQ
	RCounts []RCountQ
	Lines   []LinesQ
	Sprint  []SprintQ
}

// Cost is what the plan may cost the store, the sum of its queries' declared
// costs.
func (rp ReadPlan) Cost() Cost {
	var c Cost
	for _, q := range rp.Sprint {
		qc := QueryCost(q)
		c.Records += qc.Records
		c.RangeIDs += qc.RangeIDs
		c.Bytes += qc.Bytes
	}
	return c
}

// The declared cost of the follows of related (1.0's table: 1, plus 1 a
// follow, 15 for rcards, 64 for needs), and of the queries that name every
// stream or member of the sprint (3: members and streams together at most
// 250, so 250 of either).
const (
	costFollow       = 1
	costFollowRCards = 15
	costFollowNeeds  = 64
	costWholeSprint  = 250
)

// QueryCost is the records, range ids and bytes a composite query may cost
// (1.0). Only the queries the held rule asks are costed; the bytes are left
// at zero, since the design gives no size for a record.
func QueryCost(q SprintQ) Cost {
	switch q.Kind {
	case "related":
		n := len(q.IDs)
		if q.Line != 0 {
			n = q.Limit
		}
		per := 1
		for _, f := range q.Follow {
			switch f {
			case "rcards":
				per += costFollowRCards
			case "needs":
				per += costFollowNeeds
			default:
				per += costFollow
			}
		}
		return Cost{Records: n * per}
	case "front":
		if q.Stream == AllStreams {
			return Cost{Records: costWholeSprint}
		}
		return Cost{Records: 1}
	case "fleet":
		return Cost{Records: costWholeSprint}
	}
	panic("QueryCost: the stand-in costs only related, front and fleet, not " + q.Kind)
}

// FirstSentinel is the first sentinel of the stream in the order of its
// scores, the sentinel sigma of front(s): nil when the stream has none.
func FirstSentinel(s *Snapshot, stream string) *Card {
	if s.Work == nil {
		return nil
	}
	for _, c := range s.Work.Cell(stream, Waiting) {
		if IsSentinel(c) {
			return c
		}
	}
	return nil
}

// OpenBefore is the number of the stream's open cards (its five open cells)
// with a score below the score: n_before of front(s).
func OpenBefore(s *Snapshot, stream string, score float64) int {
	line := s.Work.openLine(stream)
	return sort.Search(len(line), func(i int) bool { return line[i].Score >= score })
}

// heldFactsOf is what the snapshot carries of the sprint's own keys beside its
// tables. The snapshot of the tree carries none of them but the judgments the
// coordinator acknowledged, which it holds the way a wait holds a judgment
// (the design keeps that hold in jopen); IT05's partial snapshot carries the
// rest, and this is the one place that reads them.
func heldFactsOf(s *Snapshot) HeldFacts { return HeldFacts{Held: s.Acked} }

// typeVerbStopped is the type of the judgment "a verb in parts stopped before
// its end" (2.2), which the tree has no name for. IT06's table names it.
const typeVerbStopped = "a verb in parts stopped before its end"

// The queries the held rule asks, built where the shape of SprintQ is known,
// so that rules_held.go names none of its fields: when IT05's ReadPlan and
// SprintQ merge, these four are the ones to write again.

// relatedOf is related over ids by id: their records with the fields, and what
// follows from each.
func relatedOf(ids, fields, follow []string) SprintQ {
	return SprintQ{Kind: "related", Table: Work, IDs: ids, Fields: fields, Follow: follow}
}

// relatedLine is related over the ids of a line by seq, from an offset, at most
// limit of them.
func relatedLine(h heldKey, limit int, fields, follow []string) SprintQ {
	return SprintQ{Kind: "related", Table: Work, Line: h.Line, Offset: h.Offset, Limit: limit, Fields: fields, Follow: follow}
}

// frontOfEveryStream is front(s) of every stream of the sprint, with no heads:
// sigma and n_before of each.
func frontOfEveryStream() SprintQ { return SprintQ{Kind: "front", Stream: AllStreams} }

// fleetOf is fleet: every member's control card with the fields.
func fleetOf(fields ...string) SprintQ { return SprintQ{Kind: "fleet", Fields: fields} }
