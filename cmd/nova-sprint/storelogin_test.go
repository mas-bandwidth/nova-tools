package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/redisconn"
	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
)

// loginApp is a test app with the seat login on, as main has it: no --redis and no
// NOVA_SPRINT_REDIS* in its environment, its config under dir, its secrets a fake that
// holds one password, and its store the in-memory one, opened through the real path
// (redisBackend, storeOptions) and recorded at each dial with the user and the
// password its login resolves to.
type loginApp struct {
	*testApp
	env   map[string]string
	dials []dialedLogin
	reads []secrets.Login
}

type dialedLogin struct{ addr, user, password string }

const loginPW = "Zq7xWp4Lk9mN2vB8tY"

func newLoginApp(t *testing.T, dir string) *loginApp {
	la := &loginApp{testApp: newTestApp(t), env: map[string]string{"NOVA_SPRINT_ACTOR": "coordinator", "XDG_CONFIG_HOME": filepath.Join(dir, "config")}}
	la.a.getenv = func(k string) string { return la.env[k] }
	la.a.seatLoginOn()
	la.a.backend = la.a.redisBackend
	la.a.loginSecret = func(l secrets.Login) (secrets.Secret, error) {
		la.reads = append(la.reads, l)
		if l.Name != "NOVA_REDIS_COORDINATOR_PASSWORD" {
			return secrets.Secret{}, errors.New("seat " + l.As + " of store " + l.Store + " holds no " + l.Name)
		}
		return secrets.NewSecret(loginPW), nil
	}
	la.a.dialStore = func(_ context.Context, addr string, o redisconn.Options, getenv func(string) string, _ sprint.Names) (store.Backend, error) {
		r, err := redisconn.Resolve(o, getenv) // the login as redisconn.Open would make it
		require.NoError(t, err)
		pw := ""
		if r.PasswordEnv != "" {
			pw = getenv(r.PasswordEnv)
		}
		la.dials = append(la.dials, dialedLogin{addr, r.User, pw})
		return la.m, nil
	}
	return la
}

// seat-store-login-built-in: nova-sprint <verb> typed bare by the coordinator reaches the
// store with the coordinator login, the password read in process from nova-secrets and
// never printed, put in an environment or written to a file, with no wrapper script.
func TestABareVerbOpensTheStoreWithTheSeatLoginFromSecrets(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	la := newLoginApp(t, dir)
	file := filepath.Join(dir, "config", "nova-sprint", "login.json")
	var said []string
	do := func(line string) (int, string, string) {
		code, out, errs := la.do(line)
		said = append(said, out, errs)
		return code, out, errs
	}

	// bare, with nothing recorded: the refusal names the login as a way on
	code, _, errs := do("where")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "a login recorded by nova-sprint seat login")
	assert.Empty(t, la.dials)

	login := "seat login --store " + filepath.Join(dir, "secrets") + " --as studio --key " + filepath.Join(dir, "studio.key") +
		" --sops /usr/local/bin/sops --secret NOVA_REDIS_COORDINATOR_PASSWORD --user coordinator --redis 127.0.0.1:6380"
	code, out, errs := do(login)
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "SEAT LOGIN RECORDED file="+file)
	assert.Contains(t, out, "user=coordinator")
	assert.Contains(t, out, "resolves=yes")
	assert.Empty(t, la.dials, "seat login records; it opens no store")

	// the record holds where the password is, never the password, for its user alone
	b, err := os.ReadFile(file)
	require.NoError(t, err)
	var rec storeLogin
	require.NoError(t, json.Unmarshal(b, &rec))
	assert.Equal(t, storeLogin{Redis: "127.0.0.1:6380", User: "coordinator", Store: filepath.Join(dir, "secrets"), As: "studio",
		Key: filepath.Join(dir, "studio.key"), Sops: "/usr/local/bin/sops", Secret: "NOVA_REDIS_COORDINATOR_PASSWORD"}, rec)
	fi, err := os.Stat(file)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())

	// bare verbs now reach the recorded store as the recorded user, the password read
	// from the seat in this process
	code, _, errs = do("init --readers reader-a --members m1")
	require.Equal(t, 0, code, errs)
	code, _, errs = do("where")
	require.Equal(t, 0, code, errs)
	require.Len(t, la.dials, 1, "one dial per process and address")
	assert.Equal(t, dialedLogin{"127.0.0.1:6380", "coordinator", loginPW}, la.dials[0])
	require.NotEmpty(t, la.reads)
	assert.Equal(t, secrets.Login{Store: filepath.Join(dir, "secrets"), As: "studio", Key: filepath.Join(dir, "studio.key"), Sops: "/usr/local/bin/sops", Name: "NOVA_REDIS_COORDINATOR_PASSWORD"}, la.reads[len(la.reads)-1])

	code, out, errs = do("seat login --check")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "SEAT LOGIN file="+file+" redis=127.0.0.1:6380 user=coordinator")
	assert.Contains(t, out, "secret=NOVA_REDIS_COORDINATOR_PASSWORD resolves=yes")

	// an explicit --redis wins, and the login is not taken to another store
	code, _, errs = do("where --redis 127.0.0.1:7000")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, dialedLogin{"127.0.0.1:7000", "", ""}, la.dials[len(la.dials)-1])

	// the environment's login wins over the recorded one
	envd := newLoginApp(t, dir)
	envd.m = la.m
	envd.env["NOVA_SPRINT_REDIS_USER"] = "bench"
	envd.env["NOVA_SPRINT_REDIS_PASSWORD_ENV"] = "BENCH_PW"
	envd.env["BENCH_PW"] = "bench-pw"
	code, _, errs = envd.do("where")
	require.Equal(t, 0, code, errs)
	assert.Equal(t, []dialedLogin{{"127.0.0.1:6380", "bench", "bench-pw"}}, envd.dials)
	assert.Empty(t, envd.reads, "the secret is not read when the environment's login wins")

	// a secret the seat does not hold is a refusal naming the setting and the remedy,
	// never a login with no password
	rec.Secret = "NOVA_REDIS_GONE"
	b, err = json.Marshal(rec)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(file, b, 0o600))
	gone := newLoginApp(t, dir)
	code, _, errs = gone.do("where")
	said = append(said, errs)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "the seat login "+file)
	assert.Contains(t, errs, "NOVA_REDIS_GONE")
	assert.Contains(t, errs, "run: nova-sprint seat login --check")
	assert.Empty(t, gone.dials, "no store is opened without its password")
	code, out, errs = gone.do("seat login --check")
	said = append(said, out, errs)
	assert.Equal(t, 1, code)
	assert.Contains(t, out, "resolves=no")
	code, _, errs = gone.do(strings.Replace(login, "NOVA_REDIS_COORDINATOR_PASSWORD", "NOVA_REDIS_GONE", 1))
	said = append(said, errs)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "nothing was recorded")

	// a file that is not a login is refused as it is, never passed over
	require.NoError(t, os.WriteFile(file, []byte("{"), 0o600))
	code, _, errs = newLoginApp(t, dir).do("where")
	said = append(said, errs)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "is not a login")
	assert.Contains(t, errs, "run: nova-sprint seat logout")

	// logout removes the record: a bare verb is refused again
	code, out, errs = do("seat logout")
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "was=recorded")
	assert.NoFileExists(t, file)
	code, out, _ = do("seat logout")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "was=none")
	code, _, _ = newLoginApp(t, dir).do("where")
	assert.Equal(t, 2, code)

	// the password is in no output, no environment a child would inherit, and no file
	pw := secrets.NewSecret(loginPW)
	for _, s := range said {
		assert.False(t, secrets.Leaks(s, pw), "output leaks the password: %q", s)
	}
	assert.False(t, secrets.Leaks(strings.Join(os.Environ(), "\n"), pw), "the process environment holds the password")
	require.NoError(t, filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		require.NoError(t, err)
		assert.False(t, secrets.Leaks(string(b), pw), "%s holds the password", p)
		return nil
	}))
}

// seat login -h and seat logout -h are their help at exit 0, and record nothing.
func TestSeatLoginHelp(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	la := newLoginApp(t, dir)
	code, out, _ := la.do("seat login -h")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "nova-sprint seat login --store <secrets dir>")
	assert.Contains(t, out, "-secret")
	code, out, _ = la.do("seat logout --help")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "seat logout removes the recorded seat login")
	assert.NoDirExists(t, filepath.Join(dir, "config"))
}
