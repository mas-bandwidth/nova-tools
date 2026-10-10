package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/redisconn"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// A friend's sprint cards (the owner, 2026-10-03: "Could we try expressing the work left
// for nova-tools-1.1.0 into cards, and doing it via the sprint, but doing parts on friends
// where we would normally do friend work."; docs/SPEC-SPRINT.md section 1, a friend's
// card). The tick deals a card whose brief says WHO: friend to a friend's fleet row
// (sprint.TickDeal); friend sync, which runs where the friends' working directories are
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

// friendJobOf is the job a friend's sprint card is delivered as: its stored id, and from its
// second generation (a card taken back and dealt again, sprint.FriendTake) .g<gen> after it, so
// a card dealt again to the same friend is a new job whose brief names its own branch.
// A read card is a card: it is delivered as a work card is, under the same job (a one-shot
// runner finds inbox/<job>/BRIEF.md where it finds a work card's), and friend sync closes it
// from outbox/<job>/REPORT.md (friendReadOf).
//
// A friend's read asked before read cards (the packet's ReadJob) keeps its
// legacy job name in generation one; a resumed generation gets its own .gN
// directory so a pre-STOP outbox report cannot be read as a new result.
func friendJobOf(p sprint.Packet) string {
	job := sprint.StoredID(p.Card, p.Epoch)
	if p.Kind == "read" && p.ReadJob != "" {
		job = p.ReadJob
	}
	if p.Gen > 1 {
		job += ".g" + strconv.Itoa(p.Gen)
	}
	return job
}

// friendBrief is the BRIEF.md of a friend's sprint card: its STATUS line, her
// configured working directory, a later attempt's start, and the brief's rules.
// A reworked card has the daemon's form (friend.ReworkedBrief), with its fix
// immediately after STATUS and its STOP updated, whichever writer delivers it.
func friendBrief(name string, p sprint.Packet) string {
	return friendBriefAtDir("", name, "", p)
}

// friendBriefAtDir names the row directory in the delivered brief (docs/FRIENDS.md).
func friendBriefAtDir(novaRoot, name, dir string, p sprint.Packet) string {
	return friend.ReworkedBrief(friendServerBriefAtDir(novaRoot, name, dir, p))
}

// friendServerBriefAtDir is the brief before a rework transforms its fix and STOP.
func friendServerBriefAtDir(novaRoot, name, dir string, p sprint.Packet) string {
	job := friendJobOf(p)
	var b strings.Builder
	fmt.Fprintf(&b, "STATUS: nova-sprint card %s, epoch %d, attempt %d; push your work to the branch %s; when done, write outbox/%s/REPORT.md with first line exactly Verdict: LAND|HOLD|FAIL, second line exactly Head: <40-hex> (blank for HOLD and FAIL)\n", p.Card, p.Epoch, p.Attempt, p.Branch, job)
	fmt.Fprintf(&b, "Work in %[1]s/jobs/%[2]s/: every clone, worktree and build output goes inside it, "+strings.ReplaceAll(swarm.FriendGoCacheLine(name), "~/"+name+"-working", friendWorkDir(novaRoot, name, dir))+", and the report goes to %[1]s/outbox/%[2]s/REPORT.md.\n", friendWorkDir(novaRoot, name, dir), job)
	if c, ok := member.CarryOf(p.Brief); ok && p.BaseHead == "" {
		// a card brief --widen widened starts from the held attempt's head (member.Carried)
		p.BaseHead, p.BaseAttempt = c.Head, c.Attempt
	}
	if p.Attempt > 1 || p.BaseHead != "" {
		b.WriteString(friendStart(p))
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

// friendStart is where a friend's later attempt starts, the rule a member's rework is staged
// by (docs/SPEC-CARD-CONTRACT.md, where a rework starts; nova-tools#5215): the current tip of
// the card's base branch on origin, never an older base, with the work of the last attempt that
// pushed carried onto it by the friend herself, and the Head she reports on that tip. A friend
// has no staged commit: nothing checks that her Head descends from the tip, and the ls-remote of
// friendFinish (Head is origin's tip of her branch) is the only guard on her finish.
func friendStart(p sprint.Packet) string {
	base := swarm.ReadCardBase([]byte(p.Brief)).Ref
	ref := "origin/" + base
	if base == "" || typedrec.IsFullSha(base) {
		base, ref = "the repository's default branch", "origin/HEAD"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "This attempt starts from the current tip of %s on origin, never from an older base: fetch it and start your branch there.", base)
	if p.BaseHead != "" {
		fmt.Fprintf(&b, " Carry the work of attempt %d onto it yourself: its head, %s, is the last pushed by any attempt before this one (`git diff %s...%s` shows that work); where it does not apply cleanly, redo it.", p.BaseAttempt, p.BaseHead, ref, p.BaseHead)
	} else {
		b.WriteString(" No attempt before this one pushed work to carry.")
	}
	b.WriteString(" The Head you report must be origin's tip of your branch when sync reads it; the attempt is expected to start from the tip named above.\n")
	return b.String()
}

// friendReportOf reads a friend's REPORT.md on a sprint card: its verdict (the first word
// of its first Verdict: line, in upper case; "" for none), its head (the first word of its
// first Head: line, in lower case, for a sha is case-insensitive hex and IsFullSha reads
// only lower case), and its first paragraph (the first block of lines that are neither a
// key line of those two nor a markdown heading), on one line.
func friendReportOf(report string) (verdict, head, para string) {
	verdict, _ = reportValue(report, "verdict")
	verdict = strings.ToUpper(strings.Trim(firstWord(verdict), "*_.,;:!"))
	head, _ = reportValue(report, "head")
	head = strings.ToLower(strings.Trim(firstWord(head), "*_`.,;:"))
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

// friendReportPinNote follows docs/FRIENDS.md, the pinned report shape. It
// shares reportValue's lenient key grammar and names the first header lines;
// their position advises the friend without changing the finish's verdict.
func friendReportPinNote(report string) string {
	lines := map[string]int{}
	for i, line := range strings.Split(report, "\n") {
		key, _, ok := strings.Cut(strings.TrimLeft(line, "#*-_ \t"), ":")
		key = strings.ToLower(strings.TrimSpace(key))
		if ok && (key == "verdict" || key == "head") && lines[key] == 0 {
			lines[key] = i + 1
		}
	}
	var where []string
	for i, key := range []string{"verdict", "head"} {
		if at := lines[key]; at != 0 && at != i+1 {
			where = append(where, fmt.Sprintf("%s: is on line %d", strings.ToUpper(key[:1])+key[1:], at))
		}
	}
	if len(where) == 0 {
		return ""
	}
	return "NOTE: friend report's " + strings.Join(where, " and ") + "; write Verdict: on line 1 and Head: on line 2"
}

func firstWord(s string) string {
	w, _, _ := strings.Cut(strings.TrimSpace(strings.TrimLeft(s, "*_ \t")), " ")
	return w
}

// maxFriendReport bounds the paragraph a friend's report puts on the work card.
const maxFriendReport = 1024

// friendReportReadCap bounds the outbox/<job>/REPORT.md friend sync reads in one
// ReadFile: a verdict, a Head line and a first paragraph need far less than 64
// KiB, and the paragraph is capped again by maxFriendReport when it is laid on
// the card; a larger report is skipped whole, never read into memory
// (docs/FRIENDS.md, the inbox/outbox standard; docs/SPEC-SPRINT.md section 1,
// friend sync).
const friendReportReadCap = 64 * 1024

// tipFn is origin's tip of a branch of a repository (a card's REPO: line, as
// swarm.ReadCardBase resolves it), "" when origin has no such branch; an error is a tip
// that could not be read.
type tipFn func(ctx context.Context, repo, branch string) (string, error)

// friendTipBudget bounds the one ls-remote of a friend's LAND, well inside the period of
// the loop friend sync runs in.
const friendTipBudget = 10 * time.Second

// branchTip is the real tipFn: one git ls-remote of the one ref, bounded.
func (a *app) branchTip(ctx context.Context, repo, branch string) (string, error) {
	ref := "refs/heads/" + branch
	out, err := gitrun.Output(ctx, gitrun.Options{Env: a.gitEnv, Timeout: friendTipBudget}, "ls-remote", "--", repo, ref)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == ref {
			return f[0], nil
		}
	}
	return "", nil
}

// friendFinish is the finish a friend's report gives her card (a reworked card's LAND that
// does not address its fix is read as a HOLD, friend.UnaddressedLand; the model is
// internal/friend/tla/OutboxFinish.tla, SyncFinish). friendCollect, the one collect of
// friend sync, friend reconcile and the run loop, calls it, and collect does too: LAND with
// a full sha Head
// that is origin's tip of the card's branch (tip, read once) is work ok at that tip, as a
// member's ok finish; HOLD, FAIL (or FAILED, BROKEN) is work that came back failed, its
// report the first paragraph, as a member's failed finish raises "work came back failed";
// one whose first paragraph names a brief defect is the brief's (sprint.BriefDefectOf), which
// the finish records in her defect cell, never in her ok%;
// a LAND with no full sha Head, or any other verdict, is failed too, saying what the
// report lacks. The report begins with the friend's name, so it is never read as a
// provider failure, no result or a staging refusal. An error refuses the finish: a Head
// that is not origin's tip (naming both shas), a branch origin does not hold, a card with
// no REPO: line, or a tip that could not be read; the card is not finished, and the next
// sync reads the report again.
func friendFinish(ctx context.Context, name string, p sprint.Packet, report string, tip tipFn) (sprint.FinishReq, error) {
	verdict, head, para := friendReportOf(report)
	if verdict == VerdictLand {
		// a LAND that does not address its brief's first line is a HOLD, head kept, by the
		// daemon's own check (friend.UnaddressedLand): whichever finishes it, it is held
		if held := friend.UnaddressedLand(friend.HeldByFriendSync, report, friendBrief(name, p)); held != "" {
			verdict, para = VerdictHold, held+" "+para
		}
	}
	para = oneline.Cap(para, maxFriendReport)
	row := sprint.FriendRow(name)
	r := sprint.FinishReq{Sel: sprint.Sel{IDs: []string{p.Card}}, As: row, Gens: map[string]int{p.Card: p.Gen}, Branch: p.Branch, Who: row}
	// published usage rides on the finish, priced as a fleet member's is (cost.go,
	// docs/SPEC-SPRINT.md, "What a card cost"). The card has no fleet route, so the
	// finish's plan stamps on_route from the route row whose provider/model matches
	// (friendStampMatchedRoute). A report with no token stays empty, so the record
	// is unpriced=no-tokens and nothing is guessed.
	if usage := friendFinishUsage(report); usage != "" {
		r.Usage = usage
	}
	switch {
	case verdict == VerdictLand && typedrec.IsFullSha(head):
		// the head is origin's tip, never the report's word: what lands is what is there
		repo := swarm.ReadCardBase([]byte(p.Brief)).Repo
		if repo == "" {
			return r, fmt.Errorf("the card names no REPO: line, so origin's tip of %s cannot be read", p.Branch)
		}
		at, err := tip(ctx, repo, p.Branch)
		switch {
		case err != nil:
			return r, fmt.Errorf("origin's tip of %s in %s cannot be read: %w", p.Branch, repo, err)
		case at == "":
			return r, fmt.Errorf("Head %s, and origin has no branch %s", head, p.Branch)
		case !strings.EqualFold(at, head):
			return r, fmt.Errorf("Head %s is not origin's tip of %s, %s", head, p.Branch, at)
		}
		r.Head, r.Report = at, "friend "+name+" LAND: "+para
	case verdict == VerdictLand:
		r.Failed, r.Report = true, "friend "+name+" LAND with no Head: <full sha>; "+para
	case verdict == VerdictHold || verdict == VerdictFail || verdict == "FAILED" || verdict == "BROKEN":
		r.Failed, r.Report = true, "friend "+name+" "+verdict+": "+para
		// a HOLD's Head, when it is origin's tip, is kept as the attempt's pushed head, so
		// brief --widen starts the next attempt from it; a tip not read keeps none, never refuses
		if repo := swarm.ReadCardBase([]byte(p.Brief)).Repo; verdict == VerdictHold && typedrec.IsFullSha(head) && repo != "" {
			if at, err := tip(ctx, repo, p.Branch); err == nil && strings.EqualFold(at, head) {
				r.Head = at
			}
		}
	default:
		r.Failed, r.Report = true, "friend "+name+" verdict "+cmp.Or(verdict, "none")+" is not LAND, HOLD or FAIL; "+para
	}
	// docs/FRIENDS.md: shifted headers still finish; the card records their lines.
	if note := friendReportPinNote(report); note != "" {
		r.Report += "; " + note
	}
	// the report's PATHS-PROPOSED line, wherever it stands, rides on the card for brief --widen
	// (docs/SPEC-SPRINT.md section 2, "recut-widen-r.w1")
	if globs, ok := member.PathsProposed(report); ok && len(globs) > 0 {
		r.Report += "; " + member.ProposedKey + " " + strings.Join(globs, ",")
	}
	return r, nil
}

// friendInbox is the directory a friend's card is delivered into, inbox/<job> of her
// working directory dir; why is a refusal: a card id that is not one (the job directory
// is named by it), or an inbox/<job> that is a symlink or a file, so nothing is written
// outside her working directory.
func friendInbox(dir string, p sprint.Packet) (in, why string, err error) {
	if !sprint.ValidCardID(p.Card) {
		return "", "the card id is not one a job directory may be named by", nil
	}
	job := friendJobOf(p)
	in = filepath.Join(dir, "inbox", job)
	fi, err := os.Lstat(in)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return in, "", nil
	case err != nil:
		return "", "", err
	case !fi.IsDir():
		return "", "inbox/" + job + " is a symlink or a file, not a directory; run: ls -la " + in, nil
	}
	return in, "", nil
}

// friendReadReport reads a friend's outbox/<job>/REPORT.md, but only when it is
// a regular file and no larger than friendReportReadCap: os.ReadFile follows a
// symlink, so a link at outbox/<job> or at REPORT.md would read a file outside
// her working directory, and a large report is read whole into memory. friend
// clean already treats a non-regular report as not done (friendclean.go, the
// Lstat and Mode().IsRegular() check near line 217); sync does the same before
// its read (docs/FRIENDS.md, the inbox/outbox standard). It returns the report
// and a why that names the path and the reason; an empty why with an empty
// report is "no report yet, still working" (nothing is said), and a non-empty
// why is "skip it, still working" — the caller says the why and continues so
// the next sync reads it again.
func friendReadReport(dir, job string) (report, why string, at time.Time, err error) {
	outDir := filepath.Join(dir, "outbox", job)
	outName := filepath.Join("outbox", job)
	reportName := filepath.Join(outName, "REPORT.md")
	fi, err := os.Lstat(outDir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", "", time.Time{}, nil
	case err != nil:
		return "", "", time.Time{}, err
	case !fi.IsDir():
		return "", outName + " is a symlink or a file, not a directory", time.Time{}, nil
	}
	fi, err = os.Lstat(filepath.Join(outDir, "REPORT.md"))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", "", time.Time{}, nil
	case err != nil:
		return "", "", time.Time{}, err
	case !fi.Mode().IsRegular():
		return "", reportName + " is a symlink or a non-regular file", time.Time{}, nil
	case fi.Size() > friendReportReadCap:
		return "", reportName + " is larger than " + strconv.Itoa(friendReportReadCap) + " bytes", time.Time{}, nil
	}
	b, err := os.ReadFile(filepath.Join(outDir, "REPORT.md"))
	if err != nil {
		return "", "", time.Time{}, err
	}
	report = string(b)
	// RESULT.md carries the same tokens: and cost: lines (internal/friend, PublishCost).
	// A missing file, a symlink or a file past the cap adds nothing: the report still
	// finishes, and a Cost: headline in it is read on its own.
	extra, err := friendResultLines(outDir)
	if err != nil {
		return "", "", time.Time{}, err
	}
	if extra != "" {
		// RESULT.md's tokens win over a Cost: headline in the report (the
		// result when it has them, the report when it does not).
		if friendHasPublishedTokens(extra) {
			report = extra + "\n" + report
		} else {
			report = strings.TrimRight(report, "\n") + "\n" + extra
			if !strings.HasSuffix(report, "\n") {
				report += "\n"
			}
		}
	}
	return report, "", fi.ModTime(), nil
}

// friendHasPublishedTokens says the text carries a tokens: line or a Cost:
// headline's tokens segment, so it is the finish's usage rather than a dollar
// beside one.
func friendHasPublishedTokens(text string) bool {
	_, _, saw := friendTokenLine(text)
	return saw
}

// friendResultLines is RESULT.md's tokens: and cost: lines, when the file is a
// regular file no larger than friendReportReadCap. "" when it is absent, a
// symlink, or too large. An error is a read that failed after the file checked out.
func friendResultLines(outDir string) (string, error) {
	name := filepath.Join(outDir, "RESULT.md")
	fi, err := os.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "", nil
	case err != nil:
		return "", err
	case !fi.Mode().IsRegular() || fi.Size() > friendReportReadCap:
		return "", nil
	}
	b, err := os.ReadFile(name)
	if err != nil {
		return "", err
	}
	var lines []string
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		key, _, ok := strings.Cut(strings.TrimLeft(t, "#*-_ \t"), ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "tokens", "cost":
			lines = append(lines, t)
		}
	}
	return strings.Join(lines, "\n"), nil
}

// friendFinishUsage is the usage a friend's finish records (docs/SPEC-SPRINT.md,
// "What a card cost"): the tokens: line, or the tokens segment of a Cost: headline,
// with actual_usd from the cost: line when it carries a reported harness dollar.
// A usage: line is the same counts when neither is there, so the friends table's
// tokens column still sums it. "" when no token class was reported: the finish
// then records unpriced=no-tokens and invents nothing.
func friendFinishUsage(report string) string {
	tokenLine, costRest, saw := friendTokenLine(report)
	if saw {
		u, ok := sprint.FriendUsage("tokens: " + tokenLine)
		if !ok || !u.Tokens.Reported() {
			return ""
		}
		return friendUsageLine(u, costRest)
	}
	u, ok := sprint.FriendUsage(report)
	if !ok || !u.Tokens.Reported() {
		return ""
	}
	return friendUsageLine(u, costRest)
}

// friendTokenLine is the first tokens: line, or the tokens segment of the first
// Cost: headline, and the cost text a dollar is read from: a bare cost: line
// when one is there, else the headline's text before its tokens segment.
func friendTokenLine(report string) (tokens, costRest string, saw bool) {
	var bare, embedded string
	for _, line := range strings.Split(report, "\n") {
		t := strings.TrimSpace(strings.TrimLeft(line, "#*-_ \t"))
		key, rest, ok := strings.Cut(t, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		rest = strings.TrimSpace(rest)
		switch key {
		case "tokens":
			if !saw {
				tokens, saw = rest, true
			}
		case "cost":
			if i := strings.Index(rest, " tokens "); i >= 0 {
				if !saw {
					tokens, saw = strings.TrimSpace(rest[i+len(" tokens "):]), true
				}
				if embedded == "" {
					embedded = strings.TrimSpace(rest[:i])
				}
			} else if bare == "" {
				bare = rest
			}
		}
	}
	if bare != "" {
		return tokens, bare, saw
	}
	return tokens, embedded, saw
}

// friendUsageLine is a parsed usage ready for the finish to price: the route and
// any harness word are dropped, so the finish's own route row prices it.
// actual_usd is the harness parenthetical when that word is a dollar. When the
// parenthetical is present and not a dollar ("-"), actual is absent: the
// list-price headline is not recorded. With no parenthetical, the first dollar is.
func friendUsageLine(u cardcost.Usage, costRest string) string {
	u.Route, u.Prices, u.Predicted, u.Unpriced = "", "", "", ""
	u.Long = false
	var extra []string
	for _, w := range u.Extra {
		k, _, _ := strings.Cut(w, "=")
		if k == "harness" || k == "price_route" {
			continue
		}
		extra = append(extra, w)
	}
	u.Extra = extra
	usd, harness := friendUSD(costRest)
	switch {
	case harness && usd != "":
		u.Actual, u.ActualBy = usd, cardcost.ActualByHarness
	case harness:
		u.Actual, u.ActualBy = "", ""
	case usd != "":
		u.Actual, u.ActualBy = usd, cardcost.ActualByHarness
	}
	return u.String()
}

// friendUSD is the dollar a cost line carries. An opencode: or harness:
// parenthetical is authoritative: its first word, when a dollar, is the actual,
// and "-" or any other non-decimal leaves the actual absent. The list-price
// headline beside that parenthetical is not read. With no parenthetical, the
// first non-negative decimal is the actual, a leading $ stripped.
func friendUSD(rest string) (usd string, harness bool) {
	lower := strings.ToLower(rest)
	for _, key := range []string{"opencode:", "harness:"} {
		if i := strings.Index(lower, key); i >= 0 {
			return harnessUSD(rest[i+len(key):]), true
		}
	}
	return friendFirstUSD(rest), false
}

// harnessUSD is the first word after opencode: or harness:. A dollar is that
// cost. "-" and any other non-decimal are unreported.
func harnessUSD(rest string) string {
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	w := strings.Trim(fields[0], "(),;")
	w = strings.TrimPrefix(w, "$")
	r, err := cardcost.Decimal(w)
	if err != nil {
		return ""
	}
	return cardcost.Text(r)
}

// friendFirstUSD is the first non-negative decimal in text, a leading $ stripped.
func friendFirstUSD(rest string) string {
	for _, w := range strings.Fields(rest) {
		w = strings.Trim(w, "(),;")
		w = strings.TrimPrefix(w, "$")
		r, err := cardcost.Decimal(w)
		if err != nil {
			continue
		}
		return cardcost.Text(r)
	}
	return ""
}

// friendStampMatchedRoute sets on_route on each cost record the finish just
// planned. A friend's card has no fleet route, so workConsumer writes on_route=-
// while costRecord still prices by the route row whose provider/model matches
// and records that name as price_route. on_route becomes that name. A record
// that already names a route, or that matched none, is left as it is.
func friendStampMatchedRoute(p *sprint.Plan) {
	if p == nil {
		return
	}
	for i := range p.Units {
		for j := range p.Units[i].Changes {
			set := p.Units[i].Changes[j].Entry.Set
			for k, v := range set {
				if !strings.HasPrefix(k, sprint.FieldCostRecord) {
					continue
				}
				if next, ok := withMatchedOnRoute(v); ok {
					set[k] = next
				}
			}
		}
	}
}

// withMatchedOnRoute copies price_route onto an empty on_route ("-" or blank).
// A route already written stays. ok is false when there is nothing to copy.
func withMatchedOnRoute(line string) (string, bool) {
	fields := strings.Fields(line)
	on := -1
	route := ""
	for i, w := range fields {
		k, val, ok := strings.Cut(w, "=")
		if !ok {
			continue
		}
		switch k {
		case "on_route":
			on = i
			if val != "-" && val != "" {
				return "", false
			}
		case "price_route":
			if route == "" {
				route = val
			}
		}
	}
	if on < 0 || route == "" || route == "-" {
		return "", false
	}
	fields[on] = "on_route=" + route
	return strings.Join(fields, " "), true
}

// friendCardsOf delivers and collects one friend's sprint cards in her working directory
// dir: every card working on her row is written as inbox/<job>/BRIEF.md when it is not
// there (written whole, never over a file there, by atomicfile), and finished from
// outbox/<job>/REPORT.md when that is there. A card whose id is not a card id, or whose
// inbox/<job> is a symlink or no directory, is refused, a line each: nothing is written
// outside her working directory. It says what it did, a line each, and how many it
// delivered and finished. A batch friend (the default) is woken once, after this
// call's deliveries, for the cards the pass wrote; a one-shot friend is woken per
// card, which is how her runner starts a lane (docs/FRIENDS.md).
func (a *app) friendCardsOf(ctx context.Context, st *store.Store, name, dir, rowDir string, say func(string)) (delivered, finished int, err error) {
	// her working cards, then the ready ones dealt behind them (sprint.TickDeal): both are
	// delivered, and her queue file says which are which
	// and the ones taken back from her (sprint.FriendTake), withdrawn on her row until the deal
	// places them again: taken in her queue file, so her daemon starts none of them
	// a queued card no longer on her row that is dealt to another (friend level, or a take
	// dealt again) left her: taken in her queue file too
	left := func(ids []string) (map[string]bool, error) {
		cards, err := st.Records(ctx, sprint.Fleet, ids)
		out := map[string]bool{}
		for _, c := range cards {
			if c != nil && c.Row != sprint.FriendRow(name) && (c.Col == sprint.Ready || c.Col == sprint.Working || c.Col == sprint.Withdrawn) {
				out[c.ID] = true
			}
		}
		return out, err
	}
	all, err := st.ReadCells(ctx, sprint.Fleet, sprint.FriendRow(name), sprint.Working, sprint.Ready, sprint.Withdrawn)
	if err != nil {
		return 0, 0, err
	}
	states := map[string]string{}
	if len(all) == 0 {
		// none on her row: a queue file there still has its queued cards that left her
		// marked taken (writeQueueFile), and none is made
		if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(queueFile))); err != nil {
			return 0, 0, nil
		}
		return 0, 0, writeQueueFile(dir, states, left, nil)
	}
	spec, err := st.FriendSpecOf(ctx, name)
	if err != nil {
		return 0, 0, err
	}
	// dealt is this pass's new files. One-shot wakes inside the loop; batch
	// waits until the files and the queue write have been attempted.
	var dealt []sprint.Packet
	wake := func(p sprint.Packet, brief, line string) error {
		if spec.Mode == config.FriendModeOneShot {
			return a.wakeFriend(ctx, st, name, p, brief, line, say)
		}
		dealt = append(dealt, p)
		return nil
	}
	var cards []*sprint.Card
	for _, c := range all {
		states[c.ID] = map[string]string{string(sprint.Working): "working", string(sprint.Ready): "queued", string(sprint.Withdrawn): queueTaken}[string(c.Col)]
		if c.Col != sprint.Withdrawn {
			cards = append(cards, c)
		}
	}
	packets, err := st.Packets(ctx, cards)
	if err != nil {
		return 0, 0, err
	}
	defer func() {
		if err == nil {
			err = writeQueueFile(dir, states, left, packets)
		}
		// Files already delivered stand even if a later collect or the queue
		// write fails; their one courtesy wake still belongs to this pass.
		if len(dealt) > 0 {
			err = errors.Join(err, a.wakeFriendPass(ctx, st, name, filepath.Join(dir, "inbox"), dealt, say))
		}
	}()
	for i, p := range packets {
		if p.Kind == "read" {
			d, f, err := a.friendReadOf(ctx, st, name, dir, rowDir, p, cards[i], say, wake)
			delivered, finished = delivered+d, finished+f
			if err != nil {
				return delivered, finished, err
			}
			continue
		}
		job := friendJobOf(p)
		in, why, err := friendInbox(dir, p)
		if err != nil {
			return delivered, finished, err
		}
		if why != "" {
			say(fmt.Sprintf("FRIEND-CARD REFUSED friend=%s card=%s: %s; nothing was written", name, oneline.Field(p.Card), oneline.Escape(why)))
			continue
		}
		brief := filepath.Join(in, "BRIEF.md")
		if _, err := os.Lstat(brief); errors.Is(err, fs.ErrNotExist) {
			if err := os.MkdirAll(in, 0o755); err != nil {
				return delivered, finished, err
			}
			switch err := atomicfile.WriteFile(brief, []byte(friendBriefAtDir(a.machineNovaRoot(), name, rowDir, p)), 0o644, atomicfile.NoReplace()); {
			case err == nil:
				delivered++
				line := fmt.Sprintf("FRIEND-CARD DELIVERED friend=%s card=%s job=%s branch=%s", name, p.Card, oneline.Field(job), p.Branch)
				say(line)
				if err := wake(p, brief, line); err != nil {
					return delivered, finished, err
				}
			case !errors.Is(err, fs.ErrExist):
				return delivered, finished, err
			}
		} else if err != nil {
			return delivered, finished, err
		}
		report, why, at, err := friendReadReport(dir, job)
		if err != nil {
			return delivered, finished, err
		}
		if report == "" {
			if why != "" {
				say(fmt.Sprintf("FRIEND-CARD REFUSED friend=%s card=%s: %s; the card is left working, and the next sync reads it again", name, oneline.Field(p.Card), oneline.Escape(why)))
			}
			continue
		}
		done, err := a.friendCollect(ctx, st, name, p, report, "", at, say)
		if err != nil {
			return delivered, finished, err
		}
		if done {
			finished++
		}
	}
	return delivered, finished, nil
}

// busRedisEnv names the friends' bus store (nova-bus's), where friend sync
// wakes a friend's daemon when it delivers her a card.
const busRedisEnv = "NOVA_BUS_REDIS"

// busUserEnv and busPasswordEnvEnv are the bus's own login, apart from the sprint store's.
const (
	busUserEnv        = "NOVA_BUS_REDIS_USER"
	busPasswordEnvEnv = "NOVA_BUS_REDIS_PASSWORD_ENV"
)

// busSendFn sends one message on the friends' bus; say is handed each line the send has
// for the verb's output (a bus store's alarm raised or cleared).
type busSendFn func(ctx context.Context, m bus.Message, say func(string)) error

// busWatch is the app's Watch of the bus store addr as user, one per store and user
// for the life of the process: its alarm is raised at the first send that fails on the
// login or the connection and cleared at the next that succeeds. Each is queued as its
// one line (Alarm.Text) for the send that saw it to say (sendBus).
func (a *app) busWatch(addr, user string) *bus.Watch {
	a.busWatchesMu.Lock()
	defer a.busWatchesMu.Unlock()
	key := addr + ":" + user
	if w, ok := a.busWatches[key]; ok {
		return w
	}
	queue := func(al bus.Alarm) {
		a.busAlarmsMu.Lock()
		defer a.busAlarmsMu.Unlock()
		a.busAlarms = append(a.busAlarms, al.Text())
	}
	w := &bus.Watch{Store: addr, User: user, Raise: queue, Clear: queue}
	if a.busWatches == nil {
		a.busWatches = map[string]*bus.Watch{}
	}
	a.busWatches[key] = w
	return w
}

// busOptions selects the login for the bus connection used by friend sync
// (SPEC-SPRINT section 1). It holds variable names, never a password value.
func busOptions(getenv func(string) string) redisconn.Options {
	addr := getenv(busRedisEnv)
	user := getenv(busUserEnv)
	o := redisconn.Options{Addr: addr, Env: redisconn.Env{User: busUserEnv}}
	if user != "" {
		o.Env.PasswordEnv = busPasswordEnvEnv
	}
	return o
}

// openBus is the real busOpen: the bus store dialed as nova-bus dials it
// (internal/redisconn, the fleet's login from the environment).
func (a *app) openBus(ctx context.Context, addr, user string) (*bus.Bus, func(), error) {
	o := busOptions(a.getenv)
	if addr != "" {
		o.Addr = addr
	}
	conn, err := redisconn.Open(ctx, o, a.getenv)
	if err != nil {
		return nil, nil, err
	}
	return &bus.Bus{Store: bus.Redis{C: conn.Client()}}, func() {
		_ = conn.Close() // ignored: the connection is closed at the end of the send; a failed close has no one to tell
	}, nil
}

// sendBus is the real busSendFn: one message sent through friend.Courier on a
// connection opened for it (busOpen) and closed after, its result watched
// (busWatch). The alarm a send raises or clears is said as one FRIEND-CARD
// BUS-ALARM line, and a send that fails while the alarm is raised says so in its
// error, which friend sync writes on the card's story.
func (a *app) sendBus(ctx context.Context, m bus.Message, say func(string)) error {
	addr := a.getenv(busRedisEnv)
	if addr == "" {
		return errors.New(busRedisEnv + " is not set: no bus to send on")
	}
	user := a.getenv(busUserEnv)
	c := &friend.Courier{
		Now:   func() time.Time { return a.now() },
		Open:  func(ctx context.Context) (*bus.Bus, func(), error) { return a.busOpen(ctx, addr, user) },
		Watch: a.busWatch(addr, user),
	}
	_, err := c.Send(ctx, m)
	a.busAlarmsMu.Lock()
	said := a.busAlarms
	a.busAlarms = nil
	a.busAlarmsMu.Unlock()
	for _, text := range said {
		say("FRIEND-CARD BUS-ALARM " + oneline.Escape(text))
	}
	if err != nil && c.Watch.Open() {
		return fmt.Errorf("%w (the bus store's alarm is raised: FRIEND-CARD BUS-ALARM)", err)
	}
	return err
}

// enrollBus makes the friend a known name of the bus store before a message is
// sent her: her name is read from her friend row (friend sync deals only to
// rows), and the bus store, which nova-config's apply never writes when it is
// a Redis apart from the sprint store (NOVA_BUS_REDIS beside
// NOVA_SPRINT_REDIS), is told it (bus.Enroll; SPEC-BUS.md, the config). A
// name added is said as one FRIEND-CARD BUS-NAMES line; one that could not be
// is said as a FRIEND-CARD NOTE, and the send after it says the rest. With no
// bus store set it does nothing: the send names that.
func (a *app) enrollBus(ctx context.Context, name string, say func(string)) {
	addr := a.getenv(busRedisEnv)
	if addr == "" {
		return
	}
	b, closeBus, err := a.busOpen(ctx, addr, a.getenv(busUserEnv))
	if err == nil {
		var added []string
		added, err = b.Enroll(ctx, name)
		if closeBus != nil {
			closeBus()
		}
		if len(added) > 0 {
			say(fmt.Sprintf("FRIEND-CARD BUS-NAMES added=%s: the bus store now knows the friend row", strings.Join(added, ",")))
		}
	}
	if err != nil {
		say(fmt.Sprintf("FRIEND-CARD NOTE friend=%s: her name could not be put on the bus store's roster (%s)", name, oneline.Escape(err.Error())))
	}
}

// wakeFriend tells a one-shot friend of the card just delivered, one bus message
// from the coordinator (the store's actor) to her: her runner starts a lane from
// it, which the inbox file alone never does. Her name is made a known bus name
// first (enrollBus), so a bud whose row exists is told. The message is a
// courtesy and the inbox file is the record: a send that fails never fails
// the delivery; it is said on sync's line and written on the card's story as
// one happened note (NFriendNotWoken), so the coordinator sees she was not
// told. Batch mode does not call this; wakeFriendPass sends one message for the pass.
func (a *app) wakeFriend(ctx context.Context, st *store.Store, name string, p sprint.Packet, brief, line string, say func(string)) error {
	a.enrollBus(ctx, name, say)
	start := "Read it and start; its STATUS line says where to push and where to report."
	if p.Kind == "read" {
		start = "It is a read: read it and start; its STATUS line says where to report, and nothing is pushed."
	}
	m := bus.Message{From: st.Actor, To: []string{name}, Subject: "card " + p.Card + " dealt: " + line,
		Body: "Your sprint card " + p.Card + " (attempt " + strconv.Itoa(p.Attempt) + " of " + p.Primary + ") is in your inbox: " + brief + "\n" + start}
	err := a.bus(ctx, m, say)
	if err == nil {
		return nil
	}
	why := oneline.Escape(err.Error())
	say(fmt.Sprintf("FRIEND-CARD NOTE friend=%s card=%s: the bus message to her was not sent (%s); the inbox file stands, tell her by hand", name, p.Card, why))
	n := sprint.Note{Kind: sprint.Happened, Type: sprint.NFriendNotWoken, Stream: p.Stream, Primaries: []string{p.Primary}, Who: st.Actor, Attempt: p.Attempt,
		What: fmt.Sprintf("%s was dealt %s into %s, and the bus message to her failed: %s; tell her by hand: nova-bus send --as %s --to %s --subject 'card %s dealt' --body '%s'", name, p.Card, brief, why, st.Actor, name, p.Card, brief)}
	res, err := st.Run(ctx, store.NoteStep("friend sync", n))
	if err == nil && len(res.Refused) > 0 {
		err = errors.New(res.Refused[0].Why)
	}
	return err
}

// wakeFriendPass is the batch wake (docs/FRIENDS.md): one status message for the
// files this pass delivered, ids cut at ten, and one pass note if the send fails.
// The inbox files are the record; a failed send does not undo them.
func (a *app) wakeFriendPass(ctx context.Context, st *store.Store, name, inbox string, dealt []sprint.Packet, say func(string)) error {
	a.enrollBus(ctx, name, say)
	ids := make([]string, 0, min(len(dealt), 10)+1)
	for _, p := range dealt[:min(len(dealt), 10)] {
		ids = append(ids, p.Card)
	}
	if len(dealt) > 10 {
		ids = append(ids, fmt.Sprintf("and %d more", len(dealt)-10))
	}
	subject := fmt.Sprintf("cards dealt: %d (%s)", len(dealt), strings.Join(ids, ", "))
	start := "Read the inbox briefs and start; each STATUS line says where to push and where to report."
	m := bus.Message{From: st.Actor, To: []string{name}, Kind: bus.KindStatus, Subject: subject,
		Body: "Your sprint cards are in your inbox: " + inbox + "\n" + start}
	if err := a.bus(ctx, m, say); err != nil {
		why := oneline.Escape(err.Error())
		say(fmt.Sprintf("FRIEND-CARD NOTE friend=%s pass=%s: the bus message to her was not sent (%s); the inbox files stand, tell her by hand", name, oneline.Field(subject), why))
		n := sprint.Note{Kind: sprint.Happened, Type: sprint.NFriendNotWoken, Count: len(dealt), Who: st.Actor,
			What: fmt.Sprintf("friend sync pass for %s delivered %s into %s, and its bus message failed: %s; tell her by hand: nova-bus send --as %s --to %s --subject '%s' --body '%s'", name, subject, inbox, why, st.Actor, name, subject, inbox)}
		res, err := st.Run(ctx, store.NoteStep("friend sync", n))
		if err == nil && len(res.Refused) > 0 {
			err = errors.New(res.Refused[0].Why)
		}
		return err
	}
	return nil
}

// stallWaker is the store's WakeFriend for the machine (tick and run): the friend stall
// part's wake turn sent on the bus (wakeFriendStall), a message not sent said on out.
func (a *app) stallWaker(st *store.Store, out io.Writer) func(string, int, time.Duration) error {
	return func(name string, rung int, d time.Duration) error {
		return a.wakeFriendStall(context.Background(), st, name, rung, d, func(l string) { fmt.Fprintln(out, l) })
	}
}

// wakeFriendStall wakes a friend whose stall ladder has climbed to a wake rung (1 or 2;
// docs/SPEC-SPRINT.md section friend-stall-ladder-r.w1; the model is tla/StallLadder.tla):
// a bus message pushed to her daemon as a turn, waking her to resume or report progress.
func (a *app) wakeFriendStall(ctx context.Context, st *store.Store, name string, rung int, d time.Duration, say func(string)) error {
	m := bus.Message{
		From:    st.Actor,
		To:      []string{name},
		Subject: fmt.Sprintf("stall wake: friend %s turn %d (%s)", name, rung, d.Round(time.Minute)),
		Body:    fmt.Sprintf("Your session has shown no activity for %s while holding dealt sprint cards (wake turn %d); please resume work or report progress.", d.Round(time.Minute), rung),
	}
	err := a.bus(ctx, m, say)
	if err == nil {
		return nil
	}
	why := oneline.Escape(err.Error())
	if say != nil {
		say(fmt.Sprintf("FRIEND-STALL NOTE friend=%s rung=%d: the bus message to her was not sent (%s); tell her by hand", name, rung, why))
	}
	return err
}

// friendReadText is the BRIEF.md of a friend's read, as friend sync and friend cards
// both write it: the read's brief, the attempt's branch, start commit and head (the packet's,
// else the card's), and a deadline of thirty minutes on the sprint clock.
func friendReadText(st *store.Store, name string, p sprint.Packet, c *sprint.Card) string {
	if p.ReadJob != "" {
		// a read asked the old way keeps the old brief
		branch, head, start := p.WorkBranch, p.Head, ""
		if c != nil {
			branch, head, start = cmp.Or(branch, c.F("branch")), cmp.Or(head, c.F("head")), c.F("start")
		}
		var deadline time.Time
		if st.Now != nil {
			deadline = st.Now().Add(sprint.FriendReadDeadline)
		}
		return sprint.FriendReadBrief(name, p.Primary, p.Brief, branch, start, head, p.Attempt, deadline)
	}
	start := ""
	var deadline time.Time
	if c != nil {
		p.WorkBranch, p.Head = cmp.Or(p.WorkBranch, c.F("branch")), cmp.Or(p.Head, c.F("head"))
		start = c.F("start")
		if c.Col == sprint.Working {
			// a read dealt to her working runs from its deal (sprint read_cards.go, readStart)
			deadline, _ = time.Parse(time.RFC3339, c.F("asked"))
			if !deadline.IsZero() {
				deadline = deadline.Add(sprint.ReadCardDeadline)
			}
		}
	}
	return sprint.ReadCardBrief(name, friendJobOf(p), p, start, deadline)
}

// friendReadTextAtDir names the row directory in a review brief header
// (docs/FRIENDS.md), keeping the pure sprint generators independent of config.
func friendReadTextAtDir(st *store.Store, novaRoot, name, dir string, p sprint.Packet, c *sprint.Card) string {
	text := friendReadText(st, name, p, c)
	header, body, found := strings.Cut(text, "\n\n")
	if !found {
		return text
	}
	return strings.ReplaceAll(header, "~/"+name+"-working", friendWorkDir(novaRoot, name, dir)) + "\n\n" + body
}

// friendReadOf delivers one friend's read and closes it from the friend's
// report. The brief is the read's (sprint.FriendReadBrief: the AS A READ
// section, the attempt's branch, start commit and head, deadline thirty
// minutes on the sprint clock), and the close retires the fleet card
// (sprint.FriendReadClose). It is not a work finish. The job directory is
// the card id, the path the ask writes, so a brief already there is kept.
// wake is the pass's: one-shot sends now, batch records the card for the one message.
func (a *app) friendReadOf(ctx context.Context, st *store.Store, name, dir, rowDir string, p sprint.Packet, c *sprint.Card, say func(string), wake func(sprint.Packet, string, string) error) (delivered, finished int, err error) {
	job := friendJobOf(p)
	in, why, err := friendInbox(dir, p)
	if err != nil {
		return 0, 0, err
	}
	if why != "" {
		say(fmt.Sprintf("FRIEND-READ REFUSED friend=%s card=%s: %s; nothing was written", name, oneline.Field(p.Card), oneline.Escape(why)))
		return 0, 0, nil
	}
	brief := filepath.Join(in, "BRIEF.md")
	if _, err := os.Lstat(brief); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(in, 0o755); err != nil {
			return 0, 0, err
		}
		text := friendReadTextAtDir(st, a.machineNovaRoot(), name, rowDir, p, c)
		switch err := atomicfile.WriteFile(brief, []byte(text), 0o644, atomicfile.NoReplace()); {
		case err == nil:
			delivered++
			line := fmt.Sprintf("FRIEND-READ DELIVERED friend=%s card=%s job=%s", name, p.Card, oneline.Field(job))
			say(line)
			if err := wake(p, brief, line); err != nil {
				return delivered, finished, err
			}
		case !errors.Is(err, fs.ErrExist):
			return delivered, finished, err
		}
	} else if err != nil {
		return delivered, finished, err
	}
	report, why, _, err := friendReadReport(dir, job) // a read close takes no report time; the work finish does
	if err != nil {
		return delivered, finished, err
	}
	if report == "" {
		if why != "" {
			say(fmt.Sprintf("FRIEND-READ REFUSED friend=%s card=%s: %s; the card is left working, and the next sync reads it again", name, oneline.Field(p.Card), oneline.Escape(why)))
		}
		return delivered, finished, nil
	}
	primary, card, reportCopy, epoch, generation := p.Primary, p.Card, report, p.Epoch, p.Gen
	// a broken report on a branch origin does not hold is the machine's fault: checked here,
	// at the close, and the read asked again (sprint read_missing.go)
	var missing map[string]sprint.MissingBranch
	if verdict, _, _ := sprint.ParseFriendReadReport(report); verdict == "broken" {
		missing = a.readMissing(ctx, []sprint.Packet{p})
	}
	step := store.Step{Verb: "read", Named: true, Mirrors: true, ReportsWork: true, Load: []string{sprint.Fleet, sprint.Work},
		Extras: sprint.NamedExtras(sprint.Fleet, []string{p.Card}), Actor: sprint.FriendRow(name), Epoch: &epoch,
		Plan: func(s *sprint.Snapshot) sprint.Plan {
			return sprint.FriendReadCloseChecked(s, name, primary, card, reportCopy, missing, generation)
		}}
	res, err := st.Run(ctx, step)
	if err != nil {
		return delivered, finished, err
	}
	if len(res.Refused) > 0 {
		say(fmt.Sprintf("FRIEND-READ REFUSED friend=%s card=%s: %s", name, p.Card, oneline.Escape(res.Refused[0].Why)))
		return delivered, finished, nil
	}
	finished++
	say(fmt.Sprintf("FRIEND-READ finished friend=%s card=%s", name, p.Card))
	return delivered, finished, nil
}

// queueFile is the friend's queue file under her working directory, nova-friend's
// daemon's: one record per task, its state queued, working or done;
// her daemon's pong reports its counts (queue, working).
const queueFile = "inbox/QUEUE.json"

// queueTaken is the queue file's state of a card taken back from her (friend take, friend
// down): not hers to start.
const queueTaken = "taken"

// friendQueue is the queue file's shape, as nova-friend reads it.
type friendQueue struct {
	Tasks []friendTask `json:"tasks"`
}

type friendTask struct {
	Gen int    `json:"gen,omitempty"`
	Job string `json:"job,omitempty"`

	ID          string `json:"id"`
	State       string `json:"state"`
	Deliverable string `json:"deliverable,omitempty"`
}

// writeQueueFile keeps the friend's queue file as the sprint sees her cards: each card
// on her row is a record, queued while it is ready behind her working cards, working
// while it is working, and taken once the coordinator has taken it back or a queued one
// has been dealt to another (friend level, leftOf); a record the sprint does not name, or one her session marked
// done, is kept as it is. The file is written whole (atomicfile), and not at all when
// nothing changes. docs/FRIENDS.md: a new generation or epoch resets a done record;
// an unchanged job preserves the session's completion.
func writeQueueFile(dir string, states map[string]string, leftOf func(ids []string) (map[string]bool, error), packets []sprint.Packet) error {
	jobs := map[string]friendTask{}
	for _, p := range packets {
		jobs[p.Card] = friendTask{ID: p.Card, Gen: max(1, p.Gen), Job: friendJobOf(p)}
	}
	path := filepath.Join(dir, filepath.FromSlash(queueFile))
	var q friendQueue
	before, err := os.ReadFile(path)
	if err == nil {
		if json.Unmarshal(before, &q) != nil {
			// ignored: a file that is no queue is replaced by one (the bare list form is read too)
			_ = json.Unmarshal(before, &q.Tasks)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var gone []string
	for _, t := range q.Tasks {
		if _, ok := states[t.ID]; !ok && t.State == "queued" && sprint.ValidCardID(t.ID) {
			gone = append(gone, t.ID)
		}
	}
	left := map[string]bool{}
	if len(gone) > 0 {
		if left, err = leftOf(gone); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for i, t := range q.Tasks {
		if state, ok := states[t.ID]; ok {
			seen[t.ID] = true
			job, assigned := jobs[t.ID]
			newJob := assigned && (max(1, t.Gen) != job.Gen || (t.Job != "" && t.Job != job.Job))
			if t.State != "done" || newJob {
				q.Tasks[i].State = state
			}
			if assigned {
				q.Tasks[i].Gen, q.Tasks[i].Job = job.Gen, job.Job
				if newJob {
					q.Tasks[i].Deliverable = ""
				}
			}
		} else if left[t.ID] {
			// queued, and dealt to another now: it left without her starting it (friend
			// level, or a take dealt again elsewhere), so it is not hers to start
			q.Tasks[i].State = queueTaken
		}
	}
	for _, id := range slices.Sorted(maps.Keys(states)) {
		if !seen[id] {
			task := jobs[id]
			task.ID, task.State = id, states[id]
			q.Tasks = append(q.Tasks, task)
		}
	}
	after, err := json.MarshalIndent(q, "", "  ")
	if err != nil {
		return err
	}
	after = append(after, '\n')
	if bytes.Equal(before, after) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicfile.WriteFile(path, after, 0o644)
}

// friendCollect finishes one card working on a friend's row from her report (friendFinish,
// then the finish step as her row), the one collect of friend sync and friend reconcile
// (docs/SPEC-SPRINT.md section 1, friend sync and friend reconcile). It says what it did in
// a line: FINISHED, or REFUSED when the tip or the sprint refused the finish, the card left
// working for the next sync to read the report again; done says the card was finished. op,
// when not empty, is the caller's --op: the finish runs under op.collect.<its args> (one
// operation id per card, as land.go gives each merge its own), so a retry of the verb with
// the same --op returns the recorded result.
func (a *app) friendCollect(ctx context.Context, st *store.Store, name string, p sprint.Packet, report, op string, at time.Time, say func(string)) (done bool, err error) {
	r, err := friendFinish(ctx, name, p, report, a.tip)
	if err != nil {
		say(fmt.Sprintf("FRIEND-CARD REFUSED friend=%s card=%s: %s; the card is not finished, and the next sync reads the report again", name, p.Card, oneline.Escape(err.Error())))
		return false, nil
	}
	r.Reported = at
	step := store.FinishStep(r)
	inner := step.Plan
	step.Plan = func(s *sprint.Snapshot) sprint.Plan {
		p := inner(s)
		friendStampMatchedRoute(&p)
		return p
	}
	step.Actor, step.Epoch = r.Who, &p.Epoch
	if op != "" {
		step.CallerOp = op + ".collect." + step.Args
	}
	res, err := st.Run(ctx, step)
	if err != nil {
		return false, err
	}
	if len(res.Refused) > 0 {
		say(fmt.Sprintf("FRIEND-CARD REFUSED friend=%s card=%s: %s", name, p.Card, oneline.Escape(res.Refused[0].Why)))
		return false, nil
	}
	// a finish from the report her session wrote is her session's evidence: her row reads up
	// on it for sprint.FriendFinishWindow (docs/SPEC-FRIEND.md, "Presence is her session's
	// evidence"); a record not written costs her that, never the finish
	if err := st.FriendFinished(ctx, name, a.now()); err != nil {
		say(fmt.Sprintf("FRIEND-CARD NOTE friend=%s card=%s: the finish is not recorded as her evidence: %s", name, p.Card, oneline.Escape(err.Error())))
	}
	result := "ok"
	if r.Failed {
		result = "failed"
	}
	say(fmt.Sprintf("FRIEND-CARD FINISHED friend=%s card=%s result=%s head=%s: %s", name, p.Card, result, cmp.Or(r.Head, "-"), oneline.Escape(oneline.Cap(r.Report, 200))))
	return true, nil
}
