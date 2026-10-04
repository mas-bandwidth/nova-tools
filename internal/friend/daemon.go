package friend

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// BeatEvery is how often the daemon beats to the sprint server while its
// loop runs: the sprint's own number (internal/sprint FriendBeatEvery, one
// second; a friend is down after fifteen without one). It is also the
// loop's read block: one read of the stream per beat.
const BeatEvery = time.Second

// MaxDeliveries is how many times a message is handed into the session
// before the daemon gives up on it: a turn that fails leaves the message
// pending and the bus hands it in again once its claim opens (bus.ClaimAfter);
// the last failure acks it, with the failure on the record, so a message the
// session cannot take never comes back for ever. A turn the provider refused
// (ProviderRefused) counts toward nothing here: the session is at fault, not
// the message, and BrokenAfter says what happens instead.
const MaxDeliveries = 3

// RecheckEvery is how long the daemon waits before trying a deferred
// delivery again (Deferred: the session cannot take a turn now and nothing
// is wrong). The message stays in the daemon's hand meanwhile: it is never
// put back on the bus, never counted toward MaxDeliveries, never acked.
const RecheckEvery = 10 * time.Second

const DaemonConsumer = "nova-friend-daemon"

const DaemonReadBatch = 128

// StatusErrorEvery bounds how often a status file that cannot be written
// is said in the record: the loop goes on beating and delivering without it.
// DeferredSaidEvery bounds how often a deferral still in hand is said.
const (
	StatusErrorEvery  = time.Minute
	DeferredSaidEvery = time.Minute
)

// DefaultSilentStop is how long a turn may print nothing before the daemon
// stops it (--silent-stop): a turn that prints keeps running however long
// it takes (the finding of 2026-10-04: a fixed ten-minute cap killed a
// friend's real work mid-turn).
const DefaultSilentStop = 20 * time.Minute

// DefaultBrokenAfter is how many turns in a row the provider must refuse
// with the same reason before the session is broken (--broken-after).
const DefaultBrokenAfter = 3

// MaxBatch bounds how many messages go into one turn, and BatchBytes how
// much text: the rest waits for the next turn, oldest first. A turn's text
// travels as one argument to some harnesses (opencode run), under the
// platform's argument limit.
const (
	MaxBatch   = 32
	BatchBytes = 256 << 10
)

// The subjects of the daemon's own messages on the bus.
const (
	DaemonPongSubject = "daemon-pong"
	PongSubject       = "pong"
	PingPrefix        = "PING "
)

// The session's state, as the status file says it.
const (
	SessionOK     = "ok"
	SessionBroken = "broken"
)

// Daemon is one friend's loop: the recv loop over the friend's stream with
// the deliver adapter, the beat, and the Machine stepped by what arrives.
// Everything it reaches outside itself is a field, so a test runs it over
// bus's Fake, a fake harness and its own clock.
type Daemon struct {
	Friend, Harness, Dir string
	StateDir             string
	Keepalive            func(context.Context) error
	Width                int
	Store                bus.Store
	Deliver              Deliverer
	Beat                 func(ctx context.Context, asleep bool) error // one beat to the sprint server
	Now                  func() time.Time
	// Pause waits d when the store did not: after a read that answered at
	// once (blocked false: an error, or a store that does not block), and
	// while a delivery runs and the loop only peeks.
	Pause  func(ctx context.Context, d time.Duration)
	Record func(line string) // one line per delivery, to the daemon's log
	// Pong is the session's recorded answer, read each step while a
	// challenge is open (ReadPong over the state files).
	Pong func() (Pong, bool, error)
	// Status receives the daemon's state whenever it changes, and every
	// StatusEvery (WriteStatus over the state files).
	Status func(Status) error
	// PongCommand is the exact pong line for this friend and nonce (the
	// binary by path, --as, --dir, --redis), put at the head of a turn while
	// a challenge is open, so a small model has one line to run and nothing
	// to fill in.
	PongCommand func(nonce string) string
	// SilentStop is how long a running turn may print nothing before it is
	// stopped (DefaultSilentStop when zero); BrokenAfter how many turns in a
	// row the provider refuses the same way before the session is broken
	// (DefaultBrokenAfter when zero); Coordinator who is told of a broken
	// session when no ping has named the seat.
	SilentStop  time.Duration
	BrokenAfter int
	Coordinator string

	m           *Machine
	status      Status
	written     time.Time
	written0    Status
	statusErrAt time.Time
}

// turn is one delivery into the session: the messages it carries (acked
// together at exit 0) and the daemon's word about the coordinator, if any.
type turn struct {
	entries  []string // the stream entries to ack
	msgs     []bus.Message
	notice   *Push
	text     string
	started  time.Time
	running  bool // a Deliver is under way (false while a deferral waits)
	cancel   context.CancelFunc
	seen     *atomic.Int64 // outputs the command printed
	seenN    int64
	lastOut  time.Time // when the daemon last saw the turn print, or its start
	stopped  bool      // the daemon stopped it: silent past SilentStop
	subjects string
}

type result struct {
	t    *turn
	exit int
	err  error
}

// Text is a message as the session reads it, the shape nova-bus recv
// prints: the header line, a blank line, the body ending in a newline.
func Text(m bus.Message) string {
	body := m.Body
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return fmt.Sprintf("RECV OK id=%s from=%s to=%s cc=%s re=%s at=%s subject=%q\n\n%s",
		m.ID, m.From, dash(strings.Join(m.To, ",")), dash(strings.Join(m.CC, ",")), dash(m.Re), m.At.Format(time.RFC3339), m.Subject, body)
}

// Batch is one turn's text: the pong line to run first while a challenge
// is open, the daemon's word about the coordinator, then every message,
// oldest first, each as nova-bus recv prints it under a numbered rule. A
// single message with nothing else is its Text alone.
func Batch(msgs []bus.Message, notice, pongCommand string) string {
	if len(msgs) == 1 && notice == "" && pongCommand == "" {
		return Text(msgs[0])
	}
	var b strings.Builder
	if pongCommand != "" {
		b.WriteString("Run this now, first, exactly as written: " + pongCommand + "\nThen read on.\n\n")
	}
	if notice != "" {
		b.WriteString("nova-friend: " + notice + "\n\n")
	}
	fmt.Fprintf(&b, "nova-friend: %d message(s) for you, oldest first, in one turn; take each in order.\n", len(msgs))
	for i, m := range msgs {
		fmt.Fprintf(&b, "\n=== message %d of %d: id=%s from=%s subject=%q ===\n", i+1, len(msgs), m.ID, m.From, m.Subject)
		b.WriteString(Text(m))
	}
	return b.String()
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// oneLine is s on one line, at most n bytes: a reason fit for a subject.
func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		s = s[:n]
	}
	return s
}

// Run is the loop until ctx ends. Each step: the clock; when the session
// is free, every pending message read off the stream (a ping is answered by
// the daemon at once and acked, never pushed in) and one turn started with
// all of them; while a turn runs, one peek, so a ping arriving during a long
// turn is still answered at once; the turn's output watched, and a turn
// silent past SilentStop stopped; the turn's result (exit 0 acks every
// message it carried); a beat when the store answered; the session's pong;
// the status. The daemon's own words about the coordinator collapse to the
// latest and ride in a turn that carries messages, never alone.
func (d *Daemon) Run(ctx context.Context) (runErr error) {
	b := &bus.Bus{Store: d.Store}
	if d.StateDir != "" {
		lock, err := TakeDaemonLock(d.StateDir, d.Friend)
		if err != nil {
			return err
		}
		defer func() { runErr = errors.Join(runErr, lock.Unlock()) }()
	}
	if d.StateDir != "" && d.Coordinator != "" {
		if _, err := UpdateSessionState(d.StateDir, func(s *SessionState) error { s.Coordinator = d.Coordinator; return nil }); err != nil {
			return err
		}
	}
	state, err := d.sessionState()
	if err != nil {
		return err
	}
	if state.Asleep && state.Coordinator == "" {
		return errors.New("asleep friend needs a configured coordinator or local wake")
	}
	if d.Keepalive != nil {
		child, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() {
			err := d.Keepalive(child)
			if err == nil && child.Err() == nil {
				err = errors.New("keepalive loop stopped before daemon shutdown")
			}
			done <- err
			cancel()
		}()
		defer func() { cancel(); runErr = errors.Join(runErr, <-done) }()
		ctx = child
	}
	_, passive := d.Deliver.(interface{ Passive() })
	silentStop, brokenAfter := d.SilentStop, d.BrokenAfter
	if silentStop <= 0 {
		silentStop = DefaultSilentStop
	}
	if brokenAfter <= 0 {
		brokenAfter = DefaultBrokenAfter
	}
	d.m = Start(d.Now())
	d.status = Status{Friend: d.Friend, Harness: d.Harness, Started: d.m.LastPing, Width: d.Width}
	if !passive {
		d.status.Session = SessionOK
	}
	answered := map[string]bool{} // entries whose ping the daemon has ponged
	observed := map[string]bool{} // passive ordinary entries applied to saved wake state
	failed := map[string]int{}    // entries whose turn failed, and how often
	var hand []string             // stream IDs read and not yet in a turn, oldest first
	inHand := map[string]bool{}
	var notice *Push    // the latest word about the coordinator the session is owed
	saidSilent := false // what the session last heard: the coordinator silent
	var busy *turn      // the turn under way, or deferred in hand
	results := make(chan result, 1)
	defer func() {
		if busy != nil && busy.running {
			busy.cancel()
			<-results
		}
	}()
	var retry time.Time // when the deferred turn in hand is tried again; zero while none is
	var deferrals int
	var deferSaid time.Time
	var refusal string // the last provider refusal, and how many turns in a row said it
	var streak int
	broken, told := false, false
	say := func(p Push) {
		switch {
		case p.Subject == "coordinator back" && !saidSilent:
			notice = nil // the session never heard otherwise: nothing to say
		default:
			notice = &p
		}
	}
	start := func(t *turn, now time.Time) (bool, error) {
		reserved := false
		reserve := func(s SessionState) error {
			state = s
			if !s.Asleep && (s.WakeBarrier == "" || len(t.entries) > 0 && s.WakeBarrier == t.entries[0]) {
				reserved = true
			}
			return nil
		}
		if d.StateDir != "" {
			if err := WithSessionState(d.StateDir, reserve); err != nil {
				return false, err
			}
		} else if err := reserve(state); err != nil {
			return false, err
		}
		if !reserved {
			return false, nil
		}
		tctx, cancel := context.WithCancel(ctx)
		seen := &atomic.Int64{}
		tctx = WithOutputSeen(tctx, func() { seen.Add(1) })
		t.started, t.running, t.cancel, t.seen, t.seenN, t.lastOut, t.stopped = now, true, cancel, seen, 0, now, false
		busy = t
		go func() {
			exit, err := d.Deliver.Deliver(tctx, t.text)
			cancel()
			results <- result{t, exit, err}
		}()
		return true, nil
	}
	ping := func(e bus.Entry, msg bus.Message, nonce, seat string, since, now time.Time) {
		if answered[e.Entry] {
			return // the machine saw it when the daemon first did
		}
		d.daemonPong(ctx, b, msg, nonce, state.Asleep)
		answered[e.Entry] = true
		if state.Asleep {
			return
		}
		var pushes []Push
		if state.Coordinator == "" {
			pushes = d.m.Ping(now, seatOf(seat, msg), since, nonce)
		} else {
			pushes = d.m.ReceivePing(now, msg.From, state.Coordinator, seatOf(seat, msg), since, nonce)
		}
		for _, p := range pushes {
			say(p)
		}
	}
	handle := func(e bus.Entry, now time.Time) error {
		msg := e.Message()
		var err error
		state, err = d.sessionState()
		if err != nil {
			return err
		}
		if nonce, seat, since, isPing := ParsePing(msg.Body); isPing {
			ping(e, msg, nonce, seat, since, now)
			if _, err := b.AckEntry(ctx, d.Friend, e.Entry); err != nil {
				return err
			}
			delete(answered, e.Entry)
			return nil
		}
		if d.StateDir != "" && state.Coordinator != "" && msg.From == state.Coordinator {
			s, err := UpdateSessionState(d.StateDir, func(s *SessionState) error {
				if s.Asleep && s.Coordinator == msg.From && e.Entry != s.WakeBarrier {
					s.Asleep = false
					s.WakeBarrier = e.Entry
				}
				return nil
			})
			if err != nil {
				return err
			}
			state = s
		}
		if !inHand[e.Entry] {
			hand = append(hand, e.Entry)
			inHand[e.Entry] = true
		}
		return nil
	}
	// Recover owned entries in bounded pages, reading bodies to classify pings
	// and wake messages. Retain only IDs, then hydrate each bounded turn at launch.
	for cursor := ""; !passive; {
		entries, next, err := b.PendingPage(ctx, d.Friend, DaemonConsumer, cursor, DaemonReadBatch)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			d.status.StoreError = err.Error()
			d.flush(d.Now())
			d.Pause(ctx, BeatEvery)
			continue
		}
		for _, e := range entries {
			if err := handle(e, d.Now()); err != nil {
				return err
			}
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if !passive && state.WakeBarrier != "" && !inHand[state.WakeBarrier] {
		return fmt.Errorf("wake barrier %s is missing from daemon-owned pending deliveries; reconcile missing or foreign-owned entry before restarting", state.WakeBarrier)
	}
	for ctx.Err() == nil {
		now := d.Now()
		state, err = d.sessionState()
		if err != nil {
			return err
		}
		d.status.Asleep = state.Asleep
		if busy != nil && !busy.running && !retry.IsZero() && (state.Asleep || state.WakeBarrier != "" && (len(busy.entries) == 0 || state.WakeBarrier != busy.entries[0])) {
			for _, id := range busy.entries {
				if !inHand[id] {
					hand = append(hand, id)
					inHand[id] = true
				}
			}
			if notice == nil {
				notice = busy.notice
			}
			busy, retry, deferrals = nil, time.Time{}, 0
		}
		for _, p := range d.m.TickWhen(now, state.Asleep) {
			say(p)
		}
		if passive && notice != nil {
			// nothing can be pushed in: the session hears of it from its own read
			d.Record(now.UTC().Format(time.RFC3339) + " not delivered: " + d.Harness + " has no deliver command: " + notice.Subject)
			notice = nil
		}
		storeOK := true
		if busy == nil && !passive && !broken && (state.Asleep || state.WakeBarrier == "" || !inHand[state.WakeBarrier]) {
			var entries []bus.Entry
			var err error
			if state.Asleep || state.WakeBarrier != "" {
				entries, err = d.Store.Read(ctx, bus.StreamOf(d.Friend), d.Friend, DaemonConsumer, BeatEvery, DaemonReadBatch)
			} else {
				entries, err = b.RecvBatch(ctx, d.Friend, DaemonConsumer, BeatEvery, MaxBatch)
			}
			if err == nil {
				for _, e := range entries {
					if err = handle(e, now); err != nil {
						break
					}
				}
			}
			switch {
			case ctx.Err() != nil:
				return nil
			case err != nil:
				storeOK = false
				d.status.StoreError = err.Error()
				d.Pause(ctx, BeatEvery)
			default:
				d.status.StoreError = ""
			}
		} else { // a turn is running, the session is broken, or the harness is passive: peek, take nothing
			_, fresh, err := b.Peek(ctx, d.Friend)
			if ctx.Err() != nil {
				return nil
			}
			if err != nil {
				storeOK = false
				d.status.StoreError = err.Error()
			} else {
				d.status.StoreError = ""
				state, err = d.sessionState()
				if err != nil {
					return err
				}
				for _, e := range fresh {
					msg := e.Message()
					nonce, seat, since, isPing := ParsePing(msg.Body)
					if passive && !isPing && !observed[e.Entry] && d.StateDir != "" && state.Coordinator != "" && msg.From == state.Coordinator {
						s, err := UpdateSessionState(d.StateDir, func(s *SessionState) error {
							if s.Asleep && s.Coordinator == msg.From {
								s.Asleep = false
							}
							return nil
						})
						if err != nil {
							return err
						}
						state = s
					}
					if passive && !isPing {
						observed[e.Entry] = true
					}
					if isPing {
						// the machine sees the ping when the daemon does: a turn longer than a window is no silence
						ping(e, msg, nonce, seat, since, now)
					}
				}
			}
			d.Pause(ctx, BeatEvery)
		}
		if busy != nil && busy.running { // the watch: a turn that prints is working
			if n := busy.seen.Load(); n != busy.seenN {
				busy.seenN, busy.lastOut = n, now
			}
			if !busy.stopped && now.Sub(busy.lastOut) >= silentStop {
				busy.stopped = true
				busy.cancel()
				d.Record(fmt.Sprintf("%s subject=%s stopping: no output for %s (silent since %s); its process group is signalled",
					now.UTC().Format(time.RFC3339), busy.subjects, silentStop, busy.lastOut.UTC().Format(time.RFC3339)))
			}
		}
		select {
		case r := <-results:
			r.t.running = false
			var deferred Deferred
			if errors.As(r.err, &deferred) && !r.t.stopped { // not a failure: the turn stays in hand, tried again, counted toward nothing
				deferrals++
				retry = now.Add(RecheckEvery)
				if deferrals == 1 || now.Sub(deferSaid) >= DeferredSaidEvery {
					deferSaid = now
					d.Record(fmt.Sprintf("%s subject=%s deferred=%d: %s; tried again every %s, counted toward nothing (said once per %s)",
						now.UTC().Format(time.RFC3339), r.t.subjects, deferrals, deferred.Reason, RecheckEvery, DeferredSaidEvery))
				}
				break
			}
			if d.StateDir != "" && state.WakeBarrier != "" && len(r.t.entries) > 0 && state.WakeBarrier == r.t.entries[0] {
				s, err := UpdateSessionState(d.StateDir, func(s *SessionState) error {
					if s.WakeBarrier == r.t.entries[0] {
						s.WakeBarrier = ""
					}
					return nil
				})
				if err != nil {
					return err
				}
				state = s
			}
			line := fmt.Sprintf("%s subject=%s messages=%d took=%s exit=%d", now.UTC().Format(time.RFC3339), r.t.subjects, len(r.t.entries), now.Sub(r.t.started).Round(time.Millisecond), r.exit)
			if r.t.notice != nil {
				line += fmt.Sprintf(" notice=%q", r.t.notice.Subject)
			}
			if r.err != nil {
				line += " error=" + fmt.Sprintf("%q", r.err.Error())
			}
			if r.t.stopped {
				line += fmt.Sprintf(" stopped=%q", "no output for "+silentStop.String())
			}
			ok := r.err == nil && r.exit == 0 && !r.t.stopped
			var refused ProviderRefused
			switch {
			case ok:
				streak, refusal = 0, ""
				if len(r.t.entries) > 0 {
					if _, err := d.Store.Ack(ctx, bus.StreamOf(d.Friend), d.Friend, r.t.entries...); err != nil {
						d.status.StoreError = err.Error()
						line += " ack=failed"
					} else {
						d.status.Delivered += len(r.t.entries)
						line += " acked=true"
						for _, e := range r.t.entries {
							delete(failed, e)
						}
					}
				}
			case errors.As(r.err, &refused) && !r.t.stopped:
				// the session is at fault, not the messages: they stay pending, counted toward nothing
				if refused.Reason == refusal {
					streak++
				} else {
					refusal, streak = refused.Reason, 1
				}
				line += fmt.Sprintf(" refused=%d/%d", streak, brokenAfter)
				if streak >= brokenAfter && !broken {
					broken = true
					d.status.Session, d.status.SessionID, d.status.SessionReason, d.status.BrokenAt = SessionBroken, refused.Session, oneLine(refused.Reason, 200), now
					line += " session=broken"
				}
			default:
				streak, refusal = 0, ""
				var given []string
				for _, e := range r.t.entries {
					failed[e]++
					if failed[e] >= MaxDeliveries {
						given = append(given, e)
					}
				}
				if len(r.t.entries) > 0 {
					line += fmt.Sprintf(" deliveries=%d/%d", failed[r.t.entries[0]], MaxDeliveries)
				}
				if len(given) > 0 {
					line += " given_up=true"
					if len(r.t.entries) > 1 {
						line += fmt.Sprintf(" given_up_messages=%d", len(given))
					}
					if _, err := d.Store.Ack(ctx, bus.StreamOf(d.Friend), d.Friend, given...); err != nil {
						d.status.StoreError = err.Error()
						line += " ack=failed"
					} else {
						line += " acked=true"
						for _, e := range given {
							delete(failed, e)
						}
					}
				}
			}
			if !ok && r.t.notice != nil && notice == nil { // the word was not heard: it is owed again
				notice = r.t.notice
				saidSilent = r.t.notice.Subject != "coordinator silent"
			}
			d.Record(line)
			if broken && !told {
				d.Record(fmt.Sprintf("%s session %s broken: %s (%d turns in a row); delivering nothing into it until the daemon restarts, every message stays pending",
					now.UTC().Format(time.RFC3339), d.status.SessionID, d.status.SessionReason, brokenAfter))
			}
			busy, retry, deferrals, deferSaid = nil, time.Time{}, 0, time.Time{}
		default:
		}
		switch {
		case !state.Asleep && busy == nil && len(hand) > 0 && !broken:
			sort.SliceStable(hand, func(i, j int) bool {
				if (hand[i] == state.WakeBarrier) != (hand[j] == state.WakeBarrier) {
					return hand[i] == state.WakeBarrier
				}
				return entryBefore(hand[i], hand[j])
			})
			t := &turn{notice: notice}
			limit := min(len(hand), MaxBatch)
			if state.WakeBarrier != "" {
				limit = 1
			}
			entries, err := d.Store.Get(ctx, bus.StreamOf(d.Friend), hand[:limit])
			if err != nil {
				return err
			}
			if len(entries) != limit {
				return errors.New("pending delivery body is missing; reconcile the stream before restarting")
			}
			size := 0
			for _, e := range entries {
				if len(t.msgs) > 0 && size+len(e.Fields["body"]) > BatchBytes {
					break
				}
				t.entries, t.msgs = append(t.entries, e.Entry), append(t.msgs, e.Message())
				size += len(e.Fields["body"])
			}
			var subjects []string
			for _, m := range t.msgs {
				subjects = append(subjects, m.Subject)
			}
			t.subjects = fmt.Sprintf("%q", strings.Join(subjects, " | "))
			text := ""
			if notice != nil {
				text = notice.Text
			}
			pong := ""
			if d.m.Challenge != Quiet && d.PongCommand != nil {
				pong = d.PongCommand(d.m.Nonce)
			}
			t.text = Batch(t.msgs, text, pong)
			started, err := start(t, now)
			if err != nil {
				return err
			}
			if started {
				if notice != nil {
					saidSilent = notice.Subject == "coordinator silent"
					notice = nil
				}
				for range t.entries {
					delete(inHand, hand[0])
					hand = hand[1:]
				}
			}
		case busy != nil && !busy.running && !retry.IsZero() && !now.Before(retry):
			started, err := start(busy, now)
			if err != nil {
				return err
			}
			if started {
				retry = time.Time{}
			}
		}
		if broken && !told {
			told = d.tellBroken(ctx, b, brokenAfter)
		}
		if storeOK {
			if err := d.Beat(ctx, state.Asleep); err != nil {
				d.status.BeatError = err.Error()
			} else {
				d.status.BeatError, d.status.Beats, d.status.LastBeat = "", d.status.Beats+1, now
			}
		}
		if d.m.Challenge != Quiet { // the nonce says which challenge a pong answers; its at is the store's clock, never compared with ours
			if p, found, err := d.Pong(); err == nil && found {
				d.m.Pong(p.At, p.Nonce)
			}
		}
		d.flush(now)
	}
	if busy != nil && busy.running {
		busy.cancel()
	}
	return nil
}

// tellBroken sends the coordinator one message that the session is broken:
// to the seat the last ping named, else Coordinator. It answers whether
// the word went out, or there was no one to tell (said on the record); a
// send that fails is tried again the next step.
func (d *Daemon) tellBroken(ctx context.Context, b *bus.Bus, brokenAfter int) bool {
	to := d.m.Seat
	if to == "" {
		to = d.Coordinator
	}
	s := d.status
	if to == "" {
		d.Record("session broken, and no coordinator to tell: no ping has named the seat and --coordinator is not set")
		return true
	}
	subject := fmt.Sprintf("friend %s: session %s broken: %s", d.Friend, s.SessionID, s.SessionReason)
	body := subject + fmt.Sprintf("\nThe provider refused %d turns in a row the same way. The daemon delivers nothing into the session until it restarts; every message stays pending, none given up. Renew the session, then restart the daemon (nova-friend install again, or launchctl kickstart -k gui/<uid>/com.nova.friend-%s).\n", brokenAfter, d.Friend)
	if _, err := b.Send(ctx, bus.Message{From: d.Friend, To: []string{to}, Subject: subject, Body: body}); err != nil {
		d.status.StoreError = "telling " + to + " the session is broken: " + err.Error()
		return false
	}
	return true
}

// seatOf is the seat a ping names, else its sender.
func seatOf(seat string, m bus.Message) string {
	if seat == "" {
		return m.From
	}
	return seat
}

// daemonPong answers a ping at once, from the daemon: transport is up.
// A send that fails is the store's error on the status.
func (d *Daemon) daemonPong(ctx context.Context, b *bus.Bus, ping bus.Message, nonce string, asleep bool) {
	state := ""
	if asleep {
		state = " asleep=true"
	}
	_, err := b.Send(ctx, bus.Message{From: d.Friend, To: []string{ping.From}, Subject: DaemonPongSubject, Re: ping.ID, Body: "daemon-pong " + nonce + state + "\n"})
	if err != nil {
		d.status.StoreError = "daemon pong: " + err.Error()
	}
}

func (d *Daemon) sessionState() (SessionState, error) {
	if d.StateDir == "" {
		return SessionState{Coordinator: d.Coordinator}, nil
	}
	return ReadSessionState(d.StateDir)
}

func entryBefore(a, b string) bool {
	am, as, _ := strings.Cut(a, "-")
	bm, bs, _ := strings.Cut(b, "-")
	x, _ := strconv.ParseUint(am, 10, 64)
	y, _ := strconv.ParseUint(bm, 10, 64)
	if x != y {
		return x < y
	}
	x, _ = strconv.ParseUint(as, 10, 64)
	y, _ = strconv.ParseUint(bs, 10, 64)
	return x < y
}

// flush writes the status when it changed, and every StatusEvery anyway,
// so a reader tells a live daemon from a dead one by the file's age.
func (d *Daemon) flush(now time.Time) {
	s := d.status
	s.Connection, s.LastPing, s.Seat, s.SeatSince = d.m.Connection, d.m.LastPing, d.m.Seat, d.m.SeatSince
	s.Challenge, s.Nonce, s.LastPong, s.Pongs = d.m.Challenge, d.m.Nonce, d.m.LastPong, d.m.Pongs
	s.At, s.LastBeat, s.Beats = time.Time{}, time.Time{}, 0 // what every beat changes is not a change
	if s == d.written0 && now.Sub(d.written) < StatusEvery {
		return
	}
	d.written0, d.written = s, now
	s.At, s.LastBeat, s.Beats = now, d.status.LastBeat, d.status.Beats
	if err := d.Status(s); err != nil {
		if now.Sub(d.statusErrAt) >= StatusErrorEvery {
			d.statusErrAt = now
			d.Record(now.UTC().Format(time.RFC3339) + " status: " + err.Error() + " (said once per " + StatusErrorEvery.String() + "; the beat goes on)")
		}
	}
}
