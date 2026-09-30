package verbs

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/machine"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/sprintfn"
)

// The coordinator's one wake a tick, end to end on the twin (errata 3
// amendment 8; the owner's rule, 2026-09-30: "one wake, end of turn, with all
// inbox ready to read and act on"; "and no wake, if nothing in the inbox"):
// the machine's loop ticks on the twin, every line its steps write is
// appended to the notes stream one at a time, as the store's XADDs wake a
// blocked XREAD, and a coordinator runs its loop, wait, read all, act on all,
// wait, counting its wakes.

// wakeRig is the machine's loop over a sprint on the twin, the notes stream
// that mirrors the twin's log, and the coordinator's loop in its own
// goroutine.
type wakeRig struct {
	t        *testing.T
	w        *jrWorld
	wt       *jrWaiter
	loop     *machine.Loop
	mirrored int
	wakes    chan InboxView
	mu       sync.Mutex
	woke     []InboxView
}

// newWakeRig is a started sprint whose machine runs the rules given, its log
// mirrored so far, and the coordinator blocked in inbox --wait from cursor 0.
func newWakeRig(t *testing.T, rules ...sprint.Rule) *wakeRig {
	t.Helper()
	return newWakeRigWith(t, true, rules...)
}

// newWakeRigWith is newWakeRig with the coordinator's loop run in its own
// goroutine only when run says so: with none, the test is the coordinator.
func newWakeRigWith(t *testing.T, run bool, rules ...sprint.Rule) *wakeRig {
	t.Helper()
	w := newJRWorld(t)
	w.start()
	// The start verb's notice, whose line queues the keys of deal and done
	// (2.1, "machine started").
	w.step(&sprintfn.Request{Meta: sprintfn.Meta{Verb: "start"}, Body: sprintfn.Body{Notes: []sprintfn.NoteReq{
		{Op: sprintfn.JOpKnow, Type: sprint.NMachineStarted, Subjects: []string{sprint.SprintSubject}}}}})
	l, err := machine.NewLoop(machine.Config{Names: jrNames, Owner: "token-a", Name: "a", Rules: rules})
	if err != nil {
		t.Fatal(err)
	}
	r := &wakeRig{t: t, w: w, wt: newJRWaiter(), loop: l, wakes: make(chan InboxView, 64)}
	for r.mirror() {
	}
	if !run {
		return r
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.coordinator(ctx)
	<-r.wt.blocked
	return r
}

// coordinator is the coordinator's loop: wait from its cursor; on a wake, send
// the view; then wait again from the later of the tick-end and what it read.
func (r *wakeRig) coordinator(ctx context.Context) {
	cursor := uint64(0)
	for ctx.Err() == nil {
		var v InboxView
		if _, err := Inbox(ctx, r.w.env, InboxReq{After: cursor, Wait: r.wt.wait(time.Hour), Out: &v}); err != nil {
			return
		}
		if !v.Woke {
			continue
		}
		r.mu.Lock()
		r.woke = append(r.woke, v)
		r.mu.Unlock()
		r.wakes <- v
		cursor, _ = strconv.ParseUint(v.Last, 10, 64)
	}
}

// mirror appends the twin's next line to the notes stream, as the store's
// XADD of it, and says whether there was one.
func (r *wakeRig) mirror() bool {
	r.t.Helper()
	lines := r.w.log.Stored(jrPrefix, "0")
	if r.mirrored >= len(lines) {
		return false
	}
	l := lines[r.mirrored]
	r.mirrored++
	if err := r.wt.ms.Append(logKey(jrPrefix, 0), string(l.Seq)+"-0", map[string]string{"n": l.N, "d": l.D}); err != nil {
		r.t.Fatal(err)
	}
	return true
}

// tick runs one tick of the machine, then appends its lines to the stream one
// at a time, each read by the blocked coordinator before the next (the
// coordinator blocks again after each: on a wake, after sending it).
func (r *wakeRig) tick() machine.Report {
	r.t.Helper()
	r.w.tick(time.Second)
	rep, err := machine.Tick(context.Background(), r.w.tw, r.loop)
	if err != nil {
		r.t.Fatalf("tick: %v", err)
	}
	for r.mirror() {
		<-r.wt.blocked
	}
	return rep
}

// count is the wakes so far.
func (r *wakeRig) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.woke)
}

// last is the last wake.
func (r *wakeRig) last() InboxView {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.woke[len(r.woke)-1]
}

// headOfFresh is a read of the head of fresh:s1, one card: the rules below
// read nothing they plan on, and a rule's read names one query at least.
func headOfFresh([]sprint.AgendaKey, sprint.ReadBounds, int) (sprint.ReadPlan, []sprint.AgendaKey) {
	return sprint.ReadPlan{Sprint: []sprint.SprintQ{{Kind: sprint.QueryRelated, Table: sprint.Work, Fields: []string{"kind"},
		Source: sprint.IDSource{Kind: sprint.SourceHead, Key: sprint.IndexFresh + ":s1", Limit: 1}}}}, nil
}

// raiseRule is a rule of the machine's (by the real rule's name, so the line
// "machine started" queues its key) that requeues its key, so it plans every
// tick, and opens *n judgments of distinct causes each time, with a notice
// beside them; the count is read when it plans.
func raiseRule(name string, n *int) sprint.Rule {
	p, ok := sprint.PriorityOf(name)
	if !ok {
		panic("no priority for " + name)
	}
	calls := 0
	return sprint.Rule{Name: name, Priority: p,
		Read: headOfFresh,
		Plan: func(s *sprint.Snapshot, keys []sprint.AgendaKey, now sprint.Now) sprint.RulePlan {
			calls++
			rp := sprint.RulePlan{Requeue: keys}
			for i := 0; i < *n; i++ {
				rp.Notes = append(rp.Notes, sprint.NoteReq{Op: "open", Type: typeStepRefused, Cause: fmt.Sprintf("C%d-%d", calls, i),
					Subjects: []string{fmt.Sprintf("x%d", i)}, Text: "raised by the test"})
			}
			if *n > 0 {
				rp.Notes = append(rp.Notes, sprint.NoteReq{Op: "know", Type: sprint.NMemberUp, Subjects: []string{fmt.Sprintf("m%d", calls)}, Text: "up"})
			}
			return rp
		}}
}

// doneRule is R15's key raising "the sprint is done" once, as the event-driven
// path's R15 does (rules_position.go planDone), with nothing else.
func doneRule() sprint.Rule {
	p, _ := sprint.PriorityOf("done")
	raised := false
	return sprint.Rule{Name: "done", Priority: p,
		Read: headOfFresh,
		Plan: func(s *sprint.Snapshot, keys []sprint.AgendaKey, now sprint.Now) sprint.RulePlan {
			rp := sprint.RulePlan{Done: keys}
			if !raised {
				raised = true
				rp.Notes = append(rp.Notes, sprint.NoteReq{Op: "open", Type: sprint.NSprintDone, Cause: sprint.SprintDoneCause,
					Subjects: []string{sprint.SprintSubject}, Text: "the sprint is done: 3 landed, 0 dropped"})
			}
			return rp
		}}
}

// judgmentNotes are the ids of the notes of the inbox's judgment groups of a
// type.
func judgmentNotes(v InboxView, typ string) map[string]bool {
	out := map[string]bool{}
	for _, g := range v.Groups {
		if g.Kind == sprint.Judgment && g.Type == typ {
			for _, id := range g.Notes {
				out[id] = true
			}
		}
	}
	return out
}

// noticeCount is the notices of a type the inbox lists.
func noticeCount(v InboxView, typ string) int {
	n := 0
	for _, g := range v.Groups {
		if g.Kind == sprint.Happened && g.Type == typ {
			n += len(g.Notes)
		}
	}
	return n
}

// TestInboxWaitWakesOnceATick: three judgments in one tick wake the
// coordinator ONCE, as the tick's last step commits, however many lines the
// tick writes before it; a tick that addresses it nothing does not wake it;
// two ticks with judgments wake it twice. The wake's Cursor lets the following
// inbox read every item of the batch: the three judgments and the notice
// beside them, which an inbox from the tick-end's seq no longer lists.
func TestInboxWaitWakesOnceATick(t *testing.T) {
	t.Parallel()
	n := 3
	r := newWakeRig(t, raiseRule("deal", &n))
	r.tick() // the lease, and the cursor learned
	before := r.mirrored
	rep := r.tick()
	if rep.Wake != 3 || r.mirrored-before < 5 {
		t.Fatalf("the tick of three judgments: %+v, %d lines", rep, r.mirrored-before)
	}
	if got := r.count(); got != 1 {
		t.Fatalf("three judgments in one tick woke the coordinator %d times, want once", got)
	}
	v := r.last()
	if v.Judgments != 3 || v.Cursor != "0" {
		t.Fatalf("the wake: %+v", v)
	}
	var all InboxView
	if _, err := Inbox(context.Background(), r.w.env, InboxReq{After: 0, Out: &all}); err != nil {
		t.Fatal(err)
	}
	if got := judgmentNotes(all, typeStepRefused); len(got) != 3 || noticeCount(all, sprint.NMemberUp) != 1 {
		t.Fatalf("the inbox from the wake's cursor lists %v and %d notices, want the batch: 3 judgments and 1 notice; %+v",
			got, noticeCount(all, sprint.NMemberUp), all.Groups)
	}
	last, _ := strconv.ParseUint(v.Last, 10, 64)
	var after InboxView
	if _, err := Inbox(context.Background(), r.w.env, InboxReq{After: last, Out: &after}); err != nil {
		t.Fatal(err)
	}
	if noticeCount(after, sprint.NMemberUp) != 0 {
		t.Fatalf("an inbox from the tick-end lists the batch's notice again: %+v", after.Groups)
	}

	n = 0
	if rep := r.tick(); rep.Wake != 0 || r.count() != 1 {
		t.Fatalf("a tick with no judgment: %+v, %d wakes; want no wake", rep, r.count())
	}
	n = 2
	r.tick()
	n = 1
	r.tick()
	if got := r.count(); got != 3 || r.last().Judgments != 1 {
		t.Fatalf("two more ticks with judgments: %d wakes in all, the last %+v; want 3", got, r.last())
	}
	n = 0
	if r.tick(); r.count() != 3 {
		t.Fatalf("a quiet tick after them woke the coordinator: %d wakes", r.count())
	}
}

// TestInboxWaitWakesOnTheDoneTick: the tick that raises "the sprint is done"
// wakes the coordinator, because that tick's count counts it, and the inbox
// the wake points to shows it.
func TestInboxWaitWakesOnTheDoneTick(t *testing.T) {
	t.Parallel()
	r := newWakeRig(t, doneRule())
	r.tick()
	rep := r.tick()
	if rep.Wake != 1 || r.count() != 1 || r.last().Judgments != 1 {
		t.Fatalf("the done tick: %+v, %d wakes", rep, r.count())
	}
	var v InboxView
	if _, err := Inbox(context.Background(), r.w.env, InboxReq{After: 0, Out: &v}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, g := range v.Groups {
		found = found || g.Type == sprint.NSprintDone
	}
	if !found {
		t.Fatalf("the inbox after the done tick's wake does not show it: %+v", v.Groups)
	}
	if r.tick(); r.count() != 1 {
		t.Fatalf("the tick after the done tick woke the coordinator: %d wakes", r.count())
	}
}
