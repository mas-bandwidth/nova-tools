package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/units"
)

// nova-swarm install disk-guard writes the unit for macOS and Linux into a directory
// the test names, running nova-swarm disk-guard itself, and loads it with the test's
// loader. mirror-refresh is refused: there is no mirror verb. Nothing is loaded on
// the machine running the test.
func TestSwarmInstallWritesTheDiskGuardAndRefusesMirrorRefresh(t *testing.T) {
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

		code, out, errb = runSwarm(t, "install", "mirror-refresh", "--dir", dir)
		assert.Equal(t, 2, code, out)
		assert.Contains(t, errb, "no mirror verb")
		mk, mirrorOK := units.UnitKindOf("mirror-refresh")
		require.True(t, mirrorOK)
		assert.NoFileExists(t, filepath.Join(dir, mk.File(goos)))
		assert.Len(t, calls, 1, "a refused install loads nothing")

		code, out, errb = runSwarm(t, "uninstall", "disk-guard", "--dry-run", "--dir", dir)
		require.Equal(t, 0, code, errb)
		assert.Contains(t, out, "present=true")
		assert.FileExists(t, unit)
		code, out, errb = runSwarm(t, "uninstall", "disk-guard", "--dir", dir)
		require.Equal(t, 0, code, errb)
		assert.Contains(t, out, "removed=true")
		assert.NoFileExists(t, unit)
		assert.Equal(t, goos+" unload "+k.File(goos), calls[len(calls)-1])
	}

	code, _, errb := runSwarm(t, "install", "store", "--dir", t.TempDir())
	assert.Equal(t, 2, code, errb)
	assert.Contains(t, errb, "nova-redis install store")
}
