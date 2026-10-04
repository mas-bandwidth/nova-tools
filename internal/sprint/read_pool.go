package sprint

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// A read is one pool (docs/SPEC-SPRINT.md section 1, a friend's read). A unit
// is a friend seat or a fleet reader. Ask deals a read to a unit whose class
// is at or above the read's class and that has room, friends first by the
// same dealer as a friend's work card, and a fleet reader only when no such
// friend has room and the class is one a fleet reader may serve. The card is
// the readers table's either way. readTierOf still collapses a frontier card
// onto pro for the route a fleet reader is drawn; the class does not.

// friendReaderPrefix begins a friend's readers-table row. A hyphen, not a dot:
// a read card id has at most three dot-separated parts (ValidCardID), and the
// friend's fleet row friend.<name> is a different row.
const friendReaderPrefix = "friend-"

// ReadFriendDeadline is how long a friend has for a read, on the sprint's
// clock: thirty minutes. It is not a wall clock (docs/SPEC-SPRINT.md section 1).
const ReadFriendDeadline = 30 * time.Minute

// FieldDue is the absolute deadline of a friend's read, the sprint clock plus
// ReadFriendDeadline, RFC3339. FieldDeadline on the same card is 1800 seconds,
// the shape a route's deadline has, because a packet reads that field as an int.
const FieldDue = "due"

// FriendReaderRow is the readers-table row a friend's reads are dealt to.
func FriendReaderRow(name string) string { return friendReaderPrefix + name }

// FriendOfReaderRow is the friend whose readers-table row it is; false for a
// fleet reader's row.
func FriendOfReaderRow(row string) (string, bool) {
	name, ok := strings.CutPrefix(row, friendReaderPrefix)
	return name, ok && name != ""
}

// IsFriendReader says the readers-table row is a friend's.
func IsFriendReader(row string) bool {
	_, ok := FriendOfReaderRow(row)
	return ok
}

// machineReaderRows is rows with the friends' readers-table rows left out, in
// the same order. The ask's round and the level run over fleet readers only,
// so a friend's row does not move the index and a friend's read is not leveled
// onto a paid reader (docs/SPEC-SPRINT.md section 1).
func machineReaderRows(rows []string) []string {
	var out []string
	for _, r := range rows {
		if !IsFriendReader(r) {
			out = append(out, r)
		}
	}
	return out
}

// readClassLadder is the class of a read, weakest first. heavy is on it so a
// frontier friend can serve a card whose tier is heavy; it is not a route.
var readClassLadder = []string{cardhdr.RouteFlash, cardhdr.RoutePro, "heavy", cardhdr.RouteFrontier}

func classRank(t string) int { return slices.Index(readClassLadder, t) }

// classAtLeast says have is a known class at or above need. An unknown class
// is never enough: an empty tier list does not serve a read.
func classAtLeast(have, need string) bool {
	h, n := classRank(have), classRank(need)
	return h >= 0 && n >= 0 && h >= n
}

// readClassOf is the class a unit must meet to serve pr's read: the tier the
// card is on (cardTier), raised by the read tier set for its stream or the
// sprint when that setting is stronger on readClassLadder, never lowered, and
// never collapsed. A frontier card stays frontier here. readTierOf's return is
// unchanged: a fleet route is still drawn on pro (docs/SPEC-SPRINT.md section 1).
func (s *Snapshot) readClassOf(pr *Card) string {
	m, _ := cardhdr.ReadModel(pr.F("brief"))
	t := cardTier(pr, m)
	if set := s.readTierSetting(pr.Row); classRank(set) > classRank(t) {
		t = set
	}
	return t
}

// fleetCanServe says a fleet reader may be asked pr's read. Flash and pro may.
// Frontier and heavy may not: a paid reader is a weaker class than those, and
// a unit below the read's class never serves it (docs/SPEC-SPRINT.md section 1).
func (s *Snapshot) fleetCanServe(pr *Card) bool {
	r := classRank(s.readClassOf(pr))
	return r >= 0 && r <= classRank(cardhdr.RoutePro)
}

// friendClass is the strongest class the seat is configured for. Empty tiers
// serve nothing: a seat loaded with no class does not take a read.
func friendClass(f FriendSeat) string {
	best := ""
	for _, t := range f.Tiers {
		if classRank(t) > classRank(best) {
			best = t
		}
	}
	return best
}

// friendRoom is the reads a friend can still take: her width, less the work
// cards on her fleet row (friendLoad, the same basis FriendDeal uses) and less
// the reads asked or reading on her readers-table row (docs/SPEC-SPRINT.md section 1).
func friendRoom(s *Snapshot, f FriendSeat) int {
	used := 0
	if s != nil && s.Fleet != nil {
		used += friendLoad(s, f.Name)
	}
	if s != nil && s.Readers != nil {
		row := FriendReaderRow(f.Name)
		used += s.Readers.Count(row, Asked) + s.Readers.Count(row, Reading)
	}
	return f.Width - used
}

func seatOf(seats []FriendSeat, name string) (FriendSeat, bool) {
	for _, f := range seats {
		if f.Name == name {
			return f, true
		}
	}
	return FriendSeat{}, false
}

// seatKnown says this tick loaded the friend's seat. A read of a friend whose
// seat was not loaded stays where it is (docs/SPEC-SPRINT.md section 1).
func seatKnown(seats []FriendSeat, name string) bool {
	_, ok := seatOf(seats, name)
	return ok
}

// pickFriend is the dealer a friend's work card and a friend's read share: the
// name in names (name order) with the most free width, the first by name among
// equals. "" when none has room (docs/SPEC-SPRINT.md section 1).
func pickFriend(names []string, free map[string]int) string {
	name := ""
	for _, f := range names {
		if free[f] > 0 && (name == "" || free[f] > free[name]) {
			name = f
		}
	}
	return name
}

// pickReadFriend is the friend Ask asks one read of pr: up, class at or above
// the read, room left in room, no card at this attempt, not already blocked.
// names is in name order. "" when none can take it (docs/SPEC-SPRINT.md section 1).
func pickReadFriend(s *Snapshot, pr *Card, names []string, room map[string]int, seats []FriendSeat, blocked map[string]bool, attempt int) string {
	class := s.readClassOf(pr)
	var eligible []string
	for _, name := range names {
		row := FriendReaderRow(name)
		if room[name] <= 0 || blocked[row] {
			continue
		}
		seat, ok := seatOf(seats, name)
		if !ok || !classAtLeast(friendClass(seat), class) {
			continue
		}
		if s.Readers != nil && s.Readers.Card(ReadCardID(pr.ID, attempt, row)) != nil {
			continue
		}
		eligible = append(eligible, name)
	}
	return pickFriend(eligible, room)
}

// freeFriends is how many friends count toward pr's read: each friend up at or
// above its class who already holds a live read of this attempt, or who has
// room and no card at the attempt. A friend holds at most one read of a primary
// (docs/SPEC-SPRINT.md section 1).
func (s *Snapshot) freeFriends(pr *Card, seats []FriendSeat) int {
	if pr == nil {
		return 0
	}
	class := s.readClassOf(pr)
	attempt := pr.Int("attempt")
	n := 0
	for _, f := range seats {
		if f.Status != Up || !classAtLeast(friendClass(f), class) {
			continue
		}
		row := FriendReaderRow(f.Name)
		id := ReadCardID(pr.ID, attempt, row)
		if s.Readers != nil {
			if c := s.Readers.Placed(id); c != nil {
				n++
				continue
			}
			if s.Readers.Card(id) != nil {
				continue
			}
		}
		if friendRoom(s, f) > 0 {
			n++
		}
	}
	return n
}

// machineUpReaders is the fleet readers that are up, in row order.
func (s *Snapshot) machineUpReaders() []string {
	return machineReaderRows(s.UpReaders())
}

// enoughReadUnits says the pool can cover the reads pr needs. A class a fleet
// reader cannot serve needs that many friends. Flash and pro keep the old
// short-circuit: a snapshot with no reader states holds every fleet reader up.
// A frontier read with no friend is not enough, however many pro readers are
// up (docs/SPEC-SPRINT.md section 1).
func (s *Snapshot) enoughReadUnits(pr *Card, seats []FriendSeat) bool {
	friends := s.freeFriends(pr, seats)
	if !s.fleetCanServe(pr) {
		return friends >= ReadsNeeded(pr)
	}
	if s.ReaderStates == nil {
		return true
	}
	return len(s.machineUpReaders())+friends >= ReadsNeeded(pr)
}

// noteFriendReaderStates marks each friend's readers-table row up, held or
// down from her seat, so a later part of this tick sees the same state. A
// snapshot with no reader states is left alone: creating the map would make
// every fleet reader look down (docs/SPEC-SPRINT.md section 1).
func (s *Snapshot) noteFriendReaderStates(seats []FriendSeat) {
	if s == nil || s.ReaderStates == nil {
		return
	}
	for _, f := range seats {
		switch f.Status {
		case Up:
			s.ReaderStates[FriendReaderRow(f.Name)] = ReaderUp
		case Held:
			s.ReaderStates[FriendReaderRow(f.Name)] = ReaderHeld
		default:
			s.ReaderStates[FriendReaderRow(f.Name)] = ReaderDown
		}
	}
}

// friendReadFields is what a friend's read card carries besides the read's own
// fields: who, the class, and the deadline. No route and no dollar amount.
// Token counts stay off the card until she reports them (docs/SPEC-SPRINT.md section 1).
func friendReadFields(s *Snapshot, pr *Card, name string) map[string]string {
	return map[string]string{
		FieldWho:      FriendRow(name),
		FieldDeadline: strconv.Itoa(int(ReadFriendDeadline / time.Second)),
		FieldDue:      stamp(s.Now.Add(ReadFriendDeadline)),
		FieldTier:     s.readClassOf(pr),
	}
}

// FriendReadBrief is the BRIEF.md of a read dealt to a friend: WHO, the
// attempt's branch, the commit it started from and the head under review, the
// deadline on the sprint's clock, and the primary's AS A READ section through
// the next heading (docs/SPEC-SPRINT.md section 1).
func FriendReadBrief(name, primary, brief, branch, start, head string, attempt int, deadline time.Time) string {
	var b strings.Builder
	b.WriteString("WHO: friend " + name + "\n")
	b.WriteString("primary: " + primary + "\n")
	b.WriteString("attempt: " + strconv.Itoa(attempt) + "\n")
	if branch != "" {
		b.WriteString("branch: " + branch + "\n")
	}
	if start != "" {
		b.WriteString("start: " + start + "\n")
	}
	if head != "" {
		b.WriteString("head: " + head + "\n")
	}
	if !deadline.IsZero() {
		b.WriteString("deadline: " + deadline.UTC().Format(time.RFC3339) + "\n")
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
// heading, through the next heading or the end.
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

// ParseReadVerdict reads a friend's report on a read. LAND closes it ok. HOLD
// closes it broken when a line names a file, a line or a rule
// (typedrec.NamesADefect, the rule a reader's broken read is held to). why is
// set when the report does not close the read (docs/SPEC-SPRINT.md section 1).
func ParseReadVerdict(report string) (verdict, finding, why string) {
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

// CloseFriendRead closes a friend's read the way a reader's verdict does:
// asked or reading moves to ok or broken through Read. A report that does not
// close the read refuses, and the card stays (docs/SPEC-SPRINT.md section 1).
func CloseFriendRead(s *Snapshot, row, cardID, report, who string) Plan {
	verdict, finding, why := ParseReadVerdict(report)
	if why != "" {
		var p Plan
		p.refuse(cardID, why)
		return p
	}
	return Read(s, ReadReq{Sel: Sel{IDs: []string{cardID}}, As: row, Verdict: verdict, Finding: finding, Who: who})
}
