// The pr verb writes the per-PR record the stream lander reads
// (nova-tools#3611-#3613), so records exist without GitHub; harvest and the
// read close call it.
//
//	nova-sprint pr record --repo <owner/repo> --n <n> [--head <sha>] [--base <b>] [--stream <s>]
//	    [--base-sha <sha>] [--ci green|red|pending] [--mergeable true|false]
//	    [--state open|closed] [--task <id>] [--kind member|stream] [--redis <addr>]
//	nova-sprint pr lines --repo <owner/repo> --n <n> --add "<typed line>" [--redis <addr>]
//
// pr:<repo>:<n> is a hash (internal/nsprint/land/stream): a new record needs
// --head, --base and --stream; a new head resets ci to pending and mergeable
// to unknown unless the same call names them. pr lines appends one typed line
// (SCORE who=<w> head=<sha> score=N/10 ..., DISPOSITION ..., HOLD ...) to reads.
// One Lua call each, one receipt line. Exit 0 written, 2 refused, 6 no Redis.
package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func init() {
	register(Verb{
		Name:    "pr",
		Summary: "pr record|lines --repo <owner/repo> --n <n> ...: the pr:<repo>:<n> record the stream lander reads (head, base, stream, ci, mergeable, typed read lines)",
		Run:     runPR,
	})
}

func runPR(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "pr", "want record or lines")
	}
	switch args[0] {
	case "record":
		return runPRRecord(ctx, args[1:], out, errOut)
	case "lines":
		return runPRLines(ctx, args[1:], out, errOut)
	}
	return refuse(errOut, "pr", "want record or lines, not "+strconv.Quote(args[0]))
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
	redisAddr := fs.String("redis", "", "")
	repo := fs.String("repo", "", "")
	n := fs.Int("n", 0, "")
	var f stream.RecordFields
	fs.StringVar(&f.Head, "head", "", "")
	fs.StringVar(&f.Base, "base", "", "")
	fs.StringVar(&f.BaseSHA, "base-sha", "", "")
	fs.StringVar(&f.Stream, "stream", "", "")
	fs.StringVar(&f.CI, "ci", "", "")
	fs.StringVar(&f.Mergeable, "mergeable", "", "")
	fs.StringVar(&f.State, "state", "", "")
	fs.StringVar(&f.Task, "task", "", "")
	fs.StringVar(&f.Kind, "kind", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 || !landRepoOK(*repo) || *n <= 0 {
		return refuse(errOut, verb, "needs --repo <owner/repo> and --n <pr number>")
	}
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
		return refuse(errOut, verb, "needs --redis <addr> or NOVA_REDIS_ADDR")
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
	return 0
}

func runPRLines(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "pr lines"
	fs := taskFlags(verb)
	redisAddr := fs.String("redis", "", "")
	repo := fs.String("repo", "", "")
	n := fs.Int("n", 0, "")
	add := fs.String("add", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 || !landRepoOK(*repo) || *n <= 0 || strings.TrimSpace(*add) == "" {
		return refuse(errOut, verb, `needs --repo <owner/repo> --n <n> --add "<typed line>"`)
	}
	if strings.ContainsAny(*add, "\r\n") {
		return refuse(errOut, verb, "--add takes one line")
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
	k, err := stream.AddLine(ctx, st.Client(), *repo, *n, *add)
	if err != nil {
		return landExit(errOut, verb, err)
	}
	word := strings.Fields(*add)[0]
	fmt.Fprintf(out, "PR LINES %s lines=%d added=%s\n", stream.PRKey(*repo, *n), k, oneline.Field(word))
	return 0
}
