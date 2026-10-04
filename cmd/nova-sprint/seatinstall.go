package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// seat install and seat uninstall (docs/SPEC-SPRINT.md, "Handing over the seat"; the
// owner, 2026-10-04: what is manual needs a verb): the seat's push loop, inbox --wait
// --push seat, as the tool's own service on this machine, in place of a unit written by
// hand. The unit's text and its install are sprint.SeatUnit and sprint.SeatInstaller;
// here are the machine's facts (its OS, the binary, the home, the store this verb was
// given) and the loader, launchctl or systemctl --user. Neither verb is the server's
// (notServed): a unit is installed where the verb is typed.

// seatOS is the OS the unit is written for: the test's, else this machine's.
func (a *app) seatOS() string {
	if a.goos != "" {
		return a.goos
	}
	return runtime.GOOS
}

// seatDir is where the unit goes when --dir names nowhere: the user's LaunchAgents on
// macOS, the systemd user directory on Linux.
func (a *app) seatDir(goos string) (string, error) {
	home, err := a.home()
	if err != nil {
		return "", err
	}
	if goos == "linux" {
		if x := a.getenv("XDG_CONFIG_HOME"); x != "" {
			return filepath.Join(x, "systemd", "user"), nil
		}
		return filepath.Join(home, ".config", "systemd", "user"), nil
	}
	return filepath.Join(home, "Library", "LaunchAgents"), nil
}

// seatInstaller is the installer into dir, its loader the test's when one is set.
func (a *app) seatInstaller(goos, dir string) sprint.SeatInstaller {
	load := a.seatLoad
	if load == nil {
		load = loadSeatUnit
	}
	return sprint.SeatInstaller{Dir: dir,
		Load:   func(p string) error { return load(goos, "load", p) },
		Unload: func(p string) error { return load(goos, "unload", p) }}
}

func (a *app) cmdSeatInstall(args []string, stdout, stderr io.Writer) int {
	const name = "seat install"
	fs, c := a.verbSetup(name)
	dir := fs.String("dir", "", "the directory the unit is written into (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)")
	logf := fs.String("log", "", "the file the loop's lines go to, macOS (default: ~/Library/Logs/nova-sprint-seat-push.log); on Linux they are in the journal")
	busAddr := fs.String("bus", a.getenv(sprint.BusEnv), "the bus store the loop pushes each judgment and note over to the seat, host:port (else "+sprint.BusEnv+")")
	dry := fs.Bool("dry-run", false, "print the unit and where it would go, and write and load nothing")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}
	goos := a.seatOS()
	u := sprint.SeatUnit{OS: goos, Log: *logf, Bus: strings.TrimSpace(*busAddr)}
	if srv := a.server(fs); srv != "" {
		u.Server = srv
	} else {
		u.Redis = strings.TrimSpace(c.redis)
	}
	exe := a.executable
	if exe == nil {
		exe = os.Executable
	}
	if u.Exe, err = exe(); err != nil {
		return refuse(stderr, name, "the path of this nova-sprint cannot be read: "+err.Error()+"; nothing was written")
	}
	if *dir == "" {
		if *dir, err = a.seatDir(goos); err != nil {
			return refuse(stderr, name, "--dir names no directory and the home cannot be read: "+err.Error())
		}
	}
	if u.Log == "" && goos == "darwin" {
		home, err := a.home()
		if err != nil {
			return refuse(stderr, name, "--log names no file and the home cannot be read: "+err.Error())
		}
		u.Log = filepath.Join(home, "Library", "Logs", "nova-sprint-seat-push.log")
	}
	text, err := u.Text()
	if err != nil {
		return refuse(stderr, name, err.Error()+"; nothing was written")
	}
	path := filepath.Join(*dir, sprint.SeatUnitFile(goos))
	if *dry {
		if c.json {
			b, _ := json.Marshal(map[string]any{"path": path, "unit": text, "args": u.Args(), "dry_run": true}) // ignored: strings always encode
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		fmt.Fprintf(stdout, "SEAT INSTALL DRY-RUN unit=%s; nothing was written or loaded\n%s", oneline.Field(path), text)
		return 0
	}
	r, err := a.seatInstaller(goos, *dir).Install(u)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s FAILED: %s\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	if c.json {
		b, _ := json.Marshal(map[string]any{"path": r.Path, "written": r.Changed, "loaded": true, "args": u.Args()}) // ignored: strings and bools always encode
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintf(stdout, "SEAT INSTALL OK unit=%s written=%t loaded=true\n", oneline.Field(r.Path), r.Changed)
	fmt.Fprintf(stdout, "  runs: %s\n", strings.Join(u.Args(), " "))
	return 0
}

func (a *app) cmdSeatUninstall(args []string, stdout, stderr io.Writer) int {
	const name = "seat uninstall"
	fs, c := a.verbSetup(name)
	dir := fs.String("dir", "", "the directory the unit was written into (default: as seat install's)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}
	goos := a.seatOS()
	if *dir == "" {
		if *dir, err = a.seatDir(goos); err != nil {
			return refuse(stderr, name, "--dir names no directory and the home cannot be read: "+err.Error())
		}
	}
	r, err := a.seatInstaller(goos, *dir).Uninstall(goos)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s FAILED: %s\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	if c.json {
		b, _ := json.Marshal(map[string]any{"path": r.Path, "removed": r.Changed}) // ignored: a string and a bool always encode
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	if !r.Changed {
		fmt.Fprintf(stdout, "SEAT UNINSTALL OK unit=%s removed=false: no unit there; nothing was changed\n", oneline.Field(r.Path))
		return 0
	}
	fmt.Fprintf(stdout, "SEAT UNINSTALL OK unit=%s removed=true\n", oneline.Field(r.Path))
	return 0
}

// loadSeatUnit loads (op load: unloaded first, so a changed unit is read again) or
// unloads the push loop's unit on this machine: launchctl in the user's gui domain on
// macOS, systemctl --user on Linux. Under NOVA_TEST_NO_HOST it refuses: a test gives
// its own loader.
func loadSeatUnit(goos, op, path string) error {
	if os.Getenv("NOVA_TEST_NO_HOST") != "" {
		return errors.New("NOVA_TEST_NO_HOST is set: no service is loaded or unloaded on this machine")
	}
	ctx := context.Background()
	run := func(name string, args ...string) error {
		cmd, cancel := subproc.Command(ctx, subproc.Tool, name, args...)
		defer cancel()
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if goos == "linux" {
		if op == "unload" {
			return run("systemctl", "--user", "disable", "--now", sprint.SeatService)
		}
		if err := run("systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		if err := run("systemctl", "--user", "enable", sprint.SeatService); err != nil {
			return err
		}
		return run("systemctl", "--user", "restart", sprint.SeatService)
	}
	service := "gui/" + strconv.Itoa(os.Getuid()) + "/" + sprint.SeatLabel
	loaded := run("launchctl", "print", service) == nil
	if loaded {
		if err := run("launchctl", "bootout", service); err != nil {
			return err
		}
	}
	if op == "unload" {
		return nil
	}
	return run("launchctl", "bootstrap", "gui/"+strconv.Itoa(os.Getuid()), path)
}
