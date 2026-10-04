package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
)

// redisSourceVersion is the repository's one Redis version, the release the
// source build installs. infra/functional-image/Containerfile names it too;
// internal/ci TestRedisIsOneVersionEverywhere holds the two together. The apt
// and Homebrew branches install what the distribution and Homebrew carry; only
// the source build is pinned.
const redisSourceVersion = "8.10.2"

const redisInstallLock = "/tmp/nova-redis-server-install.lock"

func init() {
	register(verb{
		name:    "install-redis-server",
		summary: "put redis-server on PATH for the tests that start a private server",
		help: `usage: go run ./tools/ci install-redis-server

Puts redis-server on PATH, for the controls that start a private server on
loopback; they do not dial any store. A runner that already has the binary is
unchanged. Linux uses apt-get (then sudo -n), macOS uses Homebrew, and a machine
with neither builds the pinned source release (` + redisSourceVersion + `) into $HOME/.local/bin,
which persists on a self-hosted runner. Several runners share one machine, so the
install takes the lock directory ` + redisInstallLock + ` (a lock older than 600 s is taken over,
a waiter polls every 3 s and gives up after 40 polls).

The directory holding the binary is appended to the file GITHUB_PATH names, for
the steps after this one, and "redis-server <path>" is printed.

exit 0  redis-server is on PATH
exit 1  the install failed, or the lock never came free
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
	have := func() bool { _, err := h.run.LookPath("redis-server"); return err == nil }
	found := func() int {
		bin, err := h.run.LookPath("redis-server")
		if err != nil {
			fmt.Fprintln(h.stderr, "redis-server still not on PATH after install")
			return 1
		}
		h.publish(filepath.Dir(bin))
		fmt.Fprintf(h.stdout, "redis-server %s\n", bin)
		return 0
	}
	install := func() int {
		switch {
		case lookOK(h.run, "apt-get"):
			if code := h.aptInstall("redis-server"); code != 0 {
				return code
			}
		case lookOK(h.run, "brew"):
			if code := h.runInstallStep(brewEnv, "brew", "install", "redis"); code != 0 {
				return code
			}
			prefix, code, err := capture(h.run, cmdSpec{Name: "brew", Args: []string{"--prefix"}, Stderr: h.stderr})
			if err != nil || code != 0 {
				fmt.Fprintln(h.stderr, "brew --prefix failed")
				return 1
			}
			brewbin := filepath.Join(prefix, "bin")
			h.run.PrependPath(brewbin)
			h.publish(brewbin)
		default:
			if code := h.buildRedisFromSource(); code != 0 {
				return code
			}
		}
		return found()
	}
	return lockedInstall(h, "redis-server", have, found, install)
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
	target := filepath.Join(dest, "redis-server")
	if err := os.WriteFile(target, data, 0o755); err != nil {
		fmt.Fprintf(h.stderr, "%v\n", err)
		return 1
	}
	if err := os.Chmod(target, 0o755); err != nil {
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
