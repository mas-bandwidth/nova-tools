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
// source build installs and the only version installRedisServer keeps.
// infra/functional-image/Containerfile names it too; internal/ci
// TestRedisIsOneVersionEverywhere holds the places together.
const redisSourceVersion = "8.10.2"

const redisInstallLock = "/tmp/nova-redis-server-install.lock"

func init() {
	register(verb{
		name:    "install-redis-server",
		summary: "put the pinned redis-server on PATH for the tests that start a private server",
		help: `usage: go run ./tools/ci install-redis-server

Puts the pinned redis-server (` + redisSourceVersion + `) on PATH, for the controls that
start a private server on loopback; they do not dial any store. The version is
what ` + "`redis-server --version`" + ` reports (v=` + redisSourceVersion + `).

A runner whose first redis-server on PATH reports it is unchanged. Any other
version, or none, is built over: the pinned source release is built into
$HOME/.local/bin, which persists on a self-hosted runner. A distribution's or
Homebrew's redis-server is never taken, because it is whatever version they
carry. Several runners share one machine, so the build takes the lock directory
` + redisInstallLock + ` (a lock older than 600 s is taken over, a waiter polls every 3 s and
gives up after 40 polls).

When it builds, the directory holding the binary is put first on PATH; both
cases put it in the file GITHUB_PATH names, for the steps after this one, and
"redis-server <path> v=<version>" is printed.

exit 0  the pinned redis-server is on PATH
exit 1  the build failed, or the lock never came free
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

// redisVersionOf reads the version a `redis-server --version` line reports,
// its v=<major.minor.patch> field; ok is false when the line carries no such
// field.
func redisVersionOf(line string) (string, bool) {
	for _, field := range strings.Fields(line) {
		v, ok := strings.CutPrefix(field, "v=")
		if !ok || !isThreePartVersion(v) {
			continue
		}
		return v, true
	}
	return "", false
}

// isThreePartVersion says whether v is digits.digits.digits.
func isThreePartVersion(v string) bool {
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

// installRedisServer is the verb over a host: see the help.
func installRedisServer(h installHost) int {
	// bin and version are what the last version check found, so found asks no
	// second time of the binary it prints.
	var bin, version string
	have := func() bool {
		b, err := h.run.LookPath("redis-server")
		if err != nil {
			bin, version = "", ""
			return false
		}
		if version = h.redisVersion(b); version != redisSourceVersion {
			bin = ""
			return false
		}
		bin = b
		return true
	}
	found := func() int {
		if bin == "" {
			b, err := h.run.LookPath("redis-server")
			if err != nil {
				fmt.Fprintln(h.stderr, "redis-server still not on PATH after install")
				return 1
			}
			bin, version = b, h.redisVersion(b)
		}
		h.publish(filepath.Dir(bin))
		fmt.Fprintf(h.stdout, "redis-server %s v=%s\n", bin, version)
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
	out, code, err := capture(h.run, cmdSpec{Name: bin, Args: []string{"--version"}, Stderr: h.stderr})
	if err != nil || code != 0 {
		return ""
	}
	v, _ := redisVersionOf(out)
	return v
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
	defer func() { _ = os.RemoveAll(work) }() // ignored: work is this function's own MkdirTemp scratch and may already be gone
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
