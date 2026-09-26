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

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/pitstop"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/webhook"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
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

// ErrNoToken is the typed refusal when no GitHub token is in the seat or the
// environment (nova-tools#4330).
var ErrNoToken = &Refusal{Why: "no GitHub token", Remedy: "name the seat's GitHub token env in its seats.tsv row (the seventh column) and pass --seat, or export GH_TOKEN (or GITHUB_TOKEN) for the seat that lands"}

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
	// After a merge (card pr-record-follows-github): Record is "created"
	// when the PR record was written from the REST reply (it had none),
	// "merged" when an existing record was marked merged, "none" when there
	// is no record and the reply's head is not a full sha; Card the card the
	// record names ("" none) and CardMove what happened to it (<from>-><to>,
	// "already landed", "refused <why>", or "none"); Pitstop the sprint whose
	// pit stop held the card's stream while it moved ("" none).
	Record   string
	Card     string
	CardMove string
	Pitstop  string
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
		return afterMerge(ctx, rdb, o, rep, pr.Head.Ref, pr.Base.Ref, say)
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

	// Green at the head. The PR record is what the reads scored: a record
	// whose head is not GitHub's head (the REST head, or the head the newest
	// delivery named) is refused, never merged over (card
	// pr-record-follows-github: #4371 merged while its record lagged).
	if err := staleRecord(ctx, rdb, o.Repo, o.N, pr.Head.SHA); err != nil {
		return rep, err
	}

	// The one merge, at exactly that sha.
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
	return afterMerge(ctx, rdb, o, rep, pr.Head.Ref, pr.Base.Ref, say)
}

// staleRecord refuses a PR record whose head is not GitHub's: the REST
// head the pass read, or the head the newest delivery named
// (land.StaleHead). No record is not a refusal: a PR nobody recorded has no
// read to be stale.
func staleRecord(ctx context.Context, rdb redis.Cmdable, repo string, n int, restHead string) error {
	key := PRKey(repo, n)
	rec, err := rdb.HGetAll(ctx, key).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("HGETALL %s: %w", key, err)
	}
	if rec["head"] == "" {
		return nil
	}
	remedy := "the next delivery or runner receipt of the head moves the record; read it at that head, then land pr again (or record it: nova-sprint pr record --repo " +
		repo + " --n " + strconv.Itoa(n) + " --head " + restHead + ")"
	if err := land.StaleHead(key, rec); err != nil {
		return &Refusal{Why: err.Error(), Remedy: remedy}
	}
	if rec["head"] != restHead {
		st := &land.StaleError{Key: key, Head: rec["head"], GitHub: restHead, Src: "rest"}
		return &Refusal{Why: st.Error(), Remedy: remedy}
	}
	return nil
}

// afterMerge is what a merge settles in Redis, in the same verb: the PR
// record follows the REST reply (land.RecordPRHead, source rest: created
// when absent, its head, branch and task from head.sha and head.ref, the
// card found by land.BranchCardID), is marked merged at the merge commit,
// and the card the record names lands (taskcard.Land, the one card move).
// A pit stop does not hold the move, since it records what GitHub already
// did; the receipt says so: PR <n> PITSTOP kept sprint=<S> scope=<scope>.
func afterMerge(ctx context.Context, rdb redis.Cmdable, o LandPROptions, rep LandPRReport, headRef, baseRef string, say func(string, ...any)) (LandPRReport, error) {
	if rdb == nil || rep.MergeSHA == "" {
		return rep, nil
	}
	claim := land.PRClaim{Repo: o.Repo, N: o.N, Head: rep.Head, Action: "closed", Merged: true,
		Branch: headRef, Base: baseRef, Source: land.ClaimREST}
	created := false
	if claim.Validate() == nil {
		res, err := land.RecordPRHead(ctx, rdb, claim, o.Now)
		if err != nil {
			return rep, err
		}
		created = res.Outcome == "created"
		say("RECORD %s", res.Words())
	}
	ok, err := land.RecordPRMerged(ctx, rdb, o.Repo, o.N, rep.Head, rep.MergeSHA, o.Now)
	if err != nil {
		return rep, err
	}
	switch {
	case created:
		rep.Record = "created"
	case ok:
		rep.Record = "merged"
	default:
		rep.Record = "none"
	}
	id, err := prCard(ctx, rdb, o.Repo, o.N)
	if err != nil {
		return rep, err
	}
	rep.Card, rep.CardMove = id, "none"
	if id == "" {
		say("CARD none (%s names no card)", PRKey(o.Repo, o.N))
		return rep, nil
	}
	tv, err := rdb.HMGet(ctx, taskcard.Key(id), "where", "sprint", "stream").Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return rep, fmt.Errorf("HMGET %s where sprint stream: %w", taskcard.Key(id), err)
	}
	field := func(i int) string {
		if i < len(tv) {
			s, _ := tv[i].(string)
			return s
		}
		return ""
	}
	where, sprint, strm := field(0), field(1), field(2)
	if where == "landed" {
		rep.CardMove = "already landed"
		say("CARD %s already landed", id)
		return rep, nil
	}
	if h, held, err := cardPitstop(ctx, rdb, sprint, strm); err != nil {
		return rep, err
	} else if held {
		rep.Pitstop = h.Sprint
		say("PITSTOP kept %s card=%s (the landed move is a fact; the stop holds the rest)", h.Words(), id)
	}
	res, err := taskcard.Land(ctx, rdb, id, "land-pr", rep.MergeSHA, fmt.Sprintf("merged %s#%d", o.Repo, o.N))
	if err != nil {
		if why, refused := taskcard.IsRefused(err); refused {
			rep.CardMove = "refused " + why
			say("CARD %s REFUSED %s", id, why)
			return rep, nil
		}
		return rep, err
	}
	rep.CardMove = res.From + "->" + res.To
	say("CARD %s %s->%s", id, res.From, res.To)
	return rep, nil
}

// cardPitstop is the pit stop that holds the card's stream: its sprint's
// stop (pitstop.Held) when the card names a sprint, else the first open
// sprint's stop that holds the stream (pitstop.HeldOpen).
func cardPitstop(ctx context.Context, rdb redis.Cmdable, sprint, strm string) (pitstop.Hold, bool, error) {
	if sprint != "" {
		h, held, err := pitstop.Held(ctx, rdb, sprint)
		if err != nil {
			return pitstop.Hold{}, false, fmt.Errorf("pitstop %s: %w", sprint, err)
		}
		return h, held && h.Stop.InScope(strm), nil
	}
	hs, err := pitstop.HeldOpen(ctx, rdb)
	if err != nil {
		return pitstop.Hold{}, false, fmt.Errorf("pitstop: %w", err)
	}
	h, held := hs.Stream(strm)
	return h, held, nil
}

// prCard is the card of PR n: the record's task; else a card of the record's
// stream in review, merging or working whose pr and repo are this PR's; else
// the card the record's branch names when it names this PR. "" when none.
func prCard(ctx context.Context, rdb redis.Cmdable, repo string, n int) (string, error) {
	key := PRKey(repo, n)
	v, err := rdb.HMGet(ctx, key, "task", "stream", "branch").Result()
	if err != nil {
		return "", fmt.Errorf("HMGET %s: %w", key, err)
	}
	str := func(i int) string { s, _ := v[i].(string); return s }
	if t := str(0); t != "" {
		return t, nil
	}
	var cands []string
	if st := str(1); st != "" && st != "-" {
		pipe := rdb.Pipeline()
		var sets []*redis.StringSliceCmd
		for _, w := range []string{"review", "merging", "working"} {
			sets = append(sets, pipe.ZRange(ctx, ws.Key(st, w), 0, -1))
		}
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			return "", fmt.Errorf("read stream %s: %w", st, err)
		}
		for _, s := range sets {
			cands = append(cands, s.Val()...)
		}
	}
	if b := land.BranchCardID(str(2)); b != "" {
		cands = append(cands, b)
	}
	if len(cands) == 0 {
		return "", nil
	}
	pipe := rdb.Pipeline()
	got := make([]*redis.SliceCmd, len(cands))
	for i, id := range cands {
		got[i] = pipe.HMGet(ctx, taskcard.Key(id), "repo", "pr")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return "", fmt.Errorf("read cards: %w", err)
	}
	want := strconv.Itoa(n)
	for i, id := range cands {
		f := got[i].Val()
		r, _ := f[0].(string)
		p, _ := f[1].(string)
		if p == want && (r == "" || prkey.Name(r) == prkey.Name(repo)) {
			return id, nil
		}
	}
	return "", nil
}
