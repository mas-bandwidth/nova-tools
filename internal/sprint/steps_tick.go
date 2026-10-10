package sprint

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"
)

// The machine's tick (docs/SPEC-SPRINT.md, "The machine"). While the machine
// is RUNNING, one tick a second performs every mechanical move that is due,
// in a fixed order, each part bounded so one tick stays short, and writes the
// judgments the coordinator needs, each once. Every part is a pure function
// of an observed snapshot: it calls the existing steps (resolve, resume,
// start's dealing, level, ask, check) and returns their plan. The store
// binding runs each part as one operation of the engine, on a fresh read, so
// a later part sees what an earlier part moved; running the tick twice in a
// row changes nothing the second time.

// MachineActor is who the tick's moves and notifications are recorded as.
const MachineActor = "machine"

// The bounds of one tick, and the one queue length the dealing keeps.
const (
	// TickMaxMoves bounds the units one part of a tick applies; the rest are
	// due, and the next tick moves them. It is the deal's bound, TickMaxDeal
	// (width.go): one table-layer write's member candidates, which the step
	// builder cuts into parts under the table layer's entry bounds, so a
	// part plans every row that needs it in the one tick and never lags its
	// own work (every row of every
	// table moves every tick, never a row at a time).
	TickMaxMoves = TickMaxDeal
)

// Deadlines, each against running time: time the machine was STOPPED does
// not count.
const (
	DeadlineUnfinished = 2 * time.Hour    // a work card taken and not finished (a card dealt and never taken: the dealt bound, settings.go)
	DeadlineUnbegun    = 30 * time.Minute // a read card asked and not begun
	DeadlineUnreported = 2 * time.Hour    // a read card begun and not reported
	DeadlineMergeIdle  = 30 * time.Minute // a stream with cards to merge and no merge step
	// DeadlineJudgment is how long a judgment stays open, in running time,
	// before the tick marks it overdue; a review time the coordinator set
	// (wait) is the judgment's own due time instead.
	DeadlineJudgment = 10 * time.Minute
)

// MaxRedeals is how many times one attempt's work card is dealt again after
// a take of it ended without a finish, its member down or away while the card
// was working (its redeals counter, which no take resets). A card that was
// ready, never taken, is dealt again and keeps its count: a card is retired
// for repeated failure at members, never for members flapping while it sat
// ready. A working card whose member goes down with its count at the bound
// stays withdrawn, and the bound's judgment names it (docs/SPEC-SPRINT.md
// section 2, the work card's redeals; tla/DirtyTick.tla RedealsAreEndedTakes
// and RedealBoundHolds).
const MaxRedeals = 3

// FieldTakeEnded is the work card's mark that a take of it ended without a
// finish and it was withdrawn (no member had room, or it is at its bound):
// the deal that places it again counts that take, and unsets the mark.
const FieldTakeEnded = "take_ended"

// A take the provider failed ends like any take that ended without a finish: the card
// is withdrawn with FieldTakeEnded and dealt again, never judged as failed work
// (docs/SPEC-SPRINT.md, the work card's redeals; tla/CardContract.tla, ProviderFailure;
// tla/DirtyTick.tla, RedealsAreEndedTakes). The work card keeps the last failure's line
// (FieldProviderError, for the bound's judgment, cleared when it is dealt again) and a
// record of every take the provider failed (FieldProviderTake, ProviderTake: the record
// of that take, which `card` prints and the route stats count).
const (
	FieldProviderError = "provider_error"
	FieldProviderTake  = "provider_take_" // + the take's number, redeals + 1 when it ended
)

// FieldStagingTake is the record of a launch its member refused at staging, keyed by the
// generation refused (ProviderTake's shape: the route, the member, when, the reason). A
// staging refusal spends none of the redeal bound: its own bound is the fleet, each member
// refusing an attempt's card at most once, the deal never placing it on a member that
// refused it (tla/CardContract.tla, StageRefused, Restage and StagingBound).
const FieldStagingTake = "staging_take_"

// MaxProviderErrorBytes bounds the line FieldProviderError keeps.
const MaxProviderErrorBytes = 200

// FieldReturnedAttempt is the primary's attempt when the coordinator last
// returned it to review (return): at that attempt the pump does not accept
// it, and the judgment "returned to review" decides it.
const FieldReturnedAttempt = "returned_attempt"

// The tick's own notification types.
const (
	NResumed    = "stream resumed: the card it needed landed"
	NCannotAsk  = "cannot ask"
	NNoMember   = "no fleet member is up"
	NInvariant  = "an invariant is broken"
	NWorkLate   = "a work card is past its deadline"
	NReadLate   = "a read card is past its deadline"
	NMergeLate  = "a stream has had no merge step past its deadline"
	NBound      = "a card reached its bound"
	NStarving   = "the fleet is starving"  // ready under twice the width while a wave is held (heldWave)
	NOverloaded = "a member is overloaded" // three timeouts within the window (overload.go)
	// NReadersBehind (readers_behind.go): reads asked and not begun for the window
	// NOverdue (notes.go) is the overdue line: a happened note, once per
	// judgment, when the judgment passes its due time.

	NMachineStarted = "the machine started"
	NMachineStopped = "the machine stopped"
)

// Sentinel is the kind of a card that marks a point in a stream: the tick
// never moves it from waiting or ready.
const Sentinel = "sentinel"

// ReworkOnAHigherTier is the bound's rework when the attempt before also ended at its bound on
// the card's tier (failure.go, reworkAtTheSameBound): Rework takes it only with --tier naming a
// tier above (boundDecisions).
const ReworkOnAHigherTier = "rework with a fix on a higher tier"

// TickDecisions are the decisions open to the tick's judgments.
var TickDecisions = map[string][]string{
	NFriendSyncFailing: {"ack", "wait"},
	NBound:             {"rework with a fix", "drop", "wait"},
	NCannotAsk:         {"reader add", "rework", "drop", "wait"},
	NFewReaders:        {"reader up", "reader add", "wait"},
	NNoMember:          {"fleet beat", "fleet up", "wait"},
	NAdoptFailed:       {"fleet up <m>", "wait"},                     // named per member (fleet_back.go)
	NStarving:          {"release", "wait"},                          // the first held wave's sentinel, never a single card
	NOverloaded:        {"fleet up <m> --width <half>", "wait 15m"},  // named per member (overload.go, Overload.Decisions)
	NReadersBehind:     {"reader up <r>", "restart <r>", "wait 10m"}, // named per reader (readers_behind.go, Behind.Decisions)
	NRaiseReadTier:     {"raise", "keep"},                            // readtier.go
	// the verdicts' ledger (reads_window.go): the per-reader one names its reader
	NBrokenReadsOutrun: {"look at the readers", "raise the read tier", "act"},
	NReaderBreaks:      {"look at the reader", "act"},
	NDevBehind:         {"promoted", "wait 30m"}, // promotion.go
	NNoRoute:           {"route add", "look at the card", "drop", "wait"},
	// a payment and a key are the owner's: no rework is offered (provider_funds.go)
	NProviderFunds:  {"ack", "wait"}, // and "funded <provider>", named per provider (providerConds)
	NProviderLow:    {"ack", "wait"}, // the same
	NProviderKey:    {"ack", "wait"},
	NAllOutOfCredit: {"ack", "wait"},
	NInvariant:      {"look at the card", "repair", "wait"},
	NWorkLate:       {"fleet level", "fleet down <member>", "wait", "drop"},
	NReadLate:       {"ask --another", "wait", "drop"},
	NMergeLate:      {"merge --stream <s>", "look", "wait"},
	NStalled:        {"look at the card", "wait"},
	// the backlog alarms (alarms.go): seen, or quiet for a while
	NAlarmReview:  {"ack", "wait"},
	NAlarmMerging: {"ack", "wait"},
	NAlarmReady:   {"ack", "wait"},
	NAlarmFleet:   {"ack", "wait"},
	// a member's open files over its alarm bound (fd.go): named per member, seen, or quiet a while
	NFilesAlarm: {"fleet up <m> --width <half>", "fleet down <m>", "ack", "wait 15m"},
	// the coordinator's pass (coordinator_pass.go): each names its own
	NFriendDeaf:        {"ack", "wait"},
	NFriendIdle:        {"ack", "wait"},
	NCoordinatorBehind: {"act", "wait"},
	// the drift alarms (drift.go): mended, or quiet for a while; raised again every
	// PassEvery while they hold and not waited
	NDriftAhead:    {"act", "wait"},
	NDriftCardBase: {"act", "wait"},
	NDriftServer:   {"act", "wait"},
	NDriftBaseRed:  {"act", "wait"},
}

// TickReq is what a tick is given beside the snapshot.
type TickReq struct {
	Who string // recorded with every move; "" is MachineActor
	// Stopped is the time the machine was STOPPED between two clock readings:
	// a deadline compares running time only. nil is none.
	Stopped func(from, to time.Time) time.Duration
	// Beats is each fleet member's last beat, read by the binding with the
	// tick: the presence part applies the status it derives. nil is none
	// read, and the presence part does nothing.
	Beats map[string]Beat
	// Started is the machine's first start of the sprint's epoch, the time
	// the done part's note counts from; zero is not known.
	Started time.Time
	// Friends is each friend the deal offers ready work first, read by the binding
	// with every tick while the roster has a friend (friendDealPass, before the residual
	// fleet deal), with what her beat names running (FriendSeat.Running): the tick
	// levels them after its deal (FriendLevel). nil is none: every card is the fleet's
	// but a hard pin, which waits ready, and no friend is levelled.
	Friends []FriendSeat
	// AnswerRules says the tick answers the mechanical judgments by rule (rules.go; run
	// --answer-rules); false leaves every judgment to the coordinator.
	AnswerRules bool
	// IdleAlarm says the tick watches for an idle fleet and tells the coordinator why
	// (idle.go; run --idle-alarm).
	IdleAlarm bool
	// WakeFriend wakes a friend by bus message during the stall ladder (cmd/nova-sprint/friendcards.go).
	WakeFriend func(friend string, rung int, d time.Duration) error
	// SendWidthGoal sends an idle-loaded friend the width goal by bus message (cmd/nova-sprint/friendcards.go).
	SendWidthGoal func(friend string, reads, work, width int, idle int64) error
	// Sessions is each friend's session as her last beat carries it, and whether the
	// coordinator holds her, read by the binding with every tick (coordinator_pass.go);
	// nil is none read, and no friend is deaf.
	Sessions map[string]FriendSession
	// Drift is the repository's drift facts, read by the binding (ReadDrift, with the last
	// whole-tree gate at the base; drift.go, docs/SPEC-SPRINT.md section 8, "Drift alarms"):
	// the deadlines part keeps a judgment for each drift. nil is none read, and no drift
	// judgment is raised or closed.
	Drift *DriftFacts
}

func (r TickReq) who() string {
	if r.Who == "" {
		return MachineActor
	}
	return r.Who
}

// running is the running time since the stamp: the time on the clock less the
// time the machine was STOPPED; ok is false for a stamp that is absent or
// unreadable.
func (r TickReq) running(now time.Time, stampText string) (time.Duration, bool) {
	t, err := time.Parse(time.RFC3339, stampText)
	if err != nil {
		return 0, false
	}
	d := now.Sub(t)
	if r.Stopped != nil {
		d -= r.Stopped(t, now)
	}
	return d, true
}

// TickPartFn is one part of the tick over an observed state: its plan, held
// to the part's bounds, and how many moves and judgments are due past them.
type TickPartFn func(*Snapshot, TickReq) (plan Plan, due int)

// TickPartDef is a part of the tick by name, with its planner.
type TickPartDef struct {
	Name string
	Fn   TickPartFn
}

// TableUpdate is one table's update in a tick: its parts, each an existing
// planner over every row of the table that needs it, run in order.
type TableUpdate struct {
	Table string
	Parts []TickPartDef
}

// PartDrain is the name of the work pump's first part: the work table's
// queue applied in one update (Drain). It needs the queue, which the store
// reads with the part's step: it has no planner here.
const PartDrain = "drain"

// TickTables is the tick's shape: each table gets one update in turn per tick,
// work streams, then readers, merge and fleet. The work table's update is the
// pump, run once a tick: its queue drained, then its cards advanced (a
// waiting card to ready, a ready card to working by the deal, a card queued behind
// lanes that all work to an idle lane of either side by the rebalance (Rebalance), a card in
// review with the ok reads it needs to merging); "no new work moves from waiting ->
// ready -> working except on the FIRST PASS on the work stream table, once
// per-tick". The readers', the merge's and the fleet's updates each write
// their own table, and queue their changes of the work table for the next
// tick's pump; a table another update wrote is updated again, at once, until
// none is ("the tick doesn't end until all dirty bits are cleared"). The
// model is tla/DirtyTick.tla.
var TickTables = []TableUpdate{
	{Work, []TickPartDef{{PartDrain, nil}, {"resolve", TickResolve}, {PartCapDeal, TickCapDeal}, {"deal", TickDeal}, {PartRebalance, TickRebalance}, {"accept", TickAccept}}},
	{Readers, []TickPartDef{{"ask", TickAsk}}},
	{Merge, []TickPartDef{{"resume", TickResume}}},
	{Fleet, []TickPartDef{{"presence", TickPresence}, {PartFriendStall, TickFriendStall}}},
}

// PartCapDeal is the attempt cap's default answer, the pump's part before the deal
// (TickCapDeal, brief_bound.go): a card past its cap goes to a frontier or heavy friend
// with room before the deal could give it to a machine.
const PartCapDeal = "cap deal"

// TickCapDeal is the attempt cap's default answer as a part of the tick (AttemptCapDeal).
func TickCapDeal(s *Snapshot, r TickReq) (Plan, int) { return AttemptCapDeal(s, r), 0 }

// PartFriendStall is the friend stall ladder part (friend_stall.go).
const PartFriendStall = "friend-stall"

// PartLevel and PartLevelReads are the tick start's parts: the fleet's and the
// readers' rebalance.
const (
	PartLevel      = "level"
	PartLevelReads = "level reads"
)

// TickStart is the tick's start, once, before any table's update: the fleet's level
// (ready cards from a member that
// cannot start them to one with free lanes, never past DealAhead times a
// width) and the readers' (asked reads from a reader with a backlog to one
// idle), each one batch. It runs once a tick: a table written again later in
// the tick is updated by its update, never levelled again.
var TickStart = []TickPartDef{{PartLevel, TickLevel}, {PartLevelReads, TickLevelReads}}

// TickEnd is the tick's end, once the tables are settled: what is always
// true held, the deadlines (with the backlog alarms, alarms.go), the overdue
// judgments (with the coordinator's pass, coordinator_pass.go), and the done
// part last. It writes notes, no table.
var TickEnd = []TickPartDef{
	{"check", TickCheck},
	{"deadlines", TickDeadlines},
	{"overdue", TickOverdue},
	{PartDone, TickDone},
}

// TickEndWith is the tick's end with the machine's own answers (run's defaults): with
// rules (TickReq.AnswerRules, run --answer-rules), the rule parts (TickRules) after the
// deadlines, so a judgment the end raises is answered in its own tick, and before the
// overdue part, each a step that may write any table, as a coordinator's verb does, its
// work-table changes queued for the next pump; with idle (TickReq.IdleAlarm, run
// --idle-alarm), the idle alarm (TickIdle) after the overdue part, before the done part.
// With neither the end is TickEnd's alone, as before them. The widen rule's part (widen.go)
// runs before the rule rework, which reworks nothing the widen rule leaves to a mind.
func TickEndWith(rules, idle bool) []TickPartDef {
	out := append([]TickPartDef(nil), TickEnd[:2]...)
	if rules {
		for _, p := range TickRules {
			if p.Name == PartRulePaths {
				p.Fn = TickPathsHold
			}
			if p.Name == PartRuleRework {
				out = append(out, TickPartDef{PartRuleWiden, TickRuleWiden})
				p.Fn = TickRuleReworkUnwidened
			}
			out = append(out, p)
		}
	}
	out = append(out, TickEnd[2])
	if idle {
		out = append(out, TickPartDef{PartIdle, TickIdle})
	}
	return append(out, TickEnd[3:]...)
}

// TickParts is every part with a planner in the order a tick first runs them:
// the start, the four tables' updates, then the end.
var TickParts = func() []TickPartDef {
	out := append([]TickPartDef(nil), TickStart...)
	for _, u := range TickTables {
		for _, p := range u.Parts {
			if p.Fn != nil {
				out = append(out, p)
			}
		}
	}
	return append(out, TickEnd...)
}()

// NReadyToMerge is the notice the pump addresses to the coordinator once a
// tick in which it accepted primaries: a notice, not a decision ("accept is
// mechanical, but the merge step is not"), naming every primary accepted.
const NReadyToMerge = "ready to merge"

// TickAccept is the machine's accept: accept is mechanical, but the merge
// step is the coordinator's. Every primary in review with the ok reads it
// needs at its head (ReadsNeeded), whoever read it (a reader on the readers
// table, a friend on her fleet row: okReaders), moves to merging and into its
// stream's merge queue, in stream turns from the accept's index, in the pump's
// one plan (Accept, the move accept --read-ok makes, the readers named in the
// record). It is never a judgment and never a hand step: the seat is told once
// a tick, one "ready to merge" notice naming every primary the tick accepted
// and the land command of each stream; nothing in it asks for an answer.
//
// A primary a change queued after the drain names (s.Held) is not eligible: it
// waits for the next tick's pump, where the change finds it where it expects
// it (docs/SPEC-SPRINT.md's accept row: review -> merging only on the ok reads
// it needs at the head, and the work table advances only at a tick's pump). It is
// dropped here, before the plan, so the stream's state change and the notice
// are planned for the cards accepted and for no others.
func TickAccept(s *Snapshot, r TickReq) (Plan, int) {
	eligible := func(c *Card) string {
		if c.F("result") == "failed" || !acceptable(s, c) {
			return "not the ok reads it needs"
		}
		if why := AcceptHeld(c); why != "" {
			return why
		}
		if s.Held[c.ID] {
			return "a change is queued for it"
		}
		if m := s.Merge.Card(c.ID); m != nil && (!m.Placed() || m.Col != Returned) {
			return "its merge record is " + placeWord(m)
		}
		if s.StreamCtl(c.Row) == nil {
			return "no merge row"
		}
		return ""
	}
	var ids []string
	for _, c := range eligibleTurns(s.Work.Column(Review), eligible, streamRound(s, PropAcceptStreamIndex)) {
		ids = append(ids, c.ID)
	}
	if len(ids) == 0 {
		return Plan{}, 0
	}
	p := Accept(s, AcceptReq{Sel: Sel{Only: ids}, Who: r.who()})
	if n, ok := acceptedNotice(p, ids, s.Now, r.who(), s.Coordinator); ok {
		p.Notes = append(p.Notes, n)
	}
	return p, 0
}

// acceptedNotice is the one notice of a tick's accept (TickAccept): "ready to
// merge", addressed to the coordinator, naming every primary the plan accepted
// in the order accepted, with the land command of each stream they are in. The
// stream is the note's when the primaries are of one stream. ok is false when the
// plan accepted none.
func acceptedNotice(p Plan, ids []string, now time.Time, who, to string) (Note, bool) {
	var accepted, streams []string
	for _, u := range p.Units {
		if !contains(ids, u.Key) {
			continue
		}
		accepted = append(accepted, u.Key)
		if !contains(streams, u.Stream) {
			streams = append(streams, u.Stream)
		}
	}
	if len(accepted) == 0 {
		return Note{}, false
	}
	sort.Strings(streams)
	stream := ""
	if len(streams) == 1 {
		stream = streams[0]
	}
	n := happened(NReadyToMerge, stream, now, accepted...)
	n.Who, n.To = who, to
	// the thing to run is land (git merges and pushes, then the merge step); merge alone
	// records a landing without touching git; nova-sprint run --land lands by itself
	lands := make([]string, len(streams))
	for i, st := range streams {
		lands[i] = "nova-sprint land --stream " + st
	}
	n.What = fmt.Sprintf("%d accepted and queued to merge: %s; run: %s (a nova-sprint run started with --land lands them itself)", len(accepted), Preview(accepted, " "), strings.Join(lands, "; "))
	return n, true
}

// AcceptHeld is why the pump leaves a primary in review that has the ok reads
// it needs at its head for the coordinator, "" when it
// accepts it (R9's two holds, docs/SPEC-SPRINT.md section 6):
//   - its CI is red at its head (CIRedAtHead): "ci red on a primary" is the
//     coordinator's to decide (rework, return, drop, ack); the coordinator's
//     accept still takes it;
//   - it was returned to review at its attempt (ReturnedAtAttempt): the
//     coordinator sent it back, and "returned to review" decides it (rework,
//     accept, drop); its reads stand, but only a new attempt's own reads are
//     the pump's to accept.
//
// The holder of a primary in review (reviewAccepts) and reviewJudgment read
// the same holds, so no primary in review is silent. The model is
// tla/DirtyTick.tla Held and AcceptHolds.
func AcceptHeld(c *Card) string {
	switch {
	case CIRedAtHead(c):
		return "its CI is red at its head " + orDash(c.F("head"))
	case ReturnedAtAttempt(c):
		return "it was returned to review at attempt " + c.F("attempt")
	}
	return ""
}

// CIRedAtHead says the primary's last CI observation is red, for its current
// head (a red for an older head is not about the work in review).
func CIRedAtHead(c *Card) bool {
	return c.F("ci") == "red" && c.F("ci_head") == c.F("head")
}

// ReturnedAtAttempt says the coordinator returned the primary to review at
// its current attempt.
func ReturnedAtAttempt(c *Card) bool {
	return c.F(FieldReturnedAttempt) != "" && c.Int(FieldReturnedAttempt) == c.Int("attempt")
}

// Empty says a plan writes nothing.
func (p Plan) Empty() bool {
	return len(p.Units) == 0 && len(p.Notes) == 0 && len(p.Closes) == 0 && len(p.Rows) == 0 && len(p.Updates) == 0 && p.Stop == ""
}

// bound keeps the first TickMaxMoves units, and says how many it left out:
// those are due. Notes have no bound but the step's: every judgment a part
// finds is written in its tick (the owner's rule: never a row at a time).
func bound(p Plan) (Plan, int) {
	due := 0
	if len(p.Units) > TickMaxMoves {
		due += len(p.Units) - TickMaxMoves
		p.Units = p.Units[:TickMaxMoves]
	}
	return p, due
}

// T1. TickResolve scans every stream's waiting set in score order, from the
// state, whenever the tick reads the whole sprint: a primary whose every need
// has landed moves to ready; a dropped or missing need is a blocked judgment,
// once. A sentinel is never moved: when everything it needs has landed,
// resolve marks it reached and opens its judgment, and what waits
// behind it stays waiting until the coordinator releases it. No flag says a
// scan is due: what is due is read from the state, so a tick that did not
// finish leaves it due for the next.
//
// The waiting primaries go in stream turns (dealTurns: one from each stream
// in turn, within a stream by work order), so a bound that cuts the plan
// cuts every stream alike.
func TickResolve(s *Snapshot, r TickReq) (Plan, int) {
	var ids []string
	for _, c := range dealTurns(s.Work.Column(Waiting), nil) {
		ids = append(ids, c.ID)
	}
	var p Plan
	if len(ids) > 0 {
		p = Resolve(s, ResolveReq{Sel: Sel{Only: ids}, Who: r.who()})
	}
	return bound(p)
}

// dealTurns is the order the tick resolves in (front(s) per stream): one
// card from each stream in turn, in the order of streams, until every stream's
// cards are taken; a stream with none left is skipped, and within a stream the
// cards go in work order (SortCards). A card of a stream not in streams takes
// its turn after them, its streams in name order. So every stream with a
// ready card is worked in parallel: a deal of k cards over n streams gives
// each stream k/n, give or take one, never one stream's backlog first. The
// model is tla/SprintEvents.tla: TurnSorted is this order (one card from each
// stream's front in turn, a stream with none skipped, within a stream by work
// order), PlanDeal takes the room from it, and DealTakesTurns states it from
// the counts of a plan; the witness W28 (MCSprintEventsW28.cfg) is the order of
// the whole table.
func dealTurns(cards []*Card, streams []string) []*Card {
	by := map[string][]*Card{}
	order := append([]string(nil), streams...)
	known := map[string]bool{}
	for _, st := range streams {
		known[st] = true
	}
	var extra []string
	for _, c := range cards {
		if !known[c.Row] {
			known[c.Row] = true
			extra = append(extra, c.Row)
		}
		by[c.Row] = append(by[c.Row], c)
	}
	sort.Strings(extra)
	order = append(order, extra...)
	for _, st := range order {
		SortCards(by[st])
	}
	out := make([]*Card, 0, len(cards))
	for turn := 0; len(out) < len(cards); turn++ {
		for _, st := range order {
			if turn < len(by[st]) {
				out = append(out, by[st][turn])
			}
		}
	}
	return out
}

// T7. TickResume resumes a stream stopped only because a card needed another
// stream's card, once that card has landed: the stuck cards back to queued,
// the stream merging, its judgment closed, and a happened note.
func TickResume(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	due := 0
	for _, st := range s.Merge.Rows() {
		ctl := s.StreamCtl(st)
		if ctl.F("state") != StreamStopped || ctl.F("cause") != "cross" {
			continue
		}
		stuck := s.Merge.Cell(st, Stuck)
		landed := len(stuck) > 0
		var ids []string
		for _, c := range stuck {
			need := c.F("need_card")
			landed = landed && need != "" && s.StateOf(need) == Landed
			ids = append(ids, c.ID)
		}
		if !landed {
			continue
		}
		if len(p.Units) >= TickMaxMoves {
			due++
			continue
		}
		q := Resume(s, ResumeReq{Stream: st, Did: "the card it needed landed", Who: r.who()})
		if len(q.Units) == 0 {
			continue
		}
		n := happened(NResumed, st, s.Now, ids...)
		n.Who = r.who()
		q.Units[0].Notes = append(q.Units[0].Notes, n)
		p.Units = append(p.Units, q.Units...)
	}
	return p, due
}

// T3. TickDeal deals ready primaries in stream turns (streamTurns: one from
// each stream in turn from the deal's stream index on the work table, a stream with no
// ready card skipped, each stream's oldest first by score; Deal moves the
// index past the stream of the last card dealt), each
// to the next up
// member round the fleet with room (Deal: the rolling index of round.go),
// every member filled up to its width, its ready and
// working cards together (width.go): every ready card
// the fleet has room for goes in the one plan, one step, up to TickMaxDeal;
// a withdrawn card is dealt again at a new generation. With no member up and primaries waiting to be dealt, the
// coordinator is told once (N3), and the judgment closes when a member is up.
func TickDeal(s *Snapshot, r TickReq) (Plan, int) {
	// the routes resting now, and those the no-result rule rests in this tick (rule 3,
	// route_rest.go): no card of this tick is drawn on one, and the new rests are written
	// in its plan
	s, rests := s.withRests()
	if s.Friends == nil && len(r.Friends) > 0 {
		n := *s
		n.Friends = r.Friends // the friends a tier is served by (tierServed)
		s = &n
	}
	// reads before work (reads are a card priority): while read cards are on, this deal
	// deals the read cards first, and its work in the room they leave (read_cards.go)
	s, reads := s.withReadCards(r.Friends)
	var p Plan
	due := 0
	var ready []*Card
	var conds []cond
	unserved, whyOf := map[string][]string{}, map[string]string{}
	up := s.UpMembers()
	// Friends first: every ready card a friend may take, except a held stream
	// (dealt nowhere) and a bench card (bench_deal.go keeps it for its bench).
	// A hard pin that no friend takes stays out of the fleet below.
	var offer []*Card
	for _, c := range s.Work.Column(Ready) {
		if StreamHeld(s, c.Row) || IsSentinel(c) {
			continue
		}
		if b := Bench(c); len(b) > 0 {
			continue
		}
		offer = append(offer, c)
	}
	// the ladder (priority.go, reads_priority.go): the cards above reader, then the friends'
	// reads, asked and placed in this plan, then normal and low work in the room they leave
	fp, seats, dealt, dealtWorking := friendDealByLadder(s, dealOrder(s, offer), r.Friends)
	friendPlaced := map[string]bool{}
	for _, u := range fp.Units {
		friendPlaced[u.Key] = true
	}
	for _, c := range s.Work.Column(Ready) {
		if StreamHeld(s, c.Row) {
			continue // its stream is held (hold.go): dealt to no machine and no friend until unhold
		}
		if friendPlaced[c.ID] || OnlyFriend(c) {
			continue
		}
		if wc := AtRedealBound(s, c); wc != nil {
			cd := cond{typ: NBound, stream: c.Row, card: wc.ID, primaries: []string{c.ID}, what: boundWhat(wc, c.ID)}
			held, _ := reworkAtTheSameBound(s, c, wc, "")
			if held != "" {
				// the attempt before ended at its bound on its tier: its own judgment, which
				// closes and opens again as plain when the provider is back, once (failure.go)
				cd.what += "; a second bound on tier " + cardTierOf(c) + ": not reworked on it again"
			}
			cd.decisions = boundDecisions(c, wc, held != "")
			if bb, ok := AtBriefBound(c, "", s.AttemptsCap(c.Row)); ok {
				// too many attempts on one brief: the brief is wrong, not the worker, and the
				// judgment offers brief and drop, never rework (brief_bound.go)
				cd.what = bb.String() + "; " + cd.what
				cd.decisions = append([]string(nil), Decisions[NBriefWrong]...)
			}
			conds = append(conds, cd)
			continue
		}
		if wc, takes := AtStagingBound(s, c, up); wc != nil {
			// every member up refused it at staging: the coordinator's, once, never dealt again
			var who []string
			for _, t := range takes {
				who = append(who, t.Member)
			}
			conds = append(conds, cond{typ: NBound, stream: c.Row, card: wc.ID, primaries: []string{c.ID},
				what: fmt.Sprintf("%s: attempt %s was refused at staging by every member up (%s) and is not dealt again; last: %s; its history: nova-sprint log --card %s", wc.ID, wc.F("attempt"), strings.Join(who, ", "), takes[len(takes)-1].Error, c.ID)})
			continue
		}
		if IsSentinel(c) {
			continue
		}
		if _, tier, why, byFriend := s.routeOf(escalating(s, c), nil, nil); byFriend {
			// no route serves its tier and a friend up does: the friends' deal's, never a
			// machine's (tierServed); withdrawn or taken back from every such friend, no worker
			// is left for it, and the tier's one judgment names it
			if len(s.friendsFor(c, tier)) == 0 {
				unserved[tier] = append(unserved[tier], c.ID)
				if whyOf[tier] == "" {
					whyOf[tier] = "no machine route serves tier " + tier + ", and every friend up who serves it had the card withdrawn or taken back, so no worker is left for it: bring up another friend whose row lists " + tier + ", enable a route of the tier, or drop the card"
				}
			}
			continue
		} else if why != "" {
			// no route serves its tier (the tier it escalates to, at its bound below its
			// ceiling), or its model lines cannot be read (a card admitted before the
			// lint): one judgment per tier either way
			unserved[tier] = append(unserved[tier], c.ID)
			whyOf[tier] = why
			continue
		}
		if b := Bench(c); len(b) > 0 && len(onlyBench(up, b)) == 0 {
			// its bench is down or held: it waits ready for a member of it, and is dealt to
			// no other (bench_deal.go); the no-stall rule says why (held.go)
			continue
		}
		ready = append(ready, c)
	}
	// a primary whose attempt failed the way the attempt before did (rule 2): the bound's
	// judgment the finish wrote is held while it stays in review at that attempt
	for _, c := range s.Work.Column(Review) {
		if wc := AtIdenticalFailure(s, c); wc != nil {
			conds = append(conds, cond{typ: NBound, stream: c.Row, card: wc.ID, primaries: []string{c.ID},
				what: identicalWorkWhat(wc.ID, c.Int("attempt"), c.F(FieldFailure), c.ID)})
		}
	}
	// the reads too: a primary in review waiting for reads while no enabled route
	// serves its tier is held by the same judgment of that tier, the deal's,
	// at once, not at the unreported deadline (route.go, readRouteMissing); one
	// owner of the judgment, so it is written once and closed once
	if s.Readers != nil {
		for _, c := range s.Work.Column(Review) {
			if c.F("result") == "failed" || !readsWithoutRoute(s, c) {
				continue
			}
			if tier, why := s.readRouteMissing(c); why != "" {
				unserved[tier] = append(unserved[tier], c.ID)
				if whyOf[tier] == "" {
					whyOf[tier] = why
				}
			} else if s.refusedNoRoute(c) {
				// the readers of its tier refused its read for want of a route (readers.go,
				// RetiredByRefused): the tier's one judgment, the card in review, the readers up
				tier := s.readTierOf(c)
				unserved[tier] = append(unserved[tier], c.ID)
				if whyOf[tier] == "" {
					whyOf[tier] = "every reader of tier " + tier + " free to read it refused its read for want of a route, so its reads have no reader: run nova-config route add <name> --tier " + tier + " ..., name it in nova-config tier set " + tier + " --routes <name,...>, then nova-config apply, or start a friend's or a bud's reader of tier " + tier
				}
			}
		}
	}
	for _, tier := range slices.Sorted(maps.Keys(unserved)) {
		conds = append(conds, cond{typ: NNoRoute, stream: TierSubject(tier), streamLevel: true, primaries: unserved[tier],
			what: fmt.Sprintf("%d primaries of tier %s wait: %s", len(unserved[tier]), tier, whyOf[tier])})
	}
	// one judgment per provider while its routes rest for its funds or its key, never one
	// per card (provider_funds.go)
	pc, stop := providerConds(s)
	pc, stop = friendsKeepRunning(pc, stop, r.Friends)
	conds = append(conds, pc...)
	// a member whose cards are timing out, three within the window (overload.go)
	conds = append(conds, overloadConds(s)...)
	// a member back from down whose adoption of the latest failed (fleet_back.go)
	conds = append(conds, adoptConds(s)...)
	// the friend sync loop refusing past its bound (friend_sync_state.go)
	conds = append(conds, friendSyncConds(s)...)
	// landings on the sprint branch not promoted into dev (promotion.go)
	conds = append(conds, devBehindCond(s)...)
	// one deal order for friends and machines, by the ladder: a low card fills only a lane no
	// other card ready can (ladderOrder, priority.go)
	ready = ladderOrder(dealOrder(s, ready))
	if s.FleetOff() {
		// the fleet's work is off (nova-sprint set --fleet off): no card is dealt to a
		// machine, so no member up and a short ready queue are no judgment
		ready = nil
	}
	if len(up) == 0 && len(ready) > 0 {
		c := cond{typ: NNoMember, streamLevel: true,
			what: fmt.Sprintf("%d primaries wait to be dealt and no member is up: start nova-sprint fleet beat <member> on a machine, or release a hold with nova-sprint fleet up <member>", len(ready))}
		if beatingHeld(s, r) {
			// the members beat: the hold keeps them down, so the remedy is to release it
			c.what = fmt.Sprintf("%d primaries wait to be dealt and no member is up: every member that beats is held; release a hold with nova-sprint fleet up <member>", len(ready))
			c.decisions = []string{"fleet up", "wait"}
		}
		conds = append(conds, c)
	}
	if sentinel := heldWave(s); sentinel != nil && len(up) > 0 && !s.FleetOff() {
		// ready is kept at twice the fleet's width (the owner, 2026-10-02: "Ready always
		// full"; 2026-10-03: "BATCH EVERYTHING"): under it while a wave is held, the tick
		// says so every tick and offers the wave, never a single card
		width, n := 0, 0
		for _, m := range up {
			width += s.Width(m)
		}
		for _, c := range s.Work.Column(Ready) {
			if !IsSentinel(c) {
				n++
			}
		}
		if n < 2*width {
			conds = append(conds, cond{typ: NStarving, streamLevel: true, primaries: []string{sentinel.ID},
				what: fmt.Sprintf("the fleet is starving: ready %d is under twice the width %d; release a wave: nova-sprint release %s --reason '<why>'", n, 2*width, sentinel.ID)})
		}
	}
	if len(up) > 0 {
		room := widthRoom(s, up)
		n := min(room, TickMaxDeal, len(ready))
		due = min(room, len(ready)) - n
		if n > 0 {
			ids := make([]string, n)
			for i := range ids {
				ids[i] = ready[i].ID
			}
			p = Deal(s, DealReq{Sel: Sel{Only: ids}, Who: r.who()})
		}
	}
	p.Rows, p.Units, p.Refused = append(p.Rows, fp.Rows...), append(p.Units, fp.Units...), append(p.Refused, fp.Refused...)
	for _, row := range reads.Rows {
		if !slices.Contains(p.Rows, row) {
			p.Rows = append(p.Rows, row)
		}
	}
	p.Units = append(reads.Units, p.Units...)
	if len(r.Friends) > 0 {
		// the friends level after the deal, every tick and on the tick a friend comes up, so
		// an idle lane is filled and a backlog evens itself without the coordinator, at most
		// FriendLevelPerTick cards a tick (docs/SPEC-SPRINT.md section 1,
		// friend-deal-idle-lanes-first.w1)
		// a card dealt to a friend and not started within the start bound, while her beat
		// names no job running, goes first to a friend with an idle lane, never back to
		// her (friendUnstartedLevel; docs/SPEC-SPRINT.md section 1, a friend's card is
		// working once she starts it); the level then neither moves it again nor counts it
		// on her row
		up := friendUnstartedLevel(s, seats, func(at string) (time.Duration, bool) { return r.running(s.Now, at) }, nil, dealt, FriendLevelPerTick)
		moved := map[string]bool{}
		for _, u := range up.Units {
			c := s.Fleet.Card(u.Key)
			moved[c.ID] = true
			from, _ := FriendOfRow(c.Row)
			to, _ := FriendOfRow(u.Changes[0].Entry.Move.Row)
			dealt[from]--
			dealt[to]++
		}
		lp := friendLevel(s, FriendLevelReq{Seats: seats, Who: r.who(), Max: FriendLevelPerTick, Taken: fp.Units, Moved: moved}, dealt, dealtWorking)
		lp.Rows, lp.Units = append(up.Rows, lp.Rows...), append(up.Units, lp.Units...)
		for _, row := range lp.Rows {
			if !slices.Contains(p.Rows, row) {
				p.Rows = append(p.Rows, row)
			}
		}
		p.Units = append(p.Units, lp.Units...)
	}
	// a ready card dealt on a route that rests now is withdrawn, never taken there
	p.Units = append(p.Units, restWithdrawals(s, r.who())...)
	restWrites(&p, s, rests, r.who())
	due += notify(&p, s, conds, []string{NNoMember, NStarving, NOverloaded, NAdoptFailed, NDevBehind, NBound, NNoRoute, NProviderFunds, NProviderLow, NProviderKey, NAllOutOfCredit, NFriendSyncFailing}, r)
	// every provider out of credit: the binding stops the machine as the plan commits
	p.Stop = stop
	return p, due
}

// dealOrder is the one order the tick's deal offers ready cards in, to the friends
// (friendDealPass, friendReclaim) and to the machines alike (docs/SPEC-SPRINT.md section 1, a
// friend's card): the stream turns from the deal's stream index (streamTurns), each
// stream's cards in work order, the order tla/SprintTables.tla and the reference model
// (refmodel) check. Each card then goes to the friend with the most idle lanes
// (preferredFriend), or round the fleet. The critical path first (weight.go) waits for
// the model to order by weight too.
func dealOrder(s *Snapshot, cards []*Card) []*Card {
	return streamTurns(cards, streamRound(s, PropStreamIndex))
}

// readsWithoutRoute says a primary in review waits for reads, or holds a read
// asked or begun with no route (asked while no route served its tier): what
// that tier's no-route judgment holds (TickDeal).
func readsWithoutRoute(s *Snapshot, pr *Card) bool {
	if ReadsWanted(s, pr) > 0 {
		return true
	}
	for _, rc := range liveReadsAt(s, pr, pr.Int("attempt")) {
		if (rc.Col == Asked || rc.Col == Reading) && rc.F(FieldRoute) == "" {
			return true
		}
	}
	return false
}

// beatingHeld says some fleet member beats and every member that beats is
// held (docs/SPEC-SPRINT.md, the judgment "no fleet member is up": the hold,
// not a missing beat, keeps the fleet down).
func beatingHeld(s *Snapshot, r TickReq) bool {
	beats := false
	for _, m := range s.Members() {
		if !r.Beats[m].Fresh(s.Now) {
			continue
		}
		if ctl := s.MemberCtl(m); ctl == nil || ctl.F("held") == "" {
			return false
		}
		beats = true
	}
	return beats
}

// AtRedealBound is the primary's withdrawn work card when it is at its
// redeal bound at its ceiling: a take of it ended (FieldTakeEnded) with its count at
// MaxRedeals, so the deal that would place it again would pass the bound. The
// tick deals it no more. nil when it is not: a card withdrawn while ready
// keeps its count and is dealt again (tla/DirtyTick.tla AtRB), and a card below its
// ceiling (NextTier) is the deal's to escalate, no judgment raised (route.go,
// tierLadder).
func AtRedealBound(s *Snapshot, pr *Card) *Card {
	if pr == nil || pr.Col != Ready {
		return nil
	}
	wc := s.Fleet.Placed(WorkCardID(pr.ID, pr.Int("attempt")))
	if wc == nil || wc.Col != Withdrawn || !redealBound(wc) {
		return nil
	}
	if _, atCap := AtBriefBound(pr, "", s.AttemptsCap(pr.Row)); s.NextTier(pr) == "" || atCap {
		return wc // at its ceiling, or at the attempt cap: not dealt again
	}
	return nil
}

// StagingTakes is the work card's launches refused at staging, in the order of their
// generations (FieldStagingTake).
func StagingTakes(wc *Card) (takes []ProviderTake, gens []int) {
	for g := 1; g <= wc.Int("gen"); g++ {
		if v := wc.F(FieldStagingTake + itoa(g)); v != "" {
			takes, gens = append(takes, parseTake(v)), append(gens, g)
		}
	}
	return takes, gens
}

// StagingRefusers is the members that refused the work card at staging, each once, in the
// order they refused: the deal places it on none of them.
func StagingRefusers(wc *Card) []string {
	takes, _ := StagingTakes(wc)
	var out []string
	for _, t := range takes {
		if !contains(out, t.Member) {
			out = append(out, t.Member)
		}
	}
	return out
}

// AtStagingBound is the primary's withdrawn work card when every member up (up, at least
// one) refused it at staging, and the refusals: the deal has no member to place it on, and
// the tick deals it no more until a member that has not refused it is up, or the coordinator
// reworks or drops it. nil when it is not.
func AtStagingBound(s *Snapshot, pr *Card, up []string) (*Card, []ProviderTake) {
	if pr == nil || pr.Col != Ready || len(up) == 0 {
		return nil, nil
	}
	wc := s.Fleet.Placed(WorkCardID(pr.ID, pr.Int("attempt")))
	if wc == nil || wc.Col != Withdrawn || len(without(up, StagingRefusers(wc))) > 0 {
		return nil, nil
	}
	takes, _ := StagingTakes(wc)
	return wc, takes
}

// providerWhy is what a bound's judgment adds when the take that ended last was the
// provider's: the provider (the model id's first word) and the last error line
// (tla/CardContract.tla, ProviderFailure).
func providerWhy(wc *Card) string {
	line := wc.F(FieldProviderError)
	if line == "" {
		return ""
	}
	provider, _, _ := strings.Cut(wc.F(FieldModel), "/")
	return fmt.Sprintf(": the provider %s failed it, last error: %s", orDash(provider), line)
}

// escalating is the primary c as its next deal draws its route: at its redeal bound
// below its ceiling, on the tier it escalates to (NextTier, escalate); else c.
func escalating(s *Snapshot, c *Card) *Card {
	wc := s.Fleet.Placed(WorkCardID(c.ID, c.Int("attempt")))
	if wc == nil || wc.Col != Withdrawn || !redealBound(wc) {
		return c
	}
	if t := s.NextTier(c); t != "" {
		return withField(c, FieldTierNow, t)
	}
	return c
}

// redealBound says the withdrawn work card's next deal would count a take
// past MaxRedeals, or would be its third try after two takes that ended the same
// way (rule 2, identicalEnds).
func redealBound(wc *Card) bool {
	return wc.F(FieldTakeEnded) != "" && (wc.Int("redeals") >= MaxRedeals || identicalEnds(wc) != "")
}

// T4. TickLevel is the fleet's rebalance, once at the start of every tick
// (TickStart): the up members' backlogs evened when two differ by more than
// one, the newest ready cards of the largest going round the fleet from the
// deal's index to a member below DealAhead times its width (level,
// round.levelTo).
func TickLevel(s *Snapshot, r TickReq) (Plan, int) {
	return bound(FleetStep(s, FleetReq{Op: "level", Who: r.who()}))
}

// T2. TickAsk asks the reads wanted now (ReadsWanted: the first read alone,
// then the rest it needs once the first came back ok; ReadsNeeded: one for a
// flash card, two for a pro card; cost rule 4) of every primary in review
// whose work did not fail, readers up with room only (each read to the free
// reader with the greatest share of room, the finder's first; Ask,
// askPicks): a primary whose reads wanted now find too few free readers with
// room waits, due for the next tick, with no judgment; a returned read is
// asked again in place and needs no room. A read asked of a reader that is
// not up is taken back, and its primary is asked again, in the same step.
// The primaries that cannot be asked, for want of as many different readers
// as they still need whatever their room, are one judgment per tick (N1,
// NCannotAsk: "no eligible reader for <ids>", cannotAskCond), each closed
// when its primary is asked; a primary that needs more readers than are up is
// not asked, and one judgment says so (NFewReaders, the sprint's, once per
// tick-end): with one reader up the flash cards are asked and the pro cards
// wait. The
// primaries go in stream turns from the ask's stream index on the work table
// (streamTurns, as the deal's; Ask moves the index), so the readers
// serve every stream alike and no stream's backlog waits behind another's.
func TickAsk(s *Snapshot, r TickReq) (Plan, int) {
	var ids []string
	due := 0
	askable := func(c *Card) string {
		if c.F("result") != "failed" && ReadsWanted(s, c) > 0 {
			return ""
		}
		return "asked, or its work failed"
	}
	few := false // a primary waits for more readers than are up
	// the ask's placement, rehearsed on the same rooms and round (Ask:
	// askPicks, the finder first, then round.pickByRoom from the ask's index,
	// each read placed taken off its reader's room): a primary that lacks as
	// many free readers with room as the reads it wants now is not asked this
	// tick and is due, not a judgment, as a card waits for a lane on the fleet;
	// a returned read is asked again in place of its own reader, whose room
	// holds it already
	room := s.readerRooms(s.Readers.Rows())
	rr := askRound(s)
	// by the read's level (the higher of reader and its primary's: readOrder, priority.go),
	// then stream turns
	cards := readOrder(eligibleTurns(s.Work.Column(Review), askable, askStreamRound(s)))
	// the finders first, as the ask places them (askFinders), over the
	// primaries the tick may ask
	var askNow []*Card
	for _, c := range cards {
		attempt := c.Int("attempt")
		if len(askNow) < TickMaxMoves && enoughReadersUp(s, c) &&
			len(s.freeReaders(c, attempt))+len(returnedInTier(s, c, attempt)) >= ReadsNeededIn(s, c)-len(liveReadsAt(s, c, attempt)) {
			askNow = append(askNow, c)
		}
	}
	finders := s.askFinders(askNow, false, room)
	for _, c := range cards {
		attempt := c.Int("attempt")
		// the readers it still needs, whatever their room, and the reads asked now:
		// together (ReadsWanted)
		need := ReadsNeededIn(s, c) - len(liveReadsAt(s, c, attempt))
		want := ReadsWanted(s, c)
		free := s.freeReaders(c, attempt)
		returned := len(returnedInTier(s, c, attempt))
		switch {
		case !enoughReadersUp(s, c):
			// an absent reader is never asked: the sprint's one judgment says so
			few = true
		case s.refusedNoRoute(c):
			// every reader free for it refused it for want of a route: the tier's judgment
			// holds it (TickDeal, NNoRoute), never a cannot-ask judgment of its own
		case len(ids) >= TickMaxMoves:
			due++
		case len(free)+returned < need:
			// no readers to ask it of, whatever their room: Ask refuses it,
			// and the refusal is the judgment
			ids = append(ids, c.ID)
		default:
			finder := finders[c.ID]
			picked := askPicks(rr, finder, want, free, room)
			if len(picked)+returned < want {
				for _, rd := range picked {
					room[rd] = room[rd].after(-1)
				}
				due++
				continue
			}
			for _, rd := range picked {
				if rd != finder { // the finder's read is out of turn: the index stays
					rr.moved(rd)
				}
			}
			ids = append(ids, c.ID)
		}
	}
	var p Plan
	var conds []cond
	if few {
		conds = append(conds, cond{typ: NFewReaders, streamLevel: true, what: fewReaders(s)})
	}
	// reads asked and not begun for the window: the readers are behind (readers_behind.go)
	conds = append(conds, readersBehindCond(s)...)
	// a stream whose read tier should rise: one judgment per stream (readtier.go)
	conds = append(conds, raiseReadTierConds(s)...)
	// broken reads outrunning ok reads, and a reader breaking nearly everything (reads_window.go)
	conds = append(conds, readsWindowConds(s, r)...)
	if len(ids) > 0 {
		p = Ask(s, AskReq{Sel: Sel{Only: ids}, Who: r.who()})
	}
	conds = append(conds, cannotAskCond(s, p.Refused)...)
	p.Refused = nil
	due += notify(&p, s, conds, []string{NCannotAsk, NFewReaders, NReadersBehind, NRaiseReadTier, NBrokenReadsOutrun, NReaderBreaks}, r)
	return p, due
}

// cannotAskCond is the tick's condition for the primaries the ask refused for
// want of readers (Ask, cannotAskWhy): one judgment, "no eligible reader for
// <ids>" with the first such refusal's reason, in the stream of its first
// primary, every such primary of the tick a subject of it. The ids it names
// are the primaries no open judgment of the type names yet, and notify writes
// it on those alone, keeping the rest open: a card is never silent in review
// for want of a reader, and five stranded cards are one judgment, not five
// (the night of 2026-10-03: five cards, five readers, five judgments).
func cannotAskCond(s *Snapshot, refused []Refusal) []cond {
	var all, fresh []string
	stream, why := "", ""
	for _, x := range refused {
		pr := s.Work.Placed(x.Key)
		if pr == nil {
			continue
		}
		all = append(all, pr.ID)
		if len(closesFor(s.Open, []string{NCannotAsk}, pr.ID)) > 0 {
			continue
		}
		if len(fresh) == 0 {
			stream, why = pr.Row, x.Why
		}
		fresh = append(fresh, pr.ID)
	}
	if len(all) == 0 {
		return nil
	}
	return []cond{{typ: NCannotAsk, stream: stream, primaries: all, what: NoEligibleReader + strings.Join(fresh, ", ") + ": " + why}}
}

// T6. TickCheck holds the state to what is always true (section 9): each
// violation is one judgment (N8), with the rule and the cards, closed by the
// tick when the rule holds again. Its duty is the no-stall rule too:
// each stall nothing holds is one judgment "stalled", with the decisions open
// to it, not written again while it stays and closed when it clears; a stall
// that waits behind another is told by the other's. And a judgment whose every
// card has left the table retires here, answered by the machine with the card's
// event (judgments_retire.go): the tick that sees the card gone closes it.
func TickCheck(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	var conds []cond
	for _, v := range Check(s, nil) {
		c := cond{typ: NInvariant, what: v.String()}
		for _, w := range strings.FieldsFunc(v.Detail, func(x rune) bool { return strings.ContainsRune(" ,():", x) }) {
			if pr := s.Work.Card(w); pr != nil && !contains(c.primaries, w) {
				c.primaries = append(c.primaries, w)
				c.stream = pr.Row
			}
		}
		if len(c.primaries) == 0 {
			c.streamLevel, c.stream = true, ""
		}
		conds = append(conds, c)
	}
	for _, f := range Unheld(HeldState{Snap: s, Running: true, Stopped: r.Stopped}, s.Now) {
		if f.Root != "" {
			continue
		}
		c := cond{typ: NStalled, stream: f.Stream, what: f.What + ": " + f.Why, decisions: f.Decisions}
		if strings.HasPrefix(f.Subject, "stream:") {
			c.streamLevel = true
		} else {
			c.primaries = []string{f.Subject}
		}
		conds = append(conds, c)
	}
	due := notify(&p, s, conds, []string{NInvariant, NStalled}, r)
	RetireJudgments(s, &p, r.who())
	return p, due
}

// TickDeadlines writes one judgment for each card or stream past its
// deadline, in running time (N4, N5, N6), and closes it when the card or the
// stream moves. It keeps the backlog alarms too (tickAlarms, docs/SPEC-SPRINT.md
// section 8, "Backlog alarms").
func TickDeadlines(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	var conds []cond
	late := func(stampField string, c *Card, limit time.Duration) (string, bool) {
		d, ok := r.running(s.Now, c.F(stampField))
		return c.F(stampField), ok && d > limit
	}
	// N4: work cards dealt and never taken, taken and not finished, by the
	// card's state (WorkDeadline): never taken past the dealt bound from the
	// first deal since its last take, not finished from the attempt's first
	// take. On a machine's row no redeal or withdrawal rewrites either: a
	// member whose beat lapses again and again cannot reset them, and the
	// time a card spends withdrawn counts. The one exception is a friend's
	// card dealt again after a take-back: it is measured from her own deal,
	// and a never-taken judgment raised before that deal (while it sat
	// withdrawn) closes on it (LateStands).
	for _, c := range s.Fleet.Column(Ready, Working, Withdrawn) {
		if c.Col == Withdrawn && c.F("kind") == "read" {
			continue // a read withdrawn is history: its primary is asked again (friendReadLive)
		}
		field, limit, word, own := WorkDeadline(s, c)
		friend, idle := friendLaneIdle(s, r.Friends, c)
		if idle {
			// ready on her row while she has a lane free and not started: the start bound
			// is the level's to move it (friendUnstartedLevel), and FriendReadyMax past it
			// no one has
			limit = s.FriendStartMax() + FriendReadyMax
		}
		at, ok := late(field, c, limit)
		if !ok {
			continue
		}
		// fleet down names the member only when it has had its own whole
		// deadline: a card late at the moment it is redealt is not the new
		// member's fault
		_, mine := late(own, c, limit)
		down := own != "" && mine && s.MemberCtl(c.Row).F("status") == Up
		what := fmt.Sprintf("%s %s at %s, %s; at %s", c.ID, strings.TrimPrefix(field, "first_"), at, word, placeOf(c))
		decisions := []string{"wait", "drop"}
		switch {
		case idle && word == WordNeverTaken:
			// a friend's card ready with a lane of hers free: neither the deal (batch mode)
			// nor her daemon or session (one-shot) took it (docs/SPEC-SPRINT.md section 1, a
			// friend takes her own ready cards)
			what = fmt.Sprintf("%s %s, at %s (dealt %s, ready over %s while friend %s has a lane free)", c.ID, word, placeOf(c), at, limit, friend)
			decisions = []string{"friend take " + friend + " " + c.ID, "wait"}
		case word == WordNeverTaken && c.Col == Ready:
			// dealt and waiting in a member's queue past the dealt bound: the
			// queue's, so the answers are the fleet's (nova-tools#5096 item 22)
			what = fmt.Sprintf("%s %s, at %s (dealt %s, over the dealt bound %s)", c.ID, word, placeOf(c), at, limit)
			decisions = []string{"fleet level", "wait"}
			if down {
				decisions = []string{"fleet level", "fleet down " + c.Row, "wait"}
			}
		case word == WordNeverTaken:
			what = fmt.Sprintf("%s %s, %s (dealt %s, over the dealt bound %s)", c.ID, word, placeOf(c), at, limit)
		case down:
			decisions = append([]string{"fleet down " + c.Row}, decisions...)
		}
		conds = append(conds, cond{typ: NWorkLate, stream: c.F("stream"), card: c.ID, primaries: []string{c.F("primary")},
			what: what, decisions: decisions})
	}
	// N5: read cards asked and not begun, begun and not reported.
	for _, c := range s.Readers.Column(Asked, Reading) {
		field, limit, word := "asked", DeadlineUnbegun, "not begun"
		if c.Col == Reading {
			field, limit, word = "begun", DeadlineUnreported, "not reported"
		}
		if at, ok := late(field, c, limit); ok {
			conds = append(conds, cond{typ: NReadLate, stream: c.F("stream"), card: c.ID, primaries: []string{c.F("primary")},
				what: fmt.Sprintf("%s %s of %s at %s, %s; at %s", c.ID, field, c.Row, at, word, placeOf(c))})
		}
	}
	// N6: a stream merging, or waiting with queued cards, with no merge step.
	for _, st := range s.Merge.Rows() {
		ctl := s.StreamCtl(st)
		state := ctl.F("state")
		if state != StreamMerging && (state != StreamWaiting || s.Merge.Count(st, Queued) <= 0) {
			continue
		}
		last := max(ctl.F("since"), ctl.F("moved"))
		if d, ok := r.running(s.Now, last); ok && d > DeadlineMergeIdle {
			conds = append(conds, cond{typ: NMergeLate, stream: st, streamLevel: true,
				what:      fmt.Sprintf("state %s, no merge step since %s", state, last),
				decisions: []string{"merge --stream " + st, "look", "wait"}})
		}
	}
	due := notify(&p, s, conds, []string{NWorkLate, NReadLate, NMergeLate}, r)
	// the backlog alarms, on a plan of their own: each notify closes and judges after its own closes
	a, alarmsDue := tickAlarms(s, r)
	p.Notes, p.Closes, p.Updates = append(p.Notes, a.Notes...), append(p.Closes, a.Closes...), append(p.Updates, a.Updates...)
	// the drift alarms, on a plan of their own the same way (drift.go)
	d, driftDue := TickDrift(s, r, r.Drift)
	p.Notes, p.Closes, p.Updates = append(p.Notes, d.Notes...), append(p.Closes, d.Closes...), append(p.Updates, d.Updates...)
	return p, due + alarmsDue + driftDue
}

// TickOverdue marks each open judgment overdue once, when it passes its due
// time in running time (DeadlineJudgment after it was written, or the review
// time the coordinator set with wait): one overdue line (a happened note of
// NOverdue naming the judgment), and a hold on the judgment's open subjects
// (an acknowledged-kind record of NOverdue, never a judgment and shown in no
// inbox) that stops the tick marking it again. The tick closes the hold when
// the judgment closes or is no longer overdue (a wait moved its review time
// on), so a judgment overdue again is marked again. A coordinator who is
// silent is visible: every judgment waiting on them is named, once, as
// overdue. The coordinator's pass (TickCoordinatorPass) runs in this part
// too, after the overdue lines: it reminds the coordinator, every PassEvery,
// of the judgments still late and of the friends deaf or idle.
func TickOverdue(s *Snapshot, r TickReq) (Plan, int) {
	p, due := tickOverdue(s, r)
	pass, passDue := TickCoordinatorPass(s, r)
	p.Notes, p.Closes, p.Updates = append(p.Notes, pass.Notes...), append(p.Closes, pass.Closes...), append(p.Updates, pass.Updates...)
	return p, due + passDue
}

// tickOverdue is the overdue lines and holds of TickOverdue.
func tickOverdue(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	type judg struct {
		note     Note
		subjects []string
	}
	var order []string
	byID := map[string]*judg{}
	for _, o := range s.Open {
		if o.Note.Kind != Judgment || o.Note.Type == NSprintDone {
			continue // the sprint is done has no due time
		}
		j := byID[o.Note.ID]
		if j == nil {
			j = &judg{note: o.Note}
			byID[o.Note.ID] = j
			order = append(order, o.Note.ID)
		}
		j.subjects = append(j.subjects, o.Subject())
	}
	overdue := func(n Note) bool { return JudgmentOverdue(s, r, n) }
	marked := map[string]bool{} // judgment id + subject, held as overdue
	for _, o := range s.Acked {
		if o.Note.Type != NOverdue {
			continue
		}
		j := byID[o.Note.What]
		if j == nil || !contains(j.subjects, o.Subject()) || !overdue(j.note) {
			p.Closes = append(p.Closes, o)
			continue
		}
		marked[OpenKey(o.Note.What, o.Subject())] = true
	}
	due := 0
	for _, id := range order {
		j := byID[id]
		if !overdue(j.note) {
			continue
		}
		var fresh []string
		for _, sub := range j.subjects {
			if !marked[OpenKey(id, sub)] {
				fresh = append(fresh, sub)
			}
		}
		if len(fresh) == 0 {
			continue
		}
		sort.Strings(fresh)
		n := j.note
		past := fmt.Sprintf("%s of running time", DeadlineJudgment)
		if !n.Review.IsZero() {
			past = "its review time " + stamp(n.Review)
		}
		line := Note{Kind: Happened, Type: NOverdue, Stream: n.Stream, Who: r.who(), At: s.Now,
			What: fmt.Sprintf("%s (%s) open since %s, past %s; run: nova-sprint inbox", id, n.Type, stamp(n.At), past)}
		hold := Note{Kind: Acknowledged, Type: NOverdue, Stream: n.Stream, What: id, Who: r.who(), At: s.Now,
			StreamLevel: n.StreamLevel, SprintLevel: n.SprintLevel}
		if !n.StreamLevel && !n.SprintLevel {
			line.Primaries, line.Count = fresh, len(fresh)
			hold.Primaries, hold.Count = fresh, len(fresh)
		} else {
			line.Primaries, line.Count = n.Primaries, n.Count
		}
		p.Notes = append(p.Notes, line, hold)
	}
	return p, due
}

// JudgmentOverdue says the judgment is past its due time in running time: its review
// time when the coordinator set one (wait), else DeadlineJudgment after it was written.
func JudgmentOverdue(s *Snapshot, r TickReq, n Note) bool {
	if !n.Review.IsZero() {
		// The review time wait set counts running time from when wait set
		// it, by the tree's one clock comparison, the same one a timer is
		// due by (stopped.go DueNow; docs/SPEC-SPRINT.md, "Timers").
		return DueNow(s.Now, n.Review, n.ReviewSet, r.Stopped)
	}
	d, ok := r.running(s.Now, stamp(n.At))
	return ok && d > DeadlineJudgment
}

// cond is a condition the tick tells the coordinator of: a judgment of a type
// on its subjects (primaries, or the stream as a whole; a stream-level
// condition with no stream is about the sprint).
type cond struct {
	typ, stream string
	card        string // the consumer card a late judgment is of: its own cause
	primaries   []string
	streamLevel bool
	what        string
	decisions   []string
	tier        string // the tier a judgment proposes (NRaiseReadTier)
}

// condKey identifies a condition on one subject: the type, the subject and
// what it says. N3's count and N1's count of free readers change while each
// stays one condition, so they are keyed by their type and subject only.
func condKey(typ, subject, card, what string) string {
	switch typ {
	case NNoMember, NAdoptFailed, NCannotAsk, NNoRoute, NFewReaders, NProviderFunds, NProviderLow, NProviderKey, NAllOutOfCredit, NStarving, NOverloaded, NReadersBehind, NDevBehind, NRaiseReadTier,
		NBrokenReadsOutrun, NReaderBreaks,
		NAlarmReview, NAlarmMerging, NAlarmReady, NAlarmFleet, NFilesAlarm, NFriendDeaf, NFriendIdle, NCoordinatorBehind,
		NDriftAhead, NDriftCardBase, NDriftServer, NDriftBaseRed, NFriendSyncFailing:
		what = ""
	case NWorkLate, NReadLate:
		// a lateness is one per attempt's card and kind (not taken, not
		// finished, not begun, not reported), whatever its facts say now
		what = card + "\x00" + lateKind(what)
	}
	return typ + "\x00" + subject + "\x00" + what
}

// lateKind is a lateness's kind, from its text: "<card> <stamp> at <time>,
// <kind>; at <place>", or "<card> dealt, never taken, at <place> (...)".
func lateKind(what string) string {
	if strings.Contains(what, WordNeverTaken) {
		return WordNeverTaken
	}
	what, _, _ = strings.Cut(what, "; at ")
	if i := strings.LastIndex(what, ", "); i >= 0 {
		return what[i+2:]
	}
	return what
}

// LateStands says the cause of a lateness still stands: no move that resolves
// it has happened. Not finished stands while the attempt's work card is
// ready, working or withdrawn (a redeal or a return to ready does not finish
// it); never taken while the card is not taken (ready or withdrawn) and the
// clock it is measured by (WorkDeadline's field) started before the note: a
// friend's card dealt to her after the note was raised is measured from her
// own deal, so the lateness the note records, the clock before, stands no
// more and the note closes on her deal (her own bound raises its own
// judgment); not begun while the read is asked; not reported while it is
// asked or reading. While its cause stands a lateness stays raised, whether
// or not it is late at this moment: no judgment flaps closed and open again.
func LateStands(s *Snapshot, n Note) bool {
	kind := lateKind(n.What)
	switch n.Type {
	case NWorkLate:
		c := s.Fleet.Placed(n.Card)
		if c == nil {
			return false
		}
		if kind == WordNeverTaken || kind == "not taken" { // "not taken": raised before the dealt bound
			if c.Col != Ready && c.Col != Withdrawn {
				return false
			}
			// measured from a stamp at or after the note (the note's second: stamps are
			// whole seconds, and a never-taken judgment is raised a whole bound after the
			// stamp it counts from, so a stamp in the note's second is a deal after it):
			// the note is of the clock before, which the redeal to a friend ended. On a
			// machine's row the field is untaken_since, which no redeal rewrites, so this
			// closes nothing there.
			field, _, _, _ := WorkDeadline(s, c)
			if at := stampAt(c, field); !at.IsZero() && !at.Before(n.At.Truncate(time.Second)) {
				return false
			}
			return true
		}
		return c.Col == Ready || c.Col == Working || c.Col == Withdrawn
	case NReadLate:
		c := s.Readers.Placed(n.Card)
		if c == nil {
			return false
		}
		if kind == "not begun" {
			return c.Col == Asked
		}
		return c.Col == Asked || c.Col == Reading
	}
	return false
}

// placeOf is where a card is, as a lateness says it: member:column, or
// withdrawn.
func placeOf(c *Card) string {
	if c.Col == Withdrawn {
		return "withdrawn"
	}
	return c.Row + ":" + c.Col
}

func (c cond) subjects() []string {
	if c.streamLevel {
		return []string{StreamSubject(c.stream)}
	}
	return c.primaries
}

// notify writes a judgment for each condition not open already, every one in
// the tick, and closes every open judgment of the types whose condition
// no longer holds: each judgment is written once and never every tick. It
// returns how many conditions it left unwritten past the bound: those are
// due.
func notify(p *Plan, s *Snapshot, conds []cond, types []string, r TickReq) int {
	who := r.who()
	// A condition is open while its judgment is, or while the coordinator's
	// hold of it stands (an acknowledgement, or a wait until its time has
	// passed in running time): either way it is not written again. A wait
	// that has run out is closed, and the condition, when it holds, is raised
	// again.
	var held []Open
	held = append(held, s.Open...)
	for _, o := range s.Acked {
		if !o.Note.Review.IsZero() && contains(types, o.Note.Type) {
			// A held condition's wait (WaitStep) is based at the judgment's
			// write when wait recorded no base, so its STOPPED time still does
			// not count; the comparison is the tree's one (stopped.go DueNow).
			base := o.Note.ReviewSet
			if base.IsZero() {
				base = o.Note.At
			}
			if DueNow(s.Now, o.Note.Review, base, r.Stopped) {
				p.Closes = append(p.Closes, o)
				continue
			}
		}
		held = append(held, o)
	}
	open := map[string]bool{}
	judged := map[string]Note{} // the open judgment of a condition, to update in place
	for _, o := range held {
		if contains(types, o.Note.Type) {
			k := condKey(o.Note.Type, o.Subject(), o.Note.Card, o.Note.What)
			open[k] = true
			if o.Note.Kind == Judgment {
				judged[k] = o.Note
			}
		}
	}
	holds := map[string]bool{}
	updated := map[string]bool{}
	update := func(n Note, what string, decisions []string) {
		same := n.What == what && (len(decisions) == 0 || slices.Equal(n.Decisions, decisions))
		if same || updated[n.ID] {
			return
		}
		updated[n.ID] = true
		n.What = what
		if len(decisions) > 0 {
			n.Decisions = append([]string(nil), decisions...) // the latest facts name the latest remedies
		}
		p.Updates = append(p.Updates, n)
	}
	due := 0
	for _, c := range conds {
		// the subjects no judgment of the condition is open on: a grouped
		// condition (cannotAskCond) is written on those alone, so a primary
		// judged already is not the subject of a second judgment of the type
		var fresh []string
		for _, sub := range c.subjects() {
			k := condKey(c.typ, sub, c.card, c.what)
			holds[k] = true
			if !open[k] {
				fresh = append(fresh, sub)
			}
			if n, ok := judged[k]; ok && (c.typ == NWorkLate || c.typ == NReadLate || c.typ == NFewReaders || c.typ == NStarving || c.typ == NOverloaded || c.typ == NFilesAlarm || c.typ == NReadersBehind || c.typ == NDevBehind || c.typ == NBrokenReadsOutrun || c.typ == NReaderBreaks || c.typ == NFriendSyncFailing) {
				update(n, c.what, c.decisions) // the latest facts, in place
			}
		}
		if len(fresh) == 0 {
			continue
		}
		primaries := c.primaries
		if !c.streamLevel {
			primaries = fresh
		}
		n := Note{Kind: Judgment, Type: c.typ, Stream: c.stream, Primaries: primaries, Count: len(primaries), What: c.what,
			Who: who, At: s.Now, StreamLevel: c.streamLevel, Marked: true, Card: c.card, Tier: c.tier}
		n.Decisions = append([]string(nil), c.decisions...)
		if len(n.Decisions) == 0 {
			n.Decisions = append([]string(nil), TickDecisions[c.typ]...)
		}
		p.Notes = append(p.Notes, n)
	}
	closing := map[string]bool{}
	for _, o := range held {
		if !contains(types, o.Note.Type) || holds[condKey(o.Note.Type, o.Subject(), o.Note.Card, o.Note.What)] {
			continue
		}
		if LateStands(s, o.Note) {
			// not late now, and its attempt lives: it stays raised, saying
			// where the card is
			if o.Note.Kind == Judgment {
				c := s.Fleet.Placed(o.Note.Card)
				if o.Note.Type == NReadLate {
					c = s.Readers.Placed(o.Note.Card)
				}
				what, _, _ := strings.Cut(o.Note.What, "; at ")
				update(o.Note, what+"; at "+placeOf(c), nil)
			}
			continue
		}
		p.Closes = append(p.Closes, o)
		closing[o.Note.ID] = true
	}
	// A primary in review whose last judgment the tick closes (its late read
	// reported, say) gets the judgment it needs after it, as every step that
	// leaves a primary in review does; the reads the plan places (the ask's,
	// closing "cannot ask") are its reads too, so a primary asked this tick
	// is not judged stranded as never asked.
	moved := map[string]string{}
	for _, u := range p.Units {
		for _, ch := range u.Changes {
			if ch.Table == Readers && ch.Entry.Create != nil {
				moved[ch.Entry.ID] = ch.Entry.Create.Col
			}
		}
	}
	seen := map[string]bool{}
	for _, o := range p.Closes {
		pr := s.Work.Placed(o.Subject())
		if pr == nil || seen[pr.ID] {
			continue
		}
		seen[pr.ID] = true
		if j, ok := reviewJudgment(s, pr, reviewStep{moved: moved, closing: closing, writes: p.Notes, who: who}); ok {
			p.Notes = append(p.Notes, j)
		}
	}
	return due
}

// MovesDue is how many moves the tick would make on the snapshot's work and
// fleet: primaries ready to deal, work cards withdrawn, waiting primaries
// whose needs have all landed (a sentinel is the coordinator's release),
// primaries in review to ask (as TickAsk picks them), and primaries in review
// with the ok reads they need that nothing holds (the pump accepts them,
// TickAccept): a STOPPED machine with any of these says so to the seat.
func MovesDue(s *Snapshot) int {
	n := len(s.Fleet.Column(Withdrawn))
	for _, c := range s.Work.Column(Review) {
		switch {
		case s.Readers == nil || c.F("result") == "failed":
		case acceptable(s, c):
			if AcceptHeld(c) == "" {
				n++
			}
		case ReadsWanted(s, c) > 0 && enoughReadersUp(s, c):
			n++
		}
	}
	for _, c := range s.Work.Column(Ready) {
		if !IsSentinel(c) && AtRedealBound(s, c) == nil {
			n++
		}
	}
	for _, c := range s.Work.Column(Waiting) {
		if !IsSentinel(c) && !IsHeld(c) && len(WaitsFor(s, c, nil)) == 0 {
			n++
		}
	}
	return n
}

// WorkDeadline is the deadline a work card is held to, by its state: a card
// not taken since its last deal (ready, or withdrawn again before a take) is
// late never taken past the dealt bound (s.DealtMax) from untaken_since, the
// first deal since its last take, which on a machine's row no later redeal or
// withdrawal rewrites: a card waiting in a member's ready queue is the
// machine's queue, so its own deadline starts at its take (nova-tools#5096
// item 22). The exception is a friend's row: a friend takes her own ready
// cards, so a card dealt to her after a take-back (dealt later than
// untaken_since) is measured from her own deal, never from the take-back or
// the time it waited withdrawn; a card working, or
// withdrawn from a take, is late not finished 2 hours from first_taken, the
// attempt's first take. The tick's deadline part and the no-stall rule both
// call it, so they speak at the same moment. field is the stamp it counts
// from; own is the stamp of the current member's own deal or take, which
// says whether that member has had its whole deadline ("" when the card is
// withdrawn: no member holds it). The model is tla/SprintEvents.tla, the
// untaken timer of DealtMax units.
func WorkDeadline(s *Snapshot, c *Card) (field string, limit time.Duration, word, own string) {
	first := func(fields ...string) string {
		for _, f := range fields[:len(fields)-1] {
			if c.F(f) != "" {
				return f
			}
		}
		return fields[len(fields)-1]
	}
	switch c.Col {
	case Working:
		own = "taken"
	case Ready:
		own = "dealt"
	}
	switch {
	case c.Col == Working:
		return first("first_taken", "taken"), unfinishedLimit(c), "not finished", own
	case c.F("untaken_since") != "":
		if IsFriendRow(c.Row) && c.F("dealt") > c.F("untaken_since") {
			// a friend's card dealt again after a take-back is measured from her own deal, as
			// the start bound measures it (friendUnstartedLevel): the take-back stamps
			// untaken_since and the deal to the next friend keeps it (friendRedealUnit,
			// nextGen), and measured from it the next friend's card was late the second she
			// got it, so rule friend-take took it back from her too and the card bounced
			// between friends (2026-10-08: one card, seven generations in thirty minutes,
			// started by no one). A machine's queue keeps the clock of the take before.
			return "dealt", s.DealtMax(), WordNeverTaken, own
		}
		return "untaken_since", s.DealtMax(), WordNeverTaken, own
	case c.F("first_taken") != "":
		return "first_taken", unfinishedLimit(c), "not finished", own
	}
	return first("first_dealt", "dealt"), s.DealtMax(), WordNeverTaken, own
}

// WordNeverTaken is the words of a work card late past the dealt bound.
const WordNeverTaken = "dealt, never taken"

// PartDone is the name of the tick's last part, the done part.
const PartDone = "done"

// DoneCause is the cause the machine's record carries when the done part
// stopped it, and DoneHint what the coordinator does to go on.
const (
	DoneCause = "done"
	DoneHint  = "to continue: add work, then nova-sprint start"
)

// SprintDoneCounts is the sprint's landed primaries and its dropped ones (the
// streams' control cards' dropped counters), and whether the sprint is done:
// no primary waiting, ready, working, in review or merging, and at least one
// landed or dropped. A sprint that never had a card is not done.
func SprintDoneCounts(s *Snapshot) (landed, dropped int, done bool) {
	for _, st := range []State{Waiting, Ready, Working, Review, Merging} {
		if len(s.Work.Column(st)) > 0 {
			return 0, 0, false
		}
	}
	landed = len(s.Work.Column(Landed))
	for _, row := range s.Work.Rows() {
		dropped += s.StreamCtl(row).Int("dropped")
	}
	return landed, dropped, landed+dropped > 0
}

// DoneWhat is the words of "the sprint is done": the counts and, when the
// first start is known, how long the sprint took from it in wall time.
func DoneWhat(landed, dropped int, now, started time.Time) string {
	w := fmt.Sprintf("%d landed, %d dropped", landed, dropped)
	if !started.IsZero() && !now.Before(started) {
		w += ", took " + TookText(now.Sub(started)) + " from the first start"
	}
	return w
}

// TookText is a duration as the done note and the sprint line say it: to the
// second.
func TookText(d time.Duration) string { return d.Round(time.Second).String() }

// TickDone is the sprint's done part: the sprint done
// (SprintDoneCounts) is no judgment that waits on the coordinator. The part
// writes one happened note, "the sprint is done", addressed to the
// coordinator, with the counts, the time from the first start and the hint;
// the binding stops the machine as the part's step commits, its record
// STOPPED with the cause DoneCause, so no part and no tick runs after it. It
// is the tick's last part: a tick of a RUNNING machine that finds the sprint
// done ends there. It writes nothing on a sprint that is not done, and it runs
// only while the machine is RUNNING, so it says it once for each run that
// finishes the sprint. The reference model's tick stops the same way
// (refmodel.Tick); tla/SprintEvents.tla does not model R15.
func TickDone(s *Snapshot, r TickReq) (Plan, int) {
	landed, dropped, done := SprintDoneCounts(s)
	if !done {
		return Plan{}, 0
	}
	n := happened(NSprintDone, "", s.Now)
	n.Who, n.To, n.Hint = r.who(), s.Coordinator, DoneHint
	n.What = DoneWhat(landed, dropped, s.Now, r.Started)
	return Plan{Notes: []Note{n}}, 0
}
