package sprint

import (
	"fmt"
	"sort"
	"strconv"
)

// Indexes is a set of the derived indexes as a store would hold them: each key
// to its members and their scores. A key with no member is not there, as a
// sorted set with none does not exist. The members of wait:<n> carry no score
// (section 1.3.1 gives them none): they are compared as a set.
type Indexes map[IndexKey]map[string]float64

// Apply applies ops in order: each op removes its members, then adds its
// members with their scores; a key left empty goes.
func (x Indexes) Apply(ops []IndexOp) {
	for _, o := range ops {
		m := x[o.Key]
		for _, r := range o.Rem {
			delete(m, r)
		}
		if len(o.Add) > 0 && m == nil {
			m = map[string]float64{}
			x[o.Key] = m
		}
		for _, a := range o.Add {
			m[a.Member] = a.Score
		}
		if len(m) == 0 {
			delete(x, o.Key)
		}
	}
}

// scored says the members of a key carry a score that means something.
func scored(k IndexKey) bool { return k.Index != IndexWait }

// Diff is how x differs from want, one line for each member that is in one
// and not in the other, or whose score differs (the members of wait:<n> have
// no score to differ); none when they are equal. The lines are ordered.
func (x Indexes) Diff(want Indexes) []string {
	var out []string
	for k, m := range x {
		for member, score := range m {
			w, ok := want[k][member]
			switch {
			case !ok:
				out = append(out, fmt.Sprintf("%s %s is in the running index and not in the definition", k, member))
			case scored(k) && w != score:
				out = append(out, fmt.Sprintf("%s %s is scored %s, and %s in the definition", k, member, fmtScore(score), fmtScore(w)))
			}
		}
	}
	for k, m := range want {
		for member := range m {
			if _, ok := x[k][member]; !ok {
				out = append(out, fmt.Sprintf("%s %s is in the definition and not in the running index", k, member))
			}
		}
	}
	sort.Strings(out)
	return out
}

// Lines is every member on one line, "elig:s1 p2@20" (a member of wait:<n> with
// no score: "wait:s1-1 p2"), ordered by key and then by member.
func (x Indexes) Lines() []string {
	var out []string
	for k, m := range x {
		for member, score := range m {
			if scored(k) {
				out = append(out, k.String()+" "+member+"@"+fmtScore(score))
			} else {
				out = append(out, k.String()+" "+member)
			}
		}
	}
	sort.Strings(out)
	return out
}

// IndexDefinition is every index of section 1.3.1 and the due entries of
// section 1.2 computed from scratch over a set of cards, one definition at a
// time, and nothing else: it reads no table of IndexDefs, so that a step's ops
// have something other than themselves to be equal to. It is slow (every card
// is read for every index) and meant to be obviously right.
//
//	sent:<s>       the sentinels of s: kind sentinel, in waiting.
//	elig:<s>       the primaries of s free to go: in waiting, not a sentinel,
//	               open = 0, no refused.
//	fresh:<s>      the ready primaries never dealt: in ready, attempt = 0, no
//	               refused (a sentinel is out of it by kind, section 1.3.5).
//	again:<s>      the ready primaries dealt before: in ready, attempt >= 1, no
//	               bound, no refused.
//	wait:<n>       a waiting card that names n, while n is open (n is a card of
//	               the work table that is placed and has not landed).
//	due            the card in the state of a due kind, with its due field set:
//	               untaken (a work card in a member's ready column), unfinished
//	               (working), unbegun (a read card asked), unreported (a read
//	               card begun), mergeidle (a stream's control card).
//
// Every member of sent, elig, fresh and again is scored by the card's score,
// every due entry by its field. A card with no place, and a nil card, are in
// no index. A field that holds anything but a whole number where a number is
// read is refused, naming it.
func IndexDefinition(cards []*IndexCard) (Indexes, error) {
	out := Indexes{}
	add := func(index, arg, member string, score float64) {
		k := IndexKey{index, arg}
		if out[k] == nil {
			out[k] = map[string]float64{}
		}
		out[k][member] = score
	}
	// A member of the four indexes of a stream is keyed by the card's row; a card placed with no row
	// is refused.
	var bad error
	addRow := func(index string, c *IndexCard) {
		if c.Row == "" {
			bad = errNoRow(c)
			return
		}
		add(index, c.Row, c.ID, c.Score)
	}
	open := map[string]bool{}
	for _, c := range cards {
		if c != nil && c.Table == Work && c.Col != "" && c.Col != Landed {
			open[c.ID] = true
		}
	}
	for _, c := range cards {
		if c == nil || c.Col == "" {
			continue
		}
		switch c.Table {
		case Work:
			sentinel := c.Fields["kind"] == "sentinel"
			refused, bound := c.Fields["refused"] != "", c.Fields["bound"] != ""
			if c.Col == Waiting {
				openNeeds, err := wholeField(c, "open")
				if err != nil {
					return nil, err
				}
				if sentinel {
					addRow(IndexSent, c)
				}
				if !sentinel && openNeeds == 0 && !refused {
					addRow(IndexElig, c)
				}
				for _, n := range Split(c.Fields["needs"]) {
					if open[n] {
						add(IndexWait, n, c.ID, 0)
					}
				}
			}
			if c.Col == Ready {
				attempt, err := wholeField(c, "attempt")
				if err != nil {
					return nil, err
				}
				if !sentinel && attempt == 0 && !refused {
					addRow(IndexFresh, c)
				}
				if attempt >= 1 && !bound && !refused {
					addRow(IndexAgain, c)
				}
			}
		case Fleet:
			if c.Col == Ready && c.Fields["due_untaken"] != "" {
				due, err := wholeField(c, "due_untaken")
				if err != nil {
					return nil, err
				}
				add(IndexDue, "", "untaken:"+c.ID, float64(due))
			}
			if c.Col == Working && c.Fields["due_unfinished"] != "" {
				due, err := wholeField(c, "due_unfinished")
				if err != nil {
					return nil, err
				}
				add(IndexDue, "", "unfinished:"+c.ID, float64(due))
			}
		case Readers:
			if c.Col == Asked && c.Fields["due_unbegun"] != "" {
				due, err := wholeField(c, "due_unbegun")
				if err != nil {
					return nil, err
				}
				add(IndexDue, "", "unbegun:"+c.ID, float64(due))
			}
			if c.Col == Reading && c.Fields["due_unreported"] != "" {
				due, err := wholeField(c, "due_unreported")
				if err != nil {
					return nil, err
				}
				add(IndexDue, "", "unreported:"+c.ID, float64(due))
			}
		case Merge:
			if c.Col == Ctl && c.Fields["due_mergeidle"] != "" {
				due, err := wholeField(c, "due_mergeidle")
				if err != nil {
					return nil, err
				}
				if c.Row == "" {
					bad = errNoRow(c)
				}
				add(IndexDue, "", "mergeidle:"+c.Row, float64(due))
			}
		}
	}
	if bad != nil {
		return nil, bad
	}
	return out, nil
}

// wholeField is a field that holds a whole number: 0 when it is absent or
// empty.
func wholeField(c *IndexCard, name string) (int64, error) {
	if c.Fields[name] == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(c.Fields[name], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s card %s: field %s is %q, not a whole number", c.Table, c.ID, name, c.Fields[name])
	}
	return n, nil
}
