package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/machine"
	spverbs "github.com/mas-bandwidth/nova-tools/internal/sprint/verbs"
)

// The calls of the new path's table: each verb's words and flags as the
// request of its function in internal/sprint/verbs (IT19 to IT22) or IT17's
// tick loop (internal/sprint/machine). A call that reads puts the view a
// program reads (--json) in parsed.view.

// ---- IT17: the machine's loop

// loopConfig is a run loop's config on the new path: the deployment's names,
// this process's owner token and the actor's name for the lease (1.1), the
// app's clock for the waits between ticks (every stamp is the store's).
func (a *app) loopConfig(e *spverbs.Env) machine.Config {
	ps := a.pathState()
	if ps.owner == "" {
		ps.owner = spverbs.NewOp()
	}
	return machine.Config{Names: e.Names, Owner: ps.owner, Name: e.Actor, Now: a.now}
}

// callTick runs one tick (1.4.2; IT17's Tick) with this process's loop, which
// the next tick of the same process goes on from (its lease, its cursor, what
// it owes).
func callTick(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	ps := p.app.pathState()
	if ps.loop == nil {
		l, err := machine.NewLoop(p.app.loopConfig(e))
		if err != nil {
			return spverbs.Result{Verb: p.verb}, err
		}
		ps.loop = l
	}
	rep, err := machine.Tick(ctx, e.C, ps.loop)
	res := spverbs.Result{Verb: p.verb, Trips: rep.RoundTrips}
	if n, ok := strconv.ParseUint(string(rep.Epoch), 10, 64); ok == nil {
		res.Epoch = n
	}
	refused := 0
	for _, n := range rep.Refused {
		refused += n
	}
	state := "STOPPED"
	if rep.Running {
		state = "RUNNING"
	}
	res.Said = fmt.Sprintf("tick: %s, lease held %v; %d lines, %d keys, %d steps applied, %d refused", state, rep.Held, rep.Lines, rep.Keys, rep.Applied, refused)
	p.view = rep
	if err == nil {
		err = rep.Err
	}
	return res, err
}

// callRun ticks every TickEvery until interrupted (1.4.2; IT17's Run): a tick
// that fails is counted in the heartbeat and the loop goes on.
func callRun(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	ctx, stop := p.app.notify(ctx)
	defer stop()
	err := machine.Run(ctx, e.C, p.app.loopConfig(e))
	res := spverbs.Result{Verb: p.verb, Said: "run: interrupted; the loop stopped ticking"}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return res, nil
	}
	return res, err
}

// ---- IT19: add, release, rank

func callAdd(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	r := sprint.AddReq{Stream: p.str("stream"), IDs: p.words, Count: p.num("count"), Needs: sprint.Split(p.str("needs")),
		Brief: p.str("brief"), Before: p.str("before"), After: p.str("after"), Every: p.num("sentinel-every"),
		Last: p.on("sentinel-last"), Op: p.c.op, Who: e.Actor}
	if s := p.str("sentinel"); s != "" {
		r.IDs, r.Sentinel = []string{s}, true
	}
	if s := p.str("score"); s != "" {
		f, _ := strconv.ParseFloat(s, 64) // checkAdd parsed it
		r.Score = &f
	}
	return spverbs.Add(ctx, e, r)
}

func callRelease(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	return spverbs.Release(ctx, e, sprint.ReleaseReq{IDs: p.words, Reason: p.str("reason"), Answers: sprint.Split(p.str("answers")),
		Coordinator: e.Actor, Who: e.Actor, Op: p.c.op})
}

func callRank(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	r := sprint.RankReq{IDs: p.words, First: p.on("first"), Before: p.str("before"), After: p.str("after"),
		Answers: sprint.Split(p.str("answers")), Who: e.Actor, Op: p.c.op}
	if s := p.str("score"); s != "" {
		f, _ := strconv.ParseFloat(s, 64) // the check parsed it
		r.Score = &f
	}
	return spverbs.Rank(ctx, e, r)
}

// ---- IT20: the workers' verbs and the fleet's

// namedGens are the cards named <card>[@<gen>].
func namedGens(words []string) ([]spverbs.CardGen, error) {
	out := make([]spverbs.CardGen, 0, len(words))
	for _, w := range words {
		c, err := spverbs.ParseCardGen(w)
		if err != nil {
			return nil, usage("%v", err)
		}
		out = append(out, c)
	}
	return out, nil
}

// readyOf are the first n work cards of a member's queue in ready, at their
// generations (take with no cards named: --limit, default 1).
func readyOf(ctx context.Context, e *spverbs.Env, as string, n int) ([]spverbs.CardGen, spverbs.Result, error) {
	if n <= 0 {
		n = 1
	}
	page, res, err := spverbs.QueueRead(ctx, e, spverbs.QueueReq{As: as, Limit: min(max(n, spverbs.QueueLimit), spverbs.QueueLimitMax)})
	if err != nil {
		return nil, res, err
	}
	var out []spverbs.CardGen
	for _, c := range page.Cards {
		if len(out) == n {
			break
		}
		if !strings.HasSuffix(c.Cell, ":"+string(sprint.Ready)) {
			continue
		}
		gen, _ := strconv.Atoi(c.Fields["gen"])
		out = append(out, spverbs.CardGen{Card: c.ID, Gen: gen})
	}
	return out, res, nil
}

func callTake(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	cards, err := namedGens(p.words)
	if err != nil {
		return spverbs.Result{Verb: p.verb}, err
	}
	pre := 0
	if len(cards) == 0 {
		var qr spverbs.Result
		if cards, qr, err = readyOf(ctx, e, p.str("as"), p.num("limit")); err != nil {
			return qr, err
		}
		pre = qr.Trips
		if len(cards) == 0 {
			return spverbs.Result{Verb: p.verb, Trips: pre, Replay: true, Said: "take: " + p.str("as") + " has nothing ready; nothing was written"}, nil
		}
	}
	res, err := spverbs.Take(ctx, e, spverbs.TakeReq{Cards: cards, As: p.str("as")})
	res.Trips += pre
	return res, err
}

func callFinish(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	cards, err := namedGens(p.words)
	if err != nil {
		return spverbs.Result{Verb: p.verb}, err
	}
	return spverbs.Finish(ctx, e, spverbs.FinishReq{Cards: cards, As: p.str("as"), Failed: p.on("failed"), Head: p.str("head"),
		Report: p.str("report"), Branch: p.str("branch"), Base: p.str("base")})
}

func callRead(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	cards, err := namedGens(p.words)
	if err != nil {
		return spverbs.Result{Verb: p.verb}, err
	}
	r := spverbs.ReadCardReq{Cards: cards, As: p.str("as"), Begin: p.on("begin"), Finding: p.str("finding")}
	switch {
	case p.on("ok"):
		r.Verdict = sprint.OK
	case p.on("broken"):
		r.Verdict = sprint.Broken
	}
	if !r.Begin {
		r.Summary = firstLine(r.Finding)
	}
	return spverbs.ReadCard(ctx, e, r)
}

// firstLine is a text's first line, the summary a read's finding gives.
func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return strings.TrimSpace(l)
}

// newQueueCard is a card of a queue as a program reads it (--json): the driver's
// id, col and gen, and the rest.
type newQueueCard struct {
	ID     string            `json:"id"`
	Col    string            `json:"col"`
	Gen    int               `json:"gen,omitempty"`
	Cell   string            `json:"cell"`
	Score  string            `json:"score"`
	Fields map[string]string `json:"fields,omitempty"`
}

// newQueueView is one page of a queue (--json).
type newQueueView struct {
	Cards []newQueueCard `json:"cards"`
	Next  string         `json:"next,omitempty"`
}

func callQueue(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	page, res, err := spverbs.QueueRead(ctx, e, spverbs.QueueReq{As: p.str("as"), Stream: p.str("stream")})
	if err != nil {
		return res, err
	}
	v := newQueueView{Cards: []newQueueCard{}, Next: page.Next}
	var lines []string
	for _, c := range page.Cards {
		_, col, _ := strings.Cut(c.Cell, ":")
		if p.str("col") != "" && col != p.str("col") {
			continue
		}
		gen, _ := strconv.Atoi(c.Fields["gen"])
		v.Cards = append(v.Cards, newQueueCard{ID: c.ID, Col: col, Gen: gen, Cell: c.Cell, Score: c.Score, Fields: c.Fields})
		line := c.ID + " " + col
		if gen > 0 {
			line = fmt.Sprintf("%s@%d %s", c.ID, gen, col)
		}
		lines = append(lines, line)
	}
	p.view = v
	res.Said = fmt.Sprintf("queue: %d cards", len(v.Cards))
	if len(lines) > 0 {
		res.Said += "\n" + strings.Join(lines, "\n")
	}
	return res, nil
}

func callFleetBeat(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	var loads map[string]string
	if l := p.str("load"); l != "" {
		loads = map[string]string{}
		for _, m := range p.words {
			loads[m] = l
		}
	}
	return spverbs.FleetBeat(ctx, e, spverbs.FleetBeatReq{Members: p.words, Loads: loads})
}

func callFleetUp(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	return spverbs.FleetUp(ctx, e, spverbs.FleetReq{Members: p.words, Op: p.c.op})
}

func callFleetDown(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	return spverbs.FleetDown(ctx, e, spverbs.FleetReq{Members: p.words, Op: p.c.op})
}

func callReaderAdd(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	return spverbs.ReaderAdd(ctx, e, spverbs.ReaderAddReq{Readers: p.words, Op: p.c.op})
}

// ---- IT21: review, merge, drop

func callAsk(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	return spverbs.Ask(ctx, e, spverbs.AskReq{Op: p.c.op, IDs: p.words, Another: p.on("another")})
}

func callAccept(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	return spverbs.Accept(ctx, e, spverbs.AcceptReq{Op: p.c.op, IDs: p.words, Streams: sprint.Split(p.str("stream"))})
}

func callRework(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	return spverbs.Rework(ctx, e, spverbs.ReworkReq{Op: p.c.op, IDs: p.words, Streams: sprint.Split(p.str("stream")), Fix: p.str("fix")})
}

func callReturn(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	return spverbs.Return(ctx, e, spverbs.ReturnReq{Op: p.c.op, IDs: p.words})
}

func callDrop(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	if p.on("abort") {
		return spverbs.DropAbort(ctx, e, spverbs.DropAbortReq{Op: p.c.op})
	}
	return spverbs.Drop(ctx, e, spverbs.DropReq{Op: p.c.op, IDs: p.words, Streams: sprint.Split(p.str("stream")), Col: p.str("col"), Reason: p.str("reason")})
}

func callCI(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	return spverbs.CI(ctx, e, spverbs.CIReq{Op: p.c.op, IDs: p.words, Red: p.on("red"), Head: p.str("head")})
}

func callMerge(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	suspects := p.list("suspect")
	if len(suspects) > 0 {
		suspects = append(suspects, p.words...) // ids after --suspect are more suspects
	}
	return spverbs.Merge(ctx, e, spverbs.MergeReq{Op: p.c.op, Stream: p.str("stream"), Batch: p.num("batch"), Conflict: p.str("conflict"),
		Cross: p.str("cross"), Red: p.on("red"), Suspects: suspects, Rejected: p.on("rejected"), Note: p.str("note")})
}

func callResume(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	return spverbs.Resume(ctx, e, spverbs.ResumeReq{Op: p.c.op, Streams: sprint.Split(p.str("stream")), Did: p.str("did")})
}

// ---- IT22: judgments and reads

func callAck(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	return spverbs.Ack(ctx, e, spverbs.AckReq{Op: p.c.op, Notes: p.words, Reason: p.str("reason")})
}

func callWait(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	d := p.dur("for")
	if u := p.str("until"); u != "" {
		t, _ := time.Parse(time.RFC3339, u) // the check parsed it
		if d = t.Sub(p.app.now()); d <= 0 {
			return spverbs.Result{Verb: p.verb}, usage("--until %s is past", u)
		}
	}
	return spverbs.Wait(ctx, e, spverbs.WaitReq{Op: p.c.op, Notes: p.words, For: d, Reason: p.str("reason")})
}

func callInbox(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	var v spverbs.InboxView
	req := spverbs.InboxReq{Read: p.on("read"), Out: &v}
	if p.on("wait") {
		notes, closer, err := p.app.noteStream(p.c.redis)
		if err != nil {
			return spverbs.Result{Verb: p.verb}, err
		}
		if closer != nil {
			defer func() { _ = closer() }()
		}
		req.Wait = &spverbs.InboxWait{Notes: notes, Timeout: p.dur("timeout"), Now: p.app.now}
	}
	res, err := spverbs.Inbox(ctx, e, req)
	p.view = v
	return res, err
}

func callCard(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	var v spverbs.CardView
	res, err := spverbs.Card(ctx, e, spverbs.CardReq{ID: p.words[0], Out: &v})
	p.view = v
	return res, err
}

func callLog(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	var v spverbs.LogView
	req := spverbs.LogReq{Card: p.str("card"), Stream: p.str("stream"), Out: &v}
	if s := p.str("since"); s != "" {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return spverbs.Result{Verb: p.verb}, usage("--since wants the seq of a line (the log's cursor) on the new path, got %q", s)
		}
		req.Since = n
	}
	res, err := spverbs.Log(ctx, e, req)
	p.view = v
	return res, err
}

func callWhere(ctx context.Context, e *spverbs.Env, p *parsed) (spverbs.Result, error) {
	var v spverbs.WhereView
	res, err := spverbs.Where(ctx, e, spverbs.WhereReq{Out: &v})
	p.view = v
	if err != nil || !p.on("watch") {
		return res, err
	}
	// --watch: redraw every --every until interrupted, passing the rows it saw
	// last (one round trip while they hold).
	wctx, stop := p.app.notify(ctx)
	defer stop()
	out := p.out
	for {
		if out != nil {
			fmt.Fprint(out, "\x1b[H\x1b[2J"+v.Frame+"\n")
		}
		if wctx.Err() != nil {
			res.Said = "" // drawn
			return res, nil
		}
		p.app.sleep(p.dur("every"))
		if wctx.Err() != nil {
			res.Said = ""
			return res, nil
		}
		rows := v.Rows
		next, err := spverbs.Where(wctx, e, spverbs.WhereReq{Rows: rows, Out: &v})
		res.Trips += next.Trips
		if err != nil {
			if wctx.Err() != nil {
				res.Said = ""
				return res, nil
			}
			return res, err
		}
		p.view = v
	}
}

// newSprintLine is the sprint's line after a write (the present command prints
// one after every verb that changed something): the machine's word and the
// view's summary, from IT22's Where. A read that fails prints nothing.
func newSprintLine(ctx context.Context, e *spverbs.Env) string {
	var v spverbs.WhereView
	if _, err := spverbs.Where(ctx, e, spverbs.WhereReq{Out: &v}); err != nil {
		return ""
	}
	if strings.HasPrefix(v.Machine, "STOPPED") {
		if v.All == 0 {
			return v.Machine
		}
		return v.Machine + "  " + v.Summary
	}
	return strings.TrimSpace(v.Summary + "  " + v.Machine)
}

// writeSprintLine prints the sprint's line after a verb that wrote.
func writeSprintLine(ctx context.Context, e *spverbs.Env, res spverbs.Result, w io.Writer) {
	if res.Step == nil || res.Replay {
		return
	}
	if line := newSprintLine(ctx, e); line != "" {
		fmt.Fprintln(w, line)
	}
}
