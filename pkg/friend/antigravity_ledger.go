package friend

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"time"
)

// The Antigravity ledger: every delivery into a conversation's mailbox, kept in the
// daemon's state directory (AntigravityLedgerFile) until it is read or sent again, so a
// message the bus acked is never lost (the finding of 2026-10-06: a delivery answered 0 once
// it landed, the bus acked it, and a conversation that stopped reading kept it unread for
// ever). Follow reads who read what, says each read once, and moves delivery to the
// conversation that reads, sending again into it every delivery the old one left unread;
// Deliver refuses while the live conversation has read nothing delivered for the check
// period, so the messages stay pending on the bus until it reads again.

// AntigravityLedgerFile is the ledger's file in the daemon's state directory.
const AntigravityLedgerFile = "antigravity-ledger.json"

// The ledger's rules. AntigravityReadBound is the check period: a delivery unread past it,
// with nothing read since, is a session down (Deliver refuses). A conversation that has left
// AntigravityStopped deliveries unread past it, and read nothing since the oldest, stopped
// reading; delivery moves to a conversation that read a delivery since, and never moves
// again within AntigravitySwitchHold. Follow looks every AntigravityFollowEvery.
// AntigravityKeptRead bounds the deliveries kept once read or sent again (the newest);
// one unread is never dropped.
const (
	AntigravityFollowEvery = 10 * time.Second
	AntigravityReadBound   = SessionBound
	AntigravityStopped     = 3
	AntigravitySwitchHold  = 10 * time.Minute
	AntigravityKeptRead    = 64
)

// AntigravityDelivery is one delivery: its message id in the conversation's mailbox ("" until
// it lands), when it went in and when the daemon saw it read, the conversation it was sent
// again into, and its text while it may be sent again.
type AntigravityDelivery struct {
	ID           string    `json:"id"`
	Conversation string    `json:"conversation"`
	DeliveredAt  time.Time `json:"delivered_at"`
	ReadAt       time.Time `json:"read_at,omitzero"`
	ResentTo     string    `json:"resent_to,omitempty"`
	Text         string    `json:"text,omitempty"`
}

// AntigravityLedger is the ledger's file: the conversation delivery follows (Followed, moved
// to while the named session was From, at Since) and every delivery, oldest first.
type AntigravityLedger struct {
	Followed   string                `json:"followed,omitempty"`
	From       string                `json:"from,omitempty"`
	Since      time.Time             `json:"since,omitzero"`
	Deliveries []AntigravityDelivery `json:"deliveries"`
}

// load reads the ledger once; called with mu held. A ledger that cannot be read starts
// empty, said once.
func (a *Antigravity) load() *AntigravityLedger {
	if a.ledger != nil {
		return a.ledger
	}
	a.ledger = &AntigravityLedger{}
	if a.State != "" {
		if _, err := read(filepath.Join(a.State, AntigravityLedgerFile), a.ledger); err != nil {
			a.ledger = &AntigravityLedger{}
			a.say("antigravity: the ledger cannot be read, starting empty: %s", oneLine(err.Error(), 300))
		}
	}
	return a.ledger
}

// save writes the ledger, the read and sent-again deliveries past AntigravityKeptRead
// dropped (the newest kept); called with mu held. A ledger that cannot be written is said,
// and kept in memory.
func (a *Antigravity) save() {
	l := a.load()
	done := 0
	for _, d := range l.Deliveries {
		if d.ReadAt != (time.Time{}) || d.ResentTo != "" {
			done++
		}
	}
	l.Deliveries = slices.DeleteFunc(l.Deliveries, func(d AntigravityDelivery) bool {
		if done > AntigravityKeptRead && (!d.ReadAt.IsZero() || d.ResentTo != "") {
			done--
			return true
		}
		return false
	})
	if a.State == "" {
		return
	}
	if err := write(filepath.Join(a.State, AntigravityLedgerFile), l); err != nil {
		a.say("antigravity: the ledger cannot be written, kept in memory: %s", oneLine(err.Error(), 300))
	}
}

// keep adds a delivery to the ledger.
func (a *Antigravity) keep(d AntigravityDelivery) {
	a.mu.Lock()
	defer a.mu.Unlock()
	l := a.load()
	l.Deliveries = append(l.Deliveries, d)
	a.live = d.Conversation
	a.save()
}

// known says whether the ledger holds message id.
func (a *Antigravity) known(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.ContainsFunc(a.load().Deliveries, func(d AntigravityDelivery) bool { return d.ID == id })
}

// following is the conversation delivery was moved to, while the named session is the one
// it was moved from; "" when none.
func (a *Antigravity) following() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	l := a.load()
	if l.Followed != "" && l.From == a.Session {
		return l.Followed
	}
	return ""
}

// Live is the conversation deliveries go to: the one delivery was moved to, else the one the
// last delivery went to, else the one named by Session; "" before the first delivery when
// none is named.
func (a *Antigravity) Live() string {
	if f := a.following(); f != "" {
		return f
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.live != "" {
		return a.live
	}
	return a.Session
}

// observe reads, for every delivery not yet read, its conversation's read.json, marks the
// ones read at now (each said once), and gives an id to a delivery whose message landed late
// (the conversation's titled messages the ledger does not hold, in order).
func (a *Antigravity) observe(now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	l := a.load()
	reads := map[string][]byte{}
	landed := map[string][]string{}
	changed := false
	for i := range l.Deliveries {
		d := &l.Deliveries[i]
		if !d.ReadAt.IsZero() || d.ResentTo != "" {
			continue
		}
		if d.ID == "" {
			ids, ok := landed[d.Conversation]
			if !ok {
				ids = a.untracked(d.Conversation)
			}
			if len(ids) > 0 {
				d.ID, ids, changed = ids[0], ids[1:], true
				a.say("antigravity: message %s in the mailbox of conversation %s (landed late)", d.ID, d.Conversation)
			}
			landed[d.Conversation] = ids
			if d.ID == "" {
				continue
			}
		}
		raw, ok := reads[d.Conversation]
		if !ok {
			raw, _ = fs.ReadFile(a.fsys(), path.Join(antigravityMailbox(d.Conversation), "read.json")) // ignored: a read.json not there marks nothing read
			reads[d.Conversation] = raw
		}
		if Read(raw, d.ID) {
			d.ReadAt, d.Text, changed = now, "", true
			a.say("antigravity: message %s read by conversation %s", d.ID, d.Conversation)
		}
	}
	if changed {
		a.save()
	}
}

// untracked is conversation c's titled messages that no delivery in the ledger holds, in the
// mailbox's order; called with mu held.
func (a *Antigravity) untracked(c string) []string {
	mailbox := antigravityMailbox(c)
	ids, err := a.mailbox(mailbox)
	if err != nil {
		return nil
	}
	var out []string
	for _, id := range ids {
		if slices.ContainsFunc(a.ledger.Deliveries, func(d AntigravityDelivery) bool { return d.ID == id }) {
			continue
		}
		if mine, _ := a.titled(mailbox, id); mine { // ignored: a message that cannot be read is not ours to claim
			out = append(out, id)
		}
	}
	return out
}

// lastRead is when the daemon last saw conversation c read a delivery; called with mu held.
func (a *Antigravity) lastRead(c string) time.Time {
	var t time.Time
	for _, d := range a.ledger.Deliveries {
		if d.Conversation == c && d.ReadAt.After(t) {
			t = d.ReadAt
		}
	}
	return t
}

// unread is conversation c's deliveries not read and not sent again, oldest first; called
// with mu held.
func (a *Antigravity) unread(c string) []AntigravityDelivery {
	var out []AntigravityDelivery
	for _, d := range a.ledger.Deliveries {
		if d.Conversation == c && d.ReadAt.IsZero() && d.ResentTo == "" {
			out = append(out, d)
		}
	}
	return out
}

// down is why conversation c's session is down at now: its oldest unread delivery is past
// AntigravityReadBound and nothing delivered has been read since; "" while it reads.
func (a *Antigravity) down(c string, now time.Time) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.load()
	u := a.unread(c)
	if len(u) == 0 || now.Sub(u[0].DeliveredAt) < AntigravityReadBound {
		return ""
	}
	for _, d := range a.ledger.Deliveries {
		if d.ReadAt.After(u[0].DeliveredAt) {
			return ""
		}
	}
	return fmt.Sprintf("the session is down: conversation %s has read nothing delivered since %s (%d unread, the check period is %s)", c, u[0].DeliveredAt.UTC().Format(time.RFC3339), len(u), AntigravityReadBound)
}

// ResentLine heads a delivery sent again into the conversation that reads.
func ResentLine(d AntigravityDelivery) string {
	return fmt.Sprintf("re-sent: %s was delivered to %s at %s and not read\n", dash(d.ID), d.Conversation, d.DeliveredAt.UTC().Format(time.RFC3339))
}

// Follow reads, at most every AntigravityFollowEvery, who has read the deliveries (observe),
// and moves delivery to the conversation that reads: when the live conversation has left
// AntigravityStopped deliveries unread past AntigravityReadBound and read nothing since the
// oldest of them, a conversation the daemon delivered to that read one of those deliveries
// since then is the live one (the one that read last), said once ("antigravity: live
// conversation is now <id> (the named one stopped reading)"), and never moved again within
// AntigravitySwitchHold. Every delivery the old conversation left unread is sent again into
// the new one, headed by ResentLine; one whose send fails is sent again at the next look.
func (a *Antigravity) Follow(ctx context.Context, now time.Time) {
	a.mu.Lock()
	if !a.looked.IsZero() && now.Sub(a.looked) < AntigravityFollowEvery {
		a.mu.Unlock()
		return
	}
	a.looked = now
	a.mu.Unlock()
	a.observe(now)
	live := a.Live()
	a.mu.Lock()
	l := a.load()
	lag := a.unread(live)
	moved := ""
	if len(lag) >= AntigravityStopped && now.Sub(lag[AntigravityStopped-1].DeliveredAt) >= AntigravityReadBound &&
		(l.Since.IsZero() || now.Sub(l.Since) >= AntigravitySwitchHold) && !a.lastRead(live).After(lag[0].DeliveredAt) {
		var best time.Time
		for _, d := range l.Deliveries {
			if d.Conversation != live && d.ReadAt.After(lag[0].DeliveredAt) && d.ReadAt.After(best) {
				moved, best = d.Conversation, d.ReadAt
			}
		}
	}
	if moved != "" {
		l.Followed, l.From, l.Since = moved, a.Session, now
		a.live = moved
		a.save()
		a.say("antigravity: live conversation is now %s (the named one stopped reading)", moved)
	}
	to := ""
	if l.Followed != "" && l.From == a.Session {
		to = l.Followed
	}
	var owed []AntigravityDelivery
	if to != "" {
		for _, d := range l.Deliveries {
			if d.Conversation != to && d.ReadAt.IsZero() && d.ResentTo == "" && d.Text != "" {
				owed = append(owed, d)
			}
		}
	}
	a.mu.Unlock()
	if len(owed) == 0 {
		return
	}
	srv, _, err := a.server(ctx)
	if err != nil {
		a.say("antigravity: %d deliveries owed to conversation %s not sent again: %s; sent again at the next look", len(owed), to, oneLine(err.Error(), 300))
		return
	}
	for _, d := range owed {
		if _, err := a.send(ctx, srv, to, ResentLine(d)+d.Text); err != nil {
			a.say("antigravity: message %s not sent again into conversation %s: %s; sent again at the next look", dash(d.ID), to, oneLine(err.Error(), 300))
			return
		}
		a.mu.Lock()
		for i := range a.ledger.Deliveries {
			e := &a.ledger.Deliveries[i]
			if e.Conversation == d.Conversation && e.DeliveredAt.Equal(d.DeliveredAt) && e.ID == d.ID && e.ResentTo == "" {
				e.ResentTo, e.Text = to, ""
				break
			}
		}
		a.save()
		a.mu.Unlock()
		a.say("antigravity: message %s, unread in conversation %s, sent again into conversation %s", dash(d.ID), d.Conversation, to)
	}
}

// antigravityMailbox is conversation c's mailbox, under the home.
func antigravityMailbox(c string) string {
	return path.Join(AntigravityData, "brain", c, ".system_generated", "messages")
}
