package main

import (
	"fmt"
	"path/filepath"
)

const postgresInstallLock = "/tmp/nova-postgres-install.lock"

// postgresBinDirs are the directories a versioned install leaves pg_ctl,
// initdb and postgres in when they are off PATH (Debian and Ubuntu put them
// under /usr/lib/postgresql/<v>/bin, Homebrew under the keg), in the order a
// machine is searched; postgresBinGlob is tried after them, for any other
// version.
var postgresBinDirs = []string{
	"/usr/lib/postgresql/18/bin", "/usr/lib/postgresql/16/bin", "/usr/lib/postgresql/17/bin", "/usr/lib/postgresql/15/bin", "/usr/lib/postgresql/14/bin",
	"/opt/homebrew/opt/postgresql@18/bin", "/opt/homebrew/opt/postgresql@16/bin", "/usr/local/opt/postgresql@16/bin", "/usr/pgsql-18/bin", "/usr/pgsql-16/bin",
}

const postgresBinGlob = "/usr/lib/postgresql/*/bin"

var postgresBinaries = []string{"pg_ctl", "initdb", "postgres"}

func init() {
	register(verb{
		name:    "install-postgres",
		summary: "put initdb, pg_ctl and postgres on PATH for the tests that start a private server",
		help: `usage: go run ./tools/ci install-postgres

Puts initdb, pg_ctl and postgres on PATH, for the tests that start a private
Postgres on loopback under the test's directory (pkg/nsprint/testutil/pg);
they do not dial any store. A runner that already has the binaries is unchanged.
Linux uses apt-get (postgresql-16 where the archive has it, else postgresql; then
sudo -n), macOS uses Homebrew's postgresql@16. Debian and Ubuntu install the
binaries under /usr/lib/postgresql/<v>/bin, off PATH, so that directory is
appended to the file GITHUB_PATH names, for the steps after this one.
"postgres <path> <version>" is printed. Several runners share one machine, so the
install takes the lock directory ` + postgresInstallLock + ` (a lock older than 600 s is taken over,
a waiter polls every 3 s and gives up after 40 polls).

exit 0  the binaries are on PATH or in a directory now published
exit 1  the install failed, or the lock never came free
`,
		do: func(e env, args []string) int {
			if len(args) != 0 {
				fmt.Fprintln(e.stderr, "install-postgres: takes no arguments")
				return 2
			}
			h := osInstallHost(e)
			h.lock = postgresInstallLock
			return installPostgres(h)
		},
	})
}

// installPostgres is the verb over a host: see the help.
func installPostgres(h installHost) int {
	onPath := func() bool {
		for _, b := range postgresBinaries {
			if !lookOK(h.run, b) {
				return false
			}
		}
		return true
	}
	// findBin is the directory of a versioned install that is off PATH.
	findBin := func() (string, bool) {
		complete := func(d string) bool {
			for _, b := range postgresBinaries {
				if !h.isExec(filepath.Join(d, b)) {
					return false
				}
			}
			return true
		}
		for _, d := range postgresBinDirs {
			if complete(d) {
				return d, true
			}
		}
		for _, d := range h.glob(postgresBinGlob) {
			if complete(d) {
				return d, true
			}
		}
		return "", false
	}
	have := func() bool { _, ok := findBin(); return onPath() || ok }
	found := func() int {
		var dir string
		if onPath() {
			p, _ := h.run.LookPath("pg_ctl")
			dir = filepath.Dir(p)
		} else {
			d, ok := findBin()
			if !ok {
				fmt.Fprintln(h.stderr, "postgres binaries still not found after install")
				return 1
			}
			dir = d
			h.run.PrependPath(dir)
		}
		h.publish(dir)
		bin := filepath.Join(dir, "postgres")
		ver, _, _ := capture(h.run, cmdSpec{Name: bin, Args: []string{"--version"}, Stderr: h.stderr})
		fmt.Fprintf(h.stdout, "postgres %s %s\n", bin, ver)
		return 0
	}
	install := func() int {
		switch {
		case lookOK(h.run, "apt-get"):
			pkg := "postgresql-16"
			if code, err := h.run.Run(cmdSpec{Name: "apt-cache", Args: []string{"show", pkg}}); err != nil || code != 0 {
				pkg = "postgresql"
			}
			if code := h.aptInstall(pkg); code != 0 {
				return code
			}
		case lookOK(h.run, "brew"):
			if code := h.runInstallStep(brewEnv, "brew", "install", "postgresql@16"); code != 0 {
				return code
			}
		default:
			fmt.Fprintln(h.stderr, "no apt-get and no brew: install postgresql (16) by hand and put initdb, pg_ctl and postgres on PATH, or set NOVA_PG_BIN to their directory")
			return 1
		}
		return found()
	}
	return lockedInstall(h, "postgres", have, found, install)
}
