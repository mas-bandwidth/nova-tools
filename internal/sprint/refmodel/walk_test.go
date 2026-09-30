package refmodel_test

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
)

// The random snapshots of the property tests are the states of random sprints:
// a walk of a seed adds primaries and sentinels, brings members up and holds
// them down, lets the workers, the readers, the merger and the coordinator act,
// runs the tick, and moves the clock, every step by the steps of the core, so
// each state is one the machine could reach, with two outside changes no step
// makes (walkActions). A snapshot is taken every few steps of a walk, with
// beats, goals and unknown machines drawn at random beside its tables.

// The shape of the walks: how many steps one takes, how many apart its
// snapshots are, how many actions a step tries before it gives up (an action
// with nothing to act on is not a step), and the sizes it draws its sprint
// from.
const (
	walkSteps         = 160
	walkEvery         = 4
	walkMaxPrimaries  = 14
	walkTries         = 8
	walkMaxStreams    = 3
	walkMaxMembers    = 3
	walkMaxNeeds      = 2
	walkSentinelOneIn = 14
	walkBeatAge       = 5 // seconds
	walkMaxGoals      = 2
	walkGoalHistory   = 12 * time.Minute
)

// walk is one random sprint, mid-way.
type walk struct {
	*world
	rng     *rand.Rand
	streams []string
	members []string
	now     time.Time
	next    int // ids given to primaries so far
	beats   map[string]sprint.Beat
	silent  map[string]bool // members whose machines do not beat
	running bool
	since   time.Time
	spans   []sprint.Span
}

// newWalk is the sprint of a seed: two or three streams, two or three members
// up, two or three readers, the machine RUNNING since t0.
func newWalk(seed uint64) *walk {
	rng := rand.New(rand.NewPCG(seed, 0x5eed))
	readers := []string{"reader-a", "reader-b", "reader-c"}[:2+rng.IntN(2)]
	k := &walk{world: newWorld(readers...), rng: rng, now: t0, beats: map[string]sprint.Beat{}, silent: map[string]bool{}, running: true, since: t0}
	for i := range walkMaxStreams - 1 + rng.IntN(2) {
		k.streams = append(k.streams, fmt.Sprintf("s%d", i+1))
	}
	for i := range walkMaxMembers - 1 + rng.IntN(2) {
		k.members = append(k.members, fmt.Sprintf("m%d", i+1))
	}
	for _, m := range k.members {
		k.try(sprint.FleetStep(k.s, sprint.FleetReq{Op: "release", Member: m, Who: coordinator, Fresh: true}))
	}
	k.beatAll()
	return k
}

// try applies a plan as the store would apply it, held to the lifecycle and to
// one judgment per cause, all or nothing: a plan whose guards break on the
// state it was built on is left out whole. It says whether the plan changed
// anything.
func (k *walk) try(p sprint.Plan) bool {
	k.s.Now = k.now
	p = sprint.Applied(k.s, p)
	if p.Empty() {
		return false
	}
	next := &world{s: refmodel.Snapshot{Tables: k.s}.Clone().Tables, seq: k.seq}
	if err := next.apply(p); err != nil {
		return false
	}
	k.s, k.seq = next.s, next.seq
	return true
}

func (k *walk) pick(n int) int { return k.rng.IntN(max(n, 1)) }

func (k *walk) stream() string { return k.streams[k.pick(len(k.streams))] }

func (k *walk) member() string { return k.members[k.pick(len(k.members))] }

// primaryIn is a primary of the states, at random; false when there is none.
func (k *walk) primaryIn(states ...sprint.State) (*sprint.Card, bool) {
	cs := k.s.Work.Column(states...)
	if len(cs) == 0 {
		return nil, false
	}
	return cs[k.pick(len(cs))], true
}

// walkActions are the actions of a walk and how often each is drawn, out of
// their weights' sum: the actors of a sprint, the machine's tick (a whole tick
// or one part of it, which leaves the state a tick cut short leaves), the
// clock, the beats, and the outside changes a sprint suffers that no step
// makes: a card landed by a path that moves nothing after it, the card a
// stopped stream needed landed, a card lost, and a member's queue taken empty.
// An action says whether it did anything; one that had nothing to act on is
// not a step, and the walk draws another. A snapshot is always taken right
// after an action that is marked to be, since the tick would undo what it did
// before the next snapshot of the walk.
var walkActions = []struct {
	weight  int
	do      func(*walk) bool
	capture bool
}{
	{10, (*walk).add, false},
	{3, (*walk).fleet, false},
	{10, (*walk).take, false},
	{10, (*walk).finish, false},
	{10, (*walk).read, false},
	{6, (*walk).acceptOrRework, false},
	{2, (*walk).drop, false},
	{9, (*walk).merge, false},
	{3, (*walk).resume, false},
	{4, (*walk).release, false},
	{2, (*walk).ack, false},
	{2, (*walk).wait, false},
	{3, (*walk).wholeTick, false},
	{5, (*walk).tick, false},
	{8, (*walk).clock, false},
	{4, (*walk).beat, false},
	{1, (*walk).toggle, false},
	{2, (*walk).land, true},
	{2, (*walk).landNeed, true},
	{1, (*walk).lose, true},
	{5, (*walk).unlevel, true},
}

// step is one random action of one of the actors. It says whether a snapshot is
// to be taken right after it.
func (k *walk) step() (capture bool) {
	k.beatAll()
	total := 0
	for _, a := range walkActions {
		total += a.weight
	}
	for range walkTries {
		x := k.pick(total)
		for _, a := range walkActions {
			if x < a.weight {
				k.s.Now = k.now
				if a.do(k) {
					return a.capture
				}
				break
			}
			x -= a.weight
		}
	}
	return false
}

// add admits a primary, or a sentinel, needing up to walkMaxNeeds of those
// already admitted.
func (k *walk) add() bool {
	if k.next >= walkMaxPrimaries {
		return false
	}
	id := fmt.Sprintf("p%d", k.next)
	k.next++
	var needs []string
	var all []string
	for id := range k.s.Work.Cards {
		all = append(all, id)
	}
	slices.Sort(all)
	for range k.pick(walkMaxNeeds + 1) {
		if len(all) > 0 {
			needs = append(needs, all[k.pick(len(all))])
		}
	}
	slices.Sort(needs)
	needs = slices.Compact(needs)
	sentinel := k.pick(walkSentinelOneIn) == 0
	if sentinel {
		needs = nil
	}
	return k.try(sprint.Add(k.s, sprint.AddReq{Stream: k.stream(), IDs: []string{id}, Needs: needs, Sentinel: sentinel, Who: coordinator}))
}

// fleet has the coordinator hold a member down or release it.
func (k *walk) fleet() bool {
	return k.try(sprint.FleetStep(k.s, sprint.FleetReq{Op: []string{"hold", "release", "release", "release"}[k.pick(4)], Member: k.member(), Who: coordinator, Fresh: k.pick(2) == 0}))
}

// take has a member take its oldest ready work cards.
func (k *walk) take() bool {
	m := k.member()
	return k.try(sprint.Take(k.s, sprint.TakeReq{As: m, Sel: sprint.Sel{Limit: 1 + k.pick(2)}, Who: m}))
}

// finish has a member finish a work card it holds, failed now and then.
func (k *walk) finish() bool {
	cs := k.s.Fleet.Column(sprint.Working)
	if len(cs) == 0 {
		return false
	}
	c := cs[k.pick(len(cs))]
	return k.try(sprint.Finish(k.s, sprint.FinishReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")},
		Failed: k.pick(6) == 0, Head: "h-" + c.F("primary"), Report: "boom", Who: c.Row}))
}

// read has a reader begin a read card or report on it, broken now and then.
func (k *walk) read() bool {
	cs := k.s.Readers.Column(sprint.Asked, sprint.Reading)
	if len(cs) == 0 {
		return false
	}
	c := cs[k.pick(len(cs))]
	r := sprint.ReadReq{As: c.Row, Sel: sprint.Sel{IDs: []string{c.ID}}, Who: c.Row}
	if k.pick(4) == 0 && c.Col == sprint.Asked {
		r.Begin = true
	} else {
		r.Verdict, r.Finding = "ok", "f"
		if k.pick(6) == 0 {
			r.Verdict = "broken"
		}
	}
	return k.try(sprint.Read(k.s, r))
}

// acceptOrRework has the coordinator accept the primaries of a stream that
// have two ok reads, or rework one primary in review.
func (k *walk) acceptOrRework() bool {
	c, ok := k.primaryIn(sprint.Review)
	if !ok {
		return false
	}
	if k.pick(5) == 0 {
		return k.try(sprint.Rework(k.s, sprint.ReworkReq{Sel: sprint.Sel{IDs: []string{c.ID}}, Fix: "fix", Who: coordinator}))
	}
	return k.try(sprint.Accept(k.s, sprint.AcceptReq{Sel: sprint.Sel{Stream: c.Row}, Who: coordinator}))
}

// drop has the coordinator drop a primary that has not landed.
func (k *walk) drop() bool {
	c, ok := k.primaryIn(sprint.Waiting, sprint.Ready, sprint.Working, sprint.Review)
	if !ok {
		return false
	}
	return k.try(sprint.Drop(k.s, sprint.DropReq{Sel: sprint.Sel{IDs: []string{c.ID}}, Reason: "not wanted", Who: coordinator}))
}

// merge is a merge step of a stream, with a fact that stops it now and then.
func (k *walk) merge() bool {
	st := k.stream()
	r := sprint.MergeReq{Stream: st, Batch: 1 + k.pick(3), Who: merger}
	queued := k.s.Merge.Cell(st, sprint.Queued)
	if len(queued) > 0 {
		head := queued[0].ID
		switch k.pick(10) {
		case 0:
			r.Conflict = head
		case 1:
			r.Red, r.Suspects = true, []string{head}
		case 2:
			r.Rejected = true
		case 3, 4, 5, 6:
			// a card of another stream that is on its way and has not landed
			for _, o := range k.s.Work.Column(sprint.Ready, sprint.Working, sprint.Review, sprint.Merging) {
				if o.Row != st {
					r.Cross = head + "=" + o.ID
					break
				}
			}
		}
	}
	return k.try(sprint.MergeStep(k.s, r))
}

// resume has the coordinator resume a stopped stream.
func (k *walk) resume() bool {
	for _, st := range k.streams {
		if k.s.StreamCtl(st).F("state") == sprint.StreamStopped {
			return k.try(sprint.Resume(k.s, sprint.ResumeReq{Stream: st, Did: "fixed it", Who: coordinator}))
		}
	}
	return false
}

// release has the coordinator release a sentinel the tick reached.
func (k *walk) release() bool {
	var reached []string
	for _, c := range k.s.Work.Column(sprint.Waiting) {
		if sprint.IsSentinel(c) && c.F("reached") != "" {
			reached = append(reached, c.ID)
		}
	}
	if len(reached) == 0 {
		return false
	}
	return k.try(sprint.Release(k.s, sprint.ReleaseReq{IDs: []string{reached[k.pick(len(reached))]}, Reason: "looked", Coordinator: coordinator, Who: coordinator}))
}

// openJudgment is a judgment open now, at random.
func (k *walk) openJudgment() (sprint.Open, bool) {
	if len(k.s.Open) == 0 {
		return sprint.Open{}, false
	}
	return k.s.Open[k.pick(len(k.s.Open))], true
}

// ack has the coordinator acknowledge a judgment.
func (k *walk) ack() bool {
	o, ok := k.openJudgment()
	return ok && k.try(sprint.Ack(k.s, sprint.AckReq{Notes: []string{o.Note.ID}, Reason: "seen", Who: coordinator}))
}

// wait has the coordinator hold a condition the tick keeps until a later time.
func (k *walk) wait() bool {
	o, ok := k.openJudgment()
	return ok && k.try(sprint.Wait(k.s, sprint.WaitReq{Note: o.Note.ID, Until: k.now.Add(time.Duration(1+k.pick(20)) * time.Minute), Who: coordinator}))
}

// tick runs one part of the tick, as the machine would, while it is RUNNING.
func (k *walk) tick() bool {
	if !k.running {
		return false
	}
	part := sprint.TickParts[k.pick(len(sprint.TickParts))]
	plan, _ := part.Fn(k.s, k.req())
	return k.try(plan)
}

// wholeTick runs every part of the tick in its order, each on the state the one
// before left, while the machine is RUNNING.
func (k *walk) wholeTick() bool {
	if !k.running {
		return false
	}
	did := false
	for _, part := range sprint.TickParts {
		k.s.Now = k.now
		plan, _ := part.Fn(k.s, k.req())
		did = k.try(plan) || did
	}
	return did
}

// req is what the tick is given beside the tables.
func (k *walk) req() sprint.TickReq {
	return sprint.TickReq{Who: sprint.MachineActor, Stopped: func(from, to time.Time) time.Duration { return sprint.StoppedBetween(k.spans, from, to) }, Beats: k.beats}
}

// clock moves the time on: seconds mostly, minutes now and then, and hours
// rarely, so the deadlines pass.
func (k *walk) clock() bool {
	switch x := k.pick(20); {
	case x == 0:
		k.now = k.now.Add(time.Duration(1+k.pick(3)) * time.Hour)
	case x < 6:
		k.now = k.now.Add(time.Duration(1+k.pick(20)) * time.Minute)
	default:
		k.now = k.now.Add(time.Duration(1+k.pick(30)) * time.Second)
	}
	return true
}

// beatAll is the machines of the members beating, as they do every few
// seconds: each that is not silent has beaten within the last few seconds.
func (k *walk) beatAll() {
	for _, m := range k.members {
		if !k.silent[m] {
			k.beats[m] = sprint.Beat{At: k.now.Add(-time.Duration(k.pick(walkBeatAge)) * time.Second)}
		}
	}
}

// beat has a member's machine fall silent, or beat again: a silent one is down
// once its last beat is past.
func (k *walk) beat() bool {
	m := k.member()
	k.silent[m] = !k.silent[m]
	return true
}

// toggle stops the machine or starts it again, keeping the spans.
func (k *walk) toggle() bool {
	if k.running {
		k.running = false
		k.spans = append(k.spans, sprint.Span{From: k.now})
		return true
	}
	k.running, k.since = true, k.now
	k.spans[len(k.spans)-1].To = k.now
	return true
}

// land puts a merging primary in landed without the step that lands it, as a
// repair or an outside hand would: what waited on it is left for the tick.
func (k *walk) land() bool {
	c, ok := k.primaryIn(sprint.Merging)
	if !ok {
		return false
	}
	c.Col = sprint.Landed
	c.Rev++
	k.s.Work.Put(c)
	return true
}

// landNeed lands the card a stopped stream needed, as an outside hand would:
// the stream is left for the tick to resume.
func (k *walk) landNeed() bool {
	for _, st := range k.streams {
		ctl := k.s.StreamCtl(st)
		if ctl.F("state") != sprint.StreamStopped || ctl.F("cause") != "cross" {
			continue
		}
		for _, stuck := range k.s.Merge.Cell(st, sprint.Stuck) {
			if c := k.s.Work.Placed(stuck.F("need_card")); c != nil && c.Col != sprint.Landed {
				c.Col = sprint.Landed
				c.Rev++
				k.s.Work.Put(c)
				return true
			}
		}
	}
	return false
}

// unlevel has the member with the longest ready queue take all of it, which
// leaves the queues uneven.
func (k *walk) unlevel() bool {
	most, longest := "", 0
	for _, m := range k.members {
		if n := k.s.Fleet.Count(m, sprint.Ready); n > longest {
			most, longest = m, n
		}
	}
	return longest > 0 && k.try(sprint.Take(k.s, sprint.TakeReq{As: most, Sel: sprint.Sel{Limit: longest}, Who: most}))
}

// lose loses the live work card of a working primary, as an outside hand
// would: a rule of the check is broken.
func (k *walk) lose() bool {
	c, ok := k.primaryIn(sprint.Working)
	if !ok {
		return false
	}
	wc := k.s.Fleet.Placed(c.F("work"))
	if wc == nil {
		return false
	}
	wc.Row, wc.Col = "", ""
	wc.Rev++
	k.s.Fleet.Put(wc)
	return true
}

// sample is a snapshot of the walk with the time a duty decides at: the tables
// as they stand, and beats, goals and unknown machines beside them.
type sample struct {
	snap refmodel.Snapshot
	now  time.Time
}

func (k *walk) sample() sample {
	k.s.Now = time.Time{} // the tables' own clock is not read
	snap := refmodel.Snapshot{Tables: k.s, Running: k.running, Since: k.since, Stopped: k.spans, Beats: k.beats, Goals: k.goals(), Untold: k.untold()}
	return sample{snap: snap.Clone(), now: k.now}
}

// goals is up to walkMaxGoals people: pushed a while ago or never, their
// routes failing now and then, and the judgments written for those routes
// present or not.
func (k *walk) goals() sprint.Goals {
	var g sprint.Goals
	for i := range k.pick(walkMaxGoals + 1) {
		p := sprint.Goal{Name: fmt.Sprintf("person%d", i), Text: "keep going", Route: fmt.Sprintf("file:/routes/person%d", i), Count: k.pick(5)}
		if k.pick(3) != 0 {
			p.Last = k.now.Add(-time.Duration(k.pick(int(walkGoalHistory/time.Second))) * time.Second)
		}
		switch k.pick(6) {
		case 0:
			p.Pending = true
		case 1:
			p.Fail, p.Tried = "no route", k.now.Add(-time.Minute)
		}
		g.People = append(g.People, p)
	}
	if k.pick(2) == 0 {
		g.Noted = g.Failing()
		if len(g.Noted) == 0 {
			g.Noted = nil
		}
	}
	return g
}

// untold is the unknown machines that beat and were not told of: some are no
// member, one may be.
func (k *walk) untold() []string {
	var out []string
	for _, name := range []string{"x2", "x1", k.members[0]} {
		if k.pick(4) == 0 {
			out = append(out, name)
		}
	}
	return out
}

// samples is n snapshots: those of the scenarios, and then those of the walks of
// the seeds from 1 on, taken walkEvery steps apart and after an action marked to
// be.
func samples(n int) []sample {
	out := scenarios()
	for seed := uint64(1); len(out) < n; seed++ {
		k := newWalk(seed)
		for i := 1; i <= walkSteps && len(out) < n; i++ {
			if k.step() || i%walkEvery == 0 {
				out = append(out, k.sample())
			}
		}
	}
	return out
}
