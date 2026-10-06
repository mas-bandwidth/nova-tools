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

// NStreamArchived is the tick's word that it archived streams whose every card
// has landed (stream archive): information, each stream named once.
const NStreamArchived = "streams archived"

// StreamArchive is the rule of stream archive: why each named stream may not
// be archived, none when every one may. Archiving hides a stream's rows of the
// work and merge tables (the table layer's row hide) and moves no card: its
// landed cards stay placed in its landed cell, with their costs and landings,
// and every fold, footer and summary counts them as before. A stream is
// archived only when it is a row of the work or merge table and every card it
// holds has landed: no primary or sentinel in any column of its work row but
// landed, no merge card queued or stuck in its merge row. The machine may be
// RUNNING. Archiving an archived stream changes nothing. The verb names
// several and applies all or none.
func StreamArchive(s *Snapshot, streams []string) []Refusal {
	return allOrNone(streams, "archived", func(st string) string { return streamArchiveWhy(s, st) })
}

// StreamUnarchive is the rule of stream unarchive: a stream comes back only
// when it is a row of the work or merge table and is archived.
func StreamUnarchive(s *Snapshot, streams []string) []Refusal {
	return allOrNone(streams, "unarchived", func(st string) string {
		if why := noStream(s, st); why != "" {
			return why
		}
		if !s.Work.Hidden(st) && !s.Merge.Hidden(st) {
			return fmt.Sprintf("stream %s is not archived; nothing was changed", st)
		}
		return ""
	})
}

// ArchivedStreams is the streams whose work row is archived, in row order.
func ArchivedStreams(s *Snapshot) []string {
	var out []string
	for _, st := range s.Work.Rows() {
		if s.Work.Hidden(st) {
			out = append(out, st)
		}
	}
	return out
}

// Unlanded is the cards of the stream that have not landed, in work order:
// its primaries and sentinels in any column of its work row but landed, and
// its merge cards queued or stuck.
func Unlanded(s *Snapshot, stream string) []*Card {
	var out []*Card
	for _, c := range s.Work.Cards() {
		if c.Placed() && c.Row == stream && c.Col != Landed {
			out = append(out, c)
		}
	}
	SortCards(out)
	for _, col := range []string{Queued, Stuck} {
		out = append(out, s.Merge.Cell(stream, col)...)
	}
	return out
}

func streamArchiveWhy(s *Snapshot, st string) string {
	if why := noStream(s, st); why != "" {
		return why
	}
	open := Unlanded(s, st)
	if len(open) == 0 {
		return ""
	}
	var named []string
	for i, c := range open {
		if i == 5 {
			named = append(named, fmt.Sprintf("and %d more", len(open)-i))
			break
		}
		named = append(named, c.ID+" "+c.Col)
	}
	return fmt.Sprintf("stream %s holds %d %s not landed (%s); nothing was changed; a stream is archived when every card of it has landed", st, len(open),
		map[bool]string{true: "card", false: "cards"}[len(open) == 1], strings.Join(named, ", "))
}

func noStream(s *Snapshot, st string) string {
	if !s.Work.HasRow(st) && !s.Merge.HasRow(st) {
		return fmt.Sprintf("no stream %s on the work or merge table (streams: %s); nothing was changed", st, strings.Join(s.Streams(), ","))
	}
	return ""
}

// allOrNone is each stream's refusal by why, and when any is refused every
// other with it, as a verb that names several and applies all or none.
func allOrNone(streams []string, done string, why func(string) string) []Refusal {
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

// NStreamRetired is what a judgment of a stream taken off the tables is
// answered with: its decided note's what opens with it, then the stream and
// how it left (stream remove, stream archive, or gone when the tick finds it).
const NStreamRetired = "retired with its stream"

// StreamGone says the stream is a row of neither the work nor the merge table:
// removed (stream remove), or never added. An archived stream is a row still.
// A note's stream that holds a colon is no stream (a tier's, a provider's or a
// member's subject: TierSubject, ProviderSubject, MemberSubject), and is never
// gone: no stream id has a colon.
func StreamGone(s *Snapshot, stream string) bool {
	return stream != "" && !strings.Contains(stream, ":") && !s.Work.HasRow(stream) && !s.Merge.HasRow(stream)
}

// StreamOpens is the open notes that name a stream of the set, judgments and
// acknowledged holds both, in their order: a note of the stream, or one about
// the stream as a whole.
func StreamOpens(s *Snapshot, named func(string) bool) []Open {
	var out []Open
	for _, all := range [][]Open{s.Open, s.Acked} {
		for _, o := range all {
			sub, ok := strings.CutPrefix(o.Subject(), "stream:")
			if named(o.Note.Stream) || ok && named(sub) {
				out = append(out, o)
			}
		}
	}
	return out
}

// RetireStreams is the plan that retires every open note naming one of the
// streams (judgments, alarms and stale notes, and the holds acknowledged on
// them): each closed with a decided note "retired with its stream: <stream>
// <how>", and one Said line a stream that has any, naming the notes. A stream
// that leaves the tables (stream remove, stream archive) leaves no judgment
// whose next step it would refuse with "no such stream".
func RetireStreams(s *Snapshot, streams []string, how, who string) Plan {
	var p Plan
	for _, st := range streams {
		opens := StreamOpens(s, func(x string) bool { return x == st })
		if len(opens) == 0 {
			continue
		}
		var ids []string
		for _, o := range opens {
			if !slices.Contains(ids, o.Note.ID) {
				ids = append(ids, o.Note.ID)
				what := fmt.Sprintf("%s: %s %s", NStreamRetired, st, how)
				p.Notes = append(p.Notes, decided(o, what, who, s.Now))
			}
			p.Closes = append(p.Closes, o)
		}
		p.Said = append(p.Said, fmt.Sprintf("stream %s %s: %d open %s retired with it (%s)", st, how, len(ids),
			map[bool]string{true: "note", false: "notes"}[len(ids) == 1], Preview(ids, ", ")))
	}
	return p
}

// TickRetire is the tick's retire part: the open notes of every stream the
// tables no longer hold (StreamGone), written before the stream left or by a
// remove that did not finish, are retired, each with its note.
func TickRetire(s *Snapshot, r TickReq) (Plan, int) {
	var gone []string
	for _, o := range StreamOpens(s, func(x string) bool { return StreamGone(s, x) }) {
		st := o.Note.Stream
		if sub, ok := strings.CutPrefix(o.Subject(), "stream:"); ok && StreamGone(s, sub) {
			st = sub
		}
		if !slices.Contains(gone, st) {
			gone = append(gone, st)
		}
	}
	return RetireStreams(s, gone, "is gone from the tables", r.who()), 0
}

// WithRetire is the tick part fn with TickRetire's plan joined to its own: the
// tick's end runs its first part so (store.Tick), and a tick with nothing to
// retire runs no step more than it did.
func WithRetire(fn TickPartFn) TickPartFn {
	return func(s *Snapshot, r TickReq) (Plan, int) {
		p, due := fn(s, r)
		q, _ := TickRetire(s, r)
		p.Notes = append(p.Notes, q.Notes...)
		p.Closes = append(p.Closes, q.Closes...)
		p.Said = append(p.Said, q.Said...)
		return p, due
	}
}

// WithoutGoneStreams is the tick's plan with every judgment for a stream the
// tables do not hold taken out: the tick never raises one, since its next step
// would be refused with "no such stream".
func WithoutGoneStreams(s *Snapshot, p Plan) Plan {
	keep := func(ns []Note) []Note {
		return slices.DeleteFunc(slices.Clone(ns), func(n Note) bool { return n.Kind == Judgment && StreamGone(s, n.Stream) })
	}
	p.Notes = keep(p.Notes)
	if len(p.Units) > 0 {
		p.Units = slices.Clone(p.Units)
		for i := range p.Units {
			p.Units[i].Notes = keep(p.Units[i].Notes)
		}
	}
	return p
}
