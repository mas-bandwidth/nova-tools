package stream

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

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

type prView struct {
	State          string `json:"state"`
	Merged         bool   `json:"merged"`
	MergeCommitSHA string `json:"merge_commit_sha"`
	MergeableState string `json:"mergeable_state"`
	Title          string `json:"title"`
	Head           struct {
		SHA string `json:"sha"`
	} `json:"head"`
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

	var pr prView
	if _, err := gh.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", o.Repo, o.N), nil, &pr); err != nil {
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
