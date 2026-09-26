// The spec verb (nova-tools#3370, part of #3364): the specs table in Redis.
//
//	nova-sprint spec mark --ref <repo>#<n> --rev <k> --as <friend> --score <s> [--stream <name>] [--sprint <S>] [--redis <addr>]
//	nova-sprint spec list [--stream <name>] [--redis <addr>]
//
// mark stores the line `SPEC who=<friend> rev=<k> score=<s>` on
// pr:<repo>:<n>:lines and its facts on the record in one ns_spec_mark call;
// the second distinct 10 at the current rev moves the spec working -> done
// and releases every task waiting on spec:<repo>#<n> in that call. `read
// post` of a SPEC line is the same call. list prints the specs block
// (`stream | working | done`) from Redis alone; with --stream it adds the
// stream's ids, oldest first. Exit 0 done, 1 refused (STALE_REV, INVALID),
// 2 usage or could not run.
package main

import (
	"context"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"io"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/spec"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "spec",
		Summary: "mark: a SPEC line's facts to Redis in one call, the second 10 releases its builds; list: the specs block (working | done per stream) from Redis",
		Run:     runSpec,
	})
}


func runSpec(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "spec", "wants a subverb")
	}
	if args[0] != "mark" && args[0] != "list" {
		return refuse(errOut, "spec", "unknown subverb "+args[0])
	}
	sub := args[0]
	fs := taskFlags("spec " + sub)
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	ref := fs.String("ref", "", "the spec issue, <repo>#<n> (mark)")
	rev := fs.Int("rev", 0, "the spec revision the score is of (mark)")
	who := fs.String("as", "", verbflag.HelpAs)
	score := fs.Int("score", -1, "the score, 0-10 (mark)")
	stream := fs.String("stream", "", verbflag.HelpStream)
	sprint := fs.String("sprint", "", verbflag.HelpSprint)
	if err := fs.Parse(args[1:]); err != nil {
		return refuse(errOut, "spec "+sub, err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "spec "+sub, "takes flags, not positional arguments")
	}
	var pos []string
	if *ref != "" {
		pos = []string{*ref}
	}
	*redisAddr = redisOr(*redisAddr) // the one resolver (seat.go)
	if *redisAddr == "" {
		return refuse(errOut, "spec "+sub, "needs --redis <addr> (or NOVA_SPRINT_REDIS)")
	}
	var m spec.Mark
	if sub == "mark" {
		if len(pos) != 1 {
			return refuse(errOut, "spec mark", "wants --ref <repo>#<n>")
		}
		repo, n, err := parseRepoPR(pos[0])
		if err != nil {
			return refuse(errOut, "spec mark", "want <repo>#<n>, got "+strconv.Quote(pos[0]))
		}
		_, name, err := prkey.Split(repo)
		if err != nil || *rev < 1 || *who == "" || *score < 0 || *score > 10 {
			return refuse(errOut, "spec mark", "wants --ref <repo>#<n>")
		}
		m = spec.Mark{Repo: name, N: strconv.Itoa(n), Who: *who, Rev: *rev, Score: *score, Stream: *stream, Sprint: *sprint, Actor: *who}
		m.Line = fmt.Sprintf("SPEC who=%s rev=%d score=%d", m.Who, m.Rev, m.Score)
	} else if len(pos) != 0 || *who != "" || *rev != 0 || *score != -1 || *sprint != "" {
		return refuse(errOut, "spec list", "want list [--stream <name>] [--redis <addr>]")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "spec "+sub, err.Error())
	}
	defer func() { _ = st.Close() }()
	if sub == "mark" {
		r, err := spec.Do(ctx, st.Client(), m)
		if err != nil {
			fmt.Fprintf(errOut, "nova-sprint spec mark: %v\n", err)
			return 2
		}
		if r.ExitCode() != 0 {
			fmt.Fprintln(errOut, r.Line(m))
			return 1
		}
		fmt.Fprintln(out, r.Line(m))
		return 0
	}
	l, err := spec.List(ctx, st.Client(), *stream)
	if err != nil {
		fmt.Fprintf(errOut, "nova-sprint spec list: %v\n", err)
		return 2
	}
	fmt.Fprint(out, spec.Block(l.Rows))
	for _, id := range l.Working {
		fmt.Fprintln(out, "working "+id)
	}
	for _, id := range l.Done {
		fmt.Fprintln(out, "done "+id)
	}
	w, d := 0, 0
	for _, r := range l.Rows {
		w, d = w+r.Working, d+r.Done
	}
	fmt.Fprintf(out, "SPEC LIST streams=%d working=%d done=%d\n", len(l.Rows), w, d)
	return 0
}
