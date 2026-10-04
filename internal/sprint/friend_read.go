package sprint

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// A frontier card's read (docs/SPEC-SPRINT.md, a friend's card): the tier above
// heavy, which no route serves, is asked of a friend whose tiers include
// frontier, not of a reader machine. The read card is placed on her fleet row.
// The readers table gains no friend row.

// friendReadDeadline is how long the friend has, on the sprint's clock, the
// same two hours a friend's work card is judged by. It is not a wall clock.
const friendReadDeadline = 2 * time.Hour

// FriendReadBrief is the BRIEF.md of a frontier read: WHO, the attempt's
// branch and its start commit, the deadline, and the primary's AS A READ
// section (docs/SPEC-SPRINT.md, a friend's card).
func FriendReadBrief(name, primary, brief, branch, head string, attempt int, deadline time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "WHO: friend %s\n", name)
	fmt.Fprintf(&b, "primary: %s\n", primary)
	fmt.Fprintf(&b, "attempt: %d\n", attempt)
	if branch != "" {
		fmt.Fprintf(&b, "branch: %s\n", branch)
	}
	if head != "" {
		fmt.Fprintf(&b, "start: %s\n", head)
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
// heading, up to the next blank line.
func asARead(brief string) string {
	var on bool
	var lines []string
	for _, l := range strings.Split(brief, "\n") {
		if strings.TrimSpace(l) == "AS A READ" {
			on = true
			continue
		}
		if !on {
			continue
		}
		if strings.TrimSpace(l) == "" {
			if len(lines) > 0 {
				break
			}
			continue
		}
		lines = append(lines, strings.TrimSpace(l))
	}
	return strings.Join(lines, "\n")
}

// ParseFriendReadReport reads a friend's read report. LAND closes the read
// ok. HOLD closes it broken when a line names a file, a line or a rule
// (typedrec.NamesADefect, the rule a reader's broken read is held to).
// why is set when the report does not close the read.
func ParseFriendReadReport(report string) (verdict, finding, why string) {
	word := ""
	defect := ""
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

// friendForFrontierRead is the friend a frontier read is asked of: up, below
// her width, her tiers include frontier, the most free width, the first by
// name among equals (friend_deal.go FriendDeal's chooser, plus the class).
func friendForFrontierRead(s *Snapshot, seats []FriendSeat) (string, bool) {
	best, bestFree := "", 0
	names := append([]FriendSeat(nil), seats...)
	slices.SortFunc(names, func(a, b FriendSeat) int { return strings.Compare(a.Name, b.Name) })
	for _, f := range names {
		if f.Status != Up || !slices.Contains(f.Tiers, cardhdr.RouteFrontier) {
			continue
		}
		free := f.Width - friendLoad(s, f.Name)
		if free <= 0 {
			continue
		}
		if best == "" || free > bestFree {
			best, bestFree = f.Name, free
		}
	}
	return best, best != ""
}

func frontierCard(pr *Card) bool {
	m, bad := cardhdr.ReadModel(pr.F("brief"))
	return bad == "" && cardTier(pr, m) == cardhdr.RouteFrontier
}

// FriendReadAsk asks each frontier primary in review of one friend and writes
// her inbox/<read-card>/BRIEF.md. With no friend up with room it asks no one
// and raises the judgment a read with no reader up already raises
// (NFewReaders). dir is her working directory (a test passes a fake one).
func FriendReadAsk(s *Snapshot, seats []FriendSeat, dir string) (Plan, error) {
	var p Plan
	name, up := friendForFrontierRead(s, seats)
	for _, pr := range s.Work.Column(Review) {
		if !frontierCard(pr) {
			continue
		}
		if !up {
			n := judgment(NFewReaders, pr.Row, s.Now, 0, pr.ID)
			n.What = fewReaders(s) + "; no friend whose tiers include frontier is up with room"
			p.Notes = append(p.Notes, n)
			continue
		}
		attempt := pr.Int("attempt")
		if attempt == 0 {
			attempt = 1
		}
		id := ReadCardID(pr.ID, attempt, name)
		if !ValidCardID(id) {
			p.refuse(pr.ID, "the read card id is not one a job directory may be named by")
			continue
		}
		if s.Fleet.Card(id) != nil {
			p.refuse(pr.ID, "read card "+id+" exists already")
			continue
		}
		deadline := s.Now.Add(friendReadDeadline)
		text := FriendReadBrief(name, pr.ID, pr.F("brief"), pr.F("branch"), pr.F("head"), attempt, deadline)
		in := filepath.Join(dir, "inbox", id)
		if err := os.MkdirAll(in, 0o755); err != nil {
			return Plan{}, err
		}
		if err := os.WriteFile(filepath.Join(in, "BRIEF.md"), []byte(text), 0o644); err != nil {
			return Plan{}, err
		}
		row := FriendRow(name)
		if !s.Fleet.HasRow(row) {
			p.Rows = append(p.Rows, RowAdd{Fleet, row})
		}
		fields := map[string]string{
			"kind": "read", "primary": pr.ID, "stream": pr.Row, "reader": name,
			"attempt": itoa(attempt), "head": pr.F("head"), "branch": pr.F("branch"),
			"asked": stamp(s.Now),
		}
		p.Units = append(p.Units, Unit{Key: pr.ID, Stream: pr.Row, Changes: []Change{
			change(Fleet, createEntry(id, row, Working, pr.Score, fields)),
		}, Moved: pr.ID + " asked of friend " + name})
	}
	return p, nil
}

// FriendReadClose applies one friend's read report to the read card on her
// fleet row: LAND moves it to ok, HOLD with a finding that names a defect
// moves it to broken. A report that does not close the read refuses, and
// the card stays where it is.
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
	col := OK
	set := map[string]string{"read": stamp(s.Now)}
	if verdict == "broken" {
		col = Broken
		set["finding"] = finding
	}
	p.Units = append(p.Units, Unit{Key: primary, Stream: pr.Row, Changes: []Change{
		change(Fleet, moveEntry(rc, rc.Row, col, set)),
	}, Moved: id + " " + verdict})
	return p
}
