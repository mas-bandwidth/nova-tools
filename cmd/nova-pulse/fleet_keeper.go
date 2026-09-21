package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// keeperLaunchctlRunner is the injected runner for tests. When nil, pulse.FleetKeeper
// defaults to the real execLaunchctlRunner.
var keeperLaunchctlRunner pulse.LaunchctlRunner

func cmdFleetKeeper(args []string, stdout, stderr io.Writer) int {
	actionFromPositional := ""
	rest := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub := strings.ToLower(args[0])
		switch sub {
		case "install", "inspect", "status", "unload", "generate":
			actionFromPositional = sub
			rest = args[1:]
		default:
			fmt.Fprintf(stderr, "nova-pulse fleet keeper: unknown action %q (valid actions are inspect, install, status, unload, generate; run: nova-pulse help)\n", args[0])
			return 2
		}
	}

	f := newFlags("fleet keeper")
	unit := f.fs.String("unit", "all", "")
	installLaunchd := f.fs.Bool("install-launchd", false, "")
	inspect := f.fs.Bool("inspect", false, "")
	status := f.fs.Bool("status", false, "")
	unload := f.fs.Bool("unload", false, "")
	generate := f.fs.Bool("generate", false, "")
	plistDir := f.fs.String("dir", "", "")
	workDir := f.fs.String("work-dir", "", "")
	binDir := f.fs.String("bin-dir", "", "")
	logDir := f.fs.String("log-dir", "", "")
	labelPrefix := f.fs.String("label-prefix", "", "")
	pathEnv := f.fs.String("path-env", "", "")
	ghConfig := f.fs.String("gh-config", "", "")
	dryRun := f.fs.Bool("dry-run", false, "")

	if !f.parse(rest, stderr) {
		return 2
	}

	action := actionFromPositional
	if *installLaunchd {
		action = "install"
	} else if *inspect {
		action = "inspect"
	} else if *status {
		action = "status"
	} else if *unload {
		action = "unload"
	} else if *generate {
		action = "generate"
	}

	if action == "" {
		return refuse(stderr, " fleet keeper", "an action is required (--install-launchd, --inspect, --status, --unload, --generate)")
	}

	cfg := pulse.KeeperLaunchdConfig{
		WorkDir:     *workDir,
		BinDir:      *binDir,
		LogDir:      *logDir,
		LabelPrefix: *labelPrefix,
		PlistDir:    *plistDir,
		PathEnv:     *pathEnv,
		GHConfigDir: *ghConfig,
	}

	return pulse.FleetKeeper(pulse.FleetKeeperInput{
		Action: action,
		Unit:   *unit,
		Config: cfg,
		Runner: keeperLaunchctlRunner,
		DryRun: *dryRun,
		Stdout: stdout,
		Stderr: stderr,
	})
}
