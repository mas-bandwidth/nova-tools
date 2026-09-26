// The stream landing verbs (nova-tools#3598; #3611-#3613): the flow Rowan
// lands by hand, as verbs with all state in Redis (internal/nsprint/land/stream).
//
//	nova-sprint land stream --repo <owner/repo> --stream <s> [--stream <s2>...] [--base dev] [--dry-run]
//	    [--redis <addr>] [--remote <url>] [--mirror <dir>|none] [--workdir <dir>] [--branch <b>]
//	    [--test <cmd>] [--test-timeout 20m] [--min-score N] [--by rowan] [--api <url>] [--budget N]
//	nova-sprint land status --repo <owner/repo> [--redis <addr>]
//	nova-sprint land merge --repo <owner/repo> --stream <s> [--by rowan] [--api <url>] [--budget N]
//
// land stream: members are ws:<s>:merging intersected with pr:<repo>:<n>
// records read >= 8 at head (cfg:land min_score[:<repo>]), oldest
// pr_ready_at first. --dry-run prints the order from Redis alone. A run
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
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// landStreamToken supplies the GitHub token; a test swaps it.
var landStreamToken = githubToken

func landRedisAddr(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	return os.Getenv("NOVA_REDIS_ADDR")
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

func landGitHub(api string, budget int) (*stream.GitHub, error) {
	tok, err := landStreamToken()
	if err != nil {
		return nil, err
	}
	if tok == "" {
		return nil, errors.New("GitHub token is empty; set GH_TOKEN")
	}
	return &stream.GitHub{API: api, Token: tok, Budget: budget}, nil
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
	redisAddr := fs.String("redis", "", "")
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
	api := fs.String("api", "https://api.github.com", "")
	budget := fs.Int("budget", 4, "")
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
		Test: *test, TestTimeout: *testTimeout, MinScore: *minScore, By: *by, Author: "Rowan <rowan@mas-bandwidth.com>", Log: out}
	slug, err := stream.Slug(streams...)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if !*dry {
		gh, err := landGitHub(*api, *budget)
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
	rep, err := stream.LandStream(ctx, st.Client(), opts)
	if rep.State == "dry-run" && err == nil {
		for i, m := range rep.Members {
			fmt.Fprintf(out, "ORDER %d %s#%d head=%s ready_at=%d read=%s:%d task=%s stream=%s\n",
				i+1, *repo, m.N, stream.Short(m.Head), m.ReadyAt, m.Who, m.Score, m.Task, oneline.Field(m.Stream))
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
	redisAddr := fs.String("redis", "", "")
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
		fmt.Fprintf(out, "STREAM %s streams=%s state=%s branch=%s head=%s members=%s parked=%s pr=%s ci=%s mergeable=%s%s\n",
			r.Slug, oneline.Field(r.Streams), r.State, orDash(r.Branch), orDash(stream.Short(r.Head)), orDash(strings.Join(mem, ",")),
			orDash(strings.Join(parked, ",")), pr, r.CI, r.Mergeable, stale)
	}
	fmt.Fprintf(out, "LAND STATUS repo=%s streams=%d open=%d green=%d\n", *repo, len(rows), open, green)
	return 0
}

func runLandMerge(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "land merge"
	fs := taskFlags(verb)
	var streams multiFlag
	fs.Var(&streams, "stream", "")
	redisAddr := fs.String("redis", "", "")
	repo := fs.String("repo", "", "")
	by := fs.String("by", "rowan", "")
	api := fs.String("api", "https://api.github.com", "")
	budget := fs.Int("budget", 64, "")
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
	// The token is needed only past the Redis gates; refuse on them first.
	gh, tokErr := landGitHub(*api, *budget)
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
	fmt.Fprintf(out, "LAND MERGE repo=%s stream=%s pr=#%d head=%s merge=%s members=%d moved=%d missing=%d already=%t closed=%s unclosed=%s rest_calls=%d close_lines=%d skipped=%d closes_unread=%s issues_closed=%s issues_unclosed=%s release=%s\n",
		*repo, l.Slug, l.PR, stream.Short(l.Head), stream.Short(rep.MergeSHA), len(l.Members), rep.Moved, rep.Missing, rep.Already,
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
