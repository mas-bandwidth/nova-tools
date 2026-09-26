// The whole stream landing as one verb (nova-tools#3598): what Rowan did by
// hand (land stream, ci request, pr record, land merge) in one run.
//
//	nova-sprint land --repo <owner/repo> --stream <s> [--stream <s2>...] [--base dev]
//	    [--redis <addr>] [--remote <url>] [--mirror <dir>|none] [--workdir <dir>] [--branch <b>]
//	    [--test <cmd>] [--test-timeout 20m] [--min-score N] [--by rowan] [--api <url>] [--budget N]
//	    [--ci-wait 45m] [--tick 10s] [--ci-url <url>] [--rebuild]
//
// It builds the stream branch off the base tip (the members merged --no-ff
// oldest first), pushes it, opens ONE stream PR, requests our own CI for the stream head (ci:pool; a bench's ci
// run writes the ci word on the stream PR's record), waits for that word,
// and on green merges the stream PR with land merge's step, which moves every
// member merging -> landed and closes them. A base that moved during CI is
// rebuilt on the new tip first. A re-run resumes an open landing at its CI
// wait; a red stream head stays red until --rebuild.
//
// The batch test is that CI request, claimed by a bench (nova-tools#3899):
// land runs no go test on its own seat. --test is a local pre-test (one
// batch test, bisect and park on red) for a seat that is not the
// coordinator's; on the coordinator seat (NOVA_SPRINT_REDIS_USER=coordinator)
// it is refused.
//
// One receipt line: LANDED (exit 0); LAND RED or LAND WAITING (exit 1, the
// remedy named); a build that stopped prints LAND STOPPED with its state (exit 1).
// Exit 2 refused or usage, 6 no Redis.
package main

import (
	"context"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/gh"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ci"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/preflight"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// landRunSleep is the CI wait's tick; a test swaps it for the bench's turn.
var landRunSleep func(ctx context.Context, d time.Duration) error

func runLandWhole(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "land"
	fs := taskFlags(verb)
	var streams multiFlag
	fs.Var(&streams, "stream", "")
	redisAddr := fs.String("redis", "", "")
	repo := fs.String("repo", "", "")
	base := fs.String("base", "dev", "")
	remote := fs.String("remote", "", "")
	mirror := fs.String("mirror", "", "")
	workdir := fs.String("workdir", "", "")
	branch := fs.String("branch", "", "")
	test := fs.String("test", "", "")
	testTimeout := fs.Duration("test-timeout", 20*time.Minute, "")
	minScore := fs.Int("min-score", -1, "")
	by := fs.String("by", "rowan", "")
	api := fs.String("api", gh.DefaultAPI, "")
	budget := fs.Int("budget", 64, "")
	ciWait := fs.Duration("ci-wait", 45*time.Minute, "")
	tick := fs.Duration("tick", 10*time.Second, "")
	ciURL := fs.String("ci-url", "", "")
	rebuild := fs.Bool("rebuild", false, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, verb, "takes flags, not "+strconv.Quote(fs.Arg(0)))
	}
	if *test != "" && os.Getenv(store.UserEnv) == preflight.PreflightSeat {
		return refuse(errOut, verb, "--test runs the batch test on the coordinator seat, which runs none (#3899): drop --test; "+
			"nova-sprint land makes the ci request (nova-sprint ci request) and a bench runs it (nova-sprint ci run)")
	}
	if !landRepoOK(*repo) || len(streams) == 0 {
		return refuse(errOut, verb, "needs --repo <owner/repo> and --stream <s>")
	}
	if *tick <= 0 || *ciWait < 0 {
		return refuse(errOut, verb, "--tick must be positive and --ci-wait not negative")
	}
	addr := landRedisAddr(*redisAddr)
	if addr == "" {
		return refuse(errOut, verb, "needs --redis <addr> or NOVA_REDIS_ADDR")
	}
	slug, err := stream.Slug(streams...)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	gh, err := landGitHub(verb, *api, *budget, nil)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	o := stream.RunOptions{
		Options: stream.Options{Repo: *repo, Streams: streams, Base: *base, Branch: *branch, Remote: *remote,
			Test: *test, TestTimeout: *testTimeout, NoTest: *test == "", MinScore: *minScore, By: *by,
			Author: "Rowan <rowan@mas-bandwidth.com>", Log: out, GH: gh},
		CIWait: *ciWait, Tick: *tick, Rebuild: *rebuild, Sleep: landRunSleep,
		// The runner clones --ci-url, else stages from its mirror (never the
		// forge's ssh url).
		CIURL: *ciURL,
	}
	home, _ := os.UserHomeDir()
	name := (*repo)[strings.Index(*repo, "/")+1:]
	switch *mirror {
	case "none":
	case "":
		if d := filepath.Join(home, "nova-bench", "mirror", name+".git"); isDir(d) {
			o.Mirror = d
		}
	default:
		o.Mirror = *mirror
	}
	o.Workdir = *workdir
	if o.Workdir == "" {
		o.Workdir = filepath.Join(home, "rowan-working", "tmp",
			fmt.Sprintf("land-%s-%d", strings.ReplaceAll(slug, "+", "_"), time.Now().Unix()))
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
		return 6
	}
	defer st.Close()
	gh.Redis = st.Client() // the calls are counted in the store (#4343)
	o.Request = func(ctx context.Context, repo, sha string, pr int, url string) (string, error) {
		r, err := ci.Request(ctx, st, ci.RequestRequest{Repo: bareRepo(repo), SHA: sha, PR: pr, URL: url})
		if err != nil {
			return "", err
		}
		if r.Status == "REFUSED" {
			return "", fmt.Errorf("REFUSED %s", r.Detail)
		}
		return r.Status, nil
	}
	rep, err := stream.Run(ctx, st.Client(), o)
	for _, s := range rep.Build.Skips {
		fmt.Fprintf(out, "SKIP task=%s pr=#%d why=%s\n", s.Task, s.N, oneline.Field(s.Why))
	}
	if err != nil {
		return landExit(errOut, verb, err)
	}
	l := rep.Landing
	switch rep.State {
	case "landed":
		m := rep.Merge
		members := make([]string, 0, len(m.Landing.Members))
		for _, x := range m.Landing.Members {
			members = append(members, fmt.Sprintf("#%d", x.N))
		}
		fmt.Fprintf(out, "LANDED repo=%s stream=%s pr=#%d head=%s base=%s@%s merge=%s members=%s moved=%d builds=%d resumed=%t ci=%s calls=%d rest_calls=%d unclosed=%d\n",
			*repo, slug, m.Landing.PR, stream.Short(m.Landing.Head), m.Landing.Base, stream.Short(m.Landing.BaseSHA),
			stream.Short(m.MergeSHA), orDash(strings.Join(members, ",")), m.Moved, rep.Builds, rep.Resumed, rep.CI,
			rep.Calls, gh.Calls, len(m.Unclosed))
		if len(m.Unclosed) > 0 {
			return 1
		}
		return 0
	case "red":
		fmt.Fprintf(out, "LAND RED repo=%s stream=%s pr=#%d head=%s builds=%d remedy=nova-sprint ci status --repo %s --sha %s; fix the red member, then nova-sprint land --rebuild\n",
			*repo, slug, l.PR, stream.Short(l.Head), rep.Builds, bareRepo(*repo), l.Head)
		return 1
	case "waiting":
		fmt.Fprintf(out, "LAND WAITING repo=%s stream=%s pr=#%d head=%s ci=%s request=%s builds=%d remedy=a bench runs nova-sprint ci run; re-run nova-sprint land to resume at the wait\n",
			*repo, slug, l.PR, stream.Short(l.Head), rep.CI, orDash(rep.CIRequest), rep.Builds)
		return 1
	}
	b := rep.Build
	conflict := "-"
	if c := b.Build.Conflict; c != nil {
		conflict = fmt.Sprintf("#%d:%s", c.Member.N, strings.Join(c.Files, ","))
	}
	fmt.Fprintf(out, "LAND STOPPED repo=%s stream=%s state=%s members=%d parked=%d conflict=%s workdir=%s remedy=nova-sprint land stream --repo %s --stream <s> names the stop\n",
		*repo, slug, rep.State, len(b.Build.Kept), len(b.Build.Parked), conflict, orDash(b.Workdir), *repo)
	return 1
}
