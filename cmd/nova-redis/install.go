package main

// install.go is install store, install bus and their uninstalls (card
// every-unit-installed-by-a-verb): the sprint's store and the friends' bus, each a
// Redis server, as a service of this machine (a launchd agent on macOS, a systemd
// user unit on Linux, kept alive and started again at login) in place of a
// redis-server unit written by hand on a configuration file written by hand. The unit
// runs nova-redis serve itself: serve writes redis-server's configuration from its
// flags (the binding, the port, the store directory under the bench root, the
// persistence and eviction rules) and hands it over stdin, and reads the password in
// its own process from the login the unit names (--secret and the seat it is in), so
// neither the unit nor any file holds it. The unit's text, its install and its loader
// are internal/units' (units.ServiceUnit, units.Installer, units.Load), shared with
// nova-sprint install, and nova-sprint units --check names both units installed,
// missing or different.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tool"
	"github.com/mas-bandwidth/nova-tools/internal/units"
)

// The Redis servers nova-redis installs, and each one's port and store directory
// under the bench root when no flag names them: the store's and the bus's as the
// coordinator's machine has run them.
var redisUnits = []struct {
	kind string
	port int
}{{"store", 6380}, {"bus", 6381}}

// installVerbs are install and uninstall of each Redis unit.
func installVerbs(d deps) []tool.Verb {
	var out []tool.Verb
	for _, r := range redisUnits {
		out = append(out, installVerb(d, r.kind, r.port))
	}
	for _, r := range redisUnits {
		out = append(out, uninstallVerb(d, r.kind))
	}
	return out
}

func installVerb(d deps, kind string, port int) tool.Verb {
	return tool.Verb{
		Name:    "install " + kind,
		Usage:   "install " + kind + " --secrets <dir> --as <seat> --key <file> --sops <path> --secret <NAME> [--bind <addr>[,<addr>...]] [--port <port>] [--dir <store-dir>] [--units <dir>] [--log <file>] [--dry-run]",
		Example: "",
		Effect:  tool.LocalWrite,
		DryRun:  true,
		Detail: `writes the ` + kind + `'s unit, which runs nova-redis serve itself with the binding, the port, the
store directory and the login named here, into --units (default: ~/Library/LaunchAgents on
macOS, ~/.config/systemd/user on Linux) and loads it with launchctl or systemctl --user.
The unit carries no password: serve reads it in its own process from --secret in the seat
--as of the secrets store --secrets. --dry-run prints the unit and writes and loads nothing.
example: nova-redis install ` + kind + ` --dry-run --secrets ~/nova-bench/secrets --as <seat> --key <file> --sops <path> --secret <NAME>`,
		Flags: func(f *tool.Flags) {
			f.Prints()
			f.String("bind", "127.0.0.1", "comma-separated IP addresses the server listens on, loopback or tailnet only (serve --bind)")
			f.String("port", strconv.Itoa(port), "the TCP port the server listens on (serve --port)")
			f.String("dir", "", "the absolute path of the store directory (serve --dir; default: ~/nova-bench/redis/"+kind+")")
			loginUnitFlags(f)
			f.String("units", "", "the directory the unit is written into (default: ~/Library/LaunchAgents on macOS, ~/.config/systemd/user on Linux)")
			f.String("log", "", "the file the server's lines go to, macOS (default: ~/Library/Logs/nova-redis-"+kind+".log); on Linux they are in the journal")
		},
		Run: func(c *tool.Call) *tool.Out { return installRun(c, d, kind) },
	}
}

func uninstallVerb(d deps, kind string) tool.Verb {
	return tool.Verb{
		Name:    "uninstall " + kind,
		Usage:   "uninstall " + kind + " [--units <dir>] [--dry-run]",
		Example: "",
		Effect:  tool.LocalWrite,
		DryRun:  true,
		Detail: `unloads the ` + kind + `'s unit and removes its file from --units; the store directory and its data
are kept. --dry-run names the unit and unloads and removes nothing.
example: nova-redis uninstall ` + kind + ` --dry-run`,
		Flags: func(f *tool.Flags) {
			f.Prints()
			f.String("units", "", "the directory the unit was written into (default: as install's)")
		},
		Run: func(c *tool.Call) *tool.Out { return uninstallRun(c, d, kind) },
	}
}

// loginUnitFlags are where serve reads its password in its own process: one name in
// one seat of a secrets store (internal/secrets.Login), none of them a secret.
func loginUnitFlags(f *tool.Flags) {
	f.String("secrets", "", "the secrets store's working copy the password is in (nova-secrets --store)")
	f.String("as", "", "the seat of the secrets store the password is sealed for (nova-secrets --as)")
	f.String("key", "", "the seat's age key file (nova-secrets --key)")
	f.String("sops", "", "the sops binary the seat's file is decrypted with (nova-secrets --sops)")
	f.String("secret", "", "the `NAME` of the password in the seat; with it, serve reads the password in its own process rather than from "+PasswordEnv)
}

// unitOS is the OS the unit is written for: the test's, else this machine's.
func (d deps) unitOS() string {
	if d.goos != "" {
		return d.goos
	}
	return runtime.GOOS
}

func (d deps) userHome() (string, error) {
	if d.home != nil {
		return d.home()
	}
	return os.UserHomeDir()
}

// unitInstaller is the installer into dir, its loader the test's when one is set.
func (d deps) unitInstaller(goos, dir string) units.Installer {
	load := d.loadUnit
	if load == nil {
		load = units.Load
	}
	return units.Installer{Dir: dir,
		Load:   func(p string) error { return load(goos, "load", p) },
		Unload: func(p string) error { return load(goos, "unload", p) }}
}

// unitDir is --units, else the user's unit directory.
func (d deps) unitDir(c *tool.Call, goos string) (string, error) {
	if dir := c.Str("units"); dir != "" {
		return dir, nil
	}
	home, err := d.userHome()
	if err != nil {
		return "", fmt.Errorf("--units names no directory and the home cannot be read: %v", err)
	}
	return units.Dir(goos, home, d.getenv), nil
}

func installRun(c *tool.Call, d deps, kind string) *tool.Out {
	k, _ := units.UnitKindOf(kind) // ignored: redisUnits are UnitKinds' store and bus
	goos := d.unitOS()
	binds, err := validBinds(c.Str("bind"))
	if err != nil {
		return tool.Refuse(err.Error() + "; nothing was written")
	}
	port, err := strconv.Atoi(c.Str("port"))
	if err != nil || port < 1 || port > 65535 {
		return tool.Refuse(fmt.Sprintf("--port %q needs a port from 1 to 65535; nothing was written", c.Str("port")))
	}
	home, herr := d.userHome()
	dir := c.Str("dir")
	if dir == "" {
		if herr != nil {
			return tool.Refuse("--dir names no store directory and the home cannot be read: " + herr.Error())
		}
		dir = filepath.Join(home, "nova-bench", "redis", kind)
	}
	if !filepath.IsAbs(dir) {
		return tool.Refuse(fmt.Sprintf("--dir %q is not absolute; name the store directory in full; nothing was written", dir))
	}
	login := []string{}
	var missing []string
	for _, f := range []string{"secrets", "as", "key", "sops", "secret"} {
		v := c.Str(f)
		if v == "" {
			missing = append(missing, "--"+f)
			continue
		}
		login = append(login, "--"+f, v)
	}
	if len(missing) > 0 {
		return tool.Refuse("the unit carries no password, so serve reads it in its own process from the login the unit names, and it names no " + strings.Join(missing, ", ") + "; nothing was written")
	}
	exe := d.executable
	if exe == nil {
		exe = os.Executable
	}
	bin, err := exe()
	if err != nil {
		return tool.Refuse("the path of this nova-redis cannot be read: " + err.Error() + "; nothing was written")
	}
	u := units.ServiceUnit{Kind: k, OS: goos, Log: c.Str("log"),
		Args: append([]string{bin, "serve", "--bind", strings.Join(binds, ","), "--port", strconv.Itoa(port), "--dir", filepath.Clean(dir)}, login...)}
	if u.Log == "" && goos == "darwin" {
		if herr != nil {
			return tool.Refuse("--log names no file and the home cannot be read: " + herr.Error())
		}
		u.Log = filepath.Join(home, "Library", "Logs", "nova-redis-"+kind+".log")
	}
	text, err := u.Text()
	if err != nil {
		return tool.Refuse(err.Error() + "; nothing was written")
	}
	unitsDir, err := d.unitDir(c, goos)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	head := "INSTALL " + strings.ToUpper(kind)
	if c.DryRun() {
		fmt.Fprintf(c.Stdout, "%s DRY-RUN unit=%s; nothing was written or loaded\n%s", head, oneline.Field(filepath.Join(unitsDir, k.File(goos))), text)
		return tool.Exit(0)
	}
	r, err := d.unitInstaller(goos, unitsDir).Install(u)
	if err != nil {
		fmt.Fprintf(c.Stderr, "%s FAILED err=%s\n", head, oneline.Err(err))
		return tool.Exit(1)
	}
	fmt.Fprintf(c.Stdout, "%s OK unit=%s written=%t loaded=true\n  runs: %s\n", head, oneline.Field(r.Path), r.Changed, strings.Join(u.Args, " "))
	return tool.Exit(0)
}

func uninstallRun(c *tool.Call, d deps, kind string) *tool.Out {
	k, _ := units.UnitKindOf(kind) // ignored: redisUnits are UnitKinds' store and bus
	goos := d.unitOS()
	unitsDir, err := d.unitDir(c, goos)
	if err != nil {
		return tool.Refuse(err.Error())
	}
	head := "UNINSTALL " + strings.ToUpper(kind)
	if c.DryRun() {
		file := k.File(goos)
		if file == "" {
			return tool.Refuse("uninstall " + kind + " removes a launchd agent (macOS) or a systemd user unit (Linux), and " + goos + " has neither")
		}
		path := filepath.Join(unitsDir, file)
		_, serr := os.Stat(path)
		fmt.Fprintf(c.Stdout, "%s DRY-RUN unit=%s present=%t; nothing was unloaded or removed\n", head, oneline.Field(path), serr == nil)
		return tool.Exit(0)
	}
	r, err := d.unitInstaller(goos, unitsDir).Uninstall(k, goos)
	if err != nil {
		fmt.Fprintf(c.Stderr, "%s FAILED err=%s\n", head, oneline.Err(err))
		return tool.Exit(1)
	}
	fmt.Fprintf(c.Stdout, "%s OK unit=%s removed=%t\n", head, oneline.Field(r.Path), r.Changed)
	return tool.Exit(0)
}

// errNoLogin is serve's refusal when the login it was given names a field and not
// another: the login is all five or none.
var errNoLogin = errors.New("serve reads its password from a login of five flags, --secrets, --as, --key, --sops and --secret, or from " + PasswordEnv + " with none of them")
