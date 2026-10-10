package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/seatcred"
	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
)

// coordinator-config-through-the-seat: seat install writes the nova-config seat
// profile (seats.tsv: name, dsn, password env) and the sprint's server beside the
// sprint login, and seat check reads both: a cold coordinator's seat check names the
// server from the seat, never "not set", and the config seat its nova-config --seat
// reads.
func TestSeatInstallWritesTheConfigSeatProfile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	env := map[string]string{"NOVA_SPRINT_REDIS": "127.0.0.1:6381", "NOVA_SPRINT_ACTOR": "rowan", "XDG_CONFIG_HOME": cfg}
	a := newApp(func(k string) string { return env[k] })
	a.seatLoginOn()
	a.goos = "linux"
	a.home = func() (string, error) { return dir, nil }
	a.executable = func() (string, error) { return "/opt/nova/bin/nova-sprint", nil }
	a.seatLoad = func(goos, op, path string) error { return nil }
	a.loginSecret = func(l secrets.Login) (secrets.Secret, error) { return secrets.NewSecret("pw"), nil }
	var askedAt []string
	a.forward = func(_ context.Context, addr string, verbs ...[]string) ([]sprintwire.Result, error) {
		askedAt = append(askedAt, addr)
		return []sprintwire.Result{{}}, nil
	}
	do := func(args ...string) (int, string, string) {
		var out, errb bytes.Buffer
		code := a.run(args, &out, &errb)
		return code, out.String(), errb.String()
	}
	units := filepath.Join(dir, "units")
	profile := filepath.Join(cfg, "nova-config", seatcred.ProfileFile)
	record := filepath.Join(cfg, "nova-sprint", "seat.json")
	const pgDSN = "postgres://nova_config@127.0.0.1:5432/nova"
	install := []string{"seat", "install", "--dir", units, "--server", "127.0.0.1:7399", "--harness", "opencode", "--target", t.TempDir(),
		"--config-seat", "studio", "--config-dsn", pgDSN, "--config-password-env", "NOVA_PG_CONFIG_PASSWORD"}

	// another seat's row and a stale row of this seat are already there
	require.NoError(t, os.MkdirAll(filepath.Dir(profile), 0o700))
	require.NoError(t, os.WriteFile(profile, []byte("# the fleet play's rows\nbench\t"+pgDSN+"\tNOVA_PG_BENCH_PASSWORD\nstudio\tpostgres://old@10.0.0.9:5432/nova\tNOVA_PG_OLD\n"), 0o600))

	// a dry run says what it would write, and writes nothing
	code, out, errs := do(append(install, "--dry-run")...)
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "SEAT CONFIG DRY-RUN seat=studio dsn="+pgDSN+" password-env=NOVA_PG_CONFIG_PASSWORD profile="+profile)
	assert.NoFileExists(t, record, "a dry run records nothing")
	old, err := seatcred.LoadConfigProfile(profile, "studio")
	require.NoError(t, err)
	assert.Equal(t, "NOVA_PG_OLD", old.PasswordEnv, "a dry run leaves the profile as it was")

	// the install writes the row in place of the stale one, keeps the others, and records the server
	code, out, errs = do(install...)
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "SEAT INSTALL OK unit=")
	assert.Contains(t, out, "SEAT CONFIG OK seat=studio dsn="+pgDSN+" password-env=NOVA_PG_CONFIG_PASSWORD profile="+profile+" server=127.0.0.1:7399")
	unit, err := os.ReadFile(filepath.Join(units, "nova-sprint-seat-push.service"))
	require.NoError(t, err)
	assert.Contains(t, string(unit), `Environment="NOVA_SPRINT_SERVER=127.0.0.1:7399"`, "--server is the unit's server too")
	assert.Equal(t, []string{"127.0.0.1:7399"}, askedAt, "the push target goes to --server")
	p, err := seatcred.LoadConfigProfile(profile, "studio")
	require.NoError(t, err, "nova-config --seat reads the row seat install wrote")
	assert.Equal(t, seatcred.ConfigProfile{Name: "studio", DSN: pgDSN, PasswordEnv: "NOVA_PG_CONFIG_PASSWORD"}, p)
	bench, err := seatcred.LoadConfigProfile(profile, "bench")
	require.NoError(t, err)
	assert.Equal(t, "NOVA_PG_BENCH_PASSWORD", bench.PasswordEnv, "another seat's row is kept")
	b, err := os.ReadFile(profile)
	require.NoError(t, err)
	assert.Contains(t, string(b), "# the fleet play's rows\n", "a comment is kept")
	assert.Equal(t, 1, strings.Count(string(b), "studio\t"), "one row for the seat")
	info, err := os.Stat(profile)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	assert.FileExists(t, record)

	// again: the same row, still one
	code, _, errs = do(install...)
	require.Equal(t, 0, code, errs)
	b, err = os.ReadFile(profile)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(b), "studio\t"))

	// refusals write nothing: a dsn carrying a password, a half-named seat
	before := string(b)
	for _, bad := range [][]string{
		{"--config-seat", "studio", "--config-dsn", "postgres://nova_config:hunter2@127.0.0.1:5432/nova", "--config-password-env", "NOVA_PG_CONFIG_PASSWORD"},
		{"--config-seat", "studio", "--config-password-env", "NOVA_PG_CONFIG_PASSWORD"},
		{"--config-dsn", pgDSN},
		{"--config-seat", "studio", "--config-dsn", pgDSN, "--config-password-env", "lower-case"},
	} {
		code, _, errs = do(append([]string{"seat", "install", "--dir", units}, bad...)...)
		assert.Equal(t, 2, code, "%v: %s", bad, errs)
		assert.NotContains(t, errs, "hunter2", "a refusal never quotes a password")
		b, err = os.ReadFile(profile)
		require.NoError(t, err)
		assert.Equal(t, before, string(b), "%v: a refusal writes nothing", bad)
	}

	// a cold coordinator's seat check: no NOVA_SPRINT_SERVER in its environment
	ta := newTestApp(t)
	ta.ok("init --readers reader-a --members m1")
	ta.ok("start")
	ta.ok("tick")
	cold := map[string]string{"NOVA_SPRINT_REDIS": "mem:0", "NOVA_SPRINT_ACTOR": "coordinator", "XDG_CONFIG_HOME": cfg}
	ta.a.getenv = func(k string) string { return cold[k] }
	ta.a.seatLoginOn()
	o := mockHealthyOutside()
	envServer := "" // NOVA_SPRINT_SERVER as the check reads it; set in the environment, the verb would go to that server
	o.serverAddr = func() (string, bool) { return envServer, false }
	var asked []string
	o.roundTrip = func(_ context.Context, addr string) (time.Duration, error) {
		asked = append(asked, addr)
		return 5 * time.Millisecond, nil
	}
	ta.a.outside = o
	code, out, errs = ta.do("seat check")
	assert.Equal(t, 0, code, out+errs)
	assert.Contains(t, out, "MACHINERY server OK addr=127.0.0.1:7399")
	assert.NotContains(t, out, ServerEnv+" is not set")
	assert.Equal(t, []string{"127.0.0.1:7399"}, asked, "the seat's server is the one measured")
	assert.Contains(t, out, "MACHINERY config OK seat=studio dsn="+pgDSN+" password-env=NOVA_PG_CONFIG_PASSWORD profile="+profile)

	// the environment's server wins over the seat's
	envServer = "127.0.0.1:7500"
	_, out, _ = ta.do("seat check")
	assert.Contains(t, out, "MACHINERY server OK addr=127.0.0.1:7500")
	envServer = ""

	// the seat's row gone from the profile: the check is DOWN and names the remedy
	require.NoError(t, os.WriteFile(profile, []byte("bench\t"+pgDSN+"\tNOVA_PG_BENCH_PASSWORD\n"), 0o600))
	code, out, _ = ta.do("seat check")
	assert.Equal(t, 1, code, out)
	assert.Contains(t, out, "MACHINERY config DOWN seat=studio")
	assert.Contains(t, out, `remedy="nova-sprint seat install --config-seat studio --config-dsn <dsn> --config-password-env <NAME>"`)
}
