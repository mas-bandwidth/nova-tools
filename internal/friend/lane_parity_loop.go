package friend

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// The lanes' parity with the runner stopgaps, in the loop (lane_parity.go has the
// functions, each tested alone): the row's filter and the take back, the width under
// load, the token cap, the provider hold, the cost at a finish and its note.

// VerbWait bounds a verb sent to the sprint server.
const VerbWait = 10 * time.Second

func (l *loop) laneRow() LaneRow {
	if l.d.LaneRow == nil {
		return LaneRow{}
	}
	return l.d.LaneRow()
}

// loadWidth is the width held to the row's load width while the machine's load is above
// the row's bound, said once on each change.
func (l *loop) loadWidth(row LaneRow, now time.Time, width int) int {
	if l.d.Load == nil {
		return width
	}
	load := l.d.Load()
	n, held := row.LaneWidth(width, load)
	if held != l.lanes.loadHeld {
		l.lanes.loadHeld = held
		at := now.UTC().Format(time.RFC3339)
		if held {
			l.d.Record(fmt.Sprintf("%s load %.0f above %.0f: lanes held at %d of %d", at, load, row.LoadMax, n, width))
			l.tell(fmt.Sprintf("friend %s lanes at %d: load %.0f", l.d.Friend, n, load), fmt.Sprintf("1-minute load %.0f is above %.0f with %s at %d lanes; new lanes are held to %d until it falls\n", load, row.LoadMax, l.d.Friend, width, n), now)
		} else {
			l.d.Record(fmt.Sprintf("%s load %.0f at or below %.0f: lanes back to %d", at, load, row.LoadMax, width))
		}
	}
	return n
}

func (l *loop) heldCard(id string) (HeldCard, bool) {
	for _, h := range l.d.heldCards {
		if h.Card == id {
			return h, true
		}
	}
	return HeldCard{}, false
}

// filtered says whether the row's filter leaves card c alone (a card that is not hers).
func (l *loop) filtered(row LaneRow, c Card) bool {
	h, _ := l.heldCard(c.ID)
	act, _ := row.Filter(c.ID, h.Stream, h.Tier)
	return act != FilterRun
}

// takeBack takes every dealt card the row's filter takes back and that was not started
// (no inbox directory's job dir under jobs/, no report) back for the dealer, once each.
func (l *loop) takeBack(row LaneRow, now time.Time) {
	d, s := l.d, l.lanes
	if d.Verb == nil || (len(row.Tiers) == 0 && len(row.Streams) == 0) {
		return
	}
	for _, h := range d.heldCards {
		if h.Col != "working" || !validJob(h.Job) || s.took[h.Job] {
			continue
		}
		act, why := row.Filter(h.Card, h.Stream, h.Tier)
		if act != FilterTake || exists(filepath.Join(d.Dir, "jobs", h.Job)) || exists(filepath.Join(d.Dir, "outbox", h.Job, "REPORT.md")) {
			continue
		}
		if s.took == nil {
			s.took = map[string]bool{}
		}
		s.took[h.Job] = true
		ctx, cancel := context.WithTimeout(context.WithoutCancel(l.ctx), VerbWait)
		err := d.Verb(ctx, TakeArgv(d.Friend, h.Card, why))
		cancel()
		if err != nil {
			d.Record(fmt.Sprintf("%s take %s refused: %s", now.UTC().Format(time.RFC3339), h.Card, oneLine(err.Error(), 300)))
			continue
		}
		d.Record(fmt.Sprintf("%s take %s back: %s", now.UTC().Format(time.RFC3339), h.Card, why))
	}
}

func (l *loop) usage(session string) (TokenUsage, bool) {
	if l.d.Usage == nil || session == "" || !l.laneRow().Set {
		return TokenUsage{}, false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(l.ctx), VerbWait)
	defer cancel()
	u, err := l.d.Usage(ctx, session)
	return u, err == nil
}

// cardUsage is what the card has used in its lane's session since it began.
func (l *loop) cardUsage(c Card) (TokenUsage, bool) {
	return l.usageSince(l.lanes.state.Started[filepath.Base(c.Outbox)])
}

func (l *loop) usageSince(st Started) (TokenUsage, bool) {
	u, ok := l.usage(st.Session)
	return u.Sub(st.Base), ok
}

// capCheck stops the lane whose card has used the row's token cap: a HOLD REPORT.md
// naming the cap is written, the turn is cancelled, and the lane's end finishes the card.
func (l *loop) capCheck(row LaneRow, now time.Time) {
	s := l.lanes
	if row.TokenCap <= 0 || now.Sub(s.capAt) < CapEvery {
		return
	}
	s.capAt = now
	for _, ln := range s.lanes {
		if ln.t == nil || ln.card == nil || ln.t.capped || exists(ln.card.Report()) {
			continue
		}
		u, ok := l.cardUsage(*ln.card)
		if !ok || !row.OverCap(u) {
			continue
		}
		ln.t.capped = true
		if err := WriteCapReport(*ln.card, l.d.Friend, row.TokenCap, u, "see the lane's record"); err != nil {
			l.d.Record(fmt.Sprintf("%s token cap %s: the report cannot be written: %s", now.UTC().Format(time.RFC3339), ln.card.ID, err.Error()))
			continue
		}
		l.d.Record(fmt.Sprintf("%s TOKEN CAP %s: %d tokens at the cap of %d; lane %d stopped", now.UTC().Format(time.RFC3339), ln.card.ID, u.Total(), row.TokenCap, ln.n))
		ln.t.cancel()
	}
}

// holdDown is out of funds or a provider failure that held the lanes: every running turn
// is stopped (its card stays in hand), and the friend is held down with the exact message
// until a person brings her up.
func (l *loop) holdDown(message string, now time.Time) {
	d := l.d
	for _, ln := range l.lanes.lanes {
		if ln.t != nil && ln.t.running {
			ln.t.held = true
			ln.t.cancel()
		}
	}
	if d.Verb == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(l.ctx), VerbWait)
	defer cancel()
	if err := d.Verb(ctx, DownArgv(d.Friend, l.laneRow().Model, message)); err != nil {
		d.Record(fmt.Sprintf("%s friend down refused: %s", now.UTC().Format(time.RFC3339), oneLine(err.Error(), 300)))
	}
}

// finishParity is a card finished in a lane: its cost published on REPORT.md and
// RESULT.md, and the one note to the coordinator.
func (l *loop) finishParity(c Card, st Started, now time.Time) string {
	d := l.d
	row := l.laneRow()
	if !row.Set {
		return ""
	}
	cost := "unmeasured"
	if u, ok := l.usageSince(st); ok {
		cost = row.Route.Cost(row.Model, u)
		if err := PublishCost(c, row.Model, row.Route, u); err != nil {
			d.Record(fmt.Sprintf("%s cost %s: not published: %s", now.UTC().Format(time.RFC3339), c.ID, err.Error()))
		}
	}
	verdict := ""
	if raw, err := readFirstLine(c.Report()); err == nil {
		verdict = raw
	}
	subject, body := FinishNote(d.Friend, c, verdict, cost, now.Sub(st.At))
	l.tell(subject, body, now)
	return " cost=" + strings.ReplaceAll(cost, " ", "_")
}
