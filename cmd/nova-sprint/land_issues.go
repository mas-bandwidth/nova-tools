package main

import (
	"context"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/github"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A landing closes the card's issues (docs/SPEC-SPRINT.md section 7; internal/sprint,
// land_issues.go): land runs the closer on the cards it landed as its pass ends, and the
// server's loop runs it every issuesEvery between ticks, which closes what a landing left
// pending (GitHub refused, or a landing the tick had not pumped yet). GitHub's
// answer never changes a landing.

// issuesEvery is how often the server's loop runs the closer between ticks.
const issuesEvery = 10 * time.Second

// issuesBudget bounds one pass of the closer's GitHub calls: GitHub not answering
// holds the loop no longer, and what it did not close stays pending.
const issuesBudget = 30 * time.Second

// closeIssues runs one pass of the closer on st: cards, when given, alone and at once (a
// landing's), else every pending card whose retry is due (sprint.CloseLandedIssues); each
// card's outcome recorded (sprint.IssuesClosed), then where's line of the pending closes
// (sprint.IssuesShown). It returns what it did, a line a card. GitHub is a.issueGitHub, else
// gh; a twin with no fake asks no GitHub.
func (a *app) closeIssues(ctx context.Context, st *store.Store, twin bool, cards []string) ([]string, error) {
	f := a.issueGitHub
	if f == nil {
		if twin {
			return nil, nil
		}
		f = github.GH{}
	}
	a.serial.Lock()
	s, err := st.Load(ctx, []string{sprint.Work, sprint.Merge}, nil)
	a.serial.Unlock()
	if err != nil {
		return nil, err
	}
	pctx, cancel := context.WithTimeout(ctx, issuesBudget)
	reqs := sprint.CloseLandedIssues(pctx, s, f, a.now(), cards, sprint.MachineActor)
	cancel()
	var said []string
	run := func(step store.Step) error {
		a.serial.Lock()
		defer a.serial.Unlock()
		res, err := st.Run(ctx, step)
		said = append(said, res.Moved...)
		for _, r := range res.Refused {
			said = append(said, r.Key+" not recorded: "+r.Why)
		}
		return err
	}
	for _, q := range reqs {
		if err := run(store.Step{Verb: "issues", Args: store.ArgsOf(q), Load: []string{sprint.Work}, Actor: sprint.MachineActor,
			Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.IssuesClosed(s, q) }}); err != nil {
			return said, err
		}
	}
	return said, run(store.Step{Verb: "issues", Load: []string{sprint.Work}, Actor: sprint.MachineActor, Plan: sprint.IssuesShown})
}

// closeIssuesTick is the server loop's pass of the closer: its lines, and a failure, each
// printed with the time; nothing when nothing was done.
func (a *app) closeIssuesTick(ctx context.Context, st *store.Store, say func(string)) {
	said, err := a.closeIssues(ctx, st, false, nil)
	at := oneline.Field(a.now().Format("15:04:05"))
	for _, x := range said {
		say(at + " ISSUES " + oneline.Escape(x))
	}
	if err != nil {
		say(at + " ISSUES FAILED the closer did not finish: " + oneline.Err(err) + "; what it did not record is tried again next pass")
	}
}

// commitCloses is the issues a card's landed commits close: "Closes #N" and the other
// closing keywords in the messages of the commits its merge brought (before..head), read
// against its repository (github.Issues). Nil when git cannot say: the brief's issues are
// closed still.
func (l *lander) commitCloses(ctx context.Context, dir, before string, c landCard) []string {
	out, err := l.git(ctx, dir, "log", "--format=%B", before+".."+c.head)
	if err != nil {
		return nil
	}
	repo, _ := github.Slug(c.repo)
	var closes []string
	for _, i := range github.Issues(out, repo) {
		closes = append(closes, i.String())
	}
	return closes
}
