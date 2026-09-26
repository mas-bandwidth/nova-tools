// land pr (nova-tools#4311): one pull request to its merge commit in one
// pass, the scratch script of 2026-09-26 as a verb.
//
//	nova-sprint land pr <n> [--repo owner/name] [--redis <addr>] [--api <url>]
//
// It reads the PR by REST, reads its head's check state from Redis
// (ci:<repo>:<head>:gh, written by the webhook ingest; never the check-runs
// or workflow-runs endpoints), prints PR <n> CHECKS <word> <pass>/<total>,
// and when that is green merges the PR by REST at exactly that head (PR <n>
// MERGED <sha>). Red is FAILED <first red run>; pending or unrecorded is
// WAITING and the verb returns at once: no loop, no sleep, no GraphQL. The
// token comes from the environment (GH_TOKEN, then GITHUB_TOKEN, as the
// lander reads it; internal/nsprint/land/stream); no child process. A
// missing token is the typed refusal REFUSED no GitHub token remedy=...
//
// Exit 0 merged, 1 failed, closed or in conflict, 2 usage or refused,
// 3 waiting (not yet green; run again), 6 no Redis.
package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func runLandPR(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "land pr"
	fs := taskFlags(verb)
	repo := fs.String("repo", "mas-bandwidth/nova-tools", "")
	redisAddr := fs.String("redis", "", "")
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
	const usage = "land pr <n> [--repo owner/name] [--redis <addr>] [--api <url>]"
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
	addr := landRedisAddr(*redisAddr)
	if addr == "" {
		return refuse(errOut, verb, "needs --redis <addr> or NOVA_REDIS_ADDR (the check state is read from Redis): "+usage)
	}
	tok, err := landStreamToken()
	if err != nil || tok == "" {
		return landExit(errOut, verb, stream.ErrNoToken)
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
		return 6
	}
	defer st.Close()
	gh := &stream.GitHub{API: *api, Token: tok, Budget: 3}
	rep, err := stream.LandPR(ctx, gh, st.Client(), stream.LandPROptions{Repo: *repo, N: n, Log: out})
	if err != nil {
		return landExit(errOut, verb, err)
	}
	fmt.Fprintf(out, "LAND PR repo=%s pr=#%d state=%s head=%s ci=%s merge=%s failed=%s rest_calls=%d\n",
		*repo, n, rep.State, orDash(stream.Short(rep.Head)), orDash(rep.CI), orDash(stream.Short(rep.MergeSHA)),
		orDash(strings.Join(rep.Failed, ",")), gh.Calls)
	switch rep.State {
	case "merged":
		return 0
	case "waiting":
		return 3
	}
	return 1
}
