package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// envValue is the value of name in one environment, "" when absent.
func envValue(env []string, name string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v
		}
	}
	return ""
}

// TestEveryCardOfAMachineBuildsInOneWarmCache (nova-tools#5174, cost rule 5): two launches
// in two slots under one root are handed the same GOCACHE and GOMODCACHE, the machine's
// (swarm.GoBuildCacheDir, swarm.GoModCacheDir), the one the lazy cleaner holds under the
// cap; both lie inside the wall's one --write of the shared cache root; and the child's PATH
// reaches the go shim before the bench's go.
func TestEveryCardOfAMachineBuildsInOneWarmCache(t *testing.T) {
	t.Parallel()
	root, slot := aSlot(t)
	other := filepath.Join(root, "slot-2")
	cfg := nativeRunConfig{slotDir: slot, root: root, benchHome: t.TempDir(), benchOS: "linux"}
	cacheDir := nativeCacheDir(cfg)
	require.Equal(t, swarm.CacheRoot(root), cacheDir)
	runner := &nativeRunner{root: root}
	for _, s := range []string{slot, other} {
		job := filepath.Join(s, "jobs", "card")
		shim := filepath.Join(s, "shim")
		env := nativeChildEnv(filepath.Join(s, "data"), job, filepath.Join(s, "tmp", "card"), cacheDir, "", shim, "", "/bench/go/bin")
		assert.Equal(t, swarm.GoBuildCacheDir(root), envValue(env, "GOCACHE"), "slot %s", s)
		assert.Equal(t, runner.goBuildCache(), envValue(env, "GOCACHE"), "the trimmed cache is the one the card builds in")
		assert.Equal(t, swarm.GoModCacheDir(root), envValue(env, "GOMODCACHE"), "slot %s", s)
		assert.True(t, strings.HasPrefix(envValue(env, "PATH"), shim+string(os.PathListSeparator)+"/bench/go/bin"),
			"the go shim comes before the bench's go: %s", envValue(env, "PATH"))

		argv := nativeSandboxArgv([]string{"/bin/true"}, nativeRunConfig{slotDir: s, root: root, benchHome: cfg.benchHome, benchOS: "linux"},
			filepath.Join(s, "data"), job, filepath.Join(s, "tmp", "card"))
		require.True(t, hasFlagPair(argv, "--write", cacheDir), "the shared cache root is a --write: %s", strings.Join(argv, " "))
		for _, d := range []string{envValue(env, "GOCACHE"), envValue(env, "GOMODCACHE")} {
			assert.True(t, strictlyWithin(cacheDir, d), "%s is inside the --write %s", d, cacheDir)
			assert.False(t, within(s, d), "%s is no slot's own", d)
		}
	}
	require.NoError(t, swarm.EnsureCacheDirs(root))
	for _, d := range []string{swarm.GoBuildCacheDir(root), swarm.GoModCacheDir(root)} {
		fi, err := os.Stat(d)
		require.NoError(t, err, "the directory the child is handed is the one made: %s", d)
		assert.True(t, fi.IsDir())
	}
}

// TestTheGoShimBuildsTrimpath: the wrapper adds -trimpath to whatever GOFLAGS the card
// exported, leaves a GOFLAGS that names it either way alone, and hands every argument to
// the bench's go unchanged.
func TestTheGoShimBuildsTrimpath(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the go shim is a POSIX script, written on unix benches only")
	}
	goBin := t.TempDir()
	write(t, filepath.Join(goBin, "go"), "#!/bin/sh\necho \"GOFLAGS=[$GOFLAGS] args=[$*]\"\n")
	require.NoError(t, os.Chmod(filepath.Join(goBin, "go"), 0o755))
	shim := t.TempDir()
	require.NoError(t, writeNativeGoShim(shim, goBin))
	for _, tc := range []struct{ name, goflags, want string }{
		{"unset", "", "GOFLAGS=[-trimpath] args=[vet ./...]"},
		{"the card's export", "-mod=readonly", "GOFLAGS=[-mod=readonly -trimpath] args=[vet ./...]"},
		{"already trimmed", "-trimpath -mod=readonly", "GOFLAGS=[-trimpath -mod=readonly] args=[vet ./...]"},
		{"refused by the card", "-mod=readonly -trimpath=false", "GOFLAGS=[-mod=readonly -trimpath=false] args=[vet ./...]"},
	} {
		cmd := exec.Command(filepath.Join(shim, "go"), "vet", "./...")
		cmd.Env = []string{"PATH=/usr/bin:/bin"}
		if tc.goflags != "" {
			cmd.Env = append(cmd.Env, "GOFLAGS="+tc.goflags)
		}
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s: %s", tc.name, out)
		assert.Equal(t, tc.want, strings.TrimSpace(string(out)), tc.name)
	}
	require.NoError(t, writeNativeGoShim("", goBin), "no shim directory writes nothing")
	require.NoError(t, writeNativeGoShim(shim, ""), "no bench go writes nothing")
	assert.Error(t, writeNativeGoShim(shim, shim), "a go inside the shim directory would exec itself")
}
