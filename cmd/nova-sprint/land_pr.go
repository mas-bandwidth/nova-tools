// land pr (nova-tools#4311): one pull request through GitHub's merge queue
// to its merge commit, the scratch script of 2026-09-26 as a verb.
//
//	nova-sprint land pr <n> [--repo owner/name] [--timeout 20m] [--tick 10s] [--api <url>]
//
// It waits for the PR's checks at its head (PR <n> CHECKS <pass>/<total> as
// the count changes), enqueues it (ENQUEUED; the enqueuePullRequest
// mutation, the tools' one admission to a merge queue), prints each of its
// merge_group run's QUEUE RUN <id> <status> lines as they change, and ends
// with MERGED <sha> or FAILED <check or job names>. Everything goes through
// the REST and GraphQL API with the token from the environment (GH_TOKEN,
// then GITHUB_TOKEN, as the lander reads it; internal/nsprint/land/stream);
// no child process. A missing token is the typed refusal REFUSED no GitHub
// token remedy=... (exit 2).
//
// Exit 0 merged, 1 failed, timed out or closed, 2 usage or refused.
package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
)

// landPRSleep is the wait's tick; a test swaps it for a fake clock.
var landPRSleep func(ctx context.Context, d time.Duration) error

// landPRNow is the wait's clock; a test swaps it with landPRSleep.
var landPRNow = time.Now

func runLandPR(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "land pr"
	fs := taskFlags(verb)
	repo := fs.String("repo", "mas-bandwidth/nova-tools", "")
	timeout := fs.Duration("timeout", 20*time.Minute, "")
	tick := fs.Duration("tick", 10*time.Second, "")
	api := fs.String("api", "https://api.github.com", "")
	// The one positional <n> may come before or after the flags.
	var pos []string
	for len(args) > 0 {
		if !strings.HasPrefix(args[0], "-") {
			pos = append(pos, args[0])
			args = args[1:]
			continue
		}
		if err := fs.Parse(args); err != nil {
			return refuse(errOut, verb, err.Error())
		}
		args = fs.Args()
	}
	const usage = "land pr <n> [--repo owner/name] [--timeout 20m] [--tick 10s] [--api <url>]"
	if len(pos) != 1 {
		return refuse(errOut, verb, "wants one pull request number: "+usage)
	}
	n, err := strconv.Atoi(strings.TrimPrefix(pos[0], "#"))
	if err != nil || n < 1 {
		return refuse(errOut, verb, strconv.Quote(pos[0])+" is not a pull request number: "+usage)
	}
	if !landRepoOK(*repo) {
		return refuse(errOut, verb, "--repo wants owner/name: "+usage)
	}
	if *timeout <= 0 || *tick <= 0 {
		return refuse(errOut, verb, "--timeout and --tick must be positive: "+usage)
	}
	tok, err := landStreamToken()
	if err != nil || tok == "" {
		return landExit(errOut, verb, stream.ErrNoToken)
	}
	gh := &stream.GitHub{API: *api, Token: tok}
	rep, err := stream.LandPR(ctx, gh, stream.LandPROptions{Repo: *repo, N: n, Timeout: *timeout, Tick: *tick,
		Now: landPRNow, Sleep: landPRSleep, Log: out})
	if err != nil {
		return landExit(errOut, verb, err)
	}
	fmt.Fprintf(out, "LAND PR repo=%s pr=#%d state=%s merge=%s enqueued=%t failed=%s rest_calls=%d\n",
		*repo, n, rep.State, orDash(stream.Short(rep.MergeSHA)), rep.Enqueued, orDash(strings.Join(rep.Failed, ",")), gh.Calls)
	if rep.State != "merged" {
		return 1
	}
	return 0
}
