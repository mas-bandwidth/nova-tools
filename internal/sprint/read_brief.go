package sprint

import (
	"fmt"
	"strings"
	"time"
)

// ReadCardBrief is the BRIEF.md of a read card delivered to a friend's inbox (inbox/<job>/,
// the job a work card of hers would have: friend sync, friendJobOf), whole for a lane that
// has never seen the sprint (docs/SPEC-SPRINT.md section 6, "A read is a consumer card"):
// what a read is, the work under review (repository, branch, head, base, the attempt's start),
// how to read it, how to finish (outbox/<job>/REPORT.md whose first line is "Verdict: LAND"
// or "Verdict: HOLD", a HOLD naming each defect), and the card under review verbatim with the
// worker's report. bench is the line a lane gates on (sprint.BenchLine, docs/SPEC-SPRINT.md
// section 5, "the bench a lane gates on"): the least loaded bench with its reason, written
// into the STATUS line so the read's lane runs its go commands on it and never on a name by
// habit. A one-shot runner that runs inbox/<job>/BRIEF.md and publishes outbox/<job>/REPORT.md
// runs it unchanged; friend sync closes the read from that report (FriendReadClose).
func ReadCardBrief(name, job string, p Packet, start string, deadline time.Time, bench string) string {
	repo, base := briefLine(p.Brief, "REPO:"), briefLine(p.Brief, "BASE:")
	base = firstNonEmpty(p.WorkBase, base)
	var b strings.Builder
	fmt.Fprintf(&b, "STATUS: nova-sprint read card %s, epoch %d: a READ of attempt %d of %s; change nothing, commit nothing, push nothing; when done, write outbox/%s/REPORT.md whose first line is Verdict: LAND or Verdict: HOLD\n", p.Card, p.Epoch, p.Attempt, p.Primary, job)
	if bench != "" {
		fmt.Fprintf(&b, "%s\n", bench)
	}
	fmt.Fprintf(&b, "WHO: friend %s\n", name)
	fmt.Fprintf(&b, "Work in ~/%[1]s-working/jobs/%[2]s/: the clone and every file of the read go inside it; the report goes to ~/%[1]s-working/outbox/%[2]s/REPORT.md.\n", name, job)
	b.WriteString("\n# This card is a READ\n\n")
	b.WriteString("Another worker did the card quoted at the end. You judge whether its change does what that card asks, so the sprint can land it or send it back. You read; you do not fix, commit or push anything.\n")
	b.WriteString("\n## The work under review\n\n")
	if repo != "" {
		fmt.Fprintf(&b, "- repository: %s (https://github.com/%s)\n", repo, repo)
	}
	if p.WorkBranch != "" {
		fmt.Fprintf(&b, "- branch: %s\n", p.WorkBranch)
	}
	fmt.Fprintf(&b, "- head: %s (the commit under review)\n", orDash(p.Head))
	if base != "" {
		fmt.Fprintf(&b, "- base: %s (the branch the work lands on)\n", base)
	}
	if start != "" {
		fmt.Fprintf(&b, "- start: %s (the commit this attempt continued from)\n", start)
	}
	if p.Tier != "" {
		fmt.Fprintf(&b, "- tier: %s\n", p.Tier)
	}
	if !deadline.IsZero() {
		fmt.Fprintf(&b, "- deadline: %s (past it the read is dealt to another reader)\n", deadline.UTC().Format(time.RFC3339))
	} else {
		fmt.Fprintf(&b, "- deadline: %s from when you start it (past it the read is dealt to another reader)\n", ReadCardDeadline)
	}
	b.WriteString("\n## How to read\n\n")
	clone := "git clone https://github.com/<the repository>.git repo"
	if repo != "" {
		clone = "git clone https://github.com/" + repo + ".git repo"
	}
	fmt.Fprintf(&b, "1. %s && cd repo && git fetch origin %s && git checkout --detach %s; check that git rev-parse HEAD prints %s. If that head cannot be had, write Verdict: HOLD and say so.\n", clone, orDash(p.WorkBranch), orDash(p.Head), orDash(p.Head))
	diff := "git diff $(git merge-base " + orDash(p.Head) + " origin/" + firstNonEmpty(base, "HEAD") + ")..." + orDash(p.Head)
	if start != "" {
		diff = "git diff " + start + ".." + orDash(p.Head)
	}
	fmt.Fprintf(&b, "2. The change under review is exactly %s. What landed on the base since is not the work's and is never a finding.\n", diff)
	b.WriteString("3. Judge the change against the card below: its HOW THIS CARD IS JUDGED and AS A READ sections when it has them, then its task, STEPS, TEST, PATHS and RULES. Does the change do the task; touch only the PATHS; add or keep green the tests the card names; keep its rules; is the new behaviour reached by a command or a caller?\n")
	b.WriteString("4. Run the tests the card names and the packages the change touches, where your own rules let you run them, and note each command and how it ended.\n")
	b.WriteString("Attribution (a By: line, a Co-Authored-By trailer, the model or harness a worker names) is never a finding.\n")
	b.WriteString("\n## How to finish\n\n")
	fmt.Fprintf(&b, "Write outbox/%s/REPORT.md:\n", job)
	b.WriteString("- line 1: Verdict: LAND (the work does what the card asks: land it) or Verdict: HOLD (it does not)\n")
	fmt.Fprintf(&b, "- when your runner asks for a Head: line, give the head you read, %s\n", orDash(p.Head))
	b.WriteString("- a HOLD names each defect on a line of its own: the file:line (or the card's STEP or RULE) the work breaks, and what to change; a HOLD that names no file, line or rule is not a read\n")
	b.WriteString("- then the tests you ran and how they ended\n")
	b.WriteString("Commit nothing, push nothing, open no pull request: the report is the whole of a read.\n")
	fmt.Fprintf(&b, "\n## The card under review: %s, attempt %d, verbatim\n\n", p.Primary, p.Attempt)
	b.WriteString(strings.TrimRight(p.Brief, "\n") + "\n")
	if p.Report != "" {
		b.WriteString("\n## The worker's report\n\n" + strings.TrimRight(p.Report, "\n") + "\n")
	}
	return b.String()
}

// briefLine is the value of the first line of the brief that begins with key ("REPO:"),
// trimmed; "" when none does.
func briefLine(brief, key string) string {
	for _, l := range strings.Split(brief, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), key); ok {
			if f := strings.Fields(v); len(f) > 0 {
				return f[0]
			}
		}
	}
	return ""
}

// firstNonEmpty is the first of the words that is not empty.
func firstNonEmpty(words ...string) string {
	for _, w := range words {
		if w != "" {
			return w
		}
	}
	return ""
}
