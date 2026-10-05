package sprint

import (
	"fmt"
	"maps"
	"slices"
	"time"
)

// A card pinned to a friend who is not up (docs/SPEC-SPRINT.md, "A card pinned to a
// friend who is not up"; 2026-10-04: 212 cards sat pinned to executors down or held,
// 65 of them ready, while the fleet idled, and the coordinator un-pinned them by hand).
// A ready card whose WHO line names a friend waits for her (FriendDeal), and while she is
// down or held nothing told the coordinator. Now the stall part of the tick
// (TickFriendStall) counts the wait per friend: the first tick that finds cards pinned to
// her while she is not up stamps PropFriendPinSince, and FriendPinWaitAfter later it
// raises ONE judgment naming her and every card that waits for her, stamped in
// PropFriendPinJudged so no later tick raises it again. Both stamps clear when she is up
// or no card waits for her, and the next wait counts afresh.

// FriendPinWaitAfter is how long cards wait for a friend who is not up before the
// coordinator is told.
const FriendPinWaitAfter = 30 * time.Minute

// NFriendPinned is the judgment of cards waiting for a friend who is not up.
const NFriendPinned = "cards wait for a friend who is not up"

// PropFriendPinSince is the fleet property stamping when cards began to wait for the
// friend while she was not up.
func PropFriendPinSince(friend string) string { return "friend_pin_since:" + friend }

// PropFriendPinJudged is the fleet property stamping when the friend's pin judgment was
// raised.
func PropFriendPinJudged(friend string) string { return "friend_pin_judged:" + friend }

// pinnedTo names the friend a ready primary waits for: its WHO line names her, and its
// stream is not held (a held stream's card waits for unhold, not for her). A card for any
// friend waits for no one of them.
func pinnedTo(s *Snapshot, c *Card) (string, bool) {
	name, ok := FriendCard(c)
	if !ok || name == "" || c.Col != Ready || IsSentinel(c) || StreamHeld(s, c.Row) {
		return "", false
	}
	return name, true
}

// friendPins is the pin part of TickFriendStall: for each friend with cards waiting for
// her while she is down, held or absent from the roster, it stamps the wait, and raises
// the one judgment once the wait reaches FriendPinWaitAfter; it clears both stamps of a
// friend who is up or for whom no card waits. write is the stall part's property writer.
func friendPins(s *Snapshot, r TickReq, p *Plan, write func(name, value string)) {
	status := map[string]string{}
	for _, f := range r.Friends {
		status[f.Name] = f.Status
	}
	waiting := map[string][]string{}
	for _, c := range s.Work.Column(Ready) {
		if name, ok := pinnedTo(s, c); ok {
			waiting[name] = append(waiting[name], c.ID)
		}
	}
	names := map[string]bool{}
	for name := range waiting {
		names[name] = true
	}
	for _, f := range r.Friends {
		names[f.Name] = true
	}
	for _, f := range slices.Sorted(maps.Keys(names)) {
		since, _ := s.Fleet.Prop(PropFriendPinSince(f))
		judged, _ := s.Fleet.Prop(PropFriendPinJudged(f))
		st := status[f]
		if st == "" {
			st = Down // not on the roster: nothing deals to her
		}
		if st == Up || len(waiting[f]) == 0 {
			write(PropFriendPinSince(f), "")
			write(PropFriendPinJudged(f), "")
			continue
		}
		if since == "" {
			write(PropFriendPinSince(f), stamp(s.Now))
			continue
		}
		t, err := time.Parse(time.RFC3339, since)
		if err != nil || judged != "" || s.Now.Sub(t) < FriendPinWaitAfter {
			continue
		}
		ids := waiting[f]
		jn := judgment(NFriendPinned, "", s.Now, 0, ids...)
		jn.Who, jn.To = r.who(), s.Coordinator
		jn.What = fmt.Sprintf("%d cards pinned to friend %s (WHO: friend %s) have waited %s while she is %s: %s",
			len(ids), f, f, s.Now.Sub(t).Round(time.Minute), st, Preview(ids, ", "))
		jn.Decisions = []string{
			"wait for friend " + f + " (friend up " + f + ")",
			"brief them for another friend (WHO: friend) or the fleet (WHO: -)",
			"drop",
		}
		p.Notes = append(p.Notes, jn)
		p.Units = append(p.Units, Unit{Key: PartFriendStall, Moved: fmt.Sprintf("friend %s: %d pinned cards waited %s, coordinator told", f, len(ids), FriendPinWaitAfter)})
		write(PropFriendPinJudged(f), stamp(s.Now))
	}
}
