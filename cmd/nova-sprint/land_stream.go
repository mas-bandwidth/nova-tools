// The stream landing verbs (nova-tools#3598; #3611-#3613): the flow Rowan
// lands by hand, as verbs with all state in Redis (internal/nsprint/land/stream).
//
//	nova-sprint land stream --repo <owner/repo> --stream <s> [--stream <s2>...] [--base dev] [--dry-run] [--partial] [--card <id>]
//	    [--redis <addr>] [--remote <url>] [--mirror <dir>|none] [--workdir <dir>] [--branch <b>]
//	    [--test <cmd>] [--test-timeout 20m] [--min-score N] [--by rowan] [--api <url>] [--budget N]
//	nova-sprint land status --repo <owner/repo> [--redis <addr>]
//	nova-sprint land merge --repo <owner/repo> --stream <s> [--by rowan] [--api <url>] [--budget N] [--card <id>]
//
// One writer per stream (#4324): a run that builds, pushes or merges first
// claims land:merge:<stream> owner (stream.Claim): --card <id> for a live
// merge or escalation card's child (the claim stays the card's), else a
// hand claim renewed while the run lasts. Another writer's live claim (the
// land duty's pass, a card, another run, a cross-stream wait) refuses the
// run: REFUSED LAND-OWNER stream=<s> owner=<o> (exit 2).
//
// land stream: members are ws:<s>:merging intersected with pr:<repo>:<n>
// records read >= 8 at head (cfg:land min_score[:<repo>]), in the work
// order the ws:<s>:merging score gives (#4342). --dry-run prints the plan
// (PLAN, ORDER lines) from Redis alone: the coordinator's first move. A run
// refuses LAND-SERIAL when a merging member would be left out (unread,
// held, no PR): never one at a time (#4324); --partial lands without them
// with the same line printed as allowed. A run
// clones the base shallow (--reference-if-able the bench mirror), merges each
// member --no-ff onto stream/<slug>, runs the batch test once
// (cfg:land:test:<repo>, else make check, else go test ./...), bisects a red
// batch one member at a time and parks the first red one (merging -> working
// with a PARKED receipt), pushes, opens ONE PR by REST and records
// land:<repo>:<slug>. A conflict stops the run and names the member and files
// (the workdir is kept for Rowan). Several --stream flags land as one
// land/<slug> branch (a strict up-to-date base: rowan-tools main).
//
// land status --repo: every land:<repo>:<slug> with its stream PR's ci and
// mergeable from the pr record (written by pr record --ci/--mergeable). No
// GitHub.
//
// land merge: refuses unless the stream PR record says ci=green and
// mergeable at the stream head; PUT merge by REST; one Lua call moves every
// member merging -> landed with its CLOSE receipt and logs the landing in
// ws:log; then each member gets the CLOSE comment and state=closed (two REST
// calls each, under --budget; a re-run closes the rest).
//
// Exit 0 done, 1 stopped (conflict, red base, nothing kept, a REST error),
// 2 refused or usage, 6 no Redis.
package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/gh"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// landStreamToken supplies the GitHub token; a test swaps it.
var landStreamToken = githubToken

func landRedisAddr(flagVal string) string {
	return redisOr(flagVal, "NOVA_REDIS_ADDR")
}

func landRepoOK(repo string) bool {
	owner, name, ok := strings.Cut(repo, "/")
	return ok && owner != "" && name != "" && !strings.ContainsAny(repo, " :\t\n") && !strings.Contains(name, "/")
}

// hasRepoFlag is true when land status is the stream form (--repo).
func hasRepoFlag(args []string) bool {
	for _, a := range args {
		if a == "--repo" || a == "-repo" || strings.HasPrefix(a, "--repo=") || strings.HasPrefix(a, "-repo=") {
			return true
		}
	}
	return false
}

// landGitHub is the one GitHub client for a landing verb (#4343): counted
// under verb in the store, paced, retrying a secondary limit; its retry and
// refusal lines go to stderr.
func landGitHub(verb, api string, budget int, rdb redis.Cmdable) (*stream.GitHub, error) {
	tok, err := landStreamToken()
	if err != nil {
		return nil, err
	}
	if tok == "" {
		return nil, errors.New("GitHub token is empty; set GH_TOKEN")
	}
	return &stream.GitHub{API: api, Token: tok, Budget: budget, Verb: verb, Redis: rdb, Log: os.Stderr}, nil
}

func landExit(errOut io.Writer, verb string, err error) int {
	if storeDown(errOut, verb, err) {
		return 6
	}
	var ref *stream.Refusal
	if errors.As(err, &ref) {
		return refuse77(errOut, verb, ref.Why, ref.Remedy)
	}
	var lref *stream.RefusedError
	if errors.As(err, &lref) {
		return refuse(errOut, verb, err.Error())
	}
	fmt.Fprintf(errOut, "nova-sprint %s: %s\n", verb, oneline.Escape(err.Error()))
	return 1
}

func runLandStream(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "land stream"
	fs := taskFlags(verb)
	var streams multiFlag
	fs.Var(&streams, "stream", "")
	redisAddr := fs.String("redis", redisDefault(), "")
	repo := fs.String("repo", "", "")
	base := fs.String("base", "dev", "")
	dry := fs.Bool("dry-run", false, "")
	remote := fs.String("remote", "", "")
	mirror := fs.String("mirror", "", "")
	workdir := fs.String("workdir", "", "")
	branch := fs.String("branch", "", "")
	test := fs.String("test", "", "")
	testTimeout := fs.Duration("test-timeout", 20*time.Minute, "")
	minScore := fs.Int("min-score", -1, "")
	by := fs.String("by", "rowan", "")
	api := fs.String("api", gh.DefaultAPI, "")
	budget := fs.Int("budget", 4, "")
	partial := fs.Bool("partial", false, "")
	card := fs.String("card", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, verb, "takes flags, not "+strconv.Quote(fs.Arg(0)))
	}
	if !landRepoOK(*repo) || len(streams) == 0 {
		return refuse(errOut, verb, "needs --repo <owner/repo> and --stream <s>")
	}
	addr := landRedisAddr(*redisAddr)
	if addr == "" {
		return refuse(errOut, verb, "needs --redis <addr> or NOVA_REDIS_ADDR")
	}
	opts := stream.Options{Repo: *repo, Streams: streams, Base: *base, Branch: *branch, DryRun: *dry, Remote: *remote,
		Test: *test, TestTimeout: *testTimeout, MinScore: *minScore, By: *by, Author: "Rowan <rowan@mas-bandwidth.com>", Log: out,
		Partial: *partial}
	slug, err := stream.Slug(streams...)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if !*dry {
		gh, err := landGitHub(verb, *api, *budget, nil)
		if err != nil {
			return refuse(errOut, verb, err.Error())
		}
		opts.GH = gh
		home, _ := os.UserHomeDir()
		name := (*repo)[strings.Index(*repo, "/")+1:]
		switch *mirror {
		case "none":
		case "":
			if d := filepath.Join(home, "nova-bench", "mirror", name+".git"); isDir(d) {
				opts.Mirror = d
			}
		default:
			opts.Mirror = *mirror
		}
		opts.Workdir = *workdir
		if opts.Workdir == "" {
			opts.Workdir = filepath.Join(home, "rowan-working", "tmp",
				fmt.Sprintf("land-%s-%d", strings.ReplaceAll(slug, "+", "_"), time.Now().Unix()))
		}
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
		return 6
	}
	defer st.Close()
	if opts.GH != nil {
		opts.GH.Redis = st.Client() // the calls are counted in the store (#4343)
	}
	if !*dry {
		release, err := landHold(ctx, st.Client(), streams, *card, *by)
		if err != nil {
			return landExit(errOut, verb, err)
		}
		defer release()
	}
	rep, err := stream.LandStream(ctx, st.Client(), opts)
	if rep.State == "dry-run" && err == nil {
		// The plan (nova-tools #4324): the coordinator's first move whenever
		// a stream has members in merging. The base, the ONE PR, the members
		// in work order (order= is the ws:<stream>:merging score, #4342), and
		// the LAND-SERIAL line a run would refuse with.
		fmt.Fprintf(out, "PLAN repo=%s stream=%s base=%s branch=%s pr=one members=%d left_out=%d order=ws-score\n",
			*repo, slug, *base, rep.Branch, len(rep.Members), len(rep.LeftOut))
		for i, m := range rep.Members {
			fmt.Fprintf(out, "ORDER %d %s#%d head=%s order=%d read=%s:%d task=%s stream=%s\n",
				i+1, *repo, m.N, stream.Short(m.Head), m.ReadyAt, m.Who, m.Score, m.Task, oneline.Field(m.Stream))
		}
		if rep.Serial != "" {
			fmt.Fprintf(out, "%s (a run refuses with this line; --partial lands without them)\n", rep.Serial)
		}
	}
	for _, s := range rep.Skips {
		fmt.Fprintf(out, "SKIP task=%s pr=#%d why=%s\n", s.Task, s.N, oneline.Field(s.Why))
	}
	if err != nil {
		return landExit(errOut, verb, err)
	}
	b := rep.Build
	var members, parked []string
	for _, m := range b.Kept {
		members = append(members, fmt.Sprintf("#%d", m.N))
	}
	for _, p := range b.Parked {
		parked = append(parked, fmt.Sprintf("#%d:%s", p.N, p.Why))
	}
	switch rep.State {
	case "dry-run":
		fmt.Fprintf(out, "LAND STREAM DRY repo=%s stream=%s branch=%s members=%d skipped=%d min_score=%d test=%s\n",
			*repo, slug, rep.Branch, len(rep.Members), len(rep.Skips), rep.MinScore, oneline.Field(orDash(rep.TestCmd)))
		return 0
	case "empty":
		fmt.Fprintf(out, "LAND STREAM EMPTY repo=%s stream=%s members=0 parked=%s skipped=%d\n", *repo, slug, orDash(strings.Join(parked, ",")), len(rep.Skips))
		return 1
	case "conflict":
		c := b.Conflict
		fmt.Fprintf(out, "LAND STREAM CONFLICT repo=%s stream=%s member=#%d files=%s merged_before=%d workdir=%s remedy=resolve in the workdir, then push and open by hand, or rebase the member\n",
			*repo, slug, c.Member.N, strings.Join(c.Files, ","), memberIndex(rep.Members, c.Member.N), rep.Workdir)
		return 1
	case "base-red":
		fmt.Fprintf(out, "LAND STREAM BASE-RED repo=%s stream=%s base=%s@%s red=%s workdir=%s remedy=fix the base red as a commit on the stream branch\n",
			*repo, slug, *base, stream.Short(b.BaseSHA), oneline.Field(b.RedLine), rep.Workdir)
		return 1
	}
	fmt.Fprintf(out, "LAND STREAM repo=%s stream=%s branch=%s head=%s base=%s@%s members=%s parked=%s moved=%d tests=%d pr=#%d reused=%t state=%s\n",
		*repo, slug, rep.Branch, stream.Short(b.Head), *base, stream.Short(b.BaseSHA), orDash(strings.Join(members, ",")),
		orDash(strings.Join(parked, ",")), rep.ParkedMoved, b.Tests, rep.PR, rep.Reused, rep.State)
	return 0
}

func memberIndex(ms []stream.Member, n int) int {
	for i, m := range ms {
		if m.N == n {
			return i
		}
	}
	return len(ms)
}

func runLandStreamStatus(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "land status"
	fs := taskFlags(verb)
	redisAddr := fs.String("redis", redisDefault(), "")
	repo := fs.String("repo", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 || !landRepoOK(*repo) {
		return refuse(errOut, verb, "the stream form is land status --repo <owner/repo> [--redis <addr>]")
	}
	addr := landRedisAddr(*redisAddr)
	if addr == "" {
		return refuse(errOut, verb, "needs --redis <addr> or NOVA_REDIS_ADDR")
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
		return 6
	}
	defer st.Close()
	rows, err := stream.LoadStatus(ctx, st.Client(), *repo)
	if err != nil {
		return landExit(errOut, verb, err)
	}
	open, green := 0, 0
	for _, r := range rows {
		var mem, parked []string
		for _, m := range r.Members {
			mem = append(mem, fmt.Sprintf("#%d@%s", m.N, stream.Short(m.Head)))
		}
		for _, p := range r.Parked {
			parked = append(parked, fmt.Sprintf("#%d:%s", p.N, p.Why))
		}
		pr := "-"
		if r.PR > 0 {
			pr = fmt.Sprintf("#%d", r.PR)
		}
		if r.State == "open" {
			open++
			if r.CI == "green" {
				green++
			}
		}
		stale := ""
		if r.PR > 0 && r.CI != "-" && !r.HeadMatch {
			stale = " record_head=stale"
		}
		serial := ""
		if r.Serial != "" {
			// The durable receipt of a landing that carries fewer members
			// than the stream had in merging (#4324).
			serial = fmt.Sprintf(" serial=%s partial_by=%s partial_at=%s", oneline.Field(r.Serial), orDash(r.PartialBy), orDash(r.PartialAt))
		}
		fmt.Fprintf(out, "STREAM %s streams=%s state=%s branch=%s head=%s members=%s parked=%s pr=%s ci=%s mergeable=%s%s%s\n",
			r.Slug, oneline.Field(r.Streams), r.State, orDash(r.Branch), orDash(stream.Short(r.Head)), orDash(strings.Join(mem, ",")),
			orDash(strings.Join(parked, ",")), pr, r.CI, r.Mergeable, stale, serial)
	}
	fmt.Fprintf(out, "LAND STATUS repo=%s streams=%d open=%d green=%d\n", *repo, len(rows), open, green)
	return 0
}

func runLandMerge(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "land merge"
	fs := taskFlags(verb)
	var streams multiFlag
	fs.Var(&streams, "stream", "")
	redisAddr := fs.String("redis", redisDefault(), "")
	repo := fs.String("repo", "", "")
	by := fs.String("by", "rowan", "")
	api := fs.String("api", gh.DefaultAPI, "")
	budget := fs.Int("budget", 64, "")
	card := fs.String("card", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 || !landRepoOK(*repo) || len(streams) == 0 {
		return refuse(errOut, verb, "needs --repo <owner/repo> and --stream <s>")
	}
	addr := landRedisAddr(*redisAddr)
	if addr == "" {
		return refuse(errOut, verb, "needs --redis <addr> or NOVA_REDIS_ADDR")
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
		return 6
	}
	defer st.Close()
	release, err := landHold(ctx, st.Client(), streams, *card, *by)
	if err != nil {
		return landExit(errOut, verb, err)
	}
	defer release()
	// The token is needed only past the Redis gates; refuse on them first.
	gh, tokErr := landGitHub(verb, *api, *budget, st.Client())
	o := stream.MergeOptions{Repo: *repo, Streams: streams, By: *by}
	if tokErr == nil {
		o.GH = gh
	}
	rep, err := stream.Merge(ctx, st.Client(), o)
	if err != nil {
		var ref *stream.Refusal
		if tokErr != nil && errors.As(err, &ref) && ref.Why == "no GitHub client" {
			return refuse(errOut, verb, tokErr.Error())
		}
		return landExit(errOut, verb, err)
	}
	l := rep.Landing
	closed := make([]string, 0, len(rep.Closed))
	for _, n := range rep.Closed {
		closed = append(closed, fmt.Sprintf("#%d", n))
	}
	unclosed := make([]string, 0, len(rep.Unclosed))
	for _, n := range rep.Unclosed {
		unclosed = append(unclosed, fmt.Sprintf("#%d", n))
	}
	calls := 0
	if gh != nil {
		calls = gh.Calls
	}
	unread := make([]string, 0, len(rep.Unread))
	for _, n := range rep.Unread {
		unread = append(unread, fmt.Sprintf("#%d", n))
	}
	issues := func(ns []int) string {
		out := make([]string, 0, len(ns))
		for _, n := range ns {
			out = append(out, fmt.Sprintf("#%d", n))
		}
		return orDash(strings.Join(out, ","))
	}
	fmt.Fprintf(out, "LAND MERGE repo=%s stream=%s pr=#%d head=%s merge=%s notes_dropped=%d members=%d moved=%d missing=%d already=%t closed=%s unclosed=%s rest_calls=%d close_lines=%d skipped=%d closes_unread=%s issues_closed=%s issues_unclosed=%s release=%s\n",
		*repo, l.Slug, l.PR, stream.Short(l.Head), stream.Short(rep.MergeSHA), rep.NotesDropped, len(l.Members), rep.Moved, rep.Missing, rep.Already,
		orDash(strings.Join(closed, ",")), orDash(strings.Join(unclosed, ",")), calls, rep.Lines, len(rep.Skipped), orDash(strings.Join(unread, ",")),
		issues(rep.IssuesClosed), issues(rep.IssuesUnclosed), orDash(rep.Release))
	for _, s := range rep.Skipped {
		fmt.Fprintf(errOut, "LAND MERGE SKIPPED %s\n", oneline.Field(s))
	}
	if rep.GateErr != "" {
		fmt.Fprintf(errOut, "JEV REFUSED land-gate pr=#%d why=%s remedy=%s\n", l.PR, oneline.Field(rep.GateErr),
			oneline.Field("the land stands; nova-sprint jev outcome --type gate joins a head by hand"))
	}
	if len(rep.Unclosed) > 0 || len(rep.IssuesUnclosed) > 0 {
		return 1
	}
	return 0
}

// landHold claims the streams for this run (nova-tools #4324: one writer per
// stream). With --card the card must be live and be each stream's merge or
// escalation card (land:merge:<stream> task or escalation), and its claim
// stays the card's (released when the card's episode ends); without it a hand claim is
// renewed while the run lasts and released at its end. Another writer's
// live claim is a refusal naming it.
func landHold(ctx context.Context, c stream.Client, streams []string, card, by string) (release func(), err error) {
	owned := func(err error) error {
		var o *stream.OwnedError
		if errors.As(err, &o) {
			return &stream.Refusal{Why: o.Error(), Remedy: "the stream is " + o.Owner + "'s while it is live: its card's child runs with --card <id>; wait for the duty's pass or the other run"}
		}
		return err
	}
	if card != "" {
		state, err := c.HGet(ctx, "task:"+card, "state").Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, err
		}
		if state == "" || state == "closed" {
			return nil, &stream.Refusal{Why: fmt.Sprintf("--card %s is not a live card (state=%s)", card, orDash(state)),
				Remedy: "the stream's card is land:merge:<stream> task; without --card the run claims by hand"}
		}
		if err := landCardOf(ctx, c, streams, card); err != nil {
			return nil, err
		}
		now := time.Now()
		if err := stream.Claim(ctx, c, streams, stream.CardOwner(card), now, now.Add(stream.DefaultHandTTL)); err != nil {
			return nil, owned(err)
		}
		return func() {}, nil
	}
	rel, err := stream.Hold(ctx, c, streams, stream.HandOwner(by), 0, nil)
	if err != nil {
		return nil, owned(err)
	}
	return rel, nil
}

// landCardOf refuses a --card that is not every stream's own card: the
// land:merge:<stream> task (the merge card) or escalation (nova-tools
// #4324). Any other open task would hold the stream until it closes.
func landCardOf(ctx context.Context, c stream.Client, streams []string, card string) error {
	pipe := c.Pipeline()
	cmds := make([]*redis.SliceCmd, len(streams))
	for i, s := range streams {
		cmds[i] = pipe.HMGet(ctx, stream.OwnerKey(s), "task", "escalation")
	}
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	for i, s := range streams {
		v := cmds[i].Val()
		task, _ := v[0].(string)
		esc, _ := v[1].(string)
		if card != task && card != esc {
			return &stream.Refusal{Why: fmt.Sprintf("--card %s is not stream %s's merge or escalation card (task=%s escalation=%s)",
				card, oneline.Field(s), orDash(task), orDash(esc)),
				Remedy: "run as the stream's land:merge:<stream> task or escalation card; without --card the run claims by hand"}
		}
	}
	return nil
}
