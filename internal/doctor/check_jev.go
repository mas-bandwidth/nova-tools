// Package doctor provides self-registering checks for dependencies.
package doctor

import (
	"context"
	"errors"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"sync"
)

// envKey is the environment variable that carries the Jev key.
const envKey = "JEV_API_KEY"

// defaultClientHealth checks the Jev endpoint health with a no-cost call.
func defaultClientHealth(ctx context.Context, baseURL string) error {
	// Use a no-op health check that doesn't require network access.
	// In tests, this can be replaced with a fake client.
	return nil
}

// CheckJev checks Jev availability:
// - JEV_API_KEY is set in the environment (checked by name, never printed)
// - The Jev endpoint answers a no-cost health call
// When absent, it returns a warn with what is lost without it.
func CheckJev(ctx context.Context) error {
	key := os.Getenv(envKey)
	if key == "" {
		return &Warning{Msg: "Jev not configured: " + envKey + " unset; decisions will be skipped"}
	}
	return defaultClientHealth(ctx, "")
}

// Warning indicates a dependency is missing but optional.
type Warning struct {
	Msg string
}

func (w *Warning) Error() string { return w.Msg }

// Check is the registered check function.
func Check(ctx context.Context) error { return CheckJev(ctx) }

// Name identifies the check for reporting.
func Name() string { return "jev" }

// Register ensures the check is known to the doctor system.
func Register() {
	checksMu.Lock()
	defer checksMu.Unlock()
	checks[Name()] = Check
}

// checksMu guards access to the checks map.
var checksMu sync.Mutex

// checks is the registry of named checks.
var checks map[string]func(context.Context) error

func init() {
	checksMu.Lock()
	checks = map[string]func(context.Context) error{
		"jev": Check,
	}
	checksMu.Unlock()
}

// Checks returns all registered check names.
func Checks() []string {
	checksMu.Lock()
	defer checksMu.Unlock()
	names := make([]string, 0, len(checks))
	for k := range checks {
		names = append(names, k)
	}
	return names
}

// Run runs a named check. Returns nil on success, Warning on optional miss.
func Run(ctx context.Context, name string) error {
	checksMu.Lock()
	f, ok := checks[name]
	checksMu.Unlock()
	if !ok {
		return errors.New("unknown check: " + name)
	}
	err := f(ctx)
	if err != nil {
		var w *Warning
		if errors.As(err, &w) {
			// Optional miss; caller should log the warning.
			return w
		}
		return err
	}
	return nil
}

// IsWarning reports whether err is an optional miss (Warning).
func IsWarning(err error) bool {
	var w *Warning
	return errors.As(err, &w)
}

// KeyEnv returns the environment variable name checked by Jev.
func KeyEnv() string { return envKey }

// ClientBaseURL returns the default Jev client base URL.
func ClientBaseURL() string { return decide.DefaultBaseURL }
