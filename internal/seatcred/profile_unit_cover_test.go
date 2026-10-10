package seatcred

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The unit cover of the three functions the unit tier never reached:
// LoadConfigProfile, parseConfigProfileRow, and PathsFor. All tests use
// t.TempDir() for files and a custom getenv func, so no real sops or
// process environment is needed.

func TestSeatcredProfileCoverLoadConfigProfileMissingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "seats.tsv")
	_, err := LoadConfigProfile(path, "bench")
	require.ErrorIs(t, err, ErrNoProfileRow, "missing file: %v", err)
	require.Contains(t, err.Error(), path, "missing file should name the path: %v", err)
}

func TestSeatcredProfileCoverLoadConfigProfileWithCommentsAndRow(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "seats.tsv")
	body := "# fleet play\n\nbench\tpostgres://bench@host:5432/nova\tNOVA_BENCH_PASSWORD\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	p, err := LoadConfigProfile(path, "bench")
	require.NoError(t, err, "LoadConfigProfile = %v %v", p, err)
	require.Equal(t, "bench", p.Name)
	require.Equal(t, "postgres://bench@host:5432/nova", p.DSN)
	require.Equal(t, "NOVA_BENCH_PASSWORD", p.PasswordEnv)
}

func TestSeatcredProfileCoverLoadConfigProfileUnknownSeat(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "seats.tsv")
	body := "bench\tpostgres://bench@host:5432/nova\tNOVA_BENCH_PASSWORD\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	_, err := LoadConfigProfile(path, "ghost")
	require.Error(t, err, "unknown seat: %v", err)
	require.Contains(t, err.Error(), "unknown seat ghost")
	require.Contains(t, err.Error(), "known seats")
}

func TestSeatcredProfileCoverLoadConfigProfileDuplicateRow(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "seats.tsv")
	body := "bench\tpostgres://bench@host:5432/nova\nbench\tpostgres://bench@host:5433/nova\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	_, err := LoadConfigProfile(path, "bench")
	require.Error(t, err, "duplicate row: %v", err)
	require.Contains(t, err.Error(), "second row")
}

func TestSeatcredProfileCoverLoadConfigProfileMalformedRow(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "seats.tsv")
	body := "bench\tpostgres://bench@host:5432/nova\tNOVA_BENCH_PASSWORD\nbadrow\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	_, err := LoadConfigProfile(path, "bench")
	require.Error(t, err, "malformed row: %v", err)
	require.Contains(t, err.Error(), ":2")
}

func TestSeatcredProfileCoverParseConfigProfileRowTwoColumns(t *testing.T) {
	t.Parallel()

	p, err := parseConfigProfileRow("bench\tpostgres://bench@host:5432/nova")
	require.NoError(t, err, "parseConfigProfileRow = %v %v", p, err)
	require.Equal(t, "bench", p.Name)
	require.Equal(t, "postgres://bench@host:5432/nova", p.DSN)
	require.Empty(t, p.PasswordEnv)
}

func TestSeatcredProfileCoverParseConfigProfileRowThreeColumns(t *testing.T) {
	t.Parallel()

	p, err := parseConfigProfileRow("bench\tpostgres://bench@host:5432/nova\tNOVA_BENCH_PASSWORD")
	require.NoError(t, err, "parseConfigProfileRow = %v %v", p, err)
	require.Equal(t, "NOVA_BENCH_PASSWORD", p.PasswordEnv)
}

func TestSeatcredProfileCoverParseConfigProfileRowBadPasswordEnv(t *testing.T) {
	t.Parallel()

	_, err := parseConfigProfileRow("bench\tpostgres://bench@host:5432/nova\tbad password")
	require.Error(t, err, "bad password env: %v", err)
	require.Contains(t, err.Error(), "password env")
}

func TestSeatcredProfileCoverParseConfigProfileRowBadSeatName(t *testing.T) {
	t.Parallel()

	_, err := parseConfigProfileRow("bench/name\tpostgres://bench@host:5432/nova")
	require.Error(t, err, "bad seat name: %v", err)
	require.Contains(t, err.Error(), "name")
}

func TestSeatcredProfileCoverParseConfigProfileRowEmptyDSN(t *testing.T) {
	t.Parallel()

	_, err := parseConfigProfileRow("bench\t\tNOVA_BENCH_PASSWORD")
	require.Error(t, err, "empty DSN: %v", err)
	require.Contains(t, err.Error(), "dsn is empty")
}

func TestSeatcredProfileCoverPathsForWithOverrides(t *testing.T) {
	t.Parallel()

	getenv := func(k string) string {
		switch k {
		case StoreEnv, KeyEnv:
			return "/overrides/" + k
		case SopsEnv:
			return "/overrides/sops"
		default:
			return ""
		}
	}
	p, err := PathsFor("bench", getenv)
	require.NoError(t, err, "PathsFor = %v %v", p, err)
	require.Equal(t, "/overrides/NOVA_SECRETS_STORE", p.Store)
	require.Equal(t, "/overrides/NOVA_SECRETS_KEY", p.Key)
	require.Equal(t, "/overrides/sops", p.Sops)
}

func TestSeatcredProfileCoverPathsForWithHomeOnly(t *testing.T) {
	t.Parallel()

	getenv := func(k string) string {
		if k == "HOME" {
			return "/home"
		}
		return ""
	}
	p, err := PathsFor("bench", getenv)
	require.NoError(t, err, "PathsFor = %v %v", p, err)
	require.Equal(t, "/home/nova-bench/secrets", p.Store)
	require.Equal(t, "/home/.config/nova-secrets/bench.key", p.Key)
}

func TestSeatcredProfileCoverPathsForWithHomeAndSopsOverride(t *testing.T) {
	t.Parallel()

	getenv := func(k string) string {
		switch k {
		case "HOME":
			return "/home"
		case SopsEnv:
			return "/home/bin/sops"
		default:
			return ""
		}
	}
	p, err := PathsFor("bench", getenv)
	require.NoError(t, err, "PathsFor = %v %v", p, err)
	require.Equal(t, "/home/bin/sops", p.Sops)
}

func TestSeatcredProfileCoverPathsForWithHomeAndSopsInPath(t *testing.T) {
	t.Parallel()

	getenv := func(k string) string {
		if k == "HOME" {
			return "/home"
		}
		return ""
	}
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "/bin")
	defer os.Setenv("PATH", origPath)
	_, err := PathsFor("bench", getenv)
	require.Error(t, err, "no sops on PATH: %v", err)
	require.Contains(t, err.Error(), "no sops on PATH")
}

func TestSeatcredProfileCoverPathsForNoHomeAndNoOverrides(t *testing.T) {
	t.Parallel()

	getenv := func(k string) string { return "" }
	_, err := PathsFor("bench", getenv)
	require.Error(t, err, "no HOME: %v", err)
	require.Contains(t, err.Error(), "HOME is unset")
}
