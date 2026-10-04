package friend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus2"
)

// BeatEvery is how often the daemon beats to the sprint server while its
// loop runs: the sprint's own number (internal/sprint FriendBeatEvery, one
// second; a friend is down after fifteen without one). It is also the
// loop's read block: one read of the stream per beat.
const BeatEvery = time.Second

// MaxDeliveries is how many times a message is handed into the session
// before the daemon gives up on it: a turn that fails leaves the message
// pending and the bus hands it in again once its claim opens (bus2.ClaimAfter);
// the last failure acks it, with the failure on the record, so a message the
// session cannot take never comes back for ever.
const MaxDeliveries = 3

// RecheckEvery is how long the daemon waits before trying a deferred
// delivery again (Deferred: the session cannot take a turn now and nothing
// is wrong). The message stays in the daemon's hand meanwhile: it is never
// put back on the bus, never counted toward MaxDeliveries, never acked.
const RecheckEvery = 10 * time.Second

// StatusErrorEvery bounds how often a status file that cannot be written
// is said in the record: the loop goes on beating and delivering without it.
// DeferredSaidEvery bounds how often a deferral still in hand is said.
const (
	StatusErrorEvery  = time.Minute
	DeferredSaidEvery = time.Minute
)

// The subjects of the daemon's own messages on the bus.
const (
	DaemonPongSubject = "daemon-pong"
	PongSubject       = "pong"
	PingPrefix        = "PING "
)

// Daemon is one friend's loop: the recv loop over the friend's stream with
// the deliver adapter, the beat, and the Machine stepped by what arrives.
// Everything it reaches outside itself is a field, so a test runs it over
// bus2's Fake, a fake harness and its own clock.
type Daemon struct {
	Friend, Harness, Dir string
	Width                int
	Store                bus2.Store
	Deliver              Deliverer
	Beat                 func(ctx context.Context) error // one beat to the sprint server
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
	// binary by path, --as, --dir, --redis), put at the head of every ping
	// pushed in, so a small model has one line to run and nothing to fill in.
	PongCommand func(nonce string) string

	m           *Machine
	status      Status
	written     time.Time
	written0    Status
	statusErrAt time.Time
}

// job is one thing owed to the session: a bus message (acked after exit 0)
// or a push of the daemon's own.
type job struct {
	entry   string // the stream entry to ack, "" for a push
	id      string
	subject string
	text    string
	started time.Time
}

type result struct {
	job  job
	exit int
	err  error
}

// Text is a message as the session reads it, the shape nova-bus2 recv
// prints: the header line, a blank line, the body ending in a newline.
func Text(m bus2.Message) string {
	body := m.Body
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	return fmt.Sprintf("RECV OK id=%s from=%s to=%s cc=%s re=%s at=%s subject=%q\n\n%s",
		m.ID, m.From, dash(strings.Join(m.To, ",")), dash(strings.Join(m.CC, ",")), dash(m.Re), m.At.Format(time.RFC3339), m.Subject, body)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Run is the loop until ctx ends. Each step: the clock; one read of the
// stream when the session is free (a message is handed to the adapter and
// acked when its turn ends at exit 0), else one peek so a ping arriving
// during a long turn is still answered at once by the daemon; the worker's
// result; a beat when the store answered; the session's pong; the status.
// A ping is answered twice: the daemon pong at once (transport), and the
// session's own pong as a turn, which alone makes the friend up.
func (d *Daemon) Run(ctx context.Context) error {
	bus := &bus2.Bus{Store: d.Store}
	_, passive := d.Deliver.(interface{ Passive() })
	d.m = Start(d.Now())
	d.status = Status{Friend: d.Friend, Harness: d.Harness, Started: d.m.LastPing, Width: d.Width}
	answered := map[string]bool{} // entries whose ping the daemon has ponged
	failed := map[string]int{}    // entries whose turn failed, and how often
	var queue []job
	var busy *job
	var retry time.Time // when the deferred turn in hand is tried again; zero while none is
	var deferrals int
	var deferSaid time.Time
	results := make(chan result, 1)
	start := func(j job, now time.Time) {
		j.started = now
		busy = &j
		go func() {
			exit, err := d.Deliver.Deliver(ctx, j.text)
			results <- result{j, exit, err}
		}()
	}
	for ctx.Err() == nil {
		now := d.Now()
		for _, p := range d.m.Tick(now) {
			queue = append(queue, job{subject: p.Subject, text: p.Text})
		}
		if passive {
			// nothing can be pushed in: the session hears of it from its own read
			for _, j := range queue {
				d.Record(now.UTC().Format(time.RFC3339) + " not delivered: " + d.Harness + " has no deliver command: " + j.subject)
			}
			queue = nil
		}
		storeOK := true
		if busy == nil && !passive {
			e, ok, err := bus.Recv(ctx, d.Friend, BeatEvery)
			switch {
			case ctx.Err() != nil:
				return nil
			case err != nil:
				storeOK = false
				d.status.StoreError = err.Error()
				d.Pause(ctx, BeatEvery)
			case ok:
				d.status.StoreError = ""
				msg := e.Message()
				if nonce, seat, since, isPing := ParsePing(msg.Body); isPing {
					if !answered[e.Entry] {
						d.daemonPong(ctx, bus, msg, nonce)
						answered[e.Entry] = true
						// A peek already applied this event while a turn was running.
						for _, p := range d.m.Ping(now, seatOf(seat, msg), since, nonce) {
							queue = append(queue, job{subject: p.Subject, text: p.Text})
						}
					}
				}
				text := Text(msg)
				if nonce, _, _, isPing := ParsePing(msg.Body); isPing && d.PongCommand != nil {
					text = "Run this now, first, exactly as written: " + d.PongCommand(nonce) + "\nThen read on.\n\n" + text
				}
				queue = append(queue, job{entry: e.Entry, id: msg.ID, subject: msg.Subject, text: text})
			default:
				d.status.StoreError = ""
			}
		} else { // a turn is running, or the harness is passive: peek, take nothing
			_, fresh, err := bus.Peek(ctx, d.Friend)
			if ctx.Err() != nil {
				return nil
			}
			if err != nil {
				storeOK = false
				d.status.StoreError = err.Error()
			} else {
				d.status.StoreError = ""
				for _, e := range fresh {
					msg := e.Message()
					if nonce, seat, since, isPing := ParsePing(msg.Body); isPing && !answered[e.Entry] {
						d.daemonPong(ctx, bus, msg, nonce)
						answered[e.Entry] = true
						// the machine sees the ping when the daemon does: a turn longer than a window is no silence
						for _, p := range d.m.Ping(now, seatOf(seat, msg), since, nonce) {
							queue = append(queue, job{subject: p.Subject, text: p.Text})
						}
					}
				}
			}
			d.Pause(ctx, BeatEvery)
		}
		select {
		case r := <-results:
			var deferred Deferred
			if errors.As(r.err, &deferred) { // not a failure: the message stays in hand, tried again, counted toward nothing
				deferrals++
				retry = now.Add(RecheckEvery)
				if deferrals == 1 || now.Sub(deferSaid) >= DeferredSaidEvery {
					deferSaid = now
					d.Record(fmt.Sprintf("%s subject=%q deferred=%d: %s; tried again every %s, counted toward nothing (said once per %s)",
						now.UTC().Format(time.RFC3339), r.job.subject, deferrals, deferred.Reason, RecheckEvery, DeferredSaidEvery))
				}
				break
			}
			line := fmt.Sprintf("%s subject=%q took=%s exit=%d", now.UTC().Format(time.RFC3339), r.job.subject, now.Sub(r.job.started).Round(time.Millisecond), r.exit)
			if r.err != nil {
				line += " error=" + fmt.Sprintf("%q", r.err.Error())
			}
			ok := r.err == nil && r.exit == 0
			if r.job.entry != "" && !ok {
				failed[r.job.entry]++
				line += fmt.Sprintf(" deliveries=%d/%d", failed[r.job.entry], MaxDeliveries)
				if failed[r.job.entry] >= MaxDeliveries {
					line += " given_up=true"
				}
			}
			if r.job.entry != "" && (ok || failed[r.job.entry] >= MaxDeliveries) {
				if _, err := bus.AckEntry(ctx, d.Friend, r.job.entry); err != nil {
					d.status.StoreError = err.Error()
					line += " ack=failed"
				} else {
					if ok {
						d.status.Delivered++
					}
					line += " acked=true"
					delete(failed, r.job.entry)
				}
			}
			d.Record(line)
			busy, retry, deferrals, deferSaid = nil, time.Time{}, 0, time.Time{}
		default:
		}
		switch {
		case busy == nil && len(queue) > 0:
			j := queue[0]
			queue = queue[1:]
			start(j, now)
		case busy != nil && !retry.IsZero() && !now.Before(retry):
			retry = time.Time{}
			start(*busy, now)
		}
		if storeOK {
			if err := d.Beat(ctx); err != nil {
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
	return nil
}

// seatOf is the seat a ping names, else its sender.
func seatOf(seat string, m bus2.Message) string {
	if seat == "" {
		return m.From
	}
	return seat
}

// daemonPong answers a ping at once, from the daemon: transport is up.
// A send that fails is the store's error on the status; the ping still
// goes into the session.
func (d *Daemon) daemonPong(ctx context.Context, bus *bus2.Bus, ping bus2.Message, nonce string) {
	_, err := bus.Send(ctx, bus2.Message{From: d.Friend, To: []string{ping.From}, Subject: DaemonPongSubject, Re: ping.ID, Body: "daemon-pong " + nonce + "\n"})
	if err != nil {
		d.status.StoreError = "daemon pong: " + err.Error()
	}
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
