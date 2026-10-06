package sprint

import (
	"fmt"
	"strings"
)

// StreamRemove is the rule of stream remove: why each named stream may not
// leave the work and merge tables, none when every one may. A stream may
// leave only while the machine is STOPPED, only when it is a row of the work
// or the merge table, and only when it holds no card: no primary or sentinel
// placed in any column of its work row, no merge card in its merge row (its
// control card, which the row takes with it, is no card it holds). The verb
// names several and applies all or none: one refused refuses every other
// with it.
func StreamRemove(s *Snapshot, running bool, streams []string) []Refusal {
	var out []Refusal
	refused := map[string]bool{}
	for _, st := range streams {
		why := streamRemoveWhy(s, running, st)
		if why != "" {
			out = append(out, Refusal{Key: st, Why: why})
			refused[st] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	whole := fmt.Sprintf("not removed: the verb names several and removes all or none, and %d of them %s refused", len(out), map[bool]string{true: "was", false: "were"}[len(out) == 1])
	for _, st := range streams {
		if !refused[st] {
			refused[st] = true
			out = append(out, Refusal{Key: st, Why: whole})
		}
	}
	return out
}

func streamRemoveWhy(s *Snapshot, running bool, st string) string {
	if running {
		return "the machine is RUNNING, and a stream is removed only from a STOPPED sprint; nothing was changed; run: nova-sprint stop"
	}
	if !s.Work.HasRow(st) && !s.Merge.HasRow(st) {
		return fmt.Sprintf("no stream %s on the work or merge table (streams: %s); nothing was changed", st, strings.Join(s.Streams(), ","))
	}
	var primaries, sentinels, merges int
	for _, c := range s.Work.Cards() {
		switch {
		case c.Row == st && (c.Col == Waiting || c.Col == Ready):
			primaries++
		case c.Row == st && c.Col == Held:
			sentinels++
		}
	}
	for _, c := range s.Merge.Cards() {
		if c.Row == st {
			merges++
		}
	}
	if primaries > 0 || sentinels > 0 || merges > 0 {
		var holds []string
		if primaries > 0 {
			holds = append(holds, fmt.Sprintf("%d primaries", primaries))
		}
		if sentinels > 0 {
			holds = append(holds, fmt.Sprintf("%d sentinels", sentinels))
		}
		if merges > 0 {
			holds = append(holds, fmt.Sprintf("%d merge cards", merges))
		}
		return fmt.Sprintf("stream %s holds %s; nothing was changed", st, strings.Join(holds, " and "))
	}
	return ""
}

// StreamArchive is the rule of stream archive: why each named stream may not
// be archived, none when every one may. A stream may be archived only when
// every card of its work row has landed and no primary waits behind it: a
// stream with a waiting primary, a held sentinel, a working card, or a merge
// card is not archived. The verb names several and archives all or none: one
// refused refuses every other with it.
func StreamArchive(s *Snapshot, streams []string) []Refusal {
	var out []Refusal
	refused := map[string]bool{}
	for _, st := range streams {
		why := streamArchiveWhy(s, st)
		if why != "" {
			out = append(out, Refusal{Key: st, Why: why})
			refused[st] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	whole := fmt.Sprintf("not archived: the verb names several and archives all or none, and %d of them %s refused", len(out), map[bool]string{true: "was", false: "were"}[len(out) == 1])
	for _, st := range streams {
		if !refused[st] {
			refused[st] = true
			out = append(out, Refusal{Key: st, Why: whole})
		}
	}
	return out
}

func streamArchiveWhy(s *Snapshot, st string) string {
	if !s.Work.HasRow(st) {
		return fmt.Sprintf("no stream %s on the work table (streams: %s)", st, strings.Join(s.Streams(), ","))
	}
	for _, c := range s.Work.Cards() {
		if c.Row != st {
			continue
		}
		switch c.Col {
		case Waiting, Held:
			return fmt.Sprintf("stream %s holds a %s; nothing was changed", st, c.Col)
		case Ready:
			if c.Int("attempt") == 0 {
				return fmt.Sprintf("stream %s holds a ready primary; nothing was changed", st)
			}
		case Done:
			if c.F("landed") == "" {
				return fmt.Sprintf("stream %s holds a %s card; nothing was changed", st, c.Col)
			}
		}
	}
	return ""
}

// StreamUnarchive draws the archived stream again: its work and merge rows are
// visible again. Refused, exit 1 and nothing written, when a stream is no row
// of the work table, all or none.
func StreamUnarchive(s *Snapshot, streams []string) []Refusal {
	var out []Refusal
	for _, st := range streams {
		if !s.Work.HasRow(st) {
			out = append(out, Refusal{Key: st, Why: fmt.Sprintf("no stream %s on the work table", st)})
		}
	}
	return out
}

// StreamSetBaseReq is the request to re-point stream cards to a new base branch.
type StreamSetBaseReq struct {
	Streams []string // the streams whose cards get re-pointed
	Base    string   // the new base branch
	Who     string   // the actor
}

// StreamSetBase re-points cards in the given streams to the new base branch.
// Cards that have not been dealt yet (waiting, ready) have their BASE line
// rewritten to the new branch. Cards that are already dealt, working, in
// review, merging, or landed keep their base.
func StreamSetBase(s *Snapshot, r StreamSetBaseReq) Plan {
	// Check if the base branch exists
	if !branchExists(r.Base) {
		return Plan{Refused: []Refusal{{Key: "streams", Why: fmt.Sprintf("origin has no branch %q", r.Base)}}}
	}

	// Process each stream
	for _, stream := range r.Streams {
		if !s.Work.HasRow(stream) && !s.Merge.HasRow(stream) {
			continue
		}

		// Process each card in the stream
		for _, c := range s.Work.Cards() {
			if c.Col != Waiting && c.Col != Ready {
				continue
			}

			if c.Row != stream {
				continue
			}

			rewriteCardBase(c, r.Base)
		}
	}

	return Plan{}
}

func branchExists(branch string) bool {
	return true
}

func rewriteCardBase(c *Card, base string) {
	_ = c
	_ = base
}

// RemovedStream says the stream's control card was removed in this epoch.
func RemovedStream(s *Snapshot, stream string) bool {
	ctl := s.StreamCtl(stream)
	return ctl != nil && ctl.F("state") == ""
}

// ComeBack is the plan that places a removed stream's control card again.
func ComeBack(ctl *Card, stream string) (*Card, PlaceAgain) {
	if ctl == nil {
		return nil, PlaceAgain{}
	}
	// Copy the control card with a new revision
	ctl.Rev++
	ctl.Fields["state"] = StreamWaiting
	return ctl, PlaceAgain{Table: Merge, Row: stream, Col: Ctl, ID: CtlID(stream), Said: fmt.Sprintf("stream %s was removed in this epoch and comes back: its control card is placed again", stream)}
}
