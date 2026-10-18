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
	friendcards "github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/mas-bandwidth/nova-tools/pkg/harness/notice"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// serve is the heartbeat server (docs/SPEC-HOOK.md, the heartbeat;
// tla/HookBeat.tla): one pass now, then one every --every until a signal. A
// busyFriends reads the authoritative ready/working rows in one sprint
// request (tla/HookBeat.tla, SetBusy). Unknown rows use the shorter busy
// window and surface a fault; no cached counter is a source of truth.
func (w world) busyFriends(c *tool.Call, s *session, names []string) []string {
	ctx, cancel := call()
	defer cancel()
	rows, _, err := s.hooks.Read(ctx, names...)
	if err != nil {
		fault(c, "friend roles", err)
		return names
	}
	var friends []string
	var verbs [][]string
	for _, row := range rows {
		if row.Role == hook.Friend && row.State != hook.StateUnhooked {
			friends = append(friends, row.Name)
			verbs = append(verbs, friendcards.FriendCardsArgv(row.Name))
		}
	}
	if len(friends) == 0 {
		return nil
	}
	if w.sprintBatch == nil {
		fault(c, "friend cards", fmt.Errorf("sprint reader unavailable; using busy silence window"))
		return friends
	}
	results, err := w.sprintBatch(ctx, c.Str("sprint-server"), verbs...)
	if err != nil || len(results) != len(friends) {
		fault(c, "friend cards", fmt.Errorf("sprint rows unavailable (%v, %d of %d replies); using busy silence window", err, len(results), len(friends)))
		return friends
	}
	var busy []string
	for i, name := range friends {
		cards, err := friendcards.ParseHeld(name, results[i].Stdout)
		if results[i].Code != 0 || err != nil {
			fault(c, "friend cards "+name, fmt.Errorf("sprint row refused or invalid (%d, %v); using busy silence window", results[i].Code, err))
			busy = append(busy, name)
		} else if slices.ContainsFunc(cards, func(card friendcards.HeldCard) bool { return card.Col == "ready" || card.Col == "working" }) {
			busy = append(busy, name)
		}
	}
	return busy
}

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
	downAfter := c.Dur("down-after")
	if downAfter < every {
		return tool.Refuse(fmt.Sprintf("--down-after %s is under --every %s; it wants the silence before a name goes down, at least one interval, such as %s", downAfter, every, hook.DownAfter))
	}
	within := c.Dur("judge-within")
	// the server needs only its store: no hook record of its own name is read here,
	// so a server starts however its name is hooked (absent, partial or unhooked)
	b := &bus.Bus{Store: s.bus}
	if c.DryRun() {
		return w.plan(c, s, b, every, downAfter)
	}
	if c.Bool("once") {
		w.pass(c, s, b, every, downAfter, within)
		return tool.Done().Fact("as", s.as).Fact("every", every.String()).Fact("down_after", downAfter.String())
	}
	sig, stop := w.signals(context.Background())
	defer stop()
	for {
		w.pass(c, s, b, every, downAfter, within)
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
func (w world) pass(c *tool.Call, s *session, b *bus.Bus, every, downAfter, within time.Duration) {
	ctx, cancel := call()
	names, err := s.hooks.Names(ctx)
	cancel()
	if err != nil {
		fault(c, "names", err)
		return
	}
	busy := w.busyFriends(c, s, names)
	ctx, cancel = call()
	p, err := s.hooks.Pass(ctx, names, every, downAfter, w.nonce, w.holder, busy...)
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
		m := bus.Message{From: s.as, To: []string{bt.Name}, Subject: "HOOK CHALLENGE nonce=" + bt.Nonce,
			Body: notice.ChallengeText(bt.Name, bt.Nonce, hook.AnswerBound), Once: hook.DeliveryOnce(bt.Name, bt.Epoch, bt.Nonce)}
		if _, ok, err := w.sendOnceErr(c, s, b, m); !ok && !unheard(err, bt.Name, s.as) {
			continue
		}
		// a name the bus refuses as no known name (a hook:names entry with no nova-config
		// row) is never heard: its refused challenge counts as sent, so it misses and unhooks
		// on the usual count instead of being challenged every pass for ever (a stray name on
		// the fleet's bus, 2026-10-17: 1,443 fault lines, never unhooked); a timeout or a
		// store fault is no such refusal, and is sent again at the next pass
		ctx, cancel := call()
		err := s.hooks.Delivered(ctx, bt)
		cancel()
		if err != nil {
			fault(c, "challenge delivery "+bt.Name, err)
			continue
		}
		serveLine(c, "challenge", "", "name", bt.Name, "misses", bt.Misses)
	}
	w.push(c, s, b, p, names)
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

// plan is --dry-run: what one pass now would do (pass), read from the same
// script with its writes off (hook.Store.PlanPass) and the asks' expiry plan
// (bus.DueAsks); nothing is challenged, sent, told or marked.
func (w world) plan(c *tool.Call, s *session, b *bus.Bus, every, downAfter time.Duration) *tool.Out {
	ctx, cancel := call()
	names, err := s.hooks.Names(ctx)
	cancel()
	if err != nil {
		return tool.Fail("names: " + err.Error())
	}
	busy := w.busyFriends(c, s, names) // the plan reads the same busy set the pass does
	ctx, cancel = call()
	p, err := s.hooks.PlanPass(ctx, names, every, downAfter, w.holder, busy...)
	cancel()
	if err != nil {
		return tool.Fail("pass: " + err.Error())
	}
	o := tool.Done().Fact("as", s.as).Fact("every", every.String()).Fact("down_after", downAfter.String()).Fact("taken", p.Taken)
	if !p.Taken {
		return o.Note("another server took this interval's pass")
	}
	for _, bt := range p.Beats {
		switch bt.Did {
		case hook.BeatChallenged:
			o.Item("challenge", "name", bt.Name, "misses", bt.Misses)
		case hook.BeatUnhooked:
			o.Item("unhook", "name", bt.Name, "misses", bt.Misses)
		}
	}
	for _, a := range hook.Audiences {
		if p.Changed(a) {
			down, back, _ := p.Diff(a)
			o.Item("tell", "audience", a, "down", cmp.Or(strings.Join(down, ","), "-"), "back", cmp.Or(strings.Join(back, ","), "-"))
		}
	}
	ctx, cancel = call()
	due, err := b.DueAsks(ctx)
	cancel()
	if err != nil {
		return tool.Fail("asks: " + err.Error())
	}
	for _, a := range due {
		o.Item("expire", "ask", a.ID, "from", a.From)
	}
	return o
}

// fault is one store or bus fault, said and passed over.
func fault(c *tool.Call, what string, err error) {
	serveLine(c, "fault", err.Error()+"; the next pass tries again", "at", what)
}

// unheard says err is the bus's refusal of name as no known name while it
// knows the sender from: the roster answered, holds the server's own name and
// not name. An empty roster (restored empty, or not yet synced) refuses the
// sender too, and is a store fault, never a name's miss; so is a timeout.
func unheard(err error, name, from string) bool {
	return bus.UnknownName(err, name) && !bus.UnknownName(err, from)
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
		if st.State != hook.StateUnhooked { // hooked and down: no proof from the model (hook.Passed.Unhooked)
			why[st.Name] = "hooked, not proven (" + st.State + ")"
		}
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
		// her own return is no judgment for her (tla/HookBeat.tla, JudgeCoord)
		ls := lines(down, slices.DeleteFunc(slices.Clone(back), func(n string) bool { return n == p.Coord }))
		switch {
		case p.Coord == "":
			// no coordinator up: the audience is left unmarked, so the change piles up and
			// is judged to the next coordinator up (DownJudgedToCoordinator), never dropped
		case len(ls) == 0:
			nobody(hook.ToCoordinator)
		default:
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
	sent, ok, _ := w.sendOnceErr(c, s, b, m) // ignored: the fault is said; a caller that reads the refusal asks sendOnceErr
	return sent, ok
}

// sendOnceErr is sendOnce with the send's error when it did not go.
func (w world) sendOnceErr(c *tool.Call, s *session, b *bus.Bus, m bus.Message) (bus.Message, bool, error) {
	sent, err := w.deliver(b, m)
	if err == nil {
		return sent, true, nil
	}
	ctx, cancel := call()
	went, serr := s.hooks.Sent(ctx, m.Once)
	cancel()
	if serr == nil && went {
		return bus.Message{}, true, nil
	}
	fault(c, "send "+m.Once, err)
	return bus.Message{}, false, err
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

// serverSent says m, on the stream of the name as, is the heartbeat server's
// speaking under that same name (the server runs as the coordinator): a
// challenge, a friend-status line, or a judgment it raised or pushed again. The
// hook's loop passes over a message from its own name (what the session sent
// itself with nova-bus send) and shows these, each as its own HOOK PUSH line
// (tla/HookBeat.tla, Show; Broken "selffilter" is the loop that skipped them
// all, and the coordinator saw no friend change). The server's messages are
// told by their kind and their subject, which a session sending under its name
// with another kind or subject is not.
func serverSent(m bus.Message, as string) bool {
	if m.From != as {
		return false
	}
	switch m.Kind {
	case bus.KindStatus:
		// the bus stores a kind-less send as status (bus.Bus.check): the challenge,
		// and a judgment from a server whose wire form sends it with no kind (the
		// fleet's v1.4.5 FRIEND BACK, 2026-10-16)
		return strings.HasPrefix(m.Subject, stateSubject) || strings.HasPrefix(m.Subject, challengeSubject) || judgmentSubject(m.Subject)
	case bus.KindJudgment:
		return judgmentSubject(m.Subject)
	}
	return strings.HasPrefix(m.Subject, challengeSubject)
}

// stateSubject starts a friend-status line's subject; judgmentSubjects start
// the judgments the server raises (tell) and pushes again (judgments).
const stateSubject = "FRIEND STATE "

// challengeSubject starts the heartbeat's challenge, sent with no kind and
// stored as status.
const challengeSubject = "HOOK CHALLENGE "

var judgmentSubjects = []string{"FRIEND DOWN ", "FRIEND BACK ", "COORDINATOR DOWN ", "OVERDUE "}

// judgmentSubject says subject starts one of judgmentSubjects.
func judgmentSubject(subject string) bool {
	for _, p := range judgmentSubjects {
		if strings.HasPrefix(subject, p) {
			return true
		}
	}
	return false
}

// transition is one change of a name's state the server found at a pass: from
// old to new, at the store's TIME of the pass that saw it, in the epoch it was
// found in, and the count of its push (the once-key's).
type transition struct {
	old, new string
	at       time.Time
	epoch    int64
	gen      int64
}

// push tells the coordinator each friend's crossing of proven, up or down, as
// its own message (tla/HookBeat.tla, Pass's compare and PushSend): each name's
// state as the store reads it now, proven or down (upOrDown), against the
// state last pushed for it (the row's pushed mark, read the same way) or the
// last still in hand; a difference is a transition in hand, and
// each in hand goes out oldest first as one message of kind status, never
// folded with another name's or another change's, under a once-key of the name,
// its epoch, the new state and the count, in one write with the mark. A send
// that fails leaves the transition in hand and the mark unwritten, and the next
// pass sends it; one that went is never sent again (NeverPushedTwice,
// TransitionPushedToCoordinator). The message waits on no judgment and on no
// --judge-within: it is not answered and never OVERDUE. With no coordinator the
// pass pushes nothing, and the first pass of a fresh row writes the states it
// finds as pushed with no message.
//
// Only a crossing of proven is pushed (upOrDown): live, partial, expired,
// unhooked and down are one state, down, so a change among them is no
// message, and the coordinator's own state is never pushed to her. Each bus
// message interrupts the coordinator's turn, and she acts only on a friend
// that came up or went down; the FRIEND DOWN and FRIEND BACK judgments carry
// the rest. Measured on the fleet's bus 2026-10-17: 304 FRIEND STATE pushes
// in 34 hours, none to act on, one friend's live->unhooked->live flap (a
// service's re-arm) 183 of them and the coordinator's own
// proven->partial->proven 60, pushed to herself.
func (w world) push(c *tool.Call, s *session, b *bus.Bus, p hook.Passed, names []string) {
	if p.Coord == "" || len(names) == 0 {
		return
	}
	ctx, cancel := call()
	sts, now, err := s.hooks.Read(ctx, names...)
	cancel()
	if err != nil {
		fault(c, "friend states", err)
		return
	}
	ctx, cancel = call()
	pushed, seen, err := s.hooks.Pushed(ctx)
	cancel()
	if err != nil {
		fault(c, "pushed states", err)
		return
	}
	if !seen {
		base := map[string]string{}
		for _, st := range sts {
			base[st.Name] = upOrDown(hook.PushState(st))
		}
		ctx, cancel := call()
		err := s.hooks.Baseline(ctx, base)
		cancel()
		if err != nil {
			fault(c, "pushed baseline", err)
		}
		return
	}
	if s.pending == nil {
		s.pending = map[string][]transition{}
	}
	for _, st := range sts {
		if st.Name == p.Coord { // her own state is never pushed to her (tla/HookBeat.tla, NeverPushedSelf)
			delete(s.pending, st.Name)
			continue
		}
		last := upOrDown(cmp.Or(pushed[st.Name].State, hook.StateNone))
		gen := pushed[st.Name].Gen
		q := s.pending[st.Name]
		for len(q) > 0 && q[0].gen < gen { // sent already, by this server or another
			q = q[1:]
		}
		if len(q) > 0 {
			last = q[len(q)-1].new
		}
		if cur := upOrDown(hook.PushState(st)); cur != last {
			q = append(q, transition{old: last, new: cur, at: now, epoch: st.Epoch, gen: gen + int64(len(q))})
		}
		for len(q) > 0 {
			t := q[0]
			subject := fmt.Sprintf("%s%s %s->%s at=%s", stateSubject, st.Name, t.old, t.new, t.at.UTC().Format(time.RFC3339))
			mark := hook.PushMark(st.Name, t.new, t.gen)
			m := bus.Message{From: s.as, To: []string{p.Coord}, Kind: bus.KindStatus, Subject: subject,
				Body:  fmt.Sprintf("%s\nnova-hook friends shows every name's state. No answer is wanted.\n", subject),
				Once:  hook.PushOnce(st.Name, t.epoch, t.new, t.gen),
				Marks: []bus.Mark{{Key: hook.ServeKey, Field: mark.Field, Value: mark.Value}}}
			if _, ok := w.sendOnce(c, s, b, m); !ok {
				break // kept, and sent at the next pass
			}
			serveLine(c, "pushed", subject, "to", p.Coord)
			q = q[1:]
		}
		if len(q) == 0 {
			delete(s.pending, st.Name)
		} else {
			s.pending[st.Name] = q
		}
	}
}

// upOrDown is the state a friend-status push compares and names: proven, or
// down for every other state (tla/HookBeat.tla, PS). A mark written before
// this rule (live, partial, expired, unhooked) reads as down, so the first
// pass after an upgrade pushes nothing for it.
func upOrDown(state string) string {
	if state == hook.StateProven {
		return hook.StateProven
	}
	return hook.StateDown
}
