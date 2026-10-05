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
//     overdue part named it (its overdue line is the first reminder, this the next).
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
	// NRaisedAgain is the push of a pass judgment that still holds: a happened note to
	// the coordinator, once every PassEvery of running time.
	NRaisedAgain = "a judgment still holds: raised again"
)

// PassTypes are the pass's judgment types.
var PassTypes = []string{NFriendDeaf, NFriendIdle, NCoordinatorBehind}

const (
	// PassEvery is the running time between two raises of one pass judgment.
	PassEvery = 10 * time.Minute
	// FriendDeafAfter is how old a friend's last session pong may be before her
	// session is deaf.
	FriendDeafAfter = 10 * time.Minute
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

// TickCoordinatorPass is the pass, run by the overdue part (TickOverdue): the three
// conditions raised, raised again and closed (the comment above).
func TickCoordinatorPass(s *Snapshot, r TickReq) (Plan, int) {
	var p Plan
	conds := append(append(deafConds(s, r), idleConds(s, r)...), behindConds(s, r)...)
	due := notify(&p, s, conds, PassTypes, r)
	reraise(&p, s, conds, r)
	return p, due
}

// deafConds is one condition for each friend not held whose beat carries a session pong
// older than FriendDeafAfter, in running time.
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
