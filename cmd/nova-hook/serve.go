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
	// the server's own gate, with the staleness it exists to end set aside: a
	// stale server row refuses every name, and a server must still be able to start
	ctx, cancel := call()
	st, _, err := s.hooks.Read(ctx, s.as)
	cancel()
	if err == nil {
		st[0].Stale = false
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
		if err := w.send(b, bus.Message{From: s.as, To: []string{bt.Name}, Subject: "HOOK CHALLENGE nonce=" + bt.Nonce, Body: challengeText(bt.Name, bt.Nonce, every)}); err != nil {
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
// coordinator itself went down (who takes over). An audience is marked told
// only once its message went out, so a failed send is told by the next pass,
// and never twice.
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
	mark := func(audience string) {
		ctx, cancel := call()
		defer cancel()
		if err := s.hooks.Told(ctx, p, audience); err != nil {
			fault(c, "told "+audience, err)
		}
	}
	if down, back, _ := p.Diff(hook.ToCoordinator); p.Changed(hook.ToCoordinator) {
		ls := lines(down, back)
		if p.Coord == "" || len(ls) == 0 || w.judge(c, s, b, []string{p.Coord}, strings.Join(ls, "; "), "Answer with nova-bus send --re <this message's id> and one of: wait | reassign <name>'s work | tell the owner.", within) {
			mark(hook.ToCoordinator) // nothing to say to the coordinator, or said
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
		mark(hook.ToRest)
		return
	}
	subject := strings.Join(ls, "; ")
	if was != "" && p.Coord == "" && slices.Contains(down, was) {
		if w.judge(c, s, b, rest, "COORDINATOR DOWN "+was+": who takes over? "+subject, "Answer with nova-bus send --re <this message's id> and one of: I take over (nova-hook hook --as <me> --role coordinator) | wait | tell the owner.", within) {
			mark(hook.ToRest)
		}
		return
	}
	if err := w.send(b, bus.Message{From: s.as, To: rest, Subject: subject, Body: subject + "\nnova-hook friends shows every name's state.\n"}); err != nil {
		fault(c, "tell", err)
		return
	}
	mark(hook.ToRest)
	serveLine(c, "told", subject, "to", strings.Join(rest, ","))
}

// judge raises one judgment: a bus message of kind judgment, open in
// hook:judgments until a recipient answers it. It reports whether the message
// went out.
func (w world) judge(c *tool.Call, s *session, b *bus.Bus, to []string, subject, answers string, within time.Duration) bool {
	m, err := w.deliver(b, bus.Message{From: s.as, To: to, Kind: bus.KindJudgment, Subject: subject, Body: subject + "\n" + answers + "\n"})
	if err != nil {
		fault(c, "judgment", err)
		return false
	}
	ctx, cancel := call()
	err = s.hooks.OpenJudgment(ctx, hook.Judgment{ID: m.ID, To: to, Subject: subject}, within)
	cancel()
	if err != nil {
		fault(c, "judgment record", err) // delivered all the same: it is told, unopened
		return true
	}
	serveLine(c, "judgment", subject, "id", m.ID, "to", strings.Join(to, ","))
	return true
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
			// no --re on the re-push: a reply is the bus's answer record, and the server may be the coordinator itself
			subject := "OVERDUE " + j.Subject
			if err := w.send(b, bus.Message{From: s.as, To: j.To, Kind: bus.KindJudgment, Subject: subject, Body: subject + "\nStill open: answer the judgment " + j.ID + " with nova-bus send --re " + j.ID + ".\n"}); err != nil {
				fault(c, "overdue", err)
				continue
			}
			ctx, cancel := call()
			err := s.hooks.Overdue(ctx, j)
			cancel()
			if err != nil {
				fault(c, "overdue record", err)
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

// challengeText is the heartbeat challenge as the session reads it.
func challengeText(name, nonce string, every time.Duration) string {
	return fmt.Sprintf("HOOK CHALLENGE nonce=%s\nThe hook's heartbeat asks whether the model is here. Answer from inside this turn, before anything else, within %s, with: nova-hook prove --as %s --nonce %s\nOnly the model answers: no daemon, loop or script may. %d unanswered in a row unhook %s.\n", nonce, every, name, nonce, hook.MissLimit, name)
}
