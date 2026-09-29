// fleet runners (#4356 item D) prints declared (runners.tsv) versus registered
// (GitHub Actions) versus online (unit state from beat) per bench.
//
//	fleet runners [--redis <addr>] [--bench <b>] [--runners <file>]
//	fleet runners set <bench> <n> [--runners <file>]
//
// Exit 0 all matched or runners set; 1 drift found; 2 usage; 5 store unreachable.
package main

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

func runFleetRunners(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) > 0 && args[0] == "set" {
		return runFleetRunnersSet(ctx, args[1:], out, errOut)
	}
	return runFleetRunnersList(ctx, args, out, errOut)
}

func runFleetRunnersList(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := verbflag.New("fleet runners")
	redisAddr := fs.String("redis", redisDefault(), "")
	bench := fs.String("bench", "", "")
	runners := fs.String("runners", "", "")

	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "fleet runners", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "fleet runners", "takes no positional arguments; want 'fleet runners set <bench> <n>' to change")
	}

	st, err := openFleetStore(ctx, *redisAddr)
	if err != nil {
		return unreachable(errOut, "fleet runners", err.Error())
	}
	defer st.Close()

	res, err := fleet.ReadRunnersStatus(ctx, st.Client(), *runners, *bench, fleet.DefaultGitHubRunnersFetcher(""))
	if err != nil {
		return fleetRefuse(errOut, "fleet runners", err)
	}

	for _, b := range res.Benches {
		fmt.Fprintln(out, b.Line())
	}
	fmt.Fprintln(out, res.SummaryLine())

	if len(res.Drifting) > 0 {
		return 1
	}
	return 0
}

func runFleetRunnersSet(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := verbflag.New("fleet runners set")
	runners := fs.String("runners", "", "")

	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return refuse(errOut, "fleet runners set", err.Error())
	}
	if len(pos) != 2 {
		return refuse(errOut, "fleet runners set", "wants <bench> <n>")
	}

	bench := pos[0]
	count, err := strconv.Atoi(pos[1])
	if err != nil || count < 0 {
		return refuse(errOut, "fleet runners set", fmt.Sprintf("invalid runner count %q: must be integer >= 0", pos[1]))
	}

	res, err := fleet.SetRunners(ctx, fleet.SetRunnersRequest{
		Bench:       bench,
		Count:       count,
		RunnersPath: *runners,
	})
	if err != nil {
		if res != nil && res.PlayCmd != "" {
			fmt.Fprintf(out, "RUNNERS SET bench=%s count=%d pr=none\n", res.Bench, res.Count)
			fmt.Fprintln(out, res.PlayCmd)
		}
		return refuse(errOut, "fleet runners set", err.Error())
	}

	fmt.Fprintf(out, "RUNNERS SET bench=%s count=%d pr=%s\n", res.Bench, res.Count, res.PRURL)
	fmt.Fprintln(out, res.PlayCmd)
	return 0
}
