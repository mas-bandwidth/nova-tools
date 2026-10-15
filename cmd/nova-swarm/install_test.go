package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/units"
)

// nova-swarm install disk-guard and mirror-refresh write the unit for macOS and Linux
// into a directory the test names, running nova-swarm's own verb, and load it with the
// test's loader. Nothing is loaded on the machine running the test.
func TestSwarmInstallWritesTheDiskGuardAndMirrorRefresh(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	swarmUnits.goos = "linux"
	swarmUnits.home = func() (string, error) { return home, nil }
	swarmUnits.exe = func() (string, error) { return "/opt/nova/bin/nova-swarm", nil }
	var calls []string
	swarmUnits.load = func(goos, op, path string) error {
		calls = append(calls, goos+" "+op+" "+filepath.Base(path))
		return nil
	}
	t.Cleanup(func() {
		swarmUnits = struct {
			goos string
			home func() (string, error)
			exe  func() (string, error)
			load func(goos, op, path string) error
		}{}
	})

	for _, goos := range []string{"darwin", "linux"} {
		swarmUnits.goos = goos
		calls = nil
		dir := t.TempDir()
		k, ok := units.UnitKindOf("disk-guard")
		require.True(t, ok)
		unit := filepath.Join(dir, k.File(goos))

		code, out, errb := runSwarm(t, "install", "disk-guard", "--dry-run", "--dir", dir, "--every", "15m", "--root", "/srv/run", "--scan", "/srv/scan")
		require.Equal(t, 0, code, errb)
		assert.Contains(t, out, "INSTALL DISK-GUARD DRY-RUN unit="+unit)
		assert.NotContains(t, out, "nova-secrets")
		assert.NoFileExists(t, unit)
		assert.Empty(t, calls)

		code, out, errb = runSwarm(t, "install", "disk-guard", "--dir", dir, "--every", "15m", "--root", "/srv/run", "--scan", "/srv/scan")
		require.Equal(t, 0, code, errb)
		assert.Contains(t, out, "INSTALL DISK-GUARD OK unit="+unit+" written=true loaded=true")
		assert.Contains(t, out, "runs: /opt/nova/bin/nova-swarm disk-guard --root /srv/run --scan /srv/scan")
		b, err := os.ReadFile(unit)
		require.NoError(t, err)
		args, err := units.UnitArgs(goos, b)
		require.NoError(t, err)
		assert.Equal(t, []string{"/opt/nova/bin/nova-swarm", "disk-guard", "--root", "/srv/run", "--scan", "/srv/scan"}, args)
		assert.NotContains(t, string(b), "nova-secrets")
		assert.NotContains(t, string(b), "zsh")
		if goos == "darwin" {
			log := filepath.Join(home, "Library", "Logs", "nova-swarm-disk-guard.log")
			assert.Equal(t, units.LaunchdPlist(k.Label, args, nil, log, 900), string(b), "the swarm binary writes the same plist pkg/units does")
		} else {
			assert.Equal(t, units.SystemdUnit("nova "+k.Kind+": "+k.What, args, nil, 900), string(b), "the swarm binary writes the same unit pkg/units does")
		}
		assert.Equal(t, []string{goos + " load " + k.File(goos)}, calls)

		// mirror-refresh is written and loaded like disk-guard: the unit runs
		// nova-swarm mirror itself with the same --every/--dir/--log handling.
		mk, mirrorOK := units.UnitKindOf("mirror-refresh")
		require.True(t, mirrorOK)
		mirrorUnit := filepath.Join(dir, mk.File(goos))
		code, out, errb = runSwarm(t, "install", "mirror-refresh", "--dir", dir, "--every", "1m")
		require.Equal(t, 0, code, errb)
		assert.Contains(t, out, "INSTALL MIRROR-REFRESH OK unit="+mirrorUnit+" written=true loaded=true")
		assert.Contains(t, out, "runs: /opt/nova/bin/nova-swarm mirror")
		mb, err := os.ReadFile(mirrorUnit)
		require.NoError(t, err)
		margs, err := units.UnitArgs(goos, mb)
		require.NoError(t, err)
		assert.Equal(t, []string{"/opt/nova/bin/nova-swarm", "mirror"}, margs)
		assert.NotContains(t, string(mb), "nova-secrets")
		assert.NotContains(t, string(mb), "zsh")
		if goos == "darwin" {
			log := filepath.Join(home, "Library", "Logs", "nova-swarm-mirror-refresh.log")
			assert.Equal(t, units.LaunchdPlist(mk.Label, margs, nil, log, 60), string(mb), "the swarm binary writes the same plist pkg/units does")
		} else {
			assert.Equal(t, units.SystemdUnit("nova "+mk.Kind+": "+mk.What, margs, nil, 60), string(mb), "the swarm binary writes the same unit pkg/units does")
		}
		assert.Equal(t, []string{goos + " load " + k.File(goos), goos + " load " + mk.File(goos)}, calls)

		code, out, errb = runSwarm(t, "uninstall", "disk-guard", "--dry-run", "--dir", dir)
		require.Equal(t, 0, code, errb)
		assert.Contains(t, out, "present=true")
		assert.FileExists(t, unit)
		code, out, errb = runSwarm(t, "uninstall", "disk-guard", "--dir", dir)
		require.Equal(t, 0, code, errb)
		assert.Contains(t, out, "removed=true")
		assert.NoFileExists(t, unit)
		assert.Equal(t, goos+" unload "+k.File(goos), calls[len(calls)-1])

		code, out, errb = runSwarm(t, "uninstall", "mirror-refresh", "--dir", dir)
		require.Equal(t, 0, code, errb)
		assert.Contains(t, out, "removed=true")
		assert.NoFileExists(t, mirrorUnit)
		assert.Equal(t, goos+" unload "+mk.File(goos), calls[len(calls)-1])
	}

	code, _, errb := runSwarm(t, "install", "store", "--dir", t.TempDir())
	assert.Equal(t, 2, code, errb)
	assert.Contains(t, errb, "nova-redis install store")

	// The default loader is units.Load: with the fake removed, installing reaches
	// the loader's host guard and refuses, so unitLoad's delegate (install.go:61)
	// is exercised here, where this test already owns the swarmUnits global. A
	// second parallel test would race that global; NOVA_TEST_NO_HOST is set by
	// make and by every card run.
	if os.Getenv("NOVA_TEST_NO_HOST") != "" {
		swarmUnits.goos = "linux"
		swarmUnits.load = nil
		dir := t.TempDir()
		code, _, errb = runSwarm(t, "install", "disk-guard", "--dir", dir, "--every", "15m", "--root", "/srv/run", "--scan", "/srv/scan")
		assert.Equal(t, 1, code, errb)
		assert.Contains(t, errb, "NOVA_TEST_NO_HOST is set: no service is loaded or unloaded on this machine")
	}
}
