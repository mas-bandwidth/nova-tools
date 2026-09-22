package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

func cmdSprint(args []string, stdout, stderr io.Writer, now time.Time) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "nova-pulse sprint: subcommand required (one of start, pause, resume, drain, stop, status)\n")
		return 2
	}

	subverb := ""
	rest := args
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "start", "pause", "resume", "drain", "stop", "status", "prep", "run":
		subverb = strings.ToLower(strings.TrimSpace(args[0]))
		rest = args[1:]
	}

	f := newFlags("sprint")
	stateFlag := f.fs.String("state", "", "sprint lifecycle state: start|pause|resume|drain|stop|status")
	dirFlag := f.fs.String("dir", "", "sprint directory holding state and stamp files")
	queueFlag := f.fs.String("queue", "", "queue directory (used as default dir)")
	stopFlag := f.fs.String("stop", "", "path to STOP file (defaults to <dir>/STOP)")
	rootsFlag := f.fs.String("roots", "", "comma-separated worker or bench roots to inspect for active leases")
	launchedFlag := f.fs.String("launched", "", "launched cards directory to inspect for active leases")
	reasonFlag := f.fs.String("reason", "", "reason for stopping or draining sprint")
	strictFlag := f.fs.Bool("strict", true, "strict stop verification (refuses exit 1 if active leases linger)")
	forceFlag := f.fs.Bool("force", false, "force stop even if active leases linger")
	timeoutFlag := f.fs.Duration("timeout", 5*time.Minute, "timeout for drain waiting for active workers")
	pollFlag := f.fs.Duration("poll", 1*time.Second, "poll interval for drain waiting for active workers")

	if !f.parseAny(rest, stderr) {
		return 2
	}

	if subverb == "" {
		if f.fs.NArg() > 0 {
			subverb = strings.ToLower(strings.TrimSpace(f.fs.Arg(0)))
		} else if *stateFlag != "" {
			subverb = strings.ToLower(strings.TrimSpace(*stateFlag))
		}
	}

	if subverb == "" {
		fmt.Fprintf(stderr, "nova-pulse sprint: subcommand required (one of start, pause, resume, drain, stop, status)\n")
		return 2
	}

	dir := strings.TrimSpace(*dirFlag)
	if dir == "" {
		dir = strings.TrimSpace(*queueFlag)
	}
	if dir == "" {
		dir = "."
	}

	var roots []string
	if strings.TrimSpace(*rootsFlag) != "" {
		for _, r := range strings.Split(*rootsFlag, ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				roots = append(roots, r)
			}
		}
	}
	if len(roots) == 0 {
		candidateRoots := filepath.Join(dir, "roots")
		if info, err := os.Stat(candidateRoots); err == nil && info.IsDir() {
			roots = append(roots, candidateRoots)
		}
	}

	launched := strings.TrimSpace(*launchedFlag)
	if launched == "" {
		launched = filepath.Join(dir, "launched")
	}

	leaseChecker := &pulse.DirLeaseChecker{
		Roots:    roots,
		Launched: launched,
		Now:      func() time.Time { return now },
	}

	stopPath := strings.TrimSpace(*stopFlag)

	switch subverb {
	case "prep":
		return pulse.SprintPrep(pulse.SprintPrepInput{
			Dir:    dir,
			Now:    func() time.Time { return now },
			Stdout: stdout,
			Stderr: stderr,
		})
	case "start", "run":
		return pulse.SprintStart(pulse.SprintStartInput{
			Dir:      dir,
			StopFile: stopPath,
			Now:      func() time.Time { return now },
			Stdout:   stdout,
			Stderr:   stderr,
		})
	case "pause":
		return pulse.SprintPause(pulse.SprintPauseInput{
			Dir:      dir,
			StopFile: stopPath,
			Stdout:   stdout,
			Stderr:   stderr,
		})
	case "resume":
		return pulse.SprintResume(pulse.SprintResumeInput{
			Dir:      dir,
			StopFile: stopPath,
			Stdout:   stdout,
			Stderr:   stderr,
		})
	case "drain":
		return pulse.SprintDrain(pulse.SprintDrainInput{
			Dir:          dir,
			StopFile:     stopPath,
			Leases:       leaseChecker,
			PollInterval: *pollFlag,
			Timeout:      *timeoutFlag,
			Now:          func() time.Time { return now },
			Stdout:       stdout,
			Stderr:       stderr,
		})
	case "stop":
		strict := *strictFlag && !*forceFlag
		return pulse.SprintStop(pulse.SprintStopInput{
			Dir:      dir,
			StopFile: stopPath,
			Leases:   leaseChecker,
			Strict:   strict,
			Reason:   strings.TrimSpace(*reasonFlag),
			Now:      func() time.Time { return now },
			Stdout:   stdout,
			Stderr:   stderr,
		})
	case "status":
		return pulse.SprintStatus(pulse.SprintStatusInput{
			Dir:    dir,
			Leases: leaseChecker,
			Stdout: stdout,
			Stderr: stderr,
		})
	default:
		fmt.Fprintf(stderr, "nova-pulse sprint: unknown subcommand %q (pass start, pause, resume, drain, stop, status)\n", subverb)
		return 2
	}
}
