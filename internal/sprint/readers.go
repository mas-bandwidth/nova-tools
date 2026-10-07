package sprint

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/member"
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
		out = append(out, r+" "+st)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// NFewReaders is the tick's judgment that fewer readers are up than a primary
// in review waiting to be asked needs (two for a pro card, one for a flash
// card; ReadsNeeded): the ask raises it once, and asks no absent reader.
const NFewReaders = "fewer than two readers up"

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
	if len(s.freeReaders(pr, attempt))+len(returnedInTier(s, pr, attempt)) >= ReadsNeeded(pr)-len(liveReadsAt(s, pr, attempt)) {
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

// ReadsNeeded is how many different readers' ok reads at its head make the
// primary acceptable, and so how many readers the ask asks at an attempt: one
// when the tier the card is on (cardTier) is flash, and two at any stronger tier
// (pro, or frontier). A friend whose class is at or above the read tier is asked
// first when she has room (friend_read.go); a machine read is drawn on a route of
// the card's read tier (readTierOf) only when no such friend has room (the owner,
// 2026-10-02, cost rule 4, nova-tools#5174: "Reads: one cold read per flash
// card on a flash route; two per pro card; readers still equal workers per
// machine"). The tier is the card's own, the tier it is on (cardTier: flash first,
// then the tier it escalated to, or the tier a rework recorded), never a setting, so
// a card in merging or landed is held to the count it was accepted on.
func ReadsNeeded(pr *Card) int {
	m, _ := cardhdr.ReadModel(pr.F("brief"))
	if cardTier(pr, m) == cardhdr.RouteFlash {
		return 1
	}
	return 2
}

// enoughReadersUp says as many readers of the primary's tier are up as it needs
// (ReadsNeeded). A reader counts only when it serves that tier (readerServesTier:
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
	return n+len(placed)+len(oks) >= ReadsNeeded(pr)
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

// acceptable says the primary has ok reads from ReadsNeeded different readers
// at its current attempt and head (okReaders), or one script read of a script card
// (scriptVerified).
func acceptable(s *Snapshot, pr *Card) bool {
	return len(okReaders(s, pr)) >= ReadsNeeded(pr) || scriptVerified(s, pr)
}

// placedReadsAt is the primary's placed read cards at an attempt,
// checking both the plain identity and the second identity (.g1).
func placedReadsAt(s *Snapshot, pr *Card, attempt int) []*Card {
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

// ReadsWanted is how many read cards the deal cuts for the primary now (readCardsWanted):
// the rest of the reads it needs, at once, none once a read found it broken, none for work
// that failed. The one ask: read cards are cut when a primary enters review.
func ReadsWanted(s *Snapshot, pr *Card) int { return readCardsWanted(s, pr, nil) }

// FieldFindingReader is the primary's field naming the reader whose finding its
// last rework sent back (the first broken read's reader at that attempt, in
// reader row order; Rework): the next attempt's first read is asked of them
// (Ask, finderFirst). Absent when the rework was of failed work.
const FieldFindingReader = "finding_reader"

// FieldFinderRead marks a read card asked of the finder out of turn (finderFirst):
// placed on purpose, the level leaves it where it is (levelReads).
const FieldFinderRead = "finder"

// RetiredByLevel is a read card's retired_by when the tick's level moved its
// read, asked and not begun, to another reader (levelReads).
const RetiredByLevel = "level"

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
	// a read card's broken verdict, retired on the fleet table (read_cards.go)
	if s.Fleet != nil {
		_, _, fbr := friendReadLive(s, pr)
		for _, syn := range fbr {
			if rc := s.Fleet.Card(syn.ID); rc != nil {
				overruled = append(overruled, rc)
				ids = append(ids, rc.ID)
			}
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
