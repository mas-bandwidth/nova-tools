package sprint

import (
	"fmt"
	"strings"
)

// StreamRemove is the rule of stream remove (the owner, 2026-10-01: "you
// should have a verb to remove work streams" / "they should only succeed on a
// STOPPED sprint machine"): why each named stream may not leave the work and
// merge tables, none when every one may. A stream may leave only while the
// machine is STOPPED, only when it is a row of the work or the merge table,
// and only when it holds no card: no primary or sentinel placed in any column
// of its work row, no merge card in its merge row (its control card, which
// the row takes with it, is no card it holds). The verb names several and
// applies all or none: one refused refuses every other with it.
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
