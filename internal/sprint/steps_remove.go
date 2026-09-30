package sprint

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// RemoveChunk is the most entries one part of a remove holds: layer 1's
// ceiling for one request (1.0, the chunk), the same as the other planners
// that cut their work in parts (stepChunk).
const RemoveChunk = 2000

// RowDel is a row the last part of a remove deletes from a table (ask AL6 of
// layer 1: a row whose every member the same part removes counts as empty).
type RowDel struct{ Table, Row string }

// Remove plans taking whole streams off the four tables (the upper design,
// version 2.1, the verb `remove`, B1, and IT27): for every stream named, every
// card of the stream in the work table, and every card in the readers, merge
// and fleet tables that belongs to one of them; then the stream's rows in the
// work and merge tables. It plans and applies nothing.
//
// A card belongs to a stream when it is placed in the stream's row, or names
// the stream (its stream field) or a primary of it (its primary field): the
// work cards of the fleet's table, the read cards and the merge cards carry
// both, so that a finished work card of a primary dropped earlier, whose
// primary has no place any more, goes with its stream. A primary goes with
// every card it ever had in the same part.
//
// The plan is one for the whole batch, in parts: each Unit is one part, of at
// most RemoveChunk entries, holding one stream's cards (streams in the order
// named, a stream's primaries in the order waiting, ready, working, review,
// merging, landed, each in score order), and the stream's control cards in its
// last part. A primary with more cards than a part holds is split over parts.
// The last unit of the plan carries, in its RowDels, every
// stream's rows in work and merge, as the design's last part removes them.
//
// A stream that does not exist, an empty list, or a snapshot loaded from a
// read plan (not whole tables) is refused: the plan is empty and the error
// says nothing was removed. A stream named twice is planned once.
func Remove(s *Snapshot, streams []string) (Plan, error) {
	if s == nil || s.Work == nil || s.Readers == nil || s.Merge == nil || s.Fleet == nil {
		return Plan{}, errors.New("no tables to remove from; nothing was removed")
	}
	if s.Partial != nil {
		return Plan{}, errors.New("remove is planned from the whole tables, and this snapshot was loaded from a read plan; nothing was removed")
	}
	var names []string
	seen := map[string]bool{}
	for _, st := range streams {
		if !seen[st] {
			seen[st] = true
			names = append(names, st)
		}
	}
	if len(names) == 0 {
		return Plan{}, errors.New("no stream named; nothing was removed")
	}
	var missing []string
	for _, st := range names {
		if !s.Work.HasRow(st) && !s.Merge.HasRow(st) {
			missing = append(missing, st)
		}
	}
	if len(missing) > 0 {
		return Plan{}, fmt.Errorf("no such stream: %s; nothing was removed", strings.Join(missing, ", "))
	}

	// One pass over each other table finds the cards of the batch's streams,
	// grouped by primary (control cards, which have none, by the stream).
	type key struct{ stream, primary string }
	group := func(t *Table, primaries map[string]string) map[key][]*Card {
		out := map[key][]*Card{}
		for _, c := range t.Cards() {
			if !c.Placed() {
				continue
			}
			st, pr := c.F("stream"), c.F("primary")
			if !seen[st] {
				if st = primaries[pr]; st == "" && seen[c.Row] && t == s.Merge {
					st = c.Row
				}
			}
			if st == "" || !seen[st] {
				continue
			}
			out[key{st, pr}] = append(out[key{st, pr}], c)
		}
		return out
	}
	// primaries is each placed primary of the batch's streams and its stream.
	primaries := map[string]string{}
	ordered := map[string][]*Card{}
	for _, st := range names {
		for _, col := range States {
			for _, c := range s.Work.Cell(st, col) {
				primaries[c.ID] = st
				ordered[st] = append(ordered[st], c)
			}
		}
	}
	byTable := map[string]map[key][]*Card{
		Fleet: group(s.Fleet, primaries), Readers: group(s.Readers, primaries), Merge: group(s.Merge, primaries),
	}

	var p Plan
	p.removing = true
	p.on(s)
	for _, st := range names {
		var part []Change
		n := 0
		flush := func() {
			if len(part) == 0 {
				return
			}
			n++
			p.Units = append(p.Units, Unit{Key: fmt.Sprintf("remove %s part %d", st, n), Stream: st, Changes: part,
				Moved: fmt.Sprintf("removed %d cards of stream %s", len(part), st)})
			part = nil
		}
		add := func(cs []Change) {
			// A group goes whole into one part; one over a part is split.
			if len(part) > 0 && len(part)+len(cs) > RemoveChunk {
				flush()
			}
			for len(cs) > RemoveChunk {
				part = append(part, cs[:RemoveChunk]...)
				cs = cs[RemoveChunk:]
				flush()
			}
			part = append(part, cs...)
		}
		// of is what one primary ever had, in ApplyOrder, and the primary's own
		// card last.
		of := func(pr string, work *Card) []Change {
			var cs []Change
			for _, t := range []string{Fleet, Readers, Merge} {
				k := byTable[t][key{st, pr}]
				for _, c := range k {
					cs = append(cs, change(t, removeEntry(c, nil)))
				}
				delete(byTable[t], key{st, pr})
			}
			if work != nil {
				cs = append(cs, change(Work, removeEntry(work, nil)))
			}
			return cs
		}
		for _, c := range ordered[st] {
			add(of(c.ID, c))
		}
		// Cards of primaries that have no place in the work table any more, in
		// primary order, then the stream's control cards (no primary).
		var rest []string
		for _, t := range []string{Fleet, Readers, Merge} {
			for k := range byTable[t] {
				if k.stream == st && k.primary != "" {
					rest = append(rest, k.primary)
				}
			}
		}
		sort.Strings(rest)
		for i, pr := range rest {
			if i > 0 && rest[i-1] == pr {
				continue
			}
			add(of(pr, nil))
		}
		add(of("", nil))
		flush()
	}

	rows := func() (r []RowDel) {
		for _, st := range names {
			if s.Work.HasRow(st) {
				r = append(r, RowDel{Work, st})
			}
			if s.Merge.HasRow(st) {
				r = append(r, RowDel{Merge, st})
			}
		}
		return r
	}()
	if len(p.Units) == 0 {
		p.Units = append(p.Units, Unit{Key: "remove " + strings.Join(names, ","), Stream: names[0],
			Moved: "removed no cards of " + strings.Join(names, ", ")})
	}
	last := &p.Units[len(p.Units)-1]
	last.RowDels = rows
	return Lawful(p), nil
}
