package sprint

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/units"
)

// Every unit a running sprint needs is written and loaded by a nova verb, each running
// its tool's verb itself (no nova-secrets exec, shell or wrapper in the unit), for
// macOS and Linux; units --check names each needed unit installed, missing or
// different (card every-unit-installed-by-a-verb). The units go into a fake home and
// the loader is the test's: nothing is loaded on the machine running the test.
func TestEveryUnitASprintNeedsIsInstalledByAVerb(t *testing.T) {
	t.Parallel()
	verbs := map[string]string{}
	for _, k := range UnitKinds {
		verbs[k.Kind] = k.Install
	}
	assert.Equal(t, map[string]string{
		"store": "nova-redis install store", "bus": "nova-redis install bus",
		"server": "nova-sprint install server", "member": "nova-sprint install member",
		"seat-push": "nova-sprint install seat-push", "friend-sync": "nova-sprint install friend-sync",
		"table": "nova-sprint install table", "disk-guard": "nova-worker install disk-guard",
		"mirror-refresh": "nova-worker install mirror-refresh",
	}, verbs, "every unit the Studio ran by hand has its verb")
	assert.Equal(t, []string{"server", "member", "seat-push", "friend-sync", "table"}, UnitKindNames("nova-sprint"))
	assert.Equal(t, []string{"store", "bus"}, UnitKindNames("nova-redis"))
	assert.Equal(t, []string{"disk-guard", "mirror-refresh"}, UnitKindNames("nova-worker"))
	seen := map[string]bool{}
	for _, k := range UnitKinds {
		for _, f := range []string{k.File("darwin"), k.File("linux")} {
			assert.False(t, seen[f], "two kinds share the file %s", f)
			seen[f] = true
		}
	}

	for _, goos := range []string{"darwin", "linux"} {
		t.Run(goos, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			dir := filepath.Join(home, "units")
			var calls []string
			in := SeatInstaller{Dir: dir,
				Load:   func(p string) error { calls = append(calls, "load "+filepath.Base(p)); return nil },
				Unload: func(p string) error { calls = append(calls, "unload "+filepath.Base(p)); return nil }}

			got, err := CheckUnits(dir, goos, UnitKinds)
			require.NoError(t, err)
			for _, s := range got {
				assert.Equal(t, UnitMissing, s.State, "a fresh home has no %s unit", s.Kind)
			}

			for _, k := range UnitKinds {
				u := ServiceUnit{Kind: k, OS: goos, Args: append([]string{"/opt/nova/bin/" + k.Tool}, append(k.Verb, "--a b", `50%$"`)...),
					Env: [][2]string{{"NOVA_SPRINT_ACTOR", "seat"}, {"NOVA_PG_PASSWORD_ENV", "NOVA_PG_CONFIG_PASSWORD"}}, Every: 5 * time.Minute}
				r, err := in.InstallUnit(u)
				require.NoError(t, err, k.Kind)
				assert.Equal(t, SeatResult{Path: filepath.Join(dir, k.File(goos)), Changed: true}, r)
				b, err := os.ReadFile(r.Path)
				require.NoError(t, err)
				args, err := units.UnitArgs(goos, b)
				require.NoError(t, err)
				assert.Equal(t, u.Args, args, "the unit runs exactly the verb's line:\n%s", b)
				assert.NotContains(t, string(b), "nova-secrets", "no unit runs under nova-secrets exec")
				if goos == "darwin" {
					assert.Contains(t, string(b), "<key>ThrottleInterval</key>\n\t<integer>300</integer>")
				} else {
					assert.Contains(t, string(b), "RestartSec=300\n")
				}
				r, err = in.InstallUnit(u)
				require.NoError(t, err)
				assert.False(t, r.Changed, "the same unit is kept, and loaded again")
			}
			assert.Len(t, calls, 2*len(UnitKinds))

			got, err = CheckUnits(dir, goos, UnitKinds)
			require.NoError(t, err)
			for _, s := range got {
				assert.Equal(t, UnitInstalled, s.State, "%s: %s", s.Kind, s.Why)
			}

			// a unit uninstalled is missing; a hand-written wrapper in a unit's place is different
			server, _ := UnitKindOf("server")
			r, err := in.UninstallUnit(server, goos)
			require.NoError(t, err)
			assert.True(t, r.Changed)
			assert.NoFileExists(t, r.Path)
			assert.Equal(t, "unload "+server.File(goos), calls[len(calls)-1])
			r, err = in.UninstallUnit(server, goos)
			require.NoError(t, err)
			assert.False(t, r.Changed, "no unit there: nothing to do")

			store, _ := UnitKindOf("store")
			wrapped := []string{"/usr/local/bin/nova-secrets", "exec", "--only", "NOVA_REDIS_COORDINATOR_PASSWORD", "--", "/usr/local/bin/nova-redis", "serve"}
			var text string
			if goos == "linux" {
				text = systemdUnit("hand-written", wrapped, nil)
			} else {
				text = launchdPlist(store.Label, wrapped, nil, "")
			}
			require.NoError(t, os.WriteFile(filepath.Join(dir, store.File(goos)), []byte(text), 0o644))
			table, _ := UnitKindOf("table")
			require.NoError(t, os.WriteFile(filepath.Join(dir, table.File(goos)), []byte("not a unit"), 0o644))

			got, err = CheckUnits(dir, goos, UnitKinds)
			require.NoError(t, err)
			states := map[string]UnitState{}
			for _, s := range got {
				states[s.Kind] = s
			}
			assert.Equal(t, UnitMissing, states["server"].State)
			assert.Equal(t, "nova-sprint install server", states["server"].Install)
			assert.Equal(t, UnitDifferent, states["store"].State)
			assert.Equal(t, "it runs nova-secrets, not nova-redis serve itself", states["store"].Why)
			assert.Equal(t, UnitDifferent, states["table"].State)
			assert.Contains(t, states["table"].Why, "it is no unit this tool reads")
			assert.Equal(t, UnitInstalled, states["member"].State)
		})
	}
}

// A unit is refused, and nothing written, when it would run a wrapper, another verb,
// a relative binary or carry a secret, or where there is no service manager.
func TestAUnitRefusesAWrapperAVerbNotItsOwnAndASecret(t *testing.T) {
	t.Parallel()
	server, _ := UnitKindOf("server")
	ok := ServiceUnit{Kind: server, OS: "linux", Args: []string{"/opt/nova/bin/nova-sprint", "run", "--listen", "127.0.0.1:6390"}}
	_, err := ok.Text()
	require.NoError(t, err)
	for name, tc := range map[string]struct {
		mut  func(*ServiceUnit)
		want string
	}{
		"windows":  {func(u *ServiceUnit) { u.OS = "windows" }, "not a service on windows"},
		"relative": {func(u *ServiceUnit) { u.Args = []string{"nova-sprint", "run", "--listen", "x"} }, "by its absolute path"},
		"wrapper":  {func(u *ServiceUnit) { u.Args = []string{"/usr/bin/nova-secrets", "exec", "--", "nova-sprint", "run"} }, "never a wrapper"},
		"verb":     {func(u *ServiceUnit) { u.Args = []string{"/opt/nova/bin/nova-sprint", "where"} }, "runs nova-sprint run --listen, not nova-sprint where"},
		"secret":   {func(u *ServiceUnit) { u.Env = [][2]string{{"JEV_API_KEY", "x"}} }, "carries no secret, and JEV_API_KEY is one"},
		"password": {func(u *ServiceUnit) { u.Env = [][2]string{{"NOVA_REDIS_PASSWORD", "x"}} }, "carries no secret"},
		"env name": {func(u *ServiceUnit) { u.Env = [][2]string{{"NOVA_PG_PASSWORD_ENV", "x y"}} }, "does not name a variable"},
		"every":    {func(u *ServiceUnit) { u.Every = -time.Second }, "zero or more"},
	} {
		u := ok
		tc.mut(&u)
		_, err := u.Text()
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), tc.want, name)
		dir := t.TempDir()
		in := SeatInstaller{Dir: dir, Load: func(string) error { t.Fatal("a refused unit is loaded"); return nil }}
		_, err = in.InstallUnit(u)
		require.Error(t, err, name)
		entries, _ := os.ReadDir(dir)
		assert.Empty(t, entries, "%s: a refused unit writes nothing", name)
	}
	_, err = CheckUnits(t.TempDir(), "plan9", UnitKinds)
	assert.Error(t, err)
}
