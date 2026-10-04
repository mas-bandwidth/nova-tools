package refmodel_test

import (
	"fmt"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
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
		for _, run := range []func(*walk) []sample{stoppedOnALandedCard, unevenQueues, aLateCardRedealt} {
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
		k.try(sprint.Ask(k.s, sprint.AskReq{Sel: sprint.Sel{IDs: []string{id}}, Who: sprint.MachineActor}))
		for _, rc := range k.s.Readers.Of(id) {
			k.try(sprint.Read(k.s, sprint.ReadReq{As: rc.Row, Verdict: "ok", Finding: "f", Sel: sprint.Sel{IDs: []string{rc.ID}}, Who: rc.Row}))
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
