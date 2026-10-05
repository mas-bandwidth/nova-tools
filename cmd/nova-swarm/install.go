package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/units"
)

func init() {
	verbEffect["install"] = "local write: writes the kind's unit (the verb itself, never a wrapper) into --dir and loads it with launchctl (macOS) or systemctl --user (Linux); --dry-run writes nothing"
	verbEffect["uninstall"] = "local write: unloads the kind's unit and removes its file from --dir; --dry-run names the unit and unloads and removes nothing"
	verbExit["install"] = "exit codes: 0 done (the unit written or kept, and loaded); 1 the unit did not write or load; 2 usage, or mirror-refresh (no mirror verb yet)"
	verbExit["uninstall"] = "exit codes: 0 done (removed, or no unit there); 1 the unit did not unload or remove; 2 usage"
}

// swarmUnits is what a test stands in for: the OS the unit is written for, the home,
// the binary, and the loader. Zero is this machine. A test sets them and loads nothing
// on the host; the real loader refuses while NOVA_TEST_NO_HOST is set.
var swarmUnits struct {
	goos string
	home func() (string, error)
	exe  func() (string, error)
	load func(goos, op, path string) error
}

func unitOS() string {
	if swarmUnits.goos != "" {
		return swarmUnits.goos
	}
	return runtime.GOOS
}

func unitHome() (string, error) {
	if swarmUnits.home != nil {
		return swarmUnits.home()
	}
	return os.UserHomeDir()
}

func unitExe() (string, error) {
	if swarmUnits.exe != nil {
		return swarmUnits.exe()
	}
	return os.Executable()
}

func unitLoad(goos, op, path string) error {
	if swarmUnits.load != nil {
		return swarmUnits.load(goos, op, path)
	}
	return units.Load(goos, op, path)
}

// install and uninstall of the units nova-swarm owns (card every-unit-installed-by-a-verb).
// disk-guard is written and loaded here. mirror-refresh stays owed: nova-swarm has no
// mirror verb for that unit to run. The unit runs this binary directly. A test gives
// --dir and its own loader, so nothing is loaded on the machine the test runs on.

func swarmKinds() string { return strings.Join(units.UnitKindNames("nova-swarm"), "|") }

func cmdInstall(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		f := newFlags("install")
		f.fs.String("dir", "", "the directory the unit is written into (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)")
		f.fs.String("log", "", "the file the unit's lines go to on macOS; on Linux they are in the journal")
		f.fs.Duration("every", 15*time.Minute, "the least time between two starts")
		f.fs.Bool("dry-run", false, "print the unit and write and load nothing")
		if !f.parse(args, stderr) {
			return 2
		}
		return refuse(stderr, " install", "install wants the unit's kind first: "+swarmKinds()+"; run: nova-swarm install disk-guard --dry-run")
	}
	return installKind(args[0], args[1:], stdout, stderr)
}

func installKind(kind string, args []string, stdout, stderr io.Writer) int {
	k, ok := units.UnitKindOf(kind)
	name := "install " + kind
	if !ok {
		return refuse(stderr, " install", "no unit kind "+oneline.Escape(kind)+"; the kinds are "+swarmKinds()+"; run: nova-sprint units --check")
	}
	if !strings.HasPrefix(k.Install, "nova-swarm ") {
		return refuse(stderr, " install", "the "+k.Kind+" unit is "+k.Tool+"'s to install; run: "+k.Install)
	}
	f := newFlags("install")
	dir := f.fs.String("dir", "", "the directory the unit is written into (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)")
	logf := f.fs.String("log", "", "the file the unit's lines go to, macOS (default: ~/Library/Logs/nova-swarm-"+kind+".log); on Linux they are in the journal")
	every := f.fs.Duration("every", 15*time.Minute, "the least time between two starts, above zero (launchd ThrottleInterval, systemd RestartSec)")
	dry := f.fs.Bool("dry-run", false, "print the unit and where it would go, and write and load nothing")
	var roots, scans, caches []string
	f.fs.Var(stringListValue{&roots}, "root", "a member's or reader's root `dir` the unit's disk-guard passes (again for more)")
	f.fs.Var(stringListValue{&scans}, "scan", "a `dir` each of whose subdirectories holding slots/ is a root (again for more)")
	f.fs.Var(stringListValue{&caches}, "cache", "another Go build cache, a `dir` or a glob (again for more)")
	cacheGB := f.fs.Int("cache-max-gb", 0, "the `GiB` each Go build cache is held under, when set")
	modGB := f.fs.Int("modcache-max-gb", 0, "the `GiB` over which a module cache is emptied, when set")
	logs := f.fs.String("logs", "", "the `dir` of the loop logs to rotate, when set")
	logMB := f.fs.Int("log-max-mb", 0, "the `MiB` over which a loop log is rotated, when set")
	logKeep := f.fs.Int("log-keep", 0, "how many rotated copies of a log stay, when set")
	poolIdle := f.fs.Duration("pool-idle", 0, "how long a pool's slots must be still before it is swept, when set")
	land := f.fs.String("land", "", "the `dir` nova-sprint land keeps its clones in, when set")
	cloneAge := f.fs.Duration("clone-age", 0, "how long a land clone must be unused before it is removed, when set")
	mirrors := f.fs.String("mirrors", "", "the `dir` of the bench's mirrors, when set")
	floor := f.fs.Int("disk-floor", -1, "the free `GiB` under which the run warns, when set")
	if !f.parse(args, stderr) {
		return 2
	}
	if k.Owed != "" {
		return refuse(stderr, " "+name, k.Owed+"; nothing was written")
	}
	if *every <= 0 {
		return refuse(stderr, " "+name, "--every is the least time between two starts, above zero; nothing was written")
	}
	goos := unitOS()
	bin, err := unitExe()
	if err != nil {
		return refuse(stderr, " "+name, "the path of this nova-swarm cannot be read: "+err.Error()+"; nothing was written")
	}
	uargs := []string{bin, "disk-guard"}
	add := func(flag, v string) {
		if v != "" {
			uargs = append(uargs, flag, v)
		}
	}
	for _, r := range roots {
		add("--root", r)
	}
	for _, r := range scans {
		add("--scan", r)
	}
	for _, r := range caches {
		add("--cache", r)
	}
	if *cacheGB > 0 {
		add("--cache-max-gb", strconv.Itoa(*cacheGB))
	}
	if *modGB > 0 {
		add("--modcache-max-gb", strconv.Itoa(*modGB))
	}
	add("--logs", *logs)
	if *logMB > 0 {
		add("--log-max-mb", strconv.Itoa(*logMB))
	}
	if *logKeep > 0 {
		add("--log-keep", strconv.Itoa(*logKeep))
	}
	if *poolIdle > 0 {
		add("--pool-idle", poolIdle.String())
	}
	add("--land", *land)
	if *cloneAge > 0 {
		add("--clone-age", cloneAge.String())
	}
	add("--mirrors", *mirrors)
	if *floor >= 0 {
		add("--disk-floor", strconv.Itoa(*floor))
	}
	u := units.ServiceUnit{Kind: k, OS: goos, Args: uargs, Every: *every, Log: *logf}
	if *dir == "" {
		home, err := unitHome()
		if err != nil {
			return refuse(stderr, " "+name, "--dir names no directory and the home cannot be read: "+err.Error())
		}
		*dir = units.Dir(goos, home, os.Getenv)
	}
	if u.Log == "" && goos == "darwin" {
		home, err := unitHome()
		if err != nil {
			return refuse(stderr, " "+name, "--log names no file and the home cannot be read: "+err.Error())
		}
		u.Log = filepath.Join(home, "Library", "Logs", "nova-swarm-"+kind+".log")
	}
	text, err := u.Text()
	if err != nil {
		return refuse(stderr, " "+name, err.Error()+"; nothing was written")
	}
	head := "INSTALL " + strings.ToUpper(kind)
	path := filepath.Join(*dir, k.File(goos))
	if *dry {
		fmt.Fprintf(stdout, "%s DRY-RUN unit=%s; nothing was written or loaded\n%s", oneline.Escape(head), oneline.Field(path), text)
		return 0
	}
	in := units.Installer{Dir: *dir,
		Load:   func(p string) error { return unitLoad(goos, "load", p) },
		Unload: func(p string) error { return unitLoad(goos, "unload", p) }}
	r, err := in.Install(u)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm %s FAILED: %s\n", oneline.Escape(name), oneline.Escape(err.Error()))
		return 1
	}
	fmt.Fprintf(stdout, "%s OK unit=%s written=%t loaded=true\n", oneline.Escape(head), oneline.Field(r.Path), r.Changed)
	fmt.Fprintf(stdout, "  runs: %s\n", oneline.Escape(strings.Join(u.Args, " ")))
	return 0
}

func cmdUninstall(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		f := newFlags("uninstall")
		f.fs.String("dir", "", "the directory the unit was written into (default: as install's)")
		f.fs.Bool("dry-run", false, "say which unit would be unloaded and removed, and unload and remove nothing")
		if !f.parse(args, stderr) {
			return 2
		}
		return refuse(stderr, " uninstall", "uninstall wants the unit's kind first: "+swarmKinds()+"; run: nova-swarm uninstall disk-guard --dry-run")
	}
	kind := args[0]
	k, ok := units.UnitKindOf(kind)
	name := "uninstall " + kind
	if !ok {
		return refuse(stderr, " uninstall", "no unit kind "+oneline.Escape(kind)+"; the kinds are "+swarmKinds())
	}
	if !strings.HasPrefix(k.Install, "nova-swarm ") {
		return refuse(stderr, " uninstall", "the "+k.Kind+" unit is "+k.Tool+"'s to uninstall; run: "+strings.Replace(k.Install, "install", "uninstall", 1))
	}
	f := newFlags("uninstall")
	dir := f.fs.String("dir", "", "the directory the unit was written into (default: as install's)")
	dry := f.fs.Bool("dry-run", false, "say which unit would be unloaded and removed, and unload and remove nothing")
	if !f.parse(args[1:], stderr) {
		return 2
	}
	goos := unitOS()
	if *dir == "" {
		home, err := unitHome()
		if err != nil {
			return refuse(stderr, " "+name, "--dir names no directory and the home cannot be read: "+err.Error())
		}
		*dir = units.Dir(goos, home, os.Getenv)
	}
	head := "UNINSTALL " + strings.ToUpper(kind)
	file := k.File(goos)
	if file == "" {
		return refuse(stderr, " "+name, name+" removes a launchd agent (macOS) or a systemd user unit (Linux), and "+goos+" has neither")
	}
	path := filepath.Join(*dir, file)
	if *dry {
		_, serr := os.Stat(path)
		fmt.Fprintf(stdout, "%s DRY-RUN unit=%s present=%t; nothing was unloaded or removed\n", oneline.Escape(head), oneline.Field(path), serr == nil)
		return 0
	}
	in := units.Installer{Dir: *dir,
		Load:   func(p string) error { return unitLoad(goos, "load", p) },
		Unload: func(p string) error { return unitLoad(goos, "unload", p) }}
	r, err := in.Uninstall(k, goos)
	if err != nil {
		fmt.Fprintf(stderr, "nova-swarm %s FAILED: %s\n", oneline.Escape(name), oneline.Escape(err.Error()))
		return 1
	}
	if !r.Changed {
		fmt.Fprintf(stdout, "%s OK unit=%s removed=false: no unit there; nothing was changed\n", oneline.Escape(head), oneline.Field(r.Path))
		return 0
	}
	fmt.Fprintf(stdout, "%s OK unit=%s removed=true\n", oneline.Escape(head), oneline.Field(r.Path))
	return 0
}
