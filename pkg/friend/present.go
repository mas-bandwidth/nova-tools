package friend

import (
	"cmp"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/bus"
)

// The present (the owner, 2026-10-06: "when somebody starts up, you need to tell them to skip to
// present... this should be automatic"). A session that starts, or comes back after a gap, was
// handed the backlog its stream had kept (deferred=892 on one daemon): old deals, old pings and
// old coordinator notes read as current, cards worked that had been taken back, nonces answered
// that had expired. Now, on a session start and after any gap longer than StaleAfter, the daemon
// delivers one PRESENT turn and nothing older: her live queue, the newest coordinator note to her,
// the seat's name, and one line saying what was skipped. Every older message on her stream is
// acked with the reason "superseded by the present at <time>", said on the record and, in one
// message to the seat, on the bus log. The model is tla/Delivery.tla (docs/SPEC-FRIEND.md, the
// present comes first).

// StaleAfter is the stale bound: a delivery after this long with none is a present.
const StaleAfter = 30 * time.Minute

// PresentSubject is the subject of a friend's own request for the present: a message from her
// to herself with this subject (nova-bus send --as <me> --to <me> --subject present).
const PresentSubject = "present"

// PresentTextRule heads a present's text, so a session and a test know it at a glance.
const PresentTextRule = "nova-friend: PRESENT"

// Skipped is what one present superseded, by kind: the deals (card ... dealt), the pings
// (PING and SESSION CHECK whose nonce is past the challenge window), and every other note.
type Skipped struct {
	Deals, Pings, Notes int
}

// Total is every message the present superseded.
func (s Skipped) Total() int { return s.Deals + s.Pings + s.Notes }

// PresentPlan is the present's decision over the backlog: the entries acked superseded, the
// newest coordinator note carried in the turn (nil: none), the pings still inside the challenge
// window (answered by the daemon as any ping, never superseded), and the counts.
type PresentPlan struct {
	Superseded []string
	Note       *bus.Entry
	Fresh      []bus.Entry
	Requests   int // the friend's own present requests, acked with the rest and counted as nothing
	Skipped    Skipped
}

// IsPresentRequest says m is the friend's own request for the present.
func IsPresentRequest(friend string, m bus.Message) bool {
	return m.From == friend && strings.EqualFold(strings.TrimSpace(m.Subject), PresentSubject)
}

// isDeal says m is a deal: the sprint's "card <id> dealt: ..." (nova-sprint friendcards).
func isDeal(m bus.Message) bool {
	return strings.HasPrefix(m.Subject, "card ") && strings.Contains(m.Subject, " dealt")
}

// isChallenge says m asks the session for a nonce: a PING or a SESSION CHECK.
func isChallenge(m bus.Message) bool {
	_, _, _, ping := ParsePing(m.Body)
	return ping || strings.HasPrefix(m.Subject, SessionCheckPrefix)
}

// StaleNonce says a challenge sent at sent is past the window at storeNow, both the store's
// clock: dropped, never answered.
func StaleNonce(sent, storeNow time.Time, window time.Duration) bool {
	return storeNow.Sub(sent) >= window
}

// PlanPresent is the present's decision over backlog, every message waiting on her stream,
// oldest first, at the store's time storeNow, with seat the coordinator (the seat holder, else
// who she reports to; "" unknown). A PING inside window is fresh, answered by the daemon and
// kept off the skipped line; a SESSION CHECK is never fresh (a run before this one asked it,
// and its nonce answers nothing this daemon asked). Of the rest the newest from seat that is no
// deal is the note the present carries; everything else is superseded.
func PlanPresent(friend, seat string, backlog []bus.Entry, storeNow time.Time, window time.Duration) PresentPlan {
	var p PresentPlan
	note := -1
	if seat != "" {
		for i := len(backlog) - 1; i >= 0; i-- {
			m := backlog[i].Message()
			if backlog[i].Stage != bus.Acted && m.From == seat && !isDeal(m) && !isChallenge(m) && !IsPresentRequest(friend, m) {
				note = i
				break
			}
		}
	}
	for i, e := range backlog {
		m := e.Message()
		_, _, _, ping := ParsePing(m.Body)
		switch {
		case i == note:
			p.Note = &backlog[i]
			continue
		case e.Stage != bus.Acted && ping && !StaleNonce(m.At, storeNow, window):
			p.Fresh = append(p.Fresh, e)
			continue
		case IsPresentRequest(friend, m):
			p.Requests++
		case isChallenge(m):
			p.Skipped.Pings++
		case isDeal(m):
			p.Skipped.Deals++
		default:
			p.Skipped.Notes++
		}
		p.Superseded = append(p.Superseded, e.Entry)
	}
	return p
}

// QueueLine is one card of her live queue as the present names it.
type QueueLine struct {
	Card, Col, Brief string
}

// LiveQueue is her live queue: her row as the server last said it (each card, its column and
// its inbox/<job>/BRIEF.md), else the queued and working tasks of inbox/QUEUE.json.
func (d *Daemon) LiveQueue() []QueueLine {
	var out []QueueLine
	if d.Held != nil && d.status.HeldKnown {
		for _, h := range d.heldCards {
			out = append(out, QueueLine{Card: h.Card, Col: cmp.Or(h.Col, "-"), Brief: "inbox/" + h.Job + "/BRIEF.md"})
		}
		return out
	}
	var q Queue
	path := filepath.Join(d.Dir, filepath.FromSlash(QueueFile))
	if _, err := read(path, &q); err != nil {
		if _, err := read(path, &q.Tasks); err != nil {
			return nil // ignored: a queue file that is no queue names no card; the present says the queue is empty
		}
	}
	for _, t := range q.Tasks {
		if t.State != "queued" && t.State != "" && t.State != "working" {
			continue
		}
		brief := "-"
		if t.Job != "" {
			brief = "inbox/" + t.Job + "/BRIEF.md"
		}
		out = append(out, QueueLine{Card: t.ID, Col: cmp.Or(t.State, "queued"), Brief: brief})
	}
	return out
}

// PresentText is the present turn's text: the pong line first while a challenge is open, the
// daemon's word about the coordinator, then who she is and who has the seat, her live queue,
// the skipped line, and the newest coordinator note (as the session reads it under the seat's
// authority), or that there is none.
func PresentText(friend, seat string, at time.Time, queue []QueueLine, skipped Skipped, note *bus.Message, notice, pongCommand string) string {
	var b strings.Builder
	if pongCommand != "" {
		b.WriteString("Run this now, first, exactly as written: " + pongCommand + "\nThen read on.\n\n")
	}
	if notice != "" {
		b.WriteString("nova-friend: " + notice + "\n\n")
	}
	stamp := at.UTC().Format(time.RFC3339)
	fmt.Fprintf(&b, "%s at %s. You are %s; the seat is %s. This is the present: it replaces everything older on your stream, and nothing older will be delivered. Work only what is below.\n\n",
		PresentTextRule, stamp, friend, cmp.Or(seat, "unknown (the seat is not known)"))
	if len(queue) == 0 {
		b.WriteString("Your live queue is empty: no card is on your row.\n")
	} else {
		fmt.Fprintf(&b, "Your live queue, %d card(s) on your row:\n", len(queue))
		for _, q := range queue {
			fmt.Fprintf(&b, "- %s %s %s\n", q.Card, q.Col, q.Brief)
		}
	}
	fmt.Fprintf(&b, "\nSkipped: %d deals, %d pings, %d notes, all superseded by the present at %s.\n", skipped.Deals, skipped.Pings, skipped.Notes, stamp)
	if note == nil {
		b.WriteString("No note from the coordinator is waiting.\n")
		return b.String()
	}
	b.WriteString("\nThe newest note from the coordinator:\n\n")
	b.WriteString(authored(seat, *note))
	return b.String()
}

// SupersededReason is the reason a message the present replaced is acked with.
func SupersededReason(at time.Time) string {
	return "superseded by the present at " + at.UTC().Format(time.RFC3339)
}

// presentOwed says the next delivery is the present: one is due (the session started, its id
// changed, or she asked), or, in batch mode, something waits to be delivered (a message, a
// brief written, a deferred turn) and the session has taken no turn for StaleAfter. A passive
// harness takes no turn and reads her stream itself: it is owed none. It waits for the first
// beat when her row says her mode, and RecheckEvery after a present turn that failed.
func (l *loop) presentOwed(now time.Time) bool {
	if l.d.noPresent || l.passive || now.Before(l.presentRetry) {
		return false
	}
	if l.d.Row != nil && l.d.status.Beats == 0 {
		return false // her row's mode is not known before the first beat answers it: a one-shot friend has no batch session
	}
	if l.presentDue {
		return true
	}
	waiting := len(l.hand) > 0 || len(l.dealt) > 0 || (l.busy != nil && !l.busy.running && !l.busy.present)
	return l.mode == ModeBatch && waiting && now.Sub(l.delivered) >= StaleAfter
}

// startPresent is the present: every message waiting on her stream taken off it (the hand, a
// deferred turn's, the rest read at once), planned (PlanPresent), the superseded acked with the
// reason, said on the record and once to the seat on the bus, and, withTurn, one PRESENT turn
// carrying her live queue, the seat, the skipped line and the newest coordinator note (acked as
// any turn's message when it ends at exit 0). In one-shot mode there is no batch session to
// tell: the backlog is superseded alone, and the lanes hand her cards. A store that does not
// answer leaves the present due, tried again the next step.
func (l *loop) startPresent(now time.Time, withTurn bool) {
	d, b := l.d, l.b
	var backlog []bus.Entry
	if l.presentCarry != nil {
		backlog = append(backlog, *l.presentCarry)
	}
	if l.busy != nil && !l.busy.running { // a deferred turn: its messages are older than the hand's
		for i, id := range l.busy.entries {
			backlog = append(backlog, bus.Entry{Stream: bus.StreamOf(d.Friend), Entry: id, Fields: l.busy.msgs[i].Fields()})
		}
		if l.busy.notice != nil && l.notice == nil {
			l.notice = l.busy.notice
		}
	}
	backlog = append(backlog, l.hand...)
	seen := map[string]bool{}
	for _, e := range backlog {
		seen[e.Entry] = true
	}
	// Read a finite snapshot, including messages held by the previous run. Recv
	// waits for ClaimAfter before handing those pending entries back, which is
	// too late for a new session; repeatedly claiming also cycles a large backlog.
	if err := l.presentBacklog(seen, &backlog); err != nil {
		if l.ctx.Err() == nil {
			d.status.StoreError = err.Error()
			d.Record(fmt.Sprintf("%s present: not yet: the stream could not be read: %s; tried again the next step", now.UTC().Format(time.RFC3339), oneLine(err.Error(), 300)))
		}
		return
	}
	// The raw snapshot takes entries without Recv's delivery receipt. Preserve
	// that receipt transition, and remember an already acted note so a lost ack
	// cannot make it the newest instruction delivered a second time.
	for i := 0; i < len(backlog); i += SupersedeChunk {
		chunk := backlog[i:min(i+SupersedeChunk, len(backlog))]
		ids := make([]string, len(chunk))
		for j, e := range chunk {
			ids[j] = e.Message().ID
		}
		prior, err := b.Stamp(l.ctx, d.Friend, bus.Delivered, ids...)
		if err != nil {
			d.status.StoreError = err.Error()
			d.Record(fmt.Sprintf("%s present: not yet: delivery receipts could not be read: %s; tried again the next step", now.UTC().Format(time.RFC3339), oneLine(err.Error(), 300)))
			return
		}
		for j := range chunk {
			if prior[j] == bus.Acted || l.acted[chunk[j].Message().ID] {
				chunk[j].Stage = bus.Acted
			}
		}
	}
	_, storeNow, err := d.Store.Roster(l.ctx)
	if err != nil {
		d.status.StoreError = err.Error()
		d.Record(fmt.Sprintf("%s present: not yet: the store's clock could not be read: %s; tried again the next step", now.UTC().Format(time.RFC3339), oneLine(err.Error(), 300)))
		return
	}
	// Held entries can precede this run's hand; restore stream order before
	// choosing the newest coordinator note. Redis IDs are canonical decimals.
	slices.SortFunc(backlog, func(a, b bus.Entry) int {
		aTime, aSeq, _ := strings.Cut(a.Entry, "-")
		bTime, bSeq, _ := strings.Cut(b.Entry, "-")
		return cmp.Or(cmp.Compare(len(aTime), len(bTime)), strings.Compare(aTime, bTime),
			cmp.Compare(len(aSeq), len(bSeq)), strings.Compare(aSeq, bSeq))
	})
	seat := l.seat(now)
	plan := PlanPresent(d.Friend, seat, backlog, storeNow, d.m.Window)
	if !withTurn && plan.Note != nil {
		// A per-card runner has no batch session to carry the newest note.
		// Supersede it too, so it cannot return later as an old instruction.
		plan.Superseded = append(plan.Superseded, plan.Note.Entry)
		plan.Skipped.Notes++
		plan.Note = nil
	}
	l.busy, l.retry, l.deferrals, l.deferSaid = nil, time.Time{}, 0, time.Time{}
	l.hand, l.presentCarry, l.presentDue, l.dealt = nil, nil, false, nil
	for e := range seen {
		delete(l.inHand, e)
	}
	l.presentAt = storeNow
	for _, e := range plan.Fresh {
		msg := e.Message()
		nonce, pseat, since, _ := ParsePing(msg.Body)
		l.ping(e, msg, nonce, pseat, since, now)
		if _, err := b.AckEntry(l.ctx, d.Friend, e.Entry); err == nil {
			delete(l.answered, e.Entry)
		}
	}
	reason := SupersededReason(storeNow)
	line := fmt.Sprintf("%s present at %s: skipped %d deals, %d pings, %d notes", now.UTC().Format(time.RFC3339), storeNow.UTC().Format(time.RFC3339), plan.Skipped.Deals, plan.Skipped.Pings, plan.Skipped.Notes)
	if n := len(plan.Superseded); n > 0 {
		acked := 0
		for i := 0; i < n; i += SupersedeChunk {
			chunk := plan.Superseded[i:min(i+SupersedeChunk, n)]
			if _, err := d.Store.Ack(l.ctx, bus.StreamOf(d.Friend), d.Friend, chunk...); err != nil {
				d.status.StoreError = err.Error()
				line += fmt.Sprintf(" ack=failed after %d: %s (each is superseded again when the claim hands it in)", acked, oneLine(err.Error(), 200))
				if l.lost == nil {
					l.lost = map[string]bool{}
				}
				for _, id := range plan.Superseded[i:] {
					l.lost[id] = true // the ack may have landed or not: the claim's hand-in says which
				}
				break
			}
			acked += len(chunk)
		}
		line += fmt.Sprintf(" acked=%d reason=%q", acked, reason)
		for _, id := range plan.Superseded {
			delete(l.failed, id)
		}
	}
	if !withTurn {
		d.Record(line + " (one-shot: no batch session; the lanes hand her cards)")
		l.tellSuperseded(plan, backlog, reason, storeNow, now)
		return
	}
	queue := d.LiveQueue()
	t := &turn{present: true, subjects: fmt.Sprintf("%q", "present")}
	var note *bus.Message
	if plan.Note != nil {
		m := plan.Note.Message()
		note = &m
		t.entries, t.msgs = []string{plan.Note.Entry}, []bus.Message{m}
		l.inHand[plan.Note.Entry] = true
	}
	notice, pong := l.head()
	t.notice = l.noticeTaken
	t.text = PresentText(d.Friend, seat, storeNow, queue, plan.Skipped, note, notice, pong)
	noteID := "none"
	if note != nil {
		noteID = note.ID
	}
	d.Record(line + fmt.Sprintf(" queue=%d note=%s", len(queue), noteID))
	l.tellSuperseded(plan, backlog, reason, storeNow, now)
	l.busy, l.delivered = t, now
	l.startTurn(t, now, l.deliverBatch(t))
}

// presentBacklog takes the group's pending entries and the fresh entries that
// exist at this read. Every acquired entry stays in hand before another store
// command can fail, so retrying cannot lose it behind the claim window.
func (l *loop) presentBacklog(seen map[string]bool, backlog *[]bus.Entry) error {
	d := l.d
	stream := bus.StreamOf(d.Friend)
	keep := func(entries []bus.Entry) {
		for _, e := range entries {
			if !seen[e.Entry] {
				seen[e.Entry] = true
				*backlog = append(*backlog, e)
				l.hand = append(l.hand, e)
				l.inHand[e.Entry] = true
			}
		}
	}
	if err := d.Store.EnsureGroup(l.ctx, stream, d.Friend); err != nil {
		return err
	}
	// Store count is Redis's signed integer maximum, so the snapshot covers
	// all entries rather than leaving an arbitrary page available for replay.
	const allEntries = int(^uint(0) >> 1)
	ids, err := d.Store.Pending(l.ctx, stream, d.Friend, allEntries)
	if err != nil {
		return err
	}
	for i := 0; i < len(ids); i += SupersedeChunk {
		entries, err := d.Store.Get(l.ctx, stream, ids[i:min(i+SupersedeChunk, len(ids))])
		if err != nil {
			return err
		}
		keep(entries)
	}
	last, _, err := d.Store.Group(l.ctx, stream, d.Friend)
	if err != nil {
		return err
	}
	fresh, err := d.Store.Range(l.ctx, stream, "("+last, "+", allEntries)
	if err != nil {
		return err
	}
	for left := len(fresh); left > 0; {
		entries, err := d.Store.Read(l.ctx, stream, d.Friend, bus.Consumer, 0, min(left, SupersedeChunk))
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			break
		}
		keep(entries)
		left -= len(entries)
	}
	return nil
}

// SupersedeChunk bounds the entries one ack of the superseded carries.
const SupersedeChunk = 256

// SupersededShown bounds the ids the bus message names; the record says how many in all.
const SupersededShown = 64

// tellSuperseded puts the present's acks on the bus log: one status message to the seat naming
// what was skipped, the reason and the ids superseded (the first SupersededShown of them). A
// present that superseded nothing says nothing.
func (l *loop) tellSuperseded(plan PresentPlan, backlog []bus.Entry, reason string, storeNow, now time.Time) {
	if plan.Skipped.Total() == 0 {
		return
	}
	byEntry := map[string]string{}
	for _, e := range backlog {
		byEntry[e.Entry] = e.Message().ID
	}
	var ids []string
	for _, e := range plan.Superseded {
		if id := byEntry[e]; id != "" && len(ids) < SupersededShown {
			ids = append(ids, id)
		}
	}
	more := ""
	if n := len(plan.Superseded); n > len(ids) {
		more = fmt.Sprintf(" (and %d more)", n-len(ids))
	}
	s := plan.Skipped
	l.tellKind(bus.KindStatus, fmt.Sprintf("friend %s: present at %s: skipped %d deals, %d pings, %d notes", l.d.Friend, storeNow.UTC().Format(time.RFC3339), s.Deals, s.Pings, s.Notes),
		fmt.Sprintf("Every older message on her stream was marked superseded unread, %s: %s%s\n", reason, strings.Join(ids, " "), more), now)
}

// presentEnded is a present turn's end, after its settle: one that did not end at exit 0 is
// owed again after RecheckEvery, carrying the same note while it is still pending.
func (l *loop) presentEnded(t *turn, ok bool, now time.Time) {
	if ok || !t.present {
		return
	}
	l.presentDue, l.presentRetry = true, now.Add(RecheckEvery)
	if len(t.entries) == 1 && l.failed[t.entries[0]] > 0 {
		l.presentCarry = &bus.Entry{Stream: bus.StreamOf(l.d.Friend), Entry: t.entries[0], Fields: t.msgs[0].Fields()}
		l.inHand[t.entries[0]] = true
	}
}

// staleNonce says a ping read now is past the challenge window by the store's clock (its at
// against the store's time now, one trip): dropped, never answered. A store whose time cannot
// be read leaves the ping for a present retry and answers no unverified nonce.
func (l *loop) staleNonce(m bus.Message) bool {
	if l.d.noPresent {
		return false
	}
	_, storeNow, err := l.d.Store.Roster(l.ctx)
	if err != nil {
		// Treat it as withheld, not fresh: the read keeps it in hand because
		// a present is now due; a successful clock read will classify it there.
		l.presentDue = true
		l.d.Record(fmt.Sprintf("%s ping withheld: the store's clock could not be read: %s; the present is owed", l.now.UTC().Format(time.RFC3339), oneLine(err.Error(), 200)))
		return true
	}
	return StaleNonce(m.At, storeNow, l.d.m.Window)
}
