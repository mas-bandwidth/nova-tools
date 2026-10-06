package sprint

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// A card whose read tier, before readTierOf collapses frontier onto the tier a
// route serves, is frontier is asked of a friend of frontier class
// (docs/SPEC-SPRINT.md, a friend's card). The read card is placed on her fleet
// row. The readers table gains no friend row, and no machine route is drawn.

// FriendReadDeadline is how long the friend has, on the sprint's clock, the
// same two hours a friend's work card is judged by. It is not a wall clock.
const FriendReadDeadline = 2 * time.Hour

// FriendReadBrief is the BRIEF.md of a frontier read: WHO, the attempt's
// branch, the commit it started from and the head under review, the deadline,
// and the primary's AS A READ section through the next heading
// (docs/SPEC-SPRINT.md, a friend's card).
func FriendReadBrief(name, primary, brief, branch, start, head string, attempt int, deadline time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "WHO: friend %s\n", name)
	fmt.Fprintf(&b, "primary: %s\n", primary)
	fmt.Fprintf(&b, "attempt: %d\n", attempt)
	if branch != "" {
		fmt.Fprintf(&b, "branch: %s\n", branch)
	}
	if start != "" {
		fmt.Fprintf(&b, "start: %s\n", start)
	}
	if head != "" {
		fmt.Fprintf(&b, "head: %s\n", head)
	}
	if !deadline.IsZero() {
		fmt.Fprintf(&b, "deadline: %s\n", deadline.UTC().Format(time.RFC3339))
	}
	b.WriteString("\nAS A READ\n")
	if body := asARead(brief); body != "" {
		b.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// asARead is the primary brief's AS A READ section: the lines after that
// heading, through the next heading or the end, blank lines and indentation
// kept. A heading is a markdown heading or an uppercase section line.
func asARead(brief string) string {
	lines := strings.Split(brief, "\n")
	start := -1
	for i, l := range lines {
		if strings.TrimSpace(l) == "AS A READ" {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return ""
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if sectionHeading(lines[i]) {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

func sectionHeading(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	if strings.HasPrefix(t, "#") {
		return true
	}
	letter := false
	for _, r := range t {
		if r >= 'a' && r <= 'z' {
			return false
		}
		if r >= 'A' && r <= 'Z' {
			letter = true
		}
	}
	return letter
}

// ParseFriendReadReport reads a friend's read report. LAND closes the read
// ok. HOLD closes it broken when a line names a file, a line or a rule
// (typedrec.NamesADefect, the rule a reader's broken read is held to).
// why is set when the report does not close the read.
func ParseFriendReadReport(report string) (verdict, finding, why string) {
	word, defect := "", ""
	for _, l := range strings.Split(report, "\n") {
		t := strings.TrimSpace(l)
		if word == "" {
			if rest, ok := strings.CutPrefix(t, "Verdict:"); ok {
				fields := strings.Fields(rest)
				if len(fields) > 0 {
					word = strings.ToUpper(strings.Trim(fields[0], "*_.,;:"))
				}
			}
		}
		if defect == "" && typedrec.NamesADefect(l) {
			defect = strings.TrimSpace(l)
		}
	}
	switch word {
	case "LAND":
		return "ok", "", ""
	case "HOLD":
		if defect == "" {
			return "", "", "a HOLD closes a read broken only when a finding names a file, a line or a rule"
		}
		return "broken", defect, ""
	default:
		return "", "", "a friend's read report says Verdict: LAND or Verdict: HOLD"
	}
}

// friendReadTier is the tier a primary's read would be drawn from before
// readTierOf collapses a frontier card onto the tier a route serves
// (readTierChoice): a frontier code card, a tla/ change, a heavy card whose read
// tier is set to the one above (frontier) is frontier here; readTierOf's own
// return is left as it is.
func friendReadTier(s *Snapshot, pr *Card) string {
	t, _ := s.readTierChoice(pr)
	return t
}

func friendReadCard(s *Snapshot, pr *Card) bool {
	return pr != nil && friendReadTier(s, pr) == cardhdr.RouteFrontier
}

// attemptReadAsked says a friend read of this attempt stands: placed, or
// retired with her verdict (FriendReadClose), so the ask does not ask it
// again. A read taken back from her with no verdict is not a read: the ask
// asks the attempt of another friend (friendReadAsk).
func attemptReadAsked(s *Snapshot, pr *Card, attempt int) bool {
	prefix := pr.ID + ".r" + itoa(attempt) + "."
	for _, c := range s.Fleet.Cards() {
		if c.F("kind") == "read" && strings.HasPrefix(c.ID, prefix) && (c.Placed() || c.F("verdict") != "") {
			return true
		}
	}
	return false
}

// attemptEnds is the work card's branch and head, and the commit the attempt
// started from (the last earlier attempt that finished ok). Finish writes the
// branch on the work card, not the primary (steps_work.go).
func attemptEnds(s *Snapshot, primary string, attempt int) (branch, start, head string) {
	if s.Fleet == nil {
		return "", "", ""
	}
	if wc := s.Fleet.Card(WorkCardID(primary, attempt)); wc != nil {
		branch, head = wc.F("branch"), wc.F("head")
	}
	var earlier []*Card
	for a := 1; a < attempt; a++ {
		if w := s.Fleet.Card(WorkCardID(primary, a)); w != nil {
			earlier = append(earlier, w)
		}
	}
	return branch, BaseOf(earlier).Head, head
}

func seatDir(seats []FriendSeat, name, fallback string) string {
	for _, f := range seats {
		if f.Name == name && f.Dir != "" {
			return f.Dir
		}
	}
	return fallback
}

// FriendReadAsk asks each frontier read in review of a friend of frontier
// class, the same chooser as friendDeal (up, below her free width, most room,
// first by name), and decrements that free width as friendDeal does. dir is a
// working directory used when the seat names none; empty writes no brief (friend
// sync writes it). A friend whose read of the attempt was taken back is not
// asked it again; another friend is. With no friend of frontier class up who
// may read it, it asks no one and raises the one judgment a read with no
// reader up already raises (NFewReaders); with one up at her room the read
// waits for her room, as a machine read waits for a reader's (friendReadAsk).
func FriendReadAsk(s *Snapshot, seats []FriendSeat, dir string) (Plan, error) {
	p, _, _, err := friendReadAsk(s, seats, dir)
	return p, err
}

// friendReadAsk is FriendReadAsk with the frontier primaries it left waiting
// for a friend's room (a friend of frontier class is up who may read the
// attempt, and every such friend is at her room), which the tick's ask records
// as waiting for a reader and counts due (friendAskPart), and few: a primary
// has no friend up who may read it, the readers' judgment's condition.
func friendReadAsk(s *Snapshot, seats []FriendSeat, dir string) (p Plan, waits []*Card, few bool, err error) {
	if s == nil || s.Work == nil || s.Fleet == nil {
		return p, nil, false, nil
	}
	free, lanes := map[string]int{}, map[string]int{}
	var up []FriendSeat
	for _, f := range seats {
		if f.Status != Up || !slices.Contains(f.Tiers, cardhdr.RouteFrontier) {
			continue
		}
		room, width := DealAhead*f.Width, f.Width
		if f.Mode == config.FriendModeOneShot {
			room, width = 1, 1
		}
		free[f.Name] = room - friendLoad(s, f.Name)
		lanes[f.Name] = width - s.Fleet.Count(FriendRow(f.Name), Working)
		up = append(up, f)
	}
	slices.SortFunc(up, func(a, b FriendSeat) int { return strings.Compare(a.Name, b.Name) })
	declared := map[string]bool{}
	for _, pr := range s.Work.Column(Review) {
		if !friendReadCard(s, pr) || IsSentinel(pr) || pr.F("result") == "failed" {
			continue
		}
		attempt := pr.Int("attempt")
		if attempt == 0 {
			attempt = 1
		}
		if attemptReadAsked(s, pr, attempt) {
			continue
		}
		// a friend is asked an attempt once: her read card at it, taken back,
		// keeps its id
		name, eligible := "", false
		for _, f := range up {
			if s.Fleet.Card(ReadCardID(pr.ID, attempt, f.Name)) != nil {
				continue
			}
			eligible = true
			if free[f.Name] > 0 && (name == "" || free[f.Name] > free[name]) {
				name = f.Name
			}
		}
		switch {
		case !eligible:
			few = true
			continue
		case name == "":
			waits = append(waits, pr)
			continue
		}
		if err := askOneFriend(&p, s, pr, seats, name, dir, attempt, free, lanes, declared); err != nil {
			return Plan{}, nil, false, err
		}
	}
	if few {
		// the existing sprint-level judgment, once, not one rewritten note per primary
		notify(&p, s, []cond{{typ: NFewReaders, streamLevel: true, what: fewReaders(s)}}, []string{NFewReaders}, TickReq{})
	}
	return Lawful(p), waits, few, nil
}

func askOneFriend(p *Plan, s *Snapshot, pr *Card, seats []FriendSeat, name, dir string, attempt int, free, lanes map[string]int, declared map[string]bool) error {
	id := ReadCardID(pr.ID, attempt, name)
	if !ValidCardID(id) {
		p.refuse(pr.ID, "the read card id is not one a job directory may be named by")
		return nil
	}
	if s.Fleet.Card(id) != nil {
		p.refuse(pr.ID, "read card "+id+" exists already")
		return nil
	}
	free[name]--
	col := Ready
	if lanes[name] > 0 {
		lanes[name]--
		col = Working
	}
	branch, start, head := attemptEnds(s, pr.ID, attempt)
	if d := seatDir(seats, name, dir); d != "" {
		text := FriendReadBrief(name, pr.ID, pr.F("brief"), branch, start, head, attempt, s.Now.Add(FriendReadDeadline))
		in := filepath.Join(d, "inbox", id)
		if err := os.MkdirAll(in, 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(in, "BRIEF.md"), []byte(text), 0o644); err != nil {
			return err
		}
	}
	row := FriendRow(name)
	if !s.Fleet.HasRow(row) && !declared[row] {
		p.Rows = append(p.Rows, RowAdd{Fleet, row})
		declared[row] = true
	}
	fields := map[string]string{
		"kind": "read", "primary": pr.ID, "stream": pr.Row, "reader": name,
		"attempt": itoa(attempt), "head": head, "branch": branch, "start": start,
		"asked": stamp(s.Now),
	}
	p.Units = append(p.Units, Unit{Key: pr.ID, Stream: pr.Row, Changes: []Change{
		change(Fleet, createEntry(id, row, col, pr.Score, fields)),
	}, Moved: pr.ID + " asked of friend " + name})
	return nil
}

// FriendReadClose applies one friend's read report to the read card on her
// fleet row. LAND and HOLD retire that card (it is not moved to a fleet
// column the fleet does not use, and not onto a readers column). HOLD with a
// finding raises the same broken-read judgment Read raises. The readers table
// gains no row.
func FriendReadClose(s *Snapshot, name, primary, report string) Plan {
	var p Plan
	pr := s.Work.Card(primary)
	if pr == nil {
		p.refuse(primary, "no such primary")
		return p
	}
	attempt := pr.Int("attempt")
	if attempt == 0 {
		attempt = 1
	}
	id := ReadCardID(primary, attempt, name)
	rc := s.Fleet.Card(id)
	if rc == nil || !rc.Placed() {
		p.refuse(primary, "no frontier read asked of "+name)
		return p
	}
	verdict, finding, why := ParseFriendReadReport(report)
	if why != "" {
		p.refuse(primary, why)
		return p
	}
	set := map[string]string{"verdict": verdict, "read": stamp(s.Now), "retired": stamp(s.Now), "retired_by": "read"}
	var notes []Note
	if verdict == "broken" {
		set["finding"] = finding
		n := judgment(NReadBroken, pr.Row, s.Now, pr.Int("broken_reads"), pr.ID)
		n.Who, n.Attempt, n.What = name, attempt, finding
		notes = append(notes, n)
	}
	if j, ok := reviewJudgment(s, pr, reviewStep{writes: notes, who: name}); ok {
		notes = append(notes, j)
	}
	p.Units = append(p.Units, Unit{Key: primary, Stream: pr.Row, Changes: []Change{
		change(Fleet, removeEntry(rc, set)),
	}, Moved: id + " retired " + verdict, Notes: notes})
	return p
}

// The tick's ask part lives in steps_tick.go, which this change does not edit.
// The part the machine runs is the function TickTables holds, so the friend
// ask is installed there: a frontier read is asked before a machine route is
// drawn, and the machine ask does not see that primary.
func init() {
	wrapped := friendAskPart(TickAsk)
	for i := range TickTables {
		for j := range TickTables[i].Parts {
			if TickTables[i].Parts[j].Name == "ask" && TickTables[i].Parts[j].Fn != nil {
				TickTables[i].Parts[j].Fn = wrapped
			}
		}
	}
	for i := range TickParts {
		if TickParts[i].Name == "ask" {
			TickParts[i].Fn = wrapped
		}
	}
	// function values are not comparable; the pointer is the same function TickAsk
	ask := reflect.ValueOf(TickAsk).Pointer()
	for i := range heldParts {
		if heldParts[i] != nil && reflect.ValueOf(heldParts[i]).Pointer() == ask {
			heldParts[i] = wrapped
		}
	}
}

// friendAskPart is the tick's ask: the friend ask, then the machine ask with
// the frontier reads hidden from it, in one plan. A card in review is asked in
// the tick when a reader it may be asked of has room; one that waits for room
// is recorded waiting for a reader (waitingForReader) and counted due, so the
// no-stall rule holds it and the next tick asks it. The seats are the tick's;
// a part that asks what the ask does with none (the no-stall rule's) plans on
// the snapshot's, and writes no brief.
func friendAskPart(machine TickPartFn) TickPartFn {
	return func(s *Snapshot, r TickReq) (Plan, int) {
		seats := r.Friends
		if seats == nil {
			seats = withoutDirs(s.Friends)
		}
		fp, waits, few, err := friendReadAsk(s, seats, "")
		restore := hideFriendReadPrimaries(s)
		mp, due := machine(s, r)
		restore()
		if err != nil {
			mp.refuse("ask", err.Error())
		}
		if few {
			// the friends' condition holds: the machine's ask, whose readers are
			// up, does not close the one judgment (or write it twice)
			mp.Closes = slices.DeleteFunc(mp.Closes, func(o Open) bool { return o.Note.Type == NFewReaders })
			if slices.ContainsFunc(mp.Notes, func(n Note) bool { return n.Type == NFewReaders }) {
				fp.Notes = slices.DeleteFunc(fp.Notes, func(n Note) bool { return n.Type == NFewReaders })
			}
		}
		mp.Rows = append(fp.Rows, mp.Rows...)
		mp.Units = append(fp.Units, mp.Units...)
		mp.Refused = append(mp.Refused, fp.Refused...)
		mp.Notes = append(mp.Notes, fp.Notes...)
		mp.Closes = append(mp.Closes, fp.Closes...)
		waitingForReader(&mp, s, waits)
		return mp, due + len(waits)
	}
}

// withoutDirs is the seats with no working directory: a plan made on them
// writes no friend's inbox.
func withoutDirs(seats []FriendSeat) []FriendSeat {
	if seats == nil {
		return nil
	}
	out := slices.Clone(seats)
	for i := range out {
		out[i].Dir = ""
	}
	return out
}

// waitingForReader records on each primary in review the ask left waiting for a
// reader with room, once an attempt, a happened note with it (NWaitingForReader,
// FieldWaitingReader), and clears the mark on each primary the plan asks. A
// primary waits when the machine ask could have asked it (its work did not fail,
// it wants a read, as many readers of its tier are up as it needs) and neither
// asked it nor refused it: the ask refused is the coordinator's judgment (cannot
// ask), and fewer readers up than it needs is the readers' (NFewReaders). The
// frontier reads waiting for a friend's room are friends (friendReadAsk). The
// mark is a work-table field the pump applies, as the ask's asked field is.
func waitingForReader(p *Plan, s *Snapshot, friends []*Card) {
	asked := map[string]int{} // the unit of each primary the plan asks
	for i, u := range p.Units {
		if s.Work.Placed(u.Key) != nil {
			asked[u.Key] = i
		}
	}
	judged := map[string]bool{} // the primaries the plan judges
	for _, n := range p.Notes {
		if n.Kind == Judgment {
			for _, id := range n.Primaries {
				judged[id] = true
			}
		}
	}
	waits := map[string]string{}
	for _, c := range friends {
		waits[c.ID] = "every friend of frontier class up who may read it is at her room"
	}
	for _, c := range s.Work.Column(Review) {
		if _, ok := asked[c.ID]; ok || judged[c.ID] || friendReadCard(s, c) || c.F("result") == "failed" ||
			ReadsWanted(s, c) == 0 || !enoughReadersUp(s, c) || len(closesFor(s.Open, []string{NCannotAsk}, c.ID)) > 0 {
			continue
		}
		waits[c.ID] = "no reader of its tier up has room this tick"
	}
	var marks []Unit
	for _, c := range s.Work.Column(Review) {
		attempt := c.Int("attempt")
		if attempt == 0 {
			attempt = 1
		}
		if i, ok := asked[c.ID]; ok {
			if c.F(FieldWaitingReader) != "" {
				p.Units[i].Changes = unsetOn(p.Units[i].Changes, c, FieldWaitingReader)
			}
			continue
		}
		why, ok := waits[c.ID]
		if !ok || waitingAt(c) == attempt {
			continue
		}
		n := happened(NWaitingForReader, c.Row, s.Now, c.ID)
		n.Attempt = attempt
		n.What = c.ID + " waits for a reader at attempt " + itoa(attempt) + ": " + why + "; the tick asks it when one has room (readers: " + readersText(s) + ")"
		marks = append(marks, Unit{Key: c.ID, Stream: c.Row,
			Changes: []Change{change(Work, setEntry(c, map[string]string{FieldWaitingReader: itoa(attempt) + " " + stamp(s.Now)}))},
			Moved:   c.ID + " waiting for a reader (" + why + ")", Notes: []Note{n}})
	}
	p.Units = append(p.Units, marks...)
}

// waitingAt is the attempt the primary's waiting mark was written at, 0 when
// it has none.
func waitingAt(c *Card) int {
	v, _, _ := strings.Cut(c.F(FieldWaitingReader), " ")
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}

// unsetOn unsets the field of the card in the unit's work change of it, or
// adds that change when the unit has none.
func unsetOn(changes []Change, c *Card, field string) []Change {
	for i, ch := range changes {
		if ch.Table == Work && ch.Entry.ID == c.ID {
			if !slices.Contains(ch.Entry.Unset, field) {
				changes[i].Entry.Unset = append(slices.Clone(ch.Entry.Unset), field)
			}
			return changes
		}
	}
	return append(changes, change(Work, setEntry(c, nil, field)))
}

// hideFriendReadPrimaries takes frontier reads out of review for the machine
// ask and puts them back. The plan is built against the restored places.
func hideFriendReadPrimaries(s *Snapshot) func() {
	if s == nil || s.Work == nil {
		return func() {}
	}
	type saved struct {
		c   *Card
		col string
	}
	var held []saved
	for _, c := range s.Work.Column(Review) {
		if friendReadCard(s, c) {
			held = append(held, saved{c, c.Col})
			c.Col = ""
		}
	}
	if len(held) > 0 {
		s.Work.cells, s.Work.byPrimary = nil, nil
	}
	return func() {
		for _, h := range held {
			h.c.Col = h.col
		}
		if len(held) > 0 {
			s.Work.cells, s.Work.byPrimary = nil, nil
		}
	}
}
