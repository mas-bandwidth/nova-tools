package store

// A property test of random sprints, against the in-memory store with the
// clock and the randomness injected: seeded, no sleeps. Each run is a list of
// abstract actions (setup included, so a shrink can take primaries out), each
// resolved against the state when it runs, so any sublist is a run too. After
// every action: every rule of check holds (rule 10 aside while an operation
// is pending, and with rule 12, the no-stall rule, in it); no score changed
// but by rank; every working primary has exactly one live work card; after a
// tick, every part of the tick called on the state has nothing to do and a
// second tick changes nothing. At the end, with the actors and the
// coordinator fair, every primary is landed or off the table with its
// outcome recorded. A failing run is shrunk to the shortest list of actions
// that fails the same way and printed as the verbs it ran.

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/hostload"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// propConfig is one random sprint's shape.
type propConfig struct {
	seed    uint64
	streams []string
	members []string
	readers []string
}

// pAct is one abstract action: its kind and picks, resolved modulo what the
// state offers when it runs (a pick of nothing does nothing).
type pAct struct {
	K       string
	A, B, C int
	N       []int         // needs: picks among the primaries admitted before
	F       bool          // failed, broken, a suspect named, in line
	D       time.Duration // a clock step
	Cut     string        // a store call that fails throughout the action
}

// propFail is what a run found: the action at which, its kind and detail.
type propFail struct {
	Step   int
	Kind   string
	Detail string
}

func (f *propFail) String() string {
	return fmt.Sprintf("action %d: %s: %s", f.Step, f.Kind, f.Detail)
}

// Sig is the failure's class, for the shrink: its kind and the first words of
// its detail, with every card id left out.
func (f *propFail) Sig() string {
	d := f.Detail
	switch f.Kind {
	case "invariant":
		if i := strings.Index(d, ":"); i > 0 {
			d = d[:i]
		}
	case "stall", "unraised":
		d = stallWhy(d)
	}
	var words []string
	for _, w := range strings.Fields(d) {
		if strings.ContainsAny(w, "0123456789") {
			continue
		}
		words = append(words, w)
		if len(words) == 6 {
			break
		}
	}
	return f.Kind + ": " + strings.Join(words, " ")
}

// stallWhy is a stall's why: what follows its last colon.
func stallWhy(d string) string {
	if i := strings.LastIndex(d, ": "); i >= 0 {
		return d[i+2:]
	}
	return d
}

// knownStalls are the stalls the design is known to leave, by their why, each
// with the finding that names it: met on a primary the coordinator
// acknowledged a judgment on, the run goes on past one, and the rule's own
// interrupt (a stall judgment, written by the next tick) must hold it.
var knownStalls = map[string]string{}

const findingF3 = "FINDING-F3: the overdue decision act prints ack for every judgment; an ack of the one judgment that holds a primary (ready to accept, stranded in review, reads exhausted, sentinel reached) leaves it with nothing open"

var propCutPoints = []string{"release", "apply p-work before", "apply p-merge before", "apply p-readers before", "apply p-fleet before"}

// genProp is the random sprint of a seed: 2 to 4 streams, 5 to 40
// primaries with needs within and across streams (each on one admitted
// before it: never a cycle), 0 to 3 sentinels per stream, 2 to 4 members, 2
// to 4 readers, and a run of random actions with actors silent for stretches.
func genProp(seed uint64) (propConfig, []pAct) {
	rng := rand.New(rand.NewPCG(seed, 0x5eed))
	cfg := propConfig{seed: seed}
	for i := range 2 + rng.IntN(3) {
		cfg.streams = append(cfg.streams, fmt.Sprintf("s%d", i+1))
	}
	for i := range 2 + rng.IntN(3) {
		cfg.members = append(cfg.members, fmt.Sprintf("m%d", i+1))
	}
	for i := range 2 + rng.IntN(3) {
		cfg.readers = append(cfg.readers, "reader-"+string(rune('a'+i)))
	}
	var acts []pAct
	for i := range cfg.members {
		acts = append(acts, pAct{K: "up", A: i})
	}
	np, added := 5+rng.IntN(36), 0
	sentinels := map[int]int{}
	for added < np {
		st := rng.IntN(len(cfg.streams))
		if rng.IntN(8) == 0 && sentinels[st] < 2 {
			sentinels[st]++
			acts = append(acts, pAct{K: "sentinel", A: st})
			continue
		}
		var needs []int
		if added > 0 {
			for k := rng.IntN(4) - 1; k > 0; k-- {
				needs = append(needs, rng.IntN(added))
			}
		}
		acts = append(acts, pAct{K: "add", A: st, N: needs})
		added++
	}
	acts = append(acts, pAct{K: "start"})
	// Who is silent, until which action.
	silent := map[string]int{}
	quiet := func(who string, i int) bool { return i < silent[who] }
	type w struct {
		k string
		n int
	}
	weights := []w{{"tick", 20}, {"clock", 8}, {"take", 10}, {"finish", 10}, {"begin", 6}, {"report", 10}, {"merge", 8},
		{"answer", 14}, {"down", 2}, {"up", 3}, {"stop", 2}, {"start", 3}, {"tickstop", 4}, {"add", 2}, {"sentinel", 1}}
	total := 0
	for _, x := range weights {
		total += x.n
	}
	steps := 150 + rng.IntN(250)
	for i := 0; len(acts) < steps+np+10 && i < 10*steps; i++ {
		if rng.IntN(40) == 0 {
			who := []string{"worker", "reader", "merger", "coord"}[rng.IntN(4)]
			silent[who] = i + 10 + rng.IntN(60)
		}
		x := rng.IntN(total)
		k := ""
		for _, c := range weights {
			if x < c.n {
				k = c.k
				break
			}
			x -= c.n
		}
		switch {
		case (k == "take" || k == "finish") && quiet("worker", i),
			(k == "begin" || k == "report") && quiet("reader", i),
			k == "merge" && quiet("merger", i),
			k == "answer" && quiet("coord", i):
			continue
		}
		a := pAct{K: k, A: rng.IntN(64), B: rng.IntN(64), C: rng.IntN(10)}
		switch k {
		case "finish":
			a.F = rng.IntN(5) == 0
		case "report":
			a.F = rng.IntN(7) == 0
		case "merge":
			a.F = rng.IntN(2) == 0
		case "add":
			a.F = rng.IntN(3) == 0
			for n := rng.IntN(3); n > 0; n-- {
				a.N = append(a.N, rng.IntN(64))
			}
		case "clock":
			a.D = time.Duration(1+rng.IntN(10)) * time.Second
			if rng.IntN(5) == 0 {
				a.D = time.Duration(1+rng.IntN(40)) * time.Minute
			}
		}
		if rng.IntN(33) == 0 && k != "clock" {
			a.Cut = propCutPoints[rng.IntN(len(propCutPoints))]
		}
		acts = append(acts, a)
	}
	return cfg, acts
}

// propBackend stops the machine at the fence of a chosen part of a tick: the
// stop lands between the parts of the tick.
type propBackend struct {
	*Mem
	r *propRun
}

func (b *propBackend) Acquire(ctx context.Context, gen uint64, op OpRecord) (bool, error) {
	if b.r.stopAfter > 0 && strings.HasPrefix(op.Verb, "tick ") {
		b.r.stopAfter--
	}
	if p := strings.TrimPrefix(op.Verb, "tick "); b.r.stopAfter == 0 && p != op.Verb {
		b.r.stopAfter = -1
		other := &Store{B: b.Mem, Names: b.r.st.Names, Actor: "coord", Now: b.r.st.Now, NewID: b.r.st.NewID, Sleep: b.r.st.Sleep, Rand: b.r.st.Rand}
		if _, _, _, err := other.SetMachine(ctx, false); err != nil {
			return false, err
		}
		b.r.log = append(b.r.log, "  (stop, as the tick's part "+p+" takes the fence: no later part begins)")
		b.r.stats["a stop between the parts of a tick"]++
	}
	return b.Mem.Acquire(ctx, gen, op)
}

// propRun is one run: the store, the clock, and what the checks remember.
type propRun struct {
	cfg       propConfig
	m         *Mem
	st        *Store
	ctx       context.Context
	now       time.Time
	ids       []string           // every primary admitted, in order
	tried     map[string]bool    // every id an add named: a cut add may land at a later repair
	score     map[string]float64 // each primary's score, as admitted or ranked
	ranked    map[string]bool    // the primaries a rank since the last quiet check ranks
	log       []string           // the verbs run
	cut       string             // the store call failing now
	stopAfter int                // the parts of the tick that take the fence before the machine stops (-1 none)
	rng       *rand.Rand         // the fair coordinator's choices
	known     map[string]int     // the known findings met, by name
	stats     map[string]int     // what the run did, for the log
	acked     map[string]bool    // the subjects the coordinator acknowledged a judgment on
	seq       int
	errs      []error
	tickRes   *TickResult
	cutUsed   bool
}

var errCut = errors.New("cut by the test")

func newPropRun(cfg propConfig) (*propRun, error) {
	r := &propRun{stopAfter: -1, cfg: cfg, m: NewMem(), ctx: context.Background(), now: t0, score: map[string]float64{}, tried: map[string]bool{}, known: map[string]int{}, stats: map[string]int{}, acked: map[string]bool{}, ranked: map[string]bool{},
		rng: rand.New(rand.NewPCG(cfg.seed, 0xfa11))}
	n := 0
	r.st = &Store{B: &propBackend{Mem: r.m, r: r}, Names: sprint.Names{Prefix: "p-"}, Actor: "coord",
		Now:   func() time.Time { return r.now },
		NewID: func() string { n++; return fmt.Sprint(n) },
		Sleep: func(time.Duration) {}, Rand: func(int64) int64 { return 0 }}
	r.m.Fail = func(point string) error {
		if r.cut != "" && point == r.cut {
			r.cutUsed = true
			return errCut
		}
		return nil
	}
	if err := r.st.Init(r.ctx); err != nil {
		return nil, err
	}
	if err := r.m.RowsAdd(r.ctx, "p-readers", cfg.readers); err != nil {
		return nil, err
	}
	return r, r.m.SetCoordinator(r.ctx, "coord")
}

func (r *propRun) say(format string, args ...any) {
	r.log = append(r.log, fmt.Sprintf(format, args...))
}

// run is one step of a verb, its error kept for the checks.
func (r *propRun) run(line string, s Step) Result {
	r.say("%s", line)
	res, err := r.st.Run(r.ctx, s)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s: %w", line, err))
	}
	switch {
	case err != nil:
		r.stats["verb errors (a cut, or the fence it left)"]++
	case len(res.Moved) > 0:
		r.stats["moved: "+s.Verb]++
	case len(res.Refused) > 0:
		r.stats["refused: "+s.Verb]++
	}
	return res
}

func (r *propRun) snap() *sprint.Snapshot {
	s, err := r.st.Load(r.ctx, All, tickExtras)
	if err != nil {
		r.errs = append(r.errs, err)
		return nil
	}
	return s
}

func (r *propRun) newID(prefix string) string {
	r.seq++
	return fmt.Sprintf("%s%d", prefix, r.seq)
}

func pickOf[T any](xs []T, k int) (T, bool) {
	var zero T
	if len(xs) == 0 {
		return zero, false
	}
	return xs[k%len(xs)], true
}

// act runs one action.
func (r *propRun) act(a pAct) {
	r.errs, r.tickRes = nil, nil
	r.beat()
	r.cut, r.cutUsed = a.Cut, false
	defer func() { r.cut = "" }()
	if a.Cut != "" {
		r.say("  (the store fails at %q throughout the next action)", a.Cut)
	}
	cfg := r.cfg
	member, _ := pickOf(cfg.members, a.A)
	reader, _ := pickOf(cfg.readers, a.A)
	stream, _ := pickOf(cfg.streams, a.A)
	switch a.K {
	case "up", "down":
		op := map[string]string{"up": "release", "down": "hold"}[a.K] // the verbs: fleet down holds, fleet up releases
		r.run("nova-sprint fleet "+a.K+" "+member, FleetStep(sprint.FleetReq{Op: op, Member: member, Who: "coord", Fresh: true}))
	case "start", "stop":
		r.say("nova-sprint %s", a.K)
		if _, _, _, err := r.st.SetMachine(r.ctx, a.K == "start"); err != nil {
			r.errs = append(r.errs, err)
		}
	case "tick", "tickstop":
		if a.K == "tickstop" {
			r.stopAfter = 1 + a.A%3
		}
		r.tick()
		r.stopAfter = -1
	case "clock":
		r.now = r.now.Add(a.D)
		r.beat() // the machines beat on while the time passes
		r.say("  (the clock moves %s)", a.D)
	case "add":
		r.add(stream, a.N, a.F)
	case "sentinel":
		r.sentinel(stream, a.B)
	case "take":
		r.run(fmt.Sprintf("nova-sprint take --as %s --limit %d", member, a.B%3+1), TakeStep(sprint.TakeReq{As: member, Sel: sprint.Sel{Limit: a.B%3 + 1}, Who: member}))
	case "finish":
		s := r.snap()
		if s == nil {
			return
		}
		if c, ok := pickOf(s.Fleet.Cell(member, sprint.Working), a.B); ok {
			line := fmt.Sprintf("nova-sprint finish %s@%s --as %s", c.ID, c.F("gen"), member)
			if a.F {
				line += " --failed --report boom"
			}
			r.run(line, FinishStep(sprint.FinishReq{As: member, Sel: sprint.Sel{IDs: []string{c.ID}}, Gens: map[string]int{c.ID: c.Int("gen")}, Failed: a.F, Report: "boom", Who: member}))
		}
	case "begin":
		s := r.snap()
		if s == nil {
			return
		}
		if c, ok := pickOf(s.Readers.Cell(reader, sprint.Asked), a.B); ok {
			r.run(fmt.Sprintf("nova-sprint read %s --as %s --begin", c.ID, reader), ReadStep(sprint.ReadReq{As: reader, Begin: true, Sel: sprint.Sel{IDs: []string{c.ID}}, Who: reader}))
		}
	case "report":
		s := r.snap()
		if s == nil {
			return
		}
		cs := append(append([]*sprint.Card{}, s.Readers.Cell(reader, sprint.Asked)...), s.Readers.Cell(reader, sprint.Reading)...)
		if c, ok := pickOf(cs, a.B); ok {
			v := "ok"
			if a.F {
				v = "broken"
			}
			r.run(fmt.Sprintf("nova-sprint read %s --as %s --verdict %s --finding f", c.ID, reader, v), ReadStep(sprint.ReadReq{As: reader, Verdict: v, Finding: "f", Sel: sprint.Sel{IDs: []string{c.ID}}, Who: reader}))
		}
	case "merge":
		r.merge(stream, a.B, a.C, a.F)
	case "answer":
		r.answer(a.A, a.B, false)
	default:
		panic("no action " + a.K)
	}
}

// beat is one beat of every member's machine: they beat while time passes
// (status follows the beat, and fleet down is the coordinator's hold).
func (r *propRun) beat() {
	zero := 0.0
	for _, m := range r.cfg.members {
		if _, err := r.st.Beat(r.ctx, m, &zero, hostload.Source{}); err != nil {
			r.errs = append(r.errs, err)
		}
	}
}

func (r *propRun) tick() {
	r.beat()
	r.say("nova-sprint tick")
	res, err := r.st.Tick(r.ctx)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("tick: %w", err))
		return
	}
	r.tickRes = &res
}

// add admits one primary into the stream with needs picked among those
// admitted, in line before a reached sentinel of the stream when inLine.
func (r *propRun) add(stream string, picks []int, inLine bool) {
	id := r.newID("p")
	req := sprint.AddReq{Stream: stream, IDs: []string{id}, Who: "coord"}
	for _, k := range picks {
		if n, ok := pickOf(r.ids, k); ok && !contains(req.Needs, n) {
			req.Needs = append(req.Needs, n)
		}
	}
	line := "nova-sprint add --stream " + stream + " " + id
	if len(req.Needs) > 0 {
		line += " --needs " + strings.Join(req.Needs, ",")
	}
	if inLine {
		if s := r.snap(); s != nil {
			for _, c := range s.Work.Cell(stream, sprint.Waiting) {
				if sprint.IsSentinel(c) && c.F("reached") != "" {
					req.Before = c.ID
					line += " --before " + c.ID
					break
				}
			}
		}
	}
	r.run(line, AddStep(req))
	r.admitted(id)
}

// sentinel admits a sentinel at the end of the stream (at 0) or in line
// before a card of the stream not landed; a stream holds at most three.
func (r *propRun) sentinel(stream string, at int) {
	s := r.snap()
	if s == nil {
		return
	}
	var line []*sprint.Card
	n := 0
	for _, id := range r.ids {
		if c := s.Work.Card(id); c != nil && c.Row == stream && sprint.IsSentinel(c) {
			n++
		}
	}
	if n >= 3 {
		return
	}
	for _, col := range []string{sprint.Waiting, sprint.Ready, sprint.Working, sprint.Review, sprint.Merging} {
		line = append(line, s.Work.Cell(stream, col)...)
	}
	sprint.SortCards(line)
	id := r.newID("stop")
	req := sprint.AddReq{Stream: stream, IDs: []string{id}, Sentinel: true, Who: "coord"}
	cmd := "nova-sprint add --stream " + stream + " --sentinel " + id
	if c, ok := pickOf(line, at); ok && at%3 != 0 {
		req.Before = c.ID
		cmd += " --before " + c.ID
	}
	r.run(cmd, AddStep(req))
	r.admitted(id)
}

func (r *propRun) admitted(id string) {
	r.tried[id] = true
	if s := r.snap(); s != nil {
		if c := s.Work.Card(id); c != nil {
			r.ids = append(r.ids, id)
			r.score[id] = c.Score
		}
	}
}

// merge is the merger's step for the stream, with a fact: mostly green, else
// a conflict, a red branch (a suspect named or not), a rejected batch, or a
// card that needs a card of another stream.
func (r *propRun) merge(stream string, b, fact int, suspect bool) {
	s := r.snap()
	if s == nil {
		return
	}
	q := s.Merge.Cell(stream, sprint.Queued)
	if len(q) == 0 {
		return
	}
	req := sprint.MergeReq{Stream: stream, Batch: b%3 + 1, Who: "merger"}
	line := fmt.Sprintf("nova-sprint merge --stream %s --batch %d", stream, req.Batch)
	switch fact {
	case 6:
		req.Conflict = q[0].ID
		line += " --conflict " + q[0].ID
	case 7:
		req.Red = true
		line += " --red"
		if suspect {
			req.Suspects = []string{q[0].ID}
			line += " --suspect " + q[0].ID
		}
	case 8:
		req.Rejected = true
		line += " --rejected"
	case 9:
		var others []*sprint.Card
		for _, st := range r.cfg.streams {
			if st == stream {
				continue
			}
			for _, col := range []string{sprint.Waiting, sprint.Ready, sprint.Working, sprint.Review, sprint.Merging} {
				others = append(others, s.Work.Cell(st, col)...)
			}
		}
		if o, ok := pickOf(others, b); ok {
			req.Cross = q[0].ID + "=" + o.ID
			line += " --cross " + req.Cross
		}
	}
	r.run(line, MergeStep(req))
}

// answer is the coordinator answering the judgment group at pick with the
// decision at choice, as its printed commands do. A fair coordinator
// (progress) chooses among the decisions that act.
func (r *propRun) answer(pick, choice int, progress bool) {
	v, err := r.st.Inbox(r.ctx, sprint.DeadlineJudgment, 30*time.Minute, 10000)
	if err != nil {
		r.errs = append(r.errs, err)
		return
	}
	var groups []sprint.Group
	for _, g := range v.Groups {
		if g.Kind == sprint.Judgment {
			groups = append(groups, g)
		}
	}
	g, ok := pickOf(groups, pick)
	if !ok {
		return
	}
	r.decide(v, g, choice, progress)
}

// idle is a decision that changes nothing, or adds work: a fair coordinator
// at the end of a run chooses another when it has one.
var idle = map[string]bool{"stop and look": true, "look at the card": true, "look at both": true, "check": true, "wait": true,
	"act": true, "clear": true, "add": true, "do more before going on": true, "look": true, "rank that card first": true}

func (r *propRun) decide(v InboxView, g sprint.Group, choice int, progress bool) {
	ds := append([]string(nil), g.Decisions...)
	if g.Type == sprint.NRed {
		ds = append(ds, "resume with what you did")
	}
	if progress && g.Type == sprint.NSprintDone {
		return // a fair coordinator at the end adds no more work
	}
	if progress && g.Type == sprint.NStreamStale {
		// A fair coordinator looks at a stream that does not move: check
		// names every stall in it (rule 12), and it decides on each, even one
		// it acknowledged before.
		r.say("nova-sprint check")
		rep, s, err := r.st.Check(r.ctx, 1)
		if err != nil || rep.Pending != "" {
			return
		}
		hs, err := r.st.heldState(r.ctx, s, nil)
		if err != nil {
			return
		}
		for _, f := range sprint.Unheld(hs, s.Now) {
			if f.Root == "" && f.Stream == g.Stream && !strings.HasPrefix(f.Subject, "stream:") {
				r.decide(v, sprint.Group{ID: "check", Kind: sprint.Judgment, Type: sprint.NStalled, Stream: f.Stream,
					Members: []string{f.Subject}, Decisions: f.Decisions}, choice, true)
			}
		}
		return
	}
	if progress {
		var act []string
		for _, d := range ds {
			if !idle[d] && !(d == "ack" && g.Type == sprint.NStalled) {
				act = append(act, d)
			}
		}
		if len(act) > 0 {
			ds = act
		}
	}
	d, ok := pickOf(ds, choice)
	if !ok {
		return
	}
	if d == "drop" && len(ds) > 1 && (choice/8)%4 != 0 {
		d, _ = pickOf(ds, choice+1) // drop one time in four it comes up
	}
	var first sprint.Note
	for _, o := range v.Open {
		if o.Note.ID == g.ID {
			first = o.Note
		}
	}
	card, other := first.Card, first.Other
	members, notes, st := g.Members, g.Notes, g.Stream
	r.say("  (the coordinator answers %s, %s on %s, with: %s)", g.ID, g.Type, strings.Join(members, ","), d)
	resume := func(did string) {
		r.run("nova-sprint resume --stream "+st+" --did '"+did+"' --answers "+strings.Join(notes, ","), ResumeStep(sprint.ResumeReq{Stream: st, Did: did, Answers: notes, Who: "coord"}))
	}
	ret := func(ids []string, answers []string) {
		r.run("nova-sprint return "+strings.Join(ids, " ")+" --reason r --answers "+strings.Join(answers, ","), ReturnStep(sprint.ReturnReq{Sel: sprint.Sel{IDs: ids}, Reason: "r", Answers: answers, Who: "coord"}))
	}
	rework := func(ids []string, fix string, answers []string) {
		line := "nova-sprint rework " + strings.Join(ids, " ")
		if fix != "" {
			line += " --fix '" + fix + "'"
		}
		r.run(line+" --answers "+strings.Join(answers, ","), ReworkStep(sprint.ReworkReq{Sel: sprint.Sel{IDs: ids}, Fix: fix, Answers: answers, Who: "coord"}))
	}
	drop := func(ids []string, answers []string) {
		r.run("nova-sprint drop "+strings.Join(ids, " ")+" --reason why --answers "+strings.Join(answers, ","), DropStep(sprint.DropReq{Sel: sprint.Sel{IDs: ids}, Reason: "why", Answers: answers, Who: "coord"}))
	}
	ack := func() {
		for _, m := range members {
			r.acked[m] = true
		}
		r.run("nova-sprint ack "+strings.Join(notes, ",")+" --reason none", AckStep(sprint.AckReq{Notes: notes, Reason: "none", Who: "coord"}))
	}
	wait := func(id string) {
		r.say("nova-sprint wait %s --for 30m", id)
		if _, _, err := r.st.Wait(r.ctx, id, r.now.Add(30*time.Minute)); err != nil {
			r.errs = append(r.errs, err)
		}
	}
	suspects := g.Suspects
	if len(suspects) == 0 && len(members) > 0 {
		suspects = []string{members[choice%len(members)]}
	}
	switch {
	case idle[d] && d != "wait" && d != "act" && d != "add" && d != "do more before going on" && d != "rank that card first":
		r.say("  (looked; nothing done)")
	case d == "act":
		if first.StreamLevel || choice%2 == 0 {
			wait(g.ID)
		} else {
			ack()
		}
	case d == "wait":
		wait(g.ID)
	case d == "add":
		stream, _ := pickOf(r.cfg.streams, choice)
		r.add(stream, nil, false)
	case g.Type == sprint.NSentinelReached && d == "do more before going on":
		id := r.newID("p")
		r.run("nova-sprint add --stream "+st+" --before "+card+" "+id, AddStep(sprint.AddReq{Stream: st, IDs: []string{id}, Before: card, Who: "coord"}))
		r.admitted(id)
	case d == "release":
		r.run("nova-sprint release "+strings.Join(members, " ")+" --reason seen --answers "+strings.Join(notes, ","),
			ReleaseStep(sprint.ReleaseReq{IDs: members, Reason: "seen", Coordinator: "coord", Answers: notes, Who: "coord"}))
	case g.Type == sprint.NConflict && d == "resolve and resume", g.Type == sprint.NRejected && d == "resume", d == "resume with what you did", d == "resume":
		resume("did it")
	case g.Type == sprint.NConflict && d == "rework":
		ret([]string{card}, nil)
		rework([]string{card}, "fix", nil)
		resume("returned " + card + " for rework")
	case (g.Type == sprint.NConflict || g.Type == sprint.NCross) && d == "drop":
		drop([]string{card}, notes)
		resume("dropped " + card)
	case g.Type == sprint.NCross && d == "return":
		ret([]string{card}, notes)
		resume("returned " + card)
	case g.Type == sprint.NRed && d == "take the suspect off and resume":
		ret(suspects, notes)
		resume("returned " + strings.Join(suspects, " "))
	case g.Type == sprint.NRed && d == "rework the suspect":
		ret(suspects, notes)
		rework(suspects, "fix", nil)
		resume("returned " + strings.Join(suspects, " ") + " for rework")
	case g.Type == sprint.NCross && d == "rank that card first":
		r.ranked[other] = true
		r.run("nova-sprint rank "+other+" --first --answers "+strings.Join(notes, ","), RankStep(sprint.RankReq{IDs: []string{other}, First: true, Answers: notes, Who: "coord"}))
	case g.Type == sprint.NRejected && d == "return":
		ret(members, notes)
		resume("returned the batch")
	case g.Type == sprint.NRejected && d == "drop":
		drop(members, notes)
		resume("dropped the batch")
	case d == "rework with the finding" || d == "rework with a fix" && g.Type == sprint.NWorkFailed:
		rework(members, "", notes)
	case d == "rework with a fix" || d == "rework":
		rework(members, "fix", notes)
	case d == "ask":
		r.run("nova-sprint ask "+strings.Join(members, " ")+" --answers "+strings.Join(notes, ","), AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: members}, Answers: notes, Who: "coord"}))
	case d == "ask another reader" || d == "ask --another":
		r.run("nova-sprint ask "+strings.Join(members, " ")+" --another --answers "+strings.Join(notes, ","), AskStep(sprint.AskReq{Sel: sprint.Sel{IDs: members}, Another: true, Answers: notes, Who: "coord"}))
	case d == "accept":
		r.run("nova-sprint accept "+strings.Join(members, " ")+" --answers "+strings.Join(notes, ","), AcceptStep(sprint.AcceptReq{Sel: sprint.Sel{Only: members}, Answers: notes, Who: "coord"}))
	case d == "drop":
		drop(members, notes)
	case d == "return":
		ret(members, notes)
	case d == "look" || d == "ack":
		ack()
	case d == "repair":
		r.say("nova-sprint repair")
		if _, err := r.st.Repair(r.ctx); err != nil {
			r.errs = append(r.errs, err)
		}
	case d == "reader add":
		name := "reader-" + string(rune('a'+len(r.cfg.readers)))
		r.say("nova-sprint reader add %s", name)
		if err := r.m.RowsAdd(r.ctx, "p-readers", []string{name}); err != nil {
			r.errs = append(r.errs, err)
		}
		r.cfg.readers = append(r.cfg.readers, name)
	case d == "fleet up" || d == "fleet beat":
		s := r.snap()
		for _, m := range r.cfg.members {
			if s != nil && s.MemberCtl(m).F("status") != sprint.Up {
				r.run("nova-sprint fleet up "+m, FleetStep(sprint.FleetReq{Op: "release", Member: m, Who: "coord", Fresh: true}))
				break
			}
		}
	case strings.HasPrefix(d, "fleet down "):
		m := strings.TrimPrefix(d, "fleet down ")
		r.run("nova-sprint "+d, FleetStep(sprint.FleetReq{Op: "hold", Member: m, Who: "coord"}))
	case strings.HasPrefix(d, "merge --stream "):
		r.merge(strings.TrimPrefix(d, "merge --stream "), 2, 0, false)
	default:
		r.errs = append(r.errs, fmt.Errorf("the test has no command for the decision %q of %s", d, g.Type))
	}
}

// busy says an error is the sprint telling the caller an operation is
// pending or the fence kept moving: what a cut leaves until repair.
func busy(err error) bool {
	var pe *PendingError
	var ce *CutError
	return errors.As(err, &pe) || errors.As(err, &ce) || errors.Is(err, errLost) || errors.Is(err, ErrUnknown) ||
		strings.Contains(err.Error(), "the sprint is busy") || strings.Contains(err.Error(), "run: nova-sprint repair")
}

// check is every property after an action.
func (r *propRun) check(i int, a pAct) *propFail {
	fail := func(kind, format string, args ...any) *propFail {
		return &propFail{Step: i, Kind: kind, Detail: fmt.Sprintf(format, args...)}
	}
	rep, s, err := r.st.Check(r.ctx, 1)
	if err != nil {
		return fail("error", "check: %v", err)
	}
	pending := rep.Pending != ""
	if pending {
		r.stats["actions ending with an operation pending"]++
	}
	for _, e := range r.errs {
		if !(r.cutUsed || pending || busy(e)) {
			return fail("error", "%v", e)
		}
	}
	for _, v := range rep.Violations {
		switch {
		case v.Rule == 10 && pending:
		case v.Rule == 12 && !pending:
			// judged below, by roots
		case v.Rule == 12:
			// An operation pending past its grace that the last tick could
			// not finish because the store failed it (the test's cut): the
			// machine line says so, and the stuck judgment follows the repair.
			if _, hb, err := r.st.Machine(r.ctx); err == nil && hb.Error != "" {
				continue
			}
			return fail("stall", "%s", v.Detail)
		default:
			return fail("invariant", "%s", v)
		}
	}
	if !pending {
		hs, err := r.st.heldState(r.ctx, s, nil)
		if err != nil {
			return fail("error", "held: %v", err)
		}
		for _, f := range sprint.Unheld(hs, s.Now) {
			switch {
			case f.Root != "": // told by its root's
			case knownStalls[f.Why] != "" && r.acked[f.Subject]:
				r.known[knownStalls[f.Why]]++
			default:
				return fail("stall", "%s", f)
			}
		}
	}
	// Scores change only by rank.
	for _, c := range s.Work.Column(sprint.States...) {
		want, ok := r.score[c.ID]
		if !ok && r.tried[c.ID] { // a cut add, finished by a later repair
			r.ids, r.score[c.ID], want, ok = append(r.ids, c.ID), c.Score, c.Score, true
		}
		switch {
		case !ok:
			return fail("score", "%s is on the table and was never admitted", c.ID)
		case c.Score != want && r.ranked[c.ID]:
			r.score[c.ID] = c.Score
		case c.Score != want:
			return fail("score", "%s has score %v, admitted at %v, and nothing ranked it", c.ID, c.Score, want)
		}
	}
	if !pending {
		// A rank cut short lands at a later repair: its primaries may change
		// score until no operation is pending.
		r.ranked = map[string]bool{}
		for _, c := range s.Work.Column(sprint.Working) {
			var live []string
			for _, fc := range s.Fleet.Of(c.ID) {
				if fc.Col == sprint.Ready || fc.Col == sprint.Working {
					live = append(live, fc.ID)
				}
			}
			if len(live) != 1 || live[0] != c.F("work") {
				return fail("live", "%s is working with live work cards %v, naming %s", c.ID, live, c.F("work"))
			}
		}
	}
	res := r.tickRes
	if res == nil || res.Halted != "" || res.Stale != "" || res.State != Running || pending {
		return nil
	}
	// After a tick that ran its parts: each part on the state has nothing to
	// do, and a second tick changes nothing.
	if !res.Idle {
		m, _, err := r.st.Machine(r.ctx)
		if err != nil {
			return fail("error", "machine: %v", err)
		}
		fs, _, err := r.st.Fenced(r.ctx, All, tickExtras, nil)
		if err != nil {
			return fail("error", "read: %v", err)
		}
		req := sprint.TickReq{Who: sprint.MachineActor, Stopped: m.StoppedBetween}
		for _, part := range sprint.TickParts {
			if p, due := part.Fn(fs, req); !p.Empty() || due > 0 {
				var what []string
				for _, u := range p.Units {
					what = append(what, u.Moved)
				}
				for _, n := range p.Notes {
					what = append(what, n.Kind+" "+n.Type+" "+strings.Join(n.Primaries, ",")+" "+n.What)
				}
				for _, o := range p.Closes {
					what = append(what, "closes "+o.Key+" "+o.Note.Type)
				}
				return fail("parts", "after a tick the part %s still has to do: %s", part.Name, strings.Join(what, "; "))
			}
		}
	}
	// The rule's own interrupt: after a tick, every stall a chain ends at has
	// its stall judgment, open or acknowledged.
	hs, err := r.st.heldState(r.ctx, s, nil)
	if err != nil {
		return fail("error", "held: %v", err)
	}
	raised := map[string]bool{}
	for _, o := range append(append([]sprint.Open{}, s.Open...), s.Acked...) {
		if o.Note.Type == sprint.NStalled {
			raised[o.Subject()] = true
		}
	}
	for _, f := range sprint.Unheld(hs, s.Now) {
		if !res.Idle && f.Root == "" && !raised[f.Subject] {
			return fail("unraised", "after a tick no stall judgment holds %s: %s", f.Subject, f)
		}
	}
	before := r.image()
	res2, err := r.st.Tick(r.ctx)
	if err != nil {
		return fail("error", "the second tick: %v", err)
	}
	if after := r.image(); after != before {
		return fail("second-tick", "a second tick changed the sprint: moved %v, %d notes", res2.Moved(), res2.Notes())
	}
	return nil
}

// image is the tables' revisions, the notifications and the open set.
func (r *propRun) image() string {
	var b strings.Builder
	for _, t := range All {
		fmt.Fprintf(&b, "%d ", r.m.Revision(r.st.Names.Table(t)))
	}
	notes, _, _ := r.m.NotesSince(r.ctx, "", 1<<30)
	open, _ := r.m.OpenNotes(r.ctx)
	var keys []string
	for _, o := range open {
		keys = append(keys, o.Key)
	}
	sort.Strings(keys)
	fmt.Fprintf(&b, "%d %s", len(notes), strings.Join(keys, " "))
	return b.String()
}

// done says every primary admitted is landed, or off the table with its
// outcome and reason recorded.
func (r *propRun) done() (bool, string) {
	s, err := r.st.Load(r.ctx, All, func(*sprint.Snapshot) map[string][]string { return map[string][]string{sprint.Work: r.ids} })
	if err != nil {
		return false, err.Error()
	}
	for _, id := range r.ids {
		c := s.Work.Card(id)
		switch {
		case c == nil:
			return false, id + " has no record"
		case c.Placed() && c.Col == sprint.Landed:
		case !c.Placed() && c.F("outcome") == "dropped" && c.F("reason") != "":
		case !c.Placed():
			return false, id + " is off the table with no outcome"
		default:
			return false, id + " is " + c.Col
		}
	}
	return true, ""
}

// drain is the end of a run with every actor fair: members up, the machine
// running, workers and readers at every card, the merger at every stream,
// the coordinator answering every judgment by a decision that acts, their
// faults fading. It fails when the sprint does not finish.
func (r *propRun) drain(i0 int) *propFail {
	r.say("-- from here every actor is fair")
	step := i0
	do := func(a pAct) *propFail {
		step++
		r.act(a)
		return r.check(step, a)
	}
	for k := range r.cfg.members {
		if f := do(pAct{K: "up", A: k}); f != nil {
			return f
		}
	}
	if f := do(pAct{K: "start"}); f != nil {
		return f
	}
	for round := 0; round < 400; round++ {
		fault := func(n int) bool { return round < 60 && r.rng.IntN(n+round) == 0 }
		if f := do(pAct{K: "tick"}); f != nil {
			return f
		}
		for k := range r.cfg.members {
			if f := do(pAct{K: "take", A: k, B: 2}); f != nil {
				return f
			}
			for n := 0; n < 3; n++ {
				if f := do(pAct{K: "finish", A: k, F: fault(8)}); f != nil {
					return f
				}
			}
		}
		for k := range r.cfg.readers {
			for n := 0; n < 4; n++ {
				if f := do(pAct{K: "report", A: k, F: fault(10)}); f != nil {
					return f
				}
			}
		}
		v, err := r.st.Inbox(r.ctx, sprint.DeadlineJudgment, 30*time.Minute, 10000)
		if err != nil {
			return &propFail{Step: step, Kind: "error", Detail: err.Error()}
		}
		n := 0
		for _, g := range v.Groups {
			if g.Kind == sprint.Judgment {
				n++
			}
		}
		for g := 0; g < n; g++ {
			step++
			r.errs, r.tickRes = nil, nil
			r.answer(g, r.rng.IntN(64), true)
			if f := r.check(step, pAct{K: "answer"}); f != nil {
				return f
			}
		}
		for k := range r.cfg.streams {
			c := 0
			if fault(12) {
				c = 6 + r.rng.IntN(4)
			}
			if f := do(pAct{K: "merge", A: k, B: 2, C: c, F: r.rng.IntN(2) == 0}); f != nil {
				return f
			}
		}
		d := time.Second
		if round%10 == 9 {
			d = 2 * time.Minute // past the grace of anything a cut left
		}
		if f := do(pAct{K: "clock", D: d}); f != nil {
			return f
		}
		if ok, _ := r.done(); ok {
			r.stats["runs finished"]++
			r.stats["primaries landed or dropped at the end"] += len(r.ids)
			notes, _, _ := r.m.NotesSince(r.ctx, "", 1<<30)
			for _, n := range notes {
				if n.Kind == sprint.Judgment {
					r.stats["judgment: "+n.Type]++
				}
			}
			return nil
		}
	}
	_, why := r.done()
	var held []string
	for _, id := range r.ids {
		if hd, err := r.st.Held(r.ctx, id); err == nil && hd.By != sprint.HeldDone {
			held = append(held, hd.String())
		}
	}
	sort.Strings(held)
	return &propFail{Step: step, Kind: "end", Detail: fmt.Sprintf("not finished after the fair rounds: %s; held: %s", why, strings.Join(held, " | "))}
}

// runProp runs the actions and then the fair end; the first failure, and the
// verbs run.
func runProp(cfg propConfig, acts []pAct) (*propFail, []string, map[string]int) {
	f, log, known, _ := runPropStats(cfg, acts)
	return f, log, known
}

func runPropStats(cfg propConfig, acts []pAct) (*propFail, []string, map[string]int, map[string]int) {
	r, err := newPropRun(cfg)
	if err != nil {
		return &propFail{Kind: "error", Detail: err.Error()}, nil, nil, nil
	}
	for i, a := range acts {
		r.act(a)
		if f := r.check(i, a); f != nil {
			return f, r.log, r.known, r.stats
		}
	}
	r.stats["actions"] += len(acts)
	return r.drain(len(acts)), r.log, r.known, r.stats
}

// shrinkProp is the shortest list of actions it finds that fails with the
// same class: whole chunks out first, then one action at a time.
func shrinkProp(cfg propConfig, acts []pAct, sig string, budget int) []pAct {
	fails := func(xs []pAct) bool {
		if budget <= 0 {
			return false
		}
		budget--
		f, _, _ := runProp(cfg, xs)
		return f != nil && f.Sig() == sig
	}
	for n := len(acts) / 2; n >= 1; n /= 2 {
		for i := 0; i+n <= len(acts); {
			try := append(append([]pAct{}, acts[:i]...), acts[i+n:]...)
			if fails(try) {
				acts = try
				continue
			}
			i += n
		}
	}
	return acts
}

// propSeeds runs the seeds and reports each failure class once, shrunk.
func propSeeds(t *testing.T, from, to uint64, shrinkBudget int) {
	t.Helper()
	classes := map[string]int{}
	first := map[string]string{}
	known, stats := map[string]int{}, map[string]int{}
	for seed := from; seed < to; seed++ {
		cfg, acts := genProp(seed)
		f, _, k, st := runPropStats(cfg, acts)
		for name := range k {
			known[name]++
		}
		for name, n := range st {
			stats[name] += n
		}
		if f == nil {
			continue
		}
		sig := f.Sig()
		classes[sig]++
		if _, seen := first[sig]; seen {
			continue
		}
		small := shrinkProp(cfg, acts, sig, shrinkBudget)
		f2, log, _ := runProp(cfg, small)
		if f2 == nil {
			f2, log = f, nil
		}
		first[sig] = fmt.Sprintf("seed %d (streams %v, members %v, readers %v), %d actions shrunk to %d: %s\n    %s",
			seed, cfg.streams, cfg.members, cfg.readers, len(acts), len(small), f2, strings.Join(log, "\n    "))
	}
	var sigs []string
	for s := range classes {
		sigs = append(sigs, s)
	}
	sort.Strings(sigs)
	t.Logf("%d seeds, %d failure classes", to-from, len(sigs))
	for name, n := range known {
		t.Logf("%d seeds met the known %s (the stall judgment held it each time)", n, name)
	}
	var names []string
	for name := range stats {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Logf("  %s: %d", name, stats[name])
	}
	for _, s := range sigs {
		t.Errorf("%d seeds fail with %s; the first:\n%s", classes[s], s, first[s])
	}
}

func TestRandomSprintsNeverStallAndAlwaysFinish(t *testing.T) {
	t.Parallel()
	propSeeds(t, 1, 2, 300) // one seed here; the slow tier runs 2,000 (prop_slow_test.go)
}
