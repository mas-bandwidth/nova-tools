// assign and redistribute move many tasks in one Redis Function call
// (nova-tools #3103, spec #2756 v6 4.6, control 46). The interim friend-queue
// took ~60 s per call; these are one round trip per batch.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/assign"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
)

// assignStdin is where `assign --stdin` reads its lines; tests replace it.
var assignStdin io.Reader = os.Stdin

func init() {
	register(Verb{
		Name:    "assign",
		Summary: "--sprint <S> --stdin: many `<task> <friend>` lines in ONE function call, one receipt per line",
		Run:     runAssign,
	})
	register(Verb{
		Name:    "redistribute",
		Summary: "--from <f> --reason <r> [--to <g>] [--kind k,...]: the tick's redistribution by hand, one call",
		Run:     runRedistribute,
	})
}

// runAssign prints one line per input line, in order: `ASSIGN <status> <task>
// -> <friend> [detail]`, or `DEDUP <task>: <reason>`. Exit 0 when every line
// moved or was already there, 1 when any line was refused, 2 could not run.
func runAssign(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("assign")
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	stdin := fs.Bool("stdin", false, "")
	reason := fs.String("reason", "", "")
	actor := fs.String("actor", "", "")
	idem := fs.String("idem", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "assign", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "assign", "takes flags, not positional arguments; lines come on --stdin")
	}
	if !*stdin || *sprint == "" {
		return refuse(errOut, "assign", "needs --sprint <S> and --stdin with `<task> <friend>` lines")
	}
	lines, err := assign.ParseLines(assignStdin)
	if err != nil {
		return refuse(errOut, "assign", err.Error())
	}
	if len(lines) == 0 {
		return refuse(errOut, "assign", "no `<task> <friend>` lines on stdin")
	}
	st, err := openTaskStore(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "assign", err.Error())
	}
	defer st.Close()
	receipts, err := assign.Batch(ctx, st, *sprint, lines, *reason, *actor, *idem)
	if err != nil {
		return refuse(errOut, "assign", err.Error())
	}
	code, moved := 0, 0
	for _, r := range receipts {
		fmt.Fprintln(out, r.String())
		if r.Refused() {
			code = 1
		} else if r.Status == assign.Moved {
			moved++
		}
	}
	fmt.Fprintf(out, "ASSIGNED %d/%d sprint=%s calls=1\n", moved, len(receipts), *sprint)
	return code
}

// runRedistribute prints one line per event (MOVED, DEDUP, KEPT) and the
// summary line. Exit 0 ran, 2 could not run.
func runRedistribute(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("redistribute")
	redisAddr := fs.String("redis", "", "")
	from := fs.String("from", "", "")
	reason := fs.String("reason", "", "")
	to := fs.String("to", "", "")
	kinds := fs.String("kind", "", "")
	mayHold := fs.String("may-hold", "", "")
	builders := fs.String("builders", "", "")
	coordinator := fs.String("coordinator", "", "")
	actor := fs.String("actor", "", "")
	idem := fs.String("idem", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "redistribute", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "redistribute", "takes flags, not positional arguments")
	}
	if *from == "" || *reason == "" {
		return refuse(errOut, "redistribute", "needs --from <friend> and --reason <why>; the reason is the title marker [moved from <f>: <why>]")
	}
	if *to == "" && *mayHold == "" && *builders == "" && *coordinator == "" {
		return refuse(errOut, "redistribute", "needs --to <friend> or the roster (--may-hold, --builders, --coordinator) to route by kind")
	}
	st, err := openTaskStore(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "redistribute", err.Error())
	}
	defer st.Close()
	res, err := assign.From(ctx, st, assign.FromRequest{
		From: *from, Reason: *reason, To: *to, Kinds: splitCSV(*kinds),
		Roster: life.Roster{MayHold: splitCSV(*mayHold), Builders: splitCSV(*builders), Coordinator: *coordinator},
		Actor:  *actor, Idem: *idem,
	})
	if err != nil {
		return refuse(errOut, "redistribute", err.Error())
	}
	for _, e := range res.Events {
		fmt.Fprintln(out, e.String())
	}
	fmt.Fprintln(out, res.Line())
	return 0
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
