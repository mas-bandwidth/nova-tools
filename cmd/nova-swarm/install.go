package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// install and uninstall (card every-unit-installed-by-a-verb): the machine's upkeep a
// running sprint needs, as a service of this machine (a launchd agent on macOS, a
// systemd user unit on Linux, kept alive and started again at login) in place of a
// bash loop written by hand. disk-guard is one pass of nova-swarm disk-guard every
// --every (the unit's least time between two starts); mirror-refresh waits on a
// nova-swarm mirror verb. The unit's text, its install and its loader are
// internal/sprint's (sprint.Unit, sprint.SeatInstaller, sprint.LoadUnit), shared with
// nova-sprint install and nova-redis install, and nova-sprint units --check names
// these units installed, missing or different.

// unitHost is the machine a unit is installed on: its OS, the home, this binary's
// path, the unit's loader and the environment. A test hands its own.
type unitHost struct {
	goos   string
	home   func() (string, error)
	exe    func() (string, error)
	load   func(goos, op, path string) error
	getenv func(string) string
}

func realUnitHost() unitHost {
	return unitHost{goos: runtime.GOOS, home: os.UserHomeDir, exe: os.Executable, load: sprint.LoadUnit, getenv: os.Getenv}
}

func (h unitHost) installer(dir string) sprint.SeatInstaller {
	return sprint.SeatInstaller{Dir: dir,
		Load:   func(p string) error { return h.load(h.goos, "load", p) },
		Unload: func(p string) error { return h.load(h.goos, "unload", p) }}
}

// swarmKind is the kind install or uninstall was given first, refused (code 2) when it
// names none of nova-swarm's.
func swarmKind(verb string, args []string, stderr io.Writer) (sprint.UnitKind, int) {
	kinds := sprint.UnitKindNames("nova-swarm")
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return sprint.UnitKind{}, refuse(stderr, " "+verb, verb+" wants the unit's kind first: "+strings.Join(kinds, "|")+"; run: nova-swarm "+verb+" disk-guard --dry-run")
	}
	k, ok := sprint.UnitKindOf(args[0])
	if !ok || !slices.Contains(kinds, k.Kind) {
		why := "no unit kind " + args[0] + " of nova-swarm's; its kinds are " + strings.Join(kinds, "|")
		if ok {
			why = "the " + k.Kind + " unit is " + k.Tool + "'s to " + verb + "; run: " + strings.Replace(k.Install, "install", verb, 1)
		}
		return k, refuse(stderr, " "+verb, why)
	}
	return k, 0
}

func cmdInstall(args []string, stdout, stderr io.Writer) int {
	return installUnit(realUnitHost(), args, stdout, stderr)
}

func cmdUninstall(args []string, stdout, stderr io.Writer) int {
	return uninstallUnit(realUnitHost(), args, stdout, stderr)
}

// installUnit is install <kind> [flags] [-- <the verb's own flags>]: the unit runs
// nova-swarm <verb> with the words after --.
func installUnit(h unitHost, args []string, stdout, stderr io.Writer) int {
	k, code := swarmKind("install", args, stderr)
	if code != 0 {
		return code
	}
	name := "install " + k.Kind
	own, pass := args[1:], []string(nil)
	if i := slices.Index(own, "--"); i >= 0 {
		own, pass = own[:i], own[i+1:]
	}
	f := newFlags(name)
	every := f.fs.Duration("every", 15*time.Minute, "the least time between two passes, a `duration` above zero (default 15m): the unit runs the verb again this long after it ends")
	units := f.fs.String("units", "", "the `dir` the unit is written into (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)")
	logf := f.fs.String("log", "", "the `file` the unit's lines go to, macOS (default: ~/Library/Logs/nova-swarm-"+k.Kind+".log); on Linux they are in the journal")
	dry := f.fs.Bool("dry-run", false, "print the unit and where it would go, and write and load nothing")
	if !f.parse(own, stderr) {
		return 2
	}
	if k.Kind == "mirror-refresh" {
		return refuse(stderr, " "+name, "nova-swarm has no mirror verb for the unit to run yet (card mirror-refresh-verb: nova-swarm mirror --dir <dir> --repos <a,b> --every <duration>); nothing was written")
	}
	if *every <= 0 {
		f.add("--every is the least time between two passes, above zero, got " + every.String())
	}
	if f.refused(stderr) {
		return 2
	}
	bin, err := h.exe()
	if err != nil {
		return refuse(stderr, " "+name, "the path of this nova-swarm cannot be read: "+err.Error()+"; nothing was written")
	}
	home, herr := h.home()
	u := sprint.Unit{Kind: k, OS: h.goos, Log: *logf, Every: *every, Args: append([]string{bin, "disk-guard"}, pass...)}
	if u.Log == "" && h.goos == "darwin" {
		if herr != nil {
			return refuse(stderr, " "+name, "--log names no file and the home cannot be read: "+herr.Error())
		}
		u.Log = filepath.Join(home, "Library", "Logs", "nova-swarm-"+k.Kind+".log")
	}
	text, err := u.Text()
	if err != nil {
		return refuse(stderr, " "+name, err.Error()+"; nothing was written")
	}
	dir := *units
	if dir == "" {
		if herr != nil {
			return refuse(stderr, " "+name, "--units names no directory and the home cannot be read: "+herr.Error())
		}
		dir = sprint.UnitDir(h.goos, home, h.getenv)
	}
	head := strings.ToUpper(name)
	if *dry {
		fmt.Fprintf(stdout, "%s DRY-RUN unit=%s; nothing was written or loaded\n%s", head, oneline.Field(filepath.Join(dir, k.File(h.goos))), text)
		return 0
	}
	r, err := h.installer(dir).InstallUnit(u)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm %s FAILED: %s\n", name, oneline.Escape(err.Error()))
		return 1
	}
	fmt.Fprintf(stdout, "%s OK unit=%s written=%t loaded=true\n  runs: %s\n", head, oneline.Field(r.Path), r.Changed, strings.Join(u.Args, " "))
	return 0
}

func uninstallUnit(h unitHost, args []string, stdout, stderr io.Writer) int {
	k, code := swarmKind("uninstall", args, stderr)
	if code != 0 {
		return code
	}
	name := "uninstall " + k.Kind
	f := newFlags(name)
	units := f.fs.String("units", "", "the `dir` the unit was written into (default: as install's)")
	dry := f.fs.Bool("dry-run", false, "say which unit would be unloaded and removed, and unload and remove nothing")
	if !f.parse(args[1:], stderr) {
		return 2
	}
	dir := *units
	if dir == "" {
		home, err := h.home()
		if err != nil {
			return refuse(stderr, " "+name, "--units names no directory and the home cannot be read: "+err.Error())
		}
		dir = sprint.UnitDir(h.goos, home, h.getenv)
	}
	head := strings.ToUpper(name)
	if *dry {
		file := k.File(h.goos)
		if file == "" {
			return refuse(stderr, " "+name, name+" removes a launchd agent (macOS) or a systemd user unit (Linux), and "+h.goos+" has neither")
		}
		path := filepath.Join(dir, file)
		_, serr := os.Stat(path)
		fmt.Fprintf(stdout, "%s DRY-RUN unit=%s present=%t; nothing was unloaded or removed\n", head, oneline.Field(path), serr == nil)
		return 0
	}
	r, err := h.installer(dir).UninstallUnit(k, h.goos)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm %s FAILED: %s\n", name, oneline.Escape(err.Error()))
		return 1
	}
	fmt.Fprintf(stdout, "%s OK unit=%s removed=%t\n", head, oneline.Field(r.Path), r.Changed)
	return 0
}
