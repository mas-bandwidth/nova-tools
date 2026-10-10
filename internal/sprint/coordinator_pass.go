package sprint

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"
)

// The coordinator's pass (docs/SPEC-SPRINT.md section 8, "The coordinator's pass"; the
// owner, 2026-10-05: "everything I described above needs to be mechanical, so you have a
// reminder to do it (notification) coming from the machine every 10 minutes. Otherwise,
// you will eventually drift and forget."). Three conditions the coordinator must act on,
// each a judgment the tick raises once an episode, raises again every PassEvery of
// running time while it holds, and closes when it stops holding:
//
//   - a friend's session is deaf: her beat carries her session's last pong (friend beat
//     --pong) and it is older than FriendDeafAfter;
//   - a friend is idle: she holds working cards and no work card of hers went from
//     working to done in the friend-finish window (FriendFinishAfter; a read is not
//     card work);
//   - the coordinator is behind: judgments wait on the coordinator past their due time,
//     by kind and count, each counted once PassEvery of running time has run since the
//     overdue part named it (its overdue line is the first reminder, this the next);
//   - an up friend has had an empty row for EmptyRowAfter while cards she could do sit
//     ready in the pool or unstarted on another friend's row (emptyConds);
//   - a named pin sits ready or working off its friend's row (pinConds). The deal writes
//     that judgment on the unit that places the card on someone else (friendDealPass); the
//     pass keeps it, and writes it when the card is there without that note.
//
// Raising again is a push: the judgment is rewritten in place with the latest facts and
// the count of its raises after the first (its Before), and a happened note NRaisedAgain
// addressed to the coordinator goes with it, so the tick's end wakes inbox --wait each
// time. The k-th raise again is due k times PassEvery of running time after the judgment
// was written; a tick that finds more than one due (the machine was not ticking) raises
// it once. An acknowledgement, or a wait until its review time, quiets it as it quiets
// any judgment. The pass runs in the tick's overdue part (TickOverdue). The model is
// tla/CoordinatorPass.tla; Kind = "empty" is the empty-row clock (EmptyAWholeWindow)
// and Kind = "pin" the deal's judgment the pass keeps (OneJudgmentAnEpisode).

// The pass's judgment types and its push.
const (
	NFriendDeaf        = "a friend's session is deaf"
	NFriendIdle        = "a friend holds working cards and finishes none"
	NCoordinatorBehind = "judgments wait on the coordinator past their deadline"
	// NFriendEmpty is an up friend whose row has been empty for EmptyRowAfter while
	// cards she could do sit ready in the pool or unstarted on another friend's row.
	NFriendEmpty = "an up friend has an empty row while cards wait"
	// NPinIgnored is a named pin (WHO: friend <name>, not a hard pin) sitting ready
	// or working off that friend's row.
	NPinIgnored = "a pinned card was dealt away from its friend"
	// NFriendRowEmpty is the clock for NFriendEmpty: an acknowledgement, not a
	// judgment, written when an up friend's empty row and the cards she could do
	// first hold together, and closed when they do not. It is not one of PassTypes,
	// so a pass does not raise it and does not close it by the judgment rule.
	NFriendRowEmpty = "an up friend's empty row"
	// NRaisedAgain is the push of a pass judgment that still holds: a happened note to
	// the coordinator, once every PassEvery of running time.
	NRaisedAgain = "a judgment still holds: raised again"
)

// PassTypes are the pass's judgment types: its own and the automatic stops it keeps
// (StopTypes, stops.go). NFriendRowEmpty is the empty-row clock, not a judgment, and stays
// off this list.
var PassTypes = append([]string{NFriendDeaf, NFriendIdle, NCoordinatorBehind, NFriendEmpty, NPinIgnored}, StopTypes...)

// The two judgments list ack and wait on TickDecisions, same as deaf and idle, so an
// acknowledgement is kept on the condition (steps_ack.go) and the next pass does not
// write a second note. steps_tick.go is not this card's file; the map is the tick's
// and init is how this file joins it.
func init() {
	TickDecisions[NFriendEmpty] = []string{"ack", "wait"}
	TickDecisions[NPinIgnored] = []string{"ack", "wait"}
}

const (
	// PassEvery is the running time between two raises of one pass judgment.
	PassEvery = 10 * time.Minute
	// EmptyRowAfter is how long an up friend's row stays empty, while cards she
	// could do wait elsewhere, before the pass tells the coordinator once.
	EmptyRowAfter = 10 * time.Minute
	// FriendDeafAfter is how old a friend's last session pong may be before her
	// session is deaf: FriendProofLive, since her daemon asks her session eight minutes
	// after its last ask and waits up to five for the answer (nova-friend's ProveEvery and
	// SessionBound; docs/SPEC-FRIEND.md, The push proof), so a session that answers is
	// never proved longer ago than thirteen minutes. Shorter, and a friend whose session
	// answers slowly is judged deaf and cleared again every cycle.
	FriendDeafAfter = FriendProofLive
	// FriendFinishDefault is the friend-finish window when the coordinator set none.
	FriendFinishDefault = 30 * time.Minute
	// PropFriendFinish is the work table's property: the friend-finish window, a
	// duration (nova-sprint set --friend-finish).
	PropFriendFinish = "friend_finish"
)

// FriendSession is what the tick knows of a friend's session beside the tables: her
// session's last pong as her last beat carries it (zero: her beat carries none), and
// whether the coordinator holds her (friend down).
type FriendSession struct {
	Pong time.Time
	Held bool
}

// FriendFinishAfter is the friend-finish window: the sprint's setting, else
// FriendFinishDefault.
func (s *Snapshot) FriendFinishAfter() time.Duration {
	if s.Work != nil {
		if v, ok := s.Work.Prop(PropFriendFinish); ok {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				return d
			}
		}
	}
	return FriendFinishDefault
}

// WithFriendFinish is the set step's plan with the friend-finish window written too: a
// duration above zero, or default. An empty value, or a plan refused for any reason but
// that it had nothing else to set, is returned as it is.
func WithFriendFinish(p Plan, s *Snapshot, v string) Plan {
	if v == "" {
		return p
	}
	for _, x := range p.Refused {
		if !strings.HasPrefix(x.Why, "nothing to set") {
			return p
		}
	}
	p.Refused = nil
	if v != ReadTierDefault {
		if d, err := time.ParseDuration(v); err != nil || d <= 0 {
			return Plan{Refused: []Refusal{{Key: "set", Why: "--friend-finish wants a duration above zero (30m, 1h), or " + ReadTierDefault + " for " + FriendFinishDefault.String() + "; found " + v}}}
		}
	}
	was, had := s.Work.Prop(PropFriendFinish)
	p.Props = append(p.Props, PropWrite{Table: Work, Name: PropFriendFinish, Value: v, Was: was, WasAbsent: !had})
	word := v
	if v == ReadTierDefault {
		word = fmt.Sprintf("default (%s)", FriendFinishDefault)
	}
	for i := range p.Units {
		if p.Units[i].Key == "set" {
			if strings.TrimSpace(p.Units[i].Moved) == "sprint" {
				p.Units[i].Moved = "sprint friend-finish " + word
			} else {
				p.Units[i].Moved += ", friend-finish " + word
			}
			return p
		}
	}
	p.Units = append(p.Units, Unit{Key: "set", Moved: "sprint friend-finish " + word})
	return p
}

// TickCoordinatorPass is the pass, run by the overdue part (TickOverdue): the
// conditions raised, raised again and closed (the comment above).
func TickCoordinatorPass(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	conds := append(append(deafConds(s, r), idleConds(s, r)...), behindConds(s, r)...)
	conds = append(conds, emptyConds(&p, s, r)...)
	conds = append(conds, pinConds(s, r)...)
	// every automatic stop the machine made, while it holds (stops.go)
	conds = append(conds, stopConds(s, r)...)
	due := notify(&p, s, conds, PassTypes, r)
	reraise(&p, s, conds, r)
	stopsDigest(&p, s, r)
	return p, due
}

// deafConds is one condition for each friend not held whose beat carries a session pong
// older than FriendDeafAfter, in running time: the nova-friend daemon's beat carries
// her session's last proof (--pong), so a lapsed proof is told to the coordinator once,
// as this judgment, with the remedy (docs/SPEC-SPRINT.md, "The coordinator's pass").
func deafConds(s *Snapshot, r TickReq) []cond {
	var out []cond
	for _, f := range slices.Sorted(maps.Keys(r.Sessions)) {
		ss := r.Sessions[f]
		if ss.Held || ss.Pong.IsZero() {
			continue
		}
		age, ok := r.running(s.Now, stamp(ss.Pong))
		if !ok || age <= FriendDeafAfter {
			continue
		}
		out = append(out, cond{typ: NFriendDeaf, primaries: []string{FriendRow(f)},
			what: fmt.Sprintf("friend %s: her session's last pong was %s ago (at %s), past %s; wake her: nova-friend ping --as <coordinator> --to %s --wake, then the debug steps of docs/SPEC-FRIEND.md (Presence, The harness check)",
				f, age.Round(time.Second), stamp(ss.Pong), FriendDeafAfter, f),
			decisions: []string{"nova-friend ping --as <coordinator> --to " + f + " --wake", "debug: docs/SPEC-FRIEND.md, The harness check", "friend down " + f + " --reason deaf", "ack", "wait"}})
	}
	return out
}

// idleConds is one condition for each friend not held holding working cards whose last
// working-to-done finish, or her oldest working card's take when that is later, is older
// than the friend-finish window, in running time. Only her work cards count: a read is
// not card work.
func idleConds(s *Snapshot, r TickReq) []cond {
	if s.Fleet == nil {
		return nil
	}
	window := s.FriendFinishAfter()
	var out []cond
	for _, row := range s.Fleet.Rows() {
		f, ok := FriendOfRow(row)
		if !ok || r.Sessions[f].Held || s.MemberCtl(row).F("status") == Held {
			continue
		}
		working := s.Fleet.Cell(row, Working)
		if len(working) == 0 {
			continue
		}
		var last, oldest time.Time
		for _, c := range append(append([]*Card(nil), s.Fleet.Cell(row, DoneOK)...), s.Fleet.Cell(row, DoneFailed)...) {
			if t := stampAt(c, "finished"); t.After(last) {
				last = t
			}
		}
		var ids []string
		for _, c := range working {
			ids = append(ids, c.ID)
			t := stampAt(c, "taken")
			if t.IsZero() {
				t = stampAt(c, "dealt")
			}
			if !t.IsZero() && (oldest.IsZero() || t.Before(oldest)) {
				oldest = t
			}
		}
		since := last
		if oldest.After(since) {
			since = oldest
		}
		if since.IsZero() {
			continue
		}
		d, ok := r.running(s.Now, stamp(since))
		if !ok || d <= window {
			continue
		}
		sort.Strings(ids)
		finish := "no finish yet"
		if !last.IsZero() {
			lastAge, _ := r.running(s.Now, stamp(last))
			finish = fmt.Sprintf("her last finish %s ago (at %s)", lastAge.Round(time.Second), stamp(last))
		}
		out = append(out, cond{typ: NFriendIdle, primaries: []string{row},
			what: fmt.Sprintf("friend %s holds %d working (%s) and finished none in %s: %s; check in on her: nova-friend ping --as <coordinator> --to %s --wake",
				f, len(ids), Preview(ids, ", "), window, finish, f),
			decisions: []string{"nova-friend ping --as <coordinator> --to " + f + " --wake", "friend take " + f + " --all-unstarted", "friend down " + f + " --reason idle", "ack", "wait"}})
	}
	return out
}

// behindConds is the one condition, about the sprint, that judgments wait on the
// coordinator past their due time: by type, with the count and the judgments of each.
// A judgment counts once PassEvery of running time has run since its overdue line (the
// overdue part's hold, NOverdue), so a late judgment is pushed at its deadline by the
// overdue line and every PassEvery after by the pass, never twice in one tick. The
// pass's own judgments are not counted: they are raised again on their own.
func behindConds(s *Snapshot, r TickReq) []cond {
	marked := map[string]time.Time{} // judgment id: when the overdue part first named it
	for _, o := range s.Acked {
		if o.Note.Type != NOverdue {
			continue
		}
		if at, ok := marked[o.Note.What]; !ok || o.Note.At.Before(at) {
			marked[o.Note.What] = o.Note.At
		}
	}
	byType := map[string][]string{}
	seen := map[string]bool{}
	n := 0
	var since, oldest time.Time // the oldest counted judgment's overdue line, and its write
	var oldestID string
	for _, o := range s.Open {
		j := o.Note
		if j.Kind != Judgment || j.Type == NSprintDone || slices.Contains(PassTypes, j.Type) || seen[j.ID] {
			continue
		}
		seen[j.ID] = true
		if !JudgmentOverdue(s, r, j) {
			continue
		}
		// the overdue line is the first reminder; the pass is the next, PassEvery after it
		at, ok := marked[j.ID]
		if !ok {
			continue
		}
		if d, ok := r.running(s.Now, stamp(at)); !ok || d < PassEvery {
			continue
		}
		// by id, as the overdue line names it: an alias is the store's, not the decision's
		byType[j.Type] = append(byType[j.Type], j.ID)
		n++
		if since.IsZero() || at.Before(since) {
			since = at
		}
		if oldest.IsZero() || j.At.Before(oldest) {
			oldest, oldestID = j.At, j.ID
		}
	}
	if n == 0 {
		return nil
	}
	var parts []string
	for _, typ := range slices.Sorted(maps.Keys(byType)) {
		refs := byType[typ]
		sort.Strings(refs)
		parts = append(parts, fmt.Sprintf("%d %s (%s)", len(refs), typ, Preview(refs, ", ")))
	}
	// escalating (stops.go, BehindLevel): the level opens the text and is part of the
	// condition, so each new level is a new judgment whatever quieted the one before
	level := BehindLevel(s, r, since)
	age, _ := r.running(s.Now, stamp(oldest))
	return []cond{{typ: NCoordinatorBehind, streamLevel: true,
		what: fmt.Sprintf("%s%d: %d judgments wait past their deadline, the oldest %s for %s: %s; run: nova-sprint inbox",
			behindLevelPrefix, level, n, oldestID, age.Truncate(time.Second), strings.Join(parts, "; ")),
		decisions: []string{"act", "wait"}}}
}

// reraise raises again each open pass judgment whose condition still holds and whose
// next raise is due (Before+1 times PassEvery of running time after it was written),
// unless the coordinator's wait holds it to a review time not yet reached: the judgment
// rewritten with the latest facts and the raises due counted in Before, and the push to
// the coordinator.
func reraise(p *Plan, s *Snapshot, conds []cond, r TickReq) {
	holding := map[string]cond{}
	for _, c := range conds {
		for _, sub := range c.subjects() {
			holding[condKey(c.typ, sub, c.card, c.what)] = c
		}
	}
	to := s.Coordinator
	if to == "" {
		to = "coordinator"
	}
	done := map[string]bool{}
	for _, o := range s.Open {
		n := o.Note
		if n.Kind != Judgment || !slices.Contains(PassTypes, n.Type) || slices.Contains(StopTypes, n.Type) || done[n.ID] {
			continue // a stop is raised again in the one digest of the tick (stopsDigest, stops.go)
		}
		c, ok := holding[condKey(n.Type, o.Subject(), n.Card, n.What)]
		if !ok {
			continue
		}
		done[n.ID] = true
		if !n.Review.IsZero() && s.Now.Before(n.Review) {
			continue
		}
		d, ok := r.running(s.Now, stamp(n.At))
		k := int(d / PassEvery)
		if !ok || k <= n.Before {
			continue
		}
		n.Before, n.What = k, c.what
		if len(c.decisions) > 0 {
			n.Decisions = append([]string(nil), c.decisions...)
		}
		p.Updates = append(p.Updates, n)
		push := Note{Kind: Happened, Type: NRaisedAgain, Stream: n.Stream, Primaries: n.Primaries, Count: n.Count, Who: r.who(), To: to, At: s.Now,
			What: fmt.Sprintf("%s (%s) still holds, open since %s: %s", n.ID, n.Type, stamp(n.At), c.what),
			Hint: "run: nova-sprint inbox; ack it, or wait it, to quiet it"}
		p.Notes = append(p.Notes, push)
	}
}

// idleWait is one card an empty friend could do, and the other friend whose row
// holds it when it is not sitting ready in the pool.
type idleWait struct {
	phrase string
	holder string
}

// emptyConds is one condition for each friend who is up, not held, and has had
// an empty row for EmptyRowAfter of running time while cards she could do sit
// ready in the pool or unstarted on another friend's row. The ten minutes start
// when that conjunction first holds: an acknowledgement NFriendRowEmpty on her
// row, closed when she is not up, her row is not empty, or nothing she could do
// is waiting, so time down or held does not count. The judgment's text names her,
// those cards and where they sit, and offers to deal them to her, to have a friend
// take the other row, or to keep. While the cards sit where the text says, the
// text does not change, so the episode stays one (the condition key includes the
// text; steps_tick.go is not this card's file).
func emptyConds(p *Plan, s *Snapshot, r TickReq) []cond {
	watches := map[string]Open{}
	for _, o := range s.Acked {
		if o.Note.Kind == Acknowledged && o.Note.Type == NFriendRowEmpty {
			watches[o.Subject()] = o
		}
	}
	openWhat := map[string]string{}
	for _, o := range s.Open {
		if o.Note.Kind == Judgment && o.Note.Type == NFriendEmpty {
			openWhat[o.Subject()] = o.Note.What
		}
	}
	if s.Fleet == nil {
		for _, w := range watches {
			p.Closes = append(p.Closes, w)
		}
		return nil
	}
	seats := map[string]FriendSeat{}
	for _, f := range r.Friends {
		seats[f.Name] = f
	}
	names := slices.Sorted(maps.Keys(seats))
	want := map[string]bool{}
	var out []cond
	for _, name := range names {
		f := seats[name]
		row := FriendRow(name)
		up := f.Status == Up && !r.Sessions[name].Held && s.MemberCtl(row).F("status") != Held && !s.FriendsOff()
		var cards []idleWait
		if up && friendLoad(s, name) == 0 {
			cards = cardsWaitingFor(s, f, seats)
		}
		if !up || friendLoad(s, name) != 0 || len(cards) == 0 {
			continue
		}
		want[row] = true
		_, judged := openWhat[row]
		w, watching := watches[row]
		if !watching && !judged {
			p.Notes = append(p.Notes, Note{Kind: Acknowledged, Type: NFriendRowEmpty, Primaries: []string{row}, Count: 1,
				What: row, Who: r.who(), At: s.Now})
			continue
		}
		if !judged {
			d, ok := r.running(s.Now, stamp(w.Note.At))
			if !ok || d < EmptyRowAfter {
				continue
			}
		}
		what := emptyRowWhat(name, cards)
		if prev := openWhat[row]; prev != "" {
			what = prev // one episode while it holds; the key includes the text
		}
		out = append(out, cond{typ: NFriendEmpty, primaries: []string{row}, what: what, decisions: emptyRowDecisions(name, cards)})
	}
	for row, w := range watches {
		if !want[row] {
			p.Closes = append(p.Closes, w)
		}
	}
	return out
}

// cardsWaitingFor are the cards f could do that are not on her row: a ready
// primary in the pool, or an unstarted work card on another friend's row.
func cardsWaitingFor(s *Snapshot, f FriendSeat, seats map[string]FriendSeat) []idleWait {
	var out []idleWait
	if s.Work != nil {
		for _, pr := range s.Work.Column(Ready) {
			if pr == nil || IsSentinel(pr) || StreamHeld(s, pr.Row) || len(Bench(pr)) > 0 {
				continue
			}
			if !friendCouldTake(s, f, pr, nil) {
				continue
			}
			out = append(out, idleWait{phrase: pr.ID + " ready in the pool"})
		}
	}
	if s.Fleet == nil {
		sort.Slice(out, func(i, j int) bool { return out[i].phrase < out[j].phrase })
		return out
	}
	her := FriendRow(f.Name)
	for _, row := range s.Fleet.Rows() {
		if row == her {
			continue
		}
		holder, friendRow := FriendOfRow(row)
		if !friendRow {
			continue
		}
		for _, col := range []string{Ready, Working} {
			for _, wc := range s.Fleet.Cell(row, col) {
				if wc.F("kind") != "work" {
					continue
				}
				pr := s.Work.Placed(wc.F("primary"))
				if pr == nil || IsSentinel(pr) || StreamHeld(s, pr.Row) {
					continue
				}
				if friendStarted(s, seats[holder], wc) || !friendCouldTake(s, f, pr, wc) {
					continue
				}
				out = append(out, idleWait{holder: holder, phrase: fmt.Sprintf("%s on %s:%s unstarted", wc.ID, row, col)})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].phrase < out[j].phrase })
	return out
}

// friendCouldTake says f may be given the primary: her tiers hold its tier, it
// is not a hard pin to someone else, and the work card has not left her.
func friendCouldTake(s *Snapshot, f FriendSeat, pr, wc *Card) bool {
	if pr == nil || !friendTakes(s, f, cardTierOf(pr)) {
		return false
	}
	if name, ok := FriendCard(pr); ok && name != "" && name != f.Name && OnlyFriend(pr) {
		return false
	}
	return wc == nil || !slices.Contains(friendsLeft(wc), f.Name)
}

// emptyRowWhat names the friend, the cards and where they sit. EmptyRowAfter is
// the constant, not a live age, so the text stays put while the cards do.
func emptyRowWhat(name string, cards []idleWait) string {
	phrases := make([]string, len(cards))
	for i, c := range cards {
		phrases[i] = c.phrase
	}
	return fmt.Sprintf("friend %s has had an empty row for %s while %d cards she could do sit elsewhere: %s; deal them to her, friend take the other row, or keep",
		name, EmptyRowAfter, len(phrases), strings.Join(phrases, ", "))
}

// emptyRowDecisions are the offers: deal them to her, friend take each other
// row that holds an unstarted card, or keep. ack and wait are how the pass
// quiets any of its judgments.
func emptyRowDecisions(name string, cards []idleWait) []string {
	ds := []string{"deal them to " + name}
	seen := map[string]bool{}
	var holders []string
	for _, c := range cards {
		if c.holder != "" && !seen[c.holder] {
			seen[c.holder] = true
			holders = append(holders, c.holder)
		}
	}
	sort.Strings(holders)
	for _, h := range holders {
		ds = append(ds, "friend take "+h+" --all-unstarted")
	}
	return append(ds, "keep", "ack", "wait")
}

// pinConds is one condition for each named pin whose work card is ready or
// working on a row that is not its friend's. A hard pin (OnlyFriend) waits for
// her, and is one of these only once released past its bound (PinReleased). The text is the one the deal wrote when it
// rotated the card, when that judgment is already open, so a later pass does
// not close it and open another.
func pinConds(s *Snapshot, r TickReq) []cond {
	if s.Fleet == nil || s.Work == nil {
		return nil
	}
	openWhat := map[string]string{}
	for _, o := range s.Open {
		if o.Note.Kind == Judgment && o.Note.Type == NPinIgnored {
			openWhat[o.Subject()] = o.Note.What
		}
	}
	free := map[string]int{}
	for _, f := range r.Friends {
		if f.Status != Up {
			continue
		}
		room, _ := friendRoom(f)
		free[f.Name] = room - friendLoad(s, f.Name)
	}
	var out []cond
	seen := map[string]bool{}
	for _, row := range s.Fleet.Rows() {
		for _, col := range []string{Ready, Working} {
			for _, wc := range s.Fleet.Cell(row, col) {
				if wc.F("kind") != "work" {
					continue
				}
				prID := wc.F("primary")
				if prID == "" || seen[prID] {
					continue
				}
				pr := s.Work.Placed(prID)
				if pr == nil {
					continue
				}
				pinned, ok := FriendCard(pr)
				// a hard pin off her row was released (PinReleased): judged as a named pin is
				if !ok || pinned == "" || row == FriendRow(pinned) {
					continue
				}
				seen[prID] = true
				what := openWhat[prID]
				if what == "" {
					what = pinIgnoredWhat(wc.ID, pinned, pinSkipWhy(s, r.Friends, pinned, friendsLeft(wc), cardTierOf(pr), free), row, col)
				}
				holder, _ := FriendOfRow(row)
				out = append(out, cond{typ: NPinIgnored, stream: pr.Row, card: wc.ID, primaries: []string{prID},
					what: what, decisions: pinIgnoredDecisions(pinned, holder, wc.ID)})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].primaries[0] < out[j].primaries[0] })
	return out
}

// pinSkipWhy is why a named pin was not placed on her row, checked in the
// deal's order: not up (held is its own reason), the card has left her, her
// tiers do not hold the tier, she has no room.
func pinSkipWhy(s *Snapshot, seats []FriendSeat, pinned string, left []string, tier string, free map[string]int) string {
	var seat FriendSeat
	found := false
	for _, f := range seats {
		if f.Name == pinned {
			seat = f
			found = true
			break
		}
	}
	switch {
	case !found || seat.Status == Down || seat.Status == "":
		return "she is not up"
	case seat.Status == Held:
		return "she is held"
	case seat.Status != Up:
		return "she is not up"
	case slices.Contains(left, pinned):
		return "it has left her"
	case !s.FriendsTake(tier):
		return "the friends' tiers leave out " + tier
	case !friendTakes(s, seat, tier):
		return "her tiers do not hold " + tier
	case free[pinned] <= 0:
		return "she has no room"
	default:
		return "she did not take it"
	}
}

// pinIgnoredWhat names the card, the friend it was pinned to, why she did not
// take it, and the row and column that hold it now.
func pinIgnoredWhat(cardID, pinned, why, row, col string) string {
	return fmt.Sprintf("%s pinned to %s went to %s:%s: %s did not take it because %s", cardID, pinned, row, col, pinned, why)
}

// pinIgnoredDecisions offers friend take of the row that holds the card, when
// that row is a friend's, or keep. ack and wait quiet it like the other pass
// judgments.
func pinIgnoredDecisions(pinned, holder, cardID string) []string {
	ds := []string{"keep", "ack", "wait"}
	if holder != "" {
		ds = append([]string{"friend take " + holder + " " + cardID + " --reason pinned to " + pinned}, ds...)
	}
	return ds
}

// pinIgnoredNote is the judgment the deal writes on the unit that places a
// named pin on someone else's row. The pass's pinConds keeps that same text.
func pinIgnoredNote(s *Snapshot, primary *Card, cardID, pinned, why, row, col string) Note {
	holder, _ := FriendOfRow(row)
	return Note{Kind: Judgment, Type: NPinIgnored, Stream: primary.Row, Primaries: []string{primary.ID}, Count: 1,
		Card: cardID, What: pinIgnoredWhat(cardID, pinned, why, row, col), Who: MachineActor, At: s.Now, Marked: true,
		Decisions: pinIgnoredDecisions(pinned, holder, cardID)}
}
