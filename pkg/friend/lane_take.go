package friend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The take before the lane (v1.2.6; the fault of 2026-10-10 20:14Z on a friend host). The server is
// the one source of truth for what her lanes work: a card is hers to run only once the server
// has moved it to working on her row. Before this, a free lane was handed any card on her row,
// ready or working (nextCard), so with the sprint started a second after her daemon staged its
// dealt cards, 23 lanes ran cards still ready on her row, every progress and finish was refused
// ("not working (it is friend.<name>:ready)"), and the lanes set the cards aside, stranded.
//
// Now a free lane runs a card only when the server says it is working on her row (a card her
// own finish took for her, friendNext, or one a lane held before a restart: resumed, never a
// local guess), or after its take by id (LaneTakeArgv) is answered OK. A refused take (the
// machine STOPPED, her width full, the card gone) runs nothing and changes nothing: the card
// stays where the server has it and is taken again on a later pass, after TakeRetry. In batch
// mode, where no lane exists to take, the daemon takes her ready cards up to her width each
// BatchTakeEvery (BatchTakeArgv; the server cuts it to her room). A card a lane set aside
// that the server still has ready on her row (its take or its finish refused) is no longer
// held aside: its lane-written REPORT.md is moved off and it is taken again (reclaimRefused).
// The model is tla/FriendLaneTake.tla.

// TakeRetry is how long the lanes wait after a refused take before they ask again.
const TakeRetry = 5 * time.Second

// BatchTakeEvery is how often a batch-mode daemon asks the server to take her ready cards.
const BatchTakeEvery = 15 * time.Second

// TakeWait bounds one take to the sprint server.
const TakeWait = 10 * time.Second

// LaneTakeArgv is a lane's take of one card on her row by id: `take --as friend.<name>
// <card>@<gen> --epoch <n>`, ready -> working, refused while the machine is STOPPED, past
// her width, or when the card is not ready on her row.
func LaneTakeArgv(friend string, c Card) []string {
	return []string{"take", "--as", "friend." + friend, c.ID + "@" + strconv.Itoa(c.Gen()), "--epoch", c.Epoch()}
}

// BatchTakeArgv is a batch-mode daemon's take of up to n of her ready cards: `take --as
// friend.<name> --max <n>`, the server choosing which, in its order, cut to her room.
func BatchTakeArgv(friend string, n int) []string {
	return []string{"take", "--as", "friend." + friend, "--max", strconv.Itoa(max(n, 1))}
}

// laneTake is the take before a lane starts card c: true when the card is the lane's to run
// (the server says it is working on her row, or its take was answered OK), false when the
// lane runs nothing this pass. With no Take wired (a test's world) every card is the lane's.
func (l *loop) laneTake(lane int, c Card, now time.Time) bool {
	d, s := l.d, l.lanes
	if d.Take == nil {
		return true
	}
	job := filepath.Base(c.Outbox)
	if h, ok := d.heldOf(job); ok && h.Col == "working" {
		return true // hers already on the server: resumed, no take
	}
	if now.Before(s.takeAt) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(l.ctx), TakeWait)
	err := d.Take(ctx, LaneTakeArgv(d.Friend, c))
	cancel()
	at := now.UTC().Format(time.RFC3339)
	if err != nil {
		s.takeAt = now.Add(TakeRetry)
		why := oneLine(err.Error(), 300)
		if s.takeSaid != why { // said once while it stands, never once a lane
			s.takeSaid = why
			d.Record(fmt.Sprintf("%s lane %d: card %s not started: its take was refused: %s; nothing runs, the card stays where the server has it, taken again in %s", at, lane, c.ID, why, TakeRetry))
		}
		return false
	}
	s.takeSaid = ""
	d.Record(fmt.Sprintf("%s lane %d: took card %s@%d (ready -> working on her row)", at, lane, c.ID, c.Gen()))
	return true
}

// batchTake is a batch-mode daemon's take of her ready cards, once a BatchTakeEvery while the
// server says any is ready on her row and the machine runs; a refusal is said once while it
// stands, and asked again at the next.
func (l *loop) batchTake(now time.Time) {
	d := l.d
	if d.Take == nil || l.mode != ModeBatch || d.machineStopped() || now.Before(d.batchTakeAt) {
		return
	}
	ready := 0
	for _, h := range d.heldCards {
		if h.Col == "ready" && h.Kind != "read" {
			ready++
		}
	}
	if ready == 0 {
		return
	}
	d.batchTakeAt = now.Add(BatchTakeEvery)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(l.ctx), TakeWait)
	err := d.Take(ctx, BatchTakeArgv(d.Friend, ready))
	cancel()
	at := now.UTC().Format(time.RFC3339)
	if err != nil {
		if why := oneLine(err.Error(), 300); why != d.batchTakeSaid {
			d.batchTakeSaid = why
			d.Record(fmt.Sprintf("%s take: her %d ready card(s) not taken: %s; asked again in %s", at, ready, why, BatchTakeEvery))
		}
		return
	}
	d.batchTakeSaid = ""
}

// laneReportMark is in every REPORT.md a lane writes itself (EndReport).
const laneReportMark = "\nnova-friend lane "

// reclaimRefused is the lanes' set-aside cards the server still has ready on her row: their
// take or their finish was refused, so the server never counted the run. Each is held aside
// no longer, and its REPORT.md, when the lane wrote it, is moved to REPORT.md.untaken-<unix>,
// so a free lane takes it again; a REPORT.md the friend wrote is left, and so is the card.
func (l *loop) reclaimRefused(now time.Time) {
	d, s := l.d, l.lanes
	if !s.loaded || len(s.given) == 0 {
		return
	}
	at := now.UTC().Format(time.RFC3339)
	changed := false
	for _, h := range d.heldCards {
		if h.Col != "ready" || !s.given[h.Job] || !validJob(h.Job) {
			continue
		}
		report := filepath.Join(d.Dir, "outbox", h.Job, "REPORT.md")
		if raw, err := os.ReadFile(report); err == nil {
			if !strings.Contains(string(raw), laneReportMark) {
				continue // the friend's own report: hers to finish, never moved by the daemon
			}
			if err := os.Rename(report, report+".untaken-"+strconv.FormatInt(now.Unix(), 10)); err != nil {
				d.Record(fmt.Sprintf("%s lanes: card %s is ready on her row and was set aside, and its REPORT.md could not be moved off: %s", at, h.Card, oneLine(err.Error(), 300)))
				continue
			}
		} else if !os.IsNotExist(err) {
			continue
		}
		if exists(filepath.Join(d.Dir, "outbox", h.Job, "RESULT.md")) {
			continue
		}
		delete(s.given, h.Job)
		s.state.GivenUp = slices.DeleteFunc(s.state.GivenUp, func(j string) bool { return j == h.Job })
		changed = true
		d.Record(fmt.Sprintf("%s lanes: card %s was set aside but is ready on her row (its take or finish was refused): held aside no longer, a free lane takes it again", at, h.Card))
	}
	if changed {
		l.saveLanes(now)
	}
}
