package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/github"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
)

// A landing closes the card's issues (docs/SPEC-SPRINT.md section 7; internal/sprint,
// land_issues.go): land runs the closer on the cards it landed as its pass ends, and the
// server's loop runs it every issuesEvery between ticks, which closes what a landing left
// pending (GitHub refused, or a landing the tick had not pumped yet). One pass runs at a
// time, under the closer's lease, so the two never comment on one issue twice. GitHub's
// answer never changes a landing.

// issuesEvery is how often the server's loop runs the closer between ticks.
const issuesEvery = 10 * time.Second

// issuesBudget bounds one pass of the closer's GitHub calls: GitHub not answering
// holds the loop no longer, and what it did not close stays pending.
const issuesBudget = 30 * time.Second

// closeIssues runs one pass of the closer on st: it takes the closer's lease first
// (sprint.IssuesLeaseTake), and does nothing while another pass, this process's or
// another's, holds it; then cards, when given, alone and at once (a landing's), else every
// pending card whose retry is due (sprint.CloseLandedIssues), on the work table as the
// lease's step read it, its queue applied, so a pass sees what the last one recorded; each
// card's outcome recorded (sprint.IssuesClosed), then where's line of the pending closes
// (sprint.IssuesShown) with the lease given back. It returns what it did, a line a card.
// GitHub is a.issueGitHub, else gh; a twin with no fake asks no GitHub.
func (a *app) closeIssues(ctx context.Context, st *store.Store, twin bool, cards []string) ([]string, error) {
	f := a.issueGitHub
	if f == nil {
		if twin {
			return nil, nil
		}
		f = github.GH{}
	}
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
	token := issuesToken()
	var view *sprint.Snapshot
	won := false
	a.serial.Lock()
	res, err := st.Run(ctx, store.Step{Verb: "issues", Load: []string{sprint.Work, sprint.Merge}, Actor: sprint.MachineActor,
		Plan: func(s *sprint.Snapshot) sprint.Plan {
			p, ok := sprint.IssuesLeaseTake(s, token, a.now())
			view, won = s, ok
			return p
		}})
	a.serial.Unlock()
	if err != nil {
		return nil, err
	}
	if !won || res.Lost || len(res.Refused) > 0 {
		return nil, nil // nothing pending, or another pass holds the lease and closes it
	}
	pctx, cancel := context.WithTimeout(ctx, issuesBudget)
	reqs := sprint.CloseLandedIssues(pctx, view, f, a.now(), cards, sprint.MachineActor)
	cancel()
	var failed error
	for _, q := range reqs {
		if err := run(store.Step{Verb: "issues", Args: store.ArgsOf(q), Load: []string{sprint.Work}, Actor: sprint.MachineActor,
			Plan: func(s *sprint.Snapshot) sprint.Plan { return sprint.IssuesClosed(s, q) }}); err != nil {
			failed = err
			break
		}
	}
	// the lease is given back whatever was recorded: what was not is closed on GitHub, and
	// the next pass finds it so and leaves it alone with no second comment
	err = run(store.Step{Verb: "issues", Load: []string{sprint.Work}, Actor: sprint.MachineActor, Plan: func(s *sprint.Snapshot) sprint.Plan {
		p := sprint.IssuesShown(s)
		p.Props = append(p.Props, sprint.IssuesLeaseGive(s, token).Props...)
		return p
	}})
	if failed != nil {
		return said, failed
	}
	return said, err
}

// issuesToken names one pass of the closer in its lease: 12 hex digits.
func issuesToken() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b) // ignored: crypto/rand.Read never fails (Go 1.24+)
	return hex.EncodeToString(b)
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
