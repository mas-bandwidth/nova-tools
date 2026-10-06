package friend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// The runner's behaviours in the one-shot lanes' turn (lane_parity.go): each is called from
// laneStep and laneDone (lanes.go) when the daemon has a LaneParity (nova-friend run gives an
// opencode friend one), and does nothing when it has none.

// CapEvery is how often a running lane's tokens are read against the per-card cap; LoadEvery how
// often the machine's load is read; ParityWait how long one read of either, or of the route row,
// may take.
const (
	CapEvery   = 30 * time.Second
	LoadEvery  = 5 * time.Second
	ParityWait = 10 * time.Second
)

// parityState is what the lanes keep for the runner's behaviours.
type parityState struct {
	said     map[string]string // each job the filter refused, and how: said once while it stands
	loadAt   time.Time         // when the load was last read
	load     float64           // its answer
	loadErr  string            // a load that could not be read, said once while it stands
	held     int               // the width the load holds the lanes to; 0 not held
	holding  bool              // the governor's hold is a provider stop's (the hold file)
	readSaid map[string]bool   // usage reads that failed, said once per job
}

func (l *loop) parity() *LaneParity { return l.d.Parity }

// parityStep runs before the lanes hand anything: a hold file a person removed resumes the lanes
// (one there holds them), a provider failure in any running turn's output stops every lane, and
// each running card is read against the token cap.
func (l *loop) parityStep(now time.Time) {
	p := l.parity()
	if p == nil {
		return
	}
	s, d := l.lanes, l.d
	if s.parity.said == nil {
		s.parity.said, s.parity.readSaid = map[string]string{}, map[string]bool{}
		if p.Said != "" {
			d.Record(now.UTC().Format(time.RFC3339) + " " + p.Said)
		}
	}
	if p.HoldFile != "" {
		raw, err := os.ReadFile(p.HoldFile)
		switch {
		case err == nil && s.gov.Held() == "":
			s.gov.Hold(strings.TrimSpace(string(raw)))
			s.parity.holding = true
			d.Record(fmt.Sprintf("%s lanes held by %s: %s; remove it to bring them up", now.UTC().Format(time.RFC3339), p.HoldFile, oneLine(string(raw), 300)))
		case err != nil && s.parity.holding:
			s.gov.Release()
			s.parity.holding = false
			d.Record(fmt.Sprintf("%s lanes up: %s was removed", now.UTC().Format(time.RFC3339), p.HoldFile))
		}
	}
	for _, ln := range s.lanes {
		t := ln.t
		if t == nil || !t.running || ln.card == nil || t.halted || exists(ln.card.Result()) {
			continue
		}
		if p.StopOnProvider {
			if msg := ProviderFailureLine(t.tail.String()); msg != "" {
				l.parityStop(msg, ln.card.ID, now)
				return
			}
		}
		if p.TokenCap > 0 && p.Usage != nil && ln.baseOK && !t.tokenCapped && now.Sub(ln.capAt) >= CapEvery {
			ln.capAt = now
			l.capTokens(ln, now)
		}
	}
}

// capTokens reads lane ln's card's tokens since it began; at the cap the card's REPORT.md is a
// HOLD naming the cap and the turn is ended, so laneDone finishes the card from that report.
func (l *loop) capTokens(ln *lane, now time.Time) {
	p, d, t, c := l.parity(), l.d, ln.t, *ln.card
	ctx, cancel := context.WithTimeout(l.ctx, ParityWait)
	u, err := p.Usage(ctx, ln.session)
	cancel()
	if err != nil {
		l.readFailed(c, err, now)
		return
	}
	used := u.Since(ln.base)
	if !p.OverCap(used) {
		return
	}
	t.tokenCapped = true
	words := ""
	if !exists(c.Report()) {
		err := os.MkdirAll(c.Outbox, 0o755)
		if err == nil {
			err = atomicfile.WriteFile(c.Report(), []byte(CapReport(d.Friend, c, p.TokenCap, used, LastLines(t.tail.String(), 1))), 0o644)
		}
		if err != nil {
			words = fmt.Sprintf(" report_error=%q", oneLine(err.Error(), 300))
		}
	}
	t.cancel()
	d.Record(fmt.Sprintf("%s lane %d: card %s token cap: %d tokens of a cap of %d; a HOLD naming the cap is its REPORT.md and its process group is signalled%s",
		now.UTC().Format(time.RFC3339), ln.n, c.ID, used.Total(), p.TokenCap, words))
}

func (l *loop) readFailed(c Card, err error, now time.Time) {
	s := l.lanes
	job := filepath.Base(c.Outbox)
	if s.parity.readSaid[job] {
		return
	}
	s.parity.readSaid[job] = true
	l.d.Record(fmt.Sprintf("%s card %s: its tokens cannot be read: %s", now.UTC().Format(time.RFC3339), c.ID, oneLine(err.Error(), 300)))
}

// parityStops says whether a turn's provider limit is the runner's stop: a rate limit or out of
// funds while StopOnProvider is set (a harness limit with its reset stays the governor's pause).
func (l *loop) parityStops(err error) (string, bool) {
	p := l.parity()
	if p == nil || !p.StopOnProvider {
		return "", false
	}
	var rate RateLimited
	var funds OutOfFunds
	switch {
	case errors.As(err, &funds):
		return funds.Reason, true
	case errors.As(err, &rate):
		return rate.Reason, true
	}
	return "", false
}

// parityStop is a provider failure, message its exact words, met on card: every lane's running
// turn is ended (its card kept, laneDone's parityKept), the lanes are held (the hold file keeps
// it across a restart, until a person removes it), and the coordinator is told once, the friend
// down line given: friend down is a coordinator verb the sprint server refuses from a friend.
// A card between turns is kept too. The model is tla/LaneHold.tla: HeldStopsAll and HeldFailsNone
// hold; its reversed witness MCLaneHoldBrokenRunsOn.cfg (the running lanes left going) breaks
// HeldStopsAll.
func (l *loop) parityStop(message, card string, now time.Time) {
	p, s, d := l.parity(), l.lanes, l.d
	at := now.UTC().Format(time.RFC3339)
	stopped := 0
	for _, ln := range s.lanes {
		switch {
		case ln.t != nil && ln.t.running && !ln.t.halted:
			ln.t.halted = true
			ln.t.cancel()
			stopped++
		case ln.t == nil && ln.card != nil: // between turns (handed again): kept now, or a restart while held would finish it failed (tla/LaneHold.tla)
			id := ln.card.ID
			d.Record(fmt.Sprintf("%s lane=%d session=%s card %s kept between turns: a provider failure stopped every lane%s", at, ln.n, ln.session, id, l.keepCard(ln, now)))
		}
	}
	if !s.gov.Hold(message) {
		if stopped > 0 {
			d.Record(fmt.Sprintf("%s provider failure on card %s while the lanes are held: %d running lane(s) stopped, their cards kept", at, card, stopped))
		}
		return
	}
	s.parity.holding = true
	words := ""
	if p.HoldFile != "" {
		err := os.MkdirAll(filepath.Dir(p.HoldFile), 0o755)
		if err == nil {
			err = atomicfile.WriteFile(p.HoldFile, []byte(at+" "+card+": "+message+"\n"), 0o644)
		}
		if err != nil {
			words = fmt.Sprintf(" hold_file_error=%q", oneLine(err.Error(), 300))
		}
	}
	d.Record(fmt.Sprintf("%s provider failure on card %s: %s; every lane stopped (%d running, each card kept to run again, no attempt counted) and held until a person brings them up%s", at, card, oneLine(message, 300), stopped, words))
	subject, body := ProviderStopText(d.Friend, p.Model, message, p.HoldFile)
	l.tellKind(bus.KindBlocker, subject, body, now)
}

// parityKept is the end of a turn a provider stop ended: its messages pending again, its card
// set down unfinished and uncounted (not started, its lane mark removed, never set aside), so it
// runs again once the lanes are up, after a restart as well.
func (l *loop) parityKept(ln *lane, t *turn, why string, now time.Time) {
	d := l.d
	for _, e := range t.entries {
		delete(l.inHand, e)
	}
	if t.notice != nil && l.notice == nil {
		l.notice = t.notice
		l.saidSilent = t.notice.Subject != "coordinator silent"
	}
	words := l.keepCard(ln, now)
	d.Record(fmt.Sprintf("%s lane=%d session=%s subject=%s messages=%d took=%s card=kept reason=%q%s", now.UTC().Format(time.RFC3339), ln.n, ln.session, t.subjects, len(t.entries), now.Sub(t.started).Round(time.Millisecond), why, words))
}

// keepCard sets lane ln's card down kept: not started, its lane mark removed, never set aside,
// its attempts forgotten. It answers words for the record line.
func (l *loop) keepCard(ln *lane, now time.Time) string {
	d, s := l.d, l.lanes
	job := filepath.Base(ln.card.Outbox)
	delete(s.state.Started, job)
	words := ""
	if m, ok := ReadLaneMark(d.Dir, job); ok && !m.Ended {
		if err := os.Remove(laneMarkPath(d.Dir, job)); err != nil {
			words = fmt.Sprintf(" mark_error=%q", oneLine(err.Error(), 300))
		}
	}
	l.saveLanes(now)
	ln.card, ln.attempts = nil, 0
	return words
}

// parityRefuses is the card filter in the lanes' hand: a card outside it is never handed; one
// to be taken back and not started is a blocker to the coordinator, once while it stands, with
// the friend take line (a coordinator verb the sprint server refuses from a friend).
func (l *loop) parityRefuses(c Card, now time.Time) bool {
	p := l.parity()
	if p == nil || (len(p.Tiers) == 0 && len(p.Streams) == 0) {
		return false
	}
	d, s := l.d, l.lanes
	job := filepath.Base(c.Outbox)
	tier := ""
	for _, h := range d.heldCards {
		if heldJob(h) == job {
			tier = h.Tier
		}
	}
	if tier == "" {
		tier = briefTier(c.Brief)
	}
	act, why := p.Filter(c.ID, "", tier) // the row carries no stream yet: the patterns match the id
	if act == FilterRun {
		return false
	}
	if act == FilterTake {
		if _, started := ReadLaneMark(d.Dir, job); started || exists(filepath.Join(d.Dir, "jobs", job, "repo")) {
			act, why = FilterSkip, why+"; started already, so it is not owed back"
		}
	}
	if s.parity.said[job] == string(act) {
		return true
	}
	s.parity.said[job] = string(act)
	d.Record(fmt.Sprintf("%s card %s not handed: %s %s", now.UTC().Format(time.RFC3339), c.ID, act, why))
	if act == FilterTake {
		subject, body := TakeBackText(d.Friend, c.ID, why)
		l.tellKind(bus.KindBlocker, subject, body, now)
	}
	return true
}

// parityWidth is the width the lanes take new cards at: width, held lower while the machine's
// load is above the bound (read once a LoadEvery), each change said on the record and the hold
// told to the coordinator.
func (l *loop) parityWidth(now time.Time, width int) int {
	p := l.parity()
	if p == nil || p.LoadMax <= 0 || p.Load == nil {
		return width
	}
	s, d := l.lanes, l.d
	if s.parity.loadAt.IsZero() || now.Sub(s.parity.loadAt) >= LoadEvery {
		s.parity.loadAt = now
		ctx, cancel := context.WithTimeout(l.ctx, ParityWait)
		load, err := p.Load(ctx)
		cancel()
		if err != nil {
			if s.parity.loadErr != err.Error() {
				s.parity.loadErr = err.Error()
				d.Record(fmt.Sprintf("%s load: cannot be read: %s; the width is not held by it", now.UTC().Format(time.RFC3339), oneLine(err.Error(), 300)))
			}
		} else {
			s.parity.load, s.parity.loadErr = load, ""
		}
	}
	n, held := p.LoadWidthAt(width, s.parity.load)
	switch {
	case held && s.parity.held != n:
		s.parity.held = n
		d.Record(fmt.Sprintf("%s load %.1f above %.1f: lanes held at %d of %d", now.UTC().Format(time.RFC3339), s.parity.load, p.LoadMax, n, width))
		l.tell(fmt.Sprintf("%s lanes at %d: load %.1f", d.Friend, n, s.parity.load), fmt.Sprintf("The 1-minute load %.1f is above %.1f with %s at %d lanes; new cards are held to %d lanes until it falls.\n", s.parity.load, p.LoadMax, d.Friend, width, n), now)
	case !held && s.parity.held != 0:
		s.parity.held = 0
		d.Record(fmt.Sprintf("%s load %.1f at or below %.1f: lanes back to %d", now.UTC().Format(time.RFC3339), s.parity.load, p.LoadMax, width))
	}
	return n
}

// parityBegin is a card handed to lane ln: its session's tokens read as the card's start, the
// cap and the cost counted from there.
func (l *loop) parityBegin(ln *lane, now time.Time) {
	p := l.parity()
	ln.base, ln.baseOK, ln.capAt = TokenUsage{}, false, now
	if p == nil || p.Usage == nil || ln.session == "" {
		return
	}
	ctx, cancel := context.WithTimeout(l.ctx, ParityWait)
	u, err := p.Usage(ctx, ln.session)
	cancel()
	if err != nil {
		l.readFailed(*ln.card, err, now)
		return
	}
	ln.base, ln.baseOK = u, true
}

// parityContext is a lane turn's context: the go refusal shims on its PATH.
func (l *loop) parityContext(ctx context.Context) context.Context {
	if p := l.parity(); p != nil {
		return WithShims(ctx, p.Shims)
	}
	return ctx
}

// parityFinish is a lane's card finished: its tokens since it began priced by the route row and
// published as the Cost: line on REPORT.md and RESULT.md, and a bus note to the coordinator.
// It answers the cost words for the record line.
func (l *loop) parityFinish(ln *lane, c Card, started, now time.Time) string {
	p := l.parity()
	if p == nil {
		return ""
	}
	why := "no reader of opencode's database"
	if p.Usage != nil {
		why = "the session's tokens were not read when the card began"
	}
	if ln.baseOK {
		ctx, cancel := context.WithTimeout(l.ctx, ParityWait)
		u, err := p.Usage(ctx, ln.session)
		cancel()
		if err == nil {
			u, route := u.Since(ln.base), l.route(now)
			return l.published(c, CostLine(p.Model, route, u), CostWords(p.Model, route, u), started, now)
		}
		why = err.Error()
	}
	return l.published(c, UnreadCostLine(p.Model, why), "unpriced (the tokens were not read)", started, now)
}

// published is the cost line on the card's REPORT.md and RESULT.md and the note to the
// coordinator; it answers the cost words for the record line.
func (l *loop) published(c Card, line, cost string, started, now time.Time) string {
	d := l.d
	words := ""
	if err := PublishCost(c, line); err != nil {
		words = fmt.Sprintf(" cost_error=%q", oneLine(err.Error(), 300))
	}
	wall := time.Duration(0)
	if !started.IsZero() {
		wall = now.Sub(started)
	}
	subject, body := FinishNote(d.Friend, filepath.Base(c.Outbox), firstLine(c.Report()), "cost "+cost, wall)
	l.tell(subject, body, now)
	return fmt.Sprintf(" cost=%q%s", cost, words)
}

// route is the store's route row for her model, read from nova-sprint routes --json; none (no
// reader, no row, an error) is the empty row, which prices nothing.
func (l *loop) route(now time.Time) RouteRow {
	p := l.parity()
	if p.Routes == nil {
		return RouteRow{}
	}
	ctx, cancel := context.WithTimeout(l.ctx, ParityWait)
	raw, err := p.Routes(ctx)
	cancel()
	if err == nil {
		var r RouteRow
		r, _, err = RouteOf(raw, p.Model)
		if err == nil {
			return r
		}
	}
	l.d.Record(fmt.Sprintf("%s routes: %s; the card is unpriced", now.UTC().Format(time.RFC3339), oneLine(err.Error(), 300)))
	return RouteRow{}
}
