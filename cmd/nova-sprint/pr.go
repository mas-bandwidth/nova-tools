// The pr verb writes the per-PR record the stream lander reads
// (nova-tools#3611-#3613), so records exist without GitHub; harvest and the
// read close call it.
//
//	nova-sprint pr record --repo <owner/name|name> --n <n> [--head <sha>] [--base <b>] [--stream <s>]
//	    [--base-sha <sha>] [--ci green|red|pending] [--mergeable true|false]
//	    [--state open|closed] [--task <id>] [--kind member|stream] [--closes <n,n>|-]
//	    [--branch-gone <why>] [--redis <addr>]
//	nova-sprint pr lines --repo <owner/name|name> --n <n> [--add "<typed line>"] [--redis <addr>]
//
// pr:<name>:<n> is a hash (internal/nsprint/land/stream) under the one PR
// record key (internal/nsprint/prkey): --repo owner/name and --repo name
// write the same record read and ci read. A new record needs
// --head, --base and --stream; a new head resets ci to pending and mergeable
// to unknown unless the same call names them; --closes records the issues
// the PR's body closes (- for none), which the lander lands with it. pr lines lists the record's typed lines, or with --add appends one typed line
// (SCORE who=<w> head=<sha> score=N/10 ..., DISPOSITION ..., HOLD ...) to reads.
// --branch-gone marks the PR's head branch gone (branch_gone), one of the
// three records pr reap (pr_reap.go) closes a PR on. A --head is the PR's
// head move (#4094, #4097): every primary of the PR in review at another
// head has its live fix copy ended ok, its open read copies retired and
// fresh read copies cut at the new head (one ns_cm_head call; a line
// PR REHEAD task=<id> reads=<copies> each; a refusal is a PR REHEAD
// REFUSED line on stderr and the record still exits 0).
// One Lua call each, one receipt line. Exit 0 written, 2 refused, 6 no Redis.
package main

import (
	"context"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"io"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func init() {
	register(Verb{
		Name:    "pr",
		Summary: "pr record|lines --repo <owner/name|name> --n <n> ...: the pr:<name>:<n> record the stream lander reads (head, base, stream, ci, mergeable, typed read lines); pr reap --sprint <S>: close stale superseded card PRs with a reason record",
		Run:     runPR,
	})
}

func runPR(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "pr", "want record, lines or reap")
	}
	switch args[0] {
	case "record":
		return runPRRecord(ctx, args[1:], out, errOut)
	case "lines":
		return runPRLines(ctx, args[1:], out, errOut)
	case "reap":
		return runPRReap(ctx, args[1:], out, errOut)
	}
	return refuse(errOut, "pr", "want record, lines or reap, not "+strconv.Quote(args[0]))
}

func oneOf(v string, allowed ...string) bool {
	if v == "" {
		return true
	}
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

func runPRRecord(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "pr record"
	fs := taskFlags(verb)
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	repo := fs.String("repo", "", verbflag.HelpRepo)
	n := fs.Int("n", 0, verbflag.HelpN)
	var f stream.RecordFields
	fs.StringVar(&f.Head, "head", "", "the PR's head sha")
	fs.StringVar(&f.Base, "base", "", "the PR's base branch")
	fs.StringVar(&f.BaseSHA, "base-sha", "", "the base's sha the read was against, 40 hex")
	fs.StringVar(&f.Stream, "stream", "", verbflag.HelpStream)
	fs.StringVar(&f.CI, "ci", "", "the ci word: green, red or pending")
	fs.StringVar(&f.Mergeable, "mergeable", "", "true or false")
	fs.StringVar(&f.State, "state", "", "open or closed")
	fs.StringVar(&f.Task, "task", "", "the task id the PR is for")
	fs.StringVar(&f.Kind, "kind", "", "member or stream")
	fs.StringVar(&f.BranchGone, "branch-gone", "", "why the branch is gone")
	closes := fs.String("closes", "", "the PR numbers this one closes, comma-separated, or -")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if *closes == "-" {
		f.Closes = "-"
	} else if *closes != "" {
		nums := strings.FieldsFunc(*closes, func(r rune) bool { return r == ',' || r == ' ' || r == '#' })
		for _, x := range nums {
			if k, err := strconv.Atoi(x); err != nil || k <= 0 {
				return refuse(errOut, verb, "--closes takes issue numbers (1,2) or -, got "+strconv.Quote(*closes))
			}
		}
		f.Closes = strings.Join(nums, " ")
	}
	full, rerr := prkey.Full(*repo)
	if fs.NArg() > 0 || rerr != nil || *n <= 0 {
		return refuse(errOut, verb, "needs --repo <owner/name|name> and --n <pr number>")
	}
	*repo = full
	f.Head = strings.ToLower(strings.TrimSpace(f.Head))
	if f.Head != "" && (len(f.Head) < 7 || strings.Trim(f.Head, "0123456789abcdef") != "") {
		return refuse(errOut, verb, "--head must be a hex sha, got "+strconv.Quote(f.Head))
	}
	if !oneOf(f.CI, "green", "red", "pending") || !oneOf(f.Mergeable, "true", "false") ||
		!oneOf(f.State, "open", "closed") || !oneOf(f.Kind, "member", "stream") {
		return refuse(errOut, verb, "--ci green|red|pending, --mergeable true|false, --state open|closed, --kind member|stream")
	}
	addr := landRedisAddr(*redisAddr)
	if addr == "" {
		return refuse(errOut, verb, "needs --redis <addr> or NOVA_SPRINT_REDIS")
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
		return 6
	}
	defer st.Close()
	r, err := stream.Record(ctx, st.Client(), *repo, *n, f)
	if err != nil {
		return landExit(errOut, verb, err)
	}
	fmt.Fprintf(out, "PR RECORD %s head=%s base=%s stream=%s ci=%s mergeable=%s state=%s created=%t\n",
		stream.PRKey(*repo, *n), orDash(stream.Short(r.Head)), orDash(r.Base), oneline.Field(orDash(r.Stream)),
		orDash(r.CI), orDash(r.Mergeable), orDash(r.State), r.Created)
	if f.Head == "" {
		return 0
	}
	recut, err := taskcard.Rehead(ctx, st.Client(), *repo, *n, "pr record")
	if err != nil {
		// the record is written (exit 0); the refusal is named, and the
		// deal pass's review reads duty re-heads on its next tick
		fmt.Fprintf(errOut, "PR REHEAD REFUSED %s why=%s; the record is written, the deal pass re-cuts the reads\n",
			stream.PRKey(*repo, *n), oneline.Field(err.Error()))
		return 0
	}
	for _, x := range recut {
		fmt.Fprintf(out, "PR REHEAD task=%s reads=%s\n", x.Primary, orDash(strings.Join(x.Copies, ",")))
	}
	return 0
}

func runPRLines(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "pr lines"
	fs := taskFlags(verb)
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	repo := fs.String("repo", "", verbflag.HelpRepo)
	n := fs.Int("n", 0, verbflag.HelpN)
	add := fs.String("add", "", "the typed line to add to the record (without it, the record's lines are listed)")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	full, rerr := prkey.Full(*repo)
	if fs.NArg() > 0 || rerr != nil || *n <= 0 {
		return refuse(errOut, verb, `needs --repo <owner/name|name> --n <n> [--add "<typed line>"]`)
	}
	*repo = full
	if strings.ContainsAny(*add, "\r\n") {
		return refuse(errOut, verb, "--add takes one line")
	}
	addr := landRedisAddr(*redisAddr)
	if addr == "" {
		return refuse(errOut, verb, "needs --redis <addr> or NOVA_SPRINT_REDIS")
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
		return 6
	}
	defer st.Close()
	if strings.TrimSpace(*add) == "" {
		// The listing (#4352 A): `pr lines --repo nova-tools --n 4373`
		// printed a usage line instead of the lines. Every typed line of
		// the record (its reads field, then its lines list, as the lander
		// reads them), one per line, then the receipt.
		prs, err := stream.LoadPRs(ctx, st.Client(), *repo, []int{*n})
		if err != nil {
			fmt.Fprintf(errOut, "nova-sprint %s: %v\n", verb, err)
			return 6
		}
		p := prs[0]
		for _, l := range p.Reads {
			fmt.Fprintln(out, l)
		}
		fmt.Fprintf(out, "PR LINES %s lines=%d head=%s record=%t\n", stream.PRKey(*repo, *n), len(p.Reads), orDash(stream.Short(p.Head)), p.Exists)
		return 0
	}
	k, err := stream.AddLine(ctx, st.Client(), *repo, *n, *add)
	if err != nil {
		return landExit(errOut, verb, err)
	}
	word := strings.Fields(*add)[0]
	fmt.Fprintf(out, "PR LINES %s lines=%d added=%s\n", stream.PRKey(*repo, *n), k, oneline.Field(word))
	return 0
}

// bareRepo is --repo as the bare name when it is owner/name or name
// (internal/nsprint/prkey), so a verb keyed by the bare name (ci, read)
// takes either spelling; anything else is returned as given for the verb's
// own check to refuse.
func bareRepo(repo string) string {
	if _, name, err := prkey.Split(repo); err == nil {
		return name
	}
	return repo
}
