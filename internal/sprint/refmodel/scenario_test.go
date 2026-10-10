package refmodel_test

import (
	"fmt"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// A scenario is a short script of a sprint that a random walk reaches by luck
// only now and then: a stream stopped on another's card that has landed since,
// ready queues left uneven by a member taking its cards, a lateness raised and
// then the card's member going down. The scripts draw their sprint from a seed
// as the walks do, and take a snapshot where the tick has something to do.

// scenarioSeeds is how many sprints each scenario is run on, and scenarioBase
// the first seed, kept clear of the walks' seeds.
const (
	scenarioSeeds = 16
	scenarioBase  = 1000
)

// scenarios is the snapshots of every scenario on each of its seeds.
func scenarios() []sample {
	var out []sample
	for seed := uint64(0); seed < scenarioSeeds; seed++ {
		for _, run := range []func(*walk) []sample{stoppedOnALandedCard, unevenQueues, unevenReads, aLateCardRedealt, lanesFreedBesideABacklog, aFriendStalls, aCardPastItsCap, aMemberWithFreeLanes, aQueuedCardOnAFullFriend} {
			out = append(out, run(newWalk(scenarioBase+seed))...)
		}
	}
	return out
}

// addTo admits a primary into the stream, needing the primaries named, and says
// its id; "" when it was not admitted.
func (k *walk) addTo(stream string, needs ...string) string {
	id := fmt.Sprintf("p%d", k.next)
	k.next++
	if !k.try(sprint.Add(k.s, sprint.AddReq{Brief: proBrief, Stream: stream, IDs: []string{id}, Needs: needs, Who: coordinator})) {
		return ""
	}
	return id
}

// advance takes a primary on through its lifecycle as far as the state it is
// given, by the steps of its workers, its readers and the coordinator: dealt,
// taken, finished ok, asked, read ok by two readers, accepted, merged. It says
// whether the primary is there.
func (k *walk) advance(id string, to sprint.State) bool {
	at := func() sprint.State { return k.s.StateOf(id) }
	work := func() *sprint.Card { return k.s.Fleet.Placed(k.s.Work.Card(id).F("work")) }
	if at() == sprint.Ready {
		k.try(sprint.Deal(k.s, sprint.DealReq{Sel: sprint.Sel{IDs: []string{id}}, Who: sprint.MachineActor}))
	}
	if wc := work(); at() == sprint.Working && wc != nil && wc.Col == sprint.Ready {
		k.try(sprint.Take(k.s, sprint.TakeReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}, Who: wc.Row}))
	}
	if wc := work(); at() == sprint.Working && to != sprint.Working && wc != nil && wc.Col == sprint.Working {
		k.try(sprint.Finish(k.s, sprint.FinishReq{As: wc.Row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}, Head: "h-" + id, Report: "ok", Who: wc.Row}))
	}
	if at() == sprint.Review && to != sprint.Review {
		// the reads are asked one at a time: each read ok, the next is asked
		for range 2 {
			k.try(sprint.Ask(k.s, sprint.AskReq{Sel: sprint.Sel{IDs: []string{id}}, Who: sprint.MachineActor}))
			for _, rc := range k.s.Readers.Of(id) {
				if rc.Col == sprint.Asked {
					k.try(sprint.Read(k.s, sprint.ReadReq{As: rc.Row, Verdict: "ok", Finding: "f", Sel: sprint.Sel{IDs: []string{rc.ID}}, Who: rc.Row}))
				}
			}
		}
		k.try(sprint.Accept(k.s, sprint.AcceptReq{Sel: sprint.Sel{IDs: []string{id}}, Who: coordinator}))
	}
	if at() == sprint.Merging && to == sprint.Landed {
		k.try(sprint.MergeStep(k.s, sprint.MergeReq{Stream: k.s.Work.Card(id).Row, Batch: walkMaxPrimaries, Who: merger}))
	}
	return at() == to
}

// runPart runs one part of the tick by name, as the machine would.
func (k *walk) runPart(name string) bool {
	for _, part := range sprint.TickParts {
		if part.Name == name {
			k.s.Now = k.now
			plan, _ := part.Fn(k.s, k.req())
			return k.try(plan)
		}
	}
	panic("no part of the tick named " + name)
}

// stoppedOnALandedCard stops a stream on a card of another stream, and lands
// that card by a path that moves nothing after it: the tick has a stream to
// resume.
func stoppedOnALandedCard(k *walk) []sample {
	a, b := k.streams[0], k.streams[1]
	pa, pb := k.addTo(a), k.addTo(b)
	if !k.advance(pa, sprint.Merging) {
		return nil
	}
	if !k.try(sprint.MergeStep(k.s, sprint.MergeReq{Stream: a, Batch: 1, Cross: pa + "=" + pb, Who: merger})) {
		return nil
	}
	out := []sample{k.sample()}
	if !k.advance(pb, sprint.Merging) {
		return out
	}
	c := k.s.Work.Card(pb)
	c.Col = sprint.Landed
	c.Rev++
	k.s.Work.Put(c)
	return append(out, k.sample())
}

// unevenQueues fills the members' ready queues and has one member take all of
// its own: the queues differ by more than one.
func unevenQueues(k *walk) []sample {
	for range 2 * len(k.members) {
		k.addTo(k.streams[0])
	}
	k.wholeTick()
	if !k.unlevel() {
		return nil
	}
	return []sample{k.sample()}
}

// unevenReads fills the readers with asked reads, three a reader, and has one
// reader read all of its own: the loads differ by more than one, and the
// readers' level has reads to move (reads are asked one at a time, so the walks
// alone seldom pile them up).
func unevenReads(k *walk) []sample {
	readers := k.s.Readers.Rows()
	var ids []string
	for range 3 * len(readers) {
		if id := k.addTo(k.streams[0]); id != "" {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		k.advance(id, sprint.Review)
	}
	k.wholeTick() // asks each primary its first read, round the readers
	first := readers[0]
	var mine []string
	for _, c := range k.s.Readers.Cell(first, sprint.Asked) {
		mine = append(mine, c.ID)
	}
	if len(mine) == 0 || !k.try(sprint.Read(k.s, sprint.ReadReq{As: first, Sel: sprint.Sel{IDs: mine}, Verdict: "ok", Finding: "f", Who: first})) {
		return nil
	}
	return []sample{k.sample()}
}

// aMemberWithFreeLanes has one member take all of its ready cards and finish
// them, while the others hold ready backlogs: its lanes are free beside them, the
// case the tick's level evens. The walks drop a chain whole now (a card a waiting
// card needs is refused without the cascade, docs/SPEC-SPRINT.md section 11), so
// they reach this case less often than the scenario does.
func aMemberWithFreeLanes(k *walk) []sample {
	for range 4 * len(k.members) {
		k.addTo(k.streams[0])
	}
	k.wholeTick()
	if !k.unlevel() || !k.emptyLanes() {
		return nil
	}
	return []sample{k.sample()}
}

// aLateCardRedealt lets a dealt card go untaken past its deadline, has the tick
// raise the lateness, and then takes the card's member down: the card is dealt
// to another, and the lateness is to be rewritten with where it is now.
func aLateCardRedealt(k *walk) []sample {
	for range 2 * len(k.members) {
		k.addTo(k.streams[0])
	}
	k.wholeTick()
	k.now = k.now.Add(sprint.DealtMaxDefault + time.Duration(1+k.pick(20))*time.Minute)
	k.beatAll()
	if !k.runPart("deadlines") {
		return nil
	}
	out := []sample{k.sample()}
	for _, m := range k.members {
		if k.s.Fleet.Count(m, sprint.Ready) > 0 && k.try(sprint.FleetStep(k.s, sprint.FleetReq{Op: "hold", Member: m, Who: coordinator})) {
			return append(out, k.sample())
		}
	}
	return out
}

// lanesFreedBesideABacklog fills the members' ready queues past their lanes and has
// one member finish all it is working on: its lanes are free beside the others'
// backlogs, the level's case (the level moves only to a member with free lanes).
func lanesFreedBesideABacklog(k *walk) []sample {
	for range 4 * len(k.members) {
		k.addTo(k.streams[0])
	}
	k.wholeTick()
	for _, m := range k.members {
		k.try(sprint.Take(k.s, sprint.TakeReq{As: m, Sel: sprint.Sel{Limit: 1}, Who: m}))
	}
	if !k.emptyLanes() {
		return nil
	}
	return []sample{k.sample()}
}

// friendBrief is a card's brief that names the friend it is hers.
func friendBrief(name string) string {
	return "c: a friend's card\nREPO: mas-bandwidth/nova-tools\nWHO: friend " + name + "\n\nThe task."
}

// aFriendStalls deals a friend two cards and lets her go silent: no session
// activity, no progress stamp, past the stall bound and then a step at a time, the
// friend stall climbing her ladder (the wake turns, the coordinator's judgment, her
// unstarted cards taken back, marked down), a snapshot before each climb.
func aFriendStalls(k *walk) []sample {
	k.friends = []sprint.FriendSeat{{Name: "fia", Width: 3, Status: sprint.Up, Class: cardhdr.RouteFrontier, Tiers: []string{cardhdr.RouteFlash, cardhdr.RouteFrontier}}}
	for i := range 2 {
		id := fmt.Sprintf("p%d", k.next)
		k.next++
		if !k.try(sprint.Add(k.s, sprint.AddReq{Brief: friendBrief("fia"), Stream: k.streams[i%len(k.streams)], IDs: []string{id}, Who: coordinator})) {
			return nil
		}
	}
	if !k.runPart("deal") {
		return nil
	}
	var out []sample
	for _, past := range []time.Duration{sprint.FriendStallAfterDefault + time.Minute, sprint.FriendStallAfterDefault + 2*sprint.FriendStallStepDefault + time.Minute,
		sprint.FriendStallAfterDefault + 4*sprint.FriendStallStepDefault + time.Minute} {
		k.now = t0.Add(past + time.Duration(k.pick(30))*time.Second)
		k.beatAll()
		out = append(out, k.sample())
		k.runPart(sprint.PartFriendStall)
	}
	return out
}

// aCardPastItsCap sets a stream's attempt cap to one (its control card's field, which a
// snapshot carries), deals a card of it to a member and
// holds every member, so the card's work is withdrawn and it is ready again past its
// cap with a frontier friend up with room: the cap's default answer has a card to give
// her, and no machine could take it.
func aCardPastItsCap(k *walk) []sample {
	k.friends = []sprint.FriendSeat{{Name: "gus", Width: 2, Status: sprint.Up, Class: cardhdr.RouteFrontier}}
	id := k.addTo(k.streams[0]) // the stream is made by its first card
	if id == "" || !k.try(sprint.Set(k.s, sprint.SetReq{Streams: []string{k.streams[0]}, Attempts: "1", Who: coordinator})) {
		return nil
	}
	if !k.try(sprint.Deal(k.s, sprint.DealReq{Sel: sprint.Sel{IDs: []string{id}}, Who: sprint.MachineActor})) {
		return nil
	}
	for _, m := range k.members {
		k.try(sprint.FleetStep(k.s, sprint.FleetReq{Op: "hold", Member: m, Who: coordinator}))
	}
	if k.s.StateOf(id) != sprint.Ready {
		return nil
	}
	return []sample{k.sample()}
}

// aQueuedCardOnAFullFriend deals a friend of width one two cards and has her take one:
// her lane works, the other card waits behind it, and the members' lanes are idle beside
// it: the rebalance's case (sprint.Rebalance; the walks give no friend).
func aQueuedCardOnAFullFriend(k *walk) []sample {
	k.friends = []sprint.FriendSeat{{Name: "flo", Width: 1, Status: sprint.Up, Tiers: []string{cardhdr.RouteFlash, cardhdr.RoutePro}}}
	for range 2 {
		if k.addTo(k.streams[0]) == "" {
			return nil
		}
	}
	if !k.runPart("deal") {
		return nil
	}
	row := sprint.FriendRow("flo")
	if k.s.Fleet.Count(row, sprint.Ready) < 2 {
		return nil
	}
	wc := k.s.Fleet.Cell(row, sprint.Ready)[0]
	k.s.Friends = k.friends // her take reads the roster
	took := k.try(sprint.Take(k.s, sprint.TakeReq{As: row, Sel: sprint.Sel{IDs: []string{wc.ID}}, Gens: map[string]int{wc.ID: wc.Int("gen")}, Who: row}))
	k.s.Friends = nil
	if !took {
		return nil
	}
	return []sample{k.sample()}
}
