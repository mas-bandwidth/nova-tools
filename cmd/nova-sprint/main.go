package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/route"
)

const usage = `nova-sprint: sprint management and route health monitoring

usage:
  nova-sprint version    print this build identity (--version also accepted)
  nova-sprint route health <provider/model> [--as <friend>] [--n <int>] [--threshold <float>] [--dry-run] [--check] [--probe pass|fail --receipt <id>] [--redis <addr>]

exit codes: 0 OK / unbenched; 1 BENCH (benched now or already); 2 could not run (usage, bad route, Redis down, missing flag).
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	verb := os.Args[1]
	if verb == "version" || verb == "--version" {
		fmt.Println("nova-sprint 0.12.0")
		os.Exit(0)
	}

	if verb == "route" {
		if len(os.Args) < 3 || os.Args[2] != "health" {
			fmt.Fprintln(os.Stderr, "usage: nova-sprint route health <provider/model> [flags]")
			os.Exit(2)
		}
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "error: provider/model required")
			os.Exit(2)
		}
		model := os.Args[3]

		fs := flag.NewFlagSet("route health", flag.ExitOnError)
		asFlag := fs.String("as", "", "actor friend seat")
		nFlag := fs.Int("n", 50, "number of attempts to read")
		thresholdFlag := fs.Float64("threshold", 10.0, "death rate threshold percent")
		dryRunFlag := fs.Bool("dry-run", false, "dry run mode")
		checkFlag := fs.Bool("check", false, "check mode (read only)")
		probeFlag := fs.String("probe", "", "probe action: pass or fail")
		receiptFlag := fs.String("receipt", "", "probe receipt id")
		redisFlag := fs.String("redis", "", "redis address")

		fs.Parse(os.Args[4:])

		redisAddr := route.ResolveRedisAddr(*redisFlag)

		res, err := route.EvaluateHealth(model, *nFlag, *thresholdFlag, *dryRunFlag, *checkFlag, *probeFlag, *receiptFlag, *asFlag, redisAddr)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(res.ExitCode)
		}

		if res.OutputLine != "" {
			fmt.Println(res.OutputLine)
		}
		os.Exit(res.ExitCode)
	}

	fmt.Fprint(os.Stderr, usage)
	os.Exit(2)
}
