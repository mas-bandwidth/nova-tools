package main

// The hygiene verb's flags. The work itself is internal/pulse/hygiene.go; the
// two doors below are the real /proc liveness probe and df/du, which tests
// replace with fakes so no test reads the machine or the network.

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

var (
	hygieneProcs pulse.HygieneProcs = pulse.OSProcs{}
	hygieneDisk                     = func(home string) pulse.HygieneDisk { return pulse.OSDisk{Home: home} }
)

const hygieneUsage = `nova-pulse hygiene run                     --home <dir> [--dry-run] [--hostname <name>]
nova-pulse hygiene reap <slot>            --home <dir>
nova-pulse hygiene delete-job <slot> <job> --home <dir>
nova-pulse hygiene delete-slot <slot>     --home <dir>
nova-pulse hygiene drop-cache             --home <dir>
nova-pulse hygiene log [n]                --home <dir>`

func hygieneRefuse(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "nova-pulse hygiene: %s; run: nova-pulse help\n", what)
	return 2
}

// cmdHygiene parses the subcommand and the flags that follow it. Every path
// hangs under --home: the two roots, the action log and the build cache.
func cmdHygiene(args []string, stdout, stderr io.Writer, now time.Time) int {
	var home, hostname, roots, logPath, cache string
	dry := false
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			fmt.Fprintln(stdout, hygieneUsage)
			return 0
		case a == "--dry-run":
			dry = true
		case a == "--home" || a == "--hostname" || a == "--roots" || a == "--log" || a == "--cache":
			if i+1 >= len(args) {
				return hygieneRefuse(stderr, a+" wants a value")
			}
			i++
			switch a {
			case "--home":
				home = args[i]
			case "--hostname":
				hostname = args[i]
			case "--roots":
				roots = args[i]
			case "--log":
				logPath = args[i]
			case "--cache":
				cache = args[i]
			}
		case strings.HasPrefix(a, "--home="):
			home = strings.TrimPrefix(a, "--home=")
		case strings.HasPrefix(a, "--hostname="):
			hostname = strings.TrimPrefix(a, "--hostname=")
		case strings.HasPrefix(a, "--roots="):
			roots = strings.TrimPrefix(a, "--roots=")
		case strings.HasPrefix(a, "--log="):
			logPath = strings.TrimPrefix(a, "--log=")
		case strings.HasPrefix(a, "--cache="):
			cache = strings.TrimPrefix(a, "--cache=")
		case strings.HasPrefix(a, "-") && a != "-":
			return hygieneRefuse(stderr, "unknown flag "+oneline.Escape(a))
		default:
			rest = append(rest, a)
		}
	}
	if len(rest) == 0 {
		fmt.Fprintln(stderr, hygieneUsage)
		return 2
	}
	in := pulse.HygieneInput{
		Home: home, Hostname: hostname, LogPath: logPath, CachePath: cache, DryRun: dry,
		Now: func() time.Time { return now }, Procs: hygieneProcs, Disk: hygieneDisk(home),
		Stdout: stdout, Stderr: stderr,
	}
	if roots != "" {
		for _, r := range strings.Split(roots, ",") {
			if r = strings.TrimSpace(r); r != "" {
				in.Roots = append(in.Roots, r)
			}
		}
	}
	verb := rest[0]
	switch verb {
	case "run":
		in.Verb = "run"
	case "reap":
		if len(rest) < 2 {
			return hygieneRefuse(stderr, "reap wants a slot (pass: nova-pulse hygiene reap <slot> --home <dir>)")
		}
		in.Verb, in.Slot = "reap", rest[1]
	case "delete-job":
		if len(rest) < 3 {
			return hygieneRefuse(stderr, "delete-job wants a slot and a job (pass: nova-pulse hygiene delete-job <slot> <job> --home <dir>)")
		}
		in.Verb, in.Slot, in.Job = "delete-job", rest[1], rest[2]
	case "delete-slot":
		if len(rest) < 2 {
			return hygieneRefuse(stderr, "delete-slot wants a slot (pass: nova-pulse hygiene delete-slot <slot> --home <dir>)")
		}
		in.Verb, in.Slot = "delete-slot", rest[1]
	case "drop-cache":
		in.Verb = "drop-cache"
	case "log":
		in.Verb = "log"
		in.N = 20
		if len(rest) >= 2 {
			n, err := strconv.Atoi(rest[1])
			if err != nil || n < 1 {
				return hygieneRefuse(stderr, "log wants a positive line count, got "+oneline.Field(rest[1]))
			}
			in.N = n
		}
	default:
		return hygieneRefuse(stderr, "unknown hygiene subcommand "+oneline.Quote(verb))
	}
	return pulse.Hygiene(in)
}
