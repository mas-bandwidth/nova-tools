package sprint

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
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
// readTierOf collapses a frontier card onto the tier a route serves. A heavy
// card whose read tier is set to the one above (frontier) is frontier here;
// readTierOf's own return is left as it is.
func friendReadTier(s *Snapshot, pr *Card) string {
	m, _ := cardhdr.ReadModel(pr.F("brief"))
	t := cardTier(pr, m)
	set := s.readTierSetting(pr.Row)
	if set == "" {
		return t
	}
	ladder := []string{cardhdr.RouteFlash, cardhdr.RoutePro, "heavy", cardhdr.RouteFrontier}
	if slices.Index(ladder, set) > slices.Index(ladder, t) {
		return set
	}
	return t
}

func friendReadCard(s *Snapshot, pr *Card) bool {
	return pr != nil && friendReadTier(s, pr) == cardhdr.RouteFrontier
}

// attemptReadAsked says a friend read of this attempt was already placed or
// retired, so the ask does not ask it again.
func attemptReadAsked(s *Snapshot, pr *Card, attempt int) bool {
	prefix := pr.ID + ".r" + itoa(attempt) + "."
	for _, c := range s.Fleet.Cards() {
		if c.F("kind") == "read" && strings.HasPrefix(c.ID, prefix) {
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
// class, the same chooser as FriendDeal (up, below her free width, most room,
// first by name), and decrements that free width as FriendDeal does. dir is a
// working directory used when the seat names none; empty writes no brief (friend
// sync writes it). With no friend up with room it asks no one and raises the
// one judgment a read with no reader up already raises (NFewReaders).
func FriendReadAsk(s *Snapshot, seats []FriendSeat, dir string) (Plan, error) {
	var p Plan
	if s == nil || s.Work == nil || s.Fleet == nil {
		return p, nil
	}
	free, lanes := map[string]int{}, map[string]int{}
	var up []FriendSeat
	for _, f := range seats {
		if f.Status != Up || !slices.Contains(f.Tiers, cardhdr.RouteFrontier) {
			continue
		}
		free[f.Name] = DealAhead*f.Width - friendLoad(s, f.Name)
		lanes[f.Name] = f.Width - s.Fleet.Count(FriendRow(f.Name), Working)
		up = append(up, f)
	}
	slices.SortFunc(up, func(a, b FriendSeat) int { return strings.Compare(a.Name, b.Name) })
	declared := map[string]bool{}
	waiting := false
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
		name := ""
		for _, f := range up {
			if free[f.Name] > 0 && (name == "" || free[f.Name] > free[name]) {
				name = f.Name
			}
		}
		if name == "" {
			waiting = true
			continue
		}
		if err := askOneFriend(&p, s, pr, seats, name, dir, attempt, free, lanes, declared); err != nil {
			return Plan{}, err
		}
	}
	if waiting {
		// the existing sprint-level judgment, once, not one rewritten note per primary
		notify(&p, s, []cond{{typ: NFewReaders, streamLevel: true, what: fewReaders(s)}}, []string{NFewReaders}, TickReq{})
	}
	return Lawful(p), nil
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

func friendAskPart(machine TickPartFn) TickPartFn {
	return func(s *Snapshot, r TickReq) (Plan, int) {
		fp, err := FriendReadAsk(s, r.Friends, "")
		restore := hideFriendReadPrimaries(s)
		defer restore()
		mp, due := machine(s, r)
		if err != nil {
			mp.refuse("ask", err.Error())
		}
		mp.Rows = append(fp.Rows, mp.Rows...)
		mp.Units = append(fp.Units, mp.Units...)
		mp.Refused = append(mp.Refused, fp.Refused...)
		mp.Notes = append(mp.Notes, fp.Notes...)
		mp.Closes = append(mp.Closes, fp.Closes...)
		return mp, due
	}
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
