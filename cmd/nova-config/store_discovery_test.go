package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheStoreIsFoundFromTheFleetRowWithNoFlag: getenv names only
// NOVA_SPRINT_REDIS; the fake Redis's fleet view has
// pg_dsn=postgres://nova_config@127.0.0.1:5432/nova. `route list` opens that
// DSN (the fake openStore records it) and exits 0; `status` prints
// pg-from=fleet-redis. Red at BASE (the --pg refusal).
func TestTheStoreIsFoundFromTheFleetRowWithNoFlag(t *testing.T) {
	t.Parallel()

	h := newHarness()
	const fleetDSN = "postgres://nova_config@127.0.0.1:5432/nova"
	h.env = map[string]string{
		"NOVA_SPRINT_REDIS": "127.0.0.1:6379",
	}
	h.redis.views[config.KindFleet] = map[string]config.View{
		config.KindFleet: {
			"pg_dsn": fleetDSN,
		},
	}

	var recordedDSN string
	d := h.deps()
	originalOpen := d.openStore
	d.openStore = func(ctx context.Context, dsn string) (pgStore, error) {
		recordedDSN = dsn
		return originalOpen(ctx, dsn)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "list"}, &stdout, &stderr, d)
	require.Equal(t, 0, code, "stderr: %s", stderr.String())
	assert.Equal(t, fleetDSN, recordedDSN)

	stdout.Reset()
	stderr.Reset()
	code = run([]string{"status"}, &stdout, &stderr, d)
	require.Equal(t, 0, code, "status stderr: %s", stderr.String())
	assert.Contains(t, stdout.String(), "pg-from=fleet-redis")
}

// TestAnExplicitStoreNeverDialsRedisToLook: NOVA_PG_DSN set and
// NOVA_SPRINT_REDIS set: route list opens NOVA_PG_DSN, the fake openRedis is
// called zero times. Green before and after.
func TestAnExplicitStoreNeverDialsRedisToLook(t *testing.T) {
	t.Parallel()

	h := newHarness()
	const explicitDSN = "postgres://explicit_user@127.0.0.1:5432/explicit_db"
	h.env = map[string]string{
		"NOVA_PG_DSN":       explicitDSN,
		"NOVA_SPRINT_REDIS": "127.0.0.1:6379",
	}

	var recordedDSN string
	d := h.deps()
	originalOpen := d.openStore
	d.openStore = func(ctx context.Context, dsn string) (pgStore, error) {
		recordedDSN = dsn
		return originalOpen(ctx, dsn)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "list"}, &stdout, &stderr, d)
	require.Equal(t, 0, code, "stderr: %s", stderr.String())
	assert.Equal(t, explicitDSN, recordedDSN)
	assert.Equal(t, 0, h.redis.opens, "the fake openRedis is called zero times")
}

// TestTheRefusalNamesEveryPlaceItLooked: nothing set, no login files
// (t.TempDir() as the config home): exit 2 and the one line names --pg,
// NOVA_PG_DSN, --seat/NOVA_SEAT, the nova-config login path, and "no Redis
// named" with the nova-sprint login path, in that order. Red at BASE.
func TestTheRefusalNamesEveryPlaceItLooked(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	h := newHarness()
	h.env = map[string]string{
		"XDG_CONFIG_HOME": tempDir,
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "list"}, &stdout, &stderr, h.deps())
	require.Equal(t, 2, code)
	errs := stderr.String()
	require.Equal(t, 1, strings.Count(errs, "\n"), "refusal is one line: %q", errs)

	cfgLogin := filepath.Join(tempDir, "nova-config", "login.json")
	sprintLogin := filepath.Join(tempDir, "nova-sprint", "login.json")

	pgIdx := strings.Index(errs, "--pg not given")
	envIdx := strings.Index(errs, "NOVA_PG_DSN unset")
	seatIdx := strings.Index(errs, "--seat/NOVA_SEAT unset")
	loginIdx := strings.Index(errs, "nova-config login "+cfgLogin+": none")
	redisIdx := strings.Index(errs, "no Redis named (NOVA_SPRINT_REDIS, NOVA_REDIS_ADDR, seat, nova-sprint seat login "+sprintLogin+": none)")

	require.Greater(t, pgIdx, -1, "must name --pg not given: %s", errs)
	require.Greater(t, envIdx, pgIdx, "NOVA_PG_DSN must follow --pg: %s", errs)
	require.Greater(t, seatIdx, envIdx, "--seat/NOVA_SEAT must follow NOVA_PG_DSN: %s", errs)
	require.Greater(t, loginIdx, seatIdx, "nova-config login must follow --seat: %s", errs)
	require.Greater(t, redisIdx, loginIdx, "no Redis named must follow nova-config login: %s", errs)
}

// TestAnEmptyFleetDSNIsNamed: Redis named, fleet pg_dsn empty: the refusal says
// "fleet:pg_dsn in Redis <addr> (from env): empty". Red at BASE.
func TestAnEmptyFleetDSNIsNamed(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.env = map[string]string{
		"NOVA_SPRINT_REDIS": "127.0.0.1:6379",
	}
	h.redis.views[config.KindFleet] = map[string]config.View{
		config.KindFleet: {
			"pg_dsn": "",
		},
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"route", "list"}, &stdout, &stderr, h.deps())
	require.Equal(t, 2, code)
	errs := stderr.String()
	require.Contains(t, errs, "fleet:pg_dsn in Redis 127.0.0.1:6379 (from env): empty")
}
