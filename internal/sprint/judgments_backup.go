package sprint

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/config"
)

// Backup edges (docs/SPEC-SPRINT.md section 1, "The backup state"). One judgment
// when review becomes greater than working, and one when it is not; the same
// pair when merging becomes greater than review plus working. A tick that finds
// the predicate as it was writes nothing. An acknowledgement, and a wait whose
// time has passed, keep that episode: the seat is told again only when the
// counts cross the edge. The part is added to TickEnd, before the done part,
// and left off TickParts: the reference model walks TickParts.
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

// backupEdge raises one judgment when the predicate flips and none while it
// holds. An open note, an acknowledgement, and a wait that has run out are
// the same episode: expiry is not an edge, and it does not write the judgment
// again. now is the predicate at this tick.
func backupEdge(p *Plan, s *Snapshot, r TickReq, up, clear string, now bool, facts backupFacts, col string, stamps []string) {
	openUp, ackedUp := backupOfType(s, up)
	openClear, ackedClear := backupOfType(s, clear)
	heldUp := len(openUp)+len(ackedUp) > 0
	heldClear := len(openClear)+len(ackedClear) > 0
	raise := func(typ string) {
		what, id := backupWhat(typ, facts, s, r, col, stamps)
		p.Notes = append(p.Notes, backupNote(s, r, typ, what, id))
	}
	if now {
		if heldClear {
			backupClose(p, s, openClear)
			backupClose(p, s, ackedClear)
		}
		if heldUp {
			return
		}
		raise(up)
		return
	}
	if !heldUp {
		return
	}
	backupClose(p, s, openUp)
	backupClose(p, s, ackedUp)
	if heldClear {
		return
	}
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
	return fmt.Sprintf("%s; oldest in %s is %s; readers reading %d of width %d; lander last landed at %s; stopped streams %s; run: %s",
		head, column, oldest, facts.reading, facts.width, land, stopped, backupRecovery(s)), id
}

// backupRecovery is the judgment's run: line. Each nova-sprint command names
// the readers, members and stopped streams the snapshot holds, with the tier
// and width those rows already carry, so the verb's grammar accepts it.
// nova-sprint has no route verb: a disabled route is nova-config route set
// <name> --enabled true and nova-config apply --kind route; none disabled is
// said, not an unavailable command.
func backupRecovery(s *Snapshot) string {
	if s == nil {
		return "none"
	}
	var parts []string
	parts = append(parts, backupReaderCommands(s)...)
	parts = append(parts, backupFleetCommands(s)...)
	parts = append(parts, backupRouteCommands(s)...)
	parts = append(parts, backupResumeCommands(s)...)
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, "; ")
}

// BackupRecoveryCommands is backupRecovery's commands and the route sentence,
// in the order the judgment prints them. A test pins each command against the
// verb that would run it.
func BackupRecoveryCommands(s *Snapshot) []string { return splitRecovery(backupRecovery(s)) }

func splitRecovery(line string) []string {
	if line == "" || line == "none" {
		return nil
	}
	return strings.Split(line, "; ")
}

func backupReaderCommands(s *Snapshot) []string {
	if s == nil || s.Readers == nil {
		return nil
	}
	byTier := map[string][]string{}
	var order []string
	for _, row := range s.Readers.Rows() {
		if s.Readers.Hidden(row) || !ValidID(row) {
			continue
		}
		word := ReaderTiersShown(s.readerTiersStored(row))
		if _, err := ParseReaderTiers(word); err != nil {
			continue
		}
		if _, ok := byTier[word]; !ok {
			order = append(order, word)
		}
		byTier[word] = append(byTier[word], row)
	}
	var out []string
	for _, word := range order {
		out = append(out, "nova-sprint reader set "+strings.Join(byTier[word], " ")+" --tiers "+word)
	}
	return out
}

func backupFleetCommands(s *Snapshot) []string {
	if s == nil || s.Fleet == nil {
		return nil
	}
	var out []string
	for _, row := range s.Fleet.Rows() {
		if s.Fleet.Hidden(row) || !ValidID(row) {
			continue
		}
		out = append(out, "nova-sprint fleet up "+row+" --width "+strconv.Itoa(s.Width(row)))
	}
	return out
}

func backupRouteCommands(s *Snapshot) []string {
	if s == nil {
		return []string{"no disabled route to enable (nova-sprint has no route verb)"}
	}
	var names []string
	for _, rt := range s.Routes {
		if rt.Enabled || config.ValidateName(rt.Name) != nil {
			continue
		}
		names = append(names, rt.Name)
	}
	slices.Sort(names)
	names = slices.Compact(names)
	if len(names) == 0 {
		return []string{"no disabled route to enable (nova-sprint has no route verb)"}
	}
	var out []string
	for _, name := range names {
		out = append(out, "nova-config route set "+name+" --enabled true")
	}
	out = append(out, "nova-config apply --kind route")
	return out
}

func backupResumeCommands(s *Snapshot) []string {
	var names []string
	for _, name := range backupStopped(s) {
		if ValidID(name) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	return []string{"nova-sprint resume --stream " + strings.Join(names, ",")}
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
	if s == nil || s.Merge == nil {
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
