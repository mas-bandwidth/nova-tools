// Package redisauth is the one fleet Redis seat every tool dials with from
// the environment (#3461): the ACL user and the variable holding its password.
// It is a leaf (os and fmt only) so a tool that must import no network or
// subprocess code, nova-tokens under its boundary tests, reads its login here
// rather than through internal/nsprint/store, which reaches net and, through
// internal/seatcred, os/exec.
package redisauth

import (
	"fmt"
	"os"
)

// UserEnv names the ACL user and turns authentication on; PasswordEnvEnv names
// the variable holding that user's password, DefaultPasswordEnv when unset.
const (
	UserEnv            = "NOVA_SPRINT_REDIS_USER"
	PasswordEnvEnv     = "NOVA_SPRINT_REDIS_PASSWORD_ENV"
	DefaultPasswordEnv = "NOVA_REDIS_BENCH_PASSWORD"
)

// Auth is the one fleet Redis seat every tool dials with (nova-sprint, nova-tokens
// ledger/report through --user and --password-env, #3461). user is the ACL user, else
// UserEnv; passwordEnv names the variable holding its password, else PasswordEnvEnv, else
// DefaultPasswordEnv. With no user the connection is the default user's: a password is read
// only when passwordEnv names its variable, so a throwaway test Redis needs nothing. A user
// whose password variable is empty is refused with the remedy, never dialed as default.
func Auth(user, passwordEnv string) (string, string, error) {
	named := "--user " + user
	if user == "" {
		user = os.Getenv(UserEnv)
		named = UserEnv + "=" + user
	}
	if user == "" {
		if passwordEnv == "" {
			return "", "", nil
		}
		return "", os.Getenv(passwordEnv), nil
	}
	if passwordEnv == "" {
		passwordEnv = os.Getenv(PasswordEnvEnv)
	}
	if passwordEnv == "" {
		passwordEnv = DefaultPasswordEnv
	}
	password := os.Getenv(passwordEnv)
	if password == "" {
		return "", "", fmt.Errorf("%s but %s is empty; run under nova-secrets exec --only %s", named, passwordEnv, passwordEnv)
	}
	return user, password, nil
}

// NoUserHint is the refusal the fleet Redis needs when the default user is off
// (#3520): the password is already in the environment but the ACL user is
// unset, so the verb connected as the default user and was refused NOAUTH. The
// line names the missing variable and the pair (user + password) in one line.
func NoUserHint() string {
	return UserEnv + " is unset but " + DefaultPasswordEnv + " is set; set the pair " +
		UserEnv + "=bench and " + DefaultPasswordEnv + " (password from nova-secrets exec --only " +
		DefaultPasswordEnv + ", never a flag)"
}
