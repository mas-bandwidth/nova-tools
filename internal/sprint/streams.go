package sprint

import (
	"fmt"
	"sort"
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

// NStreamRetired is the word that the open notes of streams taken off the
// tables were retired (RetireStreams): information, one note a stream,
// naming the notes it retired.
const NStreamRetired = "open notes of a removed stream retired"

// StreamOff says the stream is off the work and merge tables: a name with no
// colon (a stream's, never a subject's: no primary or stream id has one) that
// is a row of neither table. A stream removed (stream remove) is off; an
// archived one is not, its rows kept hidden. A snapshot that did not read both
// tables says no stream is off.
func StreamOff(s *Snapshot, st string) bool {
	if st == "" || strings.Contains(st, ":") || s.Work == nil || s.Merge == nil {
		return false
	}
	return !s.Work.HasRow(st) && !s.Merge.HasRow(st)
}

// namesStream says the open note is about the stream: its stream, or its
// subject the stream as a whole.
func namesStream(o Open, st string) bool {
	return o.Note.Stream == st || o.Subject() == StreamSubject(st)
}

// StreamsOff is the streams off the tables that an open judgment or
// acknowledgement names (StreamOff), each once, in order.
func StreamsOff(s *Snapshot) []string {
	seen := map[string]bool{}
	var out []string
	for _, o := range append(append([]Open(nil), s.Open...), s.Acked...) {
		st, whole := strings.CutPrefix(o.Subject(), StreamSubject(""))
		for _, st := range []string{o.Note.Stream, map[bool]string{true: st}[whole]} {
			if !seen[st] && StreamOff(s, st) {
				seen[st] = true
				out = append(out, st)
			}
		}
	}
	sort.Strings(out)
	return out
}

// RetireStreams is the plan that retires every open note naming one of the
// streams taken off the tables, how in words ("removed", "archived"): each open
// judgment, acknowledgement and overdue hold on the stream as a whole or with
// the stream as its stream is closed, decided "retired: stream <s> <how>". A
// judgment of a stream no longer drawn has no next step that runs: resume,
// merge and land refuse a stream the tables lack. One happened note a stream
// names what it retired, and the plan says it on a NOTE line (Said). A stream
// with nothing open on it writes nothing. A stream still a row and stopped (an
// archived one) keeps the judgment open on it as a whole: a stopped stream has
// one (check rule 9), and resume closes it.
func RetireStreams(s *Snapshot, streams []string, how, who string) Plan {
	var p Plan
	all := append(append([]Open(nil), s.Open...), s.Acked...)
	closed := map[string]bool{}
	for _, st := range streams {
		stopped := s.Merge != nil && s.StreamCtl(st).F("state") == StreamStopped
		var ids []string
		answered := map[string]bool{}
		for _, o := range all {
			if closed[o.Key] || !namesStream(o, st) || stopped && o.Note.Kind == Judgment && o.Subject() == StreamSubject(st) {
				continue
			}
			closed[o.Key] = true
			p.Closes = append(p.Closes, o)
			if answered[o.Note.ID] {
				continue
			}
			answered[o.Note.ID] = true
			ids = append(ids, o.Note.ID)
			p.Notes = append(p.Notes, Note{Kind: Decided, Type: o.Note.Type, Stream: o.Note.Stream, Answers: o.Note.ID,
				What: "retired: stream " + st + " " + how, Who: who, At: s.Now})
		}
		if len(ids) == 0 {
			continue
		}
		sort.Strings(ids)
		what := fmt.Sprintf("stream %s %s: %d open %s retired (%s)", st, how, len(ids), map[bool]string{true: "note", false: "notes"}[len(ids) == 1], strings.Join(ids, " "))
		n := happened(NStreamRetired, st, s.Now)
		n.Who, n.What = who, what
		p.Notes = append(p.Notes, n)
		p.Said = append(p.Said, what)
	}
	return p
}

// TickRetire is the tick's retire part: the open notes of every stream off
// the tables (StreamsOff), a stream removed while one was open, retired as
// RetireStreams retires them, by the machine.
func TickRetire(s *Snapshot, r TickReq) (Plan, int) {
	return RetireStreams(s, StreamsOff(s), "is off the tables", r.who()), 0
}

// WithoutStreamsOff is the plan with no judgment raised on a stream off the
// tables (StreamOff): the tick never raises one, since none of its next steps
// would run. Every other note stays.
func WithoutStreamsOff(s *Snapshot, p Plan) Plan {
	keep := func(notes []Note) []Note {
		var out []Note
		for _, n := range notes {
			if n.Kind == Judgment && StreamOff(s, n.Stream) {
				continue
			}
			out = append(out, n)
		}
		return out
	}
	p.Notes = keep(p.Notes)
	p.Units = append([]Unit(nil), p.Units...)
	for i := range p.Units {
		p.Units[i].Notes = keep(p.Units[i].Notes)
	}
	return p
}
