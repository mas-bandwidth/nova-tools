package sprint

import (
	"cmp"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// A read at any tier is asked of a friend whose class is at or above that tier
// when she has room, by the same ask that deals work, before a paid reader is
// drawn (docs/SPEC-SPRINT.md, a read asked of any unit with room at or above
// the read tier). The read card is placed on her fleet row. The readers table
// gains no friend row. A paid reader is asked only when no such friend has room.

// FriendReadDeadline is how long the friend has, on the sprint's clock, to
// return the read: thirty minutes. It is not a wall clock.
const FriendReadDeadline = 30 * time.Minute

// FriendReadBrief is the BRIEF.md of a friend's read: WHO, the attempt's
// branch, the commit it started from and the head under review, the deadline,
// and the primary's AS A READ section through the next heading
// (docs/SPEC-SPRINT.md, a read asked of any unit with room at or above the read tier).
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

// friendAtOrAbove says the friend may be asked a read of tier: one of her tiers
// (friendTiers) is tier or above it on capLadder (docs/SPEC-SPRINT.md, a read
// asked of any unit with room at or above the read tier). A frontier friend
// takes a flash, pro, heavy or frontier read; a flash friend takes a flash read. A tier the
// friends' tiers leave out (FriendsTake, set --friends-tiers) no friend reads.
func friendAtOrAbove(s *Snapshot, f FriendSeat, tier string) bool {
	want := slices.Index(capLadder, tier)
	if want < 0 || !s.FriendsTake(tier) {
		return false
	}
	for _, t := range friendTiers(f) {
		if i := slices.Index(capLadder, t); i >= want {
			return true
		}
	}
	return false
}

// readHeadMatches says the read's head is the primary's head or the attempt's
// work head. A friend's read is asked with the work card's head (attemptEnds);
// a machine read is asked with the primary's. Finish writes both the same.
func readHeadMatches(s *Snapshot, pr, rc *Card) bool {
	h := rc.F("head")
	if h == "" || pr == nil {
		return false
	}
	if h == pr.F("head") {
		return true
	}
	attempt := pr.Int("attempt")
	if attempt == 0 {
		attempt = 1
	}
	_, _, work := attemptEnds(s, pr.ID, attempt)
	return work != "" && h == work
}

// friendReadAgrees says a read card on the fleet table is its reader's: the reader
// its id names and its reader field are one, and its row is that reader's fleet row,
// a friend's (friend.<name>) or a member's (read cards, read_cards.go). A retired read
// has been taken off that row (its row is empty) and the id still names the reader.
func friendReadAgrees(c *Card) bool {
	_, _, idReader, ok := ParseReadCard(c.ID)
	if !ok || c.F("reader") != idReader {
		return false
	}
	if c.Row == "" || c.Row == idReader {
		return true
	}
	name, rowOK := FriendOfRow(c.Row)
	return rowOK && name == idReader
}

// friendReadLive is the friend's reads of the attempt that stand
// (docs/SPEC-SPRINT.md, a read asked of any unit with room at or above the
// read tier). A placed read stands as itself. A retired read whose verdict is
// ok stands as OK when its head matches (readHeadMatches), and one whose
// verdict is broken stands as broken, each a copy so the fleet card is left
// as it is. A read taken back with no verdict, retired or withdrawn on her row,
// does not stand: the attempt is asked of another reader, and the withdrawn
// record stays as history. The ok and broken copies are not placed on a table.
//
// A read card a member is dealt (read_cards.go) is on the fleet table as hers is,
// on the member's row, and stands the same way (fleetReadLive is this, by its
// other name).
func friendReadLive(s *Snapshot, pr *Card) (placed, okCards, broken []*Card) {
	if s == nil || s.Fleet == nil || pr == nil {
		return nil, nil, nil
	}
	// only the cards whose id is a read of this attempt: the table is walked by
	// id, never copied and sorted whole for each primary a step asks of
	return fleetReadLiveOf(s, pr, s.Fleet.WithPrefix(attemptReadPrefix(pr)))
}

// attemptReadPrefix is the id prefix of every read card of the primary's attempt.
func attemptReadPrefix(pr *Card) string {
	attempt := pr.Int("attempt")
	if attempt == 0 {
		attempt = 1
	}
	return pr.ID + ".r" + itoa(attempt) + "."
}

// fleetReadLiveOf is friendReadLive over the fleet cards given (every card of the table,
// or those of the primary a caller indexed once: fleetReadIndex).
func fleetReadLiveOf(s *Snapshot, pr *Card, cards []*Card) (placed, okCards, broken []*Card) {
	prefix := attemptReadPrefix(pr)
	for _, c := range cards {
		if c == nil || c.F("kind") != "read" || !strings.HasPrefix(c.ID, prefix) || c.Col == Withdrawn {
			continue
		}
		if c.Placed() {
			if !friendReadAgrees(c) {
				continue
			}
			placed = append(placed, c)
			continue
		}
		if !friendReadAgrees(c) {
			continue
		}
		switch c.F("verdict") {
		case "ok":
			if !readHeadMatches(s, pr, c) {
				continue
			}
			syn := *c
			syn.Col = OK
			okCards = append(okCards, &syn)
		case "broken":
			syn := *c
			syn.Col = Broken
			broken = append(broken, &syn)
		}
	}
	return placed, okCards, broken
}

// fleetReadIndex is the fleet table's read cards, placed or kept, by their primary field,
// in id order: read once for a step that asks of many primaries.
func fleetReadIndex(s *Snapshot) map[string][]*Card {
	out := map[string][]*Card{}
	if s == nil || s.Fleet == nil {
		return out
	}
	for _, c := range s.Fleet.Cards() {
		if c != nil && c.F("kind") == "read" {
			out[c.F("primary")] = append(out[c.F("primary")], c)
		}
	}
	return out
}

// machineReaderHasRoom says a paid reader of the primary's collapsed read tier
// (readTierOf) is up, has no read card of the attempt, and has free room.
func machineReaderHasRoom(s *Snapshot, pr *Card) bool {
	if s == nil || s.Readers == nil || pr == nil {
		return false
	}
	attempt := pr.Int("attempt")
	if attempt == 0 {
		attempt = 1
	}
	room := s.readerRooms(s.Readers.Rows())
	for _, rd := range s.freeReaders(pr, attempt) {
		if room[rd].free > 0 {
			return true
		}
	}
	return false
}

// attemptEnds is the branch that holds the work card's head (ReadBranch: the branch the work
// pushed, never one named by a later generation), its head, and the commit the attempt
// started from (the last earlier attempt that finished ok). Finish writes the branch on the
// work card, not the primary (steps_work.go).
func attemptEnds(s *Snapshot, primary string, attempt int) (branch, start, head string) {
	if s.Fleet == nil {
		return "", "", ""
	}
	if wc := s.Fleet.Card(WorkCardID(primary, attempt)); wc != nil {
		var pr *Card
		if s.Work != nil {
			pr = s.Work.Card(primary)
		}
		branch, head = ReadBranch(s.Prefix, s.Epoch, pr, wc), wc.F("head")
	}
	var earlier []*Card
	for a := 1; a < attempt; a++ {
		if w := s.Fleet.Card(WorkCardID(primary, a)); w != nil {
			earlier = append(earlier, w)
		}
	}
	return branch, BaseOf(earlier).Head, head
}

// attemptWorker is the friend who worked on this attempt of the primary, if any.
// A friend does not read her own work.
func attemptWorker(s *Snapshot, primary string, attempt int) string {
	if s == nil {
		return ""
	}
	if s.Fleet != nil {
		if wc := s.Fleet.Card(WorkCardID(primary, attempt)); wc != nil {
			if name, ok := FriendOfRow(wc.Row); ok {
				return name
			}
			if name, ok := FriendOfRow(wc.F("member")); ok {
				return name
			}
		}
	}
	if s.Work != nil {
		if pr := s.Work.Card(primary); pr != nil && pr.Int("attempt") == attempt {
			if name, ok := FriendOfRow(pr.F("member")); ok {
				return name
			}
		}
	}
	return ""
}

func seatDir(seats []FriendSeat, name, fallback string) string {
	for _, f := range seats {
		if f.Name == name && f.Dir != "" {
			return f.Dir
		}
	}
	return fallback
}

// friendReadAsk asks each read in review of a friend with room at or above its
// read tier, the same chooser as the friend deal (preferredFriend: an idle lane,
// then the most room, then by name), and decrements that free width as the deal
// does (docs/SPEC-SPRINT.md, a read asked of any unit with room at or above the
// read tier). dir is a working directory used when the seat names none; empty
// writes no brief (friend sync writes it). A friend whose read of the attempt
// was taken back is not asked it again; another friend is. A paid reader is left
// the read when no such friend has room. It answers too the primaries it left
// waiting: a friend at or above the read tier is up who may read the attempt,
// every such friend is at her room, and no paid reader has room. The tick's ask
// records those as waiting for a reader and counts them due (friendAskPart).
func friendReadAsk(s *Snapshot, seats []FriendSeat, dir string) (p Plan, waits []*Card, err error) {
	if s == nil || s.Work == nil || s.Fleet == nil {
		return p, nil, nil
	}
	return friendReadAskOf(s, seats, dir, readOrder(readsWaitingCards(s)))
}

// friendReadAskOf is friendReadAsk over the primaries given, in their order: the deal asks
// the reads level by level (friendDealByLadder).
func friendReadAskOf(s *Snapshot, seats []FriendSeat, dir string, cards []*Card) (p Plan, waits []*Card, err error) {
	if s == nil || s.Work == nil || s.Fleet == nil {
		return p, nil, nil
	}
	free, lanes := map[string]int{}, map[string]int{}
	var up []FriendSeat
	for _, f := range seats {
		if f.Status != Up {
			continue
		}
		room, width := friendRoom(f)
		free[f.Name] = room - friendLoad(s, f.Name)
		lanes[f.Name] = width - s.Fleet.Count(FriendRow(f.Name), Working)
		up = append(up, f)
	}
	declared := map[string]bool{}
	// by the read's level (readOrder, priority.go), work order within one
	for _, pr := range cards {
		attempt := readAttempt(pr)
		tier := friendReadTier(s, pr)
		worker := attemptWorker(s, pr.ID, attempt)
		// a friend is asked an attempt once: her read card at it, taken back,
		// keeps its id, so she is not one who may read it again. A friend who
		// worked on this attempt does not read her own work (friendMayRead).
		var withRoom []string
		eligible := false
		for _, f := range up {
			if !friendMayRead(s, f, pr, attempt, tier, worker) {
				continue
			}
			eligible = true
			if free[f.Name] > 0 {
				withRoom = append(withRoom, f.Name)
			}
		}
		name := preferredFriend(withRoom, lanes, free)
		if pinned, ok := FriendCard(pr); ok && pinned != "" && slices.Contains(withRoom, pinned) {
			name = pinned // the WHO friend is asked first when she may read and has room
		}
		switch {
		case name != "":
			if err := askOneFriend(&p, s, pr, seats, name, dir, attempt, free, lanes, declared); err != nil {
				return Plan{}, nil, err
			}
		case eligible && !machineReaderHasRoom(s, pr):
			waits = append(waits, pr)
		}
	}
	return Lawful(p), waits, nil
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
	// a read on her row is written at a live generation, from 1, as her work cards
	// are: queue, finish and her QUEUE.json name it at the one the card holds
	fields := map[string]string{
		"kind": "read", "primary": pr.ID, "stream": pr.Row, "reader": name,
		"attempt": itoa(attempt), "head": head, "branch": branch, "start": start,
		"asked": stamp(s.Now), "gen": "1",
	}
	priorityOnRead(fields, pr) // its primary's level when above reader (priority.go)
	p.Units = append(p.Units, Unit{Key: pr.ID, Stream: pr.Row, Changes: []Change{
		change(Fleet, createEntry(id, row, col, pr.Score, fields)),
	}, Moved: pr.ID + " asked of friend " + name})
	return nil
}

// FriendReadCloseChecked applies one friend's read report to the read card named (card, the
// packet's own key: a re-asked read is a later identity of the plain one, ReadCardGenIDs; "" is
// the one of her identities at the attempt that is placed), with the server's check of the
// read's branch at its close (missing, as ReadReq.Missing): a broken report on a read whose
// branch origin does not hold is retired with no verdict (read_missing.go). generation, when
// given, must be the read card's live one (a stale report is refused).
func FriendReadCloseChecked(s *Snapshot, name, primary, card, report string, missing map[string]MissingBranch, generation ...int) Plan {
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
	rc := friendReadCardOf(s, name, primary, attempt, card)
	if rc == nil {
		p.refuse(primary, "no read asked of "+name)
		return p
	}
	if len(generation) > 0 && max(generation[0], 1) != max(rc.Int("gen"), 1) {
		p.refuse(primary, fmt.Sprintf("stale read report: generation %d is not the live one (%d)", generation[0], max(rc.Int("gen"), 1)))
		return p
	}
	verdict, finding, why := ParseFriendReadReport(report)
	if why != "" {
		p.refuse(primary, why)
		return p
	}
	if m, ok := (ReadReq{Verdict: verdict, Missing: missing}).missingBranch(rc); ok {
		p.Units = append(p.Units, missingBranchUnit(s, Fleet, rc, pr, m, finding, "", FriendRow(name)))
		return p
	}
	p.Units = append(p.Units, friendReadCloseUnit(s, name, pr, rc, verdict, finding, ""))
	if pw, ok := windowWrite(s.Fleet, Fleet, []ReadVerdict{verdictOf(s, rc, pr, FriendRow(name), verdict, finding)}); ok {
		p.Props = append(p.Props, pw)
	}
	return p
}

// friendReadCardOf is the friend's placed read card of the primary at the attempt: the one
// named (card, one of her identities, ReadCardGenIDs), or with none named the placed one of
// them; nil when none is placed on her row.
func friendReadCardOf(s *Snapshot, name, primary string, attempt int, card string) *Card {
	ids := ReadCardGenIDs(primary, attempt, name)
	if card != "" {
		if !slices.Contains(ids, card) {
			return nil
		}
		ids = []string{card}
	}
	for _, id := range ids {
		if rc := s.Fleet.Card(id); rc != nil && rc.Placed() && rc.Row == FriendRow(name) {
			return rc
		}
	}
	return nil
}

// friendReadCloseUnit is the one close of a friend's read card on her fleet row,
// whether her outbox report (FriendReadClose) or the read verb (readCardVerb)
// carried the verdict: the card retired with its verdict, a broken verdict's
// judgment, and the primary's review judgment after it. usage is what the read spent, as
// the read verb's --usage carried it ("" from her outbox report): kept on the card, timed
// and priced as a reader's read is (readCostRecord), and recorded on the primary in the
// same unit (the owner's rule: the complete cost is tracked).
func friendReadCloseUnit(s *Snapshot, name string, pr, rc *Card, verdict, finding, usage string) Unit {
	attempt := rc.Int("attempt")
	if attempt == 0 {
		attempt = 1
	}
	set := map[string]string{"verdict": verdict, "read": stamp(s.Now), "retired": stamp(s.Now), "retired_by": "read"}
	var notes []Note
	if verdict == "broken" {
		set["finding"] = finding
		n := judgment(NReadBroken, pr.Row, s.Now, pr.Int("broken_reads"), pr.ID)
		n.Who, n.Attempt, n.What = name, attempt, finding
		notes = append(notes, n)
	}
	// the card is still placed when the judgment is read; the verdict is the column it stands as
	stood := OK
	if verdict == "broken" {
		stood = Broken
	}
	if j, ok := reviewJudgment(s, pr, reviewStep{moved: map[string]string{rc.ID: stood}, writes: notes, who: name}); ok {
		notes = append(notes, j)
	}
	changes := []Change{change(Fleet, removeEntry(rc, set))}
	// the terminal record is unconditional: an outbox report carries no usage, and
	// that run is unpriced with the reason, never omitted (cost.go)
	rec := readCostRecord(s, rc, usage, rc.F("asked"), cmp.Or(rc.F("begun"), stamp(s.Now)))
	if strings.TrimSpace(usage) != "" {
		set[FieldUsage] = rec
		maps.Copy(set, readUsageFields(rc, usage))
	}
	if pr.Placed() {
		costs := map[string]string{}
		addConsumer(pr, costs, readConsumer(s, rc, 0, verdict, rec))
		changes = append(changes, change(Work, setEntry(pr, costs)))
	}
	return Unit{Key: pr.ID, Stream: pr.Row, Changes: changes, Moved: rc.ID + " retired " + verdict, Notes: notes}
}

// FriendReadOutboxLine is how a read on a friend's row is returned: her
// outbox report, which friend sync reads (FriendReadClose), or the read verb
// her packet prints, which writes the same close (readCardVerb).
func FriendReadOutboxLine(row, id string, epoch uint64) string {
	return fmt.Sprintf("write outbox/%s/REPORT.md in your working directory with 'Verdict: LAND', or 'Verdict: HOLD' and a line naming the file:line or rule and what to change (friend sync reads it); or run: nova-sprint read --as %s (--ok | --broken) %s --epoch %d --finding '<file:line, and what to change>'", id, row, id, epoch)
}

// readCardVerb is the read verb on a fleet row, a friend's or a member's (read_cards.go):
// a verdict closes its read card as her outbox report does (friendReadCloseUnit); a return
// (--return, with the reason) hands it back with no verdict, retired by returned, which
// spends the reader's read of the attempt, and the read-card deal deals it to another
// reader. A read card has no begin: the take moves it to working.
func readCardVerb(s *Snapshot, r ReadReq, row, name string) Plan {
	var p Plan
	if r.Begin {
		p.refuse("read", "a read card has no begin: the take moves it to working; report it with "+
			"read --as "+row+" (--ok | --broken) <read> --finding <text>, hand it back with read --as "+row+" --return <read> --reason <text>, or write outbox/<read>/REPORT.md")
		return p
	}
	if s.Fleet == nil {
		p.refuse("read", "the fleet table was not read")
		return p
	}
	sel := r.Sel
	if len(sel.IDs) == 0 && sel.Only == nil && sel.Limit == 0 {
		sel.Limit = 1
	}
	var all []*Card
	for _, col := range []string{Working, Ready} {
		for _, c := range s.Fleet.Cell(row, col) {
			if c.F("kind") == "read" {
				all = append(all, c)
			}
		}
	}
	SortCards(all)
	chosen := pick(&p, sel, all, fieldStream, func(c *Card) string {
		if len(r.Gens) > 0 || c.F("stopped_from_gen") != "" {
			if why := liveGen("read", c, r.Gens); why != "" {
				return why
			}
		}
		switch {
		case c.F("kind") != "read":
			return "not a read (it is " + orDash(c.F("kind")) + "): work is reported with finish"
		case !c.Placed():
			return "retired at " + orDash(c.F("retired")) + " by " + orDash(c.F("retired_by")) + ": nothing to report"
		case c.Row != row:
			return "not " + r.As + "'s to read (it is " + placeWord(c) + ")"
		case s.Work.Card(c.F("primary")) == nil:
			return "its primary " + c.F("primary") + " is not on the table"
		case readAttempt(s.Work.Card(c.F("primary"))) != readAttempt(c):
			return "a read of attempt " + itoa(readAttempt(c)) + " of " + c.F("primary") + ", which is at attempt " + itoa(readAttempt(s.Work.Card(c.F("primary")))) + ": a verdict closes a read of the attempt under review only"
		}
		return ""
	}, s.Fleet.Card)
	namePrimarysReads(&p, all)
	var verdicts []ReadVerdict // the ledger's (reads_window.go)
	for _, c := range chosen {
		pr := s.Work.Card(c.F("primary"))
		if r.Return {
			set := map[string]string{"retired": stamp(s.Now), "retired_by": RetiredByReturned, "reason": cutText(r.Reason, MaxCardTextBytes)}
			changes := []Change{change(Fleet, removeEntry(c, set))}
			if c.Col == Working {
				rec := readCostRecord(s, c, r.Usage, c.F("asked"), stamp(readStart(c)))
				if r.Usage != "" {
					set[FieldUsage] = rec
					maps.Copy(set, readUsageFields(c, r.Usage))
				}
				costs := map[string]string{}
				addConsumer(pr, costs, readConsumer(s, c, 0, "retired returned", rec))
				changes = append(changes, change(Work, setEntry(pr, costs)))
			} else if r.Usage != "" {
				set["usage"] = r.Usage
			}
			n := happened(NReadReturned, pr.Row, s.Now, pr.ID)
			n.Who, n.Attempt, n.What = name, c.Int("attempt"), name+" returned "+c.ID+": "+r.Reason
			p.Units = append(p.Units, Unit{Key: pr.ID, Stream: pr.Row, Changes: changes,
				Moved: c.ID + " " + c.Col + " -> returned (retired: " + name + " gave no verdict)", Notes: []Note{n}})
			continue
		}
		if m, ok := r.missingBranch(c); ok {
			// the machine's fault, not the card's: no verdict, asked again (read_missing.go)
			p.Units = append(p.Units, missingBranchUnit(s, Fleet, c, pr, m, r.Finding, r.Usage, r.Who))
			continue
		}
		u := friendReadCloseUnit(s, name, pr, c, r.Verdict, r.Finding, r.Usage)
		u.Moved = c.ID + " " + c.Col + " -> " + r.Verdict + " (retired: read by " + name + ")"
		p.Units = append(p.Units, u)
		verdicts = append(verdicts, verdictOf(s, c, pr, row, r.Verdict, r.Finding))
	}
	if pw, ok := windowWrite(s.Fleet, Fleet, verdicts); ok {
		p.Props = append(p.Props, pw)
	}
	return p
}

// The tick's ask part lives in steps_tick.go, which this change does not edit.
// The part the machine runs is the function TickTables holds, so the friend
// ask is installed there: a read a friend has room for is asked before a
// machine route is drawn, and the machine ask does not see that primary.
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
// the reads a friend just took, and the reads waiting because every friend at
// or above the tier is at her room and no paid reader has room, hidden from
// it, in one plan (docs/SPEC-SPRINT.md, a read asked of any unit with room at
// or above the read tier). A card in review is asked in the tick when a unit
// it may be asked of has room; one that waits for room is recorded waiting for
// a reader (waitingForReader) and counted due, so the no-stall rule holds it
// and the next tick asks it. The seats are the tick's; a part that asks what
// the ask does with none (the no-stall rule's) plans on the snapshot's, and
// writes no brief.
func friendAskPart(machine TickPartFn) TickPartFn {
	return func(s *Snapshot, r TickReq) (Plan, int) {
		seats := r.Friends
		if seats == nil {
			seats = withoutDirs(s.Friends)
		}
		if s.ReadCardsOn() {
			return readCardsAskPart(s, r, seats)
		}
		fp, waits, err := friendReadAsk(s, seats, "")
		var hide []string
		if s != nil && s.Work != nil {
			for _, u := range fp.Units {
				if s.Work.Placed(u.Key) != nil {
					hide = append(hide, u.Key)
				}
			}
		}
		for _, c := range waits {
			hide = append(hide, c.ID)
		}
		restore := hidePrimaries(s, hide)
		mp, due := machine(s, r)
		restore()
		if err != nil {
			mp.refuse("ask", err.Error())
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
// reads waiting because every friend at or above the tier is at her room, and
// no paid reader has room, are friends (friendReadAsk). The mark is a
// work-table field the pump applies, as the ask's asked field is.
func waitingForReader(p *Plan, s *Snapshot, friends []*Card) {
	asked := askedUnits(p, s)
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
		waits[c.ID] = "every friend at or above its read tier who may read it is at her room, and no paid reader has room"
	}
	for _, c := range s.Work.Column(Review) {
		if _, ok := waits[c.ID]; ok {
			continue
		}
		if _, ok := asked[c.ID]; ok || judged[c.ID] || c.F("result") == "failed" ||
			ReadsWanted(s, c) == 0 || !enoughReadersUp(s, c) || len(closesFor(s.Open, []string{NCannotAsk}, c.ID)) > 0 {
			continue
		}
		waits[c.ID] = "no reader of its tier up has room this tick"
	}
	markWaiting(p, s, asked, waits)
}

// askedUnits is the unit of each primary the plan asks, by primary.
func askedUnits(p *Plan, s *Snapshot) map[string]int {
	asked := map[string]int{}
	for i, u := range p.Units {
		if s.Work.Placed(u.Key) != nil {
			asked[u.Key] = i
		}
	}
	return asked
}

// markWaiting writes the waiting mark and its note on each primary in review that waits
// (waits: why), once an attempt, and clears the mark on each primary the plan asks (asked).
func markWaiting(p *Plan, s *Snapshot, asked map[string]int, waits map[string]string) {
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

// hidePrimaries takes the named primaries out of review for the machine ask
// and puts them back. The plan is built against the restored places.
func hidePrimaries(s *Snapshot, ids []string) func() {
	if s == nil || s.Work == nil || len(ids) == 0 {
		return func() {}
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	type saved struct {
		c   *Card
		col string
	}
	var held []saved
	for _, c := range s.Work.Column(Review) {
		if want[c.ID] {
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

// openFriendReads is the read cards on the fleet table, a friend's or a member's (read
// cards, read_cards.go), still open on the primary (ready
// or working): a rework or a brief edited in place retires them with the readers table's, so
// none keeps her room or is closed against the attempt that was replaced.
func openFriendReads(s *Snapshot, primary string) []*Card {
	if s.Fleet == nil {
		return nil
	}
	var out []*Card
	for _, col := range []string{Ready, Working} {
		for _, c := range s.Fleet.Column(col) {
			if c.F("kind") == "read" && c.F("primary") == primary && c.Placed() {
				out = append(out, c)
			}
		}
	}
	return out
}
