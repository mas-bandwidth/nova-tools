package main

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// serve is the heartbeat server (docs/SPEC-HOOK.md, the heartbeat;
// tla/HookBeat.tla): one pass now, then one every --every until a signal. A
// fault is a line and the loop goes on: the server never stops on one.
func (w world) serve(c *tool.Call) *tool.Out {
	s, refused := w.openSession(c, true)
	if refused != nil {
		return refused
	}
	defer s.close()
	every := c.Dur("every")
	if every < time.Second {
		return tool.Refuse(fmt.Sprintf("--every %s is under a second; it wants the interval between passes, such as %s", every, hook.BeatEvery))
	}
	within := c.Dur("judge-within")
	// the server's own gate, with the evidence it exists to give set aside: no
	// server row, or a stale one, refuses every name, and a server must still be able to start
	ctx, cancel := call()
	st, _, err := s.hooks.Read(ctx, s.as)
	cancel()
	if err == nil {
		st[0].Down = ""
		if r := st[0].Gate(hook.Want{Name: s.as, Need: []hook.Push{hook.Bus}, Roles: []hook.Role{hook.Coordinator, hook.Comms}, Role: hook.Comms}); r != nil {
			err = r
		}
	}
	if err != nil {
		o := tool.Fail("the heartbeat server must itself be hooked in, as coordinator or comms: " + err.Error())
		o.Remedy = hook.HookCommand(s.as, hook.Comms)
		return o
	}
	b := &bus.Bus{Store: s.bus}
	if c.Bool("once") {
		w.pass(c, s, b, every, within)
		return tool.Done().Fact("as", s.as).Fact("every", every.String())
	}
	sig, stop := w.signals(context.Background())
	defer stop()
	for {
		w.pass(c, s, b, every, within)
		select {
		case <-sig.Done():
			return tool.Done().Fact("as", s.as).Fact("stopped", true)
		case <-w.after(every):
		}
	}
}

// serveLine prints one line of the server as it goes: SERVE STATE event= and
// its fields, the text (when any) as its note.
func serveLine(c *tool.Call, event, text string, kv ...any) {
	o := tool.Done().As("STATE").Fact("event", event)
	for i := 0; i+1 < len(kv); i += 2 {
		o.Fact(fmt.Sprint(kv[i]), kv[i+1])
	}
	if text != "" {
		o.Note(text)
	}
	o.Verb = "serve"
	o.Render(c.Stdout, false)
}

// pass is one heartbeat pass and its deliveries: the challenges to each name,
// what changed told once (tell), the open judgments looked after (judgments)
// and the asks' expiry. Every call is bounded by its own deadline (call); each
// fault is said and passed over.
func (w world) pass(c *tool.Call, s *session, b *bus.Bus, every, within time.Duration) {
	ctx, cancel := call()
	names, err := s.hooks.Names(ctx)
	cancel()
	if err != nil {
		fault(c, "names", err)
		return
	}
	ctx, cancel = call()
	p, err := s.hooks.Pass(ctx, names, every, hook.MissLimit, w.nonce, w.holder)
	cancel()
	if err != nil {
		fault(c, "pass", err)
		return
	}
	if !p.Taken {
		serveLine(c, "skip", "another server took this interval's pass")
		return
	}
	for _, bt := range p.Beats {
		if bt.Did != hook.BeatChallenged {
			continue
		}
		// the nonce goes only to the name's own stream: never on this server's lines
		if err := w.send(b, bus.Message{From: s.as, To: []string{bt.Name}, Subject: "HOOK CHALLENGE nonce=" + bt.Nonce, Body: notice.ChallengeText(bt.Name, bt.Nonce, every)}); err != nil {
			fault(c, "challenge "+bt.Name, err)
			continue
		}
		serveLine(c, "challenge", "", "name", bt.Name, "misses", bt.Misses)
	}
	w.tell(c, s, b, p, within)
	w.judgments(c, s, b)
	ctx, cancel = call()
	expired, err := b.ExpireAsks(ctx)
	cancel()
	for _, e := range expired {
		serveLine(c, "expired", "", "ask", e.Ask.ID, "from", e.Ask.From)
	}
	if err != nil {
		fault(c, "asks", err)
	}
}

// fault is one store or bus fault, said and passed over.
func fault(c *tool.Call, what string, err error) {
	serveLine(c, "fault", err.Error()+"; the next pass tries again", "at", what)
}

// tell says what each audience has not yet been told, in at most one
// message each (tla/HookBeat.tla, Tell): the coordinator one judgment naming
// every friend down or back; every other live name that did not itself change
// one message with all of it and the coordinator, a judgment when the
// coordinator itself went down (who takes over). Each message and its told
// mark are one atomic write under the audience's once-key (hook.Passed.Telling),
// so a crash or a failed send leaves it untold for the next pass, and a send
// that went is never repeated (NeverToldTwice, EveryTransitionToldAtLeastOnce).
func (w world) tell(c *tool.Call, s *session, b *bus.Bus, p hook.Passed, within time.Duration) {
	if !p.Changed(hook.ToCoordinator) && !p.Changed(hook.ToRest) {
		return
	}
	ctx, cancel := call()
	sts, _, err := s.hooks.Read(ctx, p.Unhooked...)
	cancel()
	if err != nil {
		fault(c, "reasons", err)
		return
	}
	why := map[string]string{}
	for _, st := range sts {
		why[st.Name] = cmp.Or(st.Reason, "unhooked")
	}
	lines := func(down, back []string) []string {
		var out []string
		for _, n := range down {
			out = append(out, "FRIEND DOWN "+n+": "+why[n])
		}
		for _, n := range back {
			out = append(out, "FRIEND BACK "+n)
		}
		return out
	}
	nobody := func(audience string) { // no one to tell: the mark alone, under the generation read
		ctx, cancel := call()
		defer cancel()
		if err := s.hooks.Told(ctx, p, audience); err != nil {
			fault(c, "told "+audience, err)
		}
	}
	if down, back, _ := p.Diff(hook.ToCoordinator); p.Changed(hook.ToCoordinator) {
		ls := lines(down, back)
		if p.Coord == "" || len(ls) == 0 {
			nobody(hook.ToCoordinator)
		} else {
			w.judge(c, s, b, p, hook.ToCoordinator, []string{p.Coord}, strings.Join(ls, "; "), "Answer with nova-bus send --re <this message's id> and one of: wait | reassign <name>'s work | tell the owner.", within)
		}
	}
	if !p.Changed(hook.ToRest) {
		return
	}
	down, back, was := p.Diff(hook.ToRest)
	ls := lines(down, back)
	if p.Coord != was && p.Coord != "" {
		ls = append(ls, "COORDINATOR IS "+p.Coord)
	}
	var rest []string // the live names this pass did not change: the ones who need telling
	for _, n := range p.Live {
		if n != p.Coord && !slices.Contains(back, n) {
			rest = append(rest, n)
		}
	}
	if len(rest) == 0 || len(ls) == 0 {
		nobody(hook.ToRest)
		return
	}
	subject := strings.Join(ls, "; ")
	if was != "" && p.Coord == "" && slices.Contains(down, was) {
		w.judge(c, s, b, p, hook.ToRest, rest, "COORDINATOR DOWN "+was+": who takes over? "+subject, "Answer with nova-bus send --re <this message's id> and one of: I take over (nova-hook hook --as <me> --role coordinator) | wait | tell the owner.", within)
		return
	}
	once, marks := telling(p, hook.ToRest)
	if _, ok := w.sendOnce(c, s, b, bus.Message{From: s.as, To: rest, Subject: subject, Body: subject + "\nnova-hook friends shows every name's state.\n", Once: once, Marks: marks}); ok {
		serveLine(c, "told", subject, "to", strings.Join(rest, ","))
	}
}

// telling is an audience's once-key and its told marks as bus marks on the server row.
func telling(p hook.Passed, audience string) (string, []bus.Mark) {
	once, tm := p.Telling(audience)
	marks := make([]bus.Mark, len(tm))
	for i, m := range tm {
		marks[i] = bus.Mark{Key: hook.ServeKey, Field: m.Field, Value: m.Value}
	}
	return once, marks
}

// sendOnce sends m under its once-key: the message (zero when an earlier
// send under the key already went) and whether it is sent. A send whose
// answer was lost is sent if its once-key stands.
func (w world) sendOnce(c *tool.Call, s *session, b *bus.Bus, m bus.Message) (bus.Message, bool) {
	sent, err := w.deliver(b, m)
	if err == nil {
		return sent, true
	}
	ctx, cancel := call()
	went, serr := s.hooks.Sent(ctx, m.Once)
	cancel()
	if serr == nil && went {
		return bus.Message{}, true
	}
	fault(c, "send "+m.Once, err)
	return bus.Message{}, false
}

// judge raises one judgment for an audience: a bus message of kind judgment,
// sent with its told mark in one write, then open in hook:judgments until a
// recipient answers it.
func (w world) judge(c *tool.Call, s *session, b *bus.Bus, p hook.Passed, audience string, to []string, subject, answers string, within time.Duration) {
	once, marks := telling(p, audience)
	m, ok := w.sendOnce(c, s, b, bus.Message{From: s.as, To: to, Kind: bus.KindJudgment, Subject: subject, Body: subject + "\n" + answers + "\n", Once: once, Marks: marks})
	if !ok || m.ID == "" {
		return // not sent, or sent by an earlier try
	}
	ctx, cancel := call()
	err := s.hooks.OpenJudgment(ctx, hook.Judgment{ID: m.ID, To: to, Subject: subject}, within)
	cancel()
	if err != nil {
		fault(c, "judgment record", err)
		return
	}
	serveLine(c, "judgment", subject, "id", m.ID, "to", strings.Join(to, ","))
}

// judgments looks after the open judgments: an answered one is closed, and
// one past its due is pushed again as OVERDUE, at most once.
func (w world) judgments(c *tool.Call, s *session, b *bus.Bus) {
	ctx, cancel := call()
	js, now, err := s.hooks.Judgments(ctx)
	cancel()
	if err != nil {
		fault(c, "judgments", err)
		return
	}
	for _, j := range js {
		ctx, cancel := call()
		answered, err := s.hooks.Answered(ctx, j)
		cancel()
		switch {
		case err != nil:
			fault(c, "answers", err)
		case answered:
			ctx, cancel := call()
			err := s.hooks.Close(ctx, j)
			cancel()
			if err != nil {
				fault(c, "close", err)
				continue
			}
			serveLine(c, "answered", j.Subject, "id", j.ID)
		case !j.Overdue && !now.Before(j.Due):
			// no --re on the re-push: a reply is the bus's answers record, and the server may be the coordinator
			// itself; the re-push and its overdue mark are one write under the judgment's overdue once-key
			subject := "OVERDUE " + j.Subject
			once, field, value, err := j.OverdueMark()
			if err != nil {
				fault(c, "overdue record", err)
				continue
			}
			if _, ok := w.sendOnce(c, s, b, bus.Message{From: s.as, To: j.To, Kind: bus.KindJudgment, Subject: subject, Body: subject + "\nStill open: answer the judgment " + j.ID + " with nova-bus send --re " + j.ID + ".\n",
				Once: once, Marks: []bus.Mark{{Key: hook.JudgmentsKey, Field: field, Value: value}}}); !ok {
				continue
			}
			serveLine(c, "overdue", j.Subject, "id", j.ID)
		}
	}
}

// send is one bus message under its own deadline.
func (w world) send(b *bus.Bus, m bus.Message) error {
	_, err := w.deliver(b, m)
	return err
}
