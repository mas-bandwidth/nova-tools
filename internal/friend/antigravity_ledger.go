package friend

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
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

// Delivery states for durable ledger tracking.
const (
	DeliveryPending   = "pending"   // recorded before external send
	DeliveryUncertain = "uncertain" // send timed out or had ambiguous error waiting for receipt
	DeliveryLanded    = "landed"    // message file confirmed in mailbox (ID != "")
	DeliveryConsumed  = "consumed"  // message read by conversation (ReadAt != zero)
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
	Hash         string    `json:"hash,omitempty"`
	BusIDs       []string  `json:"bus_ids,omitempty"`
	State        string    `json:"state,omitempty"`
}

var (
	busRecvIDRegex = regexp.MustCompile(`(?m)^(?:RECV OK id=|=== message \d+ of \d+: id=|\[\d+/\d+\]\s+)([0-9A-Za-z_-]+)`)
	busResentRegex = regexp.MustCompile(`(?m)^re-sent:\s+([0-9A-Za-z_-]+)\s+was delivered`)
	busNonceRegex  = regexp.MustCompile(`(?m)^(?:SESSION CHECK|PING)\s+([0-9A-Za-z_-]+)`)
	busPongRegex   = regexp.MustCompile(`--nonce\s+([0-9A-Za-z_-]+)`)
)

// extractDeliveryIDs extracts stable incoming bus message IDs, nonces, or turns from text.
func extractDeliveryIDs(text string) []string {
	var ids []string
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, m := range busRecvIDRegex.FindAllStringSubmatch(text, -1) {
		add(m[1])
	}
	for _, m := range busResentRegex.FindAllStringSubmatch(text, -1) {
		add(m[1])
	}
	for _, m := range busNonceRegex.FindAllStringSubmatch(text, -1) {
		add(m[1])
	}
	for _, m := range busPongRegex.FindAllStringSubmatch(text, -1) {
		add(m[1])
	}
	return ids
}

// antigravityHash computes a stable sha256 hex string for the delivery text.
func antigravityHash(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

// coversAll checks whether delivery d contains every ID in incomingIDs.
func (d *AntigravityDelivery) coversAll(incomingIDs []string) bool {
	if len(incomingIDs) == 0 || len(d.BusIDs) == 0 {
		return false
	}
	for _, inID := range incomingIDs {
		if !slices.Contains(d.BusIDs, inID) {
			return false
		}
	}
	return true
}

// overlapsAny checks whether delivery d contains at least one ID in incomingIDs.
func (d *AntigravityDelivery) overlapsAny(incomingIDs []string) bool {
	if len(incomingIDs) == 0 || len(d.BusIDs) == 0 {
		return false
	}
	for _, inID := range incomingIDs {
		if slices.Contains(d.BusIDs, inID) {
			return true
		}
	}
	return false
}

// AntigravityLedger is the ledger's file: the conversation delivery follows (Followed, moved
// to while the named session was From, at Since) and every delivery, oldest first.
type AntigravityLedger struct {
	Followed   string                `json:"followed,omitempty"`
	From       string                `json:"from,omitempty"`
	Since      time.Time             `json:"since,omitzero"`
	Deliveries []AntigravityDelivery `json:"deliveries"`
}

// loadErr reads the ledger once; called with mu held.
// If the ledger file is unreadable (e.g. corruption, permission error), it returns
// the error without caching an empty struct, allowing callers to fail closed.
func (a *Antigravity) loadErr() (*AntigravityLedger, error) {
	if a.ledger != nil {
		return a.ledger, nil
	}
	if a.State == "" {
		a.ledger = &AntigravityLedger{}
		return a.ledger, nil
	}
	l := &AntigravityLedger{}
	found, err := read(filepath.Join(a.State, AntigravityLedgerFile), l)
	if err != nil {
		return nil, err
	}
	if !found {
		a.ledger = &AntigravityLedger{}
		return a.ledger, nil
	}
	a.ledger = l
	return a.ledger, nil
}

// load reads the ledger once; called with mu held. A ledger that cannot be read starts
// empty, said once.
func (a *Antigravity) load() *AntigravityLedger {
	l, err := a.loadErr()
	if err != nil {
		a.say("antigravity: the ledger cannot be read: %s", oneLine(err.Error(), 300))
		return &AntigravityLedger{}
	}
	return l
}

// save writes the ledger, the read and sent-again deliveries past AntigravityKeptRead
// dropped (the newest kept); called with mu held.
func (a *Antigravity) save() error {
	l, err := a.loadErr()
	if err != nil {
		return fmt.Errorf("ledger unreadable: %w", err)
	}
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
		return nil
	}
	if err := write(filepath.Join(a.State, AntigravityLedgerFile), l); err != nil {
		a.say("antigravity: the ledger cannot be written, kept in memory: %s", oneLine(err.Error(), 300))
		return err
	}
	return nil
}

// keep adds a delivery to the ledger and persists it. If saving fails, the delivery is
// removed so memory does not diverge from disk, and the persistence error is returned.
func (a *Antigravity) keep(d AntigravityDelivery) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	l, err := a.loadErr()
	if err != nil {
		return fmt.Errorf("ledger unreadable: %w", err)
	}
	l.Deliveries = append(l.Deliveries, d)
	a.live = d.Conversation
	if err := a.save(); err != nil {
		l.Deliveries = l.Deliveries[:len(l.Deliveries)-1]
		return err
	}
	return nil
}

// known says whether the ledger holds message id.
func (a *Antigravity) known(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.ContainsFunc(a.load().Deliveries, func(d AntigravityDelivery) bool { return d.ID == id })
}

// Delivered reports whether the ledger holds bus message id in an already landed or consumed delivery.
// If reading the ledger encounters an error, it returns that error to fail closed.
func (a *Antigravity) Delivered(id string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	l, err := a.loadErr()
	if err != nil {
		return false, err
	}
	for _, d := range l.Deliveries {
		if (d.State == DeliveryConsumed || d.State == DeliveryLanded || !d.ReadAt.IsZero()) && slices.Contains(d.BusIDs, id) {
			return true, nil
		}
	}
	return false, nil
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
	if a.Session != "" {
		return a.Session
	}
	l := a.load()
	if len(l.Deliveries) > 0 {
		return l.Deliveries[len(l.Deliveries)-1].Conversation
	}
	return ""
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
				d.State = DeliveryLanded
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
			d.State = DeliveryConsumed
			a.say("antigravity: message %s read by conversation %s", d.ID, d.Conversation)
		}
	}
	if changed {
		if err := a.save(); err != nil {
			a.say("antigravity: cannot save consumed state to ledger: %s", oneLine(err.Error(), 300))
		}
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
	l := a.load()
	for _, d := range l.Deliveries {
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
	l := a.load()
	for _, d := range l.Deliveries {
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
	l := a.load()
	u := a.unread(c)
	if len(u) == 0 || now.Sub(u[0].DeliveredAt) < AntigravityReadBound {
		return ""
	}
	for _, d := range l.Deliveries {
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
		if err := a.save(); err != nil {
			a.say("antigravity: cannot save live conversation to ledger: %s", oneLine(err.Error(), 300))
		}
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
	} else if live != "" {
		mailbox := antigravityMailbox(live)
		inBox, err := a.mailbox(mailbox)
		if err != nil {
			a.say("antigravity: cannot list mailbox of conversation %s: %s; replay held until mailbox can be read", live, oneLine(err.Error(), 300))
		} else {
			for _, d := range l.Deliveries {
				if d.Conversation == live && d.ReadAt.IsZero() && d.ResentTo == "" && d.Text != "" {
					if d.ID != "" && !slices.Contains(inBox, d.ID) {
						owed = append(owed, d)
					}
				}
			}
			to = live
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
		if _, err := a.send(ctx, srv, to, ResentLine(d)+d.Text, d.BusIDs, len(d.BusIDs) > 0); err != nil {
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
		if err := a.save(); err != nil {
			a.say("antigravity: cannot save resent delivery to ledger: %s", oneLine(err.Error(), 300))
		}
		a.mu.Unlock()
		a.say("antigravity: message %s, unread in conversation %s, sent again into conversation %s", dash(d.ID), d.Conversation, to)
	}
}

// antigravityMailbox is conversation c's mailbox, under the home.
// reconcile checks if an existing delivery for session and text is already pending, landed,
// or consumed. It reconciles without duplicate send, or holds an ambiguous/pending send visibly.
func (a *Antigravity) reconcile(session, text string, incomingIDs []string, hasStructuredContext bool) (reconciled bool, exit int, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	l := a.load()
	mailbox := antigravityMailbox(session)
	inBox, _ := a.mailbox(mailbox)

	hasExplicitEmptyIDs := hasStructuredContext && len(incomingIDs) == 0

	for i := len(l.Deliveries) - 1; i >= 0; i-- {
		d := &l.Deliveries[i]
		if d.Conversation != session || d.ResentTo != "" {
			continue
		}

		// When explicit empty structured IDs are present (absence of identity),
		// content hash must NOT match an existing completed delivery to suppress a second
		// intentional identical send.
		hasHashMatch := (!hasExplicitEmptyIDs && d.Hash != "" && d.Hash == antigravityHash(text))
		hasFullCoverage := len(incomingIDs) > 0 && d.coversAll(incomingIDs)
		hasPartialOverlap := len(incomingIDs) > 0 && d.overlapsAny(incomingIDs)

		// For pending/uncertain deliveries in-flight: if identical text is currently pending,
		// hold visibly to prevent duplicate in-flight sends even if no IDs.
		inFlightHashMatch := (d.Hash != "" && d.Hash == antigravityHash(text))

		if !hasHashMatch && !hasFullCoverage && !hasPartialOverlap && !(inFlightHashMatch && (d.ID == "" || d.State == DeliveryPending || d.State == DeliveryUncertain)) {
			continue
		}

		// Case 1: Pending or uncertain send
		if d.ID == "" || d.State == DeliveryPending || d.State == DeliveryUncertain {
			ids := a.untracked(session)
			if len(ids) > 0 {
				d.ID = ids[0]
				d.State = DeliveryLanded
				if err := a.save(); err != nil {
					a.say("antigravity: cannot save reconciled landing to ledger: %s", oneLine(err.Error(), 300))
				}
				a.say("antigravity: message %s in the mailbox of conversation %s (reconciled late landing)", d.ID, session)
				if hasHashMatch || hasFullCoverage {
					return true, 0, nil
				}
				// Landed, but only partial overlap with incoming turn: cannot acknowledge new unseen IDs.
				continue
			}

			// File has not landed yet.
			// If full coverage or hash match: hold visibly rather than duplicate send.
			if hasHashMatch || hasFullCoverage || inFlightHashMatch {
				desc := d.Hash
				if len(d.BusIDs) > 0 {
					desc = strings.Join(d.BusIDs, ",")
				}
				a.say("antigravity: delivery for conversation %s is pending/uncertain (%s); holding send visibly rather than duplicate send", session, desc)
				exit, err := a.refuse(session, fmt.Sprintf("message delivery for conversation %s is already pending/uncertain; waiting for mailbox receipt rather than duplicate send", session))
				return true, exit, err
			}

			// Partial overlap with in-flight pending/uncertain delivery:
			// Full incoming batch contains some IDs that are pending/uncertain in-flight, AND some new IDs.
			// Holding visibly prevents duplicate sends of in-flight messages and prevents premature ACK of new messages.
			a.say("antigravity: delivery for conversation %s partially overlaps pending/uncertain delivery %s; holding send visibly", session, d.Hash)
			exit, err := a.refuse(session, fmt.Sprintf("incoming messages partially overlap pending/uncertain delivery for conversation %s; waiting for in-flight receipt", session))
			return true, exit, err
		}

		// Case 2: Already consumed or landed delivery
		// Full coverage is required before returning exit 0 (acknowledgment).
		if hasHashMatch || hasFullCoverage {
			if !d.ReadAt.IsZero() || d.State == DeliveryConsumed {
				a.say("antigravity: message %s already consumed by conversation %s; reconciled without duplicate send", dash(d.ID), session)
				return true, 0, nil
			}
			if d.ID != "" && slices.Contains(inBox, d.ID) {
				a.say("antigravity: message %s already in the mailbox of conversation %s from prior accepted send; reconciled without duplicate send", dash(d.ID), session)
				return true, 0, nil
			}
		}
	}
	return false, 0, nil
}

func (a *Antigravity) markLanded(session, hash, id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	l := a.load()
	for i := len(l.Deliveries) - 1; i >= 0; i-- {
		d := &l.Deliveries[i]
		if d.Conversation == session && (d.Hash == hash || (hash == "" && d.ID == id)) {
			d.ID = id
			d.State = DeliveryLanded
			return a.save()
		}
	}
	return nil
}

func (a *Antigravity) markUncertain(session, hash string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	l := a.load()
	for i := len(l.Deliveries) - 1; i >= 0; i-- {
		d := &l.Deliveries[i]
		if d.Conversation == session && d.Hash == hash {
			d.State = DeliveryUncertain
			return a.save()
		}
	}
	return nil
}

func antigravityMailbox(c string) string {
	return path.Join(AntigravityData, "brain", c, ".system_generated", "messages")
}
