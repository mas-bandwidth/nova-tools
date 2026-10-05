package friend

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// The lanes' steps for what the runner stopgaps did (lanes_parity.go): each is called from
// laneStep or laneDone, on the loop's goroutine.

// readDealt reads the cards the sprint dealt her, and asks the coordinator once, in one
// blocker, to take back the ones outside her filter that she has not started. A read that
// fails keeps the last answer, said once per new failure.
func (l *loop) readDealt(now time.Time) {
	d, s := l.d, l.lanes
	if d.Dealt == nil {
		return
	}
	dealt, err := d.Dealt(l.ctx)
	if err != nil {
		if err.Error() != s.saidErr {
			s.saidErr = err.Error()
			d.Record(fmt.Sprintf("%s lanes: the cards dealt to her were not read: %s; the queue file alone is used", now.UTC().Format(time.RFC3339), oneLine(err.Error(), 300)))
		}
		return
	}
	s.dealt, s.dealtOK, s.saidErr = dealt, true, ""
	takes := TakeBacks(s.cfg, dealt, l.started, s.asked)
	if len(takes) == 0 {
		return
	}
	for _, t := range takes {
		s.asked[t.Card.Job()] = true
		d.Record(fmt.Sprintf("%s lanes: card %s (%s) is outside her filter and not started: %s; the coordinator is asked to take it back", now.UTC().Format(time.RFC3339), t.Card.ID, t.Card.Job(), t.Why))
	}
	subject, body := TakeBackText(d.Friend, takes)
	l.tellKind(bus.KindBlocker, subject, body, now)
}

// started says whether she has started the dealt card: a lane holds it, its job directory
// is there, or its outbox has a report.
func (l *loop) started(c Dealt) bool {
	job := c.Job()
	for _, ln := range l.lanes.lanes {
		if ln.card != nil && filepath.Base(ln.card.Outbox) == job {
			return true
		}
	}
	return exists(filepath.Join(l.d.Dir, "jobs", job)) || exists(filepath.Join(l.d.Dir, "outbox", job, ReportFile))
}

// works says whether her lanes run the card: the dealt card's tier and stream when the
// sprint's answer has it, else the tier its brief says; a card outside her filter is said
// once and never handed.
func (l *loop) works(c Card, now time.Time) bool {
	s := l.lanes
	if len(s.cfg.Tiers) == 0 && len(s.cfg.Cards) == 0 {
		return true
	}
	job := filepath.Base(c.Outbox)
	dc := Dealt{ID: c.ID}
	found := false
	for _, x := range s.dealt {
		if x.Job() == job || (!found && x.ID == c.ID) {
			dc, found = x, true
		}
	}
	if !found {
		if raw, err := os.ReadFile(c.Brief); err == nil {
			dc.Tier = TierOfBrief(string(raw))
		}
	}
	ok, why := s.cfg.Works(dc)
	if !ok && !s.notRun[job] {
		s.notRun[job] = true
		l.d.Record(fmt.Sprintf("%s lanes: card %s (%s) not run: %s", now.UTC().Format(time.RFC3339), c.ID, job, why))
	}
	return ok
}

// loadCap is the lanes the machine's load allows at width (LaneConfig.LoadCap), each change
// said on the record, and a hold told to the coordinator.
func (l *loop) loadCap(now time.Time, width int) int {
	d, s := l.d, l.lanes
	load, read := 0.0, false
	if d.Load != nil && s.cfg.LoadMax > 0 {
		load, read = d.Load()
	}
	limit := s.cfg.LoadCap(width, load, read)
	at := now.UTC().Format(time.RFC3339)
	switch {
	case limit < width && !s.loaded1:
		s.loaded1 = true
		d.Record(fmt.Sprintf("%s load: %.1f above %g: new lanes held to %d of %d", at, load, s.cfg.LoadMax, limit, width))
		l.tell(fmt.Sprintf("friend %s: lanes held to %d of %d: load %.1f above %g", d.Friend, limit, width, load, s.cfg.LoadMax),
			fmt.Sprintf("The machine's 1-minute load is %.1f, above her bound of %g; her new lanes are held to %d of %d until it falls.\n", load, s.cfg.LoadMax, limit, width), now)
	case limit >= width && s.loaded1:
		s.loaded1 = false
		d.Record(fmt.Sprintf("%s load: %.1f at or below %g: lanes back to %d", at, load, s.cfg.LoadMax, width))
	}
	return limit
}

// cardBase reads the lane session's tokens as its card comes to it: the card's tokens are
// what the session counts beyond them (the session is the lane's, shared by its cards).
func (l *loop) cardBase(ln *lane, now time.Time) {
	ln.since, ln.base, ln.baseErr, ln.capped = now, Tokens{}, "", 0
	if l.d.TokensOf == nil {
		ln.baseErr = "no token source"
		return
	}
	base, err := l.d.TokensOf(l.ctx, ln.session)
	if err != nil {
		ln.baseErr = oneLine(err.Error(), 200)
		return
	}
	ln.base = base
}

// capCheck stops each lane whose card has reached the token cap: its turn is cancelled,
// and its end writes the HOLD (laneDone).
func (l *loop) capCheck(now time.Time) {
	d, s := l.d, l.lanes
	if s.cfg.TokenCap <= 0 || d.TokensOf == nil {
		return
	}
	for _, ln := range s.lanes {
		if ln.t == nil || !ln.t.running || ln.card == nil || ln.capped > 0 || ln.baseErr != "" {
			continue
		}
		t, err := d.TokensOf(l.ctx, ln.session)
		if err != nil {
			continue // read again next step
		}
		if n := t.Since(ln.base).Total(); n >= s.cfg.TokenCap {
			ln.capped = n
			ln.t.cancel()
			d.Record(fmt.Sprintf("%s lane %d: card %s at %d tokens, the cap is %d: the lane's turn is stopped", now.UTC().Format(time.RFC3339), ln.n, ln.card.ID, n, s.cfg.TokenCap))
		}
	}
}

// leave is a card leaving its lane (done, capped or set aside): with a token source
// (TokensOf), its tokens since it came to the lane, priced by the route row; the friend's REPORT.draft.md published as REPORT.md with
// the Cost: line under Head: and RESULT.md given tokens: and cost: lines (PublishCost); the
// lane's end (endCard: her report is the finish, else the lane writes one, which gets the
// Cost: line too); and a bus note to the coordinator. It answers the record's words.
func (l *loop) leave(ln *lane, card Card, end LaneEnd, now time.Time) string {
	d, s := l.d, l.lanes
	model := s.cfg.Model
	var tk Tokens
	cost, cline := "-", ""
	if d.TokensOf != nil { // a harness whose tokens the lanes read (opencode's database): the card is priced
		var route string
		tk, cost, route = l.cardCost(ln)
		cline = CostLine(cost, tk, model, route)
	}
	first, err := PublishCost(card.Outbox, cline, tk, model, cost)
	words := l.endCard(ln.n, card, end, now)
	if err == nil && cline != "" && !strings.HasPrefix(words, "finish=report") {
		first, err = PublishCost(card.Outbox, cline, tk, model, cost) // the report the lane wrote
	}
	if cline != "" {
		words += fmt.Sprintf(" cost=%q tokens=%q", cost, tk.Said())
	}
	if err != nil {
		words += fmt.Sprintf(" cost_error=%q", oneLine(err.Error(), 200))
	}
	subject, body := FinishText(d.Friend, filepath.Base(card.Outbox), first, cost, now.Sub(ln.since))
	l.tell(subject, body, now)
	return words
}

// cardCost is the card's tokens in its lane (the session's and its children's beyond what
// they counted when the card came to the lane) and its cost by the route row of the lanes'
// model, with the row's name; unpriced with the reason when the tokens or the routes were not
// read.
func (l *loop) cardCost(ln *lane) (tk Tokens, cost, route string) {
	d, s := l.d, l.lanes
	if ln.baseErr != "" {
		return Tokens{}, "unpriced (the tokens were not read: " + ln.baseErr + ")", ""
	}
	t, err := d.TokensOf(l.ctx, ln.session)
	if err != nil {
		return Tokens{}, "unpriced (the tokens were not read: " + oneLine(err.Error(), 200) + ")", ""
	}
	tk = t.Since(ln.base)
	var r Route
	found := false
	if d.Route != nil {
		r, found, err = d.Route(l.ctx, s.cfg.Model)
	}
	if err != nil {
		return tk, "unpriced (the routes were not read: " + oneLine(err.Error(), 200) + ")", ""
	}
	return tk, CostOf(tk, r, found, s.cfg.Model), r.Name
}

// holdAll is the provider out of funds: every lane's turn stopped (its card kept in hand),
// the hold written with the exact message to HoldPath so a restart does not resume, and
// said on the record.
func (l *loop) holdAll(reason string, now time.Time) {
	d, s := l.d, l.lanes
	at := now.UTC().Format(time.RFC3339)
	for _, ln := range s.lanes {
		if ln.t != nil && ln.t.running && !ln.t.halted {
			ln.t.halted = true
			ln.t.cancel()
		}
	}
	until := "until the daemon restarts"
	if d.HoldPath != "" {
		if err := WriteHold(d.HoldPath, at+" "+oneLine(reason, 500)); err != nil {
			d.Record(fmt.Sprintf("%s out of funds: the hold was not written to %s: %s", at, d.HoldPath, err))
		} else {
			s.filed = true
			until = "until a person runs nova-friend resume --as " + d.Friend + " (" + d.HoldPath + "), no beat meanwhile"
		}
	}
	d.Record(fmt.Sprintf("%s out of funds: lanes held %s, every lane stopped, every card kept in hand: %s", at, until, oneLine(reason, 200)))
}
