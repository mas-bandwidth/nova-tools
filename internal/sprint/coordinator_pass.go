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
//   - a friend is starved: she is up and her row has been empty for FriendEmptyAfter
//     while cards she could do wait, ready in the pool or dealt and unstarted on another
//     friend's row (the owner, 2026-10-05: "How is it that you missed Zhi having zero
//     cards? Seems bad."). Her empty row's start is kept on the fleet table
//     (PropFriendEmptySince), so it lives across ticks.
//
// Raising again is a push: the judgment is rewritten in place with the latest facts and
// the count of its raises after the first (its Before), and a happened note NRaisedAgain
// addressed to the coordinator goes with it, so the tick's end wakes inbox --wait each
// time. The k-th raise again is due k times PassEvery of running time after the judgment
// was written; a tick that finds more than one due (the machine was not ticking) raises
// it once. An acknowledgement, or a wait until its review time, quiets it as it quiets
// any judgment. The pass runs in the tick's overdue part (TickOverdue). The model is
// tla/CoordinatorPass.tla.

// The pass's judgment types and its push.
const (
	NFriendDeaf        = "a friend's session is deaf"
	NFriendIdle        = "a friend holds working cards and finishes none"
	NCoordinatorBehind = "judgments wait on the coordinator past their deadline"
	NFriendStarved     = "an up friend's row is empty while cards she could do wait"
	// NRaisedAgain is the push of a pass judgment that still holds: a happened note to
	// the coordinator, once every PassEvery of running time.
	NRaisedAgain = "a judgment still holds: raised again"
)

// PassTypes are the pass's judgment types.
var PassTypes = []string{NFriendDeaf, NFriendIdle, NCoordinatorBehind, NFriendStarved}

const (
	// PassEvery is the running time between two raises of one pass judgment.
	PassEvery = 10 * time.Minute
	// FriendDeafAfter is how old a friend's last session pong may be before her
	// session is deaf: FriendProofLive, since her daemon asks a quiet session after ten
	// minutes and waits five for the answer (docs/SPEC-FRIEND.md, The push proof), so a
	// session that answers is never proved longer ago than that. Shorter, and a quiet
	// friend whose session answers is judged deaf every ten minutes and cleared again.
	FriendDeafAfter = FriendProofLive
	// FriendFinishDefault is the friend-finish window when the coordinator set none.
	FriendFinishDefault = 30 * time.Minute
	// FriendEmptyAfter is how long, in running time, an up friend's row is empty while
	// cards she could do wait before she is starved.
	FriendEmptyAfter = 10 * time.Minute
	// PartPass is the key of the pass's unit that writes the friends' empty rows.
	PartPass = "pass"
	// PropFriendFinish is the work table's property: the friend-finish window, a
	// duration (nova-sprint set --friend-finish).
	PropFriendFinish = "friend_finish"
)

// PropFriendEmptySince is the fleet property recording when the friend, up, was first
// seen with an empty row (no ready or working card on it); absent or "" while she is not.
func PropFriendEmptySince(friend string) string { return "friend_empty_since." + friend }

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

// TickCoordinatorPass is the pass, run by the overdue part (TickOverdue): the four
// conditions raised, raised again and closed (the comment above), and the starved
// friends' empty rows written (PropFriendEmptySince).
func TickCoordinatorPass(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	starved, props := starvedConds(s, r)
	if len(props) > 0 {
		// a unit carries the writes, as the idle alarm's does: a plan of properties alone is
		// empty (Plan.Empty) and the tick writes nothing of it; the unit changes no card, so
		// it says no move, as the reference model (refmodel) says none for it
		p.Props = props
		p.Units = append(p.Units, Unit{Key: PartPass})
	}
	conds := append(append(append(deafConds(s, r), idleConds(s, r)...), behindConds(s, r)...), starved...)
	due := notify(&p, s, conds, PassTypes, r)
	reraise(&p, s, conds, r)
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

// starvedConds is one condition for each friend up, not held, whose row has been empty
// (no ready or working card) for FriendEmptyAfter of running time while cards she could do
// wait (starvedCards), with the fleet properties that keep each empty row's start: written
// the first tick she is seen up with an empty row, cleared when she is not. The judgment
// names her, the cards and where they sit, and offers: deal them to her (friend take of
// those cards from the friend holding them: the friends' deal gives them to the friend
// with an idle lane), take the other row whole (friend take --all-unstarted), or keep.
func starvedConds(s *Snapshot, r TickReq) ([]cond, []PropWrite) {
	if s.Fleet == nil || s.Work == nil {
		return nil, nil
	}
	var props []PropWrite
	write := func(name, value string) {
		was, had := s.Fleet.Prop(name)
		if (!had && value == "") || (had && was == value) {
			return
		}
		props = append(props, PropWrite{Table: Fleet, Name: name, Value: value, Was: was, WasAbsent: !had})
	}
	seats := slices.SortedFunc(slices.Values(r.Friends), func(a, b FriendSeat) int { return strings.Compare(a.Name, b.Name) })
	known := map[string]bool{}
	var out []cond
	for _, f := range seats {
		known[f.Name] = true
		prop := PropFriendEmptySince(f.Name)
		if f.Status != Up || r.Sessions[f.Name].Held || friendLoad(s, f.Name) > 0 {
			write(prop, "")
			continue
		}
		since, _ := s.Fleet.Prop(prop)
		if since == "" {
			write(prop, stamp(s.Now))
			continue
		}
		d, ok := r.running(s.Now, since)
		if !ok || d < FriendEmptyAfter {
			continue
		}
		pool, rows := starvedCards(s, f, seats)
		if len(pool) == 0 && len(rows) == 0 {
			continue
		}
		var where, decisions []string
		if len(pool) > 0 {
			where = append(where, fmt.Sprintf("%d ready in the pool (%s)", len(pool), Preview(pool, ", ")))
		}
		others := slices.Sorted(maps.Keys(rows))
		for _, g := range others {
			where = append(where, fmt.Sprintf("%d dealt and unstarted on %s (%s)", len(rows[g]), FriendRow(g), Preview(rows[g], ", ")))
		}
		for _, g := range others {
			decisions = append(decisions, "friend take "+g+" "+strings.Join(rows[g], " "))
		}
		for _, g := range others {
			decisions = append(decisions, "friend take "+g+" --all-unstarted")
		}
		decisions = append(decisions, "keep", "wait")
		n := len(pool)
		for _, ids := range rows {
			n += len(ids)
		}
		var deal, whole []string
		for _, g := range others {
			deal = append(deal, "nova-sprint friend take "+g+" "+strings.Join(rows[g], " "))
			whole = append(whole, "nova-sprint friend take "+g+" --all-unstarted")
		}
		remedy := "deal them to her: " + strings.Join(deal, ", ") + " (the friends' deal gives them to the friend with an idle lane), take the other row: " + strings.Join(whole, ", ") + ", or keep them where they are: ack it"
		if len(others) == 0 {
			remedy = "the friends' deal did not give them to her: look at the cards (nova-sprint card <id>), or keep them where they are: ack it"
		}
		out = append(out, cond{typ: NFriendStarved, primaries: []string{FriendRow(f.Name)},
			what: fmt.Sprintf("friend %s is up and her row has been empty for %s (since %s) while %d cards she could do wait: %s; %s",
				f.Name, d.Round(time.Second), since, n, strings.Join(where, "; "), remedy),
			decisions: decisions})
	}
	// a friend gone from the roster leaves no empty row behind
	for _, name := range slices.Sorted(maps.Keys(s.Fleet.Props())) {
		if f, ok := strings.CutPrefix(name, PropFriendEmptySince("")); ok && !known[f] {
			write(name, "")
		}
	}
	return out, props
}

// starvedCards is the cards the friend could do that wait elsewhere: the primaries ready
// in the pool, and the primaries whose work cards are dealt to another friend's row and
// unstarted there (ready, or working and not started: friendStarted), by that friend. A
// card she could do is one of her tiers (friendTakes), not one that has left her
// (friendsLeft), and not a hard pin to another friend (OnlyFriend); a pool card is also
// not of a held stream, a sentinel, a bench card, or at its redeal bound (the
// coordinator's already).
func starvedCards(s *Snapshot, f FriendSeat, seats []FriendSeat) (pool []string, rows map[string][]string) {
	could := func(pr, wc *Card) bool {
		if pr == nil {
			return false
		}
		if name, _ := FriendCard(pr); OnlyFriend(pr) && name != f.Name {
			return false
		}
		return friendTakes(f, cardTierOf(escalating(s, pr))) && !slices.Contains(friendsLeft(wc), f.Name)
	}
	for _, c := range s.Work.Column(Ready) {
		if StreamHeld(s, c.Row) || IsSentinel(c) || len(Bench(c)) > 0 || AtRedealBound(s, c) != nil {
			continue
		}
		wc := s.Fleet.Placed(WorkCardID(c.ID, c.Int("attempt")))
		if wc != nil && wc.Col != Withdrawn {
			wc = nil
		}
		if could(c, wc) {
			pool = append(pool, c.ID)
		}
	}
	seatOf := map[string]FriendSeat{}
	for _, g := range seats {
		seatOf[g.Name] = g
	}
	rows = map[string][]string{}
	for _, row := range s.Fleet.Rows() {
		g, ok := FriendOfRow(row)
		if !ok || g == f.Name {
			continue
		}
		seat, ok := seatOf[g]
		if !ok {
			seat = FriendSeat{Name: g}
		}
		for _, c := range append(append([]*Card(nil), s.Fleet.Cell(row, Ready)...), s.Fleet.Cell(row, Working)...) {
			if c.Col == Working && friendStarted(s, seat, c) {
				continue
			}
			if pr := s.Work.Placed(c.F("primary")); could(pr, c) {
				rows[g] = append(rows[g], pr.ID)
			}
		}
		sort.Strings(rows[g])
	}
	sort.Strings(pool)
	return pool, rows
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
	return []cond{{typ: NCoordinatorBehind, streamLevel: true,
		what:      fmt.Sprintf("%d judgments wait past their deadline: %s; run: nova-sprint inbox", n, strings.Join(parts, "; ")),
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
		if n.Kind != Judgment || !slices.Contains(PassTypes, n.Type) || done[n.ID] {
			continue
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
