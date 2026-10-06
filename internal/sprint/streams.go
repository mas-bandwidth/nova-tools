package sprint

import (
	"fmt"
	"slices"
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
		case !c.Placed() || c.Row != st:
		case IsSentinel(c):
			sentinels++
		default:
			primaries++
		}
	}
	for _, c := range s.Merge.Cards() {
		if c.Placed() && c.Row == st && c.Col != Ctl {
			merges++
		}
	}
	if primaries+sentinels+merges == 0 {
		return ""
	}
	var holds []string
	for _, h := range []struct {
		n          int
		one, other string
	}{{primaries, "primary", "primaries"}, {merges, "merge card", "merge cards"}, {sentinels, "sentinel", "sentinels"}} {
		if h.n > 0 {
			holds = append(holds, fmt.Sprintf("%d %s", h.n, map[bool]string{true: h.one, false: h.other}[h.n == 1]))
		}
	}
	return fmt.Sprintf("stream %s holds %s; nothing was changed; its cards leave with nova-sprint clear --confirm sprint, or nova-sprint drop <id>... --reason <why>", st, strings.Join(holds, ", "))
}

// RemovedStream says the stream was removed in this epoch (stream remove):
// its control card's record is kept unplaced, and the table layer never
// places a removed member again, so the stream cannot open again before the
// next clear: add refuses it then, naming the clear. The add step reads the
// record as an extra (store.AddStep).
func RemovedStream(s *Snapshot, stream string) bool {
	ctl := s.Merge.Card(CtlID(stream))
	return ctl != nil && !ctl.Placed()
}

// WhereReleasesCountCardsLeft folds cards left per release from stream clocks
// and per-stream cards left counts (docs/SPEC-SPRINT.md section 11, where --release).
func WhereReleasesCountCardsLeft(clocks []StreamClock, streamCardsLeft map[string]int64) map[string]int64 {
	out := map[string]int64{}
	for _, c := range clocks {
		if c.Release != "" {
			out[c.Release] += streamCardsLeft[c.Stream]
		}
	}
	return out
}

// The archive's notes (stream archive, docs/SPEC-SPRINT.md section 11, streams): the
// tick took a stream whose every card landed off the work and merge tables, or showed
// an archived stream again because it holds a card not landed. Each is information,
// a happened note to the coordinator, written once for the streams it names.
const (
	NStreamArchived   = "stream archived"
	NStreamUnarchived = "stream shown again"
)

// StreamArchive is the rule of stream archive: why each named stream may not leave
// the drawn work and merge tables, none when every one may. A stream is archived only
// when it is a row of the work table, not archived already, and every card it holds
// has landed: every primary and sentinel placed in its work row is in the landed
// column, and every merge card in its merge row is in the merged column (its control
// card is no card it holds). The machine may be RUNNING: archiving moves no card.
// The verb names several and applies all or none.
func StreamArchive(s *Snapshot, archived map[string]bool, streams []string) []Refusal {
	return allOrNone("archived", streams, func(st string) string {
		switch {
		case !s.Work.HasRow(st):
			return fmt.Sprintf("no stream %s on the work table (streams: %s); nothing was changed", st, strings.Join(s.Streams(), ","))
		case archived[st]:
			return fmt.Sprintf("stream %s is archived already; nothing was changed; run: nova-sprint where --archived", st)
		}
		if live := UnlandedOf(s, st); len(live) > 0 {
			return fmt.Sprintf("stream %s holds %d %s not landed: %s; nothing was changed; a stream is archived once every card it holds has landed", st, len(live), map[bool]string{true: "card", false: "cards"}[len(live) == 1], strings.Join(live, ", "))
		}
		return ""
	})
}

// StreamUnarchive is the rule of stream unarchive: a stream it names must be archived.
func StreamUnarchive(rows []string, archived map[string]bool, streams []string) []Refusal {
	return allOrNone("unarchived", streams, func(st string) string {
		switch {
		case archived[st]:
			return ""
		case slices.Contains(rows, st):
			return fmt.Sprintf("stream %s is not archived; nothing was changed", st)
		}
		return fmt.Sprintf("no stream %s on the work table; nothing was changed", st)
	})
}

// UnlandedOf is every card the stream holds that has not landed, as "<id> (<state>)",
// in id order: the work row's primaries and sentinels not in landed, the merge row's
// cards not in merged, its control card left out. At most unlandedShown are named,
// the rest counted.
func UnlandedOf(s *Snapshot, stream string) []string {
	var out []string
	n := 0
	add := func(c *Card) {
		if n++; n <= unlandedShown {
			out = append(out, c.ID+" ("+c.Col+")")
		}
	}
	for _, c := range s.Work.Cards() {
		if c.Placed() && c.Row == stream && c.Col != Landed {
			add(c)
		}
	}
	if s.Merge != nil {
		for _, c := range s.Merge.Cards() {
			if c.Placed() && c.Row == stream && c.Col != Ctl && c.Col != Merged {
				add(c)
			}
		}
	}
	if n > unlandedShown {
		out = append(out, fmt.Sprintf("and %d more", n-unlandedShown))
	}
	return out
}

const unlandedShown = 5

// allOrNone refuses every stream named when any one is refused: why is the rule for
// one, done the verb's past participle.
func allOrNone(done string, streams []string, why func(string) string) []Refusal {
	var out []Refusal
	refused := map[string]bool{}
	for _, st := range streams {
		if w := why(st); w != "" {
			out = append(out, Refusal{Key: st, Why: w})
			refused[st] = true
		}
	}
	if len(out) == 0 {
		return nil
	}
	whole := fmt.Sprintf("not %s: the verb names several and applies to all or none, and %d of them %s refused", done, len(out), map[bool]string{true: "was", false: "were"}[len(out) == 1])
	for _, st := range streams {
		if !refused[st] {
			refused[st] = true
			out = append(out, Refusal{Key: st, Why: whole})
		}
	}
	return out
}
