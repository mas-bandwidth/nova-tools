package units

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnitsUnitsCoverUnitKindOf(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want UnitKind
		ok   bool
	}{
		{"known store", "store", UnitKinds[0], true},
		{"unknown", "no-such", UnitKind{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			k, ok := UnitKindOf(tt.in)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, k)
		})
	}
}

func TestUnitsUnitsCoverUnitKindNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		tool string
		want []string
	}{
		{"nova-sprint", "nova-sprint", []string{"server", "member", "seat-push", "friend-sync", "table"}},
		{"nova-redis", "nova-redis", []string{"store", "bus"}},
		{"nova-swarm", "nova-swarm", []string{"disk-guard", "mirror-refresh"}},
		{"unknown", "unknown-tool", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := UnitKindNames(tt.tool)
			assert.Equal(t, tt.want, out)
		})
	}
}

func TestUnitsUnitsCoverFile(t *testing.T) {
	t.Parallel()
	k := UnitKinds[0]
	tests := []struct {
		name string
		goos string
		want string
	}{
		{"darwin", "darwin", "nova-redis.store.plist"},
		{"linux", "linux", "nova-redis-store.service"},
		{"unknown", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := k.File(tt.goos)
			assert.Equal(t, tt.want, out)
		})
	}
}

func TestUnitsUnitsCoverRefusal(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		u    ServiceUnit
		want string
	}{
		{"no service manager",
			ServiceUnit{Kind: UnitKinds[0], OS: ""},
			"nova-redis install store installs a launchd agent (macOS) or a systemd user unit (Linux), not a service on -; run the verb by hand: nova-redis serve"},
		{"relative first arg",
			ServiceUnit{Kind: UnitKinds[0], OS: "linux", Args: []string{"nova-redis"}, Env: [][2]string{{"REDIS_PASSWORD", "secret"}}},
			"the unit runs nova-redis by its absolute path, not nova-redis"},
		{"wrapper binary",
			ServiceUnit{Kind: UnitKinds[0], OS: "linux", Args: []string{"/usr/bin/nova-secrets"}, Env: [][2]string{{"REDIS_PASSWORD", "secret"}}},
			"the unit runs nova-redis itself, never a wrapper around it, and nova-secrets is not nova-redis"},
		{"wrong verb",
			ServiceUnit{Kind: UnitKinds[0], OS: "linux", Args: []string{"/usr/bin/nova-redis", "run"}},
			"a store unit runs nova-redis serve, not nova-redis run"},
		{"negative Every",
			ServiceUnit{Kind: UnitKinds[0], OS: "linux", Args: []string{"/usr/bin/nova-redis", "serve"}, Every: -1 * time.Second},
			"--every is the least time between two starts, zero or more, not -1s"},
		{"env name invalid",
			ServiceUnit{Kind: UnitKinds[0], OS: "linux", Args: []string{"/usr/bin/nova-redis", "serve"}, Env: [][2]string{{"1BAD", "x"}}},
			"the unit's environment names 1BAD, which is no variable's name"},
		{"_ENV value invalid",
			ServiceUnit{Kind: UnitKinds[0], OS: "linux", Args: []string{"/usr/bin/nova-redis", "serve"}, Env: [][2]string{{"REDIS_PASSWORD_ENV", "!notvar"}}},
			"REDIS_PASSWORD_ENV does not name a variable (letters, digits and underscores only), and the unit carries no secret"},
		{"secret refused",
			ServiceUnit{Kind: UnitKinds[0], OS: "linux", Args: []string{"/usr/bin/nova-redis", "serve"}, Env: [][2]string{{"REDIS_PASSWORD", "x"}}},
			"the unit carries no secret, and REDIS_PASSWORD is one; the tool reads it in its own process (nova-sprint seat login, nova-redis serve --secret)"},
		{"_ENV accepted",
			ServiceUnit{Kind: UnitKinds[0], OS: "linux", Args: []string{"/usr/bin/nova-redis", "serve"}, Env: [][2]string{{"REDIS_PASSWORD_ENV", "X"}}},
			""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := tt.u.refusal()
			assert.Equal(t, tt.want, out)
		})
	}
}

func TestUnitsUnitsCoverThrottle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		every time.Duration
		want  int
	}{
		{"zero", 0, 10},
		{"1500ms", 1500 * time.Millisecond, 2},
		{"30s", 30 * time.Second, 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			u := ServiceUnit{Every: tt.every}
			out := u.throttle()
			assert.Equal(t, tt.want, out)
		})
	}
}

func TestUnitsUnitsCoverSystemdUnit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		restart        int
		wantStartLimit bool
	}{
		{"restart over 10", 15, true},
		{"restart 10 or less", 10, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := SystemdUnit("desc", []string{"/usr/bin/foo", "arg"}, [][2]string{}, tt.restart)
			if tt.wantStartLimit {
				assert.Contains(t, out, "StartLimitIntervalSec=0")
			} else {
				assert.NotContains(t, out, "StartLimitIntervalSec=0")
			}
		})
	}
}

func TestUnitsUnitsCoverLaunchdPlist(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		env     [][2]string
		log     string
		wantEnv bool
		wantLog bool
	}{
		{"with env", [][2]string{{"FOO", "x"}}, "", true, false},
		{"with log", [][2]string{}, "/var/log/out.txt", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := LaunchdPlist("label", []string{"/usr/bin/foo"}, tt.env, tt.log, 15)
			if tt.wantEnv {
				assert.Contains(t, out, "EnvironmentVariables")
			} else {
				assert.NotContains(t, out, "EnvironmentVariables")
			}
			if tt.wantLog {
				assert.Contains(t, out, "StandardOutPath")
			} else {
				assert.NotContains(t, out, "StandardOutPath")
			}
		})
	}
}

func TestUnitsUnitsCoverSdQuote(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"space", "hello world", `"hello world"`},
		{"quote", `say "hi"`, `"say \"hi\""`},
		{"backslash", `path\to`, `"path\\to"`},
		{"percent", "100%", `"100%%"`},
		{"dollar", "$FOO", `"$$FOO"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := sdQuote(tt.in)
			assert.Equal(t, tt.want, out)
		})
	}
}

func TestUnitsUnitsCoverSdSplit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want []string
		ok   bool
	}{
		{"space", `"hello world"`, []string{"hello world"}, true},
		{"backslash", `"say \"hi\""`, []string{`say "hi"`}, true},
		{"percent", `"100%%"`, []string{"100%"}, true},
		{"dollar", `$$FOO`, []string{"$FOO"}, true},
		{"open quote", `"hello`, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, err := sdSplit(tt.in)
			if tt.ok {
				require.NoError(t, err)
				assert.Equal(t, tt.want, out)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestUnitsUnitsCoverSystemdArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want []string
		ok   bool
	}{
		{"has ExecStart", "[Unit]\nExecStart=/usr/bin/foo arg", []string{"/usr/bin/foo", "arg"}, true},
		{"no ExecStart", "[Unit]", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, err := systemdArgs(tt.in)
			if tt.ok {
				require.NoError(t, err)
				assert.Equal(t, tt.want, out)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestUnitsUnitsCoverPlistArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want []string
		ok   bool
	}{
		{"has ProgramArguments", `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist><plist version="1.0"><dict><key>ProgramArguments</key><array><string>/usr/bin/foo</string><string>arg</string></array></dict></plist>`, []string{"/usr/bin/foo", "arg"}, true},
		{"no ProgramArguments", `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist><plist version="1.0"><dict><key>Label</key><string>test</string></dict></plist>`, nil, false},
		{"malformed XML", `<bad`, nil, false},
		{"nested array ignored", `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE plist><plist version="1.0"><dict><key>ProgramArguments</key><array><array><string>foo</string></array></array></dict></plist>`, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, err := plistArgs([]byte(tt.in))
			if tt.ok {
				require.NoError(t, err)
				assert.Equal(t, tt.want, out)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestUnitsUnitsCoverInstallerWrite(t *testing.T) {
	t.Parallel()
	t.Run("new file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		var loadCalled int
		in := Installer{
			Dir: dir,
			Load: func(p string) error {
				loadCalled++
				return nil
			},
		}
		path := filepath.Join(dir, "a.txt")
		out, err := in.Write(path, "hello")
		require.NoError(t, err)
		assert.True(t, out.Changed)
		assert.Equal(t, 1, loadCalled)
	})
	t.Run("same text again", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		var loadCalled int
		in := Installer{
			Dir: dir,
			Load: func(p string) error {
				loadCalled++
				return nil
			},
		}
		path := filepath.Join(dir, "a.txt")
		err := os.WriteFile(path, []byte("hello"), 0o644)
		require.NoError(t, err)
		out, err := in.Write(path, "hello")
		require.NoError(t, err)
		assert.False(t, out.Changed)
		assert.Equal(t, 1, loadCalled)
	})
	t.Run("load error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		inError := Installer{
			Dir: dir,
			Load: func(p string) error {
				return os.ErrNotExist
			},
		}
		path := filepath.Join(dir, "err.txt")
		_, err := inError.Write(path, "test")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "did not load")
		_, err = os.Stat(path)
		assert.NoError(t, err)
	})
}

func TestUnitsUnitsCoverInstallerRemove(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var unloadCalled int
	in := Installer{
		Dir: dir,
		Unload: func(p string) error {
			unloadCalled++
			return nil
		},
	}
	tests := []struct {
		name             string
		path             string
		wantChanged      bool
		wantUnloadCalled int
	}{
		{"no file", "missing.txt", false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, err := in.Remove(tt.path)
			require.NoError(t, err)
			assert.Equal(t, tt.wantChanged, out.Changed)
			assert.Equal(t, tt.wantUnloadCalled, unloadCalled)
		})
	}
	// File with Unload error
	path := filepath.Join(dir, "test.txt")
	err := os.WriteFile(path, []byte("hello"), 0o644)
	require.NoError(t, err)
	inError := Installer{
		Dir: dir,
		Unload: func(p string) error {
			return os.ErrNotExist
		},
	}
	_, err = inError.Remove(path)
	require.Error(t, err)
	_, err = os.Stat(path)
	assert.NoError(t, err)
	// Success
	inOK := Installer{
		Dir: dir,
		Unload: func(p string) error {
			return nil
		},
	}
	out, err := inOK.Remove(path)
	require.NoError(t, err)
	assert.True(t, out.Changed)
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err))
}

func TestUnitsUnitsCoverInstallerInstall(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	loadCalled := 0
	in := Installer{
		Dir: dir,
		Load: func(p string) error {
			loadCalled++
			return nil
		},
	}
	u := ServiceUnit{
		Kind: UnitKinds[0],
		OS:   "linux",
		Args: []string{"/usr/bin/nova-redis", "serve"},
	}
	tests := []struct {
		name string
		u    ServiceUnit
		ok   bool
	}{
		{"bad unit", ServiceUnit{Kind: UnitKinds[0], OS: "linux", Args: []string{"nova-redis"}}, false},
		{"good unit", u, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := in.Install(tt.u)
			if tt.ok {
				require.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestUnitsUnitsCoverInstallerUninstall(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	unloadCalled := 0
	in := Installer{
		Dir: dir,
		Unload: func(p string) error {
			unloadCalled++
			return nil
		},
	}
	tests := []struct {
		name string
		goos string
		ok   bool
	}{
		{"no manager", "", false},
		{"linux", "linux", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := in.Uninstall(UnitKinds[0], tt.goos)
			if tt.ok {
				require.NoError(t, err)
			} else {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "uninstall")
			}
		})
	}
}

func TestUnitsUnitsCoverCheckUnits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		files     map[string]string
		wantState string
	}{
		{"missing", map[string]string{}, "missing"},
		{"installed", map[string]string{"nova-redis-store.service": "[Unit]\nExecStart=/usr/bin/nova-redis serve\n[Service]\nRestart=always\n[Install]\nWantedBy=default.target\n"}, "installed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			kinds := []UnitKind{UnitKinds[0]}
			for name, content := range tt.files {
				path := filepath.Join(dir, name)
				err := os.WriteFile(path, []byte(content), 0o644)
				require.NoError(t, err)
			}
			out, err := CheckUnits(dir, "linux", kinds)
			require.NoError(t, err)
			require.Len(t, out, 1)
			assert.Equal(t, tt.wantState, out[0].State)
		})
	}
}

func TestUnitsUnitsCoverCheckUnitsDifferent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		content   string
		wantState string
	}{
		{"wrapper", "[Unit]\nExecStart=/usr/bin/nova-secrets exec nova-redis serve\n[Service]\nRestart=always\n[Install]\nWantedBy=default.target\n", "different"},
		{"another verb", "[Unit]\nExecStart=/usr/bin/nova-redis run\n[Service]\nRestart=always\n[Install]\nWantedBy=default.target\n", "different"},
		{"runs nothing", "", "different"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "nova-redis-store.service")
			err := os.WriteFile(path, []byte(tt.content), 0o644)
			require.NoError(t, err)
			out, err := CheckUnits(dir, "linux", []UnitKind{UnitKinds[0]})
			require.NoError(t, err)
			assert.Equal(t, tt.wantState, out[0].State)
		})
	}
}

func TestUnitsUnitsCoverCheckUnitsNoManager(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out, err := CheckUnits(dir, "", []UnitKind{UnitKinds[0]})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "has neither")
	assert.Nil(t, out)
}

func TestUnitsUnitsCoverText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		u    ServiceUnit
		want bool
	}{
		{"linux", ServiceUnit{Kind: UnitKinds[0], OS: "linux", Args: []string{"/usr/bin/nova-redis", "serve"}}, true},
		{"darwin", ServiceUnit{Kind: UnitKinds[0], OS: "darwin", Args: []string{"/usr/bin/nova-redis", "serve"}}, true},
		{"refusal", ServiceUnit{Kind: UnitKinds[0], OS: "", Args: []string{"/usr/bin/nova-redis", "serve"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, err := tt.u.Text()
			if tt.want {
				require.NoError(t, err)
				assert.NotEmpty(t, out)
			} else {
				assert.Error(t, err)
				assert.Empty(t, out)
			}
		})
	}
}

func TestUnitsUnitsCoverOrDash(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "-"},
		{"non-empty", "hello", "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := orDash(tt.in)
			assert.Equal(t, tt.want, out)
		})
	}
}

func TestUnitsUnitsCoverHasPrefix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		words  []string
		prefix []string
		want   bool
	}{
		{"match", []string{"serve"}, []string{"serve"}, true},
		{"no match", []string{"run"}, []string{"serve"}, false},
		{"empty prefix", []string{"serve"}, []string{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out := hasPrefix(tt.words, tt.prefix)
			assert.Equal(t, tt.want, out)
		})
	}
}
