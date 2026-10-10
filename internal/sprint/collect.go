package sprint

import (
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// Collect is the coordinator's hand on the friends' outboxes (the coordinator's stopgap
// finish-loop.py finished 92 cards on the night of 2026-10-05 this way; docs/SPEC-SPRINT.md
// section 1, collect): every outbox/<job>/REPORT.md whose job is a work card working on a
// friend's row finishes that card, whichever friend's tree the report sits in (a report
// written ahead may sit in another friend's tree); LAND with a full sha Head finishes at
// that head, HOLD, FAIL and any other verdict finish failed with the report's first
// CollectReportChars characters; and, when dead lanes are asked, a job whose runner ENDed
// with no report (`END <job> ... report=no`, its last word on the job) finishes failed, so
// the card is dealt again. Nothing here reads a disk or a store: nova-sprint collect reads
// the trees and the friends' rows, finishes what Collect says, and the card's leaving
// working is what makes a second pass finish nothing (a job is its card's generation, so a
// report finished once names no working card again).

// CollectReportChars is how much of a failed report rides on its finish, as the daemon's
// outbox pass carries it.
const CollectReportChars = 600

// CollectCard is a work card working on a friend's row: the friend, its card id and its job
// (the inbox/outbox name: StoredID at its epoch, .g<gen> from its second generation).
type CollectCard struct {
	Friend, Card, Job string
}

// CollectTree is what one friend's working directory holds for the collect: her outbox's
// reports by job, the jobs her reports cannot be read for (and why), and her runner's log.
type CollectTree struct {
	Friend  string
	Reports map[string]string
	Unread  map[string]string
	Runner  string
}

// Collected is one card Collect finishes, or leaves. From is the friend whose outbox held
// the report ("" for a dead lane). Left, when not empty, says why the card is not finished
// this pass (a report with no Verdict line, or one that cannot be read): it is read again
// on the next.
type Collected struct {
	CollectCard
	From          string
	Verdict, Head string
	Failed, Dead  bool
	Report, Left  string
}

// Collect is what the friends' trees finish of the cards working on their rows, a line
// each, in the order of cards: the card's own friend's report first, then the first tree
// that holds one; a card with no report anywhere is a dead lane when deadLanes is asked and
// its friend's runner ENDed the job with no report, and is not named otherwise.
func Collect(cards []CollectCard, trees []CollectTree, deadLanes bool) []Collected {
	var out []Collected
	for _, c := range cards {
		var own *CollectTree
		order := make([]*CollectTree, 0, len(trees))
		for i := range trees {
			if trees[i].Friend == c.Friend {
				own = &trees[i]
				order = append([]*CollectTree{own}, order...)
			} else {
				order = append(order, &trees[i])
			}
		}
		found := false
		for _, t := range order {
			if why, ok := t.Unread[c.Job]; ok {
				out = append(out, Collected{CollectCard: c, From: t.Friend, Left: "outbox/" + c.Job + "/REPORT.md of " + t.Friend + " cannot be read: " + why})
				found = true
				break
			}
			report, ok := t.Reports[c.Job]
			if !ok {
				continue
			}
			found = true
			out = append(out, collectReport(c, t.Friend, report))
			break
		}
		if found || !deadLanes || own == nil {
			continue
		}
		if end, dead := RunnerEnded(own.Runner, c.Job); dead {
			out = append(out, Collected{CollectCard: c, Failed: true, Dead: true,
				Report: "friend " + c.Friend + " lane ended with no report: " + collectChars(end, CollectReportChars)})
		}
	}
	return out
}

// collectReport is the finish a report gives card c: LAND with a full sha Head at that head
// with the report's first paragraph; a LAND with no full sha Head, HOLD, FAIL and any other
// verdict failed, a HOLD's or FAIL's full sha Head kept, with the report's first
// CollectReportChars characters; no Verdict line leaves the card, for she may be writing it.
func collectReport(c CollectCard, from, report string) Collected {
	verdict, head := CollectVerdict(report)
	r := Collected{CollectCard: c, From: from, Verdict: verdict}
	full := typedrec.IsFullSha(head)
	switch {
	case verdict == "":
		r.Left = "outbox/" + c.Job + "/REPORT.md of " + from + " has no Verdict line"
	case verdict == "LAND" && full:
		r.Head, r.Report = head, "friend "+c.Friend+" LAND: "+collectChars(collectPara(report), CollectReportChars)
	case verdict == "LAND":
		r.Failed, r.Report = true, "friend "+c.Friend+" LAND with no Head: <full sha>; "+collectChars(report, CollectReportChars)
	default:
		r.Failed, r.Report = true, "friend "+c.Friend+" "+verdict+": "+collectChars(report, CollectReportChars)
		if full {
			r.Head = head
		}
	}
	if r.Failed {
		r.Report = member.CarryHoldFix(r.Report, report, CollectReportChars)
	}
	return r
}

// CollectVerdict is a report's verdict (the first word of its first Verdict: line, upper
// case, markdown and punctuation trimmed; "" for none) and its head (the first word of its
// first Head: line, lower case), as the daemon's outbox pass reads them.
func CollectVerdict(report string) (verdict, head string) {
	var vok, hok bool
	for _, l := range strings.Split(report, "\n") {
		key, val := collectKey(l)
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

func collectKey(line string) (key, val string) {
	key, val, ok := strings.Cut(strings.TrimLeft(line, "#*-_ \t"), ":")
	if !ok {
		return "", ""
	}
	return strings.ToLower(strings.Trim(key, "*_ \t")), val
}

// collectPara is a report's first line that is no Verdict or Head line and no heading.
func collectPara(report string) string {
	for _, l := range strings.Split(report, "\n") {
		l = strings.TrimSpace(l)
		if key, _ := collectKey(l); l == "" || strings.HasPrefix(l, "#") || key == "verdict" || key == "head" {
			continue
		}
		return l
	}
	return ""
}

// collectChars is s's first n characters (runes), on one line.
func collectChars(s string, n int) string {
	if r := []rune(s); len(r) > n {
		s = string(r[:n])
	}
	return strings.Join(strings.Fields(s), " ")
}

// RunnerEnded reads a runner's log (one line per event: `<time> START|LIMIT|END <job> ...`,
// as a bud's runner writes it) for the job: dead is true when the job's last event is an END
// whose last word is report=no and no LIMIT came after the START before it (a run stopped
// at a usage limit is run again, never dead); end is that END line.
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
