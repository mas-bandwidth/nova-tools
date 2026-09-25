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

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

var (
	hygieneProcs pulse.HygieneProcs = pulse.OSProcs{}
	hygieneDisk                     = func(home string) pulse.HygieneDisk { return pulse.OSDisk{Home: home} }

	// The two doors of --lane-dirs: the git reads over a checkout and the forge
	// read over a branch. Tests replace both, so no test of the lane sweep runs
	// git or reaches the network.
	hygieneGit pulse.LaneGit      = pulse.OSLaneGit{Timeout: hygieneLaneTimeout}
	hygienePRs pulse.LanePRSource = pulse.GHLanePRs{Timeout: hygieneLaneTimeout}
)

// hygieneLaneTimeout bounds every git and gh read the lane sweep makes. Glenn's
// two-minute rule: anything we call out to that costs real time answers well
// inside a minute or is not waited on.
const hygieneLaneTimeout = 30 * time.Second

// The runner `_diag` prune's defaults under the names their flags carry, so a
// change to either is a change to one line and the test that pins it.
const (
	diagDaysDefault     = pulse.HygieneDiagDaysDefault
	diagMaxBytesDefault = pulse.HygieneDiagMaxBytesDefault
)

const hygieneUsage = `nova-pulse hygiene run                     --home <dir> [--dry-run] [--hostname <name>]
                                          [--diag-days <n>] [--diag-max-bytes <n>]
nova-pulse hygiene reap <slot>            --home <dir>
nova-pulse hygiene delete-job <slot> <job> --home <dir>
nova-pulse hygiene delete-slot <slot>     --home <dir>
nova-pulse hygiene drop-cache             --home <dir>
nova-pulse hygiene log [n]                --home <dir>
nova-pulse hygiene --lane-dirs <root>     [--dry-run] [--older-than <n>d] [--max <n>]`

// hygieneOlderThanDays reads --older-than, which has ONE spelling: a whole
// number of days with a `d` suffix, at least 1. An empty value is no window at
// all and is legal. Hours, weeks and a bare number are refused rather than
// guessed at, because a sweep that removes directories may not have to be read
// twice to know what it did.
func hygieneOlderThanDays(v string) (int, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, true
	}
	digits, ok := strings.CutSuffix(v, "d")
	if !ok || digits == "" {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// hygieneFlagValue reads a flag's value in either spelling, --name <v> or
// --name=<v>, advancing i past a separate value. It answers false when a
// separate value is missing, which is a refusal and never a default.
func hygieneFlagValue(args []string, i *int, name string) (string, bool) {
	a := args[*i]
	if v, ok := strings.CutPrefix(a, name+"="); ok {
		return v, true
	}
	if *i+1 >= len(args) {
		return "", false
	}
	*i++
	return args[*i], true
}

func hygieneRefuse(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "nova-pulse hygiene: %s; run: nova-pulse help\n", what)
	return 2
}

// cmdHygiene parses the subcommand and the flags that follow it. Every path
// hangs under --home: the two roots, the action log and the build cache.
func cmdHygiene(args []string, stdout, stderr io.Writer, now time.Time) int {
	var home, hostname, roots, logPath, cache, laneDirs, olderThan string
	dry := false
	diagDays, diagMaxBytes := diagDaysDefault, diagMaxBytesDefault
	laneMax, laneMaxSet := bounded.Default, false
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			fmt.Fprintln(stdout, hygieneUsage)
			return 0
		case a == "--dry-run":
			dry = true
		case a == "--lane-dirs" || strings.HasPrefix(a, "--lane-dirs="):
			v, ok := hygieneFlagValue(args, &i, "--lane-dirs")
			if !ok {
				return hygieneRefuse(stderr, "--lane-dirs wants a value")
			}
			laneDirs = v
		case a == "--older-than" || strings.HasPrefix(a, "--older-than="):
			v, ok := hygieneFlagValue(args, &i, "--older-than")
			if !ok {
				return hygieneRefuse(stderr, "--older-than wants a value")
			}
			olderThan = v
		case a == "--max" || strings.HasPrefix(a, "--max="):
			v, ok := hygieneFlagValue(args, &i, "--max")
			if !ok {
				return hygieneRefuse(stderr, "--max wants a value")
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return hygieneRefuse(stderr, "--max wants a line ceiling of zero or more, got "+oneline.Field(v))
			}
			laneMax, laneMaxSet = n, true
		case a == "--diag-days" || strings.HasPrefix(a, "--diag-days="):
			v, ok := hygieneFlagValue(args, &i, "--diag-days")
			if !ok {
				return hygieneRefuse(stderr, "--diag-days wants a value")
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return hygieneRefuse(stderr, "--diag-days wants a whole number of days, at least 1, got "+oneline.Field(v))
			}
			diagDays = n
		case a == "--diag-max-bytes" || strings.HasPrefix(a, "--diag-max-bytes="):
			v, ok := hygieneFlagValue(args, &i, "--diag-max-bytes")
			if !ok {
				return hygieneRefuse(stderr, "--diag-max-bytes wants a value")
			}
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 1 {
				return hygieneRefuse(stderr, "--diag-max-bytes wants a byte count, at least 1, got "+oneline.Field(v))
			}
			diagMaxBytes = n
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
	// --lane-dirs is the one flag-form mode: it takes no subcommand, and the
	// two flags that belong to it mean nothing without it. A bare
	// `nova-pulse hygiene` still prints the usage and exits 2, as it always has.
	if olderThan != "" && laneDirs == "" {
		return hygieneRefuse(stderr, "--older-than only applies to --lane-dirs (pass: nova-pulse hygiene --lane-dirs <root> --older-than <n>d)")
	}
	if laneMaxSet && laneDirs == "" {
		return hygieneRefuse(stderr, "--max only applies to --lane-dirs (pass: nova-pulse hygiene --lane-dirs <root> --max <n>)")
	}
	if laneDirs != "" {
		if len(rest) > 0 {
			return hygieneRefuse(stderr, "--lane-dirs takes no subcommand, got "+oneline.Quote(rest[0])+" (pass: nova-pulse hygiene --lane-dirs <root> [--dry-run] [--older-than <n>d] [--max <n>])")
		}
		days, ok := hygieneOlderThanDays(olderThan)
		if !ok {
			return hygieneRefuse(stderr, "--older-than wants a whole number of days with a d suffix, at least 1, got "+oneline.Field(olderThan)+" (pass --older-than 2d; days are this flag's only spelling)")
		}
		return pulse.HygieneLanes(pulse.HygieneLanesInput{
			Root: laneDirs, DryRun: dry, OlderThanDays: days, Max: laneMax,
			Now: func() time.Time { return now }, Git: hygieneGit, PRs: hygienePRs,
			Stdout: stdout, Stderr: stderr,
		})
	}
	if len(rest) == 0 {
		fmt.Fprintln(stderr, hygieneUsage)
		return 2
	}
	in := pulse.HygieneInput{
		Home: home, Hostname: hostname, LogPath: logPath, CachePath: cache, DryRun: dry,
		DiagDays: diagDays, DiagMaxBytes: diagMaxBytes,
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
