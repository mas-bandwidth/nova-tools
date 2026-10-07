package friend

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// PresentStale is how long the daemon may deliver nothing before the next
// delivery is one present and the backlog is acked (docs/SPEC-FRIEND.md, The
// present; tla/Delivery.tla, DeliveryAfterGap).
const PresentStale = 30 * time.Minute

// PresentFile is the daemon's memory of the session, the run, and the last
// delivery, under the state directory. A missing file is a first sight, not
// a gap.
const PresentFile = "present.json"

// presentMemo is that file: the session id and the run id last seen, and when
// a delivery last went into the session. An empty id is not yet seen.
type presentMemo struct {
	Session string    `json:"session,omitempty"`
	Launch  string    `json:"launch,omitempty"`
	At      time.Time `json:"at,omitempty"`
}

// PresentReason is the ack reason of every message a present supersedes, and
// the line the bus log carries for it (docs/SPEC-FRIEND.md, The present).
func PresentReason(at time.Time) string {
	return "superseded by the present at " + at.UTC().Format(time.RFC3339)
}

// presentDir is the state directory present.json lives in: StateDir when the
// run named one (--state-dir, or the home directory when <dir>/.nova-friend
// is refused), else StateDirIn(Dir). An empty answer writes nothing.
func (d *Daemon) presentDir() string {
	if d == nil {
		return ""
	}
	if d.StateDir != "" {
		return d.StateDir
	}
	if d.Dir == "" {
		return ""
	}
	return StateDirIn(d.Dir)
}

func (d *Daemon) presentPath() string {
	return filepath.Join(d.presentDir(), PresentFile)
}

func (d *Daemon) loadPresent() presentMemo {
	var m presentMemo
	if d.presentDir() == "" {
		return m
	}
	_, _ = read(d.presentPath(), &m) // ignored: a missing file is a first sight; a file that is not this record reads as one
	return m
}

func (d *Daemon) savePresent(m presentMemo, now time.Time) {
	if d.presentDir() == "" {
		return
	}
	if err := write(d.presentPath(), m); err != nil && d.Record != nil {
		d.Record(now.UTC().Format(time.RFC3339) + " present: the state was not saved: " + oneLine(err.Error(), 200))
	}
}

// presentWhy is why this step owes a present, or "" when it does not. The
// first sight of a session id or a run id is remembered by the caller and is
// not a start (docs/SPEC-FRIEND.md, The present; tla/Delivery.tla, Open).
func (d *Daemon) presentWhy(now time.Time) string {
	m := d.loadPresent()
	switch {
	case d.Session != "" && m.Session != "" && d.Session != m.Session:
		return "session"
	case d.Launch != "" && m.Launch != "" && d.Launch != m.Launch:
		return "launch"
	case !m.At.IsZero() && now.Sub(m.At) >= PresentStale:
		return "gap"
	default:
		return ""
	}
}

// notePresentSeen remembers the session and the run, and, when delivered, the
// time, so the same start does not present again (tla/Delivery.tla, Deliver).
func (d *Daemon) notePresentSeen(now time.Time, delivered bool) {
	m := d.loadPresent()
	changed := false
	if d.Session != "" && m.Session != d.Session {
		m.Session = d.Session
		changed = true
	}
	if d.Launch != "" && m.Launch != d.Launch {
		m.Launch = d.Launch
		changed = true
	}
	// a step that delivers nothing still moves the mark, once a delivery has
	// set it: the thirty minutes are a stretch with no step, or one step
	// whose clock jumped, not a session that stays up (docs/SPEC-FRIEND.md, The present)
	if (delivered || !m.At.IsZero()) && !m.At.Equal(now) {
		m.At = now
		changed = true
	}
	if changed {
		d.savePresent(m, now)
	}
}

// noteDelivery is a turn going into the session: the gap clock starts again
// from now (docs/SPEC-FRIEND.md, The present).
func (d *Daemon) noteDelivery(now time.Time) {
	if d == nil {
		return
	}
	d.notePresentSeen(now, true)
}

// nonceStale says a ping's nonce is older than the challenge window. The
// message's time is the store's; a zero time is not stale, it is unknown
// (docs/SPEC-FRIEND.md, The present).
func nonceStale(now time.Time, m bus.Message) bool {
	return !m.At.IsZero() && !now.Before(m.At.Add(Window))
}

// dropNonce acks a stale ping without answering it.
func (l *loop) dropNonce(e bus.Entry, msg bus.Message, now time.Time) error {
	if l.d.Record != nil {
		l.d.Record(fmt.Sprintf("%s nonce dropped id=%s: older than the challenge window (%s)", now.UTC().Format(time.RFC3339), msg.ID, Window))
	}
	_, err := l.b.AckEntry(l.ctx, l.d.Friend, e.Entry)
	return err
}

// isPresentRequest is the friend's own ask for the present: her message, the
// subject or a body line exactly "present" (docs/SPEC-FRIEND.md, The present).
func isPresentRequest(friend string, m bus.Message) bool {
	if friend == "" || m.From != friend {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(m.Subject), "present") {
		return true
	}
	for _, line := range strings.Split(m.Body, "\n") {
		if strings.EqualFold(strings.TrimSpace(line), "present") {
			return true
		}
	}
	return false
}

// messageKind is how a backlog message is counted: a ping, a deal, or a note.
func messageKind(m bus.Message) string {
	if _, _, _, ok := ParsePing(m.Body); ok || strings.HasPrefix(m.Subject, PingPrefix) {
		return "ping"
	}
	if isDeal(m) {
		return "deal"
	}
	return "note"
}

func isDeal(m bus.Message) bool {
	s := strings.ToLower(m.Subject)
	if strings.Contains(s, "dealt") || s == "deal" || strings.Contains(s, " deal") {
		return true
	}
	for _, line := range strings.Split(m.Body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "REPO:") || strings.HasPrefix(line, "STATUS:") || strings.HasPrefix(line, "BASE:") {
			return true
		}
	}
	return false
}

// queueLines is the live queue as the present prints it: every task in
// QUEUE.json, its column, and its BRIEF.md path (docs/SPEC-FRIEND.md, The present).
func queueLines(dir string) []string {
	var q Queue
	if _, err := read(filepath.Join(dir, filepath.FromSlash(QueueFile)), &q); err != nil {
		return nil
	}
	lines := make([]string, 0, len(q.Tasks))
	for _, task := range q.Tasks {
		if task.ID == "" {
			continue
		}
		col := task.State
		if col == "" {
			col = "queued"
		}
		job := task.Job
		if job == "" {
			job = task.ID
		}
		lines = append(lines, task.ID+" "+col+" inbox/"+job+"/BRIEF.md")
	}
	return lines
}

func skippedLine(deals, pings, notes int) string {
	return fmt.Sprintf("%d deals, %d pings, %d notes, all superseded", deals, pings, notes)
}

// presentText is the one present turn (docs/SPEC-FRIEND.md, The present).
func presentText(seat string, queue []string, note *bus.Message, deals, pings, notes int) string {
	var b strings.Builder
	b.WriteString("nova-friend: present. The live queue follows; the backlog is superseded and is not delivered.\n")
	fmt.Fprintf(&b, "seat: %s\n", seat)
	b.WriteString("queue:\n")
	if len(queue) == 0 {
		b.WriteString("(none)\n")
	}
	for _, line := range queue {
		b.WriteString(line + "\n")
	}
	if note != nil {
		fmt.Fprintf(&b, "newest note from %s subject=%q\n%s", note.From, note.Subject, note.Body)
		if !strings.HasSuffix(note.Body, "\n") {
			b.WriteString("\n")
		}
	} else {
		b.WriteString("newest note: none\n")
	}
	b.WriteString(skippedLine(deals, pings, notes) + "\n")
	return b.String()
}

// holderOf is the other friend the running list says holds id or job, or ""
// when the list is absent or names her (docs/SPEC-FRIEND.md, The present).
func (d *Daemon) holderOf(id, job string) string {
	if d == nil || d.Running == nil {
		return ""
	}
	running := d.Running()
	for _, k := range []string{id, job} {
		if k == "" {
			continue
		}
		if who := running[k]; who != "" && who != d.Friend && who != "friend."+d.Friend {
			return who
		}
	}
	return ""
}

// maybePresent delivers one present when a start, a gap, a request, or a
// failed present is owed, and otherwise remembers a session id or a run id
// seen for the first time (docs/SPEC-FRIEND.md, The present; tla/Delivery.tla,
// DeliveryAfterGap).
func (l *loop) maybePresent(now time.Time) {
	if l.passive || l.broken || l.busy != nil || l.d.Deliver == nil {
		return
	}
	if l.lanes != nil && l.lanes.running() {
		return
	}
	why := l.d.presentWhy(now)
	request := l.handHasPresent() || l.streamAsksPresent()
	if why == "" && !request {
		if l.redo != "" {
			text := l.redo
			l.redo = ""
			l.startPresentTurn(now, text)
			return
		}
		l.d.notePresentSeen(now, false)
		return
	}
	if why == "" {
		why = "request"
	}
	l.redo = ""
	l.deliverPresent(now, why)
}

func (l *loop) handHasPresent() bool {
	for _, e := range l.hand {
		if isPresentRequest(l.d.Friend, e.Message()) {
			return true
		}
	}
	return false
}

func (l *loop) streamAsksPresent() bool {
	pending, fresh, err := l.b.Peek(l.ctx, l.d.Friend)
	if err != nil {
		return false
	}
	for _, e := range append(pending, fresh...) {
		if isPresentRequest(l.d.Friend, e.Message()) {
			return true
		}
	}
	return false
}

func (l *loop) startPresentTurn(now time.Time, text string) {
	t := &turn{text: text, subjects: `"present"`, present: true}
	l.busy = t
	l.startTurn(t, now, l.deliverBatch(t))
}

// deliverPresent acks the backlog with one reason, tells the seat so the bus
// log shows it, and delivers one present turn that carries no stream entry
// (docs/SPEC-FRIEND.md, The present; tla/Delivery.tla, DeliveryAfterGap).
func (l *loop) deliverPresent(now time.Time, why string) {
	d := l.d
	entries, msgs := l.drainBacklog()
	seat := l.seat(now)
	deals, pings, notes := 0, 0, 0
	var newest *bus.Message
	reason := PresentReason(now)
	at := now.UTC().Format(time.RFC3339)
	for i := range msgs {
		m := msgs[i]
		if isPresentRequest(d.Friend, m) {
			if d.Record != nil {
				d.Record(at + " present: ack id=" + m.ID + " " + reason)
			}
			continue
		}
		switch messageKind(m) {
		case "ping":
			pings++
		case "deal":
			deals++
		default:
			notes++
			if seat == "" || m.From == seat {
				newest = &msgs[i]
			}
		}
		if d.Record != nil {
			d.Record(at + " present: ack id=" + m.ID + " " + reason)
		}
	}
	if len(entries) > 0 {
		if _, err := d.Store.Ack(l.ctx, bus.StreamOf(d.Friend), d.Friend, entries...); err != nil {
			d.status.StoreError = err.Error()
		}
	}
	text := presentText(seat, queueLines(d.Dir), newest, deals, pings, notes)
	l.sayPresent(seat, reason, skippedLine(deals, pings, notes), now)
	if d.Record != nil {
		d.Record(fmt.Sprintf("%s present: %s (%s)", at, why, skippedLine(deals, pings, notes)))
	}
	d.notePresentSeen(now, true)
	l.startPresentTurn(now, text)
}

func (l *loop) sayPresent(seat, reason, skipped string, now time.Time) {
	to := seat
	if to == "" {
		to = l.d.Coordinator
	}
	if to == "" || to == l.d.Friend {
		if l.d.Record != nil {
			l.d.Record(now.UTC().Format(time.RFC3339) + " present: no seat to tell: " + reason)
		}
		return
	}
	if _, err := l.b.Send(l.ctx, bus.Message{From: l.d.Friend, To: []string{to}, Subject: "present", Body: reason + "\n" + skipped + "\n"}); err != nil && l.d.Record != nil {
		l.d.Record(now.UTC().Format(time.RFC3339) + " present: telling " + to + " failed: " + oneLine(err.Error(), 200) + ": " + reason)
	}
}

// drainBacklog is the hand and every message still on the stream, oldest
// first, claimed and not answered. The caller acks them.
func (l *loop) drainBacklog() (entries []string, msgs []bus.Message) {
	for _, e := range l.hand {
		entries = append(entries, e.Entry)
		msgs = append(msgs, e.Message())
		delete(l.inHand, e.Entry)
		delete(l.answered, e.Entry)
	}
	l.hand = nil
	for l.ctx.Err() == nil {
		e, ok, err := l.b.Recv(l.ctx, l.d.Friend, 0)
		if err != nil || !ok {
			break
		}
		entries = append(entries, e.Entry)
		msgs = append(msgs, e.Message())
	}
	return entries, msgs
}
