package sprint

import (
	"fmt"
	"slices"
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
// its control card's record is kept unplaced. A removal is not a tombstone:
// a card added under the stream's name brings it back (ComeBack). The add
// step reads the record as an extra (store.AddStep).
func RemovedStream(s *Snapshot, stream string) bool {
	ctl := s.Merge.Card(CtlID(stream))
	return ctl != nil && !ctl.Placed()
}

// ComeBack is a removed stream's control card as the place that brings it
// back leaves it (on its merge row's control cell, its revision bumped, its
// fields as the removal left them), and that place: the add that names the
// stream adds its rows and places the record again before its manifests
// (Plan.Places), and says so, a NOTE line. A batch never places a removed
// member, so the record is placed by the table layer's cell add, as a fleet
// member's control card is when it rejoins (store.RejoinMembers). Stream
// archive is the verb that takes landed streams off the table and keeps
// their rows; stream remove takes off a stream that holds no card, and its
// name is free for an add at once.
func ComeBack(ctl *Card, stream string) (*Card, PlaceAgain) {
	back := *ctl
	back.Row, back.Col, back.Score = stream, Ctl, 0
	back.Rev++
	return &back, PlaceAgain{Table: Merge, Row: stream, Col: Ctl, ID: ctl.ID,
		Said: "stream " + stream + " was removed in this epoch and comes back: its control card is placed again"}
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

// NamedStream is the work stream a note names: its stream, when that is a
// stream's name and no other subject's (a member's, a provider's and a tier's
// subjects hold a colon, as no stream name does); "" when it names none.
func NamedStream(n Note) string {
	if n.Stream == "" || strings.Contains(n.Stream, ":") {
		return ""
	}
	return n.Stream
}

// StreamGone says the stream is a row of neither the work nor the merge
// table: stream remove took it off, and nothing the tick or the coordinator
// does can act on it in this epoch. An archived stream's rows are kept,
// hidden, and it is not gone.
func StreamGone(s *Snapshot, stream string) bool {
	return s.Work != nil && s.Merge != nil && !s.Work.HasRow(stream) && !s.Merge.HasRow(stream)
}

// RetireStreams is the plan that retires every open judgment and every
// acknowledged condition (an alarm held, an overdue hold, a stale stream's)
// that names one of the streams (NamedStream): each is closed, its decided
// note saying why, and the step says so, a NOTE line each, naming it by its
// alias and id. Its next step named a stream that is no longer there to act
// on (nova-sprint resume --stream refuses "no such stream"), so it would wait
// in the inbox and count against the coordinator for good.
func RetireStreams(s *Snapshot, streams []string, why string) Plan {
	var p Plan
	said := map[string]bool{}
	for _, o := range append(append([]Open(nil), s.Open...), s.Acked...) {
		st := NamedStream(o.Note)
		if st == "" || !contains(streams, st) {
			continue
		}
		p.Closes = append(p.Closes, o)
		if said[o.Note.ID] {
			continue
		}
		said[o.Note.ID] = true
		w := "retired: stream " + st + " " + why
		p.Notes = append(p.Notes, Note{Kind: Decided, Type: o.Note.Type, Stream: st, Answers: o.Note.ID, What: w, Who: s.Actor, At: s.Now})
		name := o.Note.ID
		if o.Note.Alias != "" {
			name = o.Note.Alias + " " + name
		}
		p.Said = append(p.Said, fmt.Sprintf("%s %s (%s) %s", map[bool]string{true: "judgment", false: "held condition"}[o.Note.Kind == Judgment], name, o.Note.Type, w))
	}
	return p
}

// WhyGone is the retirement's reason the tick gives for a stream the work and
// merge tables lack.
const WhyGone = "is on neither the work nor the merge table (stream remove took it off), so nothing can act on this"

// PartRetire is the name of the tick's retirement part (TickRetireGone).
const PartRetire = "retire gone streams"

// TickRetireGone is the tick's retirement of what names a stream the tables
// lack (StreamGone): every open judgment and acknowledged condition naming one
// is closed, with a note (RetireStreams). Stream remove and stream archive
// retire their own as they take the stream off; this part retires what was
// open before they did, and anything left behind by a remove whose own
// retirement did not finish.
func TickRetireGone(s *Snapshot, r TickReq) (Plan, int) {
	var gone []string
	for _, o := range append(append([]Open(nil), s.Open...), s.Acked...) {
		if st := NamedStream(o.Note); st != "" && !contains(gone, st) && StreamGone(s, st) {
			gone = append(gone, st)
		}
	}
	if len(gone) == 0 {
		return Plan{}, 0
	}
	sort.Strings(gone)
	p := RetireStreams(s, gone, WhyGone)
	for i := range p.Notes {
		p.Notes[i].Who = r.who()
	}
	return p, 0
}

// ForTables is a tick part's plan with no judgment and no acknowledged
// condition raised or rewritten for a stream the tables lack (StreamGone)
// and the plan does not add a row for: its next step could only be refused.
func ForTables(s *Snapshot, p Plan) Plan {
	adds := map[string]bool{}
	for _, r := range p.Rows {
		if r.Table == Work || r.Table == Merge {
			adds[r.Row] = true
		}
	}
	gone := func(n Note) bool {
		if n.Kind != Judgment && n.Kind != Acknowledged {
			return false
		}
		st := NamedStream(n)
		return st != "" && !adds[st] && StreamGone(s, st)
	}
	keep := func(notes []Note) []Note {
		if !slices.ContainsFunc(notes, gone) {
			return notes
		}
		return slices.DeleteFunc(slices.Clone(notes), gone)
	}
	p.Notes, p.Updates = keep(p.Notes), keep(p.Updates)
	if slices.ContainsFunc(p.Units, func(u Unit) bool { return slices.ContainsFunc(u.Notes, gone) }) {
		units := slices.Clone(p.Units)
		for i := range units {
			units[i].Notes = keep(units[i].Notes)
		}
		p.Units = units
	}
	return p
}
