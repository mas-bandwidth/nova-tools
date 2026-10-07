package friend

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

// The daemon reads every outbox job (the night of 2026-10-05: a friend held eight
// working cards whose outbox/<job>/REPORT.md said Verdict: LAND with a Head, unread from
// 21:09 for two hours, because the daemon finished only the cards its own lanes ran and the
// coordinator's delivery stopgap had written those briefs; the tick raised "a friend holds
// working cards and finishes none"). Each reconcile that the server answered, the daemon
// reads every job in her outbox whose name parses to a card (<work>~<epoch>[.g<gen>],
// ParseJob) and finishes it when that card is working on her row, whoever wrote its brief,
// including while a lane's turn is still running: the session's turn writes the report and
// does not finish the card. LAND with a full sha Head finishes with that head, HOLD and
// FAIL (any other verdict too) finish --failed with the report's first 600 characters. A
// report with no Verdict line is noted once and left. A report whose card is not on her
// row, or whose generation is not the row's, is logged superseded and not finished. A
// finish the server took is kept in the state directory so a restart does not send it
// again. The pass runs on the daemon's loop, at least every OutboxPoll, never inside the
// session's turn. The models are internal/friend/tla/OutboxFinish.tla and
// internal/friend/tla/Delivery.tla (docs/SPEC-FRIEND.md, the daemon reads every outbox job).

// OutboxRetry is how long a finish the server did not answer, or refused, waits before it
// is sent again; the report stays where it is, and friend sync may finish it first.
const OutboxRetry = time.Minute

// OutboxPoll is the bound on finishing a report once it is in the outbox. The loop steps
// every beat and passes at least this often, while a turn is running and while it is not.
// A kernel watch is not linked: the fsnotify module is not a dependency of this tree, and
// go.mod is outside the change that added the pass. The poll is the watch.
const OutboxPoll = 10 * time.Second

// outboxMarksFile is the state directory's record of finishes the server took, so a
// restart does not send one again. finished_today is that file's count for FinishedOn
// (UTC). The sprint beat cannot carry it: friend beat has no such flag, and FriendReport
// has no such field.
const outboxMarksFile = "outbox-finished.json"

// ReportCap bounds the REPORT.md the daemon reads (friend sync's own cap): a verdict, a head
// and 600 characters need far less, and a larger report is noted and never read whole.
const ReportCap = 64 * 1024

// ReportChars is how much of a failed report rides on its finish.
const ReportChars = 600

// outboxState is what the daemon keeps between its outbox passes: the jobs it finished
// (never sent again, and never noted after their card leaves her row), the finishes the
// server did not take and when, the notes said while they stand, when it last passed,
// and the day's finish count (outboxMarks). loaded says the state file has been read.
type outboxState struct {
	finished      map[string]bool
	tried         map[string]time.Time
	said          map[string]bool
	polled        time.Time
	loaded        bool
	finishedOn    string
	finishedToday int
}

// outboxMarks is outbox-finished.json: the jobs whose finish the server took, and how
// many of those were today (UTC).
type outboxMarks struct {
	Finished      []string `json:"finished,omitempty"`
	FinishedOn    string   `json:"finished_on,omitempty"`
	FinishedToday int      `json:"finished_today,omitempty"`
}

// reportVerdict is a report's verdict (the first word of its first Verdict: line, upper
// case, with markdown and punctuation trimmed; "" for none) and its head (the first word
// of its first Head: line, lower case), read as friend sync reads them.
func reportVerdict(report string) (verdict, head string) {
	var vok, hok bool
	for _, l := range strings.Split(report, "\n") {
		key, val := reportKey(l)
		word, _, _ := strings.Cut(strings.TrimSpace(strings.TrimLeft(val, "*_ \t")), " ")
		switch key {
		case "verdict":
			if !vok {
				verdict, vok = strings.ToUpper(strings.Trim(word, "*_.,;:!()")), true
			}
		case "head":
			if !hok {
				head, hok = strings.ToLower(strings.Trim(word, "*_`.,;:")), true
			}
		}
	}
	return verdict, head
}

// reportKey is a report line's key, lower case, with its markdown trimmed ("" for a line
// with no colon), and what follows the colon.
func reportKey(line string) (key, val string) {
	key, val, ok := strings.Cut(strings.TrimLeft(line, "#*-_ \t"), ":")
	if !ok {
		return "", ""
	}
	return strings.ToLower(strings.Trim(key, "*_ \t")), val
}

// reportPara is a report's first line that is no Verdict or Head line and no heading, on
// one line.
func reportPara(report string) string {
	for _, l := range strings.Split(report, "\n") {
		l = strings.TrimSpace(l)
		if key, _ := reportKey(l); l == "" || strings.HasPrefix(l, "#") || key == "verdict" || key == "head" {
			continue
		}
		return l
	}
	return ""
}

// firstChars is s's first n characters (runes), on one line.
func firstChars(s string, n int) string {
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return strings.Join(strings.Fields(s), " ")
}

// OutboxFinishArgv is the finish a friend's report gives her card c (its job's card, epoch
// and generation): LAND with a full sha Head finishes at that head with the report's first
// paragraph, as friend sync words it; any other verdict (HOLD, FAIL, a LAND with no full sha
// Head) is --failed, with a HOLD's or FAIL's full sha Head kept, and the report's first
// ReportChars characters. The branch is the brief's.
func OutboxFinishArgv(friend string, c Card, verdict, head, branch, report string) []string {
	argv := []string{"finish", "--as", "friend." + friend, c.ID + "@" + strconv.Itoa(c.Gen()), "--epoch", c.Epoch()}
	full := fullSha.MatchString(head)
	var words string
	switch {
	case verdict == "LAND" && full:
		words = "friend " + friend + " LAND: " + oneLine(reportPara(report), ReportChars)
	case verdict == "LAND":
		argv = append(argv, "--failed")
		words = "friend " + friend + " LAND with no Head: <full sha>; " + firstChars(report, ReportChars)
	default:
		argv = append(argv, "--failed")
		words = "friend " + friend + " " + verdict + ": " + firstChars(report, ReportChars)
	}
	if full {
		argv = append(argv, "--head", head)
	}
	if branch != "" {
		argv = append(argv, "--branch", branch)
	}
	return append(argv, "--report", words)
}

// readReport is the job's REPORT.md under outbox: ok false when there is none, or it is no
// regular file (a symlink is never followed); err when it is larger than ReportCap or
// cannot be read.
func readReport(outbox, job string) (string, bool, error) {
	path := filepath.Join(outbox, job, "REPORT.md")
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", false, nil
	case err != nil:
		return "", false, err
	case !fi.Mode().IsRegular():
		return "", false, nil
	case fi.Size() > ReportCap:
		return "", true, fmt.Errorf("it is %d bytes, over the %d the daemon reads", fi.Size(), ReportCap)
	}
	raw, err := os.ReadFile(path)
	return string(raw), true, err
}

// outboxStateDir is where the finish record is kept: Daemon.State, else the state
// directory under her working directory. Empty when she has neither.
func (d *Daemon) outboxStateDir() string {
	if d.State != "" {
		return d.State
	}
	if d.Dir == "" {
		return ""
	}
	return StateDirIn(d.Dir)
}

// ensureOutbox reads the finish record once and makes the maps the pass uses.
func (d *Daemon) ensureOutbox(now time.Time) {
	o := &d.outbox
	if o.loaded {
		return
	}
	o.loaded = true
	if o.finished == nil {
		o.finished = map[string]bool{}
	}
	if o.tried == nil {
		o.tried = map[string]time.Time{}
	}
	if o.said == nil {
		o.said = map[string]bool{}
	}
	dir := d.outboxStateDir()
	if dir == "" {
		return
	}
	var marks outboxMarks
	found, err := read(filepath.Join(dir, outboxMarksFile), &marks)
	if err != nil {
		d.Record(now.UTC().Format(time.RFC3339) + " outbox: the finish record cannot be read: " + oneLine(err.Error(), 300) + "; a finish this run is sent again if its report still stands")
		return
	}
	if !found {
		return
	}
	for _, job := range marks.Finished {
		if validJob(job) {
			o.finished[job] = true
		}
	}
	o.finishedOn, o.finishedToday = marks.FinishedOn, marks.FinishedToday
}

// saveOutboxMarks writes the finish record. A write that fails is said by the caller;
// the memory mark still holds for this run.
func (d *Daemon) saveOutboxMarks() error {
	dir := d.outboxStateDir()
	if dir == "" {
		return nil
	}
	o := &d.outbox
	jobs := make([]string, 0, len(o.finished))
	for job := range o.finished {
		jobs = append(jobs, job)
	}
	slices.Sort(jobs)
	return write(filepath.Join(dir, outboxMarksFile), outboxMarks{
		Finished: jobs, FinishedOn: o.finishedOn, FinishedToday: o.finishedToday,
	})
}

// markOutboxFinished records that job's finish was taken, in memory and in the state
// directory, and counts it toward finished_today. A second call does nothing.
func (d *Daemon) markOutboxFinished(job string, now time.Time) {
	d.ensureOutbox(now)
	o := &d.outbox
	if o.finished[job] {
		return
	}
	day := now.UTC().Format("2006-01-02")
	if o.finishedOn != day {
		o.finishedOn, o.finishedToday = day, 0
	}
	o.finishedToday++
	o.finished[job] = true
	if err := d.saveOutboxMarks(); err != nil {
		d.Record(now.UTC().Format(time.RFC3339) + " outbox: finished " + job + " and the finish record cannot be written: " + oneLine(err.Error(), 300))
	}
}

// outboxHeld is the row's card for the job, or why the report is superseded: the row
// holds the same card at another generation or epoch. A card the row does not hold at
// all is ("", "") and the pass says so without treating a later deal as already superseded.
func outboxHeld(cards []HeldCard, job, id string, epoch, gen int) (*HeldCard, string) {
	var same *HeldCard
	for i := range cards {
		c := &cards[i]
		if c.Job == job || (c.Card == id && int(c.Epoch) == epoch && max(c.Gen, 1) == gen) {
			return c, ""
		}
		if c.Card == id {
			same = c
		}
	}
	if same != nil {
		return nil, fmt.Sprintf("superseded: card %s is generation %d epoch %d on her row, and this report is generation %d epoch %d", id, max(same.Gen, 1), same.Epoch, gen, epoch)
	}
	return nil, ""
}

// outboxWatch is the daemon's outbox poll. It runs on the loop, while a turn is under
// way and while the session is free, and not from inside the turn. The first pass is
// immediate; a later pass waits out a beat, and never longer than OutboxPoll. It uses
// the row the last reconcile read. Before that row is known there is nothing to match.
func (l *loop) outboxWatch(now time.Time) {
	d := l.d
	if d.Held == nil || !d.status.HeldKnown {
		return
	}
	o := &d.outbox
	if !o.polled.IsZero() && now.Sub(o.polled) < min(OutboxPoll, BeatEvery) {
		return
	}
	o.polled = now
	l.outboxStep(now)
}

// outboxStep is the daemon's outbox pass: every job in her outbox named
// <work>~<epoch>[.g<gen>] with a REPORT.md is finished when its card is working on her
// row (a work card, never a read), whether or not a lane is still running that job. The
// finish is sent once, and one the server did not take is sent again after OutboxRetry.
// A report with no Verdict line, one that cannot be read, and a job whose card is not
// working on her row are said once while they stand, and left. A report whose generation
// is not the row's is logged superseded and not finished.
func (l *loop) outboxStep(now time.Time) {
	d := l.d
	if d.Finish == nil {
		return
	}
	d.ensureOutbox(now)
	o := &d.outbox
	at := now.UTC().Format(time.RFC3339)
	said := map[string]bool{}
	note := func(job, why string) {
		key := job + ": " + why
		said[key] = true
		if !o.said[key] {
			d.Record(fmt.Sprintf("%s outbox: left outbox/%s/REPORT.md: %s", at, job, why))
		}
	}
	defer func() { o.said = said }()
	outbox := filepath.Join(d.Dir, "outbox")
	entries, err := os.ReadDir(outbox)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			note("*", "the outbox cannot be read: "+oneLine(err.Error(), 300))
		}
		return
	}
	running := map[string]bool{}
	for _, ln := range l.lanes.lanes {
		if ln.card != nil {
			running[filepath.Base(ln.card.Outbox)] = true
		}
	}
	if l.deadLanes(outbox, running, at) {
		if entries, err = os.ReadDir(outbox); err != nil {
			note("*", "the outbox cannot be read: "+oneLine(err.Error(), 300))
			return
		}
	}
	for _, e := range entries {
		job := e.Name()
		id, epoch, gen, ok := ParseJob(job)
		if !e.IsDir() || !ok || !validJob(job) || o.finished[job] {
			continue
		}
		report, there, err := readReport(outbox, job)
		switch {
		case !there:
			continue
		case err != nil:
			note(job, "it cannot be read: "+oneLine(err.Error(), 300))
			continue
		}
		h, superseded := outboxHeld(d.heldCards, job, id, epoch, gen)
		if superseded != "" {
			note(job, superseded) // a generation the row no longer holds: not finished, and not retried as a finish
			continue
		}
		switch {
		case h == nil:
			note(job, "superseded: card "+id+" is not on her row")
			continue
		case h.Col != "working":
			note(job, "card "+id+" is "+dash(h.Col)+" on her row, not working")
			continue
		case h.Kind == "read":
			note(job, "card "+id+" is a read, finished by its verdict, not by a report")
			continue
		}
		verdict, head := reportVerdict(report)
		if verdict == "" {
			note(job, "it has no Verdict line")
			continue
		}
		if t, ok := o.tried[job]; ok && now.Sub(t) < OutboxRetry {
			continue
		}
		card := Card{ID: id, Brief: filepath.Join(d.Dir, "inbox", job, "BRIEF.md"), Outbox: filepath.Join(outbox, job)}
		branch := h.Branch
		if branch == "" {
			_, branch = PushedHead(d.Dir, card)
		}
		if why := l.offTip(*h, verdict, head, branch); why != "" {
			o.tried[job] = now
			note(job, why+"; read again in "+OutboxRetry.String())
			continue
		}
		argv := OutboxFinishArgv(d.Friend, card, verdict, head, branch, report)
		ctx, cancel := context.WithTimeout(context.WithoutCancel(l.ctx), FinishWait)
		err = d.Finish(ctx, argv)
		cancel()
		if err != nil {
			o.tried[job] = now
			note(job, "its finish was not taken, sent again in "+OutboxRetry.String()+": "+oneLine(err.Error(), 300))
			continue
		}
		delete(o.tried, job)
		d.markOutboxFinished(job, now)
		words := "finish=ok head=" + head
		if slices.Contains(argv, "--failed") {
			words = "finish=failed"
			if fullSha.MatchString(head) {
				words += " head=" + head
			}
		}
		d.Record(fmt.Sprintf("%s outbox: finished card %s from outbox/%s/REPORT.md (Verdict %s, %s on her row): %s sent=server finished_today=%d", at, id, job, verdict, h.Col, words, o.finishedToday))
	}
}

// offTip is why a LAND with a full sha Head is not finished at it: its Head is not origin's
// tip of the card's branch, origin has no such branch, or the tip cannot be read (Daemon.Tip,
// one ls-remote; the rule nova-sprint collect and friend sync keep). "" finishes it: any
// other verdict, a daemon with no Tip, or a card whose brief names no REPO.
func (l *loop) offTip(h HeldCard, verdict, head, branch string) string {
	d := l.d
	if d.Tip == nil || verdict != "LAND" || !fullSha.MatchString(head) {
		return ""
	}
	p, ok := PacketOf(h)
	if !ok {
		return ""
	}
	branch = cmp.Or(branch, p.Branch)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(l.ctx), TipBudget)
	defer cancel()
	tip, err := d.Tip(ctx, p.Repo, branch)
	switch {
	case err != nil:
		return "origin's tip of " + branch + " in " + p.Repo + " cannot be read: " + oneLine(err.Error(), 300)
	case tip == "":
		return "Head " + head + ", and origin has no branch " + branch
	case !strings.EqualFold(tip, head):
		return "Head " + head + " is not origin's tip of " + branch + ", " + strings.ToLower(tip)
	}
	return ""
}

// A dead lane (the coordinator's finish-loop.py, the night of 2026-10-05: a run its runner
// ENDed with no REPORT.md left its card working on her row until a person looked). A bud's
// runner, which runs her cards beside the daemon, logs `<time> START|LIMIT|END <job> ...`
// lines to runner.log in her working directory or beside it, its END's last word
// report=no when the run wrote none. A working card on her row that no lane of the daemon is
// running, with no REPORT.md, whose job's last event in that log is such an END (with no
// LIMIT after its START: a run stopped at a usage limit is run again), is a dead lane: the
// daemon writes its REPORT.md, Verdict FAIL naming the END line, and the outbox pass finishes
// it --failed, so the card is dealt again. nova-sprint collect --dead-lanes keeps the same
// rule (sprint.RunnerEnded) from the coordinator's side.

// RunnerLogCap bounds the runner log the daemon reads: its last RunnerLogCap bytes.
const RunnerLogCap = 4 << 20

// RunnerLog is the log of the runner beside her working directory dir, its last
// RunnerLogCap bytes: dir's runner.log, else the one in the directory her working
// directory (its links resolved) is in; "" when there is none.
func RunnerLog(dir string) string {
	paths := []string{filepath.Join(dir, "runner.log")}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(real), "runner.log"))
	}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		fi, err := f.Stat()
		if err != nil || !fi.Mode().IsRegular() {
			_ = f.Close() // ignored: only read
			continue
		}
		off := max(fi.Size()-RunnerLogCap, 0)
		b := make([]byte, fi.Size()-off)
		n, _ := f.ReadAt(b, off) // ignored: a short read is a shorter log
		_ = f.Close()            // ignored: only read
		return string(b[:n])
	}
	return ""
}

// RunnerEnded reads a runner's log for the job: dead is true when the job's last event is an
// END whose last word is report=no and no LIMIT came after the START before it; end is that
// END line.
func RunnerEnded(log, job string) (end string, dead bool) {
	limited := false
	for _, line := range strings.Split(log, "\n") {
		f := strings.Fields(line)
		for i := 0; i+1 < len(f); i++ {
			if f[i+1] != job {
				continue
			}
			switch f[i] {
			case "START", "RESUME":
				end, dead, limited = "", false, false
			case "LIMIT":
				end, dead, limited = "", false, true
			case "END":
				end, dead = strings.TrimSpace(line), !limited && f[len(f)-1] == "report=no"
			default:
				continue
			}
			break
		}
	}
	return end, dead
}

// DeadLaneReport is the REPORT.md the daemon writes for a dead lane: Verdict FAIL, and the
// runner's END line.
func DeadLaneReport(friend, job, end string) string {
	return fmt.Sprintf("Verdict: FAIL\n\nnova-friend of %s: the runner ended job %s with no report, and no run of it is live: %s\n", friend, job, oneLine(end, 600))
}

// deadLanes writes the REPORT.md of every dead lane on her row (DeadLaneReport) and says
// whether it wrote one; the outbox pass that follows finishes it. The runner's log is read
// only when a working card has no report and no lane.
func (l *loop) deadLanes(outbox string, running map[string]bool, at string) bool {
	d := l.d
	log, read, wrote := "", false, false
	for _, h := range d.heldCards {
		job := h.Job
		if h.Col != "working" || h.Kind == "read" || !validJob(job) || running[job] || d.outbox.finished[job] {
			continue
		}
		if _, ok, _ := readReport(outbox, job); ok {
			continue
		}
		if _, _, _, ok := ParseJob(job); !ok {
			continue
		}
		if !read {
			log, read = RunnerLog(d.Dir), true
		}
		end, dead := RunnerEnded(log, job)
		if !dead {
			continue
		}
		path := filepath.Join(outbox, job, "REPORT.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			d.Record(fmt.Sprintf("%s outbox: dead lane %s: its REPORT.md cannot be written: %s", at, job, oneLine(err.Error(), 300)))
			continue
		}
		if err := atomicfile.WriteFile(path, []byte(DeadLaneReport(d.Friend, job, end)), 0o644, atomicfile.NoReplace()); err != nil {
			d.Record(fmt.Sprintf("%s outbox: dead lane %s: its REPORT.md cannot be written: %s", at, job, oneLine(err.Error(), 300)))
			continue
		}
		wrote = true
		d.Record(fmt.Sprintf("%s outbox: dead lane %s: the runner ended it with no report (%s); wrote outbox/%s/REPORT.md Verdict FAIL", at, job, oneLine(end, 300), job))
	}
	return wrote
}
