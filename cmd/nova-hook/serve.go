package main

import (
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
	ctx, cancel := call()
	err := s.hooks.Gate(ctx, hook.Want{Name: s.as, Need: []hook.Push{hook.Bus}, Roles: []hook.Role{hook.Coordinator, hook.Comms}, Role: hook.Comms})
	cancel()
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
	if p.Changed() {
		w.tell(c, s, b, p, within)
	}
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

// tell says what changed in this pass, once, in at most two messages
// (tla/HookBeat.tla, Tell): the coordinator gets one judgment naming every
// friend down or back; every other live name one message with all of it, a
// judgment when the coordinator itself went down (who takes over).
func (w world) tell(c *tool.Call, s *session, b *bus.Bus, p hook.Passed, within time.Duration) {
	ctx, cancel := call()
	sts, _, err := s.hooks.Read(ctx, p.Down...)
	cancel()
	if err != nil {
		fault(c, "reasons", err)
		return
	}
	var lines []string
	for _, st := range sts {
		why := st.Reason
		if why == "" {
			why = "unhooked"
		}
		lines = append(lines, "FRIEND DOWN "+st.Name+": "+why)
	}
	for _, n := range p.Back {
		lines = append(lines, "FRIEND BACK "+n)
	}
	coordDown := p.WasCoord != "" && p.Coord == "" && slices.Contains(p.Down, p.WasCoord)
	if p.Coord != "" && len(lines) > 0 {
		w.judge(c, s, b, []string{p.Coord}, strings.Join(lines, "; "), "Answer with nova-bus send --re <this message's id> and one of: wait | reassign <name>'s work | tell the owner.", within)
	}
	if p.Coord != p.WasCoord && p.Coord != "" {
		lines = append(lines, "COORDINATOR IS "+p.Coord)
	}
	var rest []string // the live names this pass did not change: the ones who need telling
	for _, n := range p.Live {
		if n != p.Coord && !slices.Contains(p.Back, n) {
			rest = append(rest, n)
		}
	}
	if len(rest) == 0 || len(lines) == 0 {
		return
	}
	if coordDown {
		w.judge(c, s, b, rest, "COORDINATOR DOWN "+p.WasCoord+": who takes over? "+strings.Join(lines, "; "), "Answer with nova-bus send --re <this message's id> and one of: I take over (nova-hook hook --as <me> --role coordinator) | wait | tell the owner.", within)
		return
	}
	subject := strings.Join(lines, "; ")
	if err := w.send(b, bus.Message{From: s.as, To: rest, Subject: subject, Body: subject + "\nnova-hook friends shows every name's state.\n"}); err != nil {
		fault(c, "tell", err)
		return
	}
	serveLine(c, "told", subject, "to", strings.Join(rest, ","))
}

// judge raises one judgment: a bus message of kind judgment, open in
// hook:judgments until a recipient answers it.
func (w world) judge(c *tool.Call, s *session, b *bus.Bus, to []string, subject, answers string, within time.Duration) {
	ctx, cancel := call()
	m, err := b.Send(ctx, bus.Message{From: s.as, To: to, Kind: bus.KindJudgment, Subject: subject, Body: subject + "\n" + answers + "\n"})
	cancel()
	if err != nil {
		fault(c, "judgment", err)
		return
	}
	ctx, cancel = call()
	err = s.hooks.OpenJudgment(ctx, hook.Judgment{ID: m.ID, To: to, Subject: subject}, within)
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
	ctx, cancel := call()
	defer cancel()
	_, err := b.Send(ctx, m)
	return err
}

// challengeText is the heartbeat challenge as the session reads it.
func challengeText(name, nonce string, every time.Duration) string {
	return fmt.Sprintf("HOOK CHALLENGE nonce=%s\nThe hook's heartbeat asks whether the model is here. Answer from inside this turn, before anything else, within %s, with: nova-hook prove --as %s --nonce %s\nOnly the model answers: no daemon, loop or script may. %d unanswered in a row unhook %s.\n", nonce, every, name, nonce, hook.MissLimit, name)
}
