package friend

import (
	"context"
	"errors"
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
//     holds=<card>@<gen> rank=<d> kind=<k> job=<job> (ParseLaneHold; the fields after holds=
//     in any order, one it does not read skipped).
//  2. Run exactly what the answer names, its brief from her inbox (written from the server's
//     friend cards), staged when its job is.
//  3. Report by the ids the server gave: progress and finish name the card at its generation,
//     the epoch the server said, and the lane (ProgressArgv, FinishArgv).
//  4. Any refusal, of the ask or of a report: the lane drops the job and asks again. A lane
//     never says "took" unless the server's line says it holds the card. The one exception is
//     a LANE line the lane cannot read (Unparseable): it says so once and parks, asking no
//     more until the daemon restarts; the server's record goes by the gone bound
//     (tla/FriendLane.tla ParkedRunsNothing, RefusedOnceNotALoop, ParkedNeverAsks,
//     ParkedRecordIsGone, ResumesAfterRestart; MCFriendLaneParkedBroken*.cfg).
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

// neverAgain is how long a lane that got an Unparseable LANE line waits before it asks again:
// past any daemon's life, so it asks again only when the daemon restarts.
const neverAgain = 100 * 365 * 24 * time.Hour

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

// laneLineRE is the LANE line's head; the words after holds= are key=value fields read in any
// order (docs/SPEC-FRIEND.md, the LANE line): nova-sprint prints rank=<d> between holds= and
// kind=, and a field the lane does not read is skipped, never a refusal.
var laneLineRE = regexp.MustCompile(`LANE (\S+) lane=(\d+) epoch=(\d+) holds=(\S+)(.*)`)

// laneLineGrammar is the LANE line a lane can run, said in an Unparseable refusal.
const laneLineGrammar = "LANE <member> lane=<n> epoch=<e> holds=<card>@<gen> [rank=<d>] kind=<work|read> job=<job>"

// Unparseable is a LANE line that names the lane that asked but that the lane cannot read: a
// field it needs (gen, kind, job) missing or malformed. It is a Refused too. Asking again gets
// the same line from the same server, so the lane says it once and asks no more until the
// daemon restarts (laneAsk), rather than once a TakeRetry forever.
type Unparseable struct {
	Field string // the field the line is missing: gen, kind or job
	Why   string
}

func (u *Unparseable) Error() string { return u.Why }

// Unwrap makes an unparseable line a refusal for errors.As.
func (u *Unparseable) Unwrap() error { return &Refused{Why: u.Why} }

func unparseable(field string) *Unparseable {
	return &Unparseable{Field: field, Why: "the server's LANE line names no " + field + "= the lane can read (a lane runs " + laneLineGrammar + ")"}
}

// ParseLaneHold reads the server's LANE line for member's lane n out of an ask's answer. An
// answer with no such line, or one naming another member or lane, is a refusal: the lane
// runs nothing (the fault of 2026-10-10 21:42Z: a take answered OK and the lane said "took"
// while the server still showed the card ready). A line for this lane that holds a card but
// lacks its generation, kind or job is Unparseable, naming the field (the fault of 2026-10-14
// 03:51Z: rank= between holds= and kind= left a reader-only friend's lanes refusing every read
// card's line, once every TakeRetry, all night).
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
		h := LaneHold{Lane: n, Epoch: epoch}
		for _, w := range strings.Fields(m[5]) {
			k, v, _ := strings.Cut(w, "=")
			switch k {
			case "kind":
				h.Kind = v
			case "job":
				h.Job = v
			}
		}
		if m[4] == "-" {
			return h, nil
		}
		id, g, ok := strings.Cut(m[4], "@")
		gen, err := strconv.Atoi(g)
		switch {
		case !ok || id == "" || err != nil || gen < 1:
			return LaneHold{}, unparseable("gen")
		case h.Kind != "work" && h.Kind != "read":
			return LaneHold{}, unparseable("kind")
		case h.Job == "" || !validJob(h.Job):
			return LaneHold{}, unparseable("job")
		}
		h.Card, h.Gen = id, gen
		return h, nil
	}
	// a fixed sentence, never the answer: what the server printed is the record's, not the error's
	return LaneHold{}, &Refused{Why: "the server answered with no LANE line, so the lane holds nothing it can name"}
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
	if codex, ok := d.Deliver.(*Codex); ok {
		// The server may take any unlaned working card before a ready one.
		// Check every candidate's current tier before asking it to move one;
		// one missing route parks intake without spending a card attempt.
		seen := map[string]bool{}
		for _, held := range d.heldCards {
			if held.Kind == "read" || held.Lane != 0 || held.Col != "working" && held.Col != "ready" || seen[held.Tier] {
				continue
			}
			seen[held.Tier] = true
			var err error
			if codex.ModelForTier == nil {
				err = errors.New("no canonical route reader")
			} else {
				_, err = codex.ModelForTier(l.ctx, held.Tier)
			}
			if err != nil {
				ln.askAt = now.Add(LaneIdleAsk)
				l.laneSay(ln, fmt.Sprintf("%s lane %d: card %s tier %s has no applicable Codex route: %s; no server take", at, ln.n, held.Card, dash(held.Tier), oneLine(err.Error(), 300)))
				return Card{}, false
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(l.ctx), TakeWait)
	out, err := d.Take(ctx, LaneTakeArgv(member, ln.n))
	cancel()
	var h LaneHold
	if err == nil {
		h, err = ParseLaneHold(member, ln.n, out)
	}
	ln.asked = true
	var bad *Unparseable
	if errors.As(err, &bad) {
		// the same server answers the same line: asking again is a loop, not a retry
		// (tla/FriendLane.tla Refuse: the lane parks; RefusedOnceNotALoop, ParkedNeverAsks)
		ln.askAt = now.Add(neverAgain)
		l.laneSay(ln, fmt.Sprintf("%s lane %d: asked the server what it holds: refused: %s; it runs nothing and asks no more until the daemon restarts", at, ln.n, oneLine(err.Error(), 400)))
		return Card{}, false
	}
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
