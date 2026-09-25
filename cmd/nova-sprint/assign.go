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
	"time"

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
		Summary: "--from <f> --reason <r> or --sprint <S> --floor <n>: redistribute in one call",
		Run:     runRedistribute,
	})
}

// runAssign prints one line per input line, in order: `ASSIGN <status> <task>
// -> <friend> [detail]`, or `DEDUP <task>: <reason>`. Exit 0 when every line
// moved or was already there, 1 when any line was refused, 2 could not run.
func runAssign(ctx context.Context, args []string, out, errOut io.Writer) int {
	if hasAssignFlag(args, "--actor") {
		fmt.Fprintln(errOut, "REFUSED actor: --actor is not supported; actor is NOVA_FRIEND")
		return 2
	}
	fs := taskFlags("assign")
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	stdin := fs.Bool("stdin", false, "")
	reason := fs.String("reason", "", "")
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
	actor := os.Getenv(seatEnv)
	if actor == "" {
		fmt.Fprintln(errOut, "REFUSED actor: NOVA_FRIEND is empty; run from a seat that exports it (nova-tools#2929)")
		return 2
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
	receipts, err := assign.Batch(ctx, st, *sprint, lines, *reason, actor, *idem)
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
	if hasAssignFlag(args, "--actor") {
		fmt.Fprintln(errOut, "REFUSED actor: --actor is not supported; actor is NOVA_FRIEND")
		return 2
	}
	fs := taskFlags("redistribute")
	redisAddr := fs.String("redis", "", "")
	from := fs.String("from", "", "")
	reason := fs.String("reason", "", "")
	to := fs.String("to", "", "")
	kinds := fs.String("kind", "", "")
	sprint := fs.String("sprint", "", "")
	floor := fs.Int("floor", -1, "")
	maxMove := fs.Int("max-move", 0, "")
	loop := fs.Int("loop", 0, "")
	idem := fs.String("idem", "", "")
	for _, arg := range args {
		name := strings.SplitN(arg, "=", 2)[0]
		if name == "--may-hold" || name == "--builders" || name == "--coordinator" {
			fmt.Fprintln(errOut, "REFUSED roster flags: roles come from friend:<f>:roles; change them with nova-sprint friend roles --set")
			return 2
		}
	}
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "redistribute", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "redistribute", "takes flags, not positional arguments")
	}
	if (*from == "") == (*floor < 0) {
		return refuse(errOut, "redistribute", "choose --from <friend> or --sprint <S> --floor <n>")
	}
	if *from != "" && *reason == "" {
		return refuse(errOut, "redistribute", "needs --from <friend> and --reason <why>; the reason is the title marker [moved from <f>: <why>]")
	}
	if *floor >= 0 && *sprint == "" {
		return refuse(errOut, "redistribute", "--floor needs --sprint <S>")
	}
	actor := os.Getenv(seatEnv)
	if actor == "" {
		fmt.Fprintln(errOut, "REFUSED actor: NOVA_FRIEND is empty; run from a seat that exports it (nova-tools#2929)")
		return 2
	}
	st, err := openTaskStore(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "redistribute", err.Error())
	}
	defer st.Close()
	if *floor >= 0 {
		limit := *maxMove
		if limit == 0 {
			limit = int(^uint(0) >> 1)
		}
		for {
			res, err := life.RedistributeFloor(ctx, st, *sprint, *floor, limit, actor, *idem)
			if err != nil {
				return refuse(errOut, "redistribute", err.Error())
			}
			for _, e := range res.Events {
				fmt.Fprintf(out, "%s %s -> %s %s\n", e.What, e.Task, e.To, e.Detail)
			}
			fmt.Fprintf(out, "REDISTRIBUTE sprint=%s floor=%d moved=%d calls=1\n", *sprint, *floor, res.Moved)
			if *loop <= 0 {
				return 0
			}
			select {
			case <-ctx.Done():
				return 0
			case <-time.After(time.Duration(*loop) * time.Second):
			}
		}
	}
	res, err := assign.From(ctx, st, assign.FromRequest{
		From: *from, Reason: *reason, To: *to, Kinds: splitCSV(*kinds),
		Actor: actor, Idem: *idem,
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

func hasAssignFlag(args []string, want string) bool {
	for _, arg := range args {
		if strings.SplitN(arg, "=", 2)[0] == want {
			return true
		}
	}
	return false
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
