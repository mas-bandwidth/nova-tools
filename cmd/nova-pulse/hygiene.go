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

	novalog "github.com/mas-bandwidth/nova-tools/internal/log"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

var (
	hygieneProcs pulse.HygieneProcs = pulse.OSProcs{}
	hygieneDisk                     = func(home string) pulse.HygieneDisk { return pulse.OSDisk{Home: home} }
)

// THE FLAG IS --event-log AND NOT --log. Every other emitting verb names its structured
// sink --log (SPEC-LOGS.md Part 2), but hygiene has owned --log since it shipped: there it
// names the per-bench ACTION log, the ~/hygiene.log line an operator greps and Alloy tails.
// Taking that name for the JSON stream would silently redirect a flag already in the
// timers. One name per file: --log is the action log, --event-log is the structured stream.
const hygieneUsage = `nova-pulse hygiene run                     --home <dir> [--dry-run] [--hostname <name>] [--label <name>] [--event-log <path>]
nova-pulse hygiene reap <slot>            --home <dir>
nova-pulse hygiene delete-job <slot> <job> --home <dir>
nova-pulse hygiene delete-slot <slot>     --home <dir>
nova-pulse hygiene drop-cache             --home <dir>
nova-pulse hygiene log [n]                --home <dir>`

// firstNonBlank is the first of its arguments that holds something, "" when none does.
func firstNonBlank(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func hygieneRefuse(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "nova-pulse hygiene: %s; run: nova-pulse help\n", what)
	return 2
}

// cmdHygiene parses the subcommand and the flags that follow it. Every path
// hangs under --home: the two roots, the action log and the build cache.
func cmdHygiene(args []string, stdout, stderr io.Writer, now time.Time) int {
	var home, hostname, roots, logPath, cache, eventLog, label string
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
		case a == "--home" || a == "--hostname" || a == "--roots" || a == "--log" || a == "--cache" || a == "--event-log" || a == "--label":
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
			case "--event-log":
				eventLog = args[i]
			case "--label":
				label = args[i]
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
		case strings.HasPrefix(a, "--event-log="):
			eventLog = strings.TrimPrefix(a, "--event-log=")
		case strings.HasPrefix(a, "--label="):
			label = strings.TrimPrefix(a, "--label=")
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
	// The sink is opened BEFORE the first removal: a pass whose lines nobody can write
	// says so at the start rather than deleting a slot nobody has a record of.
	// THE SINK IS THE FILE OR NOTHING: a run that names no file writes no structured line,
	// so this verb's stdout and stderr stay exactly the contract they were. The reason the
	// round-1 stderr fallback is not taken here is in SPEC-LOGS.md Part 6, and the one long
	// telling of it is in cmd/nova-bus/events.go.
	events, closer, err := novalog.Sink(eventLog, nil)
	if err != nil {
		fmt.Fprintf(stderr, "HYGIENE REFUSED: --event-log %s cannot be opened for append: %s\n",
			oneline.Field(eventLog), oneline.Err(err))
		return 2
	}
	if closer != nil {
		defer closer.Close()
	}
	in := pulse.HygieneInput{
		Home: home, Hostname: hostname, LogPath: logPath, CachePath: cache, DryRun: dry,
		Now: func() time.Time { return now }, Procs: hygieneProcs, Disk: hygieneDisk(home),
		Stdout: stdout, Stderr: stderr,
		// The bench label follows --hostname when no --label is given: an operator who
		// already told this pass what machine it is on should not have to say it twice,
		// and the HYGIENE line and the JSON line then name the same bench.
		Events: novalog.NewEmitter(events, "nova-pulse", "hygiene", novalog.BenchName(firstNonBlank(label, hostname))),
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
