package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/benchretire"
)

// benchRetireRunner is the unit half's seam: benchsh.Run in production (nil),
// a fake in tests.
var benchRetireRunner benchretire.Runner

// runBenchRetire is `nova-sprint bench retire --bench <b> --why <text>`
// (nova-tools#3645): take a bench out of the fleet in one verb. It sits next
// to `bench reset`, which clears a bench's cards but keeps the bench.
func runBenchRetire(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs, addr := lifeFlags("bench retire")
	bench := fs.String("bench", "", "bench name")
	why := fs.String("why", "", "receipted reason")
	actor := fs.String("actor", os.Getenv(seatEnv), "operator seat")
	offline := fs.Bool("offline", false, "skip the unit stop on the bench (the bench is gone or unreachable)")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "bench retire", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "bench retire", "takes flags, not positional arguments")
	}
	if *bench == "" || *actor == "" || strings.TrimSpace(*why) == "" {
		return refuse(errOut, "bench retire", "--bench, --why and --actor (or NOVA_FRIEND) are required")
	}
	st, err := openLifeStore(ctx, lifeAddr(*addr))
	if err != nil {
		return refuse(errOut, "bench retire", err.Error())
	}
	defer st.Close()
	res, err := benchretire.Retire(ctx, st.Client(), benchretire.Request{
		Bench: *bench, Actor: *actor, Why: *why, Offline: *offline, Run: benchRetireRunner,
	})
	if err != nil {
		return refuse(errOut, "bench retire", err.Error())
	}
	units := "skipped"
	if len(res.Units) > 0 {
		units = strings.Join(res.Units, ",")
	}
	line := fmt.Sprintf("BENCH RETIRE bench=%s status=%s units=%s requeued=%d repointed=%d deleted=%d took=%s",
		*bench, res.Status, units, res.Requeued, res.Repointed, res.Deleted, res.Took.Round(time.Millisecond))
	if res.Why != "" {
		line += " why=" + res.Why
	}
	if res.Code != 0 {
		fmt.Fprintln(errOut, line)
	} else {
		fmt.Fprintln(out, line)
	}
	return res.Code
}
