package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// redisVersionLine is one `redis-server --version` line for version v. It is
// built from its parts, so the test names no whole version but the pin.
func redisVersionLine(v string) string {
	return "Redis server v=" + v + " sha=00000000:0 malloc=jemalloc-5.3.0 bits=64 build=0"
}

// redisVersionHost is a testInstallHost whose redis-servers answer `--version`
// from versions (path -> version; a path absent prints nothing and exits 1),
// and whose `make` leaves the binary a build would, reporting built, first on
// PATH in $HOME/.local/bin. Nothing it does reaches the machine.
type redisVersionHost struct {
	testInstallHost
	versions map[string]string
	local    string
}

func newRedisVersionHost(t *testing.T, built string) redisVersionHost {
	t.Helper()
	h := redisVersionHost{testInstallHost: newTestInstallHost(t), versions: map[string]string{}}
	h.local = filepath.Join(h.home, ".local", "bin", "redis-server")
	h.runner.answer = func(c cmdSpec) (string, int, error) {
		switch {
		case len(c.Args) == 1 && c.Args[0] == "--version":
			h.runner.mu.Lock()
			v, ok := h.versions[c.Name]
			h.runner.mu.Unlock()
			if !ok {
				return "", 1, nil
			}
			return redisVersionLine(v), 0, nil
		case c.Name == "make" && built != "":
			// make -C <tree> -jN redis-server: produce the binary the build would.
			tree := c.Args[1]
			if err := os.MkdirAll(filepath.Join(tree, "src"), 0o755); err != nil {
				t.Error(err)
			}
			if err := os.WriteFile(filepath.Join(tree, "src", "redis-server"), []byte("built"), 0o600); err != nil {
				t.Error(err)
			}
			h.runner.mu.Lock()
			h.versions[h.local] = built
			h.runner.onPath["redis-server"] = h.local
			h.runner.mu.Unlock()
		}
		return "", 0, nil
	}
	return h
}

// buildSteps is the commands a source build ran, the `--version` checks left
// out, so a test can read the build on its own.
func (h redisVersionHost) buildSteps() []string {
	var steps []string
	for _, l := range h.runner.lines() {
		if !strings.HasSuffix(l, " --version") {
			steps = append(steps, l)
		}
	}
	return steps
}

func TestInstallRedisVersionKeepsThePinnedServerAndTakesNoLock(t *testing.T) {
	t.Parallel()
	h := newRedisVersionHost(t, redisSourceVersion)
	h.runner.onPath["redis-server"] = "/opt/r/bin/redis-server"
	h.versions["/opt/r/bin/redis-server"] = redisSourceVersion
	require.Equal(t, 0, installRedisServer(h.installHost), "stderr %q", h.errb.String())
	assert.Equal(t, "redis-server /opt/r/bin/redis-server v="+redisSourceVersion+"\n", h.out.String())
	assert.Equal(t, "/opt/r/bin\n", h.published(t))
	assert.False(t, h.lockHeld(), "a kept redis-server took the lock")
	assert.Empty(t, h.buildSteps(), "a kept redis-server was built again: %q", h.runner.lines())
	assert.Empty(t, h.runner.prepends, "a kept redis-server had PATH prepended")
}

// The bench runners' first redis-server on PATH was 7.0.15 (hetzner) and 8.0.5
// (spacegame); the installer kept it and internal/testredis went red. The rule:
// any version but the pin is built over from source.
func TestInstallRedisVersionBuildsThePinnedReleaseOverAnOlderServer(t *testing.T) {
	t.Parallel()
	h := newRedisVersionHost(t, redisSourceVersion)
	h.runner.onPath["redis-server"] = "/usr/bin/redis-server"
	h.versions["/usr/bin/redis-server"] = "7.0.15"
	require.Equal(t, 0, installRedisServer(h.installHost), "stderr %q", h.errb.String())
	steps := h.buildSteps()
	require.Len(t, steps, 3, "the build ran %q", h.runner.lines())
	assert.True(t, strings.HasPrefix(steps[0], "curl -fsSL https://download.redis.io/releases/redis-"+redisSourceVersion+".tar.gz -o "), "curl step %q", steps[0])
	assert.True(t, strings.HasPrefix(steps[1], "tar -xzf "), "tar step %q", steps[1])
	assert.True(t, strings.HasPrefix(steps[2], "make -C ") && strings.HasSuffix(steps[2], " redis-server") && strings.Contains(steps[2], "redis-"+redisSourceVersion), "make step %q", steps[2])
	dir := filepath.Dir(h.local)
	assert.Equal(t, dir, strings.Join(h.runner.prepends, ","), "the built directory is put first on PATH")
	assert.Equal(t, dir+"\n", h.published(t))
	assert.Equal(t, "redis-server "+h.local+" v="+redisSourceVersion+"\n", h.out.String())
	fi, err := os.Stat(h.local)
	require.NoError(t, err)
	b, err := os.ReadFile(h.local)
	require.NoError(t, err)
	assert.Equal(t, "built", string(b))
	assert.Equal(t, os.FileMode(0o755), fi.Mode().Perm())
	assert.False(t, h.lockHeld(), "the lock outlived the build")
	assert.False(t, h.runner.ran("apt-get") || h.runner.ran("brew") || h.runner.ran("sudo"), "a package manager ran: %q", h.runner.lines())
}

func TestInstallRedisVersionBuildsWhenTheVersionLineDoesNotParse(t *testing.T) {
	t.Parallel()
	h := newRedisVersionHost(t, redisSourceVersion)
	h.runner.onPath["redis-server"] = "/usr/bin/redis-server"
	h.versions["/usr/bin/redis-server"] = "not-a-version"
	require.Equal(t, 0, installRedisServer(h.installHost), "stderr %q", h.errb.String())
	assert.Len(t, h.buildSteps(), 3, "the build ran %q", h.runner.lines())
	assert.Equal(t, "redis-server "+h.local+" v="+redisSourceVersion+"\n", h.out.String())
	assert.Equal(t, filepath.Dir(h.local)+"\n", h.published(t))
}

func TestInstallRedisVersionOfReadsTheServerLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		line string
		want string
		ok   bool
	}{
		{"the pinned release", redisVersionLine(redisSourceVersion), redisSourceVersion, true},
		{"the hetzner bench's server", redisVersionLine("7.0.15"), "7.0.15", true},
		{"the spacegame bench's server", redisVersionLine("8.0.5"), "8.0.5", true},
		{"no v= field", "redis-server: command not found", "", false},
		{"a two-part version", redisVersionLine("7.0"), "", false},
		{"an empty line", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := redisVersionOf(tc.line)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.ok, ok)
		})
	}
}
