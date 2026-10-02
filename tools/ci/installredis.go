package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// redisSourceVersion is the repository's one Redis version, the release the
// source build installs and the one internal/testredis accepts.
// infra/functional-image/Containerfile names it too; internal/ci
// TestRedisIsOneVersionEverywhere holds the places together.
const redisSourceVersion = "8.10.2"

const redisInstallLock = "/tmp/nova-redis-server-install.lock"

func init() {
	register(verb{
		name:    "install-redis-server",
		summary: "put the pinned redis-server first on PATH for the tests that start a private server",
		help: `usage: go run ./tools/ci install-redis-server

Puts the repository's one Redis (` + redisSourceVersion + `) first on PATH, for the
controls that start a private server on loopback; they do not dial any store.
The version is what ` + "`redis-server --version`" + ` reports (v=` + redisSourceVersion + `).

A runner whose first redis-server on PATH reports it is unchanged. Else a
redis-server in $HOME/.local/bin that reports it is put first. Else the pinned
source release is built and copied into $HOME/.local/bin, which persists on a
self-hosted runner and which the bench runners' units put ahead of /usr/bin. A
distribution's or Homebrew's redis-server is never taken: it is whatever
version they carry (#5151: 7.0.15 and 8.0.5 on the benches, red on dev).
Several runners share one machine, so the build takes the lock directory
` + redisInstallLock + ` (a lock older than 600 s is taken over, a waiter polls
every 3 s and gives up after 40 polls).

The directory holding the binary is appended to the file GITHUB_PATH names, for
the steps after this one, and "redis-server <path> v=<version>" is printed.

exit 0  the pinned redis-server is first on PATH
exit 1  the build failed, the lock never came free, or the redis-server first
        on PATH after the build is still another version
`,
		do: func(e env, args []string) int {
			if len(args) != 0 {
				fmt.Fprintln(e.stderr, "install-redis-server: takes no arguments")
				return 2
			}
			h := osInstallHost(e)
			h.lock = redisInstallLock
			return installRedisServer(h)
		},
	})
}

// installRedisServer is the verb over a host: see the help.
func installRedisServer(h installHost) int {
	// pinned is the redis-server to put first: the first on PATH when it
	// reports the pin, else the one a build left in $HOME/.local/bin.
	pinned := func() (string, bool) {
		if bin, err := h.run.LookPath("redis-server"); err == nil && h.redisVersion(bin) == redisSourceVersion {
			return bin, true
		}
		if home := h.getenv("HOME"); home != "" {
			bin := filepath.Join(home, ".local", "bin", "redis-server")
			if h.isExec(bin) && h.redisVersion(bin) == redisSourceVersion {
				return bin, true
			}
		}
		return "", false
	}
	// bin is what have last found, so found asks no second version of it.
	var bin string
	have := func() bool {
		b, ok := pinned()
		bin = b
		return ok
	}
	found := func() int {
		if bin == "" && !have() {
			first, err := h.run.LookPath("redis-server")
			if err != nil {
				fmt.Fprintf(h.stderr, "no redis-server on PATH after the install; want %s\n", redisSourceVersion)
				return 1
			}
			fmt.Fprintf(h.stderr, "redis-server first on PATH is %s, version %q, after the install; want %s\n", first, h.redisVersion(first), redisSourceVersion)
			return 1
		}
		dir := filepath.Dir(bin)
		if first, err := h.run.LookPath("redis-server"); err != nil || first != bin {
			h.run.PrependPath(dir)
		}
		h.publish(dir)
		fmt.Fprintf(h.stdout, "redis-server %s v=%s\n", bin, redisSourceVersion)
		return 0
	}
	install := func() int {
		if code := h.buildRedisFromSource(); code != 0 {
			return code
		}
		return found()
	}
	return lockedInstall(h, "redis-server", have, found, install)
}

// redisVersion is the version bin reports, the v= field of `redis-server
// --version` ("Redis server v=8.10.2 sha=..."); "" when it reports none.
func (h installHost) redisVersion(bin string) string {
	out, code, err := capture(h.run, cmdSpec{Name: bin, Args: []string{"--version"}})
	if err != nil || code != 0 {
		return ""
	}
	for _, f := range strings.Fields(out) {
		if v, ok := strings.CutPrefix(f, "v="); ok {
			return v
		}
	}
	return ""
}

// buildRedisFromSource downloads the pinned release, builds redis-server and
// copies it into $HOME/.local/bin.
func (h installHost) buildRedisFromSource() int {
	home := h.getenv("HOME")
	if home == "" {
		fmt.Fprintln(h.stderr, "HOME is not set")
		return 1
	}
	dest := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		fmt.Fprintf(h.stderr, "%v\n", err)
		return 1
	}
	work, err := os.MkdirTemp("", "redis-build-")
	if err != nil {
		fmt.Fprintf(h.stderr, "%v\n", err)
		return 1
	}
	defer os.RemoveAll(work)
	tarball := filepath.Join(work, "redis.tar.gz")
	tree := filepath.Join(work, "redis-"+redisSourceVersion)
	steps := [][]string{
		{"curl", "-fsSL", "https://download.redis.io/releases/redis-" + redisSourceVersion + ".tar.gz", "-o", tarball},
		{"tar", "-xzf", tarball, "-C", work},
		{"make", "-C", tree, "-j" + strconv.Itoa(runtime.NumCPU()), "redis-server"},
	}
	for _, s := range steps {
		if code := h.runInstallStep(nil, s[0], s[1:]...); code != 0 {
			return code
		}
	}
	data, err := os.ReadFile(filepath.Join(tree, "src", "redis-server"))
	if err != nil {
		fmt.Fprintf(h.stderr, "the build produced no redis-server: %v\n", err)
		return 1
	}
	// Written beside the target and renamed over it: another runner's test may
	// be running the redis-server that is there now.
	target := filepath.Join(dest, "redis-server")
	next := target + ".new-" + strconv.Itoa(h.ownPID())
	if err := os.WriteFile(next, data, 0o755); err != nil {
		fmt.Fprintf(h.stderr, "%v\n", err)
		return 1
	}
	if err := os.Chmod(next, 0o755); err != nil {
		os.Remove(next)
		fmt.Fprintf(h.stderr, "%v\n", err)
		return 1
	}
	if err := os.Rename(next, target); err != nil {
		os.Remove(next)
		fmt.Fprintf(h.stderr, "%v\n", err)
		return 1
	}
	h.run.PrependPath(dest)
	return 0
}

func lookOK(r cmdRunner, name string) bool { _, err := r.LookPath(name); return err == nil }

// globSorted is filepath.Glob with its error dropped: a malformed pattern
// matches nothing, and every pattern here is a constant.
func globSorted(pattern string) []string {
	m, _ := filepath.Glob(pattern)
	return m
}
