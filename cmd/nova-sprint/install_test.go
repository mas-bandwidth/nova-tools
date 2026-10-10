package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/units"
)

// install writes each of nova-sprint's units for macOS and Linux into a fake home,
// running the verb itself (no nova-secrets exec, no shell), and loads it with the
// test's loader; units --check names the needed units installed, missing or
// different; uninstall unloads and removes (card every-unit-installed-by-a-verb).
func TestInstallWritesEachSprintUnitAndUnitsCheckNamesWhatIsMissing(t *testing.T) {
	t.Parallel()
	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			env := map[string]string{"NOVA_SPRINT_REDIS": "127.0.0.1:6380", "NOVA_SPRINT_ACTOR": "seat-a", OwnerEnv: "owner-a"}
			a := newApp(func(k string) string { return env[k] })
			a.goos = goos
			a.home = func() (string, error) { return home, nil }
			a.executable = func() (string, error) { return "/opt/nova/bin/nova-sprint", nil }
			m := store.NewMem()
			a.backend = func(context.Context, string, sprint.Names) (store.Backend, error) { return m, nil }
			session := t.TempDir()
			var calls []string
			a.seatLoad = func(goos, op, path string) error { calls = append(calls, op+" "+filepath.Base(path)); return nil }
			do := func(verb func(*app, []string, io.Writer, io.Writer) int, args ...string) (int, string, string) {
				var out, errb bytes.Buffer
				code := verb(a, args, &out, &errb)
				return code, out.String(), errb.String()
			}
			dir := sprint.UnitDir(goos, home, func(string) string { return "" })
			file := func(kind string) string {
				k, ok := sprint.UnitKindOf(kind)
				require.True(t, ok)
				return filepath.Join(dir, k.File(goos))
			}

			code, out, errs := do((*app).cmdUnits, "--check")
			assert.Equal(t, 1, code, errs)
			assert.Contains(t, out, "UNIT server missing unit="+file("server")+"; run: nova-sprint install server")
			assert.Contains(t, out, "UNITS CHECK DIFFERENT installed=0 missing=9 different=0")
			assert.Contains(t, out, "UNIT disk-guard missing unit="+file("disk-guard")+"; run: nova-swarm install disk-guard")
			assert.Contains(t, out, "UNIT mirror-refresh missing unit="+file("mirror-refresh")+"; owed: nova-swarm install mirror-refresh (nova-swarm has no mirror verb", "a verb not there yet is named owed, never as a line to run")

			code, out, errs = do((*app).cmdInstall, "server", "--listen", "127.0.0.1:6390", "--land", "--decide", "/srv/decide", "--dry-run")
			require.Equal(t, 0, code, errs)
			assert.Contains(t, out, "INSTALL SERVER DRY-RUN unit="+file("server"))
			assert.NoFileExists(t, file("server"), "a dry run writes nothing")
			assert.Empty(t, calls)

			for _, line := range [][]string{
				{"server", "--listen", "127.0.0.1:6390", "--land", "--decide", "/srv/decide"},
				{"member", "--as", "m1", "--server", "127.0.0.1:6390", "--harness", "/opt/h/opencode", "--root", "/srv/run", "--pass", "PROVIDER_A_KEY"},
				{"seat-push", "--harness", "opencode", "--target", session},
				{"friend-sync", "--every", "15s"},
				{"table", "--out", "/srv/table.txt"},
			} {
				code, out, errs = do((*app).cmdInstall, line...)
				require.Equal(t, 0, code, "%v: %s", line, errs)
				assert.Contains(t, out, "OK unit="+file(line[0]), line)
				b, err := os.ReadFile(file(line[0]))
				require.NoError(t, err)
				assert.NotContains(t, string(b), "nova-secrets", "%s runs no wrapper", line[0])
				assert.NotContains(t, string(b), "zsh", "%s runs no shell", line[0])
			}
			b, err := os.ReadFile(file("server"))
			require.NoError(t, err)
			args, err := units.UnitArgs(goos, b)
			require.NoError(t, err)
			assert.Equal(t, []string{"/opt/nova/bin/nova-sprint", "run", "--listen", "127.0.0.1:6390", "--redis", "127.0.0.1:6380", "--land", "--decide", "/srv/decide"}, args)
			assert.Contains(t, string(b), "owner-a", "the owner's name rides in the unit's environment")
			b, err = os.ReadFile(file("member"))
			require.NoError(t, err)
			args, err = units.UnitArgs(goos, b)
			require.NoError(t, err)
			assert.Equal(t, []string{"/opt/nova/bin/nova-swarm", "member", "--as", "m1", "--server", "127.0.0.1:6390", "--harness", "/opt/h/opencode", "--root", "/srv/run", "--pass", "PROVIDER_A_KEY"}, args)
			b, err = os.ReadFile(file("table"))
			require.NoError(t, err)
			args, err = units.UnitArgs(goos, b)
			require.NoError(t, err)
			assert.Equal(t, []string{"/opt/nova/bin/nova-sprint", "where", "--watch", "--every", "1s", "--redis", "127.0.0.1:6380"}, args)
			assert.Len(t, calls, 5, "each unit loaded once")

			// the units nova-sprint installs are in; the store, the bus and the upkeep are
			// missing; a hand-written wrapper in the server's place is different
			k, _ := sprint.UnitKindOf("server")
			wrapped := sprint.ServiceUnit{Kind: sprint.UnitKind{Kind: "x", Label: k.Label, Service: k.Service, Tool: "nova-secrets", Verb: []string{"exec"}}, OS: goos,
				Args: []string{"/usr/local/bin/nova-secrets", "exec", "--", "/opt/nova/bin/nova-sprint", "run", "--listen", "127.0.0.1:6390"}}
			text, err := wrapped.Text()
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(file("server"), []byte(text), 0o644))
			code, out, errs = do((*app).cmdUnits, "--check", "--json")
			assert.Equal(t, 1, code, errs)
			var got struct {
				Units []sprint.UnitState `json:"units"`
				OK    bool               `json:"ok"`
			}
			require.NoError(t, json.Unmarshal([]byte(out), &got), out)
			assert.False(t, got.OK)
			states := map[string]string{}
			for _, s := range got.Units {
				states[s.Kind] = s.State
			}
			assert.Equal(t, map[string]string{
				"server": sprint.UnitDifferent, "member": sprint.UnitInstalled, "seat-push": sprint.UnitInstalled,
				"friend-sync": sprint.UnitInstalled, "table": sprint.UnitInstalled, "store": sprint.UnitMissing,
				"bus": sprint.UnitMissing, "disk-guard": sprint.UnitMissing, "mirror-refresh": sprint.UnitMissing,
			}, states)

			code, out, errs = do((*app).cmdUninstall, "table")
			require.Equal(t, 0, code, errs)
			assert.Contains(t, out, "UNINSTALL TABLE OK unit="+file("table")+" removed=true")
			assert.NoFileExists(t, file("table"))
			assert.Equal(t, "unload "+filepath.Base(file("table")), calls[len(calls)-1])
			code, out, errs = do((*app).cmdUninstall, "seat-push")
			require.Equal(t, 0, code, errs)
			assert.NoFileExists(t, file("seat-push"), out)
		})
	}
}

// install refuses what it cannot install as a unit, writing and loading nothing.
func TestInstallRefusesAKindItDoesNotOwnAndAStoreTheUnitCannotLogInTo(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	env := map[string]string{"NOVA_SPRINT_REDIS": "127.0.0.1:6380", "NOVA_SPRINT_REDIS_USER": "coordinator"}
	a := newApp(func(k string) string { return env[k] })
	a.goos = "linux"
	a.home = func() (string, error) { return home, nil }
	a.executable = func() (string, error) { return "/opt/nova/bin/nova-sprint", nil }
	a.seatLoad = func(string, string, string) error { t.Fatal("a refused install loads a unit"); return nil }
	for _, tc := range []struct {
		verb func(*app, []string, io.Writer, io.Writer) int
		args []string
		want string
	}{
		{(*app).cmdInstall, nil, "install wants the unit's kind first: server|member|seat-push|friend-sync|table"},
		{(*app).cmdInstall, []string{"store"}, "the store unit is nova-redis's to install; run: nova-redis install store"},
		{(*app).cmdUninstall, []string{"disk-guard"}, "run: nova-swarm uninstall disk-guard"},
		{(*app).cmdInstall, []string{"nope"}, "no unit kind nope"},
		{(*app).cmdInstall, []string{"server"}, "--listen <address:port> is required"},
		{(*app).cmdInstall, []string{"server", "--listen", "127.0.0.1:6390"}, "nova-sprint seat login --redis 127.0.0.1:6380 --user coordinator"},
		{(*app).cmdInstall, []string{"table", "--out", "/srv/t.txt", "--redis", "mem:0"}, "the in-memory twin"},
		{(*app).cmdInstall, []string{"member", "--as", "m1"}, "--server <address:port> are required"},
		{(*app).cmdUnits, nil, "units wants --check"},
	} {
		var out, errb bytes.Buffer
		code := tc.verb(a, tc.args, &out, &errb)
		assert.Equal(t, 2, code, "%v: %s", tc.args, errb.String())
		assert.Contains(t, errb.String(), tc.want, tc.args)
	}
	entries, _ := os.ReadDir(filepath.Join(home, ".config", "systemd", "user"))
	assert.Empty(t, entries, "nothing was written")
}

// A server installed to clear a server-actor drift runs as the holder the
// command's own --actor names, not as the caller's own NOVA_SPRINT_ACTOR, whose
// value would only recreate the drift (docs/SPEC-DOCTOR.md, seat-agreement).
func TestInstallServerUnitActorFollowsTheExplicitActor(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	env := map[string]string{"NOVA_SPRINT_REDIS": "127.0.0.1:6380", "NOVA_SPRINT_ACTOR": "bob"}
	a := newApp(func(k string) string { return env[k] })
	a.goos = "linux"
	a.home = func() (string, error) { return home, nil }
	a.executable = func() (string, error) { return "/opt/nova/bin/nova-sprint", nil }
	a.seatLoad = func(string, string, string) error { return nil }
	var out, errb bytes.Buffer
	code := (*app).cmdInstall(a, []string{"server", "--listen", "127.0.0.1:6390", "--actor", "ada"}, &out, &errb)
	require.Equal(t, 0, code, errb.String())
	dir := sprint.UnitDir("linux", home, func(string) string { return "" })
	k, ok := sprint.UnitKindOf("server")
	require.True(t, ok)
	b, err := os.ReadFile(filepath.Join(dir, k.File("linux")))
	require.NoError(t, err)
	assert.Contains(t, string(b), "NOVA_SPRINT_ACTOR=ada", "the unit's actor is the explicit --actor")
	assert.NotContains(t, string(b), "NOVA_SPRINT_ACTOR=bob", "the caller's environment does not override the explicit actor")
}
