package main

import (
	"context"
	"fmt"
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
	notify := c.Str("notify")
	if notify == "" {
		notify = s.as
	}
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
		w.pass(c, s, b, every, notify)
		return tool.Done().Fact("as", s.as).Fact("every", every.String())
	}
	sig, stop := w.signals(context.Background())
	defer stop()
	for {
		w.pass(c, s, b, every, notify)
		select {
		case <-sig.Done():
			return tool.Done().Fact("as", s.as).Fact("stopped", true)
		case <-w.after(every):
		}
	}
}

// serveLine prints one line of the server as it goes.
func serveLine(c *tool.Call, o *tool.Out) {
	o.Verb = "serve"
	o.Render(c.Stdout, false)
}

// pass is one heartbeat pass and its deliveries: the challenges to each name,
// the unhooked set to notify when it changed, and the asks' expiry. Every call
// is bounded by its own deadline (call); each fault is said and passed over.
func (w world) pass(c *tool.Call, s *session, b *bus.Bus, every time.Duration, notify string) {
	fault := func(what string, err error) {
		serveLine(c, tool.Done().As("FAULT").Fact("at", what).Note(err.Error()+"; the next pass tries again"))
	}
	ctx, cancel := call()
	names, err := s.hooks.Names(ctx)
	cancel()
	if err != nil {
		fault("names", err)
		return
	}
	ctx, cancel = call()
	p, err := s.hooks.Pass(ctx, names, every, hook.MissLimit, w.nonce, w.holder)
	cancel()
	if err != nil {
		fault("pass", err)
		return
	}
	if !p.Taken {
		serveLine(c, tool.Done().As("SKIP").Note("another server took this interval's pass"))
		return
	}
	for _, bt := range p.Beats {
		switch bt.Did {
		case hook.BeatChallenged:
			// the nonce goes only to the name's own stream: never on this server's lines
			if err := w.send(b, bus.Message{From: s.as, To: []string{bt.Name}, Subject: "HOOK CHALLENGE nonce=" + bt.Nonce, Body: challengeText(bt.Name, bt.Nonce, every)}); err != nil {
				fault("challenge "+bt.Name, err)
				continue
			}
			serveLine(c, tool.Done().As("CHALLENGE").Fact("name", bt.Name).Fact("epoch", bt.Epoch).Fact("misses", bt.Misses))
		case hook.BeatUnhooked:
			serveLine(c, tool.Done().As("UNHOOKED").Fact("name", bt.Name).Fact("reason", tool.Text(bt.Reason())))
		}
	}
	if p.Changed {
		set := strings.Join(p.Unhooked, ",")
		if set == "" {
			set = "none"
		}
		body := fmt.Sprintf("The names unhooked now: %s. The heartbeat unhooks a name after %d challenges in a row go unanswered; a name hooks in again with nova-hook hook --as <name> --role <role>. nova-hook status lists every record.\n", set, hook.MissLimit)
		if err := w.send(b, bus.Message{From: s.as, To: []string{notify}, Kind: bus.KindBlocker, Subject: "HOOK UNHOOKED " + set, Body: body}); err != nil {
			fault("tell "+notify, err)
		} else {
			serveLine(c, tool.Done().As("TOLD").Fact("to", notify).Fact("unhooked", set))
		}
	}
	ctx, cancel = call()
	expired, err := b.ExpireAsks(ctx)
	cancel()
	for _, e := range expired {
		serveLine(c, tool.Done().As("EXPIRED").Fact("ask", e.Ask.ID).Fact("from", e.Ask.From))
	}
	if err != nil {
		fault("asks", err)
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
