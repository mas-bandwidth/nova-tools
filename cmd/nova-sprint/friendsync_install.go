package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// friend sync install and friend sync uninstall (docs/SPEC-SPRINT.md, "The friend sync
// loop as a service"; the owner, 2026-10-05: "We need to get away from these one shot
// shell scripts"): the friend sync loop, friend sync --every <d>, as the tool's own
// service on the machine they are typed on, as seat install runs the push loop
// (seatinstall.go), in place of a zsh while-loop that read the seat with where --json
// and jq. The unit's text and its install are sprint.FriendSyncUnit and
// sprint.SeatInstaller; here are the machine's facts (its OS, the binary, the home,
// the stores this verb was given). They are words of friend sync, dispatched by it
// (friends.go), and like it never the server's.

func init() {
	verbExit["friend sync install"] = "exit codes: 0 done (the unit written or kept, and loaded), 1 the unit did not write or load (FAILED names it), 2 usage or a unit it refuses (no --every, no Redis store, the twin, a --pg that carries a password, an --actor)"
	verbExit["friend sync uninstall"] = "exit codes: 0 done (removed, or no unit there), 1 the unit did not unload or remove (FAILED names it), 2 usage"
	verbEffect["friend sync install"] = "local write: writes the friend sync loop's unit (friend sync --every <d>, acting as the seat each pass) into --dir and loads it with launchctl (macOS) or systemctl --user (Linux); --dry-run prints it and writes nothing"
	verbEffect["friend sync uninstall"] = "local write: unloads the friend sync loop's unit and removes its file from --dir; --dry-run names the unit and unloads and removes nothing"
}

func (a *app) cmdFriendSyncInstall(args []string, stdout, stderr io.Writer) int {
	const name = "friend sync install"
	fs, c := a.verbSetup(name)
	every := fs.Duration("every", 0, "the time between the loop's passes, above zero (required); the unit runs friend sync --every with it")
	pg := fs.String("pg", "", "the config store the loop reads, Postgres postgres://user@host:port/db with no password (else NOVA_PG_DSN); the unit names it, and the password comes from the variable NOVA_PG_PASSWORD_ENV names in the service's environment")
	root := fs.String("root", "", "the directory the friends' working directories are under, an absolute path (default: none in the unit, so the service's HOME)")
	dir := fs.String("dir", "", "the directory the unit is written into (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)")
	logf := fs.String("log", "", "the file the loop's lines go to, macOS (default: ~/Library/Logs/nova-sprint-friend-sync.log); on Linux they are in the journal")
	dry := fs.Bool("dry-run", false, "print the unit and where it would go, and write and load nothing")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}
	if (verbArgs{fs: fs}).given("actor") {
		return refuse(stderr, name, "the loop acts as the sprint's coordinator seat each pass, so a seat that moves takes it along, and the unit names no actor: leave out --actor; nothing was written")
	}
	goos := a.seatOS()
	u := sprint.FriendSyncUnit{OS: goos, Every: *every, Redis: strings.TrimSpace(c.redis), Root: *root, Log: *logf}
	p := *pg
	if p == "" {
		p = a.getenv(config.EnvPG)
	}
	if p != "" {
		// the line the unit carries is a flag a ps reads: a password in it is refused
		// as nova-config refuses one, quoting nothing
		if _, err := config.ResolveDSN(p, func(string) string { return "" }); err != nil {
			return refuse(stderr, name, err.Error()+"; nothing was written")
		}
		u.PG = p
	}
	for _, k := range sprint.FriendSyncEnv {
		if v := a.getenv(k); v != "" {
			u.Env = append(u.Env, [2]string{k, v})
		}
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
		u.Log = filepath.Join(home, "Library", "Logs", "nova-sprint-friend-sync.log")
	}
	text, err := u.Text()
	if err != nil {
		return refuse(stderr, name, err.Error()+"; nothing was written")
	}
	path := filepath.Join(*dir, sprint.FriendSyncUnitFile(goos))
	if *dry {
		if c.json {
			b, _ := json.Marshal(map[string]any{"path": path, "unit": text, "args": u.Args(), "dry_run": true}) // ignored: strings always encode
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		fmt.Fprintf(stdout, "FRIEND-SYNC INSTALL DRY-RUN unit=%s; nothing was written or loaded\n%s", oneline.Field(path), text)
		return 0
	}
	r, err := a.seatInstaller(goos, *dir).InstallFriendSync(u)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s FAILED: %s\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	if c.json {
		b, _ := json.Marshal(map[string]any{"path": r.Path, "written": r.Changed, "loaded": true, "args": u.Args()}) // ignored: strings and bools always encode
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintf(stdout, "FRIEND-SYNC INSTALL OK unit=%s written=%t loaded=true\n", oneline.Field(r.Path), r.Changed)
	fmt.Fprintf(stdout, "  runs: %s\n", strings.Join(u.Args(), " "))
	return 0
}

func (a *app) cmdFriendSyncUninstall(args []string, stdout, stderr io.Writer) int {
	const name = "friend sync uninstall"
	fs, c := a.verbSetup(name)
	dir := fs.String("dir", "", "the directory the unit was written into (default: as friend sync install's)")
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
		file := sprint.FriendSyncUnitFile(goos)
		if file == "" {
			return refuse(stderr, name, "friend sync uninstall removes a launchd agent (macOS) or a systemd user unit (Linux), and "+goos+" has neither")
		}
		path := filepath.Join(*dir, file)
		_, serr := os.Stat(path)
		there := serr == nil
		if c.json {
			b, _ := json.Marshal(map[string]any{"path": path, "present": there, "dry_run": true}) // ignored: a string and bools always encode
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		fmt.Fprintf(stdout, "FRIEND-SYNC UNINSTALL DRY-RUN unit=%s present=%t; nothing was unloaded or removed\n", oneline.Field(path), there)
		return 0
	}
	r, err := a.seatInstaller(goos, *dir).UninstallFriendSync(goos)
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
		fmt.Fprintf(stdout, "FRIEND-SYNC UNINSTALL OK unit=%s removed=false: no unit there; nothing was changed\n", oneline.Field(r.Path))
		return 0
	}
	fmt.Fprintf(stdout, "FRIEND-SYNC UNINSTALL OK unit=%s removed=true\n", oneline.Field(r.Path))
	return 0
}
