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
// After MERGED (card pr-record-follows-github) the PR record follows the
// REST reply (written from head.sha and head.ref when absent: PR <n> RECORD
// ...), is marked merged, and the card it names (or head.ref spells) lands
// at the merge commit (PR <n> CARD <id> <from>->landed, and record=, card=,
// card_move= on the LAND PR line). A pit stop does not hold that move, a
// fact of GitHub's: PR <n> PITSTOP kept sprint=<S> says it proceeded. A
// record whose head is not GitHub's is REFUSED STALE before the merge.
//
// Exit 0 merged, 1 failed, closed or in conflict, 2 usage or refused,
// 3 waiting (not yet green; run again), 6 no Redis.
package main

import (
	"context"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/gh"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func runLandPR(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "land pr"
	fs := taskFlags(verb)
	repo := fs.String("repo", "mas-bandwidth/nova-tools", "")
	redisAddr := fs.String("redis", redisDefault(), "")
	api := fs.String("api", gh.DefaultAPI, "")
	wait := fs.Duration("wait", 0, "")
	tick := fs.Duration("tick", 30*time.Second, "")
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
	const usage = "land pr <n> [--repo owner/name] [--redis <addr>] [--api <url>] [--wait <d> [--tick <d>]]"
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
	if *wait < 0 || *tick <= 0 {
		return refuse(errOut, verb, "--wait must not be negative and --tick must be positive: "+usage)
	}
	addr := landRedisAddr(*redisAddr)
	if addr == "" {
		return refuse(errOut, verb, "needs --redis <addr> or NOVA_REDIS_ADDR (the check state is read from Redis): "+usage)
	}
	tok, err := landStreamToken()
	if err != nil {
		return landExit(errOut, verb, &stream.Refusal{Why: "no GitHub token", Remedy: err.Error()})
	}
	if tok == "" {
		return landExit(errOut, verb, stream.ErrNoToken)
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
		return 6
	}
	defer st.Close()
	// Three calls per pass; a --wait pass again after the head's event.
	budget := 3
	if *wait > 0 {
		budget = 0
	}
	c := &stream.GitHub{API: *api, Token: tok, Budget: budget, Verb: verb, Redis: st.Client(), Log: errOut}
	rep, err := stream.LandPRWait(ctx, c, st.Client(), stream.LandPROptions{Repo: *repo, N: n, Log: out, Wait: *wait, Tick: *tick})
	if err != nil {
		return landExit(errOut, verb, err)
	}
	token := func(s string) string { return orDash(strings.Join(strings.Fields(s), "_")) }
	fmt.Fprintf(out, "LAND PR repo=%s pr=#%d state=%s head=%s ci=%s merge=%s failed=%s record=%s card=%s card_move=%s rest_calls=%d\n",
		*repo, n, rep.State, orDash(stream.Short(rep.Head)), orDash(rep.CI), orDash(stream.Short(rep.MergeSHA)),
		orDash(strings.Join(rep.Failed, ",")), token(rep.Record), token(rep.Card), token(rep.CardMove), c.Calls)
	switch rep.State {
	case "merged":
		return 0
	case "waiting":
		return 3
	}
	return 1
}
