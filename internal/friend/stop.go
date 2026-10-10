package friend

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The machine's stop cancels jobs (docs/SPEC-SPRINT.md section 14; the owner, 2026-10-08:
// "official machine stop must cancel every active fleet or friend sprint job, preserve
// progress, return work AND reads to their same owner ready pool automatically"). The
// daemon reads the machine's word off its own beat's answer (`FRIEND-BEAT OK ...
// machine=STOPPED`, ParseMachine; the owner's wiring hands it in as MachineStopped), and
// while the word is STOPPED:
//
//   - every lane turn under way is cancelled (its process group signalled, as a provider
//     failure's stopEvery does) and every read under way with it; the job directory, the
//     branch and the lane's log are left as they are;
//   - each cancelled card is owed a stop-return, recorded in the lane state on disk
//     (LaneState.StopReturns: lane, job, card@gen, epoch, exit, when) before anything is
//     sent, so a daemon restarted mid-stop finishes the stop-returns it owes before it
//     starts anything (StopReturnsSurviveRestart);
//   - once the turn has ended the card is handed back with
//     `nova-sprint stop-return --as <row> <card>@<gen> --epoch <epoch> --reason "owned
//     process stopped"` (the store's verb: the same card back to ready, a read to asked, at
//     a new generation, idempotent on replay, refused for a worker that is not the owner or
//     a stale generation); a refusal is said and tried again each step while owed;
//   - no lane, turn or read begins (NoLaunchAfterStop: the word is read again right before
//     each start, not only at the step's head), and no work nudge goes into the session
//     (NoNudgeWhileStopped: no idle wake, no dealt-card urging; messages, pings and the
//     present still do);
//   - the beat carries the count of stop-returns still owed (friend beat --stop-returns);
//   - a refused stop-return is tried again after StopReturnRetry, its refusal said once
//     while its text stands (no verb and no line a second);
//   - a lane that took a card and had not launched it when the word turned (the window
//     between the claim and the start) gives the card up the same way: owed, its lane mark
//     removed, never a run gone;
//   - the message lanes (a bus delivery with no card: a turn of messages, a ping, the
//     present) go on while STOPPED: they carry communications and proofs, not work.
//
// RUNNING again lifts the refusal; nothing restarts by itself: the queue carries each card
// again at its new generation and the lanes take it as any card. The model is
// tla/StopCancels.tla (NoLaneWhileStopped, EveryLaneReturnsOnStop, NoLaunchAfterStop,
// StopReturnsSurviveRestart, NoNudgeWhileStopped).

// StopReturnReason is the reason every stop-return names.
const StopReturnReason = "owned process stopped"

// MachineStoppedWord is the machine's state word that cancels jobs.
const MachineStoppedWord = "STOPPED"

// StopReturnsKept bounds the stop-returns the lane state keeps once sent: the record of the
// acks, newest last.
const StopReturnsKept = 200

// StopReturnRetry is how long a refused stop-return waits before it is sent again: the
// store's verb may be absent, and the lane step runs every second.
const StopReturnRetry = StatusErrorEvery

// StopReturn is one card the machine's stop took out of a lane: the evidence (the lane, the
// job, the card at its generation, the epoch, the process's end) and the stop-return owed
// for it, Result "" while it is owed and the server's answer once it took it.
type StopReturn struct {
	Lane   int       `json:"lane"`
	Job    string    `json:"job"`
	Row    string    `json:"row"`  // the owner row the return is sent as: friend.<name>, or the reader row
	Card   string    `json:"card"` // the card id
	Gen    int       `json:"gen"`
	Epoch  string    `json:"epoch"`
	Pid    int       `json:"pid,omitempty"`   // the process signalled, when the harness said it (0: its group, by the lane's context)
	Exit   int       `json:"exit"`            // how the run ended; -1 while it has not
	Ended  bool      `json:"ended"`           // the run has ended: the return may be sent
	At     time.Time `json:"at"`              // when the stop cancelled it
	Tries  int       `json:"tries,omitempty"` // stop-returns sent and refused
	Result string    `json:"result,omitempty"`
	// NextTry is when a refused stop-return is sent again (StopReturnRetry), and Refusal
	// the refusal last said for it, said once while its text stands.
	NextTry time.Time `json:"next_try,omitzero"`
	Refusal string    `json:"refusal,omitempty"`
}

// Owed says the stop-return has not been taken by the server.
func (s StopReturn) Owed() bool { return s.Result == "" }

// StopReturnArgv is the store's verb for one stop-return, as the wire of 2026-10-08 says it.
func StopReturnArgv(row, card string, gen int, epoch string) []string {
	if gen <= 0 {
		return nil
	}
	return []string{"stop-return", "--as", row, card + "@" + strconv.Itoa(gen), "--epoch", epoch, "--reason", StopReturnReason}
}

// ParseMachine reads the machine's word off a beat's answer (`machine=RUNNING` or
// `machine=STOPPED`); ok false when the answer carries none (a server before the word).
func ParseMachine(answer string) (state string, ok bool) {
	for _, w := range strings.Fields(answer) {
		if v, found := strings.CutPrefix(w, "machine="); found {
			return v, true
		}
	}
	return "", false
}

// machineStopped says the machine's word, as the owner last read it, is STOPPED.
func (d *Daemon) machineStopped() bool {
	return d.MachineStopped != nil && d.MachineStopped()
}

// owed counts the stop-returns not yet taken.
func (s LaneState) owed() int {
	n := 0
	for _, r := range s.StopReturns {
		if r.Owed() {
			n++
		}
	}
	return n
}

// stopStep is the lane step's stop part: STOPPED cancels every turn and read under way,
// once each, records what it owes, and sends what it owes whose run has ended; it returns
// whether the machine is STOPPED, so the step starts nothing. The word RUNNING lifts the
// hold, said once, and what is still owed (a restart's) is sent all the same.
func (l *loop) stopStep(now time.Time) bool {
	d, s := l.d, l.lanes
	at := now.UTC().Format(time.RFC3339)
	stopped := d.machineStopped()
	switch {
	case stopped && !s.stopped:
		s.stopped = true
		d.Record(fmt.Sprintf("%s machine STOPPED: every lane and read under way is cancelled and handed back with stop-return; nothing starts until it runs again", at))
	case !stopped && s.stopped:
		s.stopped = false
		d.Record(fmt.Sprintf("%s machine RUNNING: the lanes take cards again, each at the generation the queue carries", at))
	}
	if stopped {
		for _, ln := range s.lanes {
			if ln.card != nil && ln.t == nil {
				l.releaseHeld(ln, now) // taken, not launched: given up the same way
				continue
			}
			t := ln.t
			if t == nil || !t.running || t.byStop || ln.card == nil {
				continue
			}
			t.byStop = true
			job := filepath.Base(ln.card.Outbox)
			l.oweStopReturn(StopReturn{Lane: ln.n, Job: job, Row: "friend." + d.Friend, Card: ln.card.ID, Gen: ln.card.Gen(), Epoch: ln.card.Epoch(), Exit: -1, At: now})
			delete(s.state.Started, job) // not a run gone: a daemon starting up owes it a stop-return, never a FAIL
			if t.cancel != nil {
				t.cancel()
			}
			d.Record(fmt.Sprintf("%s lane %d: card %s@%d cancelled by stop: its process group is told to end; the job directory and the branch are kept; stop-return owed", at, ln.n, ln.card.ID, ln.card.Gen()))
		}
		l.stopReads(now)
	}
	l.returnOwed(now)
	return stopped
}

// oweStopReturn records a stop-return owed, in the lane state, before anything is sent.
func (l *loop) oweStopReturn(r StopReturn) {
	s := l.lanes
	for i := range s.state.StopReturns {
		if s.state.StopReturns[i].Job == r.Job && s.state.StopReturns[i].Owed() {
			return
		}
	}
	s.state.StopReturns = append(s.state.StopReturns, r)
	l.saveLanes(r.At)
	l.d.owed.Store(int64(s.state.owed()))
}

// stopDone is a lane's turn ending after the stop cancelled it: the card leaves the lane's
// hand, never finished, its lane mark released, and its stop-return is sent now that the run
// has ended.
func (l *loop) stopDone(ln *lane, t *turn, r laneResult, now time.Time) {
	d, s := l.d, l.lanes
	at := now.UTC().Format(time.RFC3339)
	job := filepath.Base(ln.card.Outbox)
	exit := r.turn.Exit
	if r.err != nil && exit == 0 {
		exit = -1
	}
	for i := range s.state.StopReturns {
		if s.state.StopReturns[i].Job == job && s.state.StopReturns[i].Owed() {
			s.state.StopReturns[i].Exit, s.state.StopReturns[i].Ended = exit, true
		}
	}
	// the card is nobody's now, not ended: its lane mark goes, so a lane may run it again
	// once the machine runs (one_lane.go: an ended mark would refuse it)
	if err := os.Remove(laneMarkPath(d.Dir, job)); err != nil && !errors.Is(err, os.ErrNotExist) {
		d.Record(fmt.Sprintf("%s lane %d: card %s: its lane mark could not be removed: %s", at, ln.n, ln.card.ID, oneLine(err.Error(), 300)))
	}
	d.Record(fmt.Sprintf("%s lane=%d session=%s subject=%s took=%s exit=%d card=cancelled reason=%q", at, ln.n, ln.session, t.subjects, now.Sub(t.started).Round(time.Millisecond), exit, "cancelled by the machine's stop; never finished"))
	ln.card, ln.attempts, ln.job = nil, 0, LaneJob{}
	l.saveLanes(now)
	l.returnOwed(now)
}

// returnOwed sends every stop-return owed whose run has ended, one verb each, through the
// daemon's StopReturn (else its Sprint); the server's answer is recorded on the record,
// which is kept (StopReturnsKept) as the ack's evidence, and a refusal is tried again
// next step.
func (l *loop) returnOwed(now time.Time) {
	d, s := l.d, l.lanes
	at := now.UTC().Format(time.RFC3339)
	changed := false
	for i := range s.state.StopReturns {
		r := &s.state.StopReturns[i]
		if !r.Owed() || !r.Ended || now.Before(r.NextTry) {
			continue
		}
		if r.Gen <= 0 {
			if g := l.resolveStopReturnGen(r); g > 0 {
				r.Gen = g
			} else {
				continue // a read card whose gen is unknown waits for its gen, never sends 0
			}
		}
		argv := StopReturnArgv(r.Row, r.Card, r.Gen, r.Epoch)
		if len(argv) == 0 {
			continue
		}
		var err error
		switch {
		case d.StopReturn != nil:
			err = d.StopReturn(stopReturnCtx(l.ctx), argv)
		case d.Sprint != nil:
			_, err = d.Sprint(stopReturnCtx(l.ctx), argv)
		default:
			err = fmt.Errorf("this daemon sends no stop-return")
		}
		changed = true
		if err != nil {
			r.Tries++
			r.NextTry = now.Add(StopReturnRetry)
			if text := oneLine(err.Error(), 300); r.Refusal != text {
				r.Refusal = text
				d.Record(fmt.Sprintf("%s stop-return refused lane=%d card=%s@%d epoch=%s pid=%d exit=%d try=%d: %s; tried again every %s while it stands", at, r.Lane, r.Card, r.Gen, r.Epoch, r.Pid, r.Exit, r.Tries, text, StopReturnRetry))
			}
			continue
		}
		r.Result, r.Refusal, r.NextTry = "ok "+at, "", time.Time{}
		d.Record(fmt.Sprintf("%s stop-return OK lane=%d card=%s@%d epoch=%s pid=%d exit=%d: handed back to %s by the machine's stop", at, r.Lane, r.Card, r.Gen, r.Epoch, r.Pid, r.Exit, r.Row))
	}
	if changed {
		// the acks are kept, newest last; what is owed is never dropped
		if n := len(s.state.StopReturns); n > StopReturnsKept {
			kept := slices.DeleteFunc(slices.Clone(s.state.StopReturns[:n-StopReturnsKept]), func(r StopReturn) bool { return !r.Owed() })
			s.state.StopReturns = append(kept, s.state.StopReturns[n-StopReturnsKept:]...)
		}
		l.saveLanes(now)
	}
	d.owed.Store(int64(s.state.owed()))
}

// resolveStopReturnGen attempts to resolve the card's generation if it was unpopulated (e.g. read card).
func (l *loop) resolveStopReturnGen(r *StopReturn) int {
	if r.Gen > 0 {
		return r.Gen
	}
	if l.reads != nil {
		if a, ok := l.reads.active[r.Card]; ok && a.Gen > 0 {
			return a.Gen
		}
		for _, a := range l.reads.asked {
			if a.ID == r.Card && a.Gen > 0 {
				return a.Gen
			}
		}
	}
	return 0
}

// OwedStopReturns is how many stop-returns the lanes owe now: the beat carries it.
func (d *Daemon) OwedStopReturns() int { return int(d.owed.Load()) }

// stopReads cancels every read under way, once each, and owes its stop-return as the
// reader row's.
func (l *loop) stopReads(now time.Time) {
	d, s := l.d, l.reads
	at := now.UTC().Format(time.RFC3339)
	for id, cancel := range s.cancel {
		if s.stopped[id] {
			continue
		}
		s.stopped[id] = true
		// the read as it was begun (readSet.active): the queue's refresh lists it begun, not
		// asked, once the begin landed, so the asked list no longer names its generation
		r, known := s.active[id]
		if !known {
			if i := slices.IndexFunc(s.asked, func(a AskedRead) bool { return a.ID == id }); i >= 0 {
				r = s.asked[i]
			}
		}
		epoch := r.Epoch
		if epoch == "" && s.epoch != "" {
			epoch = s.epoch
		}
		if epoch == "" {
			for _, a := range s.asked {
				if a.Epoch != "" {
					epoch = a.Epoch
					break
				}
			}
		}
		l.oweStopReturn(StopReturn{Job: "read:" + id, Row: ReaderOf(d.Friend), Card: id, Gen: r.Gen, Epoch: epoch, Exit: -1, At: now})
		cancel()
		d.Record(fmt.Sprintf("%s read %s@%d cancelled by stop: its process group is told to end; stop-return owed", at, id, r.Gen))
	}
}

// stopReadDone is a read ending after the stop cancelled it: no verdict and no return are
// recorded; its stop-return is sent now that the run has ended.
func (l *loop) stopReadDone(r readResult, now time.Time) {
	d, s := l.d, l.lanes
	exit := r.turn.Exit
	if r.err != nil && exit == 0 {
		exit = -1
	}
	for i := range s.state.StopReturns {
		if s.state.StopReturns[i].Job == "read:"+r.read.ID && s.state.StopReturns[i].Owed() {
			s.state.StopReturns[i].Exit, s.state.StopReturns[i].Ended = exit, true
		}
	}
	d.Record(fmt.Sprintf("%s read %s: cancelled by the machine's stop, exit %d; no verdict recorded", now.UTC().Format(time.RFC3339), r.read.ID, exit))
	l.saveLanes(now)
	l.returnOwed(now)
}

// releaseHeld is a lane holding a card it has not launched when the word is STOPPED (the
// window between the claim and the start): the card is given up as a cancelled one is,
// with no run to end: owed with Ended set, out of Started, its lane mark removed.
func (l *loop) releaseHeld(ln *lane, now time.Time) {
	d := l.d
	at := now.UTC().Format(time.RFC3339)
	job := filepath.Base(ln.card.Outbox)
	l.oweStopReturn(StopReturn{Lane: ln.n, Job: job, Row: "friend." + d.Friend, Card: ln.card.ID, Gen: ln.card.Gen(), Epoch: ln.card.Epoch(), Exit: -1, Ended: true, At: now})
	delete(l.lanes.state.Started, job)
	if err := os.Remove(laneMarkPath(d.Dir, job)); err != nil && !errors.Is(err, os.ErrNotExist) {
		d.Record(fmt.Sprintf("%s lane %d: card %s: its lane mark could not be removed: %s", at, ln.n, ln.card.ID, oneLine(err.Error(), 300)))
	}
	d.Record(fmt.Sprintf("%s lane %d: card %s@%d taken and not launched when the machine stopped: given up, stop-return owed", at, ln.n, ln.card.ID, ln.card.Gen()))
	ln.card, ln.attempts, ln.job = nil, 0, LaneJob{}
	l.saveLanes(now)
}

// stopReturnCtx is the context a stop-return is sent under when the loop's own has ended: the
// ack is owed whatever stops the daemon.
func stopReturnCtx(ctx context.Context) context.Context { return context.WithoutCancel(ctx) }
