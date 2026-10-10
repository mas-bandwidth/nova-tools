package doctor

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/units"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// inventoryJSON is what `nova-config inventory` prints for one machine with the
// named loops: this host marked local, so the check reads it as this machine.
func inventoryJSON(loops string) string {
	return `{"_meta":{"hostvars":{"bench-a":{"ansible_connection":"local","nova_loops":` + loops + `}}}}`
}

// runUnitsCheck runs only the units check over a fake machine: a filesystem
// rooted in t.TempDir() holding the unit directory, and an exec that answers
// `nova-config inventory` from the inventory JSON. Nothing real runs and no
// socket is opened.
func runUnitsCheck(t *testing.T, inventory string, write func(dir string)) Result {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "units")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	if write != nil {
		write(dir)
	}
	fe := fakeEnv{
		env: map[string]string{
			"HOME":              root,
			"NOVA_UNITS_DIR":    "units",
			"NOVA_SPRINT_REDIS": "127.0.0.1:6390",
		},
		root: root,
	}
	fe.exec = func(name string, args ...string) (string, error) {
		if filepath.Base(name) == "nova-config" && len(args) == 1 && args[0] == "inventory" {
			return inventory, nil
		}
		return "", os.ErrNotExist
	}
	reg := NewRegistry()
	reg.Register(Default.checks["units"])
	res, _, err := reg.Run(context.Background(), fe, Options{})
	require.NoError(t, err)
	require.Len(t, res, 1)
	return res[0]
}

// TestDoctorUnitsCheckFindsAHandPlistAndAMissingLoop pins the units check
// (docs/SETUP.md, dep-launchd-units-bc.w6): it reads this machine's loop records
// and the installed units and names a record with no unit, a unit with no
// record (a hand plist), and a unit whose command differs from its record,
// each with the nova-config and fleet verb that applies records as its fix;
// it is ok when every record has its unit and no unit lacks a record.
func TestDoctorUnitsCheckFindsAHandPlistAndAMissingLoop(t *testing.T) {
	t.Parallel()

	t.Run("a hand plist, a missing loop and a differing command are each named", func(t *testing.T) {
		t.Parallel()
		inv := inventoryJSON(`[
			{"name":"redis-local","argv":["nova-redis","serve","--bind","127.0.0.1"]},
			{"name":"tick","argv":["nova-sprint","run","--listen"]},
			{"name":"helper","argv":["nova-sprint","where"]}
		]`)
		res := runUnitsCheck(t, inv, func(dir string) {
			// the record redis-local, installed with its command: ok
			require.NoError(t, os.WriteFile(filepath.Join(dir, "com.nova.loop.redis-local.plist"),
				[]byte(units.LaunchdPlist("com.nova.loop.redis-local", []string{"/opt/nova/nova-redis", "serve", "--bind", "127.0.0.1"}, nil, "", 10)), 0o644))
			// the record helper, installed running another verb: different
			require.NoError(t, os.WriteFile(filepath.Join(dir, "nova-loop-helper.service"),
				[]byte(units.SystemdUnit("nova loop helper", []string{"/opt/nova/nova-sprint", "server"}, nil, 10)), 0o644))
			// a hand plist no record names: a unit with no record
			require.NoError(t, os.WriteFile(filepath.Join(dir, "com.nova.loop.orphan.plist"),
				[]byte(units.LaunchdPlist("com.nova.loop.orphan", []string{"/bin/true"}, nil, "", 10)), 0o644))
		})
		assert.Equal(t, Fail, res.Status, res)
		assert.Contains(t, res.Evidence, "tick", "the record without a unit is named")
		assert.Contains(t, res.Evidence, "orphan", "the hand plist with no record is named")
		assert.Contains(t, res.Evidence, "helper", "the unit whose command differs is named")
		assert.NotContains(t, res.Evidence, "redis-local", "the matching loop is not named")
		assert.Contains(t, res.Fix, "nova-config apply --kind loop --actor")
		assert.Contains(t, res.Fix, "fleet/loops.yml")
	})

	t.Run("every record has its unit and no unit lacks a record is ok", func(t *testing.T) {
		t.Parallel()
		inv := inventoryJSON(`[{"name":"tick","argv":["nova-sprint","run","--listen"]}]`)
		res := runUnitsCheck(t, inv, func(dir string) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "nova-loop-tick.service"),
				[]byte(units.SystemdUnit("nova loop tick", []string{"/opt/nova/nova-sprint", "run", "--listen"}, nil, 10)), 0o644))
		})
		assert.Equal(t, OK, res.Status, res)
		assert.Empty(t, res.Fix)
	})

	t.Run("extra command arguments differ from the record", func(t *testing.T) {
		t.Parallel()
		inv := inventoryJSON(`[{"name":"tick","argv":["nova-sprint","run","--listen"]}]`)
		res := runUnitsCheck(t, inv, func(dir string) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "nova-loop-tick.service"),
				[]byte(units.SystemdUnit("nova loop tick", []string{"/opt/nova/nova-sprint", "run", "--listen", "--debug"}, nil, 10)), 0o644))
		})
		assert.Equal(t, Fail, res.Status, res)
		assert.Contains(t, res.Evidence, "unit whose command differs: tick")
	})

	t.Run("the play written disk guard has no loop record and is ok", func(t *testing.T) {
		t.Parallel()
		res := runUnitsCheck(t, inventoryJSON(`[]`), func(dir string) {
			body := "# " + loopMark + " disk-guard\n" + units.SystemdUnit("nova loop disk guard", []string{"/opt/nova/nova-swarm", "disk-guard"}, nil, 10)
			require.NoError(t, os.WriteFile(filepath.Join(dir, "nova-loop-disk-guard.service"), []byte(body), 0o644))
		})
		assert.Equal(t, OK, res.Status, res)
		assert.Empty(t, res.Fix)
	})

	t.Run("a record installed in the system unit directory is found", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		var dir, file, body string
		if runtime.GOOS == "darwin" {
			dir = filepath.Join(root, "Library", "LaunchDaemons")
			file = "com.nova.loop.tick.plist"
			body = units.LaunchdPlist("com.nova.loop.tick", []string{"/opt/nova/nova-sprint", "run"}, nil, "", 10)
		} else {
			dir = filepath.Join(root, "etc", "systemd", "system")
			file = "nova-loop-tick.service"
			body = units.SystemdUnit("nova loop tick", []string{"/opt/nova/nova-sprint", "run"}, nil, 10)
		}
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644))
		fe := dashboardEnv{fakeEnv{env: map[string]string{"HOME": "home", "NOVA_SPRINT_REDIS": "127.0.0.1:6390"}, root: root}}
		fe.exec = func(name string, args ...string) (string, error) {
			if filepath.Base(name) == "nova-config" && len(args) == 1 && args[0] == "inventory" {
				return inventoryJSON(`[{"name":"tick","argv":["nova-sprint","run"]}]`), nil
			}
			return "", os.ErrNotExist
		}
		reg := NewRegistry()
		reg.Register(Default.checks["units"])
		res, _, err := reg.Run(context.Background(), fe, Options{})
		require.NoError(t, err)
		require.Len(t, res, 1)
		assert.Equal(t, OK, res[0].Status, res[0])
	})
}
