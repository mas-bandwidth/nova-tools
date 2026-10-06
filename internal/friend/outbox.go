package friend

import (
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

// OutboxRetry is how long a finish the server did not answer, or refused, waits before it
// is sent again; the report stays where it is, and friend sync may finish it first.
const OutboxRetry = time.Minute

// ReportCap bounds the REPORT.md the daemon reads (friend sync's own cap): a verdict, a head
// and 600 characters need far less, and a larger report is noted and never read whole.
const ReportCap = 64 * 1024

// ReportChars is how much of a failed report rides on its finish.
const ReportChars = 600

// outboxState is what the daemon keeps between its outbox passes: the jobs it finished
// (never sent again, and never noted after their card leaves her row), the finishes the
// server did not take and when, and the notes said while they stand.
type outboxState struct {
	finished map[string]bool
	tried    map[string]time.Time
	said     map[string]bool
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

// outboxStep is the daemon's outbox pass, after each reconcile the server answered: every
// job in her outbox named <work>~<epoch>[.g<gen>] with a REPORT.md is finished when its card
// is working on her row (a work card, never a read), the job no lane is running; the finish
// is sent once, and one the server did not take is sent again after OutboxRetry. A report
// with no Verdict line, one that cannot be read, and a job whose card is not working on her
// row are said once while they stand, and left.
func (l *loop) outboxStep(now time.Time) {
	d := l.d
	if d.Finish == nil {
		return
	}
	o := &d.outbox
	if o.finished == nil {
		o.finished, o.tried, o.said = map[string]bool{}, map[string]time.Time{}, map[string]bool{}
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
			note(job, "card "+id+" is not on her row")
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
		d.Record(fmt.Sprintf("%s outbox: finished card %s from outbox/%s/REPORT.md (Verdict %s, %s on her row): %s sent=server", at, id, job, verdict, h.Col, words))
	}
}
