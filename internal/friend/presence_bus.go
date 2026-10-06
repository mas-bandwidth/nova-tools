package friend

import (
	"context"
	"strconv"
	"strings"
	"time"
)

// Presence on the bus store (docs/SPEC-FRIEND.md, "Presence"). One hash per
// friend, written by the daemon every step the store answers. State is
// PresenceState, a pure function of that record and a clock. The model is
// tla/Presence.tla (StateIsUp, ServerChangesNothing): up exactly when the
// record is present, seen is within DownAfter and asleep is 0, and no action
// of a server the beat talks to changes the record.

const (
	PresenceAsleep  = "asleep"
	PresenceSeen    = "2006-01-02T15:04:05.000Z" // seen and proved, UTC, milliseconds
	PresenceVersion = "1"
	PresenceTTL     = 60 * time.Second // a minute with no write and the record is gone
	presencePrefix  = "bus2:presence:"
)

// PresenceKey is the hash one friend's presence record lives at.
func PresenceKey(name string) string { return presencePrefix + name }

// PresenceState is up, asleep or down from one presence record and a clock.
// A nil or empty record is down (it is gone). seen within DownAfter and
// asleep 0 is up; asleep 1 within DownAfter is asleep; at exactly DownAfter,
// or older, or unreadable, it is down. A non-empty broken field is down
// whatever seen says.
func PresenceState(fields map[string]string, now time.Time) string {
	if len(fields) == 0 {
		return PresenceDown
	}
	if strings.TrimSpace(fields["broken"]) != "" {
		return PresenceDown
	}
	seen, err := time.Parse(PresenceSeen, strings.TrimSpace(fields["seen"]))
	if err != nil {
		seen, err = time.Parse(time.RFC3339Nano, strings.TrimSpace(fields["seen"]))
		if err != nil {
			return PresenceDown
		}
	}
	if now.Sub(seen) >= DownAfter {
		return PresenceDown
	}
	if fields["asleep"] == "1" {
		return PresenceAsleep
	}
	return PresenceUp
}

// writePresence is the daemon's own fields of its presence record: one HSET
// and a PEXPIRE of PresenceTTL. proved is left out, so a coordinator's proved
// survives the merge. broken and reason are written empty when the session
// is not broken, so a restart clears them.
func (l *loop) writePresence(now time.Time) {
	d := l.d
	if d.Store == nil || d.Friend == "" {
		return
	}
	if l.instance == "" {
		if d.Instance != "" {
			l.instance = d.Instance
		} else {
			l.instance = now.UTC().Format("20060102T150405.000Z") + "-" + d.Friend
		}
	}
	asleep := "0"
	if d.Asleep != nil && d.Asleep() {
		asleep = "1"
	}
	route := "push"
	switch {
	case l.passive:
		route = "passive"
	case l.deferrals > 0:
		route = "defer"
	}
	queue, working, err := ReadQueue(d.Dir)
	if err != nil {
		queue, working = 0, 0
	}
	width := d.status.Width
	if width == 0 {
		width = d.Width
	}
	broken, reason := "", ""
	if l.broken {
		reason = d.status.SessionReason
		if !d.status.BrokenAt.IsZero() {
			broken = d.status.BrokenAt.UTC().Format(time.RFC3339)
		} else {
			broken = "1"
		}
	}
	fields := map[string]string{
		"name": d.Friend, "seen": now.UTC().Format(PresenceSeen), "instance": l.instance,
		"harness": d.Harness, "route": route, "asleep": asleep,
		"queue": strconv.Itoa(queue), "working": strconv.Itoa(working), "width": strconv.Itoa(width),
		"version": PresenceVersion, "broken": broken, "reason": reason,
	}
	if err := d.Store.PutHash(l.ctx, PresenceKey(d.Friend), fields, PresenceTTL); err != nil {
		if l.presenceAt.IsZero() || now.Sub(l.presenceAt) >= StatusErrorEvery {
			l.presenceAt = now
			d.Record(now.UTC().Format(time.RFC3339) + " presence: " + err.Error() + " (said once per " + StatusErrorEvery.String() + "; the loop goes on)")
		}
	}
}

// beatAside runs one beat off the loop. A beat that returns before BeatAside
// is applied before the step goes on, which is what a beat that answers at
// once did when the loop called it inline. A beat that does not is left
// running: the loop does not start another until it returns, and the record
// says so once a minute. That wait is BeatAside on a timer, or BeatWait when
// a test has one, so the wait is never a live clock the test did not ask for.
func (l *loop) beatAside(now time.Time) {
	d := l.d
	if d.Beat == nil {
		return
	}
	if l.beatDone != nil {
		select {
		case err := <-l.beatDone:
			l.finishBeat(err, now)
		default:
			l.noteHung(now)
			return
		}
	}
	ctx, cancel := context.WithCancel(l.ctx)
	done := make(chan error, 1)
	l.beatCancel, l.beatDone, l.beatStart = cancel, done, now
	go func() {
		done <- d.Beat(ctx, d.active)
	}()
	if d.BeatWait != nil {
		d.BeatWait()
		select {
		case err := <-done:
			l.finishBeat(err, now)
		default:
			l.noteHung(now)
		}
		return
	}
	timer := time.NewTimer(BeatAside)
	select {
	case err := <-done:
		stopTimer(timer)
		l.finishBeat(err, now)
	case <-timer.C:
		if l.beatCancel != nil {
			l.beatCancel()
		}
		l.noteHung(now)
	case <-l.ctx.Done():
		stopTimer(timer)
	}
}

func stopTimer(t *time.Timer) {
	if t.Stop() {
		return
	}
	select {
	case <-t.C:
	default:
	}
}

func (l *loop) finishBeat(err error, now time.Time) {
	if l.beatCancel != nil {
		l.beatCancel()
		l.beatCancel = nil
	}
	l.beatDone = nil
	l.applyBeat(err, now)
}

func (l *loop) applyBeat(err error, now time.Time) {
	d := l.d
	if err != nil {
		d.status.BeatError = err.Error()
		if l.beatErrAt.IsZero() || now.Sub(l.beatErrAt) >= StatusErrorEvery {
			l.beatErrAt = now
			d.Record(now.UTC().Format(time.RFC3339) + " beat: " + err.Error() + " (said once per " + StatusErrorEvery.String() + "; the loop goes on)")
		}
		return
	}
	d.status.BeatError, d.status.Beats, d.status.LastBeat = "", d.status.Beats+1, now
}

// noteHung records a beat that has not answered, once BeatAside has passed
// and then once a StatusErrorEvery. It does not change presence.
func (l *loop) noteHung(now time.Time) {
	if now.Sub(l.beatStart) < BeatAside {
		return
	}
	if !l.beatSaid.IsZero() && now.Sub(l.beatSaid) < StatusErrorEvery {
		return
	}
	l.beatSaid = now
	l.d.Record(now.UTC().Format(time.RFC3339) + " beat: no answer within " + BeatAside.String() + "; the loop goes on")
}
