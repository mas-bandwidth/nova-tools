package sprint

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Backup edges (docs/SPEC-SPRINT.md section 1, "The backup state"). One judgment
// when review becomes greater than working, and one when it is not; the same
// pair when merging becomes greater than review plus working. A tick that finds
// the predicate as it was writes nothing, and an acknowledgement holds the
// judgment until a wait on it is due. The part is added to TickEnd, before the
// done part, and left off TickParts: the reference model walks TickParts.
const (
	NReadsBackedUp  = "reads are backed up"
	NReadsClear     = "reads are clear"
	NMergesBackedUp = "merges are backed up"
	NMergesClear    = "merges are clear"
	PartBackup      = "backup"
)

func init() {
	for _, typ := range []string{NReadsBackedUp, NReadsClear, NMergesBackedUp, NMergesClear} {
		TickDecisions[typ] = []string{"ack", "wait"}
	}
	end := make([]TickPartDef, 0, len(TickEnd)+1)
	placed := false
	for _, p := range TickEnd {
		if !placed && p.Name == PartDone {
			end = append(end, TickPartDef{PartBackup, TickBackup})
			placed = true
		}
		end = append(end, p)
	}
	if !placed {
		end = append(end, TickPartDef{PartBackup, TickBackup})
	}
	TickEnd = end
}

// TickBackup is the tick's backup part: the two edges, one judgment each.
func TickBackup(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	if s == nil || s.Work == nil {
		return p, 0
	}
	working, review, merging := backupCounts(s)
	reading, width := backupReaders(s)
	landed := backupLanded(s)
	stopped := backupStopped(s)
	facts := backupFacts{working: working, review: review, merging: merging, reading: reading, width: width, landed: landed, stopped: stopped}
	backupEdge(&p, s, r, NReadsBackedUp, NReadsClear, review > working, facts, Review, []string{FieldFinishedAt, "admitted"})
	backupEdge(&p, s, r, NMergesBackedUp, NMergesClear, merging > review+working, facts, Merging, []string{"accepted"})
	return p, 0
}

type backupFacts struct {
	working, review, merging int
	reading, width           int
	landed                   string
	stopped                  []string
}

// backupEdge raises one judgment when the predicate changes and none while a
// judgment of that side is still held. now is the predicate at this tick.
func backupEdge(p *Plan, s *Snapshot, r TickReq, up, clear string, now bool, facts backupFacts, col string, stamps []string) {
	openUp, ackedUp := backupOfType(s, up)
	openClear, ackedClear := backupOfType(s, clear)
	liveUp, deadUp := backupSplit(s, r, ackedUp)
	liveClear, deadClear := backupSplit(s, r, ackedClear)
	raise := func(typ string) {
		what, id := backupWhat(typ, facts, s, r, col, stamps)
		p.Notes = append(p.Notes, backupNote(s, r, typ, what, id))
	}
	if now {
		backupClose(p, s, openClear)
		backupClose(p, s, ackedClear)
		if len(openUp) > 0 || len(liveUp) > 0 {
			backupClose(p, s, deadUp)
			return
		}
		backupClose(p, s, deadUp)
		raise(up)
		return
	}
	if len(openUp)+len(liveUp)+len(deadUp) == 0 {
		if len(openClear) == 0 && len(liveClear) == 0 && len(deadClear) > 0 {
			backupClose(p, s, deadClear)
			raise(clear)
		}
		return
	}
	backupClose(p, s, openUp)
	backupClose(p, s, ackedUp)
	if len(openClear) > 0 || len(liveClear) > 0 {
		backupClose(p, s, deadClear)
		return
	}
	backupClose(p, s, deadClear)
	raise(clear)
}

// backupClose closes these notes and the overdue holds that name them. The
// overdue part has already run this tick, so a hold left open would be a note
// on the tick after the judgment closed.
func backupClose(p *Plan, s *Snapshot, notes []Open) {
	if len(notes) == 0 {
		return
	}
	p.Closes = append(p.Closes, notes...)
	ids := map[string]bool{}
	seen := map[string]bool{}
	for _, o := range notes {
		if o.Note.ID != "" {
			ids[o.Note.ID] = true
		}
	}
	for _, c := range p.Closes {
		if c.Note.Type == NOverdue {
			seen[c.Key] = true
		}
	}
	for _, o := range s.Acked {
		if o.Note.Type == NOverdue && ids[o.Note.What] && !seen[o.Key] {
			p.Closes = append(p.Closes, o)
			seen[o.Key] = true
		}
	}
}

func backupOfType(s *Snapshot, typ string) (open, acked []Open) {
	for _, o := range s.Open {
		if o.Note.Kind == Judgment && o.Note.Type == typ {
			open = append(open, o)
		}
	}
	for _, o := range s.Acked {
		if o.Note.Type == typ {
			acked = append(acked, o)
		}
	}
	return open, acked
}

func backupSplit(s *Snapshot, r TickReq, acked []Open) (live, dead []Open) {
	for _, o := range acked {
		if backupHeld(s, r, o) {
			live = append(live, o)
		} else {
			dead = append(dead, o)
		}
	}
	return live, dead
}

func backupHeld(s *Snapshot, r TickReq, o Open) bool {
	if o.Note.Review.IsZero() {
		return true
	}
	base := o.Note.ReviewSet
	if base.IsZero() {
		base = o.Note.At
	}
	return !DueNow(s.Now, o.Note.Review, base, r.Stopped)
}

func backupNote(s *Snapshot, r TickReq, typ, what, oldest string) Note {
	n := Note{Kind: Judgment, Type: typ, StreamLevel: true, Marked: true, At: s.Now, Who: r.who(), What: what,
		Decisions: append([]string(nil), TickDecisions[typ]...)}
	if oldest != "" {
		n.Primaries = []string{oldest}
		n.Count = 1
	}
	return n
}

func backupWhat(typ string, facts backupFacts, s *Snapshot, r TickReq, col string, stamps []string) (string, string) {
	id, text, stamped := oldestBackup(s, col, stamps)
	age := "unknown"
	if stamped {
		if d, ok := r.running(s.Now, text); ok {
			age = TookText(d)
		}
	}
	oldest := "none"
	if id != "" {
		oldest = id + ", age " + age
	}
	land := "none"
	if facts.landed != "" {
		land = facts.landed
	}
	stopped := "none"
	if len(facts.stopped) > 0 {
		stopped = strings.Join(facts.stopped, ", ")
	}
	var head, column string
	switch typ {
	case NReadsBackedUp:
		column = "review"
		head = fmt.Sprintf("review %d is above working %d, merging %d", facts.review, facts.working, facts.merging)
	case NReadsClear:
		column = "review"
		head = fmt.Sprintf("review %d is not above working %d, merging %d", facts.review, facts.working, facts.merging)
	case NMergesBackedUp:
		column = "merging"
		head = fmt.Sprintf("merging %d is above review %d plus working %d", facts.merging, facts.review, facts.working)
	default:
		column = "merging"
		head = fmt.Sprintf("merging %d is not above review %d plus working %d", facts.merging, facts.review, facts.working)
	}
	return fmt.Sprintf("%s; oldest in %s is %s; readers reading %d of width %d; lander last landed at %s; stopped streams %s; run: nova-sprint reader set --tiers; nova-sprint fleet up --width; nova-sprint route enable; nova-sprint resume --stream",
		head, column, oldest, facts.reading, facts.width, land, stopped), id
}

func backupCounts(s *Snapshot) (working, review, merging int) {
	for _, c := range s.Work.Column(Working, Review, Merging) {
		if IsSentinel(c) || s.Work.Hidden(c.Row) {
			continue
		}
		switch c.Col {
		case Working:
			working++
		case Review:
			review++
		case Merging:
			merging++
		}
	}
	return working, review, merging
}

func oldestBackup(s *Snapshot, col string, fields []string) (id, text string, stamped bool) {
	if s == nil || s.Work == nil {
		return "", "", false
	}
	var best *Card
	var bestAt time.Time
	var bestText string
	var bestStamped bool
	for _, c := range s.Work.Column(col) {
		if IsSentinel(c) || s.Work.Hidden(c.Row) {
			continue
		}
		at, raw, ok := backupStamp(c, fields)
		if best == nil || backupBefore(c, at, ok, best, bestAt, bestStamped) {
			best, bestAt, bestText, bestStamped = c, at, raw, ok
		}
	}
	if best == nil {
		return "", "", false
	}
	return best.ID, bestText, bestStamped
}

func backupStamp(c *Card, fields []string) (time.Time, string, bool) {
	for _, f := range fields {
		raw := c.F(f)
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			continue
		}
		return t, raw, true
	}
	return time.Time{}, "", false
}

func backupBefore(c *Card, at time.Time, stamped bool, best *Card, bestAt time.Time, bestStamped bool) bool {
	if stamped != bestStamped {
		return stamped
	}
	if stamped && !at.Equal(bestAt) {
		return at.Before(bestAt)
	}
	return c.ID < best.ID
}

func backupReaders(s *Snapshot) (reading, width int) {
	if s.Readers == nil {
		return 0, 0
	}
	reading = len(s.Readers.Column(Reading))
	for _, row := range s.Readers.Rows() {
		w := s.ReaderWidth(row)
		if w == math.MaxInt {
			continue
		}
		width += w
	}
	return reading, width
}

func backupLanded(s *Snapshot) string {
	if s.Work == nil {
		return ""
	}
	var best time.Time
	var text string
	for _, c := range s.Work.Column(Landed) {
		if IsSentinel(c) || s.Work.Hidden(c.Row) {
			continue
		}
		raw := c.F("landed")
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			continue
		}
		if text == "" || t.After(best) {
			best, text = t, raw
		}
	}
	return text
}

func backupStopped(s *Snapshot) []string {
	if s.Merge == nil {
		return nil
	}
	var names []string
	for _, row := range s.Merge.Rows() {
		if s.Merge.Hidden(row) {
			continue
		}
		if s.StreamCtl(row).F("state") == StreamStopped {
			names = append(names, row)
		}
	}
	return names
}
