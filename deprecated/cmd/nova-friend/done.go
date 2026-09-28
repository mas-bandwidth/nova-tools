package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card/harvestcopy"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/redis/go-redis/v9"
)

// doneTimeout bounds the store round trips on each side of the gate.
const doneTimeout = 30 * time.Second

// runDone is `done --as <you> --id <copy> (--ok [--pr <repo>#<n> --head
// <sha> --repo <checkout> [--test <t>] [--branch <b>]] [--done-already
// <sha>] | --score <N>/10 [--gates <g>] [--finding <text>] | --fail <why>)
// [--token <t>]`: card end --id <copy> with the same evidence, refused
// NOTMINE for a copy that is not yours. --ok --pr on a work or fix copy
// first runs the spec gate (card.GateFriendCopy) in --repo, your checkout
// at --head: the copy's TEST (a fix's own finding test, --test) red at its
// base and green at the head, then the unit tier; a red refuses the end
// (GATE lines, then the refusal) and records nothing. A green records the
// PR (card.RecordPR, what the wrapper's harvest writes) so the end is not
// refused NOPR; --branch is the PR's branch when it is not the brief's.
func runDone(ctx context.Context, e env, args []string, out, errOut io.Writer) int {
	const verb = "done"
	fs := newFlags(verb)
	redisAddr := fs.String("redis", "", redisHelp)
	as := fs.String("as", "", asHelp)
	id := fs.String("id", "", "the copy you are ending, <primary>~<n>")
	ok := fs.Bool("ok", false, "the copy is done well (with --pr for a work or fix copy)")
	pr := fs.String("pr", "", "the pull request you opened, <repo>#<n> (with --ok)")
	head := fs.String("head", "", "the PR's head sha (with --pr)")
	doneAlready := fs.String("done-already", "", "ABSTAIN: the work was done already at this sha")
	branch := fs.String("branch", "", "the PR's branch when it is not the brief's")
	repoDir := fs.String("repo", "", "your checkout at --head, where the spec gate runs (with --pr)")
	findingTest := fs.String("test", "", "a fix copy's finding test, `<package> <TestName>` (with --pr)")
	score := fs.String("score", "", "a read copy's score, N/10")
	gates := fs.String("gates", "", "a read's gates, on the record")
	finding := fs.String("finding", "", "a read's finding, on the record")
	fail := fs.String("fail", "", "the copy failed: why")
	token := fs.String("token", "", "the copy's token from pull; a stale one is FENCED")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}
	name, err := e.actor(*as)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	k := taskcard.Consumer{Kind: "friend", Name: name}
	if *id == "" || !taskcard.IsCopy(*id) {
		return refuse(errOut, verb, "--id wants a copy id <primary>~<n>")
	}
	ways := 0
	for _, on := range []bool{*ok, *fail != "", *score != ""} {
		if on {
			ways++
		}
	}
	if ways != 1 && !(*ok && *score != "") {
		return refuse(errOut, verb, "wants exactly one of --ok, --score <N>/10 and --fail <why>")
	}
	if *pr != "" && *head == "" {
		return refuse(errOut, verb, "--pr wants --head <sha>")
	}
	if *pr != "" && !*ok {
		return refuse(errOut, verb, "--pr goes with --ok")
	}
	r := taskcard.EndRequest{IDs: []string{*id}, OK: *fail == "", Why: *fail, Head: *head, DoneAlready: *doneAlready,
		Gates: *gates, Finding: *finding, Reader: name, Token: *token, By: name}
	if *pr != "" {
		repo, n, cut := strings.Cut(*pr, "#")
		if !cut || repo == "" || n == "" {
			return refuse(errOut, verb, "--pr wants <repo>#<n>")
		}
		r.Repo, r.PR = repo, n
	}
	if *score != "" {
		s, err := strconv.Atoi(strings.TrimSuffix(*score, "/10"))
		if err != nil || s < 1 || s > 10 {
			return refuse(errOut, verb, "--score wants N/10, N 1-10")
		}
		r.Score = s
	}
	start := time.Now()
	ms := func() int64 { return time.Since(start).Milliseconds() }
	// the store's calls are bounded on each side of the gate; the gate runs
	// under the caller's context (a gate is go test, not a round trip)
	parent := ctx
	ctx, cancel := context.WithTimeout(parent, doneTimeout)
	defer cancel()
	st, err := openStore(ctx, e.redis(*redisAddr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer func() { _ = st.Close() }()
	c := st.Client()
	// The copy must be yours: card end fences on the token when one is
	// given, and ending another consumer's copy by id alone would be a
	// silent theft.
	rec, err := c.HGetAll(ctx, taskcard.Key(*id)).Result()
	holder := rec["consumer"]
	switch {
	case err != nil && !errors.Is(err, redis.Nil):
		return refuse(errOut, verb, err.Error())
	case holder == "":
		return refused(errOut, verb, "NOCOPY task:"+*id+" is no copy")
	case holder != k.String():
		return refused(errOut, verb, "NOTMINE task:"+*id+" is "+holder+"'s copy, not "+k.String()+"'s")
	}
	if r.PR != "" {
		if why := gateCopyEnd(parent, c, *id, rec, *repoDir, *findingTest, *head, out); why != "" {
			return refused(errOut, verb, why)
		}
		after, cancelAfter := context.WithTimeout(parent, doneTimeout)
		defer cancelAfter()
		ctx = after
		// The PR you opened is recorded before the end, as the wrapper's
		// harvest records a bench's: card end refuses an ok whose PR
		// record is missing (NOPR) or at another head.
		n, err := strconv.Atoi(r.PR)
		if err != nil || n <= 0 {
			return refuse(errOut, verb, "--pr wants <repo>#<n>, n a PR number")
		}
		cc := card.CopyCardFrom(*id, rec)
		b := *branch
		if b == "" {
			b = strings.TrimSpace(cc.Branch)
		}
		if b == "" {
			cn, _ := card.CopyNumber(*id)
			b = card.WrapperBranch(card.CopySprint, card.CopyCardLabel(*id), cn)
		}
		if err := card.RecordPR(ctx, c, nil, harvestcopy.Result{Repo: r.Repo, PR: n, Head: r.Head, Branch: b}, cc); err != nil {
			return refuse(errOut, verb, err.Error())
		}
		fmt.Fprintf(out, "FRIEND RECORDED pr=%s#%d head=%s branch=%s\n", prkey.Name(r.Repo), n, r.Head, b)
	}
	ended, err := taskcard.End(ctx, c, r)
	if err != nil {
		if why, ok := taskcard.IsRefused(err); ok {
			refusalLine(errOut, verb, why)
			return moveRefusedCode(why)
		}
		return refuse(errOut, verb, err.Error())
	}
	for _, x := range ended {
		if x.To == "already" {
			fmt.Fprintf(out, "FRIEND ALREADY id=%s primary=%s ended=%s\n", x.Copy, x.Primary, x.From)
			continue
		}
		fmt.Fprintf(out, "FRIEND ENDED id=%s primary=%s from=%s to=%s next=%s\n", x.Copy, x.Primary, x.From, x.To, dash(x.Next))
	}
	fmt.Fprintf(out, "FRIEND DONE as=%s n=%d ms=%d\n", name, len(ended), ms())
	return 0
}

// moveRefusedCode is a move refusal's exit, as nova-sprint card end spells
// it: 3 FENCED, 4 CONFLICT, else 1.
func moveRefusedCode(why string) int {
	switch {
	case strings.HasPrefix(why, "FENCED"):
		return 3
	case strings.HasPrefix(why, "CONFLICT"):
		return 4
	}
	return 1
}

// gateCopyEnd is the spec gate at the door that ends a code copy ok with a
// PR outside the copy wrapper. A read copy carries no commit and passes.
// Any other copy's end runs the gate in repoDir, the checkout at head, the
// base the copy's (a fix's the PR head) and the test the copy's (a fix's the
// finding test it names, never none); the rows print as GATE lines. It
// returns "" when the end may go on, else the refusal: the gate's typed
// reason and why, or why the gate could not run.
func gateCopyEnd(ctx context.Context, c redis.Cmdable, id string, rec map[string]string, repoDir, findingTest, head string, out io.Writer) string {
	cc := card.CopyCardFrom(id, rec)
	if cc.Leg == "read" {
		return ""
	}
	if repoDir == "" {
		return card.GateNoTest + " --ok --pr wants --repo <your checkout at --head>: the spec gate runs there before the end"
	}
	if cc.Test == "" && cc.Primary != "" {
		// a copy cut before the carry carried test: the primary's
		cc.Test = c.HGet(ctx, taskcard.Key(cc.Primary), "test").Val()
	}
	g, err := card.GateFriendCopy(ctx, card.FriendGate{Copy: cc, Finding: findingTest, Repo: repoDir, Head: head})
	if err != nil {
		return "gate could not run: " + err.Error()
	}
	for _, row := range g.Rows {
		fmt.Fprintf(out, "GATE %s\n", row)
	}
	if !g.Passed() {
		return g.Reason + " " + g.Why
	}
	return ""
}
