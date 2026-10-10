package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/release"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
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
	return sprint.UnitDir(goos, home, a.getenv), nil
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
	dry := fs.Bool("dry-run", false, "print the unit and where it would go, and write and load nothing")
	harness := fs.String("harness", "", "the harness the seat's AI runs in (required): the push loop delivers each judgment, and the push proof, into the session through its adapter; a harness with no deliver command (claude) gets the folder adapter, each one a file written into --target")
	target := fs.String("target", "", "the session's directory, where the harness's adapter delivers (required); for the folder adapter, the directory the session watches with a Monitor, which must be there")
	session := fs.String("session", "", "the session's id, for a harness that names one (default: the adapter's newest in --target)")
	server := fs.String("server", "", "the sprint's server, `host:port` (default NOVA_SPRINT_SERVER): the unit's, and recorded in the seat beside the store login, where seat check reads it when NOVA_SPRINT_SERVER is not set")
	cs := addConfigSeatFlags(fs)
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}
	if _, _, err := cs.profile(); err != nil {
		return refuse(stderr, name, err.Error())
	}
	// the push target first: a loop that cannot reach the session is no push (pushproof.go)
	// (a dry run with no --harness still prints the unit, and says the install wants one)
	push, why := seatPushTarget(sprint.PushRecord{Name: c.actor, Harness: *harness, Target: *target, Session: *session})
	if why != "" && (!*dry || push.Harness != "") {
		return refuse(stderr, name, why+"; run: "+sprint.PushSetup(c.actor, push, push.Harness != ""))
	}
	goos := a.seatOS()
	u := sprint.SeatUnit{OS: goos, Log: *logf}
	if srv := strings.TrimSpace(*server); srv != "" {
		u.Server = srv
	} else if srv := a.server(fs); srv != "" {
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
	// A Redis unit dials in its own process. Its file carries no user and no secret
	// (SeatUnit.env), so with no login recorded for that address it crash-loops on
	// NOAUTH. A twin is refused by the unit text, which names it. A server unit does
	// not dial Redis and is installed as it is.
	if u.Redis != "" && !isTwin(u.Redis) {
		if why := a.redisUnitLogin(u.Redis); why != "" {
			return refuse(stderr, name, why+"; nothing was written")
		}
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
		if push.Harness == "" {
			fmt.Fprintf(stdout, "NOTE the install wants the seat's push target: %s\n", sprint.PushSetup(c.actor, push, false))
		}
		if err := a.installSeat(cs, u.Server, true, stdout); err != nil {
			return refuse(stderr, name, err.Error())
		}
		lp, ln, err := a.seatLinksPathCount(u.Exe)
		if err != nil {
			return refuse(stderr, name, err.Error()+"; nothing was written")
		}
		fmt.Fprintf(stdout, "SEAT LINKS DRY-RUN file=%s links=%d; nothing was written\n", oneline.Field(lp), ln)
		return 0
	}
	if code := a.recordPushTarget(u.Server, *c, push, stdout, stderr); code != 0 {
		return code
	}
	r, err := a.seatInstaller(goos, *dir).Install(u)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s FAILED: %s\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	var seat strings.Builder
	if err := a.installSeat(cs, u.Server, false, &seat); err != nil {
		fmt.Fprintf(stderr, "%s %s FAILED: the unit is installed, and %s\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	// The paths this seat reaches its tools through are recorded beside the seat
	// record: adopt repoints each one at the release it installs, and version
	// warns when one of them runs an older build (docs/SPEC-RELEASE.md, "The
	// seat's links"). The unit is already installed and loaded; a record that
	// cannot be read is not read back by a seat that never wrote one.
	lp, ln, err := a.writeSeatLinks(u.Exe)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s FAILED: the unit is installed, and the seat links were not recorded: %s\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	if c.json {
		b, _ := json.Marshal(map[string]any{"path": r.Path, "written": r.Changed, "loaded": true, "args": u.Args(), "adapter": push.AdapterName(), "links": lp, "link_count": ln, "seat": strings.Split(strings.TrimSpace(seat.String()), "\n")}) // ignored: strings and bools always encode
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintf(stdout, "SEAT INSTALL OK unit=%s written=%t loaded=true\n", oneline.Field(r.Path), r.Changed)
	fmt.Fprintf(stdout, "SEAT LINKS OK file=%s links=%d\n", oneline.Field(lp), ln)
	fmt.Fprintf(stdout, "  runs: %s\n", strings.Join(u.Args(), " "))
	if push.Adapter == sprint.AdapterFolder {
		fmt.Fprintf(stdout, "  push: %s into %s adapter=folder: each check is written there as %s<nonce> and each judgment as a file; the seat is live once the session answers the check (seat push shows it)\n", oneline.Field(push.Harness), oneline.Field(push.Target), sprint.PushProofFilePrefix)
		fmt.Fprintf(stdout, "  monitor: %s\n", sprint.FolderWatch(push.Target))
		fmt.Fprintf(stdout, "  prove: %s\n", sprint.FolderProve(push.Name, "<nonce>"))
		return 0
	}
	fmt.Fprintf(stdout, "  push: %s into %s; the seat is live once the session answers the push check with nova-sprint seat pong (seat push shows it)\n", oneline.Field(push.Harness), oneline.Field(push.Target))
	fmt.Fprint(stdout, seat.String())
	return 0
}

// redisUnitLogin is why a Redis seat unit is not installed, "" when a recorded
// login matches addr. A shell user with no matching login is unitLogin's sentence.
// No shell user is seat login --check's sentence. Nothing here is copied into the unit.
func (a *app) redisUnitLogin(addr string) string {
	if why := a.unitLogin(addr); why != "" {
		return why
	}
	l, ok, err := a.recordedLogin()
	if err != nil {
		return err.Error()
	}
	if ok && l.Redis == addr {
		return ""
	}
	file := a.loginFile
	if file == nil {
		file = a.defaultLoginFile
	}
	path, err := file()
	if err != nil {
		return err.Error()
	}
	return noSeatLogin(path)
}

// recordPushTarget writes the push record seat install was given, on the sprint's
// server when there is one (--server, else NOVA_SPRINT_SERVER), else on the
// store: the push loop reads it to reach the session.
func (a *app) recordPushTarget(srv string, c common, push sprint.PushRecord, stdout, stderr io.Writer) int {
	const name = "seat install"
	words := []string{"push", "--actor", push.Name, "--harness", push.Harness, "--target", push.Target} // the server derives the adapter again
	if push.Session != "" {
		words = append(words, "--session", push.Session)
	}
	if srv != "" {
		res, err := a.ask(context.Background(), srv, []string{"seat"}, words)
		if err != nil {
			return a.unanswered(name, srv, err, stderr)
		}
		if res.Code == 2 {
			return refuse(stderr, name, "the server refused the push record: "+strings.TrimSpace(res.Stderr))
		}
		return 0
	}
	st, err := a.store(c)
	if err != nil {
		return refuse(stderr, name, err.Error()+"; nothing was written")
	}
	if err := writePush(context.Background(), st, push); err != nil {
		return a.readFailed(name, err, stderr)
	}
	return 0
}

func (a *app) cmdSeatUninstall(args []string, stdout, stderr io.Writer) int {
	const name = "seat uninstall"
	fs, c := a.verbSetup(name)
	dir := fs.String("dir", "", "the directory the unit was written into (default: as seat install's)")
	dry := fs.Bool("dry-run", false, "say which unit would be unloaded and removed, and unload and remove nothing")
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
	if *dry {
		file := sprint.SeatUnitFile(goos)
		if file == "" {
			return refuse(stderr, name, "seat uninstall removes a launchd agent (macOS) or a systemd user unit (Linux), and "+goos+" has neither")
		}
		path := filepath.Join(*dir, file)
		_, serr := os.Stat(path)
		there := serr == nil
		if c.json {
			b, _ := json.Marshal(map[string]any{"path": path, "present": there, "dry_run": true}) // ignored: a string and bools always encode
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		fmt.Fprintf(stdout, "SEAT UNINSTALL DRY-RUN unit=%s present=%t; nothing was unloaded or removed\n", oneline.Field(path), there)
		return 0
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
// unloads the unit at path on this machine, the push loop's or the friend sync loop's
// (friendsync_install.go), named by its file: launchctl in the user's gui domain on
// macOS, its label the file's name without .plist, and systemctl --user on Linux, its
// service the file's name. Under NOVA_TEST_NO_HOST it refuses: a test gives its own
// loader.
func loadSeatUnit(goos, op, path string) error { return sprint.LoadUnit(goos, op, path) }

// The paths a seat reaches its nova binaries through, recorded beside the seat's
// record (seat.json) so adopt repoints each one at the release it installs and
// version warns when one of them runs an older build (docs/SPEC-RELEASE.md, "The
// seat's links"). A seat whose wrapper ran a pinned copy was the bug this
// record exists for.

// seatLinksBeside is the links seat install records: the wrapper's binary path,
// and every link beside it that names a nova binary.
func seatLinksBeside(exe string) release.SeatLinks {
	dir := filepath.Dir(exe)
	r := release.SeatLinks{Paths: []string{dir}, Links: []release.SeatLink{{Tool: "nova-sprint", Path: exe}}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return r
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		if p == exe {
			continue
		}
		target, err := os.Readlink(p)
		if err != nil {
			continue
		}
		tool := strings.TrimSuffix(filepath.Base(target), ".exe")
		if strings.HasPrefix(tool, "nova-") {
			r.Links = append(r.Links, release.SeatLink{Tool: tool, Path: p})
		}
	}
	return r
}

// seatLinksPath is the links record's file beside the seat's record, so the
// seat install that writes it and the version and adopt that read it agree.
func (a *app) seatLinksPath() (string, error) {
	rec, err := a.seatRecordPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(rec), release.SeatLinksFile), nil
}

// seatLinksPathCount is what seat install would record: the record's file and
// how many links it names, the version the record already holds kept. It writes
// nothing.
func (a *app) seatLinksPathCount(exe string) (string, int, error) {
	path, err := a.seatLinksPath()
	if err != nil {
		return "", 0, err
	}
	old, err := release.ReadSeatLinks(path)
	if err != nil {
		return path, 0, err
	}
	r := seatLinksBeside(exe)
	r.Server = old.Server
	return path, len(r.Links), nil
}

// writeSeatLinks records the links this seat reaches its tools through, keeping
// the version the record already holds: adopt writes that when it installs the
// release the server runs.
func (a *app) writeSeatLinks(exe string) (string, int, error) {
	path, err := a.seatLinksPath()
	if err != nil {
		return "", 0, err
	}
	old, err := release.ReadSeatLinks(path)
	if err != nil {
		return path, 0, err
	}
	r := seatLinksBeside(exe)
	r.Server = old.Server
	if err := release.WriteSeatLinks(path, r); err != nil {
		return path, 0, err
	}
	return path, len(r.Links), nil
}
