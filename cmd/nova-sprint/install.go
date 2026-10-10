package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/redisauth"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// install, uninstall and units (card every-unit-installed-by-a-verb; the owner,
// 2026-10-05: "We cannot release a product, when parts of it needed to use it
// effectively are still locked up for you only"): every unit a running
// sprint needs on its coordinator's machine is written and loaded by a verb, in place
// of a unit written by hand around nova-secrets exec, zsh or a single-instance wrapper.
// nova-sprint installs its own (server, member, seat-push, friend-sync, table); the
// store and the bus are nova-redis install's, the machine's upkeep nova-swarm
// install's (sprint.UnitKinds). Each unit runs the verb itself, and the store's
// password is read in the verb's own process from the seat login (nova-sprint seat
// login), never carried by the unit. units --check names each needed unit installed,
// missing or different, so a machine set up by a stranger is checked against what a
// sprint needs. None is the server's: a unit is installed where the verb is typed.

// installVerbs are the verbs' rows of the verb table (verbs.go appends them).
var installVerbs = []verb{
	{"install", "<server|member|seat-push|friend-sync|table> [--dir <dir>] [--log <file>] [--dry-run] (each kind's own flags are listed by install <kind> -h)", "install friend-sync --dry-run --every 15s --redis 127.0.0.1:6380", (*app).cmdInstall},
	{"uninstall", "<server|member|seat-push|friend-sync|table> [--dir <dir>] [--dry-run]", "uninstall table --dry-run --dir ./no-unit-here", (*app).cmdUninstall},
	{"units", "--check [--dir <dir>]", "units --check --dir ./no-units-here", (*app).cmdUnits},
}

// installVerbMeta gives the verbs their class, exit codes and effect, and keeps them
// off the server (verbs.go calls it once the rows are in the table).
func installVerbMeta() {
	for _, n := range []string{"install", "uninstall", "units"} {
		verbClasses[n] = classMachine
	}
	notServed = append(notServed, "install", "uninstall", "units")
	verbExit["install"] = "exit codes: 0 done (the unit written or kept, and loaded), 1 the unit did not write or load (FAILED names it), 2 usage or a unit it refuses (a kind it does not install, the twin, a store login the unit could not use, a flag missing)"
	verbExit["uninstall"] = "exit codes: 0 done (removed, or no unit there), 1 the unit did not unload or remove (FAILED names it), 2 usage"
	verbExit["units"] = "exit codes: 0 every unit a sprint needs is installed, 1 one or more is missing or different (each named with the verb that installs it), 2 usage"
	verbEffect["install"] = "local write: writes the kind's unit (the verb itself, never a wrapper) into --dir and loads it with launchctl (macOS) or systemctl --user (Linux); --dry-run prints it and writes nothing"
	verbEffect["uninstall"] = "local write: unloads the kind's unit and removes its file from --dir; --dry-run names the unit and unloads and removes nothing"
	verbEffect["units"] = "inspection: reads the unit files in --dir and names each unit a sprint needs installed, missing or different; loads and changes nothing"
}

// sprintKinds are the kinds nova-sprint install takes.
func sprintKinds() string { return strings.Join(sprint.UnitKindNames("nova-sprint"), "|") }

// kindOf is the kind install or uninstall was given as its first word: a refusal
// (code 2) when it names none, or one another tool installs.
func (a *app) kindOf(verb string, args []string, stderr io.Writer) (sprint.UnitKind, int) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		// -h with no kind is the verb's help (parse prints it); anything else is refused
		fs, _ := a.verbSetup(verb)
		fs.String("dir", "", "the directory the unit is in (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)")
		fs.Bool("dry-run", false, "print what would be done, and write, load, unload and remove nothing")
		if verb == "install" {
			fs.String("log", "", "the file the unit's lines go to, macOS (default: ~/Library/Logs/nova-sprint-<kind>.log); on Linux they are in the journal")
		}
		_, _ = parse(fs, args) // ignored: the kind is refused below whatever the flags say
		return sprint.UnitKind{}, refuse(stderr, verb, verb+" wants the unit's kind first: "+sprintKinds()+"; run: nova-sprint "+verb+" table --dry-run")
	}
	k, ok := sprint.UnitKindOf(args[0])
	if !ok {
		return k, refuse(stderr, verb, "no unit kind "+oneline.Escape(args[0])+"; the kinds are "+sprintKinds()+"; run: nova-sprint units --check")
	}
	if !strings.HasPrefix(k.Install, "nova-sprint ") {
		return k, refuse(stderr, verb, "the "+k.Kind+" unit is "+k.Tool+"'s to "+verb+"; run: "+strings.Replace(k.Install, "install", verb, 1))
	}
	return k, 0
}

func (a *app) cmdInstall(args []string, stdout, stderr io.Writer) int {
	k, code := a.kindOf("install", args, stderr)
	if code != 0 {
		return code
	}
	switch k.Kind {
	case "seat-push":
		return a.cmdSeatInstall(args[1:], stdout, stderr)
	case "friend-sync":
		return a.cmdFriendSyncInstall(args[1:], stdout, stderr)
	}
	return a.installUnit(k, args[1:], stdout, stderr)
}

func (a *app) cmdUninstall(args []string, stdout, stderr io.Writer) int {
	k, code := a.kindOf("uninstall", args, stderr)
	if code != 0 {
		return code
	}
	switch k.Kind {
	case "seat-push":
		return a.cmdSeatUninstall(args[1:], stdout, stderr)
	case "friend-sync":
		return a.cmdFriendSyncUninstall(args[1:], stdout, stderr)
	}
	name := "uninstall " + k.Kind
	fs, c := a.verbSetup(name)
	dir := fs.String("dir", "", "the directory the unit was written into (default: as install's)")
	dry := fs.Bool("dry-run", false, "say which unit would be unloaded and removed, and unload and remove nothing")
	pos, err := parse(fs, args[1:])
	if err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}
	goos := a.seatOS()
	if *dir == "" {
		if *dir, err = a.seatDir(goos); err != nil {
			return refuse(stderr, name, "--dir names no directory and the home cannot be read: "+err.Error())
		}
	}
	head := strings.ToUpper(name)
	if *dry {
		file := k.File(goos)
		if file == "" {
			return refuse(stderr, name, name+" removes a launchd agent (macOS) or a systemd user unit (Linux), and "+goos+" has neither")
		}
		path := filepath.Join(*dir, file)
		_, serr := os.Stat(path)
		if c.json {
			b, _ := json.Marshal(map[string]any{"path": path, "present": serr == nil, "dry_run": true}) // ignored: a string and bools always encode
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		fmt.Fprintf(stdout, "%s DRY-RUN unit=%s present=%t; nothing was unloaded or removed\n", head, oneline.Field(path), serr == nil)
		return 0
	}
	r, err := a.seatInstaller(goos, *dir).UninstallUnit(k, goos)
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
		fmt.Fprintf(stdout, "%s OK unit=%s removed=false: no unit there; nothing was changed\n", head, oneline.Field(r.Path))
		return 0
	}
	fmt.Fprintf(stdout, "%s OK unit=%s removed=true\n", head, oneline.Field(r.Path))
	return 0
}

// installUnit is install of server, member or table: the unit's line from the flags,
// then written and loaded as seat install writes the push loop's.
func (a *app) installUnit(k sprint.UnitKind, args []string, stdout, stderr io.Writer) int {
	name := "install " + k.Kind
	fs, c := a.verbSetup(name)
	dir := fs.String("dir", "", "the directory the unit is written into (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)")
	logf := fs.String("log", "", "the file the unit's lines go to, macOS (default: ~/Library/Logs/nova-sprint-"+k.Kind+".log); on Linux they are in the journal")
	dry := fs.Bool("dry-run", false, "print the unit and where it would go, and write and load nothing")
	var (
		listen, decideDir                    *string
		land                                 *bool
		as, server, harness, root, pass, exe *string
		out                                  *string
		every                                *time.Duration
	)
	switch k.Kind {
	case "server":
		listen = fs.String("listen", "", "the server's `address:port` on the fleet's private network (required): the unit runs nova-sprint run --listen with it")
		land = fs.Bool("land", false, "the server also lands what the readers passed (run --land)")
		decideDir = fs.String("decide", "", "the server also keeps the record of its decisions in this `dir`, an absolute path (run --decide)")
	case "member":
		as = fs.String("as", "", "the member's name on the fleet table (required): nova-swarm member --as")
		server = fs.String("server", "", "the sprint server's `address:port` the member's verbs go to (required): nova-swarm member --server")
		harness = fs.String("harness", "", "the harness binary the member's children run, an absolute path (nova-swarm member --harness)")
		root = fs.String("root", "", "the member's working root, an absolute path (nova-swarm member --root)")
		pass = fs.String("pass", "", "the `NAME,...` of the providers' keys a child is handed (nova-swarm member --pass): names only; the values are the service's environment to give")
		exe = fs.String("swarm", "", "the nova-swarm the unit runs, an absolute path (default: the nova-swarm beside this nova-sprint)")
	case "table":
		out = fs.String("out", "", "the file the live table is written to, an absolute path (required; on Linux, where the journal keeps a unit's lines, give the file with --log too)")
		every = fs.Duration("every", time.Second, "the table's redraw interval, above 0 (where --watch --every)")
	}
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}
	goos := a.seatOS()
	self := a.executable
	if self == nil {
		self = os.Executable
	}
	bin, err := self()
	if err != nil {
		return refuse(stderr, name, "the path of this nova-sprint cannot be read: "+err.Error()+"; nothing was written")
	}
	u := sprint.ServiceUnit{Kind: k, OS: goos, Log: *logf}
	// the store: the sprint's server when one is named and no --redis is, else the Redis
	// the unit opens with the seat login, in its own process
	store := func() ([]string, string) {
		if srv := a.server(fs); srv != "" {
			u.Env = append(u.Env, [2]string{ServerEnv, srv})
			return nil, ""
		}
		addr := strings.TrimSpace(c.redis)
		switch {
		case addr == "":
			return nil, "the unit reads the sprint's store: --redis <addr> (or NOVA_SPRINT_REDIS, or the address nova-sprint seat login recorded), or the sprint's server, NOVA_SPRINT_SERVER"
		case isTwin(addr):
			return nil, "the unit runs on this machine, and the in-memory twin " + addr + " is this process's alone: install it for a Redis store"
		}
		if why := a.unitLogin(addr); why != "" {
			return nil, why
		}
		return []string{"--redis", addr}, ""
	}
	switch k.Kind {
	case "server":
		if *listen == "" {
			return refuse(stderr, name, "--listen <address:port> is required: the server's address on the fleet's private network; nothing was written")
		}
		if *decideDir != "" && !filepath.IsAbs(*decideDir) {
			return refuse(stderr, name, "--decide names its directory by its absolute path, not "+*decideDir+"; nothing was written")
		}
		addr := strings.TrimSpace(c.redis)
		if addr == "" || isTwin(addr) {
			return refuse(stderr, name, "the server opens the sprint's store itself: --redis <addr> (or NOVA_SPRINT_REDIS, or the address nova-sprint seat login recorded), never the twin; nothing was written")
		}
		if why := a.unitLogin(addr); why != "" {
			return refuse(stderr, name, why+"; nothing was written")
		}
		u.Args = []string{bin, "run", "--listen", *listen, "--redis", addr}
		if *land {
			u.Args = append(u.Args, "--land")
		}
		if *decideDir != "" {
			u.Args = append(u.Args, "--decide", *decideDir)
		}
		// The unit's actor is the explicit --actor when one is given: a server
		// reinstalled to clear a server-actor drift runs as the seat's holder, not as
		// the caller's own NOVA_SPRINT_ACTOR, whose value would only recreate the
		// drift (docs/SPEC-DOCTOR.md, seat-agreement).
		if c.actor != "" {
			u.Env = append(u.Env, [2]string{"NOVA_SPRINT_ACTOR", c.actor})
		}
		if v := a.getenv(OwnerEnv); v != "" {
			u.Env = append(u.Env, [2]string{OwnerEnv, v})
		}
	case "member":
		if *as == "" || *server == "" {
			return refuse(stderr, name, "--as <member> and --server <address:port> are required: the member's name and the sprint's server; nothing was written")
		}
		swarm := *exe
		if swarm == "" {
			swarm = filepath.Join(filepath.Dir(bin), "nova-swarm")
		}
		u.Args = []string{swarm, "member", "--as", *as, "--server", *server}
		for _, f := range []struct{ flag, v string }{{"--harness", *harness}, {"--root", *root}} {
			if f.v == "" {
				continue
			}
			if !filepath.IsAbs(f.v) {
				return refuse(stderr, name, f.flag+" names its path absolutely, not "+f.v+"; nothing was written")
			}
			u.Args = append(u.Args, f.flag, f.v)
		}
		if *pass != "" {
			u.Args = append(u.Args, "--pass", *pass)
		}
	case "table":
		if *out == "" || !filepath.IsAbs(*out) {
			return refuse(stderr, name, "--out <file> is required, an absolute path: the file the live table is written to; nothing was written")
		}
		if *every <= 0 {
			return refuse(stderr, name, "--every is the redraw interval, above 0; nothing was written")
		}
		words, why := store()
		if why != "" {
			return refuse(stderr, name, why+"; nothing was written")
		}
		u.Args = append([]string{bin, "where", "--watch", "--every", every.String()}, words...)
		if u.Log == "" {
			u.Log = *out
		}
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
		u.Log = filepath.Join(home, "Library", "Logs", "nova-sprint-"+k.Kind+".log")
	}
	text, err := u.Text()
	if err != nil {
		return refuse(stderr, name, err.Error()+"; nothing was written")
	}
	head := strings.ToUpper(name)
	path := filepath.Join(*dir, k.File(goos))
	if *dry {
		if c.json {
			b, _ := json.Marshal(map[string]any{"path": path, "unit": text, "args": u.Args, "dry_run": true}) // ignored: strings always encode
			fmt.Fprintln(stdout, string(b))
			return 0
		}
		fmt.Fprintf(stdout, "%s DRY-RUN unit=%s; nothing was written or loaded\n%s", head, oneline.Field(path), text)
		return 0
	}
	r, err := a.seatInstaller(goos, *dir).InstallUnit(u)
	if err != nil {
		fmt.Fprintf(stderr, "%s %s FAILED: %s\n", prog, name, oneline.Escape(err.Error()))
		return 1
	}
	if c.json {
		b, _ := json.Marshal(map[string]any{"path": r.Path, "written": r.Changed, "loaded": true, "args": u.Args}) // ignored: strings and bools always encode
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	fmt.Fprintf(stdout, "%s OK unit=%s written=%t loaded=true\n", head, oneline.Field(r.Path), r.Changed)
	fmt.Fprintf(stdout, "  runs: %s\n", strings.Join(u.Args, " "))
	return 0
}

// unitLogin is why a unit cannot open the store at addr, "" when it can: a store this
// shell logs in to as a user (NOVA_SPRINT_REDIS_USER) is opened by the unit only with
// the seat login recorded for that address, since the unit carries no password and no
// nova-secrets exec. A store with no user (its default user) needs none.
func (a *app) unitLogin(addr string) string {
	l, ok, err := a.recordedLogin()
	if err != nil {
		return err.Error()
	}
	if ok && l.Redis == addr {
		return ""
	}
	if user := a.getenv(redisauth.UserEnv); user != "" {
		return "the unit carries no password, and the store " + addr + " is logged in to here as " + user + " from this shell's environment: record the login the unit reads in its own process, run: nova-sprint seat login --redis " + addr + " --user " + user + " --store <dir> --as <seat> --key <file> --secret <NAME>"
	}
	return ""
}

func (a *app) cmdUnits(args []string, stdout, stderr io.Writer) int {
	const name = "units"
	fs, c := a.verbSetup(name)
	check := fs.Bool("check", false, "name each unit a running sprint needs installed, missing or different (required)")
	dir := fs.String("dir", "", "the directory the units are in (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)")
	pos, err := parse(fs, args)
	if err != nil || len(pos) > 0 {
		return refuse(stderr, name, argErr("takes no words ", err, pos...))
	}
	if !*check {
		return refuse(stderr, name, "units wants --check: it names each unit a running sprint needs installed, missing or different; run: nova-sprint units --check")
	}
	goos := a.seatOS()
	if *dir == "" {
		if *dir, err = a.seatDir(goos); err != nil {
			return refuse(stderr, name, "--dir names no directory and the home cannot be read: "+err.Error())
		}
	}
	states, err := sprint.CheckUnits(*dir, goos, sprint.UnitKinds)
	if err != nil {
		return refuse(stderr, name, err.Error())
	}
	n := map[string]int{}
	for _, s := range states {
		n[s.State]++
	}
	ok := n[sprint.UnitInstalled] == len(states)
	if c.json {
		b, _ := json.Marshal(map[string]any{"dir": *dir, "units": states, "ok": ok}) // ignored: strings and bools always encode
		fmt.Fprintln(stdout, string(b))
	} else {
		for _, s := range states {
			line := fmt.Sprintf("UNIT %s %s unit=%s", s.Kind, s.State, oneline.Field(s.Path))
			if s.Why != "" {
				line += " why=" + oneline.Field(s.Why)
			}
			if s.State != sprint.UnitInstalled && s.Owed != "" {
				line += "; owed: " + s.Install + " (" + s.Owed + ")"
			} else if s.State != sprint.UnitInstalled {
				line += "; run: " + s.Install
			}
			fmt.Fprintln(stdout, line)
		}
		word := "OK"
		if !ok {
			word = "DIFFERENT"
		}
		fmt.Fprintf(stdout, "UNITS CHECK %s installed=%d missing=%d different=%d dir=%s\n", word, n[sprint.UnitInstalled], n[sprint.UnitMissing], n[sprint.UnitDifferent], oneline.Field(*dir))
	}
	if !ok {
		return 1
	}
	return 0
}
