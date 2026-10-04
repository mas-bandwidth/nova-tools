package config

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// The environment that names the config store: the DSN, the variable that
// holds its password, and the variable read when that one is not named.
// Every tool that reads nova-config (nova-config itself, nova-sprint fleet
// sync) resolves the store's address with ResolveDSN, so there is one set of
// address rules (docs/nova-config/README.md, "Connecting").
const (
	EnvPG          = "NOVA_PG_DSN"
	EnvPGPassEnv   = "NOVA_PG_PASSWORD_ENV"
	DefaultPassEnv = "NOVA_PG_PASSWORD"
)

// ResolveDSN is the Postgres DSN a tool dials: the flag, else NOVA_PG_DSN; a
// flag that carries a password is refused (it would be on the command line,
// where a ps reads it); a DSN without one takes it from the variable
// NOVA_PG_PASSWORD_ENV names, NOVA_PG_PASSWORD when unset, and a named
// variable that is empty is refused with its name (the shape
// internal/nsprint/redisauth keeps for Redis).
func ResolveDSN(flagValue string, getenv func(string) string) (string, error) {
	dsn := flagValue
	if dsn == "" {
		dsn = getenv(EnvPG)
	}
	if dsn == "" {
		return "", fmt.Errorf("--pg is required: postgres://user@host:5432/nova (or %s)", EnvPG)
	}
	cfg, err := pgconn.ParseConfig(dsn)
	if err != nil {
		return "", fmt.Errorf("--pg: %v; want postgres://user@host:5432/nova", err)
	}
	if flagValue != "" && strings.Contains(flagValue, "://") {
		if u, err := url.Parse(flagValue); err == nil {
			if _, has := u.User.Password(); has {
				return "", fmt.Errorf("--pg carries a password; leave it out and export it as the variable %s names (a ps reads the line)", EnvPGPassEnv)
			}
		}
	}
	if cfg.Password != "" {
		return dsn, nil
	}
	name := getenv(EnvPGPassEnv)
	named := name != ""
	if !named {
		name = DefaultPassEnv
	}
	pw := getenv(name)
	if pw == "" {
		if named {
			return "", fmt.Errorf("%s=%s but %s is empty; run under nova-secrets exec --only %s", EnvPGPassEnv, name, name, name)
		}
		return dsn, nil
	}
	return withPassword(dsn, cfg.User, pw)
}

// withPassword puts the password into the DSN in memory, in whichever of
// the two spellings it is written.
func withPassword(dsn, user, pw string) (string, error) {
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err != nil {
			return "", fmt.Errorf("--pg: %v", err)
		}
		if user == "" {
			user = u.User.Username()
		}
		u.User = url.UserPassword(user, pw)
		return u.String(), nil
	}
	escaped := strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(pw)
	return dsn + " password='" + escaped + "'", nil
}
