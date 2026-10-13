package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTheStoreIsFoundFromTheFleetRowWithNoFlag(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.env["XDG_CONFIG_HOME"] = t.TempDir()
	h.env[envSprintRedis] = "127.0.0.1:6399"
	h.redis.views[config.KindFleet] = map[string]config.View{config.KindFleet: {"pg_dsn": dsn}}
	d := h.deps()
	d.getenv = withLogin(d.getenv, nil)
	var opened string
	inner := d.openStore
	d.openStore = func(ctx context.Context, got string) (pgStore, error) { opened = got; return inner(ctx, got) }
	code, _, errs := runCapture([]string{"route", "list"}, d)
	require.Equal(t, 0, code, errs)
	assert.Equal(t, dsn, opened)
	code, out, errs := runCapture([]string{"status"}, d)
	require.Equal(t, 0, code, errs)
	assert.Contains(t, out, "pg-from=fleet-redis")
}

func TestAnExplicitStoreNeverDialsRedisToLook(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.env["XDG_CONFIG_HOME"] = t.TempDir()
	h.env[envPG] = dsn
	h.env[envSprintRedis] = "127.0.0.1:6399"
	d := h.deps()
	var opened string
	inner := d.openStore
	d.openStore = func(ctx context.Context, got string) (pgStore, error) { opened = got; return inner(ctx, got) }
	code, _, errs := runCapture([]string{"route", "list"}, d)
	require.Equal(t, 0, code, errs)
	assert.Equal(t, dsn, opened)
	assert.Zero(t, h.redis.opens)
}

func TestTheRefusalNamesEveryPlaceItLooked(t *testing.T) {
	t.Parallel()
	h := newHarness()
	home := t.TempDir()
	h.env["XDG_CONFIG_HOME"] = home
	d := h.deps()
	d.getenv = withLogin(d.getenv, nil)
	code, _, errs := runCapture([]string{"route", "list"}, d)
	require.Equal(t, 2, code)
	require.Contains(t, errs, "no store named")
	prior := -1
	for _, want := range []string{"--pg not given", "NOVA_PG_DSN unset", "--seat/NOVA_SEAT unset", "nova-config login " + filepath.Join(home, "nova-config", "login.json") + ": none", "no Redis named", "nova-sprint seat login " + filepath.Join(home, "nova-sprint", "login.json") + ": none"} {
		next := strings.Index(errs, want)
		assert.Greater(t, next, prior, errs)
		prior = next
	}
	assert.Equal(t, 1, strings.Count(errs, "\n"), errs)
	assert.Zero(t, h.opens)
	assert.Zero(t, h.redis.opens)
}

func TestAnEmptyFleetDSNIsNamed(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.env["XDG_CONFIG_HOME"] = t.TempDir()
	h.env[envSprintRedis] = "127.0.0.1:6399"
	code, _, errs := h.run(t, "route", "list")
	require.Equal(t, 2, code)
	assert.Contains(t, errs, "fleet:pg_dsn in Redis 127.0.0.1:6399 (from env): empty")
	assert.Zero(t, h.opens)
}

// Each row removes the preceding source so every boundary has a witness.
func TestStoreDiscoveryUsesTheFirstNamedSource(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, source string }{
		{"flag", "flag"}, {"environment", "env"}, {"seat flag", "seat"}, {"seat environment", "seat"}, {"login", "login"}, {"fleet", "fleet-redis"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness()
			home := t.TempDir()
			h.env["XDG_CONFIG_HOME"] = home
			h.env[envSprintRedis] = "127.0.0.1:6399"
			const flagDSN = "postgres://flag@127.0.0.1:5432/nova"
			const envDSN = "postgres://env@127.0.0.1:5432/nova"
			const seatDSN = "postgres://seat@127.0.0.1:5432/nova"
			const loginDSN = "postgres://login@127.0.0.1:5432/nova"
			h.redis.views[config.KindFleet] = map[string]config.View{config.KindFleet: {"pg_dsn": dsn}}
			require.NoError(t, os.MkdirAll(filepath.Join(home, "nova-config"), 0700))
			require.NoError(t, os.WriteFile(filepath.Join(home, "nova-config", "seats.tsv"), []byte("s1\t"+seatDSN+"\n"), 0600))
			login := filepath.Join(home, "nova-config", "login.json")
			if tc.source != "fleet-redis" {
				b, err := json.Marshal(loginRecord{DSN: loginDSN, Friend: "a1", Store: home, As: "s1", Key: filepath.Join(home, "key"), Secret: "PG_PASSWORD", Sops: filepath.Join(home, "sops")})
				require.NoError(t, err)
				if tc.source != "login" {
					b = []byte("{")
				}
				require.NoError(t, os.WriteFile(login, b, 0600))
			}
			d := h.deps()
			d.getenv = withLogin(d.getenv, func(secrets.Login) (secrets.Secret, error) { return secrets.NewSecret("test-password"), nil })
			t.Cleanup(func() { loginReaders.Delete(login) })
			want := dsn
			args := []string{"route", "list"}
			switch tc.source {
			case "flag":
				args = append(args, "--pg", flagDSN, "--seat", "missing")
				h.env[envPG] = envDSN
				want = flagDSN
			case "env":
				args = append(args, "--seat", "missing")
				h.env[envPG] = envDSN
				want = envDSN
			case "seat":
				if tc.name == "seat flag" {
					args = append(args, "--seat", "s1")
				} else {
					h.env["NOVA_SEAT"] = "s1"
				}
				want = seatDSN
			case "login":
				want = loginDSN
			}
			var opened string
			open := d.openStore
			d.openStore = func(ctx context.Context, got string) (pgStore, error) { opened = got; return open(ctx, got) }
			code, _, errs := runCapture(args, d)
			require.Equal(t, 0, code, errs)
			assert.Equal(t, config.Redact(want), config.Redact(opened))
			if tc.source == "fleet-redis" {
				assert.Equal(t, 1, h.redis.opens)
			} else {
				assert.Zero(t, h.redis.opens)
			}
			// Status reports the same source in its structured result.
			args[0], args[1] = "status", "--json"
			code, out, errs := runCapture(args, d)
			require.Equal(t, 0, code, errs)
			assert.Contains(t, out, `"pg-from":"`+tc.source+`"`)
		})
	}
}

func TestASelectedStoreFailureNeverFallsBackToFleet(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		args        []string
		pg, login   string
		openFailure bool
	}{
		{name: "invalid flag", args: []string{"--pg", "broken password='test-secret"}},
		{name: "invalid environment", pg: "broken password='test-secret"},
		{name: "missing seat", args: []string{"--seat", "missing"}},
		{name: "invalid login", login: "{"},
		{name: "unresolved login secret", login: `{"dsn":"postgres://login@127.0.0.1:5432/nova","friend":"a1","store":"/s","as":"s1","key":"/k","sops":"/sops","secret":"PG_PASSWORD"}`},
		{name: "failed store", pg: dsn, openFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness()
			home := t.TempDir()
			h.env["XDG_CONFIG_HOME"] = home
			h.env[envPG] = tc.pg
			h.env[envSprintRedis] = "127.0.0.1:6399"
			h.redis.views[config.KindFleet] = map[string]config.View{config.KindFleet: {"pg_dsn": dsn}}
			if tc.login != "" {
				require.NoError(t, os.MkdirAll(filepath.Join(home, "nova-config"), 0700))
				require.NoError(t, os.WriteFile(filepath.Join(home, "nova-config", "login.json"), []byte(tc.login), 0600))
			}
			d := h.deps()
			d.getenv = withLogin(d.getenv, func(secrets.Login) (secrets.Secret, error) { return secrets.Secret{}, errors.New("secret unavailable") })
			t.Cleanup(func() { loginReaders.Delete(filepath.Join(home, "nova-config", "login.json")) })
			if tc.openFailure {
				d.openStore = func(context.Context, string) (pgStore, error) { return nil, errors.New("store unavailable") }
			}
			args := append([]string{"route", "list"}, tc.args...)
			code, out, errs := runCapture(args, d)
			assert.Equal(t, 2, code)
			assert.Zero(t, h.redis.opens)
			assert.NotContains(t, out+errs, "test-secret")
		})
	}
}

func TestRedisDiscoveryFailureIsBoundedAndNeverQuotesItsError(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.env["XDG_CONFIG_HOME"] = t.TempDir()
	h.env[envSprintRedis] = "127.0.0.1:6399"
	d := h.deps()
	d.openRedis = func(ctx context.Context, addr string) (redisSide, error) {
		_, bounded := ctx.Deadline()
		assert.True(t, bounded)
		assert.Equal(t, h.env[envSprintRedis], addr)
		return nil, errors.New("postgres://u:diagnostic-secret@127.0.0.1:5432/nova")
	}
	code, out, errs := runCapture([]string{"route", "list"}, d)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "fleet:pg_dsn in Redis 127.0.0.1:6399 (from env): *errors.errorString")
	assert.NotContains(t, out+errs, "diagnostic-secret")
	assert.Zero(t, h.opens)
}

func TestFleetDiscoveryUsesThePasswordEnvironment(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, variable string
		empty          bool
	}{
		{name: "default", variable: "NOVA_PG_PASSWORD"},
		{name: "named", variable: "PG_PASSWORD"},
		{name: "named empty", variable: "PG_PASSWORD", empty: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness()
			h.env["XDG_CONFIG_HOME"] = t.TempDir()
			h.env[envSprintRedis] = "127.0.0.1:6399"
			h.redis.views[config.KindFleet] = map[string]config.View{config.KindFleet: {"pg_dsn": dsn}}
			if tc.variable != "NOVA_PG_PASSWORD" {
				h.env[envPGPassEnv] = tc.variable
			}
			if !tc.empty {
				h.env[tc.variable] = "discovery-password"
			}
			d := h.deps()
			var opened string
			open := d.openStore
			d.openStore = func(ctx context.Context, got string) (pgStore, error) { opened = got; return open(ctx, got) }
			code, out, errs := runCapture([]string{"route", "list"}, d)
			if tc.empty {
				assert.Equal(t, 2, code)
				assert.Contains(t, errs, "PG_PASSWORD is empty")
				assert.Empty(t, opened)
			} else {
				require.Equal(t, 0, code, errs)
				ok, fact := applied(opened, "nova_config", "127.0.0.1:5432", "discovery-password")
				assert.True(t, ok, fact)
			}
			assert.NotContains(t, out+errs, "discovery-password")
		})
	}
}

type failingDiscoveryRedis struct {
	*fakeRedis
	closed bool
}

func (r *failingDiscoveryRedis) Read(context.Context, string) (map[string]config.View, int64, error) {
	return nil, 0, errors.New("read failed with diagnostic-secret")
}
func (r *failingDiscoveryRedis) Close() error { r.closed = true; return nil }

func TestRedisDiscoveryReadFailureClosesAndRedacts(t *testing.T) {
	t.Parallel()
	h := newHarness()
	h.env["XDG_CONFIG_HOME"] = t.TempDir()
	h.env[envSprintRedis] = "postgres://u:address-secret@127.0.0.1:6399/nova"
	r := &failingDiscoveryRedis{fakeRedis: h.redis}
	d := h.deps()
	d.openRedis = func(context.Context, string) (redisSide, error) { return r, nil }
	code, out, errs := runCapture([]string{"route", "list"}, d)
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "fleet:pg_dsn in Redis (unparsed Redis address) (from env): *errors.errorString")
	assert.NotContains(t, out+errs, "diagnostic-secret")
	assert.NotContains(t, out+errs, "address-secret")
	assert.True(t, r.closed)
	assert.Zero(t, h.opens)
}
