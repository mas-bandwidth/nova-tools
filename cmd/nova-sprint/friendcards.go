package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// A friend's sprint cards (the owner, 2026-10-03: "Could we try expressing the work left
// for nova-tools-1.1.0 into cards, and doing it via the sprint, but doing parts on friends
// where we would normally do friend work."; docs/SPEC-SPRINT.md section 5, a friend's
// card). The tick deals a card whose brief says WHO: friend to a friend's fleet row
// (sprint.FriendDeal); friend sync, which runs where the friends' working directories are
// and is the coordinator's own loop (only the coordinator reaches out), carries it across
// the inbox/outbox standard (docs/FRIENDS.md): it delivers each card working on her row
// as inbox/<job>/BRIEF.md, and finishes it from outbox/<job>/REPORT.md once she writes
// one. <job> is the card's id as the table layer holds it at its epoch (sprint.StoredID:
// the card id itself at epoch 0), so a card id a clear brings back is another job.

// Verdicts of a friend's report on a sprint card: LAND is work ready to read and land at
// its Head; HOLD and FAIL are work that came back failed.
const (
	VerdictLand = "LAND"
	VerdictHold = "HOLD"
	VerdictFail = "FAIL"
)

// friendJobOf is the job a friend's sprint card is delivered as: its stored id.
func friendJobOf(p sprint.Packet) string { return sprint.StoredID(p.Card, p.Epoch) }

// isCardJob says an inbox directory is a sprint card's job (friendJobOf), counted from the
// fleet table as her cards, never as one of her jobs.
func isCardJob(name string) bool {
	id := sprint.CardID(name)
	_, _, ok := sprint.ParseWorkCard(id)
	return ok && sprint.ValidCardID(id)
}

// friendBrief is the BRIEF.md of a friend's sprint card: its STATUS line (the card, its
// epoch and attempt, the branch to push and the report to write), the working-directory
// line of docs/FRIENDS.md, a later attempt's start and why it exists (as a child's JOB.md
// says them), then the brief as a child is handed it (the rules it names injected).
func friendBrief(name string, p sprint.Packet) string {
	job := friendJobOf(p)
	var b strings.Builder
	fmt.Fprintf(&b, "STATUS: nova-sprint card %s, epoch %d, attempt %d; push your work to the branch %s; when done, write outbox/%s/REPORT.md with Verdict: LAND|HOLD|FAIL and Head: <sha>\n", p.Card, p.Epoch, p.Attempt, p.Branch, job)
	fmt.Fprintf(&b, "Work in ~/%[1]s-working/jobs/%[2]s/: every clone, worktree and build output goes inside it, GOCACHE=~/%[1]s-working/.cache/go-build, and the report goes to ~/%[1]s-working/outbox/%[2]s/REPORT.md.\n", name, job)
	if p.BaseHead != "" {
		fmt.Fprintf(&b, "This attempt continues attempt %d: its head, %s, is the last pushed by any attempt before this one; start from it.\n", p.BaseAttempt, p.BaseHead)
	}
	for _, l := range [][2]string{{"This attempt exists because: ", p.Why}, {"A reader found: ", p.Finding}, {"The coordinator asks: ", p.Fix}} {
		if l[1] != "" {
			b.WriteString(l[0] + l[1] + "\n")
		}
	}
	brief := p.Brief
	if p.Rules != "" {
		if rules, err := swarm.HeldRules(p.Rules); err == nil {
			brief = swarm.StagedBrief(brief, rules)
		}
	}
	b.WriteString("\n" + strings.TrimRight(brief, "\n") + "\n")
	return b.String()
}

// friendReportOf reads a friend's REPORT.md on a sprint card: its verdict (the first word
// of its first Verdict: line, in upper case; "" for none), its head (the first word of its
// first Head: line), and its first paragraph (the first block of lines that are neither a
// key line of those two nor a markdown heading), on one line.
func friendReportOf(report string) (verdict, head, para string) {
	verdict, _ = reportValue(report, "verdict")
	verdict = strings.ToUpper(strings.Trim(firstWord(verdict), "*_.,;:!"))
	head, _ = reportValue(report, "head")
	head = strings.Trim(firstWord(head), "*_`.,;:")
	var lines []string
	for _, l := range strings.Split(report, "\n") {
		key, _, _ := strings.Cut(strings.TrimLeft(l, "#*-_ \t"), ":")
		switch k := strings.ToLower(strings.TrimSpace(key)); {
		case strings.TrimSpace(l) == "":
			if len(lines) > 0 {
				return verdict, head, strings.Join(lines, " ")
			}
		case strings.HasPrefix(strings.TrimSpace(l), "#"), k == "verdict", k == "head":
		default:
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	return verdict, head, strings.Join(lines, " ")
}

func firstWord(s string) string {
	w, _, _ := strings.Cut(strings.TrimSpace(strings.TrimLeft(s, "*_ \t")), " ")
	return w
}

// maxFriendReport bounds the paragraph a friend's report puts on the work card.
const maxFriendReport = 1024

// friendFinish is the finish a friend's report gives her card: LAND with a full sha Head
// is work ok at that head, as a member's ok finish; HOLD, FAIL (or FAILED, BROKEN) is work
// that came back failed, its report the first paragraph, as a member's failed finish
// raises "work came back failed"; a LAND with no full sha Head, or any other verdict, is
// failed too, saying what the report lacks. The report begins with the friend's name, so
// it is never read as a provider failure, no result or a staging refusal.
func friendFinish(name string, p sprint.Packet, report string) sprint.FinishReq {
	verdict, head, para := friendReportOf(report)
	para = oneline.Cap(para, maxFriendReport)
	row := sprint.FriendRow(name)
	r := sprint.FinishReq{Sel: sprint.Sel{IDs: []string{p.Card}}, As: row, Gens: map[string]int{p.Card: p.Gen}, Branch: p.Branch, Who: row}
	switch {
	case verdict == VerdictLand && typedrec.IsFullSha(head):
		r.Head, r.Report = head, "friend "+name+" LAND: "+para
	case verdict == VerdictLand:
		r.Failed, r.Report = true, "friend "+name+" LAND with no Head: <full sha>; "+para
	case verdict == VerdictHold || verdict == VerdictFail || verdict == "FAILED" || verdict == "BROKEN":
		r.Failed, r.Report = true, "friend "+name+" "+verdict+": "+para
	default:
		r.Failed, r.Report = true, "friend "+name+" verdict "+cmp.Or(verdict, "none")+" is not LAND, HOLD or FAIL; "+para
	}
	return r
}

// friendCardsOf delivers and collects one friend's sprint cards in her working directory
// dir: every card working on her row is written as inbox/<job>/BRIEF.md when it is not
// there (written whole, then renamed into place), and finished from outbox/<job>/REPORT.md
// when that is there. It says what it did, a line each, and how many it delivered and
// finished.
func (a *app) friendCardsOf(ctx context.Context, st *store.Store, name, dir string, say func(string)) (delivered, finished int, err error) {
	cards, err := st.ReadCells(ctx, sprint.Fleet, sprint.FriendRow(name), sprint.Working)
	if err != nil || len(cards) == 0 {
		return 0, 0, err
	}
	packets, err := st.Packets(ctx, cards)
	if err != nil {
		return 0, 0, err
	}
	for _, p := range packets {
		job := friendJobOf(p)
		in := filepath.Join(dir, "inbox", job)
		if _, err := os.Stat(filepath.Join(in, "BRIEF.md")); errors.Is(err, fs.ErrNotExist) {
			if err := writeWhole(in, "BRIEF.md", friendBrief(name, p)); err != nil {
				return delivered, finished, err
			}
			delivered++
			say(fmt.Sprintf("FRIEND-CARD DELIVERED friend=%s card=%s job=%s branch=%s", name, p.Card, oneline.Field(job), p.Branch))
		} else if err != nil {
			return delivered, finished, err
		}
		report, err := os.ReadFile(filepath.Join(dir, "outbox", job, "REPORT.md"))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return delivered, finished, err
		}
		r := friendFinish(name, p, string(report))
		step := store.FinishStep(r)
		step.Actor, step.Epoch = r.Who, &p.Epoch
		res, err := st.Run(ctx, step)
		if err != nil {
			return delivered, finished, err
		}
		if len(res.Refused) > 0 {
			say(fmt.Sprintf("FRIEND-CARD REFUSED friend=%s card=%s: %s", name, p.Card, oneline.Escape(res.Refused[0].Why)))
			continue
		}
		finished++
		result := "ok"
		if r.Failed {
			result = "failed"
		}
		say(fmt.Sprintf("FRIEND-CARD FINISHED friend=%s card=%s result=%s head=%s: %s", name, p.Card, result, cmp.Or(r.Head, "-"), oneline.Escape(oneline.Cap(r.Report, 200))))
	}
	return delivered, finished, nil
}

// writeWhole writes one file into dir (made when it is not there) whole: to a temporary
// name beside it, then renamed into place, so a reader never sees half of it.
func writeWhole(dir, name, text string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp := filepath.Join(dir, "."+name+".tmp")
	if err := os.WriteFile(tmp, []byte(text), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}
