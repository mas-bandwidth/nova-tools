package up_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/up"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeMachine is a machine with the programs a setup runs, each a strict
// stand-in that refuses what the real one refuses where a step relies on it,
// and writes only under the home it is given (a t.TempDir()). It records
// every command and every password it was handed.
type fakeMachine struct {
	mu        sync.Mutex
	home      string
	absent    map[string]bool
	calls     []up.Cmd
	sealed    map[string]bool
	passwords []string
}

func newFake(t *testing.T, absent ...string) *fakeMachine {
	f := &fakeMachine{home: t.TempDir(), absent: map[string]bool{}, sealed: map[string]bool{}}
	for _, a := range absent {
		f.absent[a] = true
	}
	return f
}

func (f *fakeMachine) machine(goos string) func() (up.Machine, error) {
	return func() (up.Machine, error) {
		return up.Machine{GOOS: goos, Home: f.home, UID: 501, Exec: f,
			Now:  func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) },
			Rand: strings.NewReader(strings.Repeat("0123456789abcdef", 64))}, nil
	}
}

func (f *fakeMachine) LookPath(name string) (string, error) {
	if f.absent[name] {
		return "", errors.New("executable file not found in $PATH")
	}
	return "/fake/bin/" + name, nil
}

func env(c up.Cmd, k string) string {
	for _, kv := range c.Env {
		if v, ok := strings.CutPrefix(kv, k+"="); ok {
			return v
		}
	}
	return ""
}

func arg(args []string, flag string) string {
	if i := slices.Index(args, flag); i >= 0 && i+1 < len(args) {
		return args[i+1]
	}
	return ""
}

func (f *fakeMachine) Run(_ context.Context, c up.Cmd) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	name, a := filepath.Base(c.Name), c.Args
	if f.absent[name] {
		return "", errors.New("not found")
	}
	if len(a) == 1 && (a[0] == "version" || a[0] == "--version") {
		return name + " version 1.2.0\n", nil
	}
	switch name {
	case "git":
		if a[0] == "init" {
			dir := a[len(a)-1]
			if !slices.Contains(a, "--bare") {
				dir = filepath.Join(dir, ".git")
			}
			return "", os.MkdirAll(dir, 0o755)
		}
		return "", nil
	case "nova-sprint":
		twin, ok := strings.CutPrefix(env(c, "NOVA_SPRINT_REDIS"), "mem:")
		if !ok || env(c, "NOVA_SPRINT_ACTOR") == "" {
			return "", errors.New("nova-sprint: no mem: store or no actor")
		}
		if _, err := os.Stat(twin); err != nil && a[0] != "init" {
			return "", errors.New("nova-sprint: the twin is not initialized")
		}
		b, _ := os.ReadFile(twin) // ignored: a twin that is not there is begun by init
		b = append(b, strings.Join(a, " ")+"\n"...)
		if err := os.WriteFile(twin, b, 0o644); err != nil {
			return "", err
		}
		if a[0] == "tick" && bytes.Contains(b, []byte("merge --stream s1")) {
			return "TICK OK state=RUNNING\n1/1 100.0% -> ETA -  machine: running\n", nil
		}
		return "OK\n0/1 0.0% -> ETA -  machine: running\n", nil
	case "nova-secrets":
		switch a[0] {
		case "keygen":
			key := arg(a, "--key")
			if st, err := os.Stat(filepath.Dir(key)); err != nil || st.Mode().Perm() != 0o700 {
				return "", fmt.Errorf("keygen: %s is not a 0700 directory", filepath.Dir(key))
			}
			return "KEYGEN OK\n", os.WriteFile(key, []byte("# created: 2026\n# public key: age1fakepub\nAGE-SECRET-KEY-FAKE\n"), 0o600)
		case "names":
			if _, err := os.Stat(filepath.Join(arg(a, "--store"), ".git")); err != nil {
				return "", errors.New("names: no store")
			}
			var out strings.Builder
			for n := range f.sealed {
				fmt.Fprintf(&out, "SECRETS NAME key=%s clear=false\n", n)
			}
			return out.String() + "SECRETS NAMES OK\n", nil
		case "seal":
			if c.Stdin == "" || !slices.Contains(a, "--stdin") {
				return "", errors.New("seal: no value on stdin")
			}
			f.sealed[arg(a, "--name")] = true
			f.passwords = append(f.passwords, c.Stdin)
			return fmt.Sprintf("SECRETS SEAL OK name=%s seat=coordinator committed branch=seal/coordinator-%s-20261004-120000\n", arg(a, "--name"), arg(a, "--name")), nil
		case "exec":
			for _, n := range strings.Split(arg(a, "--only"), ",") {
				if !f.sealed[n] {
					return "", fmt.Errorf("exec: %s is not sealed", n)
				}
			}
			return "SECRETS EXEC OK\n", nil
		}
	case "launchctl", "systemctl":
		return "", nil
	}
	return "", fmt.Errorf("the fake machine has no program %s %v", name, a)
}

// writes is every recorded command that is no inspection: not a version, not names.
func (f *fakeMachine) writes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var w []string
	for _, c := range f.calls {
		if len(c.Args) == 1 && (c.Args[0] == "version" || c.Args[0] == "--version") || len(c.Args) > 0 && c.Args[0] == "names" {
			continue
		}
		w = append(w, c.String())
	}
	return w
}

// nova runs nova-up on the fake machine and returns its exit and streams.
func (f *fakeMachine) nova(goos string, args ...string) (int, string, string) {
	var out, errs bytes.Buffer
	code := up.Tool("test", f.machine(goos)).Run(args, strings.NewReader(""), &out, &errs)
	return code, out.String(), errs.String()
}

func (f *fakeMachine) novaWithEnv(goos string, getenv func(string) string, args ...string) (int, string, string) {
	var out, errs bytes.Buffer
	code := up.ToolWithEnv("test", f.machine(goos), getenv).Run(args, strings.NewReader(""), &out, &errs)
	return code, out.String(), errs.String()
}

// tree is every path under dir with its contents, for comparing two runs.
func tree(t *testing.T, dir string) map[string]string {
	got := map[string]string{}
	require.NoError(t, filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		got[p] = string(b)
		return err
	}))
	return got
}

var stepNames = []string{"platform", "dirs", "binaries", "sprint", "secrets", "ssh", "redis", "seat", "smoke"}

func TestUpSeatWritesFleetEndpointsAndPreservesReferences(t *testing.T) {
	t.Parallel()
	config := t.TempDir()
	gh := filepath.Join(t.TempDir(), "gh")
	require.NoError(t, os.Mkdir(gh, 0o700))
	environment := map[string]string{
		"XDG_CONFIG_HOME":    config,
		"NOVA_SPRINT_REDIS":  "store.example:6380",
		"NOVA_SPRINT_SERVER": "sprint.example:6390",
		"NOVA_BUS_REDIS":     "bus.example:6381",
	}
	getenv := func(key string) string { return environment[key] }
	profileDir := filepath.Join(config, "nova-sprint")
	legacyPath := filepath.Join(profileDir, "seat.json")
	legacy := []byte(`{"server":"legacy-server","config_seat":"legacy-seat"}`)
	path := filepath.Join(profileDir, "seat-environment.json")
	code, out, errs := newFake(t).novaWithEnv("linux", getenv, "--seat")
	require.Equal(t, 0, code, out+errs)
	assert.NotContains(t, out+errs, "store.example")
	assert.NotContains(t, out+errs, "sprint.example")
	assert.NotContains(t, out+errs, "bus.example")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, float64(1), got["version"])
	assert.Equal(t, map[string]any{
		"NOVA_SPRINT_REDIS":  "store.example:6380",
		"NOVA_SPRINT_SERVER": "sprint.example:6390",
		"NOVA_BUS_REDIS":     "bus.example:6381",
	}, got["environment"])
	assert.Equal(t, os.FileMode(0o600), mustStat(t, path).Mode().Perm())
	assert.Equal(t, os.FileMode(0o700), mustStat(t, filepath.Dir(path)).Mode().Perm())
	require.NoError(t, os.WriteFile(legacyPath, legacy, 0o600))

	// A subsequent writer update must carry forward secret references and the
	// selected forge config without reading the forge directory's contents.
	profile := map[string]any{
		"version": 1,
		"environment": map[string]string{
			"NOVA_SPRINT_REDIS": "store.example:6380", "NOVA_SPRINT_SERVER": "sprint.example:6390", "NOVA_BUS_REDIS": "bus.example:6381",
		},
		"login": map[string]string{
			"redis": "store.example:6380", "user": "coordinator", "store": "/secrets", "as": "seat",
			"key": "/key", "sops": "sops", "secret": "NOVA_REDIS_COORDINATOR_PASSWORD",
		},
		"forge_identity": gh,
	}
	old, err := json.Marshal(profile)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, old, 0o600))
	environment["NOVA_SPRINT_REDIS"] = "ignored.example:6380"
	code, out, errs = newFake(t).novaWithEnv("linux", getenv, "--seat", "--sprint-redis", "store.example:6380")
	require.Equal(t, 0, code, out+errs)
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	got = nil
	require.NoError(t, json.Unmarshal(data, &got))
	require.IsType(t, map[string]any{}, got["login"])
	login := got["login"].(map[string]any)
	assert.Equal(t, "coordinator", login["user"])
	assert.Equal(t, "NOVA_REDIS_COORDINATOR_PASSWORD", login["secret"])
	assert.Equal(t, gh, got["forge_identity"])
	assert.Equal(t, "store.example:6380", got["environment"].(map[string]any)["NOVA_SPRINT_REDIS"])
	legacyAfter, err := os.ReadFile(legacyPath)
	require.NoError(t, err)
	assert.Equal(t, legacy, legacyAfter, "legacy seat.json remains unchanged")

	// A validation refusal includes the parser's safe cause and leaves the
	// malformed prior record untouched for repair.
	invalid := []byte(`{"version":1,"unexpected":true}`)
	require.NoError(t, os.WriteFile(path, invalid, 0o600))
	code, out, errs = newFake(t).novaWithEnv("linux", getenv, "--seat")
	assert.Equal(t, 2, code, out+errs)
	assert.Contains(t, errs, "existing seat environment could not be validated (seat environment is invalid: unknown key)")
	data, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, invalid, data)
}

func TestUpSeatRefusesMissingOrCredentialBearingEndpoint(t *testing.T) {
	t.Parallel()
	config := t.TempDir()
	environment := map[string]string{
		"XDG_CONFIG_HOME":    config,
		"NOVA_SPRINT_REDIS":  "",
		"NOVA_SPRINT_SERVER": "sprint.example:6390",
		"NOVA_BUS_REDIS":     "bus.example:6381",
	}
	getenv := func(key string) string { return environment[key] }
	code, out, errs := newFake(t).novaWithEnv("linux", getenv, "--seat")
	assert.Equal(t, 2, code, out+errs)
	assert.Contains(t, errs, "missing fleet endpoints NOVA_SPRINT_REDIS")
	assert.NoFileExists(t, filepath.Join(config, "nova-sprint", "seat-environment.json"))

	code, out, errs = newFake(t).novaWithEnv("linux", getenv, "--seat", "--sprint-redis", "redis://user:password@example:6380")
	assert.Equal(t, 2, code, out+errs)
	assert.Contains(t, errs, "invalid password-free host:port endpoints NOVA_SPRINT_REDIS")
	assert.NotContains(t, out+errs, "user:password@example")
	assert.NoFileExists(t, filepath.Join(config, "nova-sprint", "seat-environment.json"))
	code, out, errs = newFake(t).novaWithEnv("linux", getenv, "--seat", "--sprint-redis", "mem:/tmp/local.twin")
	assert.Equal(t, 2, code, out+errs)
	assert.Contains(t, errs, "invalid password-free host:port endpoints NOVA_SPRINT_REDIS")
	assert.NoFileExists(t, filepath.Join(config, "nova-sprint", "seat-environment.json"))

	environment["NOVA_SPRINT_REDIS"] = "store.example:6380"
	code, out, errs = newFake(t).novaWithEnv("linux", getenv, "--seat", "--dry-run", "--sprint-redis", "store.example:6380")
	assert.Equal(t, 0, code, out+errs)
	assert.Contains(t, out, "dry run: seat environment validated; nothing written")
	assert.NoFileExists(t, filepath.Join(config, "nova-sprint", "seat-environment.json"))

	unsafeDir := filepath.Join(config, "nova-sprint")
	require.NoError(t, os.MkdirAll(unsafeDir, 0o700))
	require.NoError(t, os.Chmod(unsafeDir, 0o777))
	code, out, errs = newFake(t).novaWithEnv("linux", getenv, "--seat")
	assert.Equal(t, 2, code, out+errs)
	assert.Contains(t, errs, "directory is not safe to write")
	assert.Contains(t, errs, "directory is writable by another user")
	assert.NoFileExists(t, filepath.Join(unsafeDir, "seat-environment.json"))
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	st, err := os.Stat(path)
	require.NoError(t, err)
	return st
}

// upLines is the UP <step> lines of an output, the step and state of each.
func upLines(out string) [][2]string {
	var got [][2]string
	for _, l := range strings.Split(out, "\n") {
		w := strings.Fields(l)
		if len(w) >= 3 && w[0] == "UP" && slices.Contains(stepNames, w[1]) {
			got = append(got, [2]string{w[1], w[2]})
		}
	}
	return got
}

// TestUpLocalPlansAWorkingSingleMachineWithNoConfig pins the card: with no
// config, nova-up --local plans and applies, on a fake machine, everything a
// first sprint needs (the twin, the secrets store and its seat key, the
// loopback Redis loop with its passwords sealed, the seat, a landed smoke),
// prints one line per step and what it found, never prints a password, and a
// second run changes nothing and says so.
func TestUpLocalPlansAWorkingSingleMachineWithNoConfig(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ goos, unit, load string }{
		{"darwin", "Library/LaunchAgents/com.nova.loop.redis-local.plist", "launchctl bootstrap gui/501"},
		{"linux", ".config/systemd/user/nova-loop-redis-local.service", "systemctl --user enable --now nova-loop-redis-local.service"},
	} {
		t.Run(c.goos, func(t *testing.T) {
			t.Parallel()
			f := newFake(t)
			root := filepath.Join(f.home, "nova")

			code, out, errs := f.nova(c.goos, "--local")
			require.Equal(t, 0, code, "first run: %s%s", out, errs)
			assert.True(t, strings.HasPrefix(out, "UP OK root="+root+" steps=9 changes=7 applied=7"), out)
			assert.Equal(t, [][2]string{{"platform", "ok"}, {"dirs", "create"}, {"binaries", "ok"}, {"sprint", "create"},
				{"secrets", "create"}, {"ssh", "create"}, {"redis", "create"}, {"seat", "create"}, {"smoke", "create"}}, upLines(out))

			for _, p := range []string{"stores/sprint.twin", "keys/coordinator.key", "secrets/.git", "secrets/.sops.yaml", "secrets.git",
				"stores/redis/acl.applied", "seat.env", "smoke/repo/.git", "smoke/landed"} {
				_, err := os.Stat(filepath.Join(root, p))
				assert.NoError(t, err, "%s is there", p)
			}
			st, err := os.Stat(filepath.Join(root, "keys"))
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o700), st.Mode().Perm(), "keys/ is the login's alone")
			unit, err := os.ReadFile(filepath.Join(f.home, c.unit))
			require.NoError(t, err, "the redis loop's unit is in the service manager's directory")
			assert.Contains(t, string(unit), "127.0.0.1", "the store binds loopback")
			assert.Contains(t, string(unit), filepath.Join(root, "stores/redis"))
			seat, err := os.ReadFile(filepath.Join(root, "seat.env"))
			require.NoError(t, err)
			assert.Contains(t, string(seat), "NOVA_SPRINT_REDIS='mem:"+filepath.Join(root, "stores/sprint.twin")+"'")
			assert.Contains(t, string(seat), "NOVA_SPRINT_ACTOR='coordinator'")
			landed, err := os.ReadFile(filepath.Join(root, "smoke/landed"))
			require.NoError(t, err)
			assert.Equal(t, "2026-10-04T12:00:00Z 1/1 100.0% -> ETA -  machine: running\n", string(landed))

			writes := strings.Join(f.writes(), "\n")
			assert.Contains(t, writes, c.load, "the unit is loaded by the platform's service manager")
			assert.Contains(t, writes, "nova-redis acl apply --addr 127.0.0.1:6390")
			assert.Contains(t, writes, "nova-redis fn load --addr 127.0.0.1:6390 --user coordinator")
			assert.Len(t, f.passwords, 4, "one password per ACL user, each sealed")
			for _, pw := range f.passwords {
				assert.NotContains(t, out+errs, pw, "a password is never printed")
				assert.NotContains(t, writes, pw, "a password is in no argument")
				for p, body := range tree(t, f.home) {
					assert.NotContains(t, body, pw, "a password is in no file nova-up writes: %s", p)
				}
			}

			before, calls := tree(t, f.home), len(f.writes())
			code, out, errs = f.nova(c.goos, "--local")
			require.Equal(t, 0, code, "second run: %s%s", out, errs)
			assert.True(t, strings.HasPrefix(out, "UP UNCHANGED root="+root+" steps=9 changes=0 applied=0"), out)
			assert.Contains(t, out, "UP NOTE nothing to change")
			for _, l := range upLines(out) {
				assert.Equal(t, "ok", l[1], "second run, step %s", l[0])
			}
			assert.Len(t, upLines(out), len(stepNames))
			assert.Equal(t, before, tree(t, f.home), "a second run writes nothing")
			assert.Equal(t, calls, len(f.writes()), "a second run runs nothing but inspections")
		})
	}
}

// TestUpLocalDryRunWritesNothing: --dry-run prints the plan the real run
// would take, from the same code path, and writes nothing.
func TestUpLocalDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	code, out, errs := f.nova("darwin", "--local", "--dry-run")
	require.Equal(t, 0, code, out+errs)
	assert.Contains(t, out, "dry_run=true")
	assert.Equal(t, [][2]string{{"platform", "ok"}, {"dirs", "create"}, {"binaries", "ok"}, {"sprint", "create"},
		{"secrets", "create"}, {"ssh", "create"}, {"redis", "create"}, {"seat", "create"}, {"smoke", "create"}}, upLines(out))
	assert.Empty(t, tree(t, f.home), "a dry run writes no file")
	assert.NoDirExists(t, filepath.Join(f.home, "nova"))
	assert.Empty(t, f.writes(), "a dry run runs nothing but inspections")
}

// TestUpLocalStopsOnWhatItCannotProvide: a missing program or an unsupported
// system is one plain line with what provides it, exit 1, and nothing applied.
func TestUpLocalStopsOnWhatItCannotProvide(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, goos string
		absent     []string
		line       string
	}{
		{"a program on darwin", "darwin", []string{"sops", "nova-bus"},
			"UP binaries missing sops,nova-bus not on PATH or not answering its version; install: brew install sops && go install "},
		{"a program on linux", "linux", []string{"redis-server"},
			"UP binaries missing redis-server not on PATH or not answering its version; install: sudo apt-get install -y redis-server"},
		{"an unsupported system", "windows", nil,
			"UP platform missing windows is not supported; nova-up --local runs on darwin or linux (on windows: wsl --install, then run it inside)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			f := newFake(t, c.absent...)
			code, out, errs := f.nova(c.goos, "--local")
			assert.Equal(t, 1, code, out+errs)
			assert.Contains(t, errs, "\n"+c.line)
			assert.Contains(t, errs, "nothing was applied")
			if slices.Contains(c.absent, "nova-bus") {
				assert.Regexp(t, `go install (\S+/cmd/nova-bus@latest|\./cmd/nova-bus )`, errs, "a tool of this repository installs from its cmd/")
			}
			assert.Empty(t, tree(t, f.home), "nothing half-applied")
			assert.Empty(t, f.writes())
		})
	}
}

// TestUpRefusesWhatItCannotRun: the bare command and a run without --local
// are refusals at exit 2 naming what is wanted, and the definition meets the
// standard pkg/tool holds every tool to.
func TestUpRefusesWhatItCannotRun(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"bare", nil, "UP REFUSED: no verb and no file given"},
		{"no mode", []string{"up"}, "UP REFUSED: choose exactly one mode"},
		{"a positional", []string{"up", "--local", "extra"}, "takes no positional arguments"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			code, out, errs := f.nova("linux", c.args...)
			assert.Equal(t, 2, code, out+errs)
			assert.Contains(t, errs, c.want)
		})
	}
	assert.Empty(t, up.Tool("test", f.machine("linux")).Problems())
	var names []string
	for _, s := range up.Steps() {
		names = append(names, s.Name)
	}
	assert.Equal(t, stepNames, names, "the steps, in order (docs/SPEC-UP.md)")
}
