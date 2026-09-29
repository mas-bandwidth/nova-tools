// fleet doctor (#4356 item B) is the 7-dimension bench health screen from
// Redis alone: no ssh.
//
//	fleet doctor [--redis <addr>] [--bench <b>] [--runners <file>]
//
// It displays an 8-column overview table of every registered bench, one
// line per drift with the fix, and a summary line (DOCTOR OK ... or
// DOCTOR FIX ...).
//
// Exit 0 clean; 1 one or more drifts; 2 usage; 5 store unreachable.
package main

import (
	"context"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

func runFleetDoctor(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := verbflag.New("fleet doctor")
	redisAddr := fs.String("redis", redisDefault(), "")
	bench := fs.String("bench", "", "")
	runners := fs.String("runners", "", "")

	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "fleet doctor", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "fleet doctor", "takes no positional arguments")
	}

	st, err := openFleetStore(ctx, *redisAddr)
	if err != nil {
		return unreachable(errOut, "fleet doctor", err.Error())
	}
	defer st.Close()

	res, err := fleet.Doctor(ctx, st.Client(), fleet.DoctorRequest{
		BenchFilter: *bench,
		RunnersPath: *runners,
	})
	if err != nil {
		return fleetRefuse(errOut, "fleet doctor", err)
	}

	// 1. Overview table
	fmt.Fprint(out, res.ScreenTable())

	// 2. Fix lines
	for _, fix := range res.Fixes {
		fmt.Fprintln(out, fix.String())
	}

	// 3. Summary line
	fmt.Fprintln(out, res.SummaryLine())

	if res.TotalFixes > 0 {
		return 1
	}
	return 0
}
