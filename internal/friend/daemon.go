package friend

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
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

// DaemonConsumer keeps held deliveries separate from interactive receivers.
const DaemonConsumer = "nova-friend-daemon"

// At the bus's 1 MiB body ceiling, one read retains at most 128 MiB of
// payloads transiently. Held work retains IDs, not another copy of bodies.
const DaemonReadBatch = 128

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
	Friend, Harness, Dir  string
	StateDir, Coordinator string
	Width                 int
	Store                 bus2.Store
	Deliver               Deliverer
	Beat                  func(ctx context.Context, asleep bool) error // one beat to the sprint server
	Now                   func() time.Time
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
func (d *Daemon) Run(ctx context.Context) (runErr error) {
	bus := &bus2.Bus{Store: d.Store}
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
	_, passive := d.Deliver.(interface{ Passive() })
	d.m = Start(d.Now())
	d.status = Status{Friend: d.Friend, Harness: d.Harness, Started: d.m.LastPing, Width: d.Width}
	answered := map[string]bool{} // entries whose ping the daemon has ponged
	failed := map[string]int{}    // entries whose turn failed, and how often
	var queue []job
	queueDirty := true
	lastBarrier := state.WakeBarrier
	known := map[string]bool{}
	var busy *job
	var retry time.Time // when the deferred turn in hand is tried again; zero while none is
	var deferrals int
	var deferSaid time.Time
	results := make(chan result, 1)
	start := func(j job, now time.Time) (bool, error) {
		if j.entry != "" {
			entries, err := d.Store.Get(ctx, bus2.StreamOf(d.Friend), []string{j.entry})
			if err != nil {
				return false, err
			}
			if len(entries) != 1 {
				return false, fmt.Errorf("pending delivery %s is missing; reconcile the stream before restarting", j.entry)
			}
			msg := entries[0].Message()
			j.id, j.subject, j.text = msg.ID, msg.Subject, Text(msg)
			if nonce, _, _, isPing := ParsePing(msg.Body); isPing && d.PongCommand != nil {
				j.text = "Run this now, first, exactly as written: " + d.PongCommand(nonce) + "\nThen read on.\n\n" + j.text
			}
		}
		reserved := false
		reserve := func(s SessionState) error {
			state = s
			if !s.Asleep && (s.WakeBarrier == "" || s.WakeBarrier == j.entry) {
				j.started = now
				busy = &j
				reserved = true
			}
			return nil
		}
		if d.StateDir != "" {
			if err := WithSessionState(d.StateDir, reserve); err != nil {
				return false, err
			}
		} else {
			_ = reserve(state)
		}
		if !reserved {
			return false, nil
		}
		// The reservation is the start boundary; harness I/O begins after unlock.
		go func() {
			exit, err := d.Deliver.Deliver(ctx, j.text)
			results <- result{j, exit, err}
		}()
		return true, nil
	}
	handle := func(e bus2.Entry, now time.Time) error {
		msg := e.Message()
		state, err = d.sessionState()
		if err != nil {
			return err
		}
		if d.StateDir != "" && state.Coordinator != "" && msg.From == state.Coordinator {
			s, err := UpdateSessionState(d.StateDir, func(s *SessionState) error {
				if s.Asleep && s.Coordinator != "" && s.Coordinator == msg.From && e.Entry != s.WakeBarrier {
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
		if nonce, seat, since, isPing := ParsePing(msg.Body); isPing && !answered[e.Entry] {
			d.daemonPong(ctx, bus, msg, nonce, state.Asleep)
			answered[e.Entry] = true
			if !state.Asleep {
				var pushes []Push
				if state.Coordinator == "" {
					pushes = d.m.Ping(now, seatOf(seat, msg), since, nonce)
				} else {
					pushes = d.m.ReceivePing(now, msg.From, state.Coordinator, seatOf(seat, msg), since, nonce)
				}
				for _, p := range pushes {
					queueDirty = true
					queue = append(queue, job{subject: p.Subject, text: p.Text})
				}
			}
		}
		if !known[e.Entry] {
			queueDirty = true
			queue = append(queue, job{entry: e.Entry})
			known[e.Entry] = true
		}
		return nil
	}
	// Recover every page of this daemon's PEL; never claim an interactive
	// consumer's current entries. Empty recovery also validates the group.
	for cursor := ""; !passive; {
		entries, next, err := bus.PendingPage(ctx, d.Friend, DaemonConsumer, cursor, DaemonReadBatch)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			d.status.StoreError = err.Error()
			d.flush(d.Now())
			d.Pause(ctx, BeatEvery)
			continue
		}
		d.status.StoreError = ""
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
	if !passive && state.WakeBarrier != "" && !known[state.WakeBarrier] {
		return fmt.Errorf("wake barrier %s is missing from daemon-owned pending deliveries; reconcile missing or foreign-owned entry before restarting", state.WakeBarrier)
	}
	for ctx.Err() == nil {
		now := d.Now()
		state, err = d.sessionState()
		if err != nil {
			return err
		}
		d.status.Asleep = state.Asleep
		if busy != nil && !retry.IsZero() && (state.Asleep || (state.WakeBarrier != "" && state.WakeBarrier != busy.entry)) {
			if busy.entry != "" {
				queueDirty = true
				queue = append(queue, job{entry: busy.entry})
			}
			busy = nil
			retry = time.Time{}
			deferrals = 0
		}
		if state.Asleep {
			// A deferred adapter has returned: park its pending job so a later
			// coordinator wake can take priority without retrying the held turn.
			kept := queue[:0]
			for _, j := range queue {
				if j.entry != "" {
					kept = append(kept, j)
				}
			}
			queue = kept
		}
		for _, p := range d.m.TickWhen(now, state.Asleep) {
			queueDirty = true
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
		barrierQueued := state.WakeBarrier != "" && known[state.WakeBarrier]
		if !passive && (state.Asleep || (state.WakeBarrier != "" && !barrierQueued && busy == nil) || (busy == nil && len(queue) == 0)) {
			var entries []bus2.Entry
			var err error
			if state.Asleep || state.WakeBarrier != "" {
				// Only fresh reads: reclaiming our old held jobs can starve a
				// coordinator wake behind them. Recovery already ensured the group.
				entries, err = d.Store.Read(ctx, bus2.StreamOf(d.Friend), d.Friend, DaemonConsumer, BeatEvery, DaemonReadBatch)
			} else {
				entries, err = bus.RecvBatch(ctx, d.Friend, DaemonConsumer, BeatEvery, 1)
			}
			switch {
			case ctx.Err() != nil:
				return nil
			case err != nil:
				storeOK = false
				d.status.StoreError = err.Error()
				d.Pause(ctx, BeatEvery)
			case len(entries) > 0:
				d.status.StoreError = ""
				for _, e := range entries {
					if err := handle(e, now); err != nil {
						return err
					}
				}
			default:
				d.status.StoreError = ""
			}
		} else if busy != nil || passive { // a turn is running, or the harness is passive: peek, take nothing
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
					if passive && d.StateDir != "" && msg.From == state.Coordinator {
						s, err := UpdateSessionState(d.StateDir, func(s *SessionState) error {
							if s.Coordinator == msg.From {
								s.Asleep = false
							}
							return nil
						})
						if err != nil {
							return err
						}
						state = s
					}
					if nonce, seat, since, isPing := ParsePing(msg.Body); isPing && !answered[e.Entry] {
						d.daemonPong(ctx, bus, msg, nonce, state.Asleep)
						answered[e.Entry] = true
						// the machine sees the ping when the daemon does: a turn longer than a window is no silence
						var pushes []Push
						if state.Coordinator == "" {
							pushes = d.m.Ping(now, seatOf(seat, msg), since, nonce)
						} else {
							pushes = d.m.ReceivePing(now, msg.From, state.Coordinator, seatOf(seat, msg), since, nonce)
						}
						for _, p := range pushes {
							queueDirty = true
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
				if state.Asleep || (state.WakeBarrier != "" && state.WakeBarrier != busy.entry) {
					if busy.entry != "" {
						queueDirty = true
						queue = append(queue, job{entry: busy.entry})
					}
					busy = nil
					retry = time.Time{}
					deferrals = 0
				}
				break
			}
			if d.StateDir != "" && state.WakeBarrier != "" && state.WakeBarrier == r.job.entry {
				s, err := UpdateSessionState(d.StateDir, func(s *SessionState) error {
					if s.WakeBarrier != "" && s.WakeBarrier == r.job.entry {
						s.WakeBarrier = ""
					}
					return nil
				})
				if err != nil {
					return err
				}
				state = s
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
					delete(known, r.job.entry)
				}
			}
			d.Record(line)
			if r.job.entry != "" {
				delete(known, r.job.entry)
			}
			busy, retry, deferrals, deferSaid = nil, time.Time{}, 0, time.Time{}
		default:
		}
		switch {
		case !state.Asleep && busy == nil && len(queue) > 0:
			if queueDirty || lastBarrier != state.WakeBarrier {
				sort.SliceStable(queue, func(i, j int) bool {
					if state.WakeBarrier != "" && (queue[i].entry == state.WakeBarrier) != (queue[j].entry == state.WakeBarrier) {
						return queue[i].entry == state.WakeBarrier
					}
					if queue[i].entry == "" || queue[j].entry == "" {
						return queue[i].entry == "" && queue[j].entry != ""
					}
					return entryBefore(queue[i].entry, queue[j].entry)
				})
				queueDirty, lastBarrier = false, state.WakeBarrier
			}
			j := queue[0]
			started, err := start(j, now)
			if err != nil {
				return err
			}
			if started {
				queue = queue[1:]
			}
		case !state.Asleep && busy != nil && !retry.IsZero() && !now.Before(retry):
			started, err := start(*busy, now)
			if err != nil {
				return err
			}
			if started {
				retry = time.Time{}
			}
		}
		d.status.Asleep = state.Asleep
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
func (d *Daemon) daemonPong(ctx context.Context, bus *bus2.Bus, ping bus2.Message, nonce string, asleep bool) {
	state := ""
	if asleep {
		state = " asleep=true"
	}
	_, err := bus.Send(ctx, bus2.Message{From: d.Friend, To: []string{ping.From}, Subject: DaemonPongSubject, Re: ping.ID, Body: "daemon-pong " + nonce + state + "\n"})
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
