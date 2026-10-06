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
// ParseJob) and finishes it when that card is working on her row, whoever wrote its brief:
// LAND with a full sha Head finishes with that head, HOLD and FAIL (any other verdict too)
// finish --failed with the report's first 600 characters. A report with no Verdict line,
// and a job whose card is not working on her row, are noted once and left. The model is
// internal/friend/tla/OutboxFinish.tla (docs/SPEC-FRIEND.md, the daemon reads every outbox
// job).
//
// The outbox is finished by the daemon, never by the session's turn (2026-10-06: a friend wrote
// 12 REPORT.md files in two hours and the sprint log shows one finish by her; her lanes'
// turns ran on through long tool sequences after each report, and the pass left every job a
// lane was running until its turn ended). The pass is the daemon's own, every loop step,
// whatever the session or a lane is doing: a watch on the outbox (each step stats its
// REPORT.md files; a change passes at once) with OutboxPoll as the bound when nothing is
// seen to change. A report is finished when it appears, a lane running its card or not;
// only a LAND with no full sha Head waits for a running lane's end, for the run may still be
// writing it. A finished job is marked in the state directory (OutboxFile), so a daemon
// that starts again never finishes it twice; a report whose card is not hers (off her row,
// or there at another epoch or generation) is superseded: said once, marked, never read
// again. Each finish is one line, and the finishes of the day are the status's
// finished_today. The model is tla/Delivery.tla (WrittenIsFinished: a report written is
// finished within the poll bound, the session busy or not).

// OutboxRetry is how long a finish the server did not answer, or refused, waits before it
// is sent again; the report stays where it is, and friend sync may finish it first.
const OutboxRetry = time.Minute

// ReportCap bounds the REPORT.md the daemon reads (friend sync's own cap): a verdict, a head
// and 600 characters need far less, and a larger report is noted and never read whole.
const ReportCap = 64 * 1024

// ReportChars is how much of a failed report rides on its finish.
const ReportChars = 600

// OutboxPoll bounds how long a written report waits for the daemon's pass when the watch
// sees no change: the poll under the watch.
const OutboxPoll = 10 * time.Second

// OutboxFile is the outbox marks in the state directory: the jobs finished and superseded.
const OutboxFile = "outbox.json"

// OutboxKept is how long a mark is kept once its job has left her outbox.
const OutboxKept = 7 * 24 * time.Hour

// OutboxMarks is what the daemon keeps of her outbox across its restarts: each job it
// finished and each it found superseded, with when.
type OutboxMarks struct {
	Finished   map[string]time.Time `json:"finished,omitempty"`
	Superseded map[string]time.Time `json:"superseded,omitempty"`
}

// ReadOutbox is the outbox marks in stateDir; none when the file is not there.
func ReadOutbox(stateDir string) (OutboxMarks, error) {
	var m OutboxMarks
	_, err := read(filepath.Join(stateDir, OutboxFile), &m)
	return m, err
}

// WriteOutbox is the outbox marks written whole to stateDir.
func WriteOutbox(stateDir string, m OutboxMarks) error {
	return write(filepath.Join(stateDir, OutboxFile), m)
}

// FinishedOn is how many jobs the marks say were finished on now's UTC day.
func (m OutboxMarks) FinishedOn(now time.Time) int {
	y, mo, d := now.UTC().Date()
	n := 0
	for _, at := range m.Finished {
		if ay, amo, ad := at.UTC().Date(); ay == y && amo == mo && ad == d {
			n++
		}
	}
	return n
}

// outboxState is what the daemon keeps between its outbox passes: the jobs it finished
// (never sent again, and never noted after their card leaves her row), the finishes the
// server did not take and when, and the notes said while they stand.
type outboxState struct {
	finished map[string]bool
	tried    map[string]time.Time
	said     map[string]bool
	marks    OutboxMarks // finished and superseded, as the state directory keeps them
	loaded   bool
	sign     string    // the outbox's reports as the last watch saw them
	passed   time.Time // the last pass
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

// outboxSign is the watch's reading of her outbox: each job's REPORT.md, its size and its
// time, so a report written or rewritten changes it ("" when the outbox cannot be read).
func outboxSign(outbox string) string {
	entries, err := os.ReadDir(outbox)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if fi, err := os.Lstat(filepath.Join(outbox, e.Name(), "REPORT.md")); err == nil {
			fmt.Fprintf(&b, "%s %d %d\n", e.Name(), fi.Size(), fi.ModTime().UnixNano())
		}
	}
	return b.String()
}

// outboxWatch is the daemon's outbox watch, every loop step whatever the session or a lane
// is doing: once the server has said what is on her row, a pass runs when her row was just
// read, when a report was written or changed since the last look, and at least once an
// OutboxPoll.
func (l *loop) outboxWatch(now time.Time, reconciled bool) {
	d := l.d
	if d.Finish == nil || !d.status.HeldKnown {
		return
	}
	o := &d.outbox
	sign := outboxSign(filepath.Join(d.Dir, "outbox"))
	if !reconciled && sign == o.sign && !o.passed.IsZero() && now.Sub(o.passed) < OutboxPoll {
		return
	}
	o.sign, o.passed = sign, now
	l.outboxStep(now)
}

// loadOutbox reads the marks the state directory keeps, once a run.
func (l *loop) loadOutbox(now time.Time) {
	d := l.d
	o := &d.outbox
	if o.loaded {
		return
	}
	o.loaded = true
	if d.LoadOutbox != nil {
		m, err := d.LoadOutbox()
		if err != nil {
			d.Record(now.UTC().Format(time.RFC3339) + " outbox: the outbox marks cannot be read: " + oneLine(err.Error(), 300) + "; a report finished before is finished again only if its card is still working")
		}
		o.marks = m
	}
	if o.marks.Finished == nil {
		o.marks.Finished = map[string]time.Time{}
	}
	if o.marks.Superseded == nil {
		o.marks.Superseded = map[string]time.Time{}
	}
	for job := range o.marks.Finished {
		o.finished[job] = true
	}
}

// saveOutbox writes the marks, those of jobs gone from her outbox past OutboxKept dropped.
func (l *loop) saveOutbox(now time.Time, present map[string]bool) {
	d := l.d
	o := &d.outbox
	for _, m := range []map[string]time.Time{o.marks.Finished, o.marks.Superseded} {
		for job, at := range m {
			if !present[job] && now.Sub(at) > OutboxKept {
				delete(m, job)
			}
		}
	}
	d.status.FinishedToday = o.marks.FinishedOn(now)
	if d.SaveOutbox == nil {
		return
	}
	if err := d.SaveOutbox(o.marks); err != nil {
		d.Record(now.UTC().Format(time.RFC3339) + " outbox: the outbox marks cannot be written: " + oneLine(err.Error(), 300))
	}
}

// outboxStep is the daemon's outbox pass (outboxWatch): every job in her outbox named
// <work>~<epoch>[.g<gen>] with a REPORT.md is finished when its card is working on her row at
// that epoch and generation (a work card, never a read), a lane running it or not; the finish
// is sent once, marked in the state directory, and one the server did not take is sent again
// after OutboxRetry. A LAND with no full sha Head whose lane still runs waits for the lane's
// end. A report whose card is not on her row at its epoch and generation is superseded:
// said once and never read again. A report with no Verdict line, one that cannot be read,
// and a job whose card is on her row and not working are said once while they stand, and
// left.
func (l *loop) outboxStep(now time.Time) {
	d := l.d
	if d.Finish == nil {
		return
	}
	o := &d.outbox
	if o.finished == nil {
		o.finished, o.tried, o.said = map[string]bool{}, map[string]time.Time{}, map[string]bool{}
	}
	l.loadOutbox(now)
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
	present, changed := map[string]bool{}, false
	defer func() {
		if changed || d.status.FinishedToday != o.marks.FinishedOn(now) {
			l.saveOutbox(now, present)
		}
	}()
	for _, e := range entries {
		job := e.Name()
		present[job] = true
		id, epoch, gen, ok := ParseJob(job)
		if !e.IsDir() || !ok || !validJob(job) || o.finished[job] {
			continue
		}
		if _, gone := o.marks.Superseded[job]; gone {
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
		var h *HeldCard
		other := ""
		for i, c := range d.heldCards {
			if c.Job == job || (c.Card == id && c.Epoch == uint64(epoch) && max(c.Gen, 1) == gen) {
				h = &d.heldCards[i]
				break
			}
			if c.Card == id {
				other = c.Job
			}
		}
		switch {
		case h == nil:
			why := "card " + id + " is not on her row"
			if other != "" {
				why = "card " + id + " is on her row as " + other + ", not at epoch " + strconv.Itoa(epoch) + " generation " + strconv.Itoa(gen)
			}
			o.marks.Superseded[job], changed = now, true
			d.Record(fmt.Sprintf("%s outbox: superseded outbox/%s/REPORT.md: %s; not retried", at, job, why))
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
		if running[job] && verdict == "LAND" && !fullSha.MatchString(head) {
			note(job, "a LAND with no full sha Head while lane runs it; read again at its end")
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
		o.finished[job] = true
		o.marks.Finished[job], changed = now, true
		words := "finish=ok head=" + head
		if slices.Contains(argv, "--failed") {
			words = "finish=failed"
			if fullSha.MatchString(head) {
				words += " head=" + head
			}
		}
		if running[job] {
			words += " lane=running"
		}
		d.Record(fmt.Sprintf("%s outbox: finished card %s from outbox/%s/REPORT.md (Verdict %s, %s on her row): %s sent=server", at, id, job, verdict, h.Col, words))
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
