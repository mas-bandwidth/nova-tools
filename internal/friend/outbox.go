package friend

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// The daemon reads every outbox job (the night of 2026-10-05: a friend held eight
// working cards whose outbox/<job>/REPORT.md said Verdict: LAND with a Head, unread from
// 21:09 for two hours, because the daemon finished only the cards its own lanes ran and the
// coordinator's delivery stopgap had written those briefs; the tick raised "a friend holds
// working cards and finishes none"). Each reconcile that the server answered, the daemon
// reads every job in her outbox whose name parses to a card (<work>~<epoch>[.g<gen>],
// ParseJob) and finishes it when that card is working on her row, whoever wrote its brief:
// LAND with a full sha Head finishes with that head, HOLD and FAIL finish --failed
// with the report's first 600 characters; a LAND whose report does not carry the key
// words of its brief's fix (THE ONE THING LEFT, rework.go) is a HOLD by the daemon,
// finished --failed with its head kept and the words that say why. A report is final
// only in the shape Final reads (its first line Verdict: LAND, HOLD or FAIL, its second
// Head: a full sha or Head: -, read as friend sync reads them, and a blank second line
// for HOLD or FAIL); every other report is left and noted once, and past the card's
// deadline it is collected as FAIL. A report with no Verdict line stays until its
// deadline too; a job whose card is not working on her row is noted once and left. The
// model is
// internal/friend/tla/OutboxFinish.tla (docs/SPEC-FRIEND.md, the daemon reads every outbox
// job).

// OutboxRetry is how long a finish the server did not answer, or refused, waits before it
// is sent again; the report stays where it is, and friend sync may finish it first.
const OutboxRetry = time.Minute

// HoldersBudget bounds the current ownership view asked for an old report.
const HoldersBudget = 10 * time.Second

// ReportCap bounds the REPORT.md the daemon reads (friend sync's own cap): a verdict, a head
// and 600 characters need far less, and a larger report is noted and never read whole.
const ReportCap = 64 * 1024

// ReportChars is how much of a failed report rides on its finish.
const ReportChars = 600

// outboxState is what the daemon keeps between its outbox passes: the jobs it finished
// (never sent again, and never noted after their card leaves her row), the finishes the
// server did not take and when, the notes said while they stand, and when each working
// card was first seen on her row (a duration deadline runs from the card's start, that
// pass, not from the pass that first reads its report).
type outboxState struct {
	finished map[string]bool
	tried    map[string]time.Time
	said     map[string]bool
	started  map[string]time.Time
}

// reportVerdict is a report's verdict (the first word of its first Verdict: line, upper
// case, with markdown and punctuation trimmed; "" for none) and its head (the first word
// of its first Head: line, lower case), read as friend sync reads them.
func reportVerdict(report string) (verdict, head string) {
	var vok, hok bool
	for _, l := range strings.Split(report, "\n") {
		key, word := reportWord(l)
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

// Final reports whether a report's first and second lines are a final verdict
// (docs/SPEC-FRIEND.md, the daemon reads every outbox job). ok is true only when
// first is a verdict of LAND, HOLD or FAIL, in any case, and second is
// "Head: <40-hex>" or "Head: -", read as friend sync reads them (markdown
// trimmed); a blank second line is final for HOLD and FAIL, whose brief says
// line 2 is blank (docs/FRIENDS.md). why is the line that is not, so a report
// that is not final can be named. The collector (outboxStep) calls it.
func Final(first, second string) (ok bool, why string) {
	first, second = strings.TrimSpace(first), strings.TrimSpace(second)
	verdict, vok := finalVerdict(first)
	if !vok {
		return false, first
	}
	if finalHead(second) {
		return true, ""
	}
	if (verdict == "HOLD" || verdict == "FAIL") && second == "" {
		return true, ""
	}
	return false, second
}

// finalVerdict is a first line whose verdict is LAND, HOLD or FAIL, read as
// friend sync reads it (reportWord: markdown trimmed, any case); it returns
// the verdict, upper case.
func finalVerdict(line string) (verdict string, ok bool) {
	key, word := reportWord(line)
	if key != "verdict" {
		return "", false
	}
	verdict = strings.ToUpper(strings.Trim(word, "*_.,;:!()"))
	switch verdict {
	case "LAND", "HOLD", "FAIL":
		return verdict, true
	default:
		return "", false
	}
}

// finalHead is a second line that is a full sha head or a dash, read as friend
// sync reads it (reportWord: markdown trimmed, any case).
func finalHead(line string) bool {
	key, word := reportWord(line)
	if key != "head" {
		return false
	}
	word = strings.ToLower(strings.Trim(word, "*_`.,;:"))
	return word == "-" || fullSha.MatchString(word)
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

// reportWord is a report line's key (reportKey) and the first word of what
// follows the colon ("" for none), read as friend sync reads them.
func reportWord(line string) (key, word string) {
	key, val := reportKey(line)
	word, _, _ = strings.Cut(strings.TrimSpace(strings.TrimLeft(val, "*_ \t")), " ")
	return key, word
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

// reportFinalWithin is how long a report that is not final may stand when the brief
// names no deadline: two hours, the unfinished bound a friend's card carries then
// (sprint.DeadlineUnfinished).
const reportFinalWithin = 2 * time.Hour

// deadlineWithinRE is a DEADLINE value the brief writes as words: `finish within <n> <unit>`
// or a bare `<n> <unit>` (cardhdr's deadline shape), with anything the sentence says after it
// (`finish within 240 minutes.`, the template's `... minutes; the judgment ...`) ignored. A
// Go duration is read first.
var deadlineWithinRE = regexp.MustCompile(`(?i)^(?:finish within\s+)?(\d+)\s*(seconds?|secs?|s|minutes?|mins?|m|hours?|h)(?:\s*[.;,].*)?$`)

// cardDeadline is the brief's deadline (docs/SPEC-FRIEND.md, the daemon reads every
// outbox job): an absolute time, or a duration measured from the card's start. The
// DEADLINE key is matched in any case, for an issued brief writes `Deadline:`; ok is
// false when the brief names none the daemon can read.
func cardDeadline(brief string) (at time.Time, within time.Duration, ok bool) {
	val, found := briefValueFold(brief, "DEADLINE")
	if !found {
		return time.Time{}, 0, false
	}
	if t, err := time.Parse(time.RFC3339, val); err == nil {
		return t, 0, true
	}
	if d, okd := deadlineWithin(val); okd {
		return time.Time{}, d, true
	}
	return time.Time{}, 0, false
}

// briefValueFold is cardhdr.Value with the key matched in any case: an issued brief writes
// `Deadline:`, and cardhdr.Value matches the key as written, so a card's own deadline was
// read as none and the daemon fell back to its two hours.
func briefValueFold(brief, key string) (value string, ok bool) {
	for _, l := range strings.Split(brief, "\n") {
		if k, v, isKV := cardhdr.KeyValue(l); isKV && strings.EqualFold(k, key) {
			return v, true
		}
	}
	return "", false
}

// deadlineWithin reads a duration deadline: a Go duration or `finish within <n> <unit>`.
func deadlineWithin(v string) (time.Duration, bool) {
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d, true
	}
	m := deadlineWithinRE.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, false
	}
	unit := time.Second
	switch strings.ToLower(m[2])[0] {
	case 'm':
		unit = time.Minute
	case 'h':
		unit = time.Hour
	}
	return time.Duration(n) * unit, true
}

// cardStart is when the card began, the anchor a duration deadline runs from
// (docs/SPEC-FRIEND.md, the daemon reads every outbox job): the lane's own start when this
// daemon began it (LaneState.Started, kept across restarts), else the first outbox pass
// that saw it working on her row (outboxState.started). now is the pass, recorded when the
// card has no start yet.
func (l *loop) cardStart(job string, now time.Time) time.Time {
	if st, ok := l.lanes.state.Started[job]; ok && !st.At.IsZero() {
		return st.At
	}
	o := &l.d.outbox
	if o.started == nil {
		o.started = map[string]time.Time{}
	}
	if at, seen := o.started[job]; seen {
		return at
	}
	o.started[job] = now
	return now
}

// pastFinalDeadline says the card's deadline has passed for a report that is not final
// (docs/SPEC-FRIEND.md, the daemon reads every outbox job). An absolute deadline is that
// time. A duration, and the two hours used when the brief names none, runs from the
// card's start, never from the pass that first read the report.
func (l *loop) pastFinalDeadline(h *HeldCard, job string, now time.Time) bool {
	at, within, ok := cardDeadline(h.Brief)
	if ok && !at.IsZero() {
		return !now.Before(at)
	}
	if !ok || within <= 0 {
		within = reportFinalWithin
	}
	return !now.Before(l.cardStart(job, now).Add(within))
}

// firstTwo is a report's first line and its second, without a trailing carriage return.
func firstTwo(report string) (first, second string) {
	lines := strings.Split(report, "\n")
	if len(lines) > 0 {
		first = strings.TrimRight(lines[0], "\r")
	}
	if len(lines) > 1 {
		second = strings.TrimRight(lines[1], "\r")
	}
	return first, second
}

// reportSHA is the sha256 of the report bytes the daemon collected, hex encoded.
func reportSHA(report string) string {
	sum := sha256.Sum256([]byte(report))
	return hex.EncodeToString(sum[:])
}

// outboxStep is the daemon's outbox pass, after each reconcile the server answered: every
// job in her outbox named <work>~<epoch>[.g<gen>] with a REPORT.md is finished when its card
// is working on her row (a work card, never a read), the job no lane is running, and the
// report is final (Final). The finish is sent once, and one the server did not take is
// sent again after OutboxRetry. A report that is not final is left and noted once, and
// past the card's deadline collected as FAIL. A report with no Verdict line, one that
// cannot be read, and a job whose card is not working on her row are said once while they
// stand; the first stays until its deadline, and the rest are left.
func (l *loop) outboxStep(now time.Time) {
	d := l.d
	if d.Finish == nil {
		return
	}
	o := &d.outbox
	if o.finished == nil {
		o.finished, o.tried, o.said = map[string]bool{}, map[string]time.Time{}, map[string]bool{}
		o.started = map[string]time.Time{}
	}
	// A duration deadline runs from the card's start: record every card working on her
	// row the first pass it is seen, before a report is read, so a report written late
	// does not give the card a fresh deadline (pastFinalDeadline).
	for i := range d.heldCards {
		h := &d.heldCards[i]
		if h.Col != "working" || h.Kind == "read" {
			continue
		}
		if _, seen := o.started[h.Job]; !seen {
			o.started[h.Job] = now
		}
	}
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
	// Ask for every holder together, only when an old report needs a refusal.
	// Hundreds of old reports cost one view, not one server trip per report.
	owners, ownersRead, ownersError := d.Running, false, ""
	refusal := func(card, job string) string {
		if d.Holders != nil && !ownersRead {
			ownersRead = true
			ctx, cancel := context.WithTimeout(l.ctx, HoldersBudget)
			held, err := d.Holders(ctx)
			cancel()
			owners = func() map[string]string { return held }
			if err != nil {
				owners = nil // an old running list cannot stand in for a failed current view
				ownersError = "; the holder view could not be read: " + oneLine(err.Error(), 200)
			}
		}
		return NotHers(card, job, d.Friend, owners) + ownersError
	}
	for _, e := range entries {
		job := e.Name()
		id, epoch, gen, ok := ParseJob(job)
		if !e.IsDir() || !ok || !validJob(job) || o.finished[job] || running[job] {
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
		for i, c := range d.heldCards {
			if c.Job == job || (c.Card == id && c.Epoch == uint64(epoch) && max(c.Gen, 1) == gen) {
				h = &d.heldCards[i]
				break
			}
		}
		switch {
		case h == nil:
			note(job, refusal(id, job))
			continue
		case h.Col != "working":
			note(job, "card "+id+" is "+dash(h.Col)+" on her row, not working")
			continue
		case h.Kind == "read":
			note(job, "card "+id+" is a read, finished by its verdict, not by a report")
			continue
		}
		verdict, head := reportVerdict(report)
		first, second := firstTwo(report)
		collected := report
		// Collection is gated on Final for every report (docs/SPEC-FRIEND.md, the
		// daemon reads every outbox job): a report Final does not read as final is
		// left and noted once, and past the card's deadline it is collected as
		// FAIL, so a card cannot hang forever.
		if final, _ := Final(first, second); !final {
			if !l.pastFinalDeadline(h, job, now) {
				note(job, "report not final yet: "+id+": "+oneLine(first, 200))
				continue
			}
			report = "report never became final: first line " + oneLine(first, 300) + "\n"
			verdict, head = "FAIL", ""
		}
		if t, ok := o.tried[job]; ok && now.Sub(t) < OutboxRetry {
			continue
		}
		card := Card{ID: id, Brief: filepath.Join(d.Dir, "inbox", job, "BRIEF.md"), Outbox: filepath.Join(outbox, job)}
		written := verdict
		if verdict == "LAND" {
			// a LAND that does not address its brief's first line is a HOLD (rework.go)
			if held := unaddressed(HeldByDaemon, report, jobFix(card.Brief, h.Brief)); held != "" {
				verdict, report, written = "HOLD", held+"\n\n"+report, "LAND, "+held
			}
		}
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
		words := "finish=ok head=" + head
		if slices.Contains(argv, "--failed") {
			words = "finish=failed"
			if fullSha.MatchString(head) {
				words += " head=" + head
			}
		}
		d.Record(fmt.Sprintf("%s outbox: finished card %s from outbox/%s/REPORT.md (Verdict %s, %s on her row): %s sent=server sha256=%s", at, id, job, written, h.Col, words, reportSHA(collected)))
		l.releaseJob(job, now)
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

// DeadLaneReport is the REPORT.md the daemon writes for a dead lane: Verdict FAIL, the
// Head: - line that makes it final (Final), and the runner's END line. The outbox pass
// that follows finishes it.
func DeadLaneReport(friend, job, end string) string {
	return fmt.Sprintf("Verdict: FAIL\nHead: -\n\nnova-friend of %s: the runner ended job %s with no report, and no run of it is live: %s\n", friend, job, oneLine(end, 600))
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

// NotHers is the refusal of a report on a card that is no longer hers: never finished, with
// the line naming who holds it now, as the beat's running list says (a card id or job to the
// friend whose lane runs it), else that no row the daemon reads names one.
func NotHers(card, job, friend string, running func() map[string]string) string {
	holder := ""
	if running != nil {
		r := running()
		holder = cmp.Or(r[card], r[job])
	}
	switch {
	case holder != "" && holder != friend:
		return "refused: card " + card + " is not on her row, no longer hers; " + holder + " holds it now"
	default:
		return "refused: card " + card + " is not on her row, no longer hers; no row the daemon reads says who holds it now (nova-sprint view coordinator does)"
	}
}
