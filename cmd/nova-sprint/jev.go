// The jev verb: Jev's mechanical passes over one PR (nova-tools #3631), run
// first on every PR, before any friend read. Registered through registry.go;
// main.go is untouched.
//
//	nova-sprint jev mech --repo <r> --n <n> --body-file <f> [--mirror <dir>] [--redis <addr>]
//
// mech reads the PR record pr:<name>:<n> (head, base, base_sha, paths and
// the reads already on it) in one round trip, the changed files
// base_sha..head from the bench mirror (default ~/nova-bench/mirror/<r>.git;
// the verb never fetches), and the PR body from --body-file. It runs lint,
// scope and base (internal/jev) and appends ONE typed line to the record's
// reads (one land_stream.lua call):
//
//	JEV who=jev pass=mech head=<sha> gate=ok|fail lint=<w> scope=<w> base=<w> why=<one line>
//
// The line is never a read (stream.ReadAt skips who=jev*); the stream
// lander reads it as a gate (cfg:land jev). A new line is also a gate row in
// the decision ledger (jev:row:gate:<name>#<n>@<head12>), whose outcome the
// lander joins when the head lands. The same line already last at
// head is not appended again.
//
// Exit 0 recorded with gate=ok; 1 recorded with gate=fail (the remedy on
// the line), or refused (no record, no Redis); 2 usage, before Redis is
// touched.
//
// The decision ledger (nova-tools #4316) is the other subverbs, in
// internal/nsprint/jev: jev sync (the decision points ws:log records, as
// rows, with their outcomes), jev ask (TypeSafe Jev's shadow answer on each
// row, one typed call per row), jev report (agreement with outcomes per
// type, source and prompt version) and jev outcome (the coordinator's
// confirm or override, by hand).
package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/jev"
	ledger "github.com/mas-bandwidth/nova-tools/internal/nsprint/jev"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/prkey"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

func init() {
	ledger.DefaultAddr = func() string { return redisDefault() } // the one resolver (seat.go)
	register(Verb{
		Name: "jev",
		Summary: "mech --repo <r> --n <n> --body-file <f>: Jev's mechanical passes (lint, scope, base) as one JEV line on the PR record, before any friend read; never a read. " +
			ledger.Usage + ": the Jev decision ledger (#4316), every decision a row, Jev's shadow answer, agreement with outcomes",
		Run: runJev,
	})
}

const jevUsage = "want mech --repo <owner/name|name> --n <n> --body-file <f> [--mirror <dir>] [--redis <addr>]"

func runJev(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) > 0 && args[0] != "mech" {
		return ledger.Main(ctx, args, out, errOut)
	}
	if len(args) == 0 {
		return refuse(errOut, "jev", jevUsage+", or "+ledger.Usage)
	}
	const verb = "jev mech"
	fs := taskFlags(verb)
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	repo := fs.String("repo", "", verbflag.HelpRepo)
	n := fs.Int("n", 0, verbflag.HelpN)
	bodyFile := fs.String("body-file", "", "the PR body, a file")
	mirror := fs.String("mirror", "", "the bench mirror of the repo (default ~/nova-bench/mirror/<repo>.git)")
	if err := fs.Parse(args[1:]); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, verb, jevUsage)
	}
	_, name, rerr := prkey.Split(*repo)
	if *repo == "" || rerr != nil || *n <= 0 || *bodyFile == "" {
		return refuse(errOut, verb, jevUsage)
	}
	body, err := os.ReadFile(*bodyFile)
	if err != nil {
		return refuse(errOut, verb, "cannot read --body-file: "+err.Error())
	}
	addr := landRedisAddr(*redisAddr)
	if addr == "" {
		return refuse(errOut, verb, "needs --redis <addr> or NOVA_SPRINT_REDIS")
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		fmt.Fprintf(errOut, "JEV REFUSED %s why=no Redis at %s (%s); remedy: start Redis or pass --redis <addr>\n",
			prkey.Key(name, *n), addr, oneline.Escape(err.Error()))
		return 1
	}
	defer func() { _ = st.Close() }()
	return jevMech(ctx, st.Client(), name, *n, string(body), readMirror(*mirror, name), out, errOut)
}

// jevMech is the verb after its flags: one HMGET, the mirror diff, the
// passes, and at most one AddLine.
func jevMech(ctx context.Context, c *redis.Client, name string, n int, body, mirror string, out, errOut io.Writer) int {
	key := prkey.Key(name, n)
	v, err := c.HMGet(ctx, key, "head", "base", "base_sha", "paths", "reads").Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		fmt.Fprintf(errOut, "JEV REFUSED %s why=%s; remedy: check Redis and rerun\n", key, oneline.Escape(err.Error()))
		return 1
	}
	get := func(i int) string {
		if i < len(v) {
			if s, ok := v[i].(string); ok {
				return strings.TrimSpace(s)
			}
		}
		return ""
	}
	head, base, baseSHA, paths, reads := get(0), get(1), get(2), get(3), get(4)
	if head == "" {
		fmt.Fprintf(errOut, "JEV REFUSED %s why=no record with a head; remedy: nova-sprint pr record --repo %s --n %d --head <sha> --base <b> --stream <s>\n", key, name, n)
		return 1
	}
	in := jev.Input{Head: head, Base: base, BaseSHA: baseSHA, Paths: paths, Body: body}
	from := baseSHA
	if from == "" {
		from = jev.ParseBody(body).Value("BASE-SHA")
	}
	switch {
	case mirror == "":
		in.FilesWhy = "no mirror; pass --mirror <dir>"
	case from == "":
		in.FilesWhy = "no base_sha on the record or base-sha on the body"
	default:
		d, derr := read.MirrorDiff(ctx, mirror, strconv.Itoa(n), from, head)
		if derr != nil {
			in.FilesWhy = derr.Error()
		} else {
			in.Files, in.FilesKnown = d.Files, true
		}
	}
	l := jev.Mech(in)
	line := l.String()
	var lines []string
	for _, s := range strings.Split(reads, "\n") {
		if s = strings.TrimSpace(s); s != "" {
			lines = append(lines, s)
		}
	}
	outcome := "RECORDED"
	if last, ok := jev.At(lines, head); ok && last.String() == line {
		outcome = "SAME"
	} else if _, err := stream.AddLine(ctx, c, name, n, line); err != nil {
		fmt.Fprintf(errOut, "JEV REFUSED %s why=%s; remedy: nova-sprint pr record first, then rerun\n", key, oneline.Escape(err.Error()))
		return 1
	}
	if outcome == "RECORDED" {
		// the gate is a decision (#4316): one row at this head; the lander
		// joins the head's landing to it
		if err := ledger.RecordGate(ctx, c, name, n, head, string(l.Gate()), line); err != nil {
			fmt.Fprintf(errOut, "JEV REFUSED %s ledger why=%s; remedy: the JEV line stands; nova-sprint jev report shows the gate rows\n",
				key, oneline.Escape(err.Error()))
		}
	}
	receipt := fmt.Sprintf("JEV %s %s head=%s gate=%s lint=%s scope=%s base=%s", outcome, key, stream.Short(head),
		l.Gate(), l.Lint.Word, l.Scope.Word, l.Base.Word)
	if l.Gate() == jev.Fail {
		fmt.Fprintf(out, "%s why=%s remedy=fix the %s and push; jev mech runs again at the new head\n",
			receipt, oneline.Escape(l.Why), strings.Join(l.Failed(nil), ", "))
		return 1
	}
	fmt.Fprintln(out, receipt)
	return 0
}
