package ci

// env.go scrubs the environment a check runs in (2026-09-25 Studio, first
// real `nova-sprint ci run` pass). The runner is a bench seat: it runs under
// `nova-secrets exec` with the fleet Redis user and password in its
// environment so its own verbs authenticate (internal/nsprint/store). A check
// is the repo's own test suite, and every test that starts a throwaway
// redis-server opens it through the same store, so an inherited seat user
// turned every one of them into WRONGPASS. A check therefore runs with the
// seat's Redis and secrets variables removed; everything else (PATH, HOME,
// the Go caches and flags, TMPDIR, LANG, USER, ...) passes through.

import (
	"os"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// scrubPrefixes are the variable families a check never inherits.
var scrubPrefixes = []string{"NOVA_SPRINT_", "NOVA_REDIS_", "NOVA_SECRETS_"}

// scrubNames are single variables a check never inherits.
var scrubNames = map[string]bool{"REDISCLI_AUTH": true, "NOVA_SEAT": true}

// CheckEnv returns environ without the seat's Redis and secrets variables:
// every NOVA_SPRINT_*, NOVA_REDIS_* and NOVA_SECRETS_* variable, REDISCLI_AUTH, NOVA_SEAT,
// and the variable named by the value of NOVA_SPRINT_REDIS_PASSWORD_ENV
// (whatever it is called). dropped is the sorted, de-duplicated list of the
// names removed; values are never returned.
func CheckEnv(environ []string) (env, dropped []string) {
	indirect := ""
	for _, kv := range environ {
		if name, value, ok := strings.Cut(kv, "="); ok && name == store.PasswordEnvEnv {
			indirect = value
		}
	}
	seen := map[string]bool{}
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if scrubbed(name, indirect) {
			if !seen[name] {
				seen[name] = true
				dropped = append(dropped, name)
			}
			continue
		}
		env = append(env, kv)
	}
	sort.Strings(dropped)
	return env, dropped
}

func scrubbed(name, indirect string) bool {
	if scrubNames[name] || (indirect != "" && name == indirect) {
		return true
	}
	for _, p := range scrubPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// checkEnviron is the scrubbed process environment plus the variables every
// check gets, and the dropped names for the run's ENV line.
func checkEnviron() (env, dropped []string) {
	env, dropped = CheckEnv(os.Environ())
	return append(env, "GIT_TERMINAL_PROMPT=0", "CI=1"), dropped
}
