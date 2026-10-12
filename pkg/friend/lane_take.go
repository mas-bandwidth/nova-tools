package friend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/atomicfile"
)

// A friend's lanes are thin clients of the sprint server (v1.2.6; the owner, 2026-10-10:
// "always client/server, not distributed programming"). The server (nova-sprint, Redis, keyed
// by epoch) is the one truth for what each lane holds: a working card on her row carries the
// lane that holds it. A lane keeps nothing that decides what it holds. One loop per lane:
//
//  1. Ask. A free lane sends `take --as friend.<name> --lane <n>` (LaneTakeArgv): its friend
//     and its lane, nothing else; the server picks the epoch and answers the card the lane
//     holds (after a crash, a restart or a clear the lane asks the same way and continues what
//     the server says it holds), else gives it one (a working card no lane holds, else her next
//     ready card, taken as her start), on one line: LANE friend.<name> lane=<n> epoch=<e>
//     holds=<card>@<gen> kind=<k> job=<job> (ParseLaneHold).
//  2. Run exactly what the answer names, its brief from her inbox (written from the server's
//     friend cards), staged when its job is.
//  3. Report by the ids the server gave: progress and finish name the card at its generation,
//     the epoch the server said, and the lane (ProgressArgv, FinishArgv).
//  4. Any refusal, of the ask or of a report: the lane drops the job and asks again. A lane
//     never says "took" unless the server's line says it holds the card.
//
// A reader's reads go through the same loop (read_lanes.go): `take --as reader-<name> --lane
// <n>` begins its next read. The model is tla/FriendLane.tla (WorkingOnlyOnTheServersSay,
// OneRequestApart, NoStaleStep, WorkingResolves, AllDone; reversed witnesses
// MCFriendLaneBroken*.cfg: RunOnNone is the "took" logged with the server still showing the
// card ready, LocalEpoch is progress at epoch 0 from a job directory that named none).

// TakeRetry is how long a lane waits after a refused ask, or an answer it cannot run yet (its
// brief not in her inbox, its job not staged), before it asks again.
const TakeRetry = 5 * time.Second

// LaneIdleAsk is how long a lane the server gave nothing waits before it asks again.
const LaneIdleAsk = 15 * time.Second

// BatchTakeEvery is how often a batch-mode daemon asks the server to take her ready cards.
const BatchTakeEvery = 15 * time.Second

// TakeWait bounds one ask to the sprint server.
const TakeWait = 10 * time.Second

// LaneTakeArgv is lane n's ask of the sprint server: what it holds, else its next card. It
// names the worker's row and the lane and nothing else: no card, no epoch.
func LaneTakeArgv(member string, n int) []string {
	return []string{"take", "--as", member, "--lane", strconv.Itoa(n)}
}

// LaneHold is the server's answer to a lane's ask: the lane, the epoch the server holds, and
// the card the lane holds there (Card "" for none) at its generation, its kind (work or read)
// and its job (<id>~<epoch>, .g<gen> from the second generation).
type LaneHold struct {
	Lane  int
	Epoch uint64
	Card  string
	Gen   int
	Kind  string
	Job   string
}

var laneLineRE = regexp.MustCompile(`LANE (\S+) lane=(\d+) epoch=(\d+) holds=(\S+)(?: kind=(\S+) job=(\S+))?`)

// ParseLaneHold reads the server's LANE line for member's lane n out of an ask's answer. An
// answer with no such line, or one naming another member or lane, is a refusal: the lane
// runs nothing (the fault of 2026-10-10 21:42Z: a take answered OK and the lane said "took"
// while the server still showed the card ready).
func ParseLaneHold(member string, n int, out string) (LaneHold, error) {
	for _, line := range strings.Split(out, "\n") {
		m := laneLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		lane, _ := strconv.Atoi(m[2])
		if m[1] != member || lane != n {
			return LaneHold{}, &Refused{Why: "the server's LANE line names another member's lane or another lane"}
		}
		epoch, err := strconv.ParseUint(m[3], 10, 64)
		if err != nil {
			return LaneHold{}, &Refused{Why: "the server's LANE line names no epoch"}
		}
		h := LaneHold{Lane: n, Epoch: epoch, Kind: m[5], Job: m[6]}
		if m[4] == "-" {
			return h, nil
		}
		id, g, ok := strings.Cut(m[4], "@")
		gen, err := strconv.Atoi(g)
		if !ok || id == "" || err != nil || gen < 1 || h.Job == "" || !validJob(h.Job) {
			return LaneHold{}, &Refused{Why: "the server's LANE line names no card at a generation and its job"}
		}
		h.Card, h.Gen = id, gen
		return h, nil
	}
	// a fixed sentence, never the answer: what the server printed is the record's, not the error's
	return LaneHold{}, &Refused{Why: "the server answered with no LANE line, so the lane holds nothing it can name"}
}

// BatchTakeArgv is a batch-mode daemon's take of up to n of her ready cards: `take --as
// friend.<name> --max <n> --epoch <e>`, the server choosing which, in its order, cut to her
// room. The server refuses every worker's verb sent to it that names no epoch ("a worker's
// verb sent to the server names the epoch its worker holds"), so the take names the epoch her
// ready cards were handed at, the newest one her row last said.
func BatchTakeArgv(friend string, n int, epoch uint64) []string {
	return []string{"take", "--as", "friend." + friend, "--max", strconv.Itoa(max(n, 1)), "--epoch", strconv.FormatUint(epoch, 10)}
}

// laneAsk is a free lane asking the server what it holds: the card the answer names, when the
// lane can run it now (its brief in her inbox, its job staged, no report of a run before it in
// her outbox); false when the lane runs nothing this pass and asks again later. A refusal is
// said once while it stands; "took" is said only when the server's line names the card.
func (l *loop) laneAsk(ln *lane, now time.Time) (Card, bool) {
	d, s := l.d, l.lanes
	if now.Before(ln.askAt) || (ln.asked && now.Before(s.noneUntil)) {
		// a lane that has asked since the daemon started waits while another was just given
		// nothing: one ask a LaneIdleAsk for an idle row, not one a lane (a lane's first ask, after
		// a start, is never held back: the server may say it holds a card)
		return Card{}, false
	}
	at := now.UTC().Format(time.RFC3339)
	member := "friend." + d.Friend
	ctx, cancel := context.WithTimeout(context.WithoutCancel(l.ctx), TakeWait)
	out, err := d.Take(ctx, LaneTakeArgv(member, ln.n))
	cancel()
	var h LaneHold
	if err == nil {
		h, err = ParseLaneHold(member, ln.n, out)
	}
	ln.asked = true
	if err != nil {
		ln.askAt = now.Add(TakeRetry)
		l.laneSay(ln, fmt.Sprintf("%s lane %d: asked the server what it holds: refused: %s; it runs nothing and asks again in %s", at, ln.n, oneLine(err.Error(), 300), TakeRetry))
		return Card{}, false
	}
	if h.Card == "" {
		ln.askAt, s.noneUntil = now.Add(LaneIdleAsk), now.Add(LaneIdleAsk)
		l.laneSay(ln, "")
		return Card{}, false
	}
	c := Card{ID: h.Card, Brief: filepath.Join(d.Dir, "inbox", h.Job, "BRIEF.md"), Outbox: filepath.Join(d.Dir, "outbox", h.Job), Lane: ln.n, ServerEpoch: strconv.FormatUint(h.Epoch, 10), ServerGen: h.Gen}
	for _, other := range l.lanes.lanes {
		if other != ln && other.card != nil && other.card.Outbox == c.Outbox {
			ln.askAt = now.Add(TakeRetry)
			l.laneSay(ln, fmt.Sprintf("%s lane %d: the server says it holds card %s, which lane %d of this daemon runs: it runs nothing and asks again in %s", at, ln.n, c.ID, other.n, TakeRetry))
			return Card{}, false
		}
	}
	wait := ""
	switch held, ok := d.heldOf(h.Job); {
	case exists(c.Result()) || exists(c.Report()):
		// the report of a run before: the outbox step finishes one it can take; one it cannot
		// (no REPORT.md, no Verdict, off its tip, refused) would hold the lane forever, so past
		// ReportWait the lane finishes it itself, failed, by its lane (finishStale)
		if ln.reportJob != h.Job {
			ln.reportJob, ln.reportSince = h.Job, now
		}
		if terminalReport(c) && now.Sub(ln.reportSince) >= ReportWait {
			l.finishStale(ln, c, now)
			ln.askAt, ln.reportJob = now.Add(TakeRetry), ""
			return Card{}, false
		}
		wait = "its outbox holds the report of a run before: the outbox step finishes it on the server"
		if terminalReport(c) {
			wait = "its outbox holds a run's RESULT.md or a REPORT.md with no Verdict, which no outbox step finishes: the lane finishes it failed after " + ReportWait.String()
		}
	case !exists(c.Brief):
		wait = d.say("its brief is not in {their} inbox yet (written from the server's friend cards)")
	case ok && d.stageOwed(held):
		wait = "its job is being staged"
	}
	if wait != "" {
		// what it waits on is her own (her inbox, a stage, the outbox step): asked again at the
		// idle pace, not once a TakeRetry, so lanes that wait do not load the server's line
		ln.askAt = now.Add(LaneIdleAsk)
		l.laneSay(ln, fmt.Sprintf("%s lane %d: holds card %s@%d at epoch %d on the server; not started yet: %s", at, ln.n, c.ID, h.Gen, h.Epoch, wait))
		return Card{}, false
	}
	ln.reportJob = ""
	l.laneSay(ln, "")
	how := "holds"
	if strings.Contains(out, "MOVED "+h.Card+" ") {
		how = "took"
	}
	d.Record(fmt.Sprintf("%s lane %d: %s card %s@%d (kind %s, epoch %d): the server says lane %d holds it", at, ln.n, how, c.ID, h.Gen, dash(h.Kind), h.Epoch, ln.n))
	return c, true
}

// ReportWait is how long a lane the server says holds a card whose outbox has the report of a
// run before that no outbox step can ever finish (a RESULT.md alone, a REPORT.md with no
// Verdict: a run cut between its writes) waits before it finishes it failed itself. A report
// with a Verdict is the outbox step's alone, whatever it waits on (a LAND's head not yet
// origin's tip, a tip that cannot be read, a finish not taken): the lane never finishes it.
const ReportWait = 2 * time.Minute

// terminalReport says card c's outbox holds a report of a run before that no outbox step will
// finish: no REPORT.md with a Verdict line.
func terminalReport(c Card) bool {
	raw, err := os.ReadFile(c.Report())
	if err != nil {
		return true
	}
	v, _ := reportLine(string(raw), "Verdict")
	return v == ""
}

// finishStale is a lane's finish of a card it holds whose outbox has a run's RESULT.md or a
// REPORT.md with no Verdict, which no outbox step finishes: failed, by the lane and the
// server's ids, with a report the lane writes (EndReport) naming why, written as REPORT.md when
// there is none. A refusal is said; the lane asks the server again either way.
func (l *loop) finishStale(ln *lane, c Card, now time.Time) {
	d := l.d
	head, branch := PushedHead(d.Dir, c)
	report := EndReport(d.Friend, ln.n, c, LaneEnd{Err: "the outbox held a run's RESULT.md or a REPORT.md with no Verdict, which no outbox step finishes"}, head, branch)
	if !exists(c.Report()) {
		if err := os.MkdirAll(c.Outbox, 0o755); err == nil {
			_ = atomicfile.WriteFile(c.Report(), []byte(report), 0o644) // ignored: the finish below carries the same words
		}
	}
	l.sendStale(ln, c, FinishArgv(d.Friend, c, report, head, branch), now)
}

// sendStale sends a lane's finish of a card whose outbox's report of a run before no step
// finished, and says it.
func (l *loop) sendStale(ln *lane, c Card, argv []string, now time.Time) {
	d := l.d
	at := now.UTC().Format(time.RFC3339)
	if d.Finish == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(l.ctx), FinishWait)
	err := d.Finish(ctx, argv)
	cancel()
	how := "finished"
	if slices.Contains(argv, "--failed") {
		how = "finished failed"
	}
	if err != nil {
		d.Record(fmt.Sprintf("%s lane %d: card %s: its outbox's report of a run before was not finished in %s, and the lane's finish was refused: %s", at, ln.n, c.ID, ReportWait, oneLine(err.Error(), 300)))
		return
	}
	d.Record(fmt.Sprintf("%s lane %d: card %s: its outbox's report of a run before was not finished in %s: %s by lane %d sent=server", at, ln.n, c.ID, ReportWait, how, ln.n))
}

// laneSay says a lane's line once while it stands ("" clears it).
func (l *loop) laneSay(ln *lane, line string) {
	_, key, _ := strings.Cut(line, " ") // the line without its time
	if key == ln.said {
		return
	}
	ln.said = key
	if line != "" {
		l.d.Record(line)
	}
}

// laneDrop is a lane told by the server that it no longer holds its card (a report refused,
// her row read with the card elsewhere): its run is stopped, nothing is finished by it, and
// the lane asks the server again.
func (l *loop) laneDrop(ln *lane, why string, now time.Time) {
	if ln.card == nil || ln.ended != "" {
		return
	}
	ln.ended = "the server"
	if ln.t != nil && ln.t.cancel != nil {
		ln.t.cancel()
	}
	l.d.Record(fmt.Sprintf("%s lane %d: card %s dropped: %s; its run is stopped, nothing is finished by this lane, and the lane asks the server what it holds", now.UTC().Format(time.RFC3339), ln.n, ln.card.ID, oneLine(why, 300)))
	if ln.t == nil {
		l.setDown(ln, now)
	}
}

// serverLanes is the server holding what each lane holds: the daemon asks it (Take wired).
func (l *loop) serverLanes() bool { return l.d.Take != nil }

// laneCheck is each lane holding a card checked against her row as the server last said it:
// a card no longer working on her row, or held there by another lane, is dropped
// (OneRequestApart: the lane and the server disagree for no longer than one ask).
func (l *loop) laneCheck(now time.Time) {
	d := l.d
	if d.Held == nil || !d.status.HeldKnown {
		return
	}
	for _, ln := range l.lanes.lanes {
		if ln.card == nil || ln.ended != "" || ln.card.Lane == 0 || !d.heldAt.After(ln.marked) {
			// a row read asked before the lane was given its card says nothing of it: the card
			// was ready there, or not yet dealt (only a later read can disagree with the lane)
			continue
		}
		h, ok := d.heldOf(filepath.Base(ln.card.Outbox))
		switch {
		case !ok:
			if exists(ln.card.Result()) || exists(ln.card.Report()) {
				continue // its own report: the outbox step finished it
			}
			l.laneDrop(ln, d.say("the card left {their} row on the server"), now)
		case h.Col != "working":
			l.laneDrop(ln, "the server has the card "+dash(h.Col)+d.say(" on {their} row, not working"), now)
		case h.Lane != 0 && h.Lane != ln.n:
			l.laneDrop(ln, fmt.Sprintf("the server says lane %d holds it", h.Lane), now)
		}
	}
}

// batchTake is a batch-mode daemon's take of her ready cards, once a BatchTakeEvery while the
// server says any is ready on her row and the machine runs; a refusal is said once while it
// stands, and asked again at the next.
func (l *loop) batchTake(now time.Time) {
	d := l.d
	if d.Take == nil || l.mode != ModeBatch || d.machineStopped() || now.Before(d.batchTakeAt) {
		return
	}
	ready, epoch := 0, uint64(0)
	for _, h := range d.heldCards {
		if h.Col == "ready" && h.Kind != "read" {
			ready++
			epoch = max(epoch, h.Epoch) // the newest: a card of an older epoch is another sprint's
		}
	}
	if ready == 0 {
		return
	}
	d.batchTakeAt = now.Add(BatchTakeEvery)
	at := now.UTC().Format(time.RFC3339)
	if epoch == 0 { // the server refuses a take that names no epoch: none is sent, never a guessed 0
		if why := d.say("no ready card on {their} row carries an epoch"); why != d.batchTakeSaid {
			d.batchTakeSaid = why
			d.Record(d.sayf("%s take: {their} %d ready card(s) not taken: %s; asked again in %s", at, ready, why, BatchTakeEvery))
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(l.ctx), TakeWait)
	_, err := d.Take(ctx, BatchTakeArgv(d.Friend, ready, epoch))
	cancel()
	if err != nil {
		if why := oneLine(err.Error(), 300); why != d.batchTakeSaid {
			d.batchTakeSaid = why
			d.Record(d.sayf("%s take: {their} %d ready card(s) not taken: %s; asked again in %s", at, ready, why, BatchTakeEvery))
		}
		return
	}
	d.batchTakeSaid = ""
}
