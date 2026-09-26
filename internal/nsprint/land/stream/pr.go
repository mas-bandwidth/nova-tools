package stream

import (
	"context"
	"errors"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/gh"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/webhook"
	"github.com/redis/go-redis/v9"
)

// LAND PR (nova-tools#4311): one pull request to its merge commit in one
// pass, no loop and no wait. The scratch script of 2026-09-26 (wait for the
// PR's checks, enqueue it, watch the merge_group run) ran about twenty times
// that day; this is it as a verb within nova-sprint's two GitHub rules: REST
// only, never GraphQL (7.21, TestNoGraphQLInNovaSprint), and no check state
// read from GitHub (TestNoPollingPathsRemain): the check state is
// ci:<repo>:<head>:gh, which the webhook ingest (internal/nsprint/webhook)
// writes from the check_run and workflow_run deliveries.
//
// The pass: one REST read of the PR (merged, state, mergeable_state, head);
// one HGETALL of the GitHub leg at that head; then, when the leg is green
// and the PR has no conflict, one REST merge at exactly that head (GitHub
// refuses when the head moved), the admin merge at a proved sha that skips
// the merge queue (Glenn 2026-09-26 11:35 AM ET: the queue's run re-proved
// the same tree for minutes). A pending or unrecorded leg is WAITING: the
// verb returns at once and a later run, after the webhook writes green,
// merges. Three GitHub calls at most, and none of them a check-state read.

// ErrNoToken is the typed refusal when no GitHub token is in the environment.
var ErrNoToken = &Refusal{Why: "no GitHub token", Remedy: "export GH_TOKEN (or GITHUB_TOKEN) for the seat that lands"}

// LandPROptions is one land pr pass.
type LandPROptions struct {
	Repo string    // owner/name
	N    int       // the pull request
	Log  io.Writer // the progress lines; nil discards them
	// Wait, when positive, makes a WAITING pass block on ev:github for the
	// head's next check delivery (#4343: events over polling, the verb form
	// of `gh pr checks --watch`) and pass again, up to Wait in all; Tick
	// bounds one block (default 30 s). Now and Await are the clock and the
	// wait; nil are the wall and ev:github.
	Wait time.Duration
	Tick time.Duration
	Now  func() time.Time
	// Await returns what woke it: "head" (a check delivery for head), "pr"
	// (a pull_request delivery for this PR) or "" (the time passed).
	Await func(ctx context.Context, head string, d time.Duration) (string, error)
}

// LandPRWait runs LandPR, and while the pass is waiting and Wait allows,
// waits on ev:github and passes again. The cursor is taken before the
// first pass, so a delivery between the pass and the wait is not missed.
// After a wake only the Redis leg is read: GitHub is called again only
// when the leg is green (the merge) or the wake was a pull_request event
// for this PR (its state moved); a red leg ends the wait with no call, and
// a timeout or an unrelated delivery costs nothing.
func LandPRWait(ctx context.Context, gh *GitHub, rdb redis.Cmdable, o LandPROptions) (LandPRReport, error) {
	await := o.Await
	if await == nil && o.Wait > 0 && rdb != nil {
		var err error
		if await, err = awaitPR(ctx, rdb, o.Repo, o.N); err != nil {
			return LandPRReport{}, err
		}
	}
	rep, err := LandPR(ctx, gh, rdb, o)
	if err != nil || rep.State != "waiting" || o.Wait <= 0 {
		return rep, err
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	tick := o.Tick
	if tick <= 0 {
		tick = 30 * time.Second
	}
	log := o.Log
	if log == nil {
		log = io.Discard
	}
	deadline := now().Add(o.Wait)
	for rep.State == "waiting" {
		left := deadline.Sub(now())
		if left <= 0 {
			return rep, nil
		}
		d := tick
		if left < d {
			d = left
		}
		wake, err := await(ctx, rep.Head, d)
		if err != nil {
			return rep, err
		}
		key := webhook.Key(o.Repo, rep.Head)
		m, err := rdb.HGetAll(ctx, key).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return rep, fmt.Errorf("HGETALL %s: %w", key, err)
		}
		ci := webhook.Parse(m)
		rep.CI = ci.Word
		switch {
		case ci.Word == webhook.Red:
			fail := ci.Fail
			if fail == "" {
				fail = "gh red at " + short(rep.Head)
			}
			rep.State, rep.Failed = "failed", []string{fail}
			fmt.Fprintf(log, "PR %d FAILED %s\n", o.N, fail)
		case ci.Word == webhook.Green || wake == "pr":
			if rep, err = LandPR(ctx, gh, rdb, o); err != nil {
				return rep, err
			}
		default:
			fmt.Fprintf(log, "PR %d WAITING %s has gh=%s after %s\n", o.N, key, orDash(ci.Word), orDash(wake))
		}
	}
	return rep, nil
}

// awaitPR is the production Await of land pr: one XREAD BLOCK on
// ev:github from the tip taken now, waking on a check delivery for the
// head ("head") or a pull_request delivery for the PR ("pr"); "" is a
// timeout.
func awaitPR(ctx context.Context, rdb redis.Cmdable, repo string, n int) (func(ctx context.Context, head string, d time.Duration) (string, error), error) {
	tip, err := gh.Tip(ctx, rdb)
	if err != nil {
		return nil, err
	}
	pr := gh.PREvent(repo, strconv.Itoa(n))
	return func(ctx context.Context, head string, d time.Duration) (string, error) {
		wake := ""
		headEv := gh.HeadEvent(head)
		tip, _, err = gh.Await(ctx, rdb, tip, d, func(f map[string]any) bool {
			switch {
			case pr(f):
				wake = "pr"
			case headEv(f):
				wake = "head"
			default:
				return false
			}
			return true
		})
		return wake, err
	}, nil
}

// LandPRReport is how the pass ended: State merged, waiting, failed, closed
// or conflict; Head the PR head the pass judged; CI the GitHub leg's word at
// that head ("" when nothing is recorded); MergeSHA when merged; Failed the
// first red run (kind:name) when the leg is red.
type LandPRReport struct {
	State    string
	Head     string
	CI       string
	MergeSHA string
	Failed   []string
}

// LandPR runs the pass. The error is a *Refusal for a refused input, else
// the API or store error that stopped it; a pass that ended failed, closed,
// in conflict or waiting returns its report with a nil error.
func LandPR(ctx context.Context, gh *GitHub, rdb redis.Cmdable, o LandPROptions) (LandPRReport, error) {
	var rep LandPRReport
	if gh == nil || gh.Token == "" {
		return rep, ErrNoToken
	}
	if o.N < 1 || !strings.Contains(o.Repo, "/") {
		return rep, &Refusal{Why: "land pr wants <n> and --repo owner/name", Remedy: "nova-sprint land pr <n> --repo owner/name"}
	}
	if rdb == nil {
		return rep, &Refusal{Why: "land pr reads the check state from Redis and has no store", Remedy: "--redis <addr> or NOVA_REDIS_ADDR"}
	}
	if o.Log == nil {
		o.Log = io.Discard
	}
	say := func(format string, a ...any) { fmt.Fprintf(o.Log, "PR %d "+format+"\n", append([]any{o.N}, a...)...) }

	pr, err := gh.ViewPR(ctx, o.Repo, o.N)
	if err != nil {
		return rep, err
	}
	rep.Head = pr.Head.SHA
	switch {
	case pr.Merged:
		rep.State, rep.MergeSHA = "merged", pr.MergeCommitSHA
		say("MERGED %s", pr.MergeCommitSHA)
		return rep, nil
	case pr.State == "closed":
		rep.State = "closed"
		say("FAILED closed without a merge")
		return rep, nil
	case pr.MergeableState == "dirty":
		rep.State = "conflict"
		say("FAILED conflict with the base (mergeable_state=dirty)")
		return rep, nil
	case pr.Head.SHA == "":
		return rep, fmt.Errorf("GET pull %s#%d: no head sha in the reply", o.Repo, o.N)
	}

	// The check state at the head, from the webhook's record, never GitHub.
	key := webhook.Key(o.Repo, pr.Head.SHA)
	m, err := rdb.HGetAll(ctx, key).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return rep, fmt.Errorf("HGETALL %s: %w", key, err)
	}
	ci := webhook.Parse(m)
	rep.CI = ci.Word
	pass, total := 0, 0
	for _, r := range ci.Runs {
		total++
		if r.Word == webhook.Green {
			pass++
		}
	}
	say("CHECKS %s %d/%d head=%s", orDash(ci.Word), pass, total, short(pr.Head.SHA))
	switch {
	case ci.Word == webhook.Red:
		fail := ci.Fail
		if fail == "" {
			fail = "gh red at " + short(pr.Head.SHA)
		}
		rep.State, rep.Failed = "failed", []string{fail}
		say("FAILED %s", fail)
		return rep, nil
	case ci.Word != webhook.Green:
		rep.State = "waiting"
		say("WAITING %s has gh=%s; run again once the webhook writes green", key, orDash(ci.Word))
		return rep, nil
	}

	// Green at the head: the one merge, at exactly that sha.
	title := fmt.Sprintf("Merge pull request #%d", o.N)
	if pr.Title != "" {
		title = fmt.Sprintf("%s (#%d)", pr.Title, o.N)
	}
	sha, err := gh.MergePR(ctx, o.Repo, o.N, pr.Head.SHA, title)
	if err != nil {
		return rep, err
	}
	rep.State, rep.MergeSHA = "merged", sha
	say("MERGED %s", sha)
	return rep, nil
}
