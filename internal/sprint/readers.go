package sprint

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/mas-bandwidth/nova-tools/pkg/member"
)

// A reader's state (docs/SPEC-SPRINT.md section 6, the readers table; the
// model is tla/DirtyTick.tla, the readers update: a read is placed only on a
// reader up, and a read asked of a reader that goes away is taken back). A
// reader is a row of the readers table, which the coordinator declares (init
// --readers, reader add); a reader with its row says it is there by asking for its
// own queue (queue --as <reader> writes its beat; a name with no row writes none,
// and its queue answers reader false). Its state is derived, never typed: away while the
// coordinator holds it away (reader away; reader up releases the hold),
// whatever it beats; else up while its last beat is within ReaderBeatBound;
// else away when it beat once and has lapsed, down when it has never beaten.
// The ask deals reads to readers up only.

const (
	// ReaderUp, ReaderAway and ReaderDown are a reader's states.
	ReaderUp   = "up"
	ReaderAway = "away"
	ReaderDown = "down"
	// ReaderRetired is a reader the coordinator retired (reader retire): held
	// away for good, its beat writing none and its queue answering reader false,
	// its row and read cards kept, the history (the comfort list of 2026-10-03,
	// item 6); reader up brings it back.
	ReaderRetired = "retired"
	// ReaderHeld is a reader the coordinator holds (hold <reader>, hold.go): asked
	// nothing, its reads asked and not begun asked of another, its reads begun
	// finishing unless the hold took them back (--return).
	ReaderHeld = Held

	// ReaderBeatBound is how long a reader stays up after its last beat: the
	// fleet's bound (BeatDeadline), named once so a reader's can be told apart.
	ReaderBeatBound = BeatDeadline
)

// ReaderState is a reader's state at now, from the coordinator's hold and its
// last beat.
func ReaderState(away bool, b Beat, now time.Time) string {
	switch {
	case away:
		return ReaderAway
	case b.Beaten() && now.Sub(b.At) <= ReaderBeatBound:
		return ReaderUp
	case b.Beaten():
		return ReaderAway
	}
	return ReaderDown
}

// ReaderIsUp says a reader may be asked: it is up, or the snapshot carries no
// reader states (a core test's, which holds every reader up).
func (s *Snapshot) ReaderIsUp(reader string) bool {
	return s.ReaderStates == nil || s.ReaderStates[reader] == ReaderUp
}

// UpReaders is the readers whose state is up, in row order.
func (s *Snapshot) UpReaders() []string {
	var out []string
	for _, r := range s.Readers.Rows() {
		if s.ReaderIsUp(r) {
			out = append(out, r)
		}
	}
	return out
}

// readersText names every reader with its state, in name order: the text of
// the judgment "fewer than two readers up".
func readersText(s *Snapshot) string {
	var out []string
	for _, r := range s.Readers.Rows() {
		st := s.ReaderStates[r]
		switch {
		case s.ReaderStates == nil:
			st = ReaderUp
		case st == ReaderRetired:
			continue // off the table: no reader the judgment names
		case st == "":
			st = ReaderDown
		}
		if why := s.NoRoom[r]; why != "" {
			st += " (starts no read: " + why + ")"
		}
		out = append(out, r+" "+st)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// NFewReaders is the tick's judgment that fewer readers are up than a primary
// in review waiting to be asked needs (two for a pro card, one for a flash
// card; ReadsNeeded): the ask raises it once, and asks no absent reader.
const NFewReaders = "fewer than two readers up"

// fewReaders is the text of the judgment.
func fewReaders(s *Snapshot) string {
	return fmt.Sprintf("%s: %s", NFewReaders, readersText(s))
}

// cannotAskWhy is the ask's refusal of a primary at an attempt no reader can be
// asked: it needs want different readers, free is the number up with room and
// no read card at the attempt, full the number more that are at width. A reader
// is asked an attempt once (its read card, placed or retired, is one read per
// reader per attempt: a read taken back away, levelled or returned counts), so
// the readers left are new ones (reader add), or the next attempt (rework).
func cannotAskWhy(s *Snapshot, pr *Card, attempt, want, free, full int) string {
	return fmt.Sprintf("needs %d different readers and %d is free with no read card at attempt %d of %s (%d free but at width); a reader is asked an attempt once, and once more when its read was taken back with no verdict, and a reader away or down is not asked (readers: %s); run: nova-sprint reader add <name>, nova-sprint reader up <name>, or nova-sprint rework %s --fix <text> for a new attempt every reader may read", want, free, attempt, pr.ID, full, readersText(s), pr.ID)
}

// NWaitingForReader is the tick's note on a primary in review whose read it
// could not ask for want of a reader with room (a machine reader of its tier
// at its width, or a friend at or above its read tier at her room when no paid
// reader has room): a happened note,
// no decision, written once an attempt (FieldWaitingReader), and the read is
// asked the tick a reader frees. Review never holds a card with no read asked
// and no note: an ask refused for a reason the coordinator decides is a
// judgment (cannot ask, fewer than two readers up), and the rest wait with
// this note (docs/SPEC-SPRINT.md section 6, a card waiting for a reader).
const NWaitingForReader = "waiting for a reader"

// FieldWaitingReader is the primary's mark of the note: "<attempt> <since>",
// the attempt it waits at and when the tick first found it waiting. The ask
// that asks it clears it.
const FieldWaitingReader = "waiting_reader"

// NoEligibleReader opens the tick's one judgment for every primary of a tick
// the ask refused for want of readers (TickAsk): "no eligible reader for <ids>".
const NoEligibleReader = "no eligible reader for "

// awayRead says a read card is asked, not begun, of a reader that is not up:
// the ask takes it back (retires it) and asks the primary's next reader in the
// same step, at its attempt and with no redeal spent (tla/DirtyTick.tla,
// ReaderAway). A read begun stays with its reader.
func awayRead(s *Snapshot, rc *Card) bool { return rc.Col == Asked && !s.ReaderIsUp(rc.Row) }

// returnedRead says a read card was handed back by its reader with no verdict
// (read --return) and is not asked again yet: it is back in asked on its own
// row, stamped returned. A return is not a read: the ask places it again, on
// another reader free at the attempt, or on the same reader in place
// (tla/DirtyTick.tla, A RETURN IS NOT A READ, JudgedOnlyAfterTheBound).
func returnedRead(rc *Card) bool { return rc.Col == Asked && rc.F(FieldReturned) != "" }

// FieldReturned is the stamp on a read card handed back with no verdict, until
// the ask asks it again.
const FieldReturned = "returned"

// ReaderPrefix names a reader for its machine: reader-<m> is the one reader on
// the fleet machine m, and it runs at m's width, the fleet row's, read with its
// queue every tick as the member on m reads its own (the owner, 2026-10-02:
// "The reader widths seem to be very ad-hoc, unlike the machine widths"; "why
// not just have as many readers as workers per-machine"). The readers table
// holds no width column: a reader's width is derived from its fleet row
// (ReaderWidth), `queue --as reader-<m>` carries m's fleet row's width, and a
// reader named for no row carries none and begins nothing.
const ReaderPrefix = "reader-"

// ReaderMachine is the machine a reader is named for: reader-<m> names m; a
// name of another shape names no machine.
func ReaderMachine(reader string) (machine string, ok bool) {
	m, found := strings.CutPrefix(reader, ReaderPrefix)
	if !found || !ValidID(m) {
		return "", false
	}
	return m, true
}

// A read refused (docs/SPEC-SPRINT.md section 6, a read refused is not a read): a read its
// reader hands back because it could not launch it (cardhdr.EndLaunch: no route, a slot it
// cannot make) never ran, so it is no read, no re-ask and no mark against its reader. The
// return retires it off that reader (RetiredByRefused, the reason in FieldRefused) and the
// ask asks the read of another reader of its tier; the reader that refused may be asked it
// once more at the attempt (ReadCardForAsk's second identity), as a reader taken back away
// may. When no reader is left free at the attempt and one refused it for no route
// (refusedNoRoute), the tier's one judgment says so (NNoRoute, TickDeal) and the card stays
// in review: the readers stay up and are asked their next reads. On 2026-10-06 every fleet
// reader was asked heavy reads it had no route for, each return was counted toward the
// re-ask bound as a read, and the readers were spent at the attempt.
const (
	// RetiredByRefused is the retired_by of a read its reader refused to launch.
	RetiredByRefused = "refused"
	// FieldRefused is why the reader refused it, as its return said.
	FieldRefused = "refused_why"
)

// RefusedReturn says a return's reason is a launch refusal: the read never ran.
func RefusedReturn(reason string) bool {
	return strings.Contains(reason, cardhdr.EndLaunch) || NoRouteRefusal(reason)
}

// NoRouteRefusal says a refusal is for want of a route: the reader had no model, budget
// or deadline to run the read on.
func NoRouteRefusal(reason string) bool {
	return strings.Contains(reason, "has no route") || strings.Contains(reason, "no route serves")
}

// refusedNoRoute says the primary waits in review for reads no reader can launch: its
// work did not fail, it wants a read, fewer readers are free to be asked it (freeReaders,
// and its returned reads asked again in place) than the reads it still needs, which the
// ask would refuse as "cannot ask", and a reader refused it at the attempt for want of a
// route. Its tier's judgment holds it (TickDeal), and the ask leaves it (TickAsk).
func (s *Snapshot) refusedNoRoute(pr *Card) bool {
	if s.Readers == nil || pr.Col != Review || pr.F("result") == "failed" || ReadsWanted(s, pr) == 0 {
		return false
	}
	attempt := pr.Int("attempt")
	if len(s.freeReaders(pr, attempt))+len(returnedInTier(s, pr, attempt)) >= ReadsNeededIn(s, pr)-len(liveReadsAt(s, pr, attempt)) {
		return false
	}
	if !anyReadCardAt(s, pr, attempt) {
		return false
	}
	for _, rd := range s.Readers.Rows() {
		for _, id := range ReadCardIDs(pr.ID, attempt, rd) {
			if c := s.Readers.Card(id); c != nil && c.F("retired_by") == RetiredByRefused && NoRouteRefusal(c.F(FieldRefused)) {
				return true
			}
		}
	}
	return false
}

// FieldReasked is how many times a read card's reader returned it and it went
// back to asked on the reader's row, counted by Read itself at each return, so
// the bound holds whatever the tick does and however many readers are up (the
// ask need not run for the count to move); MaxReadReasks is the most: the
// return after them retires the card, counted as a read (tla/DirtyTick.tla,
// MaxReasks and ReasksBounded).
const (
	FieldReasked  = "reasked"
	MaxReadReasks = 2
)

// FieldLeveled marks a read card the level moved: the level moves it no more, so a
// read is never shuttled between readers tick after tick and the level never sticks on one card.
const FieldLeveled = "leveled"

// ReadsNeeded is the read rule of a card's tier: how many different readers' ok
// reads at its head make the primary acceptable, and so how many readers the ask
// asks at an attempt, while the sprint sets no count (ReadsNeededIn): one when the
// tier the card is on (cardTier) is flash, and two at any stronger tier (pro, or
// frontier). A friend whose class is at or above the read tier is asked
// first when she has room (friend_read.go); a machine read is drawn on a route of
// the card's read tier (readTierOf) only when no such friend has room (the owner,
// 2026-10-02, cost rule 4, nova-tools#5174: "Reads: one cold read per flash
// card on a flash route; two per pro card; readers still equal workers per
// machine"). The tier is the card's own, the tier it is on (cardTier: flash first,
// then the tier it escalated to, or the tier a rework recorded).
func ReadsNeeded(pr *Card) int {
	m, _ := cardhdr.ReadModel(pr.F("brief"))
	if cardTier(pr, m) == cardhdr.RouteFlash {
		return 1
	}
	return 2
}

// FieldReadsNeeded is the primary's field the accept writes when it accepted the card on
// the sprint's count (set --reads) rather than its tier's rule (ReadsNeeded): that count,
// so a card past review is held to the count it was accepted on.
const FieldReadsNeeded = "reads_needed"

// ReadsNeededIn is how many different readers' ok reads at its head the primary needs in
// the snapshot, the count every live caller uses. In review: the sprint's count while one
// is set (nova-sprint set --reads 0, 1 or 2, PropReadsNeeded; the owner, 2026-10-06: "I'd
// like to waive the second read for the moment"), whatever the card's tier, so a card
// with that many ok reads at its head is accepted on the next tick after the setting
// changes; else its tier's rule (ReadsNeeded). At 0 no read is asked, and a primary whose
// work finished LAND at its head is accepted on it (acceptable). A card in merging or
// landed is never judged again: it is held to the count it was accepted on, the accept's
// FieldReadsNeeded, else its tier's rule.
func ReadsNeededIn(s *Snapshot, pr *Card) int {
	if pr.Col == Merging || pr.Col == Landed {
		if n, err := strconv.Atoi(pr.F(FieldReadsNeeded)); err == nil && n >= 0 {
			return n
		}
		return ReadsNeeded(pr)
	}
	if s != nil && s.Work != nil {
		if v, _ := s.Work.Prop(PropReadsNeeded); slices.Contains(readsWords, v) {
			n, _ := strconv.Atoi(v)
			return n
		}
	}
	return ReadsNeeded(pr)
}

// enoughReadersUp says as many readers of the primary's tier are up as it needs
// (ReadsNeededIn). A reader counts only when it serves that tier (readerServesTier:
// its tiers cell names it, an empty cell every tier, and it brings its own model
// or a route of the tier is there to draw). A snapshot with no reader states, no
// tiers cell set and a route of the tier (or none at all) holds every reader up, as
// it did before the column: the
// ask may ask it (TickAsk); else it waits, judged NFewReaders. A friend is asked
// before this (friendReadAsk); under the interim rule (reads asked together) a read she
// holds at the attempt, outstanding or ok, counts toward what it needs, so a heavy
// friend and one fleet reader of pro are its two readers.
func enoughReadersUp(s *Snapshot, pr *Card) bool {
	// the fast path: no states, no tiers cell, the tier routed, and no fleet reader held to
	// flash by its empty cell for a read above flash (fleetReadsFlashOnly)
	if t := s.readTierOf(pr); s.ReaderStates == nil && !s.readersCarryTiers() && s.tierRouted(t) && (len(s.Routes) == 0 || t == cardhdr.RouteFlash) {
		return true
	}
	tier, n := s.readTierOf(pr), 0
	for _, rd := range s.Readers.Rows() {
		if s.ReaderIsUp(rd) && s.readerServesTier(rd, tier) {
			n++
		}
	}
	placed, oks, _ := friendReadLive(s, pr)
	return n+len(placed)+len(oks) >= ReadsNeededIn(s, pr)
}

// ScriptReadPrefix begins the finding of an ok read a script reader gave: the reader ran
// the card's SCRIPT program at the attempt's start commit and its diff was the head's,
// byte for byte (docs/SPEC-SPRINT.md, the script read; member.ScriptVerifier). It is the
// member's own constant, so the finding a reader writes is the one counted here.
const ScriptReadPrefix = member.ScriptReadPrefix

// scriptVerified says the primary is a script card (CLASS: script, with its SCRIPT
// program) with an ok read at its current attempt and head whose finding is a script
// read's: that one read counts as every read the card needs. A script read that found
// a difference gives no verdict (the member goes on to read the card as a model reader),
// so a card whose head the program did not make is read by models as any card is, and
// no read is accepted on the worker's word: the reader ran the program itself.
func scriptVerified(s *Snapshot, pr *Card) bool {
	if c, _ := cardhdr.ReadClass(pr.F("brief")); !c.IsScript() {
		return false
	}
	for _, c := range readsAt(s, pr, pr.Int("attempt")) {
		if c.Col == OK && c.F("head") == pr.F("head") && ReadCardAgrees(c) && strings.HasPrefix(c.F("finding"), ScriptReadPrefix) {
			return true
		}
	}
	return false
}

// acceptable says the primary has ok reads from ReadsNeededIn different readers
// at its current attempt and head (okReaders), or one script read of a script card
// (scriptVerified). With no read needed (set --reads 0) its work's finish is the
// evidence: the work came back LAND at a head.
func acceptable(s *Snapshot, pr *Card) bool {
	need := ReadsNeededIn(s, pr)
	if need == 0 {
		return pr.F("result") != "failed" && pr.F("head") != ""
	}
	return len(okReaders(s, pr)) >= need || scriptVerified(s, pr)
}

// placedReadsAt is the primary's placed read cards at an attempt,
// checking both the plain identity and the second identity (.g1).
func placedReadsAt(s *Snapshot, pr *Card, attempt int) []*Card {
	if !anyReadCardAt(s, pr, attempt) {
		return nil
	}
	var out []*Card
	for _, r := range s.Readers.Rows() {
		for _, id := range ReadCardIDs(pr.ID, attempt, r) {
			if c := s.Readers.Placed(id); c != nil {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// anyReadCardAt says the readers table holds a read card of the primary's attempt,
// placed or kept: every one's id, plain or second, begins with ReadCardID(primary,
// attempt, ""). Most primaries a step asks of have none, and are passed over without
// building every reader's two identities.
func anyReadCardAt(s *Snapshot, pr *Card, attempt int) bool {
	return s.Readers.AnyWithPrefix(ReadCardID(pr.ID, attempt, ""))
}

// liveReadsAt is the primary's placed read cards at an attempt less the reads
// the ask takes back or places again: the reads that stand.
func liveReadsAt(s *Snapshot, pr *Card, attempt int) []*Card {
	var out []*Card
	for _, rc := range placedReadsAt(s, pr, attempt) {
		if !awayRead(s, rc) && !returnedRead(rc) {
			out = append(out, rc)
		}
	}
	return out
}

// returnedReadsAt is the primary's read cards at an attempt handed back with
// no verdict by readers up (returnedRead): the ask places each again, on a
// free reader with room, or in place on its own reader, whose room already
// holds it (readerLoad counts it).
func returnedReadsAt(s *Snapshot, pr *Card, attempt int) []*Card {
	var out []*Card
	for _, rc := range readsAt(s, pr, attempt) {
		if !awayRead(s, rc) && returnedRead(rc) {
			out = append(out, rc)
		}
	}
	return out
}

// ReadCardForAsk returns the read card ID to use when asking a reader of a primary
// at an attempt: plain identity if no card exists yet, or second identity if a card
// retired without a verdict (away, refused, returned, levelled, held, taken back)
// exists with the plain identity and no second card exists yet.
// ok is true if the reader is eligible to be asked.
func ReadCardForAsk(s *Snapshot, primary string, attempt int, reader string) (id string, ok bool) {
	plain := ReadCardID(primary, attempt, reader)
	existing := s.Readers.Card(plain)
	if existing == nil {
		return plain, true
	}
	// Workaround (the coordinator, 2026-10-06 7:30 PM ET; the owner: "fix it now, to work around it"): a
	// read card retired for any reason without a verdict (away, refused, returned,
	// levelled, taken back by a hold or a reader restart) leaves the reader askable again
	// under the second identity; before, only away and refused did, and tonight's reader
	// restarts spent most readers on most cards. Read cards replace this path next.
	if by := existing.F("retired_by"); by != "" && existing.F("verdict") == "" {
		second := ReadCardSecondID(primary, attempt, reader)
		if s.Readers.Card(second) == nil {
			return second, true
		}
	}
	return plain, false
}

// freeReaders is the readers the ask may ask the primary's attempt of: up,
// serving the primary's tier (readerServesTier; an empty tiers cell reads every
// tier, and a fleet reader serves only a tier it can draw a route of), with no
// read card of it at the attempt, placed or retired with a verdict (a reader
// with one has read it). When a card retired without a verdict exists with the
// plain identity and no second card exists yet, the reader is eligible to be
// re-asked under second identity .g1 (ReadCardForAsk): an away-retired card and
// a refused card (RetiredByRefused) alike. The next attempt is read on new
// cards, by every reader of the tier.
func (s *Snapshot) freeReaders(pr *Card, attempt int) []string {
	tier := s.readTierOf(pr)
	var out []string
	for _, rd := range s.Readers.Rows() {
		if s.ReaderIsUp(rd) && s.readerServesTier(rd, tier) {
			if _, ok := ReadCardForAsk(s, pr.ID, attempt, rd); ok {
				out = append(out, rd)
			}
		}
	}
	return out
}

// ReadsWanted is how many reads the ask places on the primary now, at its
// attempt. Under the interim rule of 2026-10-06 (docs/SPEC-SPRINT.md section 6;
// the owner, 6:02 PM ET: "send out multiple consumer cards in ||") a card's reads
// are asked together: the rest it needs (ReadsNeededIn), a read outstanding counted
// among those that stand; none once a read found it broken (the judgment stands
// and a rework follows). The sequential rule before it (2026-10-04) asked the
// first read alone and the rest once it came back ok. A read
// taken back from a reader away or handed back with no verdict (liveReadsAt)
// does not stand and is asked again whatever stands: it was wanted when it
// was placed (a pair's second read by --another too). Each read wanted goes
// to a reader with room (askPicks): how many are wanted is this rule, where
// they go is the readers' room. A friend's read of the attempt stands with
// them (friendReadLive): placed or ok, it counts toward ReadsNeeded; broken, no second
// read is asked (docs/SPEC-SPRINT.md, a read asked of any unit with room at
// or above the read tier).
func ReadsWanted(s *Snapshot, pr *Card) int {
	if readBranchMissingOpen(s, pr) {
		return 0 // its branch is not on origin: the seat's judgment holds it (read_missing.go)
	}
	placed := readsAt(s, pr, pr.Int("attempt"))
	live := liveReadsAt(s, pr, pr.Int("attempt"))
	fp, fok, fbr := friendReadLive(s, pr)
	placed = append(placed, fp...)
	live = append(append(append(live, fp...), fok...), fbr...)
	return max(readsWantedOf(s, pr, live), len(placed)-len(live))
}

// readsWantedOf is ReadsWanted over the reads that stand, live.
func readsWantedOf(s *Snapshot, pr *Card, live []*Card) int {
	// Workaround (the coordinator, 2026-10-06 7:47 PM ET; the owner: 6:02 PM: "send out multiple consumer
	// cards in ||"): a card's reads are asked together, not one after the other; a read
	// outstanding counts toward the reads it needs, and only a broken read stops the rest.
	// Read cards replace this.
	for _, rc := range live {
		if rc.Col == Broken {
			return 0
		}
	}
	return max(0, ReadsNeededIn(s, pr)-len(live))
}

// FieldFindingReader is the primary's field naming the reader whose finding its
// last rework sent back (the first broken read's reader at that attempt, in
// reader row order; Rework): the next attempt's first read is asked of them
// (Ask, finderFirst). Absent when the rework was of failed work.
const FieldFindingReader = "finding_reader"

// FieldFinderRead marks a read card asked of the finder out of turn (finderFirst):
// placed on purpose, the level leaves it where it is (levelReads).
const FieldFinderRead = "finder"

// finderFirst is the reader the primary's first read at attempt is asked of out
// of turn: the reader whose finding the attempt's fix answers (FieldFindingReader
// at FieldFindingAttempt, the attempt before), so the check is against the finding
// and not a fresh opinion (docs/SPEC-SPRINT.md section 6; the owner, 2026-10-04),
// when that reader is free at the attempt (free: up, with no card at it) and has
// free room under its machine's width (room, readerRooms); "" otherwise, and the
// room chooses. The second reader stays fresh: only the first read is the finder's.
func finderFirst(c *Card, attempt int, free []string, room map[string]readerRoom) string {
	rd := c.F(FieldFindingReader)
	if rd == "" || c.Int(FieldFindingAttempt) != attempt-1 || !contains(free, rd) || room[rd].free <= 0 {
		return ""
	}
	return rd
}

// askFinders is the finders the ask asks out of turn, by primary, over the
// primaries it asks in the order it asks them: each the primary's finder
// (finderFirst) when the primary has no read placed at its attempt (its
// first read; not for --another) and the finder is free with room, the read
// taken off the finder's room at once. The finders are placed before any
// other read of the step, so every read after them goes by the room they
// left and the ask's placement stays the level's fixed point: a finder read
// is never levelled, and a reader it fills is not also given reads its room
// no longer holds (docs/SPEC-SPRINT.md section 6; the model is
// tla/ReadsByRoom.tla FinderFirst).
func (s *Snapshot) askFinders(cards []*Card, another bool, room map[string]readerRoom) map[string]string {
	finders := map[string]string{}
	if another {
		return finders
	}
	for _, c := range cards {
		attempt := c.Int("attempt")
		if len(readsAt(s, c, attempt)) > 0 {
			continue
		}
		if f := finderFirst(c, attempt, s.freeReaders(c, attempt), room); f != "" {
			finders[c.ID] = f
			room[f] = room[f].after(1)
		}
	}
	return finders
}

// askPicks is the readers the ask asks of the primary now, want of them, at
// most: its finder first (askFinders, whose room the finder's read already
// took), then the rest each the free reader with the greatest share of room
// (round.pickByRoom), so the reads wanted (ReadsWanted) go
// where the machines' widths have room (docs/SPEC-SPRINT.md section 6, the
// reads, sequential and by room; the model is tla/ReadsByRoom.tla and the
// reference model's AskChoice). Every read picked is taken off its reader's
// room, so the room, not a turn count, keeps the readers' loads even. It
// moves no index: the ask moves it past the readers it picked in turn, never
// the finder (an out-of-turn read leaves the round where it was).
func askPicks(rr *round, finder string, want int, free []string, room map[string]readerRoom) []string {
	if want <= 0 {
		return nil
	}
	var picked []string
	if finder != "" {
		picked = append(picked, finder)
		free = without(free, []string{finder})
	}
	return append(picked, rr.pickByRoom(want-len(picked), free, room)...)
}

// sweepReads is the readers' rebalance safety: every read asked or reading of a
// reader that is not up is taken back, retired as the ask takes back a read
// asked of a reader away
// (retired_by away: that reader keeps its card at the attempt, so it is not
// asked that attempt again), and the tick's ask asks it of the readers up. A
// read stays where it is when the ask could not place it (fewer readers up
// than its primary needs, ReadsNeeded, or none up without a card at its
// attempt): it is judged while its reader is away and read when the reader is
// back (read_return_test.go). A snapshot with no reader states holds every
// reader up: nothing moves.
func sweepReads(s *Snapshot, p *Plan) {
	up := s.UpReaders()
	if s.ReaderStates == nil || len(up) == 0 {
		return
	}
	taker := func(c *Card) bool {
		pr := s.Work.Card(c.F("primary"))
		if pr == nil || !enoughReadersUp(s, pr) {
			return false
		}
		tier := s.readTierOf(pr)
		for _, rd := range up {
			if !s.readerServesTier(rd, tier) {
				continue
			}
			if _, ok := ReadCardForAsk(s, c.F("primary"), c.Int("attempt"), rd); ok {
				return true
			}
		}
		return false
	}
	for _, rd := range s.Readers.Rows() {
		if s.ReaderIsUp(rd) {
			continue
		}
		cards := append([]*Card{}, s.Readers.Cell(rd, Asked)...)
		if s.ReaderStates[rd] != ReaderHeld {
			// a held reader's reads begun finish (hold.go); an away or down one's go
			cards = append(cards, s.Readers.Cell(rd, Reading)...)
		}
		SortCards(cards)
		for _, c := range cards {
			if !taker(c) {
				continue
			}
			p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"),
				Changes: []Change{change(Readers, removeEntry(c, map[string]string{"retired": stamp(s.Now), "retired_by": "away"}))},
				Moved:   fmt.Sprintf("%s %s:%s -> taken back (%s is %s); the ask asks it of a reader up", c.ID, rd, c.Col, rd, orDash(s.ReaderStates[rd]))})
		}
	}
}

// RetiredByLevel is a read card's retired_by when the tick's level moved its
// read, asked and not begun, to another reader (levelReads).
const RetiredByLevel = "level"

// TickLevelReads is the readers' rebalance, once at the start of every tick,
// before any other part: levelReads, in one plan.
func TickLevelReads(s *Snapshot, _ TickReq) (Plan, int) {
	var p Plan
	levelReads(s, &p)
	return bound(p)
}

// ReaderWidth is a reader's width, the most reads it runs at once: its
// machine's fleet row's (ReaderPrefix: reader-<m> runs at m's width, the width
// the reader itself reads with its queue every tick), so the ask and the level
// know it too (the owner, 2026-10-03: with widths 4/8/16/16/24 the count-levelled
// reads queued seven deep on the two narrow machines while 31 reader slots sat
// idle). A reader named for no fleet row, or read with no fleet table, keeps
// the unbounded room it had: math.MaxInt, so it is never at width and its room
// is ordered by its load alone.
func (s *Snapshot) ReaderWidth(reader string) int {
	m, ok := ReaderMachine(reader)
	if !ok || s.Fleet == nil {
		return math.MaxInt
	}
	ctl := s.MemberCtl(m)
	if ctl == nil {
		return math.MaxInt
	}
	return MemberWidth(ctl)
}

// readerLoad is a reader's reads asked and reading together.
func (s *Snapshot) readerLoad(reader string) int {
	return s.Readers.Count(reader, Asked) + s.Readers.Count(reader, Reading)
}

// readerRoom is a reader's room: its width (ReaderWidth) and its free room,
// the width less its load (readerLoad), below zero for a reader over its
// width. The ask and the level compare rooms by share, free room as a part of
// width, so a machine of 4 and one of 24 are each filled to the same fraction
// (ten reads: the 24 takes eight), never by count alone.
type readerRoom struct{ width, free int }

// roomParts is the parts a share is counted in.
const roomParts = 1 << 20

// share is the reader's free room as parts of its width (roomParts when idle,
// below zero when over width); a reader with unbounded width (no fleet row)
// counts roomParts less its load, so such readers order by load alone, below
// an idle reader with a width and above any that has begun to fill.
func (r readerRoom) share() int {
	if r.width == math.MaxInt {
		return roomParts - (r.width - r.free)
	}
	return r.free * roomParts / r.width
}

// after is the room with n more reads placed on it.
func (r readerRoom) after(n int) readerRoom { return readerRoom{width: r.width, free: r.free - n} }

// readerRooms is each reader's room (readerRoom). The ask gives a read to the
// reader with the greatest share (round.pickByRoom, taking the reads it places
// off the room as it goes) and the level moves reads toward it
// (round.levelToRoom); a reader with no free room is given nothing by either,
// and a reader whose beat says it starts no read (Snapshot.NoRoom) has none.
func (s *Snapshot) readerRooms(readers []string) map[string]readerRoom {
	room := make(map[string]readerRoom, len(readers))
	for _, rd := range readers {
		w := s.ReaderWidth(rd)
		free := w - s.readerLoad(rd)
		if s.NoRoom[rd] != "" && free > 0 {
			// its beat says it starts no read (its disk under its floor): no free room, so
			// it is asked nothing until its beat says it starts reads again
			free = 0
		}
		room[rd] = readerRoom{width: w, free: free}
	}
	return room
}

// levelReads evens the up readers' room, the fleet's level (level) in the
// readers' shape: a reader's room is its width less its load, reads asked and
// reading together, compared as a share of its width (readerRoom), and a
// reader named for no fleet row has unbounded room, so among such readers the
// share differs by the load alone. While the reader with the least share that
// holds an asked read not yet levelled (over its width when the share is
// below zero) could hand one to a reader with free room whose share would
// still not fall under its own, its newest such read (the last in work order)
// moves to the reader with the greatest share, ties the first round the
// readers from the ask's index (askRound, round.levelToRoom), the index moved
// past it as the ask moves it. So no reader up has free lanes while another
// holds a backlog, no move fills a reader past its width (a reader at width is
// given nothing), and no read is shuttled back: the ask's placement is the
// level's fixed point. A read moves only to a reader with no card at
// its primary's attempt, placed or retired: a primary's two reads stay with
// two different readers, and no reader is asked an attempt it already read.
// The move retires the read card (retired_by level: the reader it left is
// never asked that attempt again) and asks the read of the other reader at
// the same attempt and head, its route kept, in the readers table only,
// marked leveled: a read is moved at most once, so a late read is not asked
// afresh on reader after reader. A read begun stays with its reader; a reader
// that is not up is neither a source nor a target: sweepReads takes its reads
// back first. Each move takes a read off a queue, so the level ends.
func levelReads(s *Snapshot, p *Plan) {
	sweepReads(s, p)
	up := s.UpReaders()
	if len(up) < 2 {
		return
	}
	queues := map[string][]*Card{}
	for _, rd := range up {
		for _, c := range s.Readers.Cell(rd, Asked) {
			if c.F(FieldLeveled) == "" && c.F(FieldFinderRead) == "" { // a finder's read is placed on purpose
				queues[rd] = append(queues[rd], c)
			}
		}
		SortCards(queues[rd])
	}
	room := s.readerRooms(up)
	rr := askRound(s)
	moves := roundMoves{}
	planned := map[string]bool{} // the read cards this plan already creates: none is created twice
	for {
		long := ""
		for _, rd := range up {
			if len(queues[rd]) > 0 && (long == "" || room[rd].share() < room[long].share()) {
				long = rd
			}
		}
		if long == "" {
			break
		}
		q := queues[long]
		i, to := len(q)-1, ""
		for ; i >= 0 && to == ""; i-- {
			avoid := []string{long}
			pr := s.Work.Card(q[i].F("primary"))
			tier := ""
			if pr != nil {
				tier = s.readTierOf(pr)
			}
			for _, rd := range up {
				targetID, ok := ReadCardForAsk(s, q[i].F("primary"), q[i].Int("attempt"), rd)
				if !ok || planned[targetID] || (tier != "" && !s.readerServesTier(rd, tier)) {
					avoid = append(avoid, rd)
				}
			}
			to = rr.levelToRoom(up, room, long, avoid)
		}
		if to == "" {
			break
		}
		i++
		c := q[i]
		room[long] = room[long].after(-1)
		room[to] = room[to].after(1)
		queues[long] = append(q[:i:i], q[i+1:]...)
		moves[c.ID] = to
		fields := movedReadFields(c, to, s.Now)
		targetID, _ := ReadCardForAsk(s, c.F("primary"), c.Int("attempt"), to)
		planned[targetID] = true
		p.Units = append(p.Units, Unit{Key: c.ID, Stream: c.F("stream"), Changes: []Change{
			change(Readers, removeEntry(c, map[string]string{"retired": stamp(s.Now), "retired_by": RetiredByLevel})),
			change(Readers, createEntry(targetID, to, Asked, c.Score, fields)),
		}, Moved: fmt.Sprintf("%s %s:asked -> %s:asked (%s)", c.ID, long, to, targetID)})
	}
	roundWrites(p, rr, moves)
}

// movedReadFields is the fields of the card a read moved by the level is asked
// on: the read's own (its primary, stream, attempt, head and route) for the
// reader it goes to, asked now, marked leveled, and none of its run on the reader it left: not
// returned, not reasked (the new reader's bound starts at zero), no
// read_take_<n> and no usage. The card it leaves is retired with every field it
// had, and a returned run's cost is the primary's (cost_record:<card>#r<n>,
// cost.go): the move loses none of it.
func movedReadFields(c *Card, to string, now time.Time) map[string]string {
	fields := map[string]string{}
	for k, v := range c.Fields {
		switch {
		case k == FieldReturned, k == FieldReasked, k == FieldUsage, strings.HasPrefix(k, FieldReadTake):
			continue
		}
		fields[k] = v
	}
	fields["reader"], fields["asked"], fields[FieldLeveled] = to, stamp(now), "1"
	return fields
}

// DefaultReadLease is how long an in-flight read's lease stands without renewal (10m).
const DefaultReadLease = 10 * time.Minute

// FieldLease is a read card's lease expiration timestamp.
const FieldLease = "lease"

// RetiredByLapsed is a read card's retired_by when its lease lapsed and it was taken back on restart.
const RetiredByLapsed = "lapsed"

// ReadLeaseExpires returns when an in-flight read's lease expires.
// Started by read --begin (begun + DefaultReadLease) and renewed by reader beat (FieldLease).
func ReadLeaseExpires(c *Card) time.Time {
	if c == nil {
		return time.Time{}
	}
	if s := c.F(FieldLease); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
	}
	if s := c.F("begun"); s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t.Add(DefaultReadLease)
		}
	}
	return time.Time{}
}

// ReadLeaseLive reports whether the in-flight read's lease is live at now.
func ReadLeaseLive(c *Card, now time.Time) bool {
	exp := ReadLeaseExpires(c)
	if exp.IsZero() {
		return false
	}
	return !now.After(exp)
}

// RestartReads is the server restart plan (the model is tla/ServerLanes.tla,
// Restart, LiveLeaseNeverTakenBack, EveryLapsedReadTakenBack): on server start,
// keep every in-flight read whose lease is live, and only take back reads whose
// lease has lapsed (retired_by lapsed).
func RestartReads(s *Snapshot) Plan {
	var p Plan
	if s.Readers == nil {
		return p
	}
	for _, rd := range s.Readers.Rows() {
		cards := s.Readers.Cell(rd, Reading)
		SortCards(cards)
		for _, c := range cards {
			if ReadLeaseLive(c, s.Now) {
				continue
			}
			p.Units = append(p.Units, Unit{
				Key:    c.ID,
				Stream: c.F("stream"),
				Changes: []Change{
					change(Readers, removeEntry(c, map[string]string{
						"retired":    stamp(s.Now),
						"retired_by": RetiredByLapsed,
					})),
				},
				Moved: fmt.Sprintf("%s %s:%s -> taken back (lease lapsed); the ask asks it of a reader up", c.ID, rd, c.Col),
			})
		}
	}
	return p
}

// ServerRestart is an alias for RestartReads.
func ServerRestart(s *Snapshot) Plan {
	return RestartReads(s)
}

// RenewReaderLeases renews the lease of every in-flight read on reader to now + DefaultReadLease.
func RenewReaderLeases(s *Snapshot, reader string) Plan {
	var p Plan
	if s.Readers == nil {
		return p
	}
	cards := s.Readers.Cell(reader, Reading)
	SortCards(cards)
	for _, c := range cards {
		exp := s.Now.Add(DefaultReadLease)
		p.Units = append(p.Units, Unit{
			Key:    c.ID,
			Stream: c.F("stream"),
			Changes: []Change{
				change(Readers, setEntry(c, map[string]string{
					FieldLease: stamp(exp),
				})),
			},
			Moved: fmt.Sprintf("%s lease renewed until %s", c.ID, stamp(exp)),
		})
	}
	return p
}

// The coordinator's heavy read (docs/SPEC-SPRINT.md section 6,
// accept-heavy-verdict-b.w1): accept --heavy records the coordinator's own read
// of the primary's attempt and head on the primary, under coordinator:<actor>,
// with the evidence file it read and that file's sha256, and it counts as one ok
// read toward the card's read rule (ReadsNeeded). It is never a reader's read: no
// read card is written for it, no reader row names it, and the readers field
// names the readers' oks alone. A reader's broken read at the same attempt stays
// as the reader left it, named on the primary as overruled.
const (
	CoordinatorReaderPrefix = "coordinator:"
	HeavyKind               = "heavy"

	FieldHeavyReader    = "heavy_reader"
	FieldHeavyKind      = "heavy_kind"
	FieldHeavyVerdict   = "heavy_verdict"
	FieldHeavyEvidence  = "heavy_evidence"
	FieldHeavySHA       = "heavy_evidence_sha256"
	FieldHeavyReason    = "heavy_reason"
	FieldHeavyAttempt   = "heavy_attempt"
	FieldHeavyHead      = "heavy_head"
	FieldHeavyAt        = "heavy_at"
	FieldHeavyOverrules = "heavy_overrules"
)

// heavyWhy is the refusal of an accept --heavy that does not carry its evidence:
// the path of the file the coordinator read, its sha256 and the reason.
func heavyWhy(r AcceptReq) string {
	var why []string
	if r.Evidence == "" {
		why = append(why, "--evidence <path>, a readable file the coordinator's heavy read rests on")
	}
	if r.Evidence != "" && r.EvidenceSHA == "" {
		why = append(why, "the evidence file's sha256 (the file read)")
	}
	if r.Reason == "" {
		why = append(why, "--reason <text>, why the coordinator's read stands")
	}
	if len(why) == 0 {
		return ""
	}
	return "the coordinator heavy read wants " + strings.Join(why, ", ")
}

// heavyRead is the coordinator's heavy read of the primary at its attempt and
// head, as fields of the primary: who read it (coordinator:<actor>), its kind
// and verdict, its evidence and the broken reads at the attempt it overrules.
func heavyRead(s *Snapshot, pr *Card, r AcceptReq) (fields map[string]string, overruled []*Card) {
	var ids []string
	for _, rc := range readsAt(s, pr, pr.Int("attempt")) {
		if rc.Col == Broken {
			overruled = append(overruled, rc)
			ids = append(ids, rc.ID)
		}
	}
	who := r.Who
	if who == "" {
		who = "coordinator"
	}
	return map[string]string{
		FieldHeavyReader: CoordinatorReaderPrefix + who, FieldHeavyKind: HeavyKind, FieldHeavyVerdict: "ok",
		FieldHeavyEvidence: r.Evidence, FieldHeavySHA: r.EvidenceSHA, FieldHeavyReason: r.Reason,
		FieldHeavyAttempt: pr.F("attempt"), FieldHeavyHead: pr.F("head"), FieldHeavyAt: stamp(s.Now),
		FieldHeavyOverrules: strings.Join(ids, ","),
	}, overruled
}
